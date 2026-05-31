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
    #[allow(dead_code)]
    fn dim(&self) -> usize {
        1024
    }
    #[allow(dead_code)]
    fn mode(&self) -> &str {
        "ngram"
    }
}

// ── NgramEncoder ──────────────────────────────────────────

mod ngram;

pub use ngram::NgramEncoder;

// ── Hybrid encoder ──────────────────────────────────────

mod hybrid;
pub use hybrid::HybridEncoder;

// ── ONNX encoder (feature-gated) ──────────────────────────

#[cfg(feature = "onnx")]
mod onnx;
#[cfg(feature = "onnx")]
pub use onnx::OnnxEncoder;

// ── Candle encoder (feature-gated, pure Rust) ─────────────

#[cfg(feature = "candle")]
mod candle;
#[cfg(feature = "candle")]
pub use candle::CandleEncoder;

// ── ONNX cross-encoder reranker (feature-gated) ──────────

#[cfg(feature = "onnx")]
pub mod reranker;
#[cfg(feature = "onnx")]
pub use reranker::Reranker;

// ── API encoder (feature-gated) ───────────────────────────

#[cfg(feature = "api-encoder")]
mod api;
#[cfg(feature = "api-encoder")]
pub use api::ApiEncoder;
