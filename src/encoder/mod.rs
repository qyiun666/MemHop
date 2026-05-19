use half::f16;
use std::collections::HashMap;

// ── Encoder output ────────────────────────────────────────

#[derive(Debug, PartialEq)]
pub struct EncoderOutput {
    pub dense: Vec<f16>,               // 1024 维, f16 存储
    pub sparse: HashMap<String, f32>,  // ngram -> weight
}

// ── Encoder trait ─────────────────────────────────────────

pub trait Encoder: Send + Sync {
    fn encode(&self, text: &str) -> EncoderOutput;
    fn dim(&self) -> usize {
        1024
    }
    fn mode(&self) -> &str {
        "ngram"
    }
}

// ── NgramEncoder ──────────────────────────────────────────

mod ngram;

pub use ngram::NgramEncoder;
