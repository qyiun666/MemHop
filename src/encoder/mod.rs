// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

use crate::MemHopError;
use half::f16;
use std::collections::HashMap;

// ============================================================================
// Encoder trait & output
// ============================================================================

pub trait Encoder: Send + Sync {
    fn encode(&self, text: &str) -> Result<EncoderOutput, MemHopError>;
    fn dim(&self) -> usize;
    fn mode(&self) -> &str;

    /// Rerank a list of documents against a query.
    ///
    /// The default implementation returns an error so existing implementors
    /// keep compiling while the capability is rolled out.
    fn rerank(&self, _query: &str, _documents: &[String]) -> Result<Vec<f32>, MemHopError> {
        Err(MemHopError::EncoderError("rerank not implemented".into()))
    }
}

pub struct EncoderOutput {
    pub dense: Vec<f16>,
    pub sparse: HashMap<String, f32>,
}

#[cfg(feature = "grpc-encoder")]
pub mod grpc;

#[cfg(feature = "grpc-encoder")]
pub use grpc::{GrpcEncoder, DEFAULT_ENCODER_ADDR};

// ============================================================================
// MockEncoder — deterministic, dependency-free test double
// ============================================================================

#[cfg(test)]
pub struct MockEncoder {
    dim: usize,
}

#[cfg(test)]
impl MockEncoder {
    pub fn new(dim: usize) -> Self {
        Self { dim }
    }
}

#[cfg(test)]
impl Encoder for MockEncoder {
    fn encode(&self, _text: &str) -> Result<EncoderOutput, MemHopError> {
        Ok(EncoderOutput {
            dense: vec![f16::from_f32(0.1); self.dim],
            sparse: HashMap::new(),
        })
    }

    fn dim(&self) -> usize {
        self.dim
    }

    fn mode(&self) -> &str {
        "mock"
    }

    fn rerank(&self, query: &str, documents: &[String]) -> Result<Vec<f32>, MemHopError> {
        let query_tokens: Vec<String> =
            query.split_whitespace().map(|t| t.to_lowercase()).collect();
        Ok(documents
            .iter()
            .map(|doc| {
                let doc_lower = doc.to_lowercase();
                query_tokens
                    .iter()
                    .filter(|t| doc_lower.contains(t.as_str()))
                    .count() as f32
            })
            .collect())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_mock_encoder_encode_dim() {
        let encoder = MockEncoder::new(8);
        let out = encoder.encode("hello world").unwrap();
        assert_eq!(out.dense.len(), 8);
        assert!(out.sparse.is_empty());
        assert_eq!(encoder.dim(), 8);
        assert_eq!(encoder.mode(), "mock");
    }

    #[test]
    fn test_mock_encoder_rerank_overlap() {
        let encoder = MockEncoder::new(4);
        let scores = encoder
            .rerank(
                "foo bar",
                &[
                    "foo".to_string(),
                    "bar baz".to_string(),
                    "foo bar qux".to_string(),
                    " unrelated ".to_string(),
                ],
            )
            .unwrap();
        assert_eq!(scores, vec![1.0, 1.0, 2.0, 0.0]);
    }
}
