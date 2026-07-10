// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L3 Hypergraph CRUD — pure data operations.
//!
//! Includes Node/Edge CRUD, graph management, traversal, and the new
//! update_node/update_edge/invalidate_edge/get_neighbors/find_path operations.
//!
//! v0.57.0 重构：所有存储 I/O 改为 StorageEngine 接口。

use crate::layers::context::TopicSlot;
use crate::layers::hypergraph::{GraphEdge, GraphEdgeKind, GraphNode};
use crate::query::types::{
    EdgeListQuery, EdgeListResult, NodeListQuery, NodeListResult, TraversalHop,
};
use crate::shared::common::{
    format_hash, has_more, matches_keyword, now_ms, pagination_params, parse_id_to_hash,
};
use crate::storage::record::{REC_L3_GRAPH_EDGE, REC_L3_GRAPH_NODE};
use crate::storage::StorageEngine;
use crate::store::{delete_slot, read_slot, write_slot};
use crate::MemHopError;
use std::collections::{HashMap, HashSet, VecDeque};

// ============================================================================
// Update types for new operations
// ============================================================================

/// Fields that can be updated on a GraphNode.
pub struct NodeUpdateFields {
    pub title: Option<String>,
    pub content: Option<String>,
    pub summary: Option<String>,
    pub keywords: Option<Vec<String>>,
    pub importance: Option<f32>,
    pub valid_until: Option<i64>,
}

/// Fields that can be updated on a GraphEdge.
pub struct EdgeUpdateFields {
    pub weight: Option<f32>,
    pub label: Option<String>,
    pub description: Option<String>,
    pub confidence: Option<f32>,
    pub valid_until: Option<i64>,
}

/// Result of a neighbor lookup.
pub struct NeighborResult {
    pub nodes: Vec<GraphNode>,
    pub edges: Vec<GraphEdge>,
}

/// Result of a path-finding query.
pub struct PathResult {
    pub nodes: Vec<GraphNode>,
    pub edges: Vec<GraphEdge>,
}

// ============================================================================
// Node CRUD
// ============================================================================

/// Add a GraphNode to the graph and write to storage.
/// Returns the hex-formatted node ID string.
pub fn add_node(
    engine: &mut StorageEngine,
    mut node: GraphNode,
    tracker: Option<&mut crate::l3::DegreeTracker>,
    index_map: Option<&mut HashMap<u64, crate::l3::L3Index>>,
) -> Result<String, MemHopError> {
    if node.content.len() > 200 {
        node.content = node.content.chars().take(200).collect();
    }

    write_slot(engine, REC_L3_GRAPH_NODE, node.id_hash, &node)?;

    if let Some(tracker) = tracker {
        tracker.on_node_added(node.graph_id, node.id_hash);
    }
    if let Some(index_map) = index_map {
        index_map.entry(node.graph_id).or_default().add_node(&node);
    }

    Ok(format_hash(node.id_hash))
}

/// Delete a GraphNode and cascade-delete all edges that reference it.
pub fn delete_node(
    engine: &mut StorageEngine,
    node_id: &str,
    tracker: Option<&mut crate::l3::DegreeTracker>,
    index_map: Option<&mut HashMap<u64, crate::l3::L3Index>>,
) -> Result<(), MemHopError> {
    let id_hash = parse_id_to_hash(node_id);

    let node: GraphNode = match read_slot(engine, id_hash)? {
        Some(n) => n,
        None => return Ok(()),
    };

    let graph_id = node.graph_id;

    // Find all edges referencing this node.
    let mut edges_to_delete: Vec<u64> = Vec::new();
    for (&id_hash, &_offset) in engine.iter_index() {
        match engine.read_record(id_hash) {
            Ok(Some((record_type, data))) => {
                if record_type != REC_L3_GRAPH_EDGE {
                    continue;
                }
                if let Ok(edge) = bincode::deserialize::<GraphEdge>(data) {
                    if edge.graph_id == graph_id && edge.node_ids.contains(&id_hash) {
                        edges_to_delete.push(id_hash);
                    }
                }
            }
            _ => continue,
        }
    }

    for edge_hash in &edges_to_delete {
        delete_slot(engine, *edge_hash)?;
    }

    if let Some(tracker) = tracker {
        tracker.on_node_deleted(graph_id, id_hash);
    }
    if let Some(index_map) = index_map {
        if let Some(index) = index_map.get_mut(&graph_id) {
            index.remove_node(id_hash, &node);
        }
    }

    delete_slot(engine, id_hash)?;
    Ok(())
}

// ============================================================================
// Edge CRUD
// ============================================================================

/// Add a GraphEdge to the graph and write to storage.
pub fn add_edge(
    engine: &mut StorageEngine,
    edge: GraphEdge,
    tracker: Option<&mut crate::l3::DegreeTracker>,
) -> Result<String, MemHopError> {
    write_slot(engine, REC_L3_GRAPH_EDGE, edge.id_hash, &edge)?;

    if let Some(tracker) = tracker {
        tracker.on_edge_added(edge.graph_id, &edge.node_ids);
    }

    Ok(format_hash(edge.id_hash))
}

// ============================================================================
// Graph-level operations
// ============================================================================

/// List all GraphNodes belonging to a specific graph, with pagination and filtering.
pub fn list_nodes_by_graph(
    engine: &StorageEngine,
    graph_id: u64,
    query: &NodeListQuery,
) -> Result<NodeListResult, MemHopError> {
    let mut all_nodes: Vec<GraphNode> = Vec::new();

    for (&id_hash, &_offset) in engine.iter_index() {
        match engine.read_record(id_hash) {
            Ok(Some((record_type, data))) => {
                if record_type != REC_L3_GRAPH_NODE {
                    continue;
                }
                if let Ok(node) = bincode::deserialize::<GraphNode>(data) {
                    if node.graph_id != graph_id {
                        continue;
                    }
                    if let Some(ref nt) = query.node_type {
                        if &node.node_type != nt {
                            continue;
                        }
                    }
                    if let Some(ref keyword) = query.keyword {
                        let combined = format!("{} {}", node.title, node.content);
                        if !matches_keyword(&combined, keyword) {
                            continue;
                        }
                    }
                    if let Some(min_imp) = query.min_importance {
                        if node.importance < min_imp {
                            continue;
                        }
                    }
                    all_nodes.push(node);
                }
            }
            _ => continue,
        }
    }

    all_nodes.sort_by(|a, b| {
        b.importance
            .partial_cmp(&a.importance)
            .unwrap_or(std::cmp::Ordering::Equal)
    });

    let (skip, take) = pagination_params(query.page, query.page_size);
    let total = all_nodes.len();
    let items: Vec<crate::query::types::GraphNode> = all_nodes
        .into_iter()
        .skip(skip)
        .take(take)
        .map(Into::into)
        .collect();

    Ok(NodeListResult {
        items,
        total,
        page: query.page,
        page_size: query.page_size,
        has_more: has_more(skip, take, total),
    })
}

/// List all GraphEdges belonging to a specific graph, with pagination and filtering.
pub fn list_edges_by_graph(
    engine: &StorageEngine,
    graph_id: u64,
    query: &EdgeListQuery,
) -> Result<EdgeListResult, MemHopError> {
    let mut all_edges: Vec<GraphEdge> = Vec::new();

    for (&id_hash, &_offset) in engine.iter_index() {
        match engine.read_record(id_hash) {
            Ok(Some((record_type, data))) => {
                if record_type != REC_L3_GRAPH_EDGE {
                    continue;
                }
                if let Ok(edge) = bincode::deserialize::<GraphEdge>(data) {
                    if edge.graph_id != graph_id {
                        continue;
                    }
                    if let Some(kind) = query.kind {
                        if edge.kind != kind {
                            continue;
                        }
                    }
                    if let Some(ref nid) = query.node_id {
                        let node_hash = parse_id_to_hash(nid);
                        if !edge.node_ids.contains(&node_hash) {
                            continue;
                        }
                    }
                    all_edges.push(edge);
                }
            }
            _ => continue,
        }
    }

    all_edges.sort_by_key(|e| std::cmp::Reverse(e.created_at));

    let (skip, take) = pagination_params(query.page, query.page_size);
    let total = all_edges.len();
    let items: Vec<crate::query::types::GraphEdge> = all_edges
        .into_iter()
        .skip(skip)
        .take(take)
        .map(Into::into)
        .collect();

    Ok(EdgeListResult {
        items,
        total,
        page: query.page,
        page_size: query.page_size,
        has_more: has_more(skip, take, total),
    })
}

/// Delete an entire L3 graph: cascade-deletes nodes, edges, and GraphSlot.
pub fn delete_graph(engine: &mut StorageEngine, l3_id: &str) -> Result<(), MemHopError> {
    let graph_hash = parse_id_to_hash(l3_id);

    if engine.read_record(graph_hash)?.is_none() {
        return Ok(());
    }

    let mut node_hashes: Vec<u64> = Vec::new();
    let mut edge_hashes: Vec<u64> = Vec::new();

    for (&id_hash, &_offset) in engine.iter_index() {
        match engine.read_record(id_hash) {
            Ok(Some((record_type, data))) => {
                if record_type == REC_L3_GRAPH_NODE {
                    if let Ok(node) = bincode::deserialize::<GraphNode>(data) {
                        if node.graph_id == graph_hash {
                            node_hashes.push(id_hash);
                        }
                    }
                } else if record_type == REC_L3_GRAPH_EDGE {
                    if let Ok(edge) = bincode::deserialize::<GraphEdge>(data) {
                        if edge.graph_id == graph_hash {
                            edge_hashes.push(id_hash);
                        }
                    }
                }
            }
            _ => continue,
        }
    }

    for edge_hash in &edge_hashes {
        delete_slot(engine, *edge_hash)?;
    }

    for node_hash in &node_hashes {
        delete_slot(engine, *node_hash)?;
    }

    delete_slot(engine, graph_hash)?;
    Ok(())
}

// ============================================================================
// BFS Traversal
// ============================================================================

fn build_adjacency_index(
    engine: &StorageEngine,
    graph_id: u64,
    edge_kinds: Option<&[GraphEdgeKind]>,
) -> crate::l3::GraphAdjacency {
    let mut adjacency: HashMap<u64, Vec<(GraphEdge, Vec<u64>)>> = HashMap::new();
    for (&id_hash, &_offset) in engine.iter_index() {
        match engine.read_record(id_hash) {
            Ok(Some((record_type, data))) => {
                if record_type != REC_L3_GRAPH_EDGE {
                    continue;
                }
                if let Ok(edge) = bincode::deserialize::<GraphEdge>(data) {
                    if edge.graph_id != graph_id {
                        continue;
                    }
                    if let Some(kinds) = edge_kinds {
                        if !kinds.contains(&edge.kind) {
                            continue;
                        }
                    }
                    let other_ids: Vec<u64> = edge.node_ids.to_vec();
                    for &node_id in &edge.node_ids {
                        adjacency
                            .entry(node_id)
                            .or_default()
                            .push((edge.clone(), other_ids.clone()));
                    }
                }
            }
            _ => continue,
        }
    }
    std::sync::Arc::new(adjacency)
}

fn bfs_with_adjacency(
    adjacency: &HashMap<u64, Vec<(GraphEdge, Vec<u64>)>>,
    start_node: u64,
    max_depth: usize,
) -> Vec<TraversalHop> {
    let mut hops = Vec::new();
    let mut node_depth: HashMap<u64, usize> = HashMap::new();
    let mut visited_edges: HashSet<u64> = HashSet::new();
    let mut queue: VecDeque<(u64, usize)> = VecDeque::new();

    node_depth.insert(start_node, 0);
    queue.push_back((start_node, 0));

    while let Some((current_node, current_depth)) = queue.pop_front() {
        if current_depth >= max_depth {
            continue;
        }

        if let Some(edges) = adjacency.get(&current_node) {
            for (edge, other_ids) in edges {
                if !visited_edges.insert(edge.id_hash) {
                    continue;
                }

                let hop_depth = current_depth + 1;
                for &to_node in other_ids {
                    if to_node == current_node {
                        continue;
                    }

                    if let std::collections::hash_map::Entry::Vacant(e) = node_depth.entry(to_node)
                    {
                        e.insert(hop_depth);
                        queue.push_back((to_node, hop_depth));
                    }

                    hops.push(TraversalHop {
                        depth: hop_depth,
                        from_node: current_node,
                        edge: edge.clone().into(),
                        to_node,
                    });
                }
            }
        }
    }

    hops
}

/// BFS traversal with adjacency cache support.
pub fn bfs_traversal_cached(
    engine: &StorageEngine,
    graph_id: u64,
    start_node: u64,
    max_depth: usize,
    edge_kinds: Option<&[GraphEdgeKind]>,
    cache: &mut crate::l3::AdjacencyCache,
) -> Result<Vec<TraversalHop>, MemHopError> {
    if max_depth == 0 {
        return Ok(Vec::new());
    }

    let adjacency = if let Some(cached) = cache.get(graph_id, edge_kinds) {
        cached.clone()
    } else {
        let adjacency = build_adjacency_index(engine, graph_id, edge_kinds);
        cache.insert(graph_id, edge_kinds, adjacency.clone());
        adjacency
    };

    Ok(bfs_with_adjacency(
        adjacency.as_ref(),
        start_node,
        max_depth,
    ))
}

// ============================================================================
// NEW: Update operations
// ============================================================================

/// Partially update a GraphNode's fields in-place.
pub fn update_node(
    engine: &mut StorageEngine,
    node_id: u64,
    updates: NodeUpdateFields,
) -> Result<(), MemHopError> {
    let mut node: GraphNode = match read_slot(engine, node_id)? {
        Some(n) => n,
        None => return Err(MemHopError::NotFound(format!("node {}", node_id))),
    };

    if let Some(title) = updates.title {
        node.title = title;
    }
    if let Some(content) = updates.content {
        node.content = content;
    }
    if let Some(summary) = updates.summary {
        node.summary = Some(summary);
    }
    if let Some(keywords) = updates.keywords {
        node.keywords = keywords;
    }
    if let Some(importance) = updates.importance {
        node.importance = importance;
    }
    if let Some(valid_until) = updates.valid_until {
        node.valid_until = valid_until;
    }

    node.updated_at = now_ms();
    write_slot(engine, REC_L3_GRAPH_NODE, node_id, &node)?;
    Ok(())
}

/// Partially update a GraphEdge's fields in-place.
pub fn update_edge(
    engine: &mut StorageEngine,
    edge_id: u64,
    updates: EdgeUpdateFields,
) -> Result<(), MemHopError> {
    let mut edge: GraphEdge = match read_slot(engine, edge_id)? {
        Some(e) => e,
        None => return Err(MemHopError::NotFound(format!("edge {}", edge_id))),
    };

    if let Some(weight) = updates.weight {
        edge.weight = weight;
    }
    if let Some(label) = updates.label {
        edge.label = Some(label);
    }
    if let Some(description) = updates.description {
        edge.description = Some(description);
    }
    if let Some(confidence) = updates.confidence {
        edge.confidence = confidence;
    }
    if let Some(valid_until) = updates.valid_until {
        edge.valid_until = valid_until;
    }

    write_slot(engine, REC_L3_GRAPH_EDGE, edge_id, &edge)?;
    Ok(())
}

/// Set an edge's valid_until to now (soft-delete).
pub fn invalidate_edge(
    engine: &mut StorageEngine,
    edge_id: u64,
    now: i64,
) -> Result<(), MemHopError> {
    update_edge(
        engine,
        edge_id,
        EdgeUpdateFields {
            weight: None,
            label: None,
            description: None,
            confidence: None,
            valid_until: Some(now),
        },
    )
}

/// Get 1-hop neighbors of a node within a specific graph, with optional edge kind filtering.
pub fn get_neighbors(
    engine: &StorageEngine,
    graph_id: u64,
    node_id: u64,
    edge_kinds: Option<&[GraphEdgeKind]>,
    only_valid: bool,
) -> Result<NeighborResult, MemHopError> {
    let now = now_ms();
    let mut neighbor_ids = HashSet::new();
    let mut edges = Vec::new();

    for (&id_hash, &_offset) in engine.iter_index() {
        match engine.read_record(id_hash) {
            Ok(Some((record_type, data))) => {
                if record_type != REC_L3_GRAPH_EDGE {
                    continue;
                }
                if let Ok(edge) = bincode::deserialize::<GraphEdge>(data) {
                    if edge.graph_id != graph_id {
                        continue;
                    }
                    if !edge.node_ids.contains(&node_id) {
                        continue;
                    }
                    if let Some(kinds) = edge_kinds {
                        if !kinds.contains(&edge.kind) {
                            continue;
                        }
                    }
                    if only_valid && edge.valid_until != 0 && edge.valid_until < now {
                        continue;
                    }

                    for &other_id in &edge.node_ids {
                        if other_id != node_id {
                            neighbor_ids.insert(other_id);
                        }
                    }
                    edges.push(edge);
                }
            }
            _ => continue,
        }
    }

    let mut nodes = Vec::new();
    for &nid in &neighbor_ids {
        match read_slot::<GraphNode>(engine, nid)? {
            Some(node) => {
                if only_valid && node.valid_until != 0 && node.valid_until < now {
                    continue;
                }
                nodes.push(node);
            }
            None => continue,
        }
    }

    Ok(NeighborResult { nodes, edges })
}

/// BFS shortest path between two nodes in a graph.
pub fn find_path(
    engine: &StorageEngine,
    graph_id: u64,
    from: u64,
    to: u64,
    max_depth: usize,
) -> Result<PathResult, MemHopError> {
    if from == to {
        let mut nodes = Vec::new();
        if let Ok(Some(node)) = read_slot::<GraphNode>(engine, from) {
            nodes.push(node);
        }
        return Ok(PathResult {
            nodes,
            edges: Vec::new(),
        });
    }

    // Build adjacency for this graph
    let mut adjacency: HashMap<u64, Vec<(GraphEdge, u64)>> = HashMap::new(); // node -> list of (edge, neighbor)

    for (&id_hash, &_offset) in engine.iter_index() {
        match engine.read_record(id_hash) {
            Ok(Some((record_type, data))) => {
                if record_type != REC_L3_GRAPH_EDGE {
                    continue;
                }
                if let Ok(edge) = bincode::deserialize::<GraphEdge>(data) {
                    if edge.graph_id != graph_id {
                        continue;
                    }
                    for &nid in &edge.node_ids {
                        for &other in &edge.node_ids {
                            if other != nid {
                                adjacency
                                    .entry(nid)
                                    .or_default()
                                    .push((edge.clone(), other));
                            }
                        }
                    }
                }
            }
            _ => continue,
        }
    }

    // BFS to find shortest path (limited by max_depth)
    let mut visited: HashSet<u64> = HashSet::new();
    let mut parent: HashMap<u64, (u64, GraphEdge)> = HashMap::new();
    let mut queue: VecDeque<(u64, usize)> = VecDeque::new();

    visited.insert(from);
    queue.push_back((from, 0));
    let mut found = false;

    while let Some((current, depth)) = queue.pop_front() {
        if current == to {
            found = true;
            break;
        }
        if depth >= max_depth {
            continue;
        }
        if let Some(neighbors) = adjacency.get(&current) {
            for (edge, neighbor) in neighbors {
                if !visited.contains(neighbor) {
                    visited.insert(*neighbor);
                    parent.insert(*neighbor, (current, edge.clone()));
                    queue.push_back((*neighbor, depth + 1));
                }
            }
        }
    }

    if !found {
        return Ok(PathResult {
            nodes: Vec::new(),
            edges: Vec::new(),
        });
    }

    // Reconstruct path
    let mut path_node_ids: Vec<u64> = Vec::new();
    let mut path_edges: Vec<GraphEdge> = Vec::new();
    let mut current = to;
    while current != from {
        path_node_ids.push(current);
        if let Some((par, edge)) = parent.get(&current) {
            path_edges.push(edge.clone());
            current = *par;
        } else {
            break;
        }
    }
    path_node_ids.push(from);
    path_node_ids.reverse();
    path_edges.reverse();

    // Load full node objects
    let mut nodes = Vec::new();
    for &nid in &path_node_ids {
        if let Ok(Some(node)) = read_slot::<GraphNode>(engine, nid) {
            nodes.push(node);
        }
    }

    Ok(PathResult {
        nodes,
        edges: path_edges,
    })
}

// ============================================================================
// Compatibility helpers for v1 → v2 migration
// ============================================================================

/// Compatibility helper: returns the record type for a given id_hash.
/// Replaces the old `page_type_of(mmap, page_ref)` pattern.
pub fn page_type_of(engine: &StorageEngine, id_hash: u64) -> Option<u16> {
    match engine.read_record(id_hash) {
        Ok(Some((record_type, _data))) => Some(record_type as u16),
        _ => None,
    }
}

/// Collect L2 ContextSlots that reference a given L3 graph hash.
/// Returns `(id_hash)` pairs for each matching context.
pub fn collect_l2_refs(engine: &StorageEngine, graph_hash: u64) -> Result<Vec<u64>, MemHopError> {
    use crate::storage::record::REC_L2_TOPIC;

    let mut refs = Vec::new();
    for (&id_hash, &_offset) in engine.iter_index() {
        match engine.read_record(id_hash) {
            Ok(Some((record_type, data))) => {
                if record_type != REC_L2_TOPIC {
                    continue;
                }
                match bincode::deserialize::<TopicSlot>(data) {
                    Ok(ctx) => {
                        if ctx.user_l3_refs.contains(&graph_hash)
                            || ctx.agent_l3_refs.contains(&graph_hash)
                        {
                            refs.push(id_hash);
                        }
                    }
                    Err(_) => continue,
                }
            }
            _ => continue,
        }
    }
    Ok(refs)
}

/// Remove an L3 graph hash reference from a TopicSlot's l3_refs.
/// Performs read-modify-write via the StorageEngine.
pub fn remove_l3_ref_from_context(
    engine: &mut StorageEngine,
    id_hash: u64,
    graph_hash: u64,
) -> Result<bool, MemHopError> {
    use crate::storage::record::REC_L2_TOPIC;

    let mut ctx: TopicSlot = match read_slot(engine, id_hash)? {
        Some(c) => c,
        None => return Ok(false),
    };

    let had_user = ctx.user_l3_refs.contains(&graph_hash);
    let had_agent = ctx.agent_l3_refs.contains(&graph_hash);

    if !had_user && !had_agent {
        return Ok(false);
    }

    ctx.user_l3_refs.retain(|&h| h != graph_hash);
    ctx.agent_l3_refs.retain(|&h| h != graph_hash);
    ctx.updated_at = now_ms();

    write_slot(engine, REC_L2_TOPIC, id_hash, &ctx)?;
    Ok(true)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::test_helpers::*;

    #[test]
    fn test_bfs_traversal_one_hop() {
        let temp = tempfile::NamedTempFile::new().unwrap();
        let mut engine = StorageEngine::create(temp.path(), 768).unwrap();
        let (_nodes, _edges) = build_test_graph(&mut engine);

        let mut cache = crate::l3::AdjacencyCache::new();
        let hops = bfs_traversal_cached(&engine, 1, 101, 1, None, &mut cache).unwrap();

        assert_eq!(hops.len(), 3, "expected 3 one-hop neighbors from n1");

        let to_nodes: Vec<u64> = hops.iter().map(|h| h.to_node).collect();
        assert!(to_nodes.contains(&102));
        assert!(to_nodes.contains(&103));
        assert!(to_nodes.contains(&105));

        for hop in &hops {
            assert_eq!(hop.depth, 1);
            assert_eq!(hop.from_node, 101);
        }
    }

    #[test]
    fn test_bfs_traversal_two_hops() {
        let temp = tempfile::NamedTempFile::new().unwrap();
        let mut engine = StorageEngine::create(temp.path(), 768).unwrap();
        let (_nodes, _edges) = build_test_graph(&mut engine);

        let mut cache = crate::l3::AdjacencyCache::new();
        let hops = bfs_traversal_cached(&engine, 1, 101, 2, None, &mut cache).unwrap();

        assert_eq!(hops.len(), 5, "expected 5 hops total at depth <= 2");

        let depth2_hops: Vec<&TraversalHop> = hops.iter().filter(|h| h.depth == 2).collect();
        assert_eq!(depth2_hops.len(), 2, "expected 2 depth-2 hops");
        let depth2_targets: Vec<u64> = depth2_hops.iter().map(|h| h.to_node).collect();
        // BFS edge processing order depends on HashMap iteration, so depth-2
        // target 103 may appear as 102 when node 103 is dequeued before 102.
        assert!(
            depth2_targets.contains(&104),
            "expected depth-2 hop to node 104"
        );
        assert!(
            depth2_targets.contains(&103) || depth2_targets.contains(&102),
            "expected depth-2 hop to node 102 or 103, got {:?}",
            depth2_targets
        );
    }

    #[test]
    fn test_bfs_traversal_edge_kind_filter() {
        let temp = tempfile::NamedTempFile::new().unwrap();
        let mut engine = StorageEngine::create(temp.path(), 768).unwrap();
        let (_nodes, _edges) = build_test_graph(&mut engine);

        let mut cache = crate::l3::AdjacencyCache::new();
        let hops = bfs_traversal_cached(
            &engine,
            1,
            101,
            2,
            Some(&[GraphEdgeKind::Related]),
            &mut cache,
        )
        .unwrap();

        assert_eq!(hops.len(), 2);
        assert!(hops.iter().all(|h| h.edge.kind == GraphEdgeKind::Related));
    }

    #[test]
    fn test_bfs_avoids_cycles() {
        let temp = tempfile::NamedTempFile::new().unwrap();
        let mut engine = StorageEngine::create(temp.path(), 768).unwrap();
        let mut cache = crate::l3::AdjacencyCache::new();

        for &nid in &[101u64, 102, 103] {
            add_node(
                &mut engine,
                create_test_node(nid, 1, &format!("node{}", nid)),
                None,
                None,
            )
            .unwrap();
        }
        add_edge(
            &mut engine,
            create_test_edge(201, 1, GraphEdgeKind::Related, vec![101, 102]),
            None,
        )
        .unwrap();
        add_edge(
            &mut engine,
            create_test_edge(202, 1, GraphEdgeKind::Related, vec![102, 103]),
            None,
        )
        .unwrap();
        add_edge(
            &mut engine,
            create_test_edge(203, 1, GraphEdgeKind::Related, vec![103, 101]),
            None,
        )
        .unwrap();

        let hops = bfs_traversal_cached(&engine, 1, 101, 3, None, &mut cache).unwrap();
        assert_eq!(hops.len(), 3);
    }

    #[test]
    fn test_bfs_traversal_subgraph() {
        let temp = tempfile::NamedTempFile::new().unwrap();
        let mut engine = StorageEngine::create(temp.path(), 768).unwrap();
        let (node_ids, _edge_ids) = build_test_graph(&mut engine);
        let mut cache = crate::l3::AdjacencyCache::new();
        let hops = bfs_traversal_cached(&engine, 1, 101, 2, None, &mut cache).unwrap();

        let mut returned_node_ids: HashSet<u64> = HashSet::new();
        returned_node_ids.insert(101);
        for hop in &hops {
            returned_node_ids.insert(hop.from_node);
            returned_node_ids.insert(hop.to_node);
        }
        assert_eq!(returned_node_ids, node_ids.iter().copied().collect());
    }

    #[test]
    fn test_update_node_in_place() {
        let temp = tempfile::NamedTempFile::new().unwrap();
        let mut engine = StorageEngine::create(temp.path(), 768).unwrap();
        let nid = add_node(
            &mut engine,
            create_test_node(101, 1, "original"),
            None,
            None,
        )
        .unwrap();
        let hash = u64::from_str_radix(&nid, 16).unwrap();

        update_node(
            &mut engine,
            hash,
            NodeUpdateFields {
                title: Some("updated".into()),
                content: None,
                summary: Some("new summary".into()),
                keywords: None,
                importance: Some(0.9),
                valid_until: None,
            },
        )
        .unwrap();

        let node = read_slot::<GraphNode>(&engine, hash).unwrap().unwrap();
        assert_eq!(node.title, "updated");
        assert_eq!(node.summary, Some("new summary".into()));
        assert!((node.importance - 0.9).abs() < f32::EPSILON);
    }

    #[test]
    fn test_get_neighbors_one_hop() {
        let temp = tempfile::NamedTempFile::new().unwrap();
        let mut engine = StorageEngine::create(temp.path(), 768).unwrap();
        build_test_graph(&mut engine);

        let result = get_neighbors(&engine, 1, 101, None, false).unwrap();
        let neighbor_ids: HashSet<u64> = result.nodes.iter().map(|n| n.id_hash).collect();
        assert!(neighbor_ids.contains(&102));
        assert!(neighbor_ids.contains(&103));
        assert!(neighbor_ids.contains(&105));
        assert!(!neighbor_ids.contains(&101));
        assert!(!neighbor_ids.contains(&104));
        assert_eq!(result.edges.len(), 2);
    }

    #[test]
    fn test_find_path_simple() {
        let temp = tempfile::NamedTempFile::new().unwrap();
        let mut engine = StorageEngine::create(temp.path(), 768).unwrap();
        build_test_graph(&mut engine);

        // 101 -> ... -> 104: exists via 101-103-104
        let result = find_path(&engine, 1, 101, 104, 5).unwrap();
        assert!(!result.nodes.is_empty(), "path should exist");
        assert_eq!(result.nodes.len(), 3); // 101, 103, 104
        assert_eq!(result.nodes[0].id_hash, 101);
        assert_eq!(result.nodes[2].id_hash, 104);
    }

    #[test]
    fn test_invalidate_edge() {
        let temp = tempfile::NamedTempFile::new().unwrap();
        let mut engine = StorageEngine::create(temp.path(), 768).unwrap();
        let eid = add_edge(
            &mut engine,
            create_test_edge(201, 1, GraphEdgeKind::Related, vec![101, 102]),
            None,
        )
        .unwrap();
        let hash = u64::from_str_radix(&eid, 16).unwrap();
        let now = now_ms();

        invalidate_edge(&mut engine, hash, now).unwrap();

        let edge = read_slot::<GraphEdge>(&engine, hash).unwrap().unwrap();
        assert_eq!(edge.valid_until, now);
    }
}
