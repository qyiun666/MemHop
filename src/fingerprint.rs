//! Cognitive Fingerprint — memory content fingerprinting and conflict detection.
//!
//! When multiple CloneCats write to the same memory store (HiveMind mode),
//! Cognitive Fingerprint detects write conflicts via content hashing and
//! version tracking.
//!
//! Fingerprint data is stored in the blob's JSON metadata under keys:
//! - `_fingerprint_hash`: content hash (u64 as string)
//! - `_fingerprint_version`: version counter (u64 as string)
//! - `_fingerprint_writer`: last writer ID (string)
//! - `_fingerprint_modified_at`: last modification timestamp (u64 as string)

use std::collections::hash_map::DefaultHasher;
use std::hash::{Hash, Hasher};

use crate::calibrator::Calibrator;

// ── MemoryFingerprint ─────────────────────────────────────

/// Fingerprint data for a single memory — embedded in blob metadata.
#[derive(Debug, Clone)]
pub struct MemoryFingerprint {
    pub id: String,
    pub content_hash: u64,
    pub version: u32,
    pub last_writer: String,
    pub last_modified_at: u64,
}

// ── ConflictInfo ──────────────────────────────────────────

/// Information about a detected write conflict.
#[derive(Debug, Clone)]
pub struct ConflictInfo {
    pub memory_id: String,
    pub my_version: u32,
    pub current_version: u32,
    pub current_writer: String,
}

// ── Resolution ────────────────────────────────────────────

/// How to resolve a write conflict.
#[derive(Debug, Clone)]
pub enum Resolution {
    /// Keep my version (I was right)
    KeepMine,
    /// Keep their version (they were right)
    KeepTheirs,
    /// Merge both versions
    Merge {
        merged_text: String,
    },
    /// Keep both as separate memories
    KeepBoth,
}

// ── CognitiveFingerprint ──────────────────────────────────

/// Stateless fingerprint utility — hash computation and conflict detection.
///
/// This struct holds no runtime state; fingerprints are stored externally
/// (in blob metadata or a coordinating store).
pub struct CognitiveFingerprint;

impl CognitiveFingerprint {
    /// Compute a content hash for the given text using SipHash.
    ///
    /// Returns a u64 fingerprint that changes if the text changes.
    pub fn compute_hash(text: &str) -> u64 {
        let mut hasher = DefaultHasher::new();
        text.hash(&mut hasher);
        hasher.finish()
    }

    /// Extract a `MemoryFingerprint` from serialised metadata fields.
    ///
    /// Expects the following keys in `meta`:
    /// - `_fingerprint_hash` (string → u64)
    /// - `_fingerprint_version` (string → u32)
    /// - `_fingerprint_writer` (string)
    /// - `_fingerprint_modified_at` (string → u64)
    pub fn extract_from_meta(
        id: &str,
        meta: &std::collections::HashMap<String, serde_json::Value>,
    ) -> Option<MemoryFingerprint> {
        let hash_str = meta.get("_fingerprint_hash")?.as_str()?;
        let version_str = meta.get("_fingerprint_version")?.as_str()?;
        let writer = meta.get("_fingerprint_writer")?.as_str()?;
        let modified_str = meta.get("_fingerprint_modified_at")?.as_str()?;

        let content_hash: u64 = hash_str.parse().ok()?;
        let version: u32 = version_str.parse().ok()?;
        let last_modified_at: u64 = modified_str.parse().ok()?;

        Some(MemoryFingerprint {
            id: id.to_string(),
            content_hash,
            version,
            last_writer: writer.to_string(),
            last_modified_at,
        })
    }

    /// Build metadata entries for a fingerprint so callers can embed them
    /// into a `BlobRecord.meta` HashMap.
    pub fn build_meta_entries(
        content: &str,
        writer: &str,
        version: u32,
        now_ms: u64,
    ) -> std::collections::HashMap<String, serde_json::Value> {
        let mut meta = std::collections::HashMap::new();
        let hash = Self::compute_hash(content);
        meta.insert(
            "_fingerprint_hash".to_string(),
            serde_json::Value::String(hash.to_string()),
        );
        meta.insert(
            "_fingerprint_version".to_string(),
            serde_json::Value::String(version.to_string()),
        );
        meta.insert(
            "_fingerprint_writer".to_string(),
            serde_json::Value::String(writer.to_string()),
        );
        meta.insert(
            "_fingerprint_modified_at".to_string(),
            serde_json::Value::String(now_ms.to_string()),
        );
        meta
    }

    /// Check whether a write to `memory_id` by `writer` conflicts with a
    /// previously stored fingerprint.
    ///
    /// A conflict exists when:
    /// - The stored writer differs from the current writer, AND
    /// - The stored version is ahead of `my_version`
    ///
    /// Returns `Some(ConflictInfo)` if a conflict is detected.
    pub fn check_conflict(
        &self,
        memory_id: &str,
        writer: &str,
        my_version: u32,
        stored: &MemoryFingerprint,
    ) -> Option<ConflictInfo> {
        // Same writer — no conflict (they are updating their own memory)
        if stored.last_writer == writer {
            return None;
        }

        // Stored version is not ahead — no conflict
        if stored.version <= my_version {
            return None;
        }

        // Content hash is identical — no semantic conflict
        // (the other writer happened to produce the same content)
        // We still report the version mismatch for transparency.

        Some(ConflictInfo {
            memory_id: memory_id.to_string(),
            my_version,
            current_version: stored.version,
            current_writer: stored.last_writer.clone(),
        })
    }

    /// Resolve a conflict by delegating to the Calibrator.
    ///
    /// Returns a `Resolution`:
    /// - Calibrator says duplicate → `Merge` with suggestion
    /// - Calibrator says distinct → `KeepBoth`
    pub fn resolve_conflict(
        &self,
        conflict: &ConflictInfo,
        my_content: &str,
        their_content: &str,
        calibrator: &dyn Calibrator,
    ) -> Resolution {
        let result = match calibrator.cal_dedup(my_content, their_content) {
            Ok(r) => r,
            Err(_) => return Resolution::KeepBoth,
        };

        if result.is_duplicate {
            Resolution::Merge {
                merged_text: result
                    .merge_suggestion
                    .unwrap_or_else(|| their_content.to_string()),
            }
        } else {
            Resolution::KeepBoth
        }
    }

    /// Determine the next version number.  If `stored` exists and the
    /// content hash matches, reuse the version (no real change). Otherwise
    /// increment.
    pub fn next_version(stored: Option<&MemoryFingerprint>, new_content: &str) -> u32 {
        let new_hash = Self::compute_hash(new_content);
        match stored {
            Some(fp) if fp.content_hash == new_hash => fp.version, // unchanged
            Some(fp) => fp.version + 1,
            None => 1,
        }
    }
}

// ── Tests ─────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;
    use crate::calibrator::{CalibrationContext, DedupResult, LinkValidation};

    struct MockCalibrator;
    impl Calibrator for MockCalibrator {
        fn cal_importance(
            &self,
            _text: &str,
            _context: &CalibrationContext,
        ) -> Result<f32, crate::types::BrainError> {
            Ok(0.5)
        }
        fn cal_dedup(
            &self,
            _text_a: &str,
            _text_b: &str,
        ) -> Result<DedupResult, crate::types::BrainError> {
            Ok(DedupResult {
                is_duplicate: true,
                confidence: 0.9,
                merge_suggestion: Some("merged".into()),
            })
        }
        fn cal_link(
            &self,
            _from_text: &str,
            _to_text: &str,
            _relation: &str,
        ) -> Result<LinkValidation, crate::types::BrainError> {
            Ok(LinkValidation {
                is_valid: true,
                confidence: 0.9,
            })
        }
    }

    // ── Hash ─────────────────────────────────────────────

    #[test]
    fn test_compute_hash_is_deterministic() {
        let h1 = CognitiveFingerprint::compute_hash("hello world");
        let h2 = CognitiveFingerprint::compute_hash("hello world");
        assert_eq!(h1, h2);
    }

    #[test]
    fn test_compute_hash_differs_for_different_text() {
        let h1 = CognitiveFingerprint::compute_hash("hello world");
        let h2 = CognitiveFingerprint::compute_hash("hello world!");
        assert_ne!(h1, h2);
    }

    #[test]
    fn test_compute_hash_empty_string() {
        let h = CognitiveFingerprint::compute_hash("");
        // Should not panic and return a deterministic value
        assert_eq!(h, CognitiveFingerprint::compute_hash(""));
    }

    // ── Build / extract meta ─────────────────────────────

    #[test]
    fn test_build_and_extract_roundtrip() {
        let meta = CognitiveFingerprint::build_meta_entries("test content", "cat_a", 1, 1000);
        let extracted = CognitiveFingerprint::extract_from_meta("mem_1", &meta);
        assert!(extracted.is_some());
        let fp = extracted.unwrap();
        assert_eq!(fp.id, "mem_1");
        assert_eq!(fp.version, 1);
        assert_eq!(fp.last_writer, "cat_a");
        assert_eq!(
            fp.content_hash,
            CognitiveFingerprint::compute_hash("test content")
        );
    }

    #[test]
    fn test_extract_from_empty_meta() {
        let meta = std::collections::HashMap::new();
        let extracted = CognitiveFingerprint::extract_from_meta("mem_1", &meta);
        assert!(extracted.is_none());
    }

    #[test]
    fn test_build_meta_keys_exist() {
        let meta = CognitiveFingerprint::build_meta_entries("text", "cat_b", 3, 5000);
        assert!(meta.contains_key("_fingerprint_hash"));
        assert!(meta.contains_key("_fingerprint_version"));
        assert!(meta.contains_key("_fingerprint_writer"));
        assert!(meta.contains_key("_fingerprint_modified_at"));
    }

    // ── Conflict detection ───────────────────────────────

    #[test]
    fn test_no_conflict_same_writer() {
        let fp = MemoryFingerprint {
            id: "mem_1".into(),
            content_hash: 123,
            version: 5,
            last_writer: "cat_a".into(),
            last_modified_at: 2000,
        };
        let cf = CognitiveFingerprint;
        let conflict = cf.check_conflict("mem_1", "cat_a", 3, &fp);
        assert!(conflict.is_none(), "Same writer should not conflict");
    }

    #[test]
    fn test_no_conflict_version_not_ahead() {
        let fp = MemoryFingerprint {
            id: "mem_1".into(),
            content_hash: 123,
            version: 3,
            last_writer: "cat_b".into(),
            last_modified_at: 2000,
        };
        let cf = CognitiveFingerprint;
        // My version (5) is AHEAD of stored (3) → my write is newer
        let conflict = cf.check_conflict("mem_1", "cat_a", 5, &fp);
        assert!(conflict.is_none(), "My version is newer, no conflict");
    }

    #[test]
    fn test_conflict_detected() {
        let fp = MemoryFingerprint {
            id: "mem_1".into(),
            content_hash: 456,
            version: 5,
            last_writer: "cat_b".into(),
            last_modified_at: 2000,
        };
        let cf = CognitiveFingerprint;
        // Different writer + stored version ahead → conflict
        let conflict = cf.check_conflict("mem_1", "cat_a", 3, &fp);
        assert!(conflict.is_some());
        let info = conflict.unwrap();
        assert_eq!(info.memory_id, "mem_1");
        assert_eq!(info.my_version, 3);
        assert_eq!(info.current_version, 5);
        assert_eq!(info.current_writer, "cat_b");
    }

    // ── Resolution ───────────────────────────────────────

    #[test]
    fn test_resolve_conflict_merge() {
        let calibrator = MockCalibrator;
        let cf = CognitiveFingerprint;
        let conflict = ConflictInfo {
            memory_id: "mem_1".into(),
            my_version: 1,
            current_version: 2,
            current_writer: "cat_b".into(),
        };
        let resolution = cf.resolve_conflict(&conflict, "my text", "their text", &calibrator);
        match resolution {
            Resolution::Merge { merged_text } => {
                assert_eq!(merged_text, "merged");
            }
            other => panic!("Expected Merge, got {:?}", other),
        }
    }

    #[test]
    fn test_resolve_conflict_keep_both_on_failure() {
        // A calibrator that returns error should result in KeepBoth
        struct FailingCalibrator;
        impl Calibrator for FailingCalibrator {
            fn cal_importance(
                &self,
                _: &str,
                _: &CalibrationContext,
            ) -> Result<f32, crate::types::BrainError> {
                Ok(0.5)
            }
            fn cal_dedup(
                &self,
                _: &str,
                _: &str,
            ) -> Result<DedupResult, crate::types::BrainError> {
                Err(crate::types::BrainError::CalibratorFailed("mock".into()))
            }
            fn cal_link(
                &self,
                _: &str,
                _: &str,
                _: &str,
            ) -> Result<LinkValidation, crate::types::BrainError> {
                Ok(LinkValidation {
                    is_valid: true,
                    confidence: 0.9,
                })
            }
        }

        let cf = CognitiveFingerprint;
        let conflict = ConflictInfo {
            memory_id: "mem_1".into(),
            my_version: 1,
            current_version: 2,
            current_writer: "cat_b".into(),
        };
        let resolution =
            cf.resolve_conflict(&conflict, "a", "b", &FailingCalibrator);
        assert!(matches!(resolution, Resolution::KeepBoth));
    }

    // ── Next version ─────────────────────────────────────

    #[test]
    fn test_next_version_first_write() {
        let version = CognitiveFingerprint::next_version(None, "new content");
        assert_eq!(version, 1);
    }

    #[test]
    fn test_next_version_increments() {
        let stored = MemoryFingerprint {
            id: "mem_1".into(),
            content_hash: CognitiveFingerprint::compute_hash("old content"),
            version: 3,
            last_writer: "cat_a".into(),
            last_modified_at: 1000,
        };
        let version = CognitiveFingerprint::next_version(Some(&stored), "new content");
        assert_eq!(version, 4);
    }

    #[test]
    fn test_next_version_unchanged_content() {
        let stored = MemoryFingerprint {
            id: "mem_1".into(),
            content_hash: CognitiveFingerprint::compute_hash("same content"),
            version: 3,
            last_writer: "cat_a".into(),
            last_modified_at: 1000,
        };
        let version = CognitiveFingerprint::next_version(Some(&stored), "same content");
        assert_eq!(version, 3, "No change → same version");
    }
}
