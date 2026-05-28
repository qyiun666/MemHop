use half::f16;
use crate::encoder::{Encoder, EncoderOutput, NgramEncoder};

pub const DEFAULT_NGRAM_WEIGHT: f32 = 0.3;
pub const DEFAULT_SEMANTIC_WEIGHT: f32 = 0.7;

pub struct HybridEncoder {
    primary: NgramEncoder,
    secondary: Option<Box<dyn Encoder>>,
    ngram_weight: f32,
    semantic_weight: f32,
    dim: usize,
}

impl HybridEncoder {
    pub fn new(primary: NgramEncoder) -> Self {
        let dim = primary.dim();
        HybridEncoder { primary, secondary: None, ngram_weight: DEFAULT_NGRAM_WEIGHT, semantic_weight: DEFAULT_SEMANTIC_WEIGHT, dim }
    }

    pub fn with_secondary(primary: NgramEncoder, secondary: Box<dyn Encoder>) -> Self {
        assert_eq!(primary.dim(), secondary.dim(), "encoder dim mismatch");
        let dim = primary.dim();
        HybridEncoder { primary, secondary: Some(secondary), ngram_weight: DEFAULT_NGRAM_WEIGHT, semantic_weight: DEFAULT_SEMANTIC_WEIGHT, dim }
    }

    pub fn with_weights(mut self, ngram_weight: f32, semantic_weight: f32) -> Self {
        self.ngram_weight = ngram_weight; self.semantic_weight = semantic_weight; self
    }

    pub fn has_secondary(&self) -> bool { self.secondary.is_some() }

    pub fn encode_full(&self, text: &str) -> EncoderOutput {
        let primary_out = self.primary.encode(text);
        let secondary = match &self.secondary { Some(s) => s, None => return primary_out };
        let dense = fuse_dense(&primary_out.dense, &secondary.encode(text).dense, self.ngram_weight, self.semantic_weight);
        EncoderOutput { dense, sparse: primary_out.sparse }
    }
}

impl Encoder for HybridEncoder {
    fn encode(&self, text: &str) -> EncoderOutput { self.encode_full(text) }
    fn dim(&self) -> usize { self.dim }
}

fn fuse_dense(a: &[f16], b: &[f16], wa: f32, wb: f32) -> Vec<f16> {
    debug_assert_eq!(a.len(), b.len());
    let dim = a.len();
    let mut out = vec![0.0f32; dim];
    let na = l2_norm(a); let nb = l2_norm(b);
    if na > 1e-8 { for (i, v) in a.iter().enumerate() { out[i] += wa * (v.to_f32() / na); } }
    if nb > 1e-8 { for (i, v) in b.iter().enumerate() { out[i] += wb * (v.to_f32() / nb); } }
    let norm: f32 = out.iter().map(|x| x * x).sum::<f32>().sqrt();
    if norm > 1e-8 { for v in out.iter_mut() { *v /= norm; } }
    out.iter().map(|&v| f16::from_f32(v)).collect()
}

fn l2_norm(v: &[f16]) -> f32 { v.iter().map(|x| { let f = x.to_f32(); f * f }).sum::<f32>().sqrt() }
