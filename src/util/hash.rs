// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

use std::hash::Hasher;
use twox_hash::XxHash64;

pub fn hash_id(id: &str) -> u64 {
    let mut hasher = XxHash64::with_seed(0);
    hasher.write(id.as_bytes());
    hasher.finish()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_hash_consistency() {
        let hash1 = hash_id("test-id-123");
        let hash2 = hash_id("test-id-123");
        assert_eq!(hash1, hash2);
    }

    #[test]
    fn test_hash_different_inputs() {
        let hash1 = hash_id("test-id-1");
        let hash2 = hash_id("test-id-2");
        assert_ne!(hash1, hash2);
    }

    #[test]
    fn test_hash_empty_string() {
        let hash = hash_id("");
        assert_ne!(hash, 0); // xxHash64 of empty string is not zero
    }

    #[test]
    fn test_hash_unicode() {
        let hash1 = hash_id("你好世界");
        let hash2 = hash_id("你好世界");
        assert_eq!(hash1, hash2);

        let hash3 = hash_id("🦀 Rust");
        let hash4 = hash_id("🦀 Rust");
        assert_eq!(hash3, hash4);

        assert_ne!(hash1, hash3);
    }

    #[test]
    fn test_hash_deterministic() {
        let expected_hash = 0x8e656eb0ab2d506c;
        for _ in 0..100 {
            let hash = hash_id("deterministic-test");
            assert_eq!(hash, expected_hash);
        }
    }
}
