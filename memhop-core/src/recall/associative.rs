//! recall/associative — associative recall via BFS hyperedge diffusion.
//! Uses L1Hypergraph::bfs_spread() for Hopfield-like spreading activation.

use std::collections::HashMap;

use crate::brain::Brain;
use crate::error::Result;
use crate::query_engine;
use crate::types::{Layer, RecallRequest, RecallResponse, RecallResult};

/// Associative recall: standard L1 search → BFS spread from seed → RRF merge.
pub fn associative_recall(brain: &mut Brain, req: &RecallRequest) -> Result<RecallResponse> {
    // 1. Get seed results from standard L1 search
    let encoded = brain.encoder.encode(&req.query);
    let l1_results =
        query_engine::search_l1(brain, &encoded.sparse, &encoded.dense, req.max_results)?;

    if l1_results.is_empty() {
        return Ok(RecallResponse {
            results: vec![],
            total_count: 0,
            l0_profile: None,
            confidence: None,
            activated_topics: Vec::new(),
            recommended_crystals: Vec::new(),
        });
    }

    // 2. Extract seed IDs (top 3)
    let seed_ids: Vec<String> = l1_results.iter().take(3).map(|r| r.id.clone()).collect();

    // 3. BFS spread
    let depth = req.spread_depth.unwrap_or(2);
    brain.ensure_l1()?;
    let l1 = brain.l1.as_mut().unwrap();
    let l1_env = brain.l1_env.as_ref().unwrap();
    let txn = l1_env
        .env
        .read_txn()
        ?;
    let spread = l1.bfs_spread(&txn, l1_env, &seed_ids, depth)?;
    drop(txn);

    if spread.is_empty() {
        // No spread results: return standard L1 results
        return Ok(RecallResponse {
            total_count: l1_results.len(),
            results: l1_results,
            l0_profile: None,
            confidence: None,
            activated_topics: Vec::new(),
            recommended_crystals: Vec::new(),
        });
    }

    // 4. Load spread node details into RecallResult
    let txn = l1_env
        .env
        .read_txn()
        ?;
    let mut spread_results: Vec<RecallResult> = Vec::new();
    for (nid, weight) in &spread {
        // Skip seeds (already in l1_results)
        if seed_ids.contains(nid) {
            continue;
        }
        if let Ok(Some(node)) = l1.get_node(&txn, l1_env, nid) {
            spread_results.push(RecallResult {
                layer: Layer::L1,
                id: nid.clone(),
                text: node
                    .summary
                    .unwrap_or(node.text)
                    .chars()
                    .take(200)
                    .collect(),
                score: *weight,
                topic_label: None,
                created_at: node.created_at,
                version: node.version,
                emotion: None,
            });
        }
    }
    drop(txn);

    // 5. RRF merge: l1_results + spread_results
    let merged = merge_rrf(l1_results, spread_results, req.max_results);
    let total = merged.len();

    Ok(RecallResponse {
        results: merged,
        total_count: total,
        l0_profile: None,
        confidence: None,
        activated_topics: Vec::new(),
        recommended_crystals: Vec::new(),
    })
}

/// Reciprocal Rank Fusion merge of two ranked result lists.
fn merge_rrf(
    primary: Vec<RecallResult>,
    secondary: Vec<RecallResult>,
    max: usize,
) -> Vec<RecallResult> {
    let k = 60.0f64;
    let mut rrf_scores: HashMap<String, f64> = HashMap::new();
    let mut id_to_result: HashMap<String, RecallResult> = HashMap::new();

    // Primary list ranks
    for (rank, r) in primary.into_iter().enumerate() {
        *rrf_scores.entry(r.id.clone()).or_insert(0.0) += 1.0 / (k + rank as f64);
        id_to_result.entry(r.id.clone()).or_insert(r);
    }

    // Secondary list ranks
    for (rank, r) in secondary.into_iter().enumerate() {
        *rrf_scores.entry(r.id.clone()).or_insert(0.0) += 1.0 / (k + rank as f64);
        id_to_result.entry(r.id.clone()).or_insert(r);
    }

    let mut ranked: Vec<(String, f64)> = rrf_scores.into_iter().collect();
    ranked.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
    ranked.truncate(max);

    ranked
        .into_iter()
        .filter_map(|(id, rrf_score)| {
            id_to_result.remove(&id).map(|mut r| {
                r.score = rrf_score as f32;
                r
            })
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_merge_rrf_primary_empty() {
        let primary = vec![];
        let secondary = vec![RecallResult {
            layer: Layer::L1,
            id: "kn_1".to_string(),
            text: "test".to_string(),
            score: 0.5,
            topic_label: None,
            created_at: 1000,
            version: 1,
            emotion: None,
        }];
        let result = merge_rrf(primary, secondary, 10);
        assert_eq!(result.len(), 1);
        assert_eq!(result[0].id, "kn_1");
    }

    #[test]
    fn test_merge_rrf_deduplicates() {
        let primary = vec![RecallResult {
            layer: Layer::L1,
            id: "kn_1".to_string(),
            text: "test".to_string(),
            score: 0.9,
            topic_label: None,
            created_at: 1000,
            version: 1,
            emotion: None,
        }];
        let secondary = vec![RecallResult {
            layer: Layer::L1,
            id: "kn_1".to_string(),
            text: "test".to_string(),
            score: 0.5,
            topic_label: None,
            created_at: 1000,
            version: 1,
            emotion: None,
        }];
        let result = merge_rrf(primary, secondary, 10);
        assert_eq!(result.len(), 1);
    }

    #[test]
    fn test_merge_rrf_max_limit() {
        let primary: Vec<RecallResult> = (0..5)
            .map(|i| RecallResult {
                layer: Layer::L1,
                id: format!("kn_{}", i),
                text: "test".to_string(),
                score: 1.0,
                topic_label: None,
                created_at: 1000 + i as i64,
                version: 1,
                emotion: None,
            })
            .collect();
        let secondary: Vec<RecallResult> = (5..10)
            .map(|i| RecallResult {
                layer: Layer::L1,
                id: format!("kn_{}", i),
                text: "test".to_string(),
                score: 0.5,
                topic_label: None,
                created_at: 1000 + i as i64,
                version: 1,
                emotion: None,
            })
            .collect();

        let result = merge_rrf(primary, secondary, 3);
        assert_eq!(result.len(), 3);
    }
}
