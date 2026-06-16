// Simple deterministic test encoder — generates vectors from text content hash.
// For use in integration tests only; NOT part of the library.

use half::f16;
use memhop::encoder::{Encoder, EncoderOutput};
use memhop::MemHopError;
use std::collections::HashMap;

pub struct TestEncoder {
    dim: usize,
}

impl TestEncoder {
    pub fn new(dim: usize) -> Self {
        Self { dim }
    }
}

impl Encoder for TestEncoder {
    fn encode(&self, text: &str) -> Result<EncoderOutput, MemHopError> {
        // Deterministic content-sensitive vectors via FNV hash
        let mut hash: u64 = 0xcbf29ce484222325;
        for byte in text.bytes() {
            hash ^= byte as u64;
            hash = hash.wrapping_mul(0x100000001b3);
        }
        let hash_f = (hash & 0xFFFF) as f32;

        let dense = (0..self.dim)
            .map(|i| f16::from_f32((hash_f + i as f32) / (self.dim as f32)))
            .collect();

        let mut sparse = HashMap::new();
        for word in text.split_whitespace() {
            sparse.insert(word.to_lowercase(), 1.0);
        }

        Ok(EncoderOutput { dense, sparse })
    }

    fn dim(&self) -> usize {
        self.dim
    }

    fn mode(&self) -> &str {
        "test"
    }
}
