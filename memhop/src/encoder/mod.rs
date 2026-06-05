use half::f16;
use std::collections::HashMap;

// ── Encoder output ────────────────────────────────────────

#[derive(Debug, PartialEq)]
pub struct EncoderOutput {
    pub dense: Vec<f16>,
    pub sparse: HashMap<String, f32>,
}

// ── Encoder trait ─────────────────────────────────────────

pub trait Encoder: Send + Sync {
    fn encode(&self, text: &str) -> EncoderOutput;
    fn dim(&self) -> usize { 1024 }
    fn mode(&self) -> &str { "ngram" }
}

// ── NgramEncoder ──────────────────────────────────────────

mod ngram;
pub use ngram::NgramEncoder;

// ── CandleEncoder (feature-gated, pure Rust BERT) ────────────
// v0.18.0: 暂时禁用candle feature
// #[cfg(feature = "candle")]
// pub(crate) mod candle;
// #[cfg(feature = "candle")]
// pub use candle::CandleEncoder;
