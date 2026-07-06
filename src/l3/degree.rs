// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L3 Degree Tracker — incremental node degree tracking for isolated node detection.
//! Write-path hooks are O(1); dirty-flag triggers full BTree scan rebuild on next query.

use crate::index::btree::BTreeIndex;
use crate::layers::hypergraph::{HypergraphEdge, HypergraphNode};
use crate::shared::slot_io::get_slot_data;
use crate::util::PageType;
use crate::MemHopError;
use memmap2::MmapMut;
use serde::{Deserialize, Serialize};
use std::collections::{HashMap, HashSet};

// ── Types ──────────────────────────────────────────────────────────────────

/// Per-graph storage: node hash → degree (number of hyperedges referencing it).
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct GraphDegrees {
    pub node_degrees: HashMap<u64, u32>,
}

/// Incremental degree tracker stored in-memory on `MemHop`.
///
/// Each graph (identified by `graph_id`) has an independent `GraphDegrees`
/// map. The `dirty_graphs` set tracks graphs whose in-memory degree data
/// may be stale, triggering a full-scan rebuild on the next query.
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct DegreeTracker {
    /// graph_id → per-graph degree index.
    pub per_graph: HashMap<u64, GraphDegrees>,
    /// Graphs whose degree data needs a full-scan rebuild.
    pub dirty_graphs: HashSet<u64>,
    /// Default degree threshold for "isolated" queries (0 = strict).
    pub default_threshold: u32,
}

/// The result of an isolated-node detection query.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct IsolatedResult {
    /// Hex-formatted graph ID.
    pub graph_id: String,
    /// The degree threshold used for this query.
    pub threshold: u32,
    /// Nodes whose degree ≤ threshold.
    pub isolated_nodes: Vec<IsolatedNode>,
    /// Total number of nodes in the graph.
    pub total_nodes: usize,
}

/// A single node reported as isolated (or low-degree).
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct IsolatedNode {
    /// Hex-formatted node ID.
    pub id: String,
    /// Human-readable title.
    pub title: String,
    /// Node type (concept, entity, event, etc.).
    pub node_type: String,
    /// Current degree (number of hyperedges referencing this node).
    pub degree: u32,
}

// ── DegreeTracker impl ─────────────────────────────────────────────────────

impl DegreeTracker {
    /// Create an empty tracker with strict isolation (degree ≤ 0).
    pub fn new() -> Self {
        Self::default()
    }

    /// Create a tracker with a custom default threshold.
    pub fn new_with_threshold(threshold: u32) -> Self {
        Self {
            default_threshold: threshold,
            ..Default::default()
        }
    }

    // ── Write-path hooks ────────────────────────────────────────────────

    /// Register a newly added node (initial degree = 0).
    pub fn on_node_added(&mut self, graph_id: u64, node_hash: u64) {
        self.per_graph
            .entry(graph_id)
            .or_default()
            .node_degrees
            .entry(node_hash)
            .or_insert(0);
    }

    /// Remove a deleted node from tracking.
    pub fn on_node_deleted(&mut self, graph_id: u64, node_hash: u64) {
        if let Some(degrees) = self.per_graph.get_mut(&graph_id) {
            degrees.node_degrees.remove(&node_hash);
        }
    }

    /// Increment degree for every node in a newly added edge.
    pub fn on_edge_added(&mut self, graph_id: u64, node_hashes: &[u64]) {
        let degrees = self.per_graph.entry(graph_id).or_default();
        for &node_hash in node_hashes {
            *degrees.node_degrees.entry(node_hash).or_insert(0) += 1;
        }
    }

    /// Decrement degree for every node in a removed edge.
    ///
    /// Saturates at 0 to avoid underflow in edge cases.
    pub fn on_edge_deleted(&mut self, graph_id: u64, node_hashes: &[u64]) {
        if let Some(degrees) = self.per_graph.get_mut(&graph_id) {
            for &node_hash in node_hashes {
                if let Some(deg) = degrees.node_degrees.get_mut(&node_hash) {
                    *deg = deg.saturating_sub(1);
                }
            }
        }
    }

    // ── Read-path ───────────────────────────────────────────────────────

    /// Get the degree of a single node (0 if never tracked).
    pub fn get_degree(&self, graph_id: u64, node_hash: u64) -> u32 {
        self.per_graph
            .get(&graph_id)
            .and_then(|d| d.node_degrees.get(&node_hash))
            .copied()
            .unwrap_or(0)
    }

    /// Return all node hashes whose degree ≤ threshold.
    pub fn get_low_degree_nodes(&self, graph_id: u64, threshold: u32) -> Vec<u64> {
        self.per_graph
            .get(&graph_id)
            .map(|d| {
                d.node_degrees
                    .iter()
                    .filter(|(_, &deg)| deg <= threshold)
                    .map(|(&hash, _)| hash)
                    .collect()
            })
            .unwrap_or_default()
    }

    // ── Dirty-flag management ───────────────────────────────────────────

    /// Mark a graph as dirty — next query triggers full-scan rebuild.
    pub fn mark_dirty(&mut self, graph_id: u64) {
        self.dirty_graphs.insert(graph_id);
    }

    /// Whether this graph needs a full-scan rebuild.
    pub fn is_dirty(&self, graph_id: u64) -> bool {
        self.dirty_graphs.contains(&graph_id)
    }

    /// Remove all tracking data for a graph (used before rebuild).
    pub fn clear_graph(&mut self, graph_id: u64) {
        self.per_graph.remove(&graph_id);
        self.dirty_graphs.remove(&graph_id);
    }

    /// Invalidate ALL graphs — used after dream pipeline since we cannot
    /// cheaply determine which graphs were modified.
    ///
    /// Clears all data and marks known graphs as dirty so the next query
    /// on any graph triggers a full-scan rebuild.
    pub fn invalidate_all(&mut self) {
        for gid in self.per_graph.keys() {
            self.dirty_graphs.insert(*gid);
        }
        self.per_graph.clear();
    }
}

// ── Full-scan fallback ─────────────────────────────────────────────────────

/// Rebuild the degree index for one graph by scanning the entire BTree.
///
/// This is the cold-start / dirty-graph fallback. It finds every edge page
/// belonging to `graph_id` and counts references per node, then registers
/// nodes with zero edges as degree=0.
pub fn full_scan_degrees(mmap: &MmapMut, btree: &BTreeIndex, graph_id: u64) -> GraphDegrees {
    let data: &[u8] = &mmap[..];
    let mut degrees: HashMap<u64, u32> = HashMap::new();

    for (&_id, &page_ref) in btree.iter_unsorted() {
        if super::store::page_type_of(data, page_ref) != Some(PageType::HypergraphEdge as u16) {
            continue;
        }
        if let Some(slot_data) = get_slot_data(data, page_ref) {
            if let Ok(edge) = HypergraphEdge::deserialize(slot_data) {
                if edge.graph_id != graph_id {
                    continue;
                }
                for &node_hash in &edge.node_ids {
                    *degrees.entry(node_hash).or_insert(0) += 1;
                }
            }
        }
    }

    for (&_id, &page_ref) in btree.iter_unsorted() {
        if super::store::page_type_of(data, page_ref) != Some(PageType::HypergraphNode as u16) {
            continue;
        }
        if let Some(slot_data) = get_slot_data(data, page_ref) {
            if let Ok(node) = HypergraphNode::deserialize(slot_data) {
                if node.graph_id == graph_id {
                    degrees.entry(node.id_hash).or_insert(0);
                }
            }
        }
    }

    GraphDegrees {
        node_degrees: degrees,
    }
}

// ── Public entry point ─────────────────────────────────────────────────────

/// Detect isolated (or low-degree) nodes in an L3 graph.
///
/// Dirty-graph triggers full-scan rebuild, then reports nodes with degree ≤ threshold.
///
/// # Arguments
/// * `threshold` — maximum degree to report. 0 = strictly isolated.
pub fn detect_isolated(
    mmap: &MmapMut,
    btree: &BTreeIndex,
    graph_id: u64,
    tracker: &mut DegreeTracker,
    threshold: u32,
) -> Result<IsolatedResult, MemHopError> {
    let data: &[u8] = &mmap[..];

    if tracker.is_dirty(graph_id) || !tracker.per_graph.contains_key(&graph_id) {
        let degrees = full_scan_degrees(mmap, btree, graph_id);
        tracker.per_graph.insert(graph_id, degrees);
        tracker.dirty_graphs.remove(&graph_id);
    }

    let low_degree_hashes: HashSet<u64> = tracker
        .get_low_degree_nodes(graph_id, threshold)
        .into_iter()
        .collect();

    let mut isolated_nodes = Vec::new();
    let mut total_nodes = 0usize;

    for (&_id, &page_ref) in btree.iter_unsorted() {
        if super::store::page_type_of(data, page_ref) != Some(PageType::HypergraphNode as u16) {
            continue;
        }
        if let Some(slot_data) = get_slot_data(data, page_ref) {
            if let Ok(node) = HypergraphNode::deserialize(slot_data) {
                if node.graph_id != graph_id {
                    continue;
                }
                total_nodes += 1;
                if low_degree_hashes.contains(&node.id_hash) {
                    isolated_nodes.push(IsolatedNode {
                        id: format!("{:016x}", node.id_hash),
                        title: node.title,
                        node_type: node.node_type,
                        degree: tracker.get_degree(graph_id, node.id_hash),
                    });
                }
            }
        }
    }

    Ok(IsolatedResult {
        graph_id: format!("{:016x}", graph_id),
        threshold,
        isolated_nodes,
        total_nodes,
    })
}

// ── Tests ──────────────────────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;
    use crate::test_helpers::*;

    // ── Unit tests ──────────────────────────────────────────────────────

    #[test]
    fn test_tracker_add_node() {
        let mut tracker = DegreeTracker::new();
        tracker.on_node_added(1, 101);
        assert_eq!(tracker.get_degree(1, 101), 0);
    }

    #[test]
    fn test_tracker_add_edge() {
        let mut tracker = DegreeTracker::new();
        tracker.on_node_added(1, 101);
        tracker.on_node_added(1, 102);
        tracker.on_edge_added(1, &[101, 102]);
        assert_eq!(tracker.get_degree(1, 101), 1);
        assert_eq!(tracker.get_degree(1, 102), 1);
    }

    #[test]
    fn test_tracker_hyperedge() {
        let mut tracker = DegreeTracker::new();
        tracker.on_edge_added(1, &[101, 102, 103]);
        assert_eq!(tracker.get_degree(1, 101), 1);
        assert_eq!(tracker.get_degree(1, 102), 1);
        assert_eq!(tracker.get_degree(1, 103), 1);
    }

    #[test]
    fn test_tracker_delete_edge() {
        let mut tracker = DegreeTracker::new();
        tracker.on_edge_added(1, &[101, 102]);
        tracker.on_edge_deleted(1, &[101, 102]);
        assert_eq!(tracker.get_degree(1, 101), 0);
        assert_eq!(tracker.get_degree(1, 102), 0);
    }

    #[test]
    fn test_tracker_isolated() {
        let mut tracker = DegreeTracker::new();
        tracker.on_edge_added(1, &[101, 102]);
        tracker.on_node_added(1, 103); // isolated
        let isolated = tracker.get_low_degree_nodes(1, 0);
        assert!(isolated.contains(&103));
        assert!(!isolated.contains(&101));
    }

    #[test]
    fn test_tracker_dirty() {
        let mut tracker = DegreeTracker::new();
        tracker.mark_dirty(1);
        assert!(tracker.is_dirty(1));
        tracker.clear_graph(1);
        assert!(!tracker.is_dirty(1));
    }

    #[test]
    fn test_tracker_threshold() {
        let tracker = DegreeTracker::new_with_threshold(2);
        assert_eq!(tracker.default_threshold, 2);
    }

    #[test]
    fn test_tracker_invalidate_all() {
        let mut tracker = DegreeTracker::new();
        tracker.on_edge_added(1, &[101, 102]);
        tracker.mark_dirty(2);
        tracker.invalidate_all();
        assert!(tracker.per_graph.is_empty());
        // graph 1 was known (had data), so it should be dirty
        assert!(tracker.dirty_graphs.contains(&1));
        // graph 2 was already dirty, stays dirty
        assert!(tracker.dirty_graphs.contains(&2));
        // graph 3 was never known, not dirty
        assert!(!tracker.dirty_graphs.contains(&3));
        assert_eq!(tracker.get_degree(1, 101), 0); // data gone
    }

    #[test]
    fn test_full_scan_degrees() {
        let (mut mmap, mut header, mut btree, mut file) = create_test_mmap(64);
        let graph_id = 1u64;

        crate::l3::store::add_node(
            &mut mmap,
            &mut header,
            &mut btree,
            make_node(101, graph_id, "a"),
            &mut file,
            None,
            None,
        )
        .unwrap();
        crate::l3::store::add_node(
            &mut mmap,
            &mut header,
            &mut btree,
            make_node(102, graph_id, "b"),
            &mut file,
            None,
            None,
        )
        .unwrap();
        crate::l3::store::add_node(
            &mut mmap,
            &mut header,
            &mut btree,
            make_node(103, graph_id, "isolated"),
            &mut file,
            None,
            None,
        )
        .unwrap();
        crate::l3::store::add_edge(
            &mut mmap,
            &mut header,
            &mut btree,
            make_edge(201, graph_id, vec![101, 102]),
            &mut file,
            None,
        )
        .unwrap();

        let degrees = full_scan_degrees(&mmap, &btree, graph_id);
        assert_eq!(degrees.node_degrees.get(&101), Some(&1));
        assert_eq!(degrees.node_degrees.get(&102), Some(&1));
        assert_eq!(degrees.node_degrees.get(&103), Some(&0));
    }

    #[test]
    fn test_detect_isolated_cold_start() {
        let (mut mmap, mut header, mut btree, mut file) = create_test_mmap(64);
        let graph_id = 1u64;

        crate::l3::store::add_node(
            &mut mmap,
            &mut header,
            &mut btree,
            make_node(101, graph_id, "a"),
            &mut file,
            None,
            None,
        )
        .unwrap();
        crate::l3::store::add_node(
            &mut mmap,
            &mut header,
            &mut btree,
            make_node(102, graph_id, "b"),
            &mut file,
            None,
            None,
        )
        .unwrap();
        crate::l3::store::add_node(
            &mut mmap,
            &mut header,
            &mut btree,
            make_node(103, graph_id, "isolated"),
            &mut file,
            None,
            None,
        )
        .unwrap();
        crate::l3::store::add_edge(
            &mut mmap,
            &mut header,
            &mut btree,
            make_edge(201, graph_id, vec![101, 102]),
            &mut file,
            None,
        )
        .unwrap();

        let mut tracker = DegreeTracker::new();
        tracker.mark_dirty(graph_id); // simulate cold start

        let result = detect_isolated(&mmap, &btree, graph_id, &mut tracker, 0).unwrap();
        assert_eq!(result.isolated_nodes.len(), 1);
        assert_eq!(result.isolated_nodes[0].id, format!("{:016x}", 103));
        assert_eq!(result.total_nodes, 3);
        assert!(!tracker.is_dirty(graph_id)); // dirty flag cleared
    }

    #[test]
    fn test_detect_isolated_multiple_graphs() {
        // Two independent graphs — isolation should not leak across graphs
        let (mut mmap, mut header, mut btree, mut file) = create_test_mmap(128);
        let g1 = 1u64;
        let g2 = 2u64;

        // Graph 1: two connected nodes
        crate::l3::store::add_node(
            &mut mmap,
            &mut header,
            &mut btree,
            make_node(101, g1, "a"),
            &mut file,
            None,
            None,
        )
        .unwrap();
        crate::l3::store::add_node(
            &mut mmap,
            &mut header,
            &mut btree,
            make_node(102, g1, "b"),
            &mut file,
            None,
            None,
        )
        .unwrap();
        crate::l3::store::add_edge(
            &mut mmap,
            &mut header,
            &mut btree,
            make_edge(201, g1, vec![101, 102]),
            &mut file,
            None,
        )
        .unwrap();

        // Graph 2: one isolated node
        crate::l3::store::add_node(
            &mut mmap,
            &mut header,
            &mut btree,
            make_node(301, g2, "x"),
            &mut file,
            None,
            None,
        )
        .unwrap();

        let mut tracker = DegreeTracker::new();

        // Graph 1 should have 0 isolated
        let r1 = detect_isolated(&mmap, &btree, g1, &mut tracker, 0).unwrap();
        assert_eq!(r1.isolated_nodes.len(), 0);

        // Graph 2 should have 1 isolated
        let r2 = detect_isolated(&mmap, &btree, g2, &mut tracker, 0).unwrap();
        assert_eq!(r2.isolated_nodes.len(), 1);
    }
}
