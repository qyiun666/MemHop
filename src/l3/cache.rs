// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L3 Adjacency Cache — cache graph adjacency lists to avoid repeated BTree scans.

use crate::layers::hypergraph::{GraphEdgeKind, HypergraphEdge};
use std::collections::{HashMap, HashSet};

/// Adjacency list for a single graph: node_id -> list of (edge, connected_node_ids)
pub type GraphAdjacency = HashMap<u64, Vec<(HypergraphEdge, Vec<u64>)>>;

/// Cache key for adjacency lists, combining graph_id and edge_kinds filter.
#[derive(Debug, Clone, PartialEq, Eq, Hash)]
struct CacheKey {
    graph_id: u64,
    edge_kinds: Option<Vec<GraphEdgeKind>>,
}

impl CacheKey {
    fn new(graph_id: u64, edge_kinds: Option<&[GraphEdgeKind]>) -> Self {
        Self {
            graph_id,
            edge_kinds: edge_kinds.map(|kinds| {
                let mut sorted: Vec<GraphEdgeKind> = kinds.to_vec();
                sorted.sort_by_key(|k| format!("{:?}", k));
                sorted
            }),
        }
    }
}

/// Cache for graph adjacency lists, keyed by (graph_id, edge_kinds).
///
/// This cache is invalidated when edges are added or removed from a graph.
#[derive(Debug, Clone, Default)]
pub struct AdjacencyCache {
    /// (graph_id, edge_kinds) -> adjacency list
    cache: HashMap<CacheKey, GraphAdjacency>,
    /// Track which graph_ids have cached entries for efficient invalidation
    graph_ids: HashSet<u64>,
}

impl AdjacencyCache {
    /// Create a new empty adjacency cache.
    pub fn new() -> Self {
        Self {
            cache: HashMap::new(),
            graph_ids: HashSet::new(),
        }
    }

    /// Get the cached adjacency list for a graph and edge_kinds, if available.
    pub fn get(
        &self,
        graph_id: u64,
        edge_kinds: Option<&[GraphEdgeKind]>,
    ) -> Option<&GraphAdjacency> {
        let key = CacheKey::new(graph_id, edge_kinds);
        self.cache.get(&key)
    }

    /// Insert or replace the adjacency list for a graph and edge_kinds.
    pub fn insert(
        &mut self,
        graph_id: u64,
        edge_kinds: Option<&[GraphEdgeKind]>,
        adjacency: GraphAdjacency,
    ) {
        let key = CacheKey::new(graph_id, edge_kinds);
        self.cache.insert(key, adjacency);
        self.graph_ids.insert(graph_id);
    }

    /// Invalidate all cached entries for a specific graph (e.g., after edge add/remove).
    pub fn invalidate(&mut self, graph_id: u64) {
        self.cache.retain(|k, _| k.graph_id != graph_id);
        self.graph_ids.remove(&graph_id);
    }

    /// Invalidate all cached entries (e.g., after bulk operations).
    pub fn invalidate_all(&mut self) {
        self.cache.clear();
        self.graph_ids.clear();
    }
}
