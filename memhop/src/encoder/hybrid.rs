//! Hybrid encoder — fuses NgramEncoder (primary) with an optional semantic
//! encoder (secondary, e.g. BGE-M3 ONNX) into a single dense vector.
//!
//! Design constraints (v0.7.1):
//! - Hopfield recall consumes one dense `Vec<f16>` per query/pattern. We
//!   therefore fuse the two encoders' outputs into a single vector rather
//!   than running two parallel recall paths.
//! - SparseIndex keys must stay in the FNV-1a ngram-hash space (used for
//!   seed lookup), so the sparse half always comes from the primary
//!   NgramEncoder regardless of secondary availability.
//! - When `secondary == None`, HybridEncoder is byte-equivalent to the
//!   wrapped NgramEncoder — guaranteeing zero behavioural drift when the
//!   `onnx` feature is disabled.
//! - Both encoders MUST report the same `dim()` (VECTOR_DIM). Construction
//!   panics on mismatch — this is a programmer error, not a runtime fault.
//!
//! Fusion math (per dimension `j`):
//!     v_j = ngram_weight * normalize(ngram_dense)_j
//!         + semantic_weight * normalize(semantic_dense)_j
//!     output = L2_normalize(v)
//!
//! Default weights mirror the LongMemEval analysis: ngram=0.3, semantic=0.7.

use half::f16;

use crate::encoder::{Encoder, EncoderOutput, NgramEncoder};

/// Default fusion weights tuned for LongMemEval (ngram weak on
/// preference/temporal queries, semantic strong).
pub const DEFAULT_NGRAM_WEIGHT: f32 = 0.3;
pub const DEFAULT_SEMANTIC_WEIGHT: f32 = 0.7;

/// Hybrid encoder — primary ngram + optional secondary semantic encoder.
///
/// `secondary` is `Option<Box<dyn Encoder>>` so callers can compose any
/// implementation that satisfies the `Encoder` trait (BGE-M3 ONNX,
/// remote LLM stub, etc.). When `None`, this encoder behaves identically
/// to the wrapped `NgramEncoder`.
pub struct HybridEncoder {
    primary: NgramEncoder,
    secondary: Option<Box<dyn Encoder>>,
    ngram_weight: f32,
    semantic_weight: f32,
    dim: usize,
}

impl HybridEncoder {
    /// Construct a pure-ngram hybrid (no secondary). Equivalent in output
    /// to `NgramEncoder::new(dim)` but exposes the hybrid surface so
    /// callers can later swap in a secondary without API churn.
    pub fn new(primary: NgramEncoder) -> Self {
        let dim = primary.dim();
        HybridEncoder {
            primary,
            secondary: None,
            ngram_weight: DEFAULT_NGRAM_WEIGHT,
            semantic_weight: DEFAULT_SEMANTIC_WEIGHT,
            dim,
        }
    }

    /// Construct a hybrid with both encoders active and default weights.
    ///
    /// # Panics
    /// If `primary.dim() != secondary.dim()`.
    pub fn with_secondary(primary: NgramEncoder, secondary: Box<dyn Encoder>) -> Self {
        assert_eq!(
            primary.dim(),
            secondary.dim(),
            "hybrid encoder requires matching dim: primary={}, secondary={}",
            primary.dim(),
            secondary.dim()
        );
        let dim = primary.dim();
        HybridEncoder {
            primary,
            secondary: Some(secondary),
            ngram_weight: DEFAULT_NGRAM_WEIGHT,
            semantic_weight: DEFAULT_SEMANTIC_WEIGHT,
            dim,
        }
    }

    /// Override fusion weights. No normalization is performed — the final
    /// L2 step on the fused vector subsumes any rescaling.
    pub fn with_weights(mut self, ngram_weight: f32, semantic_weight: f32) -> Self {
        self.ngram_weight = ngram_weight;
        self.semantic_weight = semantic_weight;
        self
    }

    /// Whether a secondary encoder is currently attached.
    pub fn has_secondary(&self) -> bool {
        self.secondary.is_some()
    }

    /// Combined dense + sparse encoding — drop-in replacement for
    /// `NgramEncoder::encode_full`.
    ///
    /// Sparse is always the primary's output (preserves SparseIndex key
    /// space). Dense is the fused vector when a secondary is present,
    /// otherwise the primary's dense output verbatim.
    pub fn encode_full(&self, text: &str) -> EncoderOutput {
        let primary_out = self.primary.encode_full(text);

        let secondary = match &self.secondary {
            Some(s) => s,
            None => return primary_out,
        };

        let dense = fuse_dense(
            &primary_out.dense,
            &secondary.encode(text),
            self.ngram_weight,
            self.semantic_weight,
        );

        EncoderOutput {
            dense,
            sparse: primary_out.sparse,
        }
    }
}

impl Encoder for HybridEncoder {
    fn encode(&self, text: &str) -> Vec<f16> {
        self.encode_full(text).dense
    }

    fn sparse(&self, text: &str) -> Vec<(u64, f32)> {
        // Sparse must stay in the ngram hash space used by SparseIndex.
        self.primary.sparse(text)
    }

    fn dim(&self) -> usize {
        self.dim
    }
}

/// Fuse two dense f16 vectors:
///     1. Re-normalize each input to L2=1 (defensive — encoders should
///        already deliver normalized output, but a stale/zero vector
///        would otherwise dominate the linear combination).
///     2. Weighted sum in f32.
///     3. Final L2 normalize → f16.
///
/// Empty/zero inputs degrade gracefully: if a side has zero norm, its
/// contribution is dropped (the other side is used alone, then re-normed).
fn fuse_dense(a: &[f16], b: &[f16], wa: f32, wb: f32) -> Vec<f16> {
    debug_assert_eq!(a.len(), b.len(), "dense fusion requires matching dim");
    let dim = a.len();

    let mut out = vec![0.0f32; dim];

    let na = l2_norm_f16_local(a);
    let nb = l2_norm_f16_local(b);

    if na > 1e-8 {
        for (i, v) in a.iter().enumerate() {
            out[i] += wa * (v.to_f32() / na);
        }
    }
    if nb > 1e-8 {
        for (i, v) in b.iter().enumerate() {
            out[i] += wb * (v.to_f32() / nb);
        }
    }

    let norm: f32 = out.iter().map(|x| x * x).sum::<f32>().sqrt();
    if norm > 1e-8 {
        for v in out.iter_mut() {
            *v /= norm;
        }
    }

    out.iter().map(|&v| f16::from_f32(v)).collect()
}

/// Minimal L2 norm helper to avoid pulling hopfield internals into the
/// encoder layer. Keeps fusion math self-contained.
fn l2_norm_f16_local(v: &[f16]) -> f32 {
    let mut s = 0.0f32;
    for x in v {
        let f = x.to_f32();
        s += f * f;
    }
    s.sqrt()
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::types::VECTOR_DIM;

    fn cosine(a: &[f16], b: &[f16]) -> f32 {
        let mut dot = 0.0f32;
        let mut na = 0.0f32;
        let mut nb = 0.0f32;
        for (x, y) in a.iter().zip(b.iter()) {
            let xf = x.to_f32();
            let yf = y.to_f32();
            dot += xf * yf;
            na += xf * xf;
            nb += yf * yf;
        }
        if na < 1e-8 || nb < 1e-8 {
            return 0.0;
        }
        dot / (na.sqrt() * nb.sqrt())
    }

    /// Stub semantic encoder: deterministic dense output derived from a
    /// per-text seed. Used to verify fusion math without pulling ONNX.
    struct StubSemanticEncoder {
        dim: usize,
    }

    impl Encoder for StubSemanticEncoder {
        fn encode(&self, text: &str) -> Vec<f16> {
            // Map text bytes → simple deterministic dense via a rolling
            // hash. Not a real semantic encoder, but stable + L2≈1.
            let mut v = vec![0.0f32; self.dim];
            let mut h: u64 = 1469598103934665603;
            for b in text.bytes() {
                h ^= b as u64;
                h = h.wrapping_mul(1099511628211);
                let idx = (h % self.dim as u64) as usize;
                v[idx] += 1.0;
            }
            let n: f32 = v.iter().map(|x| x * x).sum::<f32>().sqrt();
            if n > 1e-8 {
                for x in v.iter_mut() {
                    *x /= n;
                }
            }
            v.iter().map(|&x| f16::from_f32(x)).collect()
        }

        fn sparse(&self, _text: &str) -> Vec<(u64, f32)> {
            Vec::new()
        }

        fn dim(&self) -> usize {
            self.dim
        }
    }

    #[test]
    fn no_secondary_matches_ngram_exactly() {
        let ng = NgramEncoder::new(VECTOR_DIM);
        let hy = HybridEncoder::new(NgramEncoder::new(VECTOR_DIM));

        let ng_out = ng.encode_full("hello world");
        let hy_out = hy.encode_full("hello world");

        assert_eq!(ng_out.dense, hy_out.dense);
        assert_eq!(ng_out.sparse, hy_out.sparse);
    }

    #[test]
    fn no_secondary_trait_surface_matches_ngram() {
        let ng = NgramEncoder::new(VECTOR_DIM);
        let hy = HybridEncoder::new(NgramEncoder::new(VECTOR_DIM));

        assert_eq!(ng.encode("中文测试"), hy.encode("中文测试"));
        assert_eq!(ng.dim(), hy.dim());
        // sparse() returns hashed pairs in indeterminate order — compare as set.
        let mut a = ng.sparse("中文测试");
        let mut b = hy.sparse("中文测试");
        a.sort_by_key(|(h, _)| *h);
        b.sort_by_key(|(h, _)| *h);
        assert_eq!(a, b);
    }

    #[test]
    fn with_secondary_produces_normalized_output() {
        let stub = Box::new(StubSemanticEncoder { dim: VECTOR_DIM });
        let hy = HybridEncoder::with_secondary(NgramEncoder::new(VECTOR_DIM), stub);
        let out = hy.encode_full("混合编码测试");
        let norm: f32 = out.dense.iter().map(|v| v.to_f32().powi(2)).sum::<f32>().sqrt();
        assert!(
            (norm - 1.0).abs() < 1e-2,
            "fused vector should be L2-normalized, got {}",
            norm
        );
    }

    #[test]
    fn fusion_changes_output_vs_pure_ngram() {
        let ng = NgramEncoder::new(VECTOR_DIM);
        let stub = Box::new(StubSemanticEncoder { dim: VECTOR_DIM });
        let hy = HybridEncoder::with_secondary(NgramEncoder::new(VECTOR_DIM), stub);

        let ng_out = ng.encode_full("preference query");
        let hy_out = hy.encode_full("preference query");

        let sim = cosine(&ng_out.dense, &hy_out.dense);
        // Fused vector should be related to ngram (overlapping) but not
        // identical — secondary contributes its own component.
        assert!(sim < 0.999, "fused output should differ from pure ngram, sim={}", sim);
        assert!(sim > 0.0, "fused output should still correlate with ngram, sim={}", sim);
    }

    #[test]
    fn weights_shift_toward_secondary() {
        let stub_high = Box::new(StubSemanticEncoder { dim: VECTOR_DIM });
        let stub_low = Box::new(StubSemanticEncoder { dim: VECTOR_DIM });

        let hy_semantic_heavy =
            HybridEncoder::with_secondary(NgramEncoder::new(VECTOR_DIM), stub_high)
                .with_weights(0.0, 1.0);
        let hy_ngram_heavy =
            HybridEncoder::with_secondary(NgramEncoder::new(VECTOR_DIM), stub_low)
                .with_weights(1.0, 0.0);

        let stub_ref = StubSemanticEncoder { dim: VECTOR_DIM };
        let semantic_only = stub_ref.encode("alignment check");
        let ngram_only = NgramEncoder::new(VECTOR_DIM).encode("alignment check");

        // weights=(0,1) should align with secondary
        let s1 = cosine(&hy_semantic_heavy.encode("alignment check"), &semantic_only);
        // weights=(1,0) should align with primary
        let s2 = cosine(&hy_ngram_heavy.encode("alignment check"), &ngram_only);

        assert!(s1 > 0.99, "semantic-heavy should match secondary, sim={}", s1);
        assert!(s2 > 0.99, "ngram-heavy should match primary, sim={}", s2);
    }

    #[test]
    fn sparse_always_from_ngram() {
        let stub = Box::new(StubSemanticEncoder { dim: VECTOR_DIM });
        let hy = HybridEncoder::with_secondary(NgramEncoder::new(VECTOR_DIM), stub);

        // StubSemanticEncoder returns empty sparse; hybrid must still
        // expose ngram-derived sparse pairs so SparseIndex stays usable.
        let sp = hy.sparse("seed lookup test");
        assert!(!sp.is_empty(), "hybrid sparse must come from ngram, not secondary");
    }

    #[test]
    fn empty_text_yields_zero_vector() {
        let stub = Box::new(StubSemanticEncoder { dim: VECTOR_DIM });
        let hy = HybridEncoder::with_secondary(NgramEncoder::new(VECTOR_DIM), stub);
        let out = hy.encode_full("");
        assert!(out.dense.iter().all(|v| *v == f16::ZERO));
        assert!(out.sparse.is_empty());
    }

    #[test]
    fn dim_matches_primary() {
        let hy = HybridEncoder::new(NgramEncoder::new(VECTOR_DIM));
        assert_eq!(hy.dim(), VECTOR_DIM);
    }

    #[test]
    fn has_secondary_flag() {
        let hy_solo = HybridEncoder::new(NgramEncoder::new(VECTOR_DIM));
        assert!(!hy_solo.has_secondary());

        let stub = Box::new(StubSemanticEncoder { dim: VECTOR_DIM });
        let hy_dual = HybridEncoder::with_secondary(NgramEncoder::new(VECTOR_DIM), stub);
        assert!(hy_dual.has_secondary());
    }

    #[test]
    #[should_panic(expected = "matching dim")]
    fn dim_mismatch_panics() {
        let stub = Box::new(StubSemanticEncoder { dim: 512 });
        let _ = HybridEncoder::with_secondary(NgramEncoder::new(VECTOR_DIM), stub);
    }
}
