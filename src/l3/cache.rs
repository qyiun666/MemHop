// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L3 Adjacency Cache — cache graph adjacency lists to avoid repeated BTree scans.

use crate::layers::hypergraph::{GraphEdgeKind, HypergraphEdge};
use std::collections::{HashMap, HashSet};
use std::sync::Arc;
use std::time::Instant;

/// Adjacency list for a single graph: node_id -> list of (edge, connected_node_ids).
/// Wrapped in `Arc` so cached copies are cheap to clone.
pub type GraphAdjacency = Arc<HashMap<u64, Vec<(HypergraphEdge, Vec<u64>)>>>;

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

#[derive(Debug, Clone)]
struct CacheEntry {
    adjacency: GraphAdjacency,
    last_accessed: Instant,
}

/// Cache for graph adjacency lists, keyed by (graph_id, edge_kinds).
///
/// This cache is invalidated when edges are added or removed from a graph.
#[derive(Debug, Clone)]
pub struct AdjacencyCache {
    /// (graph_id, edge_kinds) -> adjacency list + access metadata
    cache: HashMap<CacheKey, CacheEntry>,
    /// Track which graph_ids have cached entries for efficient invalidation
    graph_ids: HashSet<u64>,
    /// Maximum number of entries before LRU eviction.
    max_entries: usize,
}

impl Default for AdjacencyCache {
    fn default() -> Self {
        Self::new()
    }
}

impl AdjacencyCache {
    /// Create a new empty adjacency cache with the default capacity (128).
    pub fn new() -> Self {
        Self::with_capacity(128)
    }

    /// Create a new adjacency cache with the specified capacity.
    pub fn with_capacity(max_entries: usize) -> Self {
        Self {
            cache: HashMap::new(),
            graph_ids: HashSet::new(),
            max_entries,
        }
    }

    /// Get the cached adjacency list for a graph and edge_kinds, if available.
    pub fn get(
        &mut self,
        graph_id: u64,
        edge_kinds: Option<&[GraphEdgeKind]>,
    ) -> Option<&GraphAdjacency> {
        let key = CacheKey::new(graph_id, edge_kinds);
        if let Some(entry) = self.cache.get_mut(&key) {
            entry.last_accessed = Instant::now();
            Some(&entry.adjacency)
        } else {
            None
        }
    }

    /// Insert or replace the adjacency list for a graph and edge_kinds.
    pub fn insert(
        &mut self,
        graph_id: u64,
        edge_kinds: Option<&[GraphEdgeKind]>,
        adjacency: GraphAdjacency,
    ) {
        let key = CacheKey::new(graph_id, edge_kinds);
        if !self.cache.contains_key(&key) && self.cache.len() >= self.max_entries {
            self.evict_lru();
        }
        self.cache.insert(
            key,
            CacheEntry {
                adjacency,
                last_accessed: Instant::now(),
            },
        );
        self.graph_ids.insert(graph_id);
    }

    fn evict_lru(&mut self) {
        if let Some(oldest_key) = self
            .cache
            .iter()
            .min_by_key(|(_, entry)| entry.last_accessed)
            .map(|(key, _)| key.clone())
        {
            let graph_id = oldest_key.graph_id;
            self.cache.remove(&oldest_key);
            if !self.cache.iter().any(|(key, _)| key.graph_id == graph_id) {
                self.graph_ids.remove(&graph_id);
            }
        }
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
