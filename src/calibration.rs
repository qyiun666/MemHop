//! CalibrationEngine — memory maintenance tasks run periodically in the background.
//!
//! These tasks use the Calibrator (a small, fast model) to:
//! - Re-score importance of under-valued memories
//! - Detect and mark semantic duplicates
//! - Validate link relationships between memories
//!
//! Calibration is best-effort: failures are logged (via BrainError) but
//! never crash the cognitive loop.

use crate::engine::EngineInner;
use crate::router::ModelRouter;

use crate::calibrator::CalibrationContext;

/// Engine for memory-calibration tasks.
///
/// Stateless — each method receives the engine and router it needs.
pub struct CalibrationEngine;

impl CalibrationEngine {
    /// Scan all non-dormant memories whose `importance` is below `threshold`
    /// and re-score them using the calibrator.
    ///
    /// Returns the number of memories that were updated.
    pub fn run_importance_scoring(
        engine: &mut EngineInner,
        router: &ModelRouter,
        threshold: f32,
    ) -> u32 {
        let metas = match engine.storage.all_metas() {
            Ok(m) => m,
            Err(e) => {
                log_error(&format!("importance: all_metas failed: {}", e));
                return 0;
            }
        };

        let mut updated = 0u32;
        for (id, meta) in &metas {
            // Skip dormant or already high-importance memories
            if meta.is_dormant || meta.importance >= threshold {
                continue;
            }

            // Get blob to extract text + context
            let blob = match engine.storage.get_blob(id) {
                Ok(Some(b)) => b,
                _ => continue,
            };

            let ctx = CalibrationContext {
                domain: blob.meta.get("domain").and_then(|v| v.as_str().map(String::from)),
                layer: blob.meta.get("layer").and_then(|v| v.as_str().map(String::from)),
                recent_count: 0,
            };

            let new_importance = match router.route_calibrate_importance(&blob.text, &ctx) {
                Ok(s) => s.clamp(0.0, 1.0),
                Err(e) => {
                    log_error(&format!("importance: calibrator failed for {}: {}", id, e));
                    continue;
                }
            };

            let mut new_meta = meta.clone();
            new_meta.importance = new_importance;
            if let Err(e) = engine.storage.update_meta(id, &new_meta) {
                log_error(&format!("importance: update_meta failed for {}: {}", id, e));
                continue;
            }
            updated += 1;
        }
        updated
    }

    /// Check the most recent `max_check` memories for semantic duplicates.
    ///
    /// When a duplicate pair is found, the newer one is marked dormant.
    /// Returns the number of duplicate pairs found.
    pub fn run_semantic_dedup(
        engine: &mut EngineInner,
        router: &ModelRouter,
        max_check: u32,
    ) -> u32 {
        // Get all metas sorted by created_at (most recent first)
        let metas = match engine.storage.all_metas() {
            Ok(m) => m,
            Err(e) => {
                log_error(&format!("dedup: all_metas failed: {}", e));
                return 0;
            }
        };

        // Collect recent non-dormant memories
        let mut recent: Vec<(String, String)> = Vec::new();
        for (id, meta) in &metas {
            if meta.is_dormant {
                continue;
            }
            if recent.len() >= max_check as usize {
                break;
            }
            if let Ok(Some(blob)) = engine.storage.get_blob(id) {
                recent.push((id.clone(), blob.text));
            }
        }

        // O(n²) pair-wise check — small n (< calibrate_threshold, default 20)
        let mut dup_count = 0u32;
        for i in 0..recent.len() {
            for j in (i + 1)..recent.len() {
                let (id_b, text_b) = &recent[j];
                let result = match router.route_calibrate_dedup(&recent[i].1, text_b) {
                    Ok(r) => r,
                    Err(e) => {
                        log_error(&format!("dedup: calibrator failed for {}: {}", id_b, e));
                        continue;
                    }
                };

                if result.is_duplicate && result.confidence > 0.7 {
                    // Mark the newer one (index j) as dormant
                    if let Ok(Some(mut meta)) = engine.storage.get_meta(id_b) {
                        meta.is_dormant = true;
                        if let Err(e) = engine.storage.update_meta(id_b, &meta) {
                            log_error(&format!("dedup: update_meta failed for {}: {}", id_b, e));
                        }
                    }
                    dup_count += 1;
                    break; // memory already dormant, skip further checks
                }
            }
        }
        dup_count
    }

    /// Validate link relationships among recent memories.
    ///
    /// Checks `connections` entries in blob metadata and removes invalid ones.
    /// Returns the number of invalid links removed.
    pub fn run_link_validation(
        engine: &mut EngineInner,
        router: &ModelRouter,
        max_check: u32,
    ) -> u32 {
        let metas = match engine.storage.all_metas() {
            Ok(m) => m,
            Err(e) => {
                log_error(&format!("link: all_metas failed: {}", e));
                return 0;
            }
        };

        let mut removed = 0u32;
        let mut checked = 0u32;

        for (id, meta) in &metas {
            if meta.is_dormant {
                continue;
            }
            if checked >= max_check {
                break;
            }

            let blob = match engine.storage.get_blob(id) {
                Ok(Some(b)) => b,
                _ => continue,
            };

            // Extract connections from blob meta
            let connections: Vec<serde_json::Value> = blob
                .meta
                .get("connections")
                .and_then(|v| v.as_array())
                .map(|a| a.to_vec())
                .unwrap_or_default();

            if connections.is_empty() {
                continue;
            }

            let mut valid_connections: Vec<serde_json::Value> = Vec::new();
            for conn in &connections {
                let to = conn
                    .get("to")
                    .and_then(|v| v.as_str())
                    .map(|s| s.to_string());
                let relation = conn
                    .get("relation")
                    .and_then(|v| v.as_str())
                    .map(|s| s.to_string())
                    .unwrap_or_default();

                let to_text = match &to {
                    Some(t) => match engine.storage.get_blob(t) {
                        Ok(Some(b)) => b.text,
                        _ => continue, // target doesn't exist, skip link
                    },
                    None => continue,
                };

                checked += 1;
                if checked > max_check {
                    break;
                }

                let result = match router.route_calibrate_link(&blob.text, &to_text, &relation) {
                    Ok(r) => r,
                    Err(e) => {
                        log_error(&format!("link: calibrator failed for {}: {}", id, e));
                        valid_connections.push(conn.clone());
                        continue;
                    }
                };

                if result.is_valid {
                    valid_connections.push(conn.clone());
                } else {
                    removed += 1;
                }
            }

            // Update blob with only valid connections
            if valid_connections.len() < connections.len() {
                let mut new_meta_map = blob.meta.clone();
                new_meta_map.insert(
                    "connections".into(),
                    serde_json::Value::Array(valid_connections),
                );
                let new_blob = crate::storage::BlobRecord {
                    text: blob.text,
                    meta: new_meta_map,
                    content_type: blob.content_type,
                    blob_data: blob.blob_data,
                };
                if let Err(e) = engine.storage.update_blob(id, &new_blob) {
                    log_error(&format!("link: update_blob failed for {}: {}", id, e));
                }
            }
        }
        removed
    }
}

/// Simple structured error logging within calibration (no external dependency).
fn log_error(msg: &str) {
    // Use eprintln for now — calibration errors are non-fatal.
    // In production this would route to a structured logger.
    eprintln!("[CalibrationError] {}", msg);
}

// ── Tests ─────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;
    use crate::engine::EngineInner;
    use crate::storage::{self, LmdbStorage};
    use crate::hopfield::ModernHopfield;
    use crate::encoder::NgramEncoder;
    use crate::index::SparseIndex;
    use crate::meta_index::MetaIndex;
    use crate::scene_gating::SceneState;
    use crate::calibrator::{Calibrator, CalibrationContext, DedupResult, LinkValidation};
    use crate::thinker::Thinker;
    use crate::router::ModelRouter;
    use crate::types::BrainError;
    use std::collections::HashSet;

    /// A calibrator that always returns a fixed importance.
    struct FixedCalibrator(f32);

    impl Calibrator for FixedCalibrator {
        fn cal_importance(&self, _: &str, _: &CalibrationContext) -> Result<f32, BrainError> {
            Ok(self.0)
        }
        fn cal_dedup(&self, a: &str, b: &str) -> Result<DedupResult, BrainError> {
            Ok(DedupResult {
                is_duplicate: a == b,
                confidence: 1.0,
                merge_suggestion: None,
            })
        }
        fn cal_link(&self, _: &str, _: &str, _: &str) -> Result<LinkValidation, BrainError> {
            Ok(LinkValidation { is_valid: true, confidence: 1.0 })
        }
    }

    /// Dummy thinker (not used in calibration tests).
    struct DummyThinker;
    impl Thinker for DummyThinker {
        fn think_fast(&self, _: &str) -> Result<String, BrainError> {
            Ok("dummy".into())
        }
        fn think_deep(&self, _: &str) -> Result<String, BrainError> {
            Ok("dummy".into())
        }
        fn think_stream(&self, _: &str, _: &mut dyn FnMut(&str)) -> Result<String, BrainError> {
            Ok("dummy".into())
        }
    }

    fn make_engine(path: &str) -> EngineInner {
        let storage = LmdbStorage::open(path).expect("open storage");
        let encoder = NgramEncoder::new(1024);
        let hopfield = ModernHopfield::new(1024, 8.0);
        let sparse_index = SparseIndex::new();
        let meta_index = MetaIndex::new();
        EngineInner {
            storage,
            encoder,
            encoder_mode: "ngram".into(),
            storage_path: path.into(),
            hopfield,
            sparse_index,
            meta_index,
            confidence_threshold: 0.3,
            beta: 8.0,
            max_memories: 100_000,
            closed: false,
            dirty_patterns: HashSet::new(),
            scene_state: SceneState::new(),
        }
    }

    fn make_router(fixed_importance: f32) -> ModelRouter {
        let thinker: Box<dyn Thinker> = Box::new(DummyThinker);
        let calibrator: Box<dyn Calibrator> = Box::new(FixedCalibrator(fixed_importance));
        ModelRouter::new(thinker, calibrator)
    }

    #[test]
    fn test_run_importance_scoring_empty_engine() {
        let dir = tempfile::tempdir().unwrap();
        let mut engine = make_engine(dir.path().join("test.db").to_str().unwrap());
        let router = make_router(0.9);
        let updated = CalibrationEngine::run_importance_scoring(&mut engine, &router, 0.3);
        assert_eq!(updated, 0);
    }

    #[test]
    fn test_run_importance_scoring_updates_low_importance() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("test.db").to_str().unwrap().to_string();
        let mut engine = make_engine(&path);

        // Insert a memory with importance 0.1 (below threshold)
        use crate::storage::MetaRecord;
        let blob = crate::storage::BlobRecord {
            text: "test memory".into(),
            meta: std::collections::HashMap::new(),
            content_type: None,
            blob_data: None,
        };
        let meta = MetaRecord {
            created_at: 1,
            importance: 0.1,
            protection: 0,
            is_dormant: false,
            key: None,
            importance_decay_rate: None,
        };
        engine.storage.put("mem1", &[], &blob, &meta).unwrap();

        // Cache the pattern so all_metas has an entry — put() already adds it.
        // but in EngineInner, remember() does the full dance. For this test we
        // directly use put() which writes to storage but doesn't update cache.
        // all_metas reads from the DB, so this works.

        let router = make_router(0.9);
        let _ = engine.storage.put("mem1", &[], &blob, &meta);

        let updated = CalibrationEngine::run_importance_scoring(&mut engine, &router, 0.3);
        // The calibrator returns 0.9, so importance should be updated
        // But note: the meta we wrote has importance 0.1 which IS < 0.3
        // So it should be updated
        assert_eq!(updated, 1);
    }

    #[test]
    fn test_run_semantic_dedup_no_duplicates() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("test.db").to_str().unwrap().to_string();
        let mut engine = make_engine(&path);

        use crate::storage::{BlobRecord, MetaRecord};
        // Insert two different memories
        for (i, text) in ["apple", "banana"].iter().enumerate() {
            let id = format!("mem{}", i);
            let blob = BlobRecord {
                text: text.to_string(),
                meta: std::collections::HashMap::new(),
                content_type: None,
                blob_data: None,
            };
            let meta = MetaRecord {
                created_at: i as i64,
                importance: 0.5,
                protection: 0,
                is_dormant: false,
                key: None,
                importance_decay_rate: None,
            };
            engine.storage.put(&id, &[], &blob, &meta).unwrap();
        }

        let router = make_router(0.5);
        let result = CalibrationEngine::run_semantic_dedup(&mut engine, &router, 10);
        // calibrator returns is_duplicate=true only when text_a==text_b
        // Since "apple" != "banana", no duplicates found
        assert_eq!(result, 0);
    }

    #[test]
    fn test_run_semantic_dedup_finds_duplicate() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("test.db").to_str().unwrap().to_string();
        let mut engine = make_engine(&path);

        use crate::storage::{BlobRecord, MetaRecord};
        // Insert two memories with SAME text
        for i in 0..2u32 {
            let id = format!("mem{}", i);
            let blob = BlobRecord {
                text: "same content".into(),
                meta: std::collections::HashMap::new(),
                content_type: None,
                blob_data: None,
            };
            let meta = MetaRecord {
                created_at: i as i64,
                importance: 0.5,
                protection: 0,
                is_dormant: false,
                key: None,
                importance_decay_rate: None,
            };
            engine.storage.put(&id, &[], &blob, &meta).unwrap();
        }

        let router = make_router(0.5);
        let result = CalibrationEngine::run_semantic_dedup(&mut engine, &router, 10);
        // calibrator returns is_duplicate=true when text_a==text_b
        assert_eq!(result, 1);

        // Verify mem1 is now dormant
        let meta1 = engine.storage.get_meta("mem1").unwrap().unwrap();
        assert!(meta1.is_dormant);
    }
}
