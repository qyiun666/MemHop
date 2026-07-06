// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L1 associated-context lookup: finds L2 contexts related to matched contexts
//! via the L1 hypergraph (ContextNode + Hyperedge traversal).

use crate::index::btree::BTreeIndex;
use crate::layers::context::ContextSlot;
use crate::layers::context_node::ContextNode;
use crate::layers::hyperedge::HyperedgeSlot;
use crate::query::search::L1ReverseIndex;
use crate::shared::slot_io::get_slot_data;
use crate::MemHopError;
use std::collections::HashSet;

/// Via L1 hypergraph, find associated L2 contexts for matched contexts.
///
/// Uses L1 reverse index to find ContextNodes, traverses hyperedges to
/// discover sibling nodes, then loads their associated L2 ContextSlots.
/// Also includes parent contexts of matched contexts.
pub fn get_l1_associated_contexts(
    data: &[u8],
    matched: &[(ContextSlot, f32)],
    btree: &BTreeIndex,
    l1_reverse: &L1ReverseIndex,
) -> Result<Vec<(ContextSlot, f32)>, MemHopError> {
    if matched.is_empty() {
        return Ok(vec![]);
    }

    let matched_ids: HashSet<u64> = matched.iter().map(|(c, _)| c.id_hash).collect();
    let mut seen: HashSet<u64> = matched_ids.clone();
    let mut weighted_results: Vec<(ContextSlot, f32)> = Vec::new();

    let associated_nodes = l1_reverse.find_associated(&matched_ids);
    for (_node_hash, page_ref) in associated_nodes {
        if let Some(slot_data) = get_slot_data(data, page_ref) {
            if let Ok(node) = ContextNode::deserialize(slot_data) {
                for &edge_hash in &node.edge_ptrs {
                    if let Some(edge_data) = btree
                        .search(edge_hash)
                        .and_then(|pr| get_slot_data(data, pr))
                    {
                        if let Ok(hyperedge) = HyperedgeSlot::deserialize(edge_data) {
                            for &sibling_hash in &hyperedge.node_ptrs {
                                if let Some(sib_data) = btree
                                    .search(sibling_hash)
                                    .and_then(|pr| get_slot_data(data, pr))
                                {
                                    if let Ok(sibling_node) = ContextNode::deserialize(sib_data) {
                                        let ctx_id = sibling_node.context_id;
                                        if seen.contains(&ctx_id) {
                                            continue;
                                        }
                                        if let Some(ctx_data) = btree
                                            .search(ctx_id)
                                            .and_then(|pr| get_slot_data(data, pr))
                                        {
                                            if let Ok(ctx) = ContextSlot::deserialize(ctx_data) {
                                                seen.insert(ctx_id);
                                                let assoc_weight =
                                                    hyperedge.weight * sibling_node.importance;
                                                weighted_results.push((ctx, assoc_weight));
                                            }
                                        }
                                    }
                                }
                            }
                        }
                    }
                }
            }
        }
    }

    // Also include parent contexts of matched contexts (weight = parent importance)
    for (ctx, _) in matched {
        if let Some(parent_id) = ctx.parent_id {
            if seen.contains(&parent_id) {
                continue;
            }
            if let Some(parent_data) = btree
                .search(parent_id)
                .and_then(|pr| get_slot_data(data, pr))
            {
                if let Ok(parent) = ContextSlot::deserialize(parent_data) {
                    seen.insert(parent_id);
                    let parent_importance = parent.importance;
                    weighted_results.push((parent, parent_importance));
                }
            }
        }
    }

    weighted_results.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));

    Ok(weighted_results)
}
