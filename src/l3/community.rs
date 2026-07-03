// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L3 Community Detection — Leiden algorithm on hypergraphs via clique expansion.
//! V1: hyperedge→binary edge reduction (weight/(k-1)), then Leiden. V2: h-Louvain (arXiv:2406.17556).

use crate::index::btree::BTreeIndex;
use crate::layers::hypergraph::{HypergraphEdge, HypergraphNode};
use crate::shared::slot_io::get_slot_data;
use crate::util::PageType;
use crate::MemHopError;
use memmap2::MmapMut;
use serde::{Deserialize, Serialize};
use std::collections::{HashMap, HashSet};

// ── Config ─────────────────────────────────────────────────────────────────

/// Configuration for community detection.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CommunityConfig {
    /// Maximum number of nodes in a hyperedge before it is skipped during
    /// clique expansion. Higher values increase binary edge count quadratically.
    #[serde(default = "default_max_hyperedge_size")]
    pub max_hyperedge_size: usize,
}

fn default_max_hyperedge_size() -> usize {
    10
}

impl Default for CommunityConfig {
    fn default() -> Self {
        Self {
            max_hyperedge_size: default_max_hyperedge_size(),
        }
    }
}

// ── Result types ───────────────────────────────────────────────────────────

/// The result of running community detection on a graph.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CommunityResult {
    /// Hex-formatted graph ID.
    pub graph_id: String,
    /// Detected communities, sorted by size descending.
    pub communities: Vec<Community>,
    /// Modularity score (Leiden quality). Higher = better partition.
    pub modularity: f64,
    /// Total number of nodes in the graph.
    pub total_nodes: usize,
    /// Number of communities detected.
    pub total_communities: usize,
}

/// A single detected community (cluster of related nodes).
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Community {
    /// Community ID (arbitrary, assigned by Leiden).
    pub id: u32,
    /// Hex-formatted node IDs belonging to this community.
    pub node_ids: Vec<String>,
    /// Number of nodes in this community.
    pub size: usize,
}

// ── Hyperedge reduction ────────────────────────────────────────────────────

/// Reduce hyperedges to weighted binary edges via clique expansion.
///
/// For each hyperedge with nodes `[n1, n2, ..., nk]`:
/// - If `k < 2`: skipped (degenerate edge).
/// - If `k > max_size`: skipped with a warning (high-degree edge).
/// - Otherwise: all C(k,2) unordered pairs are added as binary edges,
///   with weight = `edge.weight / (k-1)` (divided to avoid overweighting
///   nodes in large hyperedges).
///
/// Duplicate pairs from different hyperedges have their weights summed.
fn reduce_hyperedges(edges: &[HypergraphEdge], max_size: usize) -> Vec<(u64, u64, f64)> {
    let mut edge_weights: HashMap<(u64, u64), f64> = HashMap::new();

    for edge in edges {
        let nodes = &edge.node_ids;
        let k = nodes.len();

        if k < 2 {
            continue;
        }
        if k > max_size {
            tracing::warn!(
                "hyperedge {} has {} nodes (max={}), skipping in community detection",
                edge.id_hash,
                k,
                max_size
            );
            continue;
        }

        // Clique expansion weight: divide by (k-1) so each node's total
        // incident weight is roughly proportional to its original hyperedge count.
        let pair_weight = edge.weight as f64 / (k - 1) as f64;

        for i in 0..k {
            for j in (i + 1)..k {
                let (a, b) = if nodes[i] < nodes[j] {
                    (nodes[i], nodes[j])
                } else {
                    (nodes[j], nodes[i])
                };
                *edge_weights.entry((a, b)).or_insert(0.0) += pair_weight;
            }
        }
    }

    edge_weights
        .into_iter()
        .map(|((a, b), w)| (a, b, w))
        .collect()
}

// ── Community detection entry point ────────────────────────────────────────

/// Run Leiden community detection on an L3 graph.
///
/// Runs clique expansion → Leiden → community mapping.
pub fn run_community_detection(
    mmap: &MmapMut,
    btree: &BTreeIndex,
    graph_id: u64,
    config: &CommunityConfig,
) -> Result<CommunityResult, MemHopError> {
    let data: &[u8] = &mmap[..];

    let mut edges: Vec<HypergraphEdge> = Vec::new();
    for (&_id, &page_ref) in btree.iter() {
        if super::store::page_type_of(data, page_ref) != Some(PageType::HypergraphEdge as u16) {
            continue;
        }
        if let Some(slot_data) = get_slot_data(data, page_ref) {
            if let Ok(edge) = HypergraphEdge::deserialize(slot_data) {
                if edge.graph_id == graph_id {
                    edges.push(edge);
                }
            }
        }
    }

    let binary_edges = reduce_hyperedges(&edges, config.max_hyperedge_size);

    let mut node_set: HashSet<u64> = HashSet::new();
    for &(a, b, _) in &binary_edges {
        node_set.insert(a);
        node_set.insert(b);
    }
    // Also include nodes with no edges (they form singleton communities)
    for (&_id, &page_ref) in btree.iter() {
        if super::store::page_type_of(data, page_ref) != Some(PageType::HypergraphNode as u16) {
            continue;
        }
        if let Some(slot_data) = get_slot_data(data, page_ref) {
            if let Ok(node) = HypergraphNode::deserialize(slot_data) {
                if node.graph_id == graph_id {
                    node_set.insert(node.id_hash);
                }
            }
        }
    }

    let node_hashes: Vec<u64> = node_set.into_iter().collect();
    let node_count = node_hashes.len();

    if node_count == 0 {
        return Ok(CommunityResult {
            graph_id: format!("{:016x}", graph_id),
            communities: Vec::new(),
            modularity: 0.0,
            total_nodes: 0,
            total_communities: 0,
        });
    }

    let hash_to_idx: HashMap<u64, usize> = node_hashes
        .iter()
        .enumerate()
        .map(|(i, &h)| (h, i))
        .collect();

    let mut builder = leiden_rs::GraphDataBuilder::new(node_count);
    for (a, b, weight) in &binary_edges {
        let idx_a = hash_to_idx[a];
        let idx_b = hash_to_idx[b];
        builder
            .add_edge(idx_a, idx_b, *weight)
            .map_err(|e| MemHopError::Serialization(e.to_string()))?;
    }
    let graph = builder
        .build()
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;

    let leiden = leiden_rs::Leiden::new(leiden_rs::LeidenConfig::default());
    let result = leiden
        .run(&graph)
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;

    let mut communities: Vec<Community> = result
        .partition
        .communities()
        .into_iter()
        .map(|(comm_id, node_indices)| {
            let node_ids: Vec<String> = node_indices
                .iter()
                .map(|&idx| format!("{:016x}", node_hashes[idx]))
                .collect();
            let size = node_ids.len();
            Community {
                id: comm_id as u32,
                node_ids,
                size,
            }
        })
        .collect();

    // Sort for deterministic output
    communities.sort_by_key(|c| std::cmp::Reverse(c.size));

    let total_communities = communities.len();

    Ok(CommunityResult {
        graph_id: format!("{:016x}", graph_id),
        communities,
        modularity: result.quality,
        total_nodes: node_count,
        total_communities,
    })
}

// ── Tests ──────────────────────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;
    use crate::test_helpers::*;

    #[test]
    fn test_reduce_binary_edge() {
        let edges = vec![make_edge(1, 100, vec![10, 20])];
        let binary = reduce_hyperedges(&edges, 10);
        assert_eq!(binary.len(), 1);
        assert_eq!(binary[0].0, 10);
        assert_eq!(binary[0].1, 20);
    }

    #[test]
    fn test_reduce_hyperedge_clique() {
        // 3-node hyperedge → 3 binary edges (clique)
        let edges = vec![make_edge(1, 100, vec![10, 20, 30])];
        let binary = reduce_hyperedges(&edges, 10);
        assert_eq!(binary.len(), 3);
    }

    #[test]
    fn test_reduce_truncate_large_hyperedge() {
        let nodes: Vec<u64> = (0..15).collect();
        let edges = vec![make_edge(1, 100, nodes)];
        let binary = reduce_hyperedges(&edges, 10);
        assert_eq!(binary.len(), 0); // truncated — 15 > max_size(10)
    }

    #[test]
    fn test_reduce_dedup_weights() {
        let edges = vec![
            make_edge(1, 100, vec![10, 20]),
            make_edge(2, 100, vec![10, 20]),
        ];
        let binary = reduce_hyperedges(&edges, 10);
        assert_eq!(binary.len(), 1);
        // weight = 1.0/(2-1) + 1.0/(2-1) = 2.0
        assert!((binary[0].2 - 2.0).abs() < 1e-6);
    }
}
