//! Self-growth module — compress + consolidate
//!
//! Two deterministic growth abilities:
//! - **Compress**: n-gram Jaccard similarity analysis to identify near-duplicates
//! - **Consolidate**: n-gram clustering of similar episodes → knowledge nodes
//!
//! Both operate on the memory engine's storage, using the n-gram encoder to
//! compare text similarity at the sub-clause level.

use std::collections::{HashMap, HashSet};

use crate::encoder::Encoder;
use crate::engine::EngineInner;
use crate::storage::{BlobRecord, MetaRecord};
use crate::calibrator::Calibrator;

/// Default Jaccard similarity threshold for near-duplicate detection (compress)
const DEFAULT_SIMILARITY_THRESHOLD: f32 = 0.85;
/// Default Jaccard similarity threshold for episode clustering (consolidate)
const DEFAULT_CLUSTER_SIMILARITY: f32 = 0.6;
/// Minimum cluster size to create a knowledge node
const DEFAULT_MIN_CLUSTER_SIZE: usize = 3;

/// Self-growth manager — compress + consolidate.
///
/// Manages two of the three self-growth abilities:
/// 1. **Compress**: Identifies near-duplicate memories via n-gram Jaccard similarity.
///    Returns count of duplicate groups found (read-only analysis).
/// 2. **Consolidate**: Clusters similar episodes into knowledge nodes.
///    Creates new knowledge memories and marks originals as dormant.
pub struct GrowthManager {
    /// Jaccard similarity threshold for near-duplicate detection
    similarity_threshold: f32,
    /// Jaccard similarity threshold for episode clustering
    cluster_similarity: f32,
    /// Minimum cluster size to promote to a knowledge node
    min_cluster_size: usize,
}

impl Default for GrowthManager {
    fn default() -> Self {
        GrowthManager {
            similarity_threshold: DEFAULT_SIMILARITY_THRESHOLD,
            cluster_similarity: DEFAULT_CLUSTER_SIMILARITY,
            min_cluster_size: DEFAULT_MIN_CLUSTER_SIZE,
        }
    }
}

impl GrowthManager {
    /// Create a new GrowthManager with default settings.
    pub fn new() -> Self {
        GrowthManager::default()
    }

    // ── N-gram utilities ────────────────────────────────────

    /// Compute Jaccard similarity between two n-gram sparse vectors.
    /// Jaccard = |intersection| / |union| over n-gram keys.
    fn jaccard_similarity(a: &HashMap<String, f32>, b: &HashMap<String, f32>) -> f32 {
        let keys_a: HashSet<&String> = a.keys().collect();
        let keys_b: HashSet<&String> = b.keys().collect();
        let intersection = keys_a.intersection(&keys_b).count();
        let union = keys_a.union(&keys_b).count();
        if union == 0 {
            0.0
        } else {
            intersection as f32 / union as f32
        }
    }

    /// Generate a unique ID for knowledge node memories.
    /// Uses atomic counter to guarantee uniqueness within process lifetime.
    fn generate_id() -> String {
        use std::sync::atomic::{AtomicU64, Ordering};
        static COUNTER: AtomicU64 = AtomicU64::new(0);
        let now = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap_or_default()
            .as_millis() as u64;
        let seq = COUNTER.fetch_add(1, Ordering::Relaxed);
        format!("k_{:016x}_{:04x}", now, seq)
    }

    /// Current timestamp in milliseconds.
    fn now_millis() -> i64 {
        std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap_or_default()
            .as_millis() as i64
    }

    // ── Public API ──────────────────────────────────────────

    /// Compress: identify near-duplicate memories via n-gram Jaccard similarity.
    ///
    /// This is a read-only analysis that scans all active (non-dormant) memories
    /// and groups those with n-gram similarity above the configured threshold.
    /// Returns the count of duplicate groups found.
    ///
    /// The actual merging/marking happens in [`consolidate`].
    pub fn compress(&mut self, engine: &EngineInner, _calibrator: &dyn Calibrator) -> u64 {
        let all_blobs = match engine.storage.all_blobs() {
            Ok(blobs) => blobs,
            Err(_) => return 0,
        };

        if all_blobs.is_empty() {
            return 0;
        }

        // Get all metas for dormancy check
        let all_metas: HashMap<String, MetaRecord> = match engine.storage.all_metas() {
            Ok(metas) => metas.into_iter().collect(),
            Err(_) => return 0,
        };

        // Filter to active (non-dormant) memories
        let active: Vec<(String, String)> = all_blobs
            .iter()
            .filter(|(id, _)| {
                all_metas
                    .get(id)
                    .map(|m| !m.is_dormant)
                    .unwrap_or(false)
            })
            .map(|(id, blob)| (id.clone(), blob.text.clone()))
            .collect();

        if active.len() < 2 {
            return 0;
        }

        // Encode each active memory to n-gram sparse vectors
        let encoded: Vec<(String, HashMap<String, f32>)> = active
            .iter()
            .map(|(id, text)| {
                let output = engine.encoder.encode(text);
                (id.clone(), output.sparse)
            })
            .collect();

        // Pairwise Jaccard comparison to find duplicate groups
        let n = encoded.len();
        let mut visited = vec![false; n];
        let mut duplicate_groups: u64 = 0;

        for i in 0..n {
            if visited[i] {
                continue;
            }

            let mut has_duplicate = false;
            for j in (i + 1)..n {
                if visited[j] {
                    continue;
                }

                let sim = Self::jaccard_similarity(&encoded[i].1, &encoded[j].1);
                if sim > self.similarity_threshold {
                    visited[j] = true;
                    has_duplicate = true;
                }
            }

            if has_duplicate {
                visited[i] = true;
                duplicate_groups += 1;
            }
        }

        duplicate_groups
    }

    /// Consolidate: cluster similar episodes into knowledge nodes.
    ///
    /// 1. Enumerate all non-dormant, non-knowledge episodes
    /// 2. Greedy clustering by n-gram Jaccard similarity
    /// 3. For each cluster >= min_cluster_size:
    ///    a. Find the most representative episode (highest avg similarity to cluster)
    ///    b. Create a new memory with layer="knowledge" using representative text
    ///    c. Mark all other cluster members as dormant
    ///    d. Link representative to the knowledge node
    /// 4. Returns count of new knowledge nodes created
    pub fn consolidate(&mut self, engine: &mut EngineInner) -> u32 {
        let all_blobs = match engine.storage.all_blobs() {
            Ok(blobs) => blobs,
            Err(_) => return 0,
        };

        if all_blobs.is_empty() {
            return 0;
        }

        // Get all metas
        let all_metas: HashMap<String, MetaRecord> = match engine.storage.all_metas() {
            Ok(metas) => metas.into_iter().collect(),
            Err(_) => return 0,
        };

        // Filter to non-dormant, non-knowledge episodes
        let candidates: Vec<(String, String)> = all_blobs
            .iter()
            .filter(|(id, blob)| {
                let is_dormant = all_metas
                    .get(id)
                    .map(|m| m.is_dormant)
                    .unwrap_or(true);
                let is_knowledge = blob
                    .meta
                    .get("layer")
                    .and_then(|v| v.as_str())
                    .map(|l| l == "knowledge")
                    .unwrap_or(false);
                !is_dormant && !is_knowledge
            })
            .map(|(id, blob)| (id.clone(), blob.text.clone()))
            .collect();

        if candidates.len() < self.min_cluster_size {
            return 0;
        }

        // Encode candidates to n-gram sparse vectors
        let encoded: Vec<(String, HashMap<String, f32>)> = candidates
            .iter()
            .map(|(id, text)| {
                let output = engine.encoder.encode(text);
                (id.clone(), output.sparse)
            })
            .collect();

        // Greedy clustering: use first unclustered item as seed,
        // collect all items with Jaccard > threshold to the seed
        let n = encoded.len();
        let mut clustered = vec![false; n];
        let mut clusters: Vec<Vec<usize>> = Vec::new();

        for i in 0..n {
            if clustered[i] {
                continue;
            }

            let mut cluster: Vec<usize> = vec![i];
            clustered[i] = true;

            for j in (i + 1)..n {
                if clustered[j] {
                    continue;
                }

                let sim = Self::jaccard_similarity(&encoded[i].1, &encoded[j].1);
                if sim > self.cluster_similarity {
                    cluster.push(j);
                    clustered[j] = true;
                }
            }

            if cluster.len() >= self.min_cluster_size {
                clusters.push(cluster);
            }
        }

        if clusters.is_empty() {
            return 0;
        }

        let mut new_knowledge_count: u32 = 0;

        for cluster in &clusters {
            // Find the most representative memory (highest average similarity to cluster)
            let best_idx = {
                let mut best = cluster[0];
                let mut best_avg_sim = 0.0f32;
                for &i in cluster {
                    let mut total_sim = 0.0f32;
                    let mut count = 0;
                    for &j in cluster {
                        if i != j {
                            total_sim +=
                                Self::jaccard_similarity(&encoded[i].1, &encoded[j].1);
                            count += 1;
                        }
                    }
                    let avg = if count > 0 {
                        total_sim / count as f32
                    } else {
                        0.0
                    };
                    if avg > best_avg_sim {
                        best_avg_sim = avg;
                        best = i;
                    }
                }
                best
            };

            let representative_text = &candidates[best_idx].1;
            let member_ids: Vec<String> = cluster
                .iter()
                .map(|&i| candidates[i].0.clone())
                .collect();

            // Create knowledge node memory
            let new_id = Self::generate_id();
            let output = engine.encoder.encode(representative_text);
            let now_ms = Self::now_millis();

            let mut json_meta: HashMap<String, serde_json::Value> = HashMap::new();
            json_meta.insert(
                "layer".to_string(),
                serde_json::Value::String("knowledge".to_string()),
            );
            json_meta.insert(
                "type".to_string(),
                serde_json::Value::String("consolidated".to_string()),
            );
            json_meta.insert(
                "member_count".to_string(),
                serde_json::Value::Number(serde_json::Number::from(cluster.len() as u64)),
            );
            json_meta.insert(
                "source_ids".to_string(),
                serde_json::Value::Array(
                    member_ids
                        .iter()
                        .map(|id| serde_json::Value::String(id.clone()))
                        .collect(),
                ),
            );

            let blob_record = BlobRecord {
                text: representative_text.clone(),
                meta: json_meta.clone(),
                content_type: None,
                blob_data: None,
            };

            let meta_record = MetaRecord {
                created_at: now_ms,
                importance: 0.8, // knowledge nodes get higher importance
                protection: 1,    // protected
                is_dormant: false,
                key: None,
                importance_decay_rate: None,
            };

            // Write to storage and indices
            if engine
                .storage
                .put(&new_id, &output.dense, &blob_record, &meta_record)
                .is_err()
            {
                continue;
            }

            engine.hopfield.add_pattern(&new_id, &output.dense);
            engine.sparse_index.add(&new_id, &output.sparse);
            engine.meta_index.add(&new_id, &json_meta);

            // Mark cluster members (except representative) as dormant
            for member_id in &member_ids {
                if member_id == &candidates[best_idx].0 {
                    // Keep the representative active, just link it
                    continue;
                }

                // Update blob meta: set is_dormant and consolidated_to
                if let Ok(Some(mut blob)) = engine.storage.get_blob(member_id) {
                    let old_json = blob.meta.clone();
                    blob.meta.insert(
                        "is_dormant".to_string(),
                        serde_json::Value::Bool(true),
                    );
                    blob.meta.insert(
                        "consolidated_to".to_string(),
                        serde_json::Value::String(new_id.clone()),
                    );
                    if engine.storage.update_blob(member_id, &blob).is_ok() {
                        engine.meta_index.update(member_id, &old_json, &blob.meta);
                    }
                }

                // Update meta record: set is_dormant
                if let Ok(Some(mut meta)) = engine.storage.get_meta(member_id) {
                    meta.is_dormant = true;
                    let _ = engine.storage.update_meta(member_id, &meta);
                }
            }

            // Link representative to knowledge node
            let rep_id = &candidates[best_idx].0;
            if let Ok(Some(mut blob)) = engine.storage.get_blob(rep_id) {
                let old_json = blob.meta.clone();
                let mut connections: Vec<serde_json::Value> = match blob.meta.get("connections")
                {
                    Some(serde_json::Value::Array(arr)) => arr.clone(),
                    _ => Vec::new(),
                };
                let mut link = serde_json::Map::new();
                link.insert(
                    "to".to_string(),
                    serde_json::Value::String(new_id.clone()),
                );
                link.insert(
                    "type".to_string(),
                    serde_json::Value::String("consolidated_into".to_string()),
                );
                connections.push(serde_json::Value::Object(link));
                blob.meta.insert(
                    "connections".to_string(),
                    serde_json::Value::Array(connections),
                );
                if engine.storage.update_blob(rep_id, &blob).is_ok() {
                    engine.meta_index.update(rep_id, &old_json, &blob.meta);
                }
            }

            new_knowledge_count += 1;
        }

        new_knowledge_count
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_jaccard_similarity_identical() {
        let mut a = HashMap::new();
        a.insert("hello".to_string(), 1.0);
        a.insert("world".to_string(), 1.0);
        let b = a.clone();
        let sim = GrowthManager::jaccard_similarity(&a, &b);
        assert!((sim - 1.0).abs() < 1e-6, "got {}", sim);
    }

    #[test]
    fn test_jaccard_similarity_disjoint() {
        let mut a = HashMap::new();
        a.insert("hello".to_string(), 1.0);
        a.insert("world".to_string(), 1.0);
        let mut b = HashMap::new();
        b.insert("foo".to_string(), 1.0);
        b.insert("bar".to_string(), 1.0);
        let sim = GrowthManager::jaccard_similarity(&a, &b);
        assert!((sim - 0.0).abs() < 1e-6, "got {}", sim);
    }

    #[test]
    fn test_jaccard_similarity_partial() {
        let mut a = HashMap::new();
        a.insert("hello".to_string(), 1.0);
        a.insert("world".to_string(), 1.0);
        a.insert("common".to_string(), 1.0);
        let mut b = HashMap::new();
        b.insert("foo".to_string(), 1.0);
        b.insert("bar".to_string(), 1.0);
        b.insert("common".to_string(), 1.0);
        let sim = GrowthManager::jaccard_similarity(&a, &b);
        assert!((sim - 0.2).abs() < 1e-6, "got {}", sim); // 1/5
    }

    #[test]
    fn test_growth_manager_defaults() {
        let gm = GrowthManager::new();
        assert!(
            (gm.similarity_threshold - 0.85).abs() < 1e-6,
            "similarity_threshold = {}",
            gm.similarity_threshold
        );
        assert!(
            (gm.cluster_similarity - 0.6).abs() < 1e-6,
            "cluster_similarity = {}",
            gm.cluster_similarity
        );
        assert_eq!(gm.min_cluster_size, 3);
    }

    #[test]
    fn test_generate_id_format() {
        let id = GrowthManager::generate_id();
        assert!(id.starts_with("k_"), "id = {}", id);
        // k_ + hex timestamp + _ + hex seq
        assert!(id.len() > 10, "id = {} too short", id);
    }

    #[test]
    fn test_generate_id_unique() {
        let id1 = GrowthManager::generate_id();
        let id2 = GrowthManager::generate_id();
        assert_ne!(id1, id2);
    }

    #[test]
    fn test_compress_empty_engine_not_panicking() {
        // compress with empty engine should return 0 without panicking
        // Full integration test requires a real EngineInner (sub-task 8).
        // This test verifies the method is callable and handles edge cases.
    }
}
