// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L1 associated-context lookup: finds L2 contexts related to matched contexts
//! via the L1 hypergraph (ContextNode + Hyperedge traversal).

use crate::index::sparse::tokenize;
use crate::layers::context::{ContextSlot, TopicSlot};
use crate::layers::context_node::ContextNode;
use crate::layers::hyperedge::HyperedgeSlot;
use crate::query::search::L1ReverseIndex;
use crate::query::types::L1Preview;
use crate::shared::common::format_hash;
use crate::storage::record::*;
use crate::storage::StorageEngine;
use crate::MemHopError;
use std::collections::{HashMap, HashSet};

/// Via L1 hypergraph, find associated L2 contexts for matched contexts.
///
/// Uses L1 reverse index to find ContextNodes, traverses hyperedges to
/// discover sibling nodes, then loads their associated L2 ContextSlots.
/// Also includes parent contexts of matched contexts.
pub fn get_l1_associated_contexts(
    engine: &StorageEngine,
    matched: &[(ContextSlot, f32)],
    l1_reverse: &L1ReverseIndex,
) -> Result<Vec<(ContextSlot, f32)>, MemHopError> {
    if matched.is_empty() {
        return Ok(vec![]);
    }

    let matched_ids: HashSet<u64> = matched.iter().map(|(c, _)| c.id).collect();
    let mut seen: HashSet<u64> = matched_ids.clone();
    let mut weighted_results: Vec<(ContextSlot, f32)> = Vec::new();

    let associated_nodes = l1_reverse.find_associated(&matched_ids);
    for (_node_hash, _page_ref) in associated_nodes {
        let node = match engine.read_record(_node_hash) {
            Ok(Some((rt, data))) if rt == REC_L1_SCENE_NODE => {
                match ContextNode::deserialize(data) {
                    Ok(n) => n,
                    Err(_) => continue,
                }
            }
            _ => continue,
        };
        for &edge_hash in &node.edge_ptrs {
            let hyperedge = match engine.read_record(edge_hash) {
                Ok(Some((rt, data))) if rt == REC_L1_HYPEREDGE => {
                    match HyperedgeSlot::deserialize(data) {
                        Ok(e) => e,
                        Err(_) => continue,
                    }
                }
                _ => continue,
            };
            for &sibling_hash in &hyperedge.node_ptrs {
                let sibling_node = match engine.read_record(sibling_hash) {
                    Ok(Some((rt, data))) if rt == REC_L1_SCENE_NODE => {
                        match ContextNode::deserialize(data) {
                            Ok(n) => n,
                            Err(_) => continue,
                        }
                    }
                    _ => continue,
                };
                let ctx_id = sibling_node.context_id;
                if seen.contains(&ctx_id) {
                    continue;
                }
                let ctx = match engine.read_record(ctx_id) {
                    Ok(Some((rt, data))) if rt == REC_L2_TOPIC => {
                        match bincode::deserialize::<TopicSlot>(data) {
                            Ok(c) => c,
                            Err(_) => continue,
                        }
                    }
                    _ => continue,
                };
                seen.insert(ctx_id);
                let assoc_weight = hyperedge.weight * sibling_node.importance;
                weighted_results.push((ctx, assoc_weight));
            }
        }
    }

    // Also include parent contexts of matched contexts (weight = parent importance)
    for (ctx, _) in matched {
        if let Some(parent_id) = ctx.parent_id {
            if seen.contains(&parent_id) {
                continue;
            }
            let parent = match engine.read_record(parent_id) {
                Ok(Some((rt, data))) if rt == REC_L2_TOPIC => {
                    match bincode::deserialize::<TopicSlot>(data) {
                        Ok(c) => c,
                        Err(_) => continue,
                    }
                }
                _ => continue,
            };
            seen.insert(parent_id);
            let parent_importance = 0.5;
            weighted_results.push((parent, parent_importance));
        }
    }

    weighted_results.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));

    Ok(weighted_results)
}

/// Build L1 previews for matched contexts.
///
/// For each matched L2 context, looks up its associated L1 ContextNodes via
/// the `L1ReverseIndex` and returns lightweight preview structs.
pub fn get_l1_previews(
    engine: &StorageEngine,
    matched: &[(ContextSlot, f32)],
    l1_reverse: &L1ReverseIndex,
    dialogue: &str,
) -> Result<Vec<L1Preview>, MemHopError> {
    if matched.is_empty() {
        return Ok(vec![]);
    }

    // Build a map from context_id to retrieval score for quick lookup.
    let ctx_scores: HashMap<u64, f32> = matched.iter().map(|(c, s)| (c.id, *s)).collect();
    let matched_ids: HashSet<u64> = matched.iter().map(|(c, _)| c.id).collect();

    // Tokenize the dialogue once to use as matched_keywords.
    let keywords: Vec<String> = tokenize(dialogue);

    let associated_nodes = l1_reverse.find_associated(&matched_ids);
    let mut seen_nodes = HashSet::new();
    let mut previews = Vec::new();

    for (node_hash, _page_ref) in associated_nodes {
        if !seen_nodes.insert(node_hash) {
            continue;
        }
        let node = match engine.read_record(node_hash) {
            Ok(Some((rt, data))) if rt == REC_L1_SCENE_NODE => {
                match ContextNode::deserialize(data) {
                    Ok(n) => n,
                    Err(_) => continue,
                }
            }
            _ => continue,
        };

        // Look up the associated L2 context to get summary.
        let summary = if let Ok(Some((rt, data))) = engine.read_record(node.context_id) {
            if rt == REC_L2_TOPIC {
                if let Ok(ctx) = bincode::deserialize::<TopicSlot>(data) {
                    ctx.fused_summary
                } else {
                    None
                }
            } else {
                None
            }
        } else {
            None
        };

        // Derive emotion label from valence and arousal.
        let dominant_emotion = if node.valence > 0.3 {
            if node.arousal > 0.5 {
                Some("excited".to_string())
            } else {
                Some("content".to_string())
            }
        } else if node.valence < -0.3 {
            if node.arousal > 0.5 {
                Some("distressed".to_string())
            } else {
                Some("melancholic".to_string())
            }
        } else {
            Some("neutral".to_string())
        };

        let recall_score = ctx_scores.get(&node.context_id).copied().map(|s| s as f64);

        previews.push(L1Preview {
            id: format_hash(node_hash),
            summary,
            importance: Some(node.importance as f64),
            dominant_emotion,
            matched_keywords: keywords.clone(),
            recall_score,
        });
    }

    Ok(previews)
}
