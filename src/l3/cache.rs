//! L3 Adjacency Cache
//!
//! Provides caching for graph adjacency lists to avoid repeated BTree scans
//! during graph_query operations.

use crate::slot::hypergraph::{GraphEdgeKind, HypergraphEdge};
use std::collections::HashMap;

/// Adjacency list for a single graph: node_id -> list of (edge, connected_node_ids)
pub type GraphAdjacency = HashMap<u64, Vec<(HypergraphEdge, Vec<u64>)>>;

/// Cache for graph adjacency lists, keyed by graph_id.
///
/// This cache is invalidated when edges are added or removed from a graph.
#[derive(Debug, Clone, Default)]
pub struct AdjacencyCache {
    /// graph_id -> adjacency list
    cache: HashMap<u64, GraphAdjacency>,
}

impl AdjacencyCache {
    /// Create a new empty adjacency cache.
    pub fn new() -> Self {
        Self {
            cache: HashMap::new(),
        }
    }

    /// Check if the cache has an entry for the given graph_id.
    pub fn contains_graph(&self, graph_id: u64) -> bool {
        self.cache.contains_key(&graph_id)
    }

    /// Get the cached adjacency list for a graph, if available.
    pub fn get(&self, graph_id: u64) -> Option<&GraphAdjacency> {
        self.cache.get(&graph_id)
    }

    /// Insert or replace the adjacency list for a graph.
    pub fn insert(&mut self, graph_id: u64, adjacency: GraphAdjacency) {
        self.cache.insert(graph_id, adjacency);
    }

    /// Invalidate the cache for a specific graph (e.g., after edge add/remove).
    pub fn invalidate(&mut self, graph_id: u64) {
        self.cache.remove(&graph_id);
    }

    /// Invalidate all cached graphs (e.g., after bulk operations).
    pub fn invalidate_all(&mut self) {
        self.cache.clear();
    }

    /// Incrementally update the cache when a new edge is added.
    ///
    /// If the graph is not cached, this is a no-op (will be built on next query).
    pub fn on_edge_added(&mut self, edge: &HypergraphEdge) {
        if let Some(adjacency) = self.cache.get_mut(&edge.graph_id) {
            let other_ids: Vec<u64> = edge.node_ids.to_vec();
            for &node_id in &edge.node_ids {
                adjacency
                    .entry(node_id)
                    .or_default()
                    .push((edge.clone(), other_ids.clone()));
            }
        }
    }

    /// Incrementally update the cache when an edge is removed.
    ///
    /// If the graph is not cached, this is a no-op.
    pub fn on_edge_removed(&mut self, graph_id: u64, edge_id: u64) {
        if let Some(adjacency) = self.cache.get_mut(&graph_id) {
            // Remove the edge from all node entries
            for edges in adjacency.values_mut() {
                edges.retain(|(e, _)| e.id_hash != edge_id);
            }
            // Remove empty entries
            adjacency.retain(|_, edges| !edges.is_empty());
        }
    }
}
