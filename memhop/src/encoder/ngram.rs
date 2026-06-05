use crate::encoder::{Encoder, EncoderOutput};
use half::f16;
use std::collections::HashMap;

#[allow(dead_code)]
const DEFAULT_DIM: usize = 1024;

/// N-gram length → weight: longer grams carry more information.
const NGRAM_CONFIGS: [(usize, f32); 3] = [(2, 1.0), (3, 1.5), (4, 2.0)];

/// FNV-1a hash constants (deterministic, no external dependency).
const FNV_OFFSET_BASIS: u64 = 0xcbf29ce484222325;
const FNV_PRIME: u64 = 0x100000001b3;

/// Deterministic FNV-1a hash. Same input always produces same output.
fn fnv1a_hash(data: &[u8]) -> u64 {
    let mut hash = FNV_OFFSET_BASIS;
    for byte in data {
        hash ^= *byte as u64;
        hash = hash.wrapping_mul(FNV_PRIME);
    }
    hash
}

/// N-gram hash encoder: zero-model, language-independent.
///
/// Extracts character-level 2/3/4-grams from input text, hashes each ngram
/// to a fixed dimension via FNV-1a, accumulates TF × length_weight, and
/// L2-normalizes the result. Also produces a sparse ngram→weight map.
///
/// Design:
/// - Uses `.chars()` for Unicode/Chinese-friendly character-level operations
/// - Short text (< 4 chars) gets unigram + whole-text augmentation
/// - Deterministic: same input → same output (FNV-1a, no random seed)
/// - Output: f16 dense vector for memory efficiency (2KB/record vs 4KB)
pub struct NgramEncoder {
    dim: usize,
    /// v0.4.0: Optional IDF map for term reweighting.
    /// When set, each ngram's contribution to the dense vector is multiplied
    /// by its IDF factor. High-frequency ngrams are downweighted,
    /// rare/important ngrams are emphasized.
    idf: Option<HashMap<String, f32>>,
}

impl NgramEncoder {
    pub fn new(dim: usize) -> Self {
        NgramEncoder { dim, idf: None }
    }

    /// v0.6.x: IDF-weighted alternative constructor.
    #[allow(dead_code)]
    pub fn new_with_idf(dim: usize, idf: HashMap<String, f32>) -> Self {
        NgramEncoder {
            dim,
            idf: Some(idf),
        }
    }

    /// v0.6.x: Default encoder factory.
    #[allow(dead_code)]
    pub fn default_encoder() -> Self {
        NgramEncoder::new(DEFAULT_DIM)
    }

    /// Set IDF map at runtime. Replaces any existing IDF map.
    #[allow(dead_code)]
    pub fn set_idf(&mut self, idf_map: HashMap<String, f32>) {
        self.idf = Some(idf_map);
    }

    /// Clear IDF map, restoring uniform weighting.
    #[allow(dead_code)]
    pub fn clear_idf(&mut self) {
        self.idf = None;
    }

    /// Extract character-level n-grams with weighted accumulation.
    pub(crate) fn extract_ngrams(text: &str) -> HashMap<String, f32> {
        let chars: Vec<char> = text.chars().collect();
        let char_count = chars.len();
        let mut result: HashMap<String, f32> = HashMap::new();

        if char_count > 0 && char_count < 4 {
            *result.entry(text.to_string()).or_insert(0.0) += 1.0;
            for ch in &chars {
                *result.entry(ch.to_string()).or_insert(0.0) += 0.5;
            }
        }

        for (n, weight) in NGRAM_CONFIGS {
            if char_count < n {
                continue;
            }
            let count = char_count - n + 1;
            for i in 0..count {
                let ngram: String = chars[i..i + n].iter().collect();
                *result.entry(ngram).or_insert(0.0) += weight;
            }
        }

        result
    }
}

impl Encoder for NgramEncoder {
    fn encode(&self, text: &str) -> EncoderOutput {
        let trimmed = text.trim();
        if trimmed.is_empty() {
            return EncoderOutput {
                dense: vec![f16::ZERO; self.dim],
                sparse: HashMap::new(),
            };
        }

        let ngram_weights = Self::extract_ngrams(trimmed);

        // Build dense vector in f32 with IDF-weighted accumulation
        let mut dense_f32 = vec![0.0f32; self.dim];
        if let Some(ref idf_map) = self.idf {
            for (ngram, weight) in &ngram_weights {
                let idx = (fnv1a_hash(ngram.as_bytes()) % self.dim as u64) as usize;
                let idf_factor = idf_map.get(ngram).copied().unwrap_or(1.0);
                dense_f32[idx] += *weight * idf_factor;
            }
        } else {
            for (ngram, weight) in &ngram_weights {
                let idx = (fnv1a_hash(ngram.as_bytes()) % self.dim as u64) as usize;
                dense_f32[idx] += *weight;
            }
        }

        // L2 normalize in f32
        let norm: f32 = dense_f32.iter().map(|v| v * v).sum::<f32>().sqrt();
        if norm > 1e-8 {
            for v in dense_f32.iter_mut() {
                *v /= norm;
            }
        }

        // Convert to f16 for storage
        let dense = dense_f32.iter().map(|&v| f16::from_f32(v)).collect();

        EncoderOutput {
            dense,
            sparse: ngram_weights,
        }
    }

    fn dim(&self) -> usize {
        self.dim
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::encoder::Encoder;

    fn cosine_sim(a: &[f16], b: &[f16]) -> f32 {
        let a_f32: Vec<f32> = a.iter().map(|x| x.to_f32()).collect();
        let b_f32: Vec<f32> = b.iter().map(|x| x.to_f32()).collect();
        let dot: f32 = a_f32.iter().zip(&b_f32).map(|(x, y)| x * y).sum();
        let norm_a: f32 = a_f32.iter().map(|x| x * x).sum::<f32>().sqrt();
        let norm_b: f32 = b_f32.iter().map(|x| x * x).sum::<f32>().sqrt();
        if norm_a < 1e-8 || norm_b < 1e-8 {
            return 0.0;
        }
        dot / (norm_a * norm_b)
    }

    #[allow(dead_code)]
    fn to_f16_vec(v: &[f32]) -> Vec<f16> {
        v.iter().map(|&x| f16::from_f32(x)).collect()
    }

    // ── Acceptance criteria ────────────────────────────────

    #[test]
    fn test_determinism() {
        let enc = NgramEncoder::default_encoder();
        let out1 = enc.encode("测试文本");
        let out2 = enc.encode("测试文本");
        assert_eq!(out1.dense, out2.dense, "dense vectors differ");
        assert_eq!(out1.sparse, out2.sparse, "sparse maps differ");
    }

    #[test]
    fn test_l2_normalized() {
        let enc = NgramEncoder::default_encoder();
        let out = enc.encode("测试文本");
        let norm: f32 = out
            .dense
            .iter()
            .map(|v| v.to_f32().powi(2))
            .sum::<f32>()
            .sqrt();
        assert!(
            (norm - 1.0).abs() < 1e-2,
            "L2 norm = {}, expected ~1.0 (f16 precision OK)",
            norm
        );
    }

    #[test]
    fn test_similar_more_similar_than_unrelated() {
        let enc = NgramEncoder::default_encoder();
        let sim_shared = cosine_sim(
            &enc.encode("今天天气真好").dense,
            &enc.encode("今天天气不错").dense,
        );
        let sim_unrelated = cosine_sim(
            &enc.encode("今天天气真好").dense,
            &enc.encode("量子力学方程").dense,
        );
        assert!(
            sim_shared > sim_unrelated,
            "shared-ngram sim ({}) should > unrelated sim ({})",
            sim_shared,
            sim_unrelated
        );
    }

    #[test]
    fn test_shared_ngram_similarity() {
        let enc = NgramEncoder::default_encoder();
        let sim = cosine_sim(
            &enc.encode("今天天气真好").dense,
            &enc.encode("今天天气不错").dense,
        );
        assert!(
            sim > 0.3,
            "shared-ngram similarity = {}, expected > 0.3",
            sim
        );
    }

    #[test]
    fn test_unrelated_low_similarity() {
        let enc = NgramEncoder::default_encoder();
        let sim = cosine_sim(&enc.encode("豆浆油条").dense, &enc.encode("量子力学").dense);
        assert!(sim < 0.2, "unrelated similarity = {}, expected < 0.2", sim);
    }

    #[test]
    fn test_empty_text() {
        let enc = NgramEncoder::default_encoder();
        let out = enc.encode("");
        assert!(out.dense.iter().all(|v| *v == f16::ZERO));
        assert!(out.sparse.is_empty());
    }

    #[test]
    fn test_whitespace_only() {
        let enc = NgramEncoder::default_encoder();
        let out = enc.encode("   \t\n  ");
        assert!(out.dense.iter().all(|v| *v == f16::ZERO));
        assert!(out.sparse.is_empty());
    }

    #[test]
    fn test_short_text_enhancement() {
        let enc = NgramEncoder::default_encoder();
        let out = enc.encode("你好");
        assert!(out.sparse.contains_key("你"), "should have unigram 你");
        assert!(out.sparse.contains_key("好"), "should have unigram 好");
        let hello_weight = out.sparse.get("你好").unwrap();
        assert!(
            *hello_weight > 1.0,
            "你好 weight = {}, expected > 1.0 (2-gram + whole-text enhancement)",
            hello_weight
        );
    }

    #[test]
    fn test_unicode_support() {
        let enc = NgramEncoder::default_encoder();
        let out = enc.encode("中文测试");
        assert_eq!(out.sparse.len(), 6, "expected 6 ngram entries");
    }

    #[test]
    fn test_english_text() {
        let enc = NgramEncoder::default_encoder();
        let out = enc.encode("hello world");
        let norm: f32 = out
            .dense
            .iter()
            .map(|v| v.to_f32().powi(2))
            .sum::<f32>()
            .sqrt();
        assert!(
            (norm - 1.0).abs() < 1e-2,
            "English text should be L2 normalized, norm = {}",
            norm
        );
    }

    #[test]
    fn test_mixed_language() {
        let enc = NgramEncoder::default_encoder();
        let out = enc.encode("Hello世界");
        let norm: f32 = out
            .dense
            .iter()
            .map(|v| v.to_f32().powi(2))
            .sum::<f32>()
            .sqrt();
        assert!((norm - 1.0).abs() < 1e-2, "norm = {}", norm);
        assert!(!out.sparse.is_empty());
    }

    #[test]
    fn test_dim() {
        let enc_custom = NgramEncoder::new(512);
        assert_eq!(enc_custom.dim(), 512);
        let enc_default = NgramEncoder::default_encoder();
        assert_eq!(enc_default.dim(), 1024);
    }

    #[test]
    fn test_dense_vector_length() {
        let enc = NgramEncoder::default_encoder();
        let out = enc.encode("test");
        assert_eq!(out.dense.len(), enc.dim());
    }

    #[test]
    fn test_repeated_ngram_tf() {
        let enc = NgramEncoder::default_encoder();
        let out_repeat = enc.encode("abcab");
        let out_single = enc.encode("abcd");
        let ab_weight_repeat = out_repeat.sparse.get("ab").unwrap_or(&0.0);
        let ab_weight_single = out_single.sparse.get("ab").unwrap_or(&0.0);
        assert!(
            *ab_weight_repeat > *ab_weight_single,
            "repeated ngram should have higher TF: 'ab' in 'abcab' = {}, in 'abcd' = {}",
            ab_weight_repeat,
            ab_weight_single
        );
    }

    #[test]
    fn test_no_shared_ngrams_near_zero() {
        let enc = NgramEncoder::default_encoder();
        let sim = cosine_sim(
            &enc.encode("豆浆油条").dense,
            &enc.encode("早餐吃什么").dense,
        );
        assert!(
            sim < 0.15,
            "texts with no shared ngrams should have very low similarity: {}",
            sim
        );
    }
}
