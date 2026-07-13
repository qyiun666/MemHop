// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L3 Hypergraph Storage Layer — CRUD for HypergraphNode/Edge and graph-level management.
//!
//! v0.57.0 重构：所有存储 I/O 改为 StorageEngine 接口。

use crate::layers::hypergraph::{GraphEdgeKind, HypergraphEdge, HypergraphNode};
use crate::query::types::*;
use crate::shared::common::{format_hash, has_more, matches_keyword, pagination_params};
use crate::storage::record::{REC_L3_GRAPH_EDGE, REC_L3_GRAPH_NODE};
use crate::storage::StorageEngine;
use crate::store::{delete_slot, read_slot, write_slot};
use crate::MemHopError;
use std::collections::{HashMap, HashSet, VecDeque};

// ============================================================================
// Inline helpers
// ============================================================================

/// Quick-read record type from a record (v2 engine equivalent of old `page_type_of`).
#[inline]
#[cfg(test)]
pub(crate) fn page_type_of(engine: &StorageEngine, id_hash: u64) -> Option<u16> {
    match engine.read_record(id_hash) {
        Ok(Some((record_type, _data))) => Some(record_type as u16),
        _ => None,
    }
}

// ============================================================================
// Node CRUD
// ============================================================================

/// Add a HypergraphNode to the graph — write to engine only (no mmap).
/// Returns the hex-formatted node ID string.
pub fn add_node_with_engine(
    engine: &mut StorageEngine,
    mut node: HypergraphNode,
    tracker: Option<&mut crate::l3::DegreeTracker>,
    index_map: Option<&mut HashMap<u64, crate::l3::L3Index>>,
) -> Result<String, MemHopError> {
    // L3 is a knowledge graph — content should be a short summary/index,
    // not the original text. Enforce a 200-char cap to keep nodes small.
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

/// Delete a HypergraphNode and cascade-delete all edges that reference it.
pub fn delete_node_with_engine(
    engine: &mut StorageEngine,
    node_id: &str,
    tracker: Option<&mut crate::l3::DegreeTracker>,
    index_map: Option<&mut HashMap<u64, crate::l3::L3Index>>,
) -> Result<(), MemHopError> {
    let id_hash = crate::shared::common::parse_id_to_hash(node_id);

    let node: HypergraphNode = match read_slot(engine, id_hash)? {
        Some(n) => n,
        None => return Ok(()),
    };

    let graph_id = node.graph_id;

    // Find all edges referencing this node via engine
    let mut edges_to_delete: Vec<u64> = Vec::new();
    for (&eid, &_offset) in engine.iter_index() {
        match engine.read_record(eid) {
            Ok(Some((record_type, data))) => {
                if record_type != REC_L3_GRAPH_EDGE {
                    continue;
                }
                if let Ok(edge) = bincode::deserialize::<HypergraphEdge>(data) {
                    if edge.graph_id == graph_id && edge.node_ids.contains(&id_hash) {
                        edges_to_delete.push(eid);
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

/// Add a HypergraphEdge to the graph — write to engine only (no mmap).
pub fn add_edge_with_engine(
    engine: &mut StorageEngine,
    edge: HypergraphEdge,
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

/// List all HypergraphNodes belonging to a specific graph, with pagination and filtering.
pub fn list_nodes_by_graph(
    engine: &StorageEngine,
    graph_id: u64,
    query: &NodeListQuery,
) -> Result<NodeListResult, MemHopError> {
    let mut all_nodes: Vec<HypergraphNode> = Vec::new();

    for (&id_hash, &_offset) in engine.iter_index() {
        match engine.read_record(id_hash) {
            Ok(Some((record_type, data))) => {
                if record_type != REC_L3_GRAPH_NODE {
                    continue;
                }
                if let Ok(node) = bincode::deserialize::<HypergraphNode>(data) {
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
    let items: Vec<GraphNode> = all_nodes
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

/// List all HypergraphEdges belonging to a specific graph, with pagination and filtering.
pub fn list_edges_by_graph(
    engine: &StorageEngine,
    graph_id: u64,
    query: &EdgeListQuery,
) -> Result<EdgeListResult, MemHopError> {
    let mut all_edges: Vec<HypergraphEdge> = Vec::new();

    for (&id_hash, &_offset) in engine.iter_index() {
        match engine.read_record(id_hash) {
            Ok(Some((record_type, data))) => {
                if record_type != REC_L3_GRAPH_EDGE {
                    continue;
                }
                if let Ok(edge) = bincode::deserialize::<HypergraphEdge>(data) {
                    if edge.graph_id != graph_id {
                        continue;
                    }

                    if let Some(kind) = query.kind {
                        if edge.kind != kind {
                            continue;
                        }
                    }

                    if let Some(ref nid) = query.node_id {
                        let node_hash = crate::shared::common::parse_id_to_hash(nid);
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
    let items: Vec<GraphEdge> = all_edges
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

/// Delete an entire L3 graph: cascade-deletes all nodes, edges, and the HypergraphSlot.
///
/// NOTE: This does NOT clean up L2 ContextSlot l3_refs. Callers should handle
/// L2 cleanup separately (see MemHop::delete_knowledge).
pub fn delete_graph_with_engine(
    engine: &mut StorageEngine,
    l3_id: &str,
) -> Result<(), MemHopError> {
    let graph_hash = crate::shared::common::parse_id_to_hash(l3_id);

    if engine.read_record(graph_hash)?.is_none() {
        return Ok(());
    }

    let mut node_hashes: Vec<u64> = Vec::new();
    let mut edge_hashes: Vec<u64> = Vec::new();

    for (&id_hash, &_offset) in engine.iter_index() {
        match engine.read_record(id_hash) {
            Ok(Some((record_type, data))) => {
                if record_type == REC_L3_GRAPH_NODE {
                    if let Ok(node) = bincode::deserialize::<HypergraphNode>(data) {
                        if node.graph_id == graph_hash {
                            node_hashes.push(id_hash);
                        }
                    }
                } else if record_type == REC_L3_GRAPH_EDGE {
                    if let Ok(edge) = bincode::deserialize::<HypergraphEdge>(data) {
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

/// Delete an entire L3 graph using engine API (migration compat alias).
pub fn delete_graph(engine: &mut StorageEngine, l3_id: &str) -> Result<(), MemHopError> {
    delete_graph_with_engine(engine, l3_id)
}

/// Collect L2 ContextSlot IDs that reference a given L3 graph.
/// Returns a list of id_hashes for ContextSlots that need l3_refs cleanup.
pub fn collect_l2_refs(engine: &StorageEngine, graph_hash: u64) -> Result<Vec<u64>, MemHopError> {
    use crate::storage::record::REC_L2_TOPIC;

    let mut refs = Vec::new();
    for (&id_hash, &_offset) in engine.iter_index() {
        match engine.read_record(id_hash) {
            Ok(Some((record_type, data))) => {
                if record_type != REC_L2_TOPIC {
                    continue;
                }
                if let Ok(ctx) = bincode::deserialize::<crate::layers::context::ContextSlot>(data) {
                    if ctx.user_l3_refs.contains(&graph_hash)
                        || ctx.agent_l3_refs.contains(&graph_hash)
                    {
                        refs.push(id_hash);
                    }
                }
            }
            _ => continue,
        }
    }

    Ok(refs)
}

/// Update a L2 ContextSlot's l3_refs by removing a graph hash, via engine read-modify-write.
pub fn remove_l3_ref_from_context(
    engine: &mut StorageEngine,
    id_hash: u64,
    graph_hash: u64,
) -> Result<bool, MemHopError> {
    use crate::storage::record::REC_L2_TOPIC;

    let mut ctx: crate::layers::context::ContextSlot = match read_slot(engine, id_hash)? {
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
    ctx.updated_at = crate::shared::common::now_ms();

    write_slot(engine, REC_L2_TOPIC, id_hash, &ctx)?;
    Ok(true)
}

/// BFS traversal of an L3 hypergraph starting from `start_node`.
///
/// Returns a flat list of `TraversalHop` records, one per traversed edge
/// endpoint. Hyperedges are supported: a single edge containing the current
/// node may produce multiple hops to every other endpoint in the same edge.
fn build_adjacency_index(
    engine: &StorageEngine,
    graph_id: u64,
    edge_kinds: Option<&[GraphEdgeKind]>,
) -> crate::l3::GraphAdjacency {
    let mut adjacency: HashMap<u64, Vec<(HypergraphEdge, Vec<u64>)>> = HashMap::new();
    for (&_eid, &_offset) in engine.iter_index() {
        match engine.read_record(_eid) {
            Ok(Some((record_type, data))) => {
                if record_type != REC_L3_GRAPH_EDGE {
                    continue;
                }
                if let Ok(edge) = bincode::deserialize::<HypergraphEdge>(data) {
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

/// Execute BFS traversal on a pre-built adjacency index.
fn bfs_with_adjacency(
    adjacency: &HashMap<u64, Vec<(HypergraphEdge, Vec<u64>)>>,
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

                    match node_depth.get(&to_node) {
                        Some(&d) if d < hop_depth => {}
                        _ => {
                            hops.push(TraversalHop {
                                depth: hop_depth,
                                from_node: current_node,
                                edge: edge.clone().into(),
                                to_node,
                            });
                        }
                    }

                    if let std::collections::hash_map::Entry::Vacant(e) = node_depth.entry(to_node)
                    {
                        e.insert(hop_depth);
                        queue.push_back((to_node, hop_depth));
                    }
                }
            }
        }
    }

    hops
}

/// BFS traversal with adjacency cache support.
///
/// If the cache contains a valid adjacency list for the graph, it is reused.
/// Otherwise, the adjacency list is built from scratch and stored in the cache.
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

#[cfg(test)]
mod tests {
    use super::*;
    use crate::test_helpers::*;

    #[test]
    fn test_bfs_traversal_one_hop() {
        let temp = tempfile::NamedTempFile::new().unwrap();
        let mut engine = crate::storage::StorageEngine::create(temp.path(), 768).unwrap();
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
        let mut engine = crate::storage::StorageEngine::create(temp.path(), 768).unwrap();
        let (_nodes, _edges) = build_test_graph(&mut engine);

        let mut cache = crate::l3::AdjacencyCache::new();
        let hops = bfs_traversal_cached(&engine, 1, 101, 2, None, &mut cache).unwrap();

        // depth 1: 101->102 (e201), 101->103 (e204), 101->105 (e204)
        // depth 2: 103->104 (e203)
        // Note: edge 202 (102->103) is NOT traversed because hyperedge 204
        // already discovers node 103 at depth 1, making 102->103 a back-edge.
        assert_eq!(hops.len(), 4, "expected 4 hops total at depth <= 2");

        let depth2_hops: Vec<&TraversalHop> = hops.iter().filter(|h| h.depth == 2).collect();
        assert_eq!(depth2_hops.len(), 1);

        let depth2_targets: Vec<u64> = depth2_hops.iter().map(|h| h.to_node).collect();
        assert!(depth2_targets.contains(&104));
    }

    #[test]
    fn test_bfs_traversal_edge_kind_filter() {
        let temp = tempfile::NamedTempFile::new().unwrap();
        let mut engine = crate::storage::StorageEngine::create(temp.path(), 768).unwrap();
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

        // Only Related edges: 101->102, 102->103
        assert_eq!(hops.len(), 2);
        assert!(hops.iter().all(|h| h.edge.kind == GraphEdgeKind::Related));
    }

    #[test]
    fn test_bfs_avoids_cycles() {
        let temp = tempfile::NamedTempFile::new().unwrap();
        let mut engine = crate::storage::StorageEngine::create(temp.path(), 768).unwrap();
        let mut cache = crate::l3::AdjacencyCache::new();

        // Create a triangle: 101 <-> 102 <-> 103 <-> 101
        for &nid in &[101u64, 102, 103] {
            add_node_with_engine(
                &mut engine,
                create_test_node(nid, 1, &format!("node{}", nid)),
                None,
                None,
            )
            .unwrap();
        }
        add_edge_with_engine(
            &mut engine,
            create_test_edge(201, 1, GraphEdgeKind::Related, vec![101, 102]),
            None,
        )
        .unwrap();
        add_edge_with_engine(
            &mut engine,
            create_test_edge(202, 1, GraphEdgeKind::Related, vec![102, 103]),
            None,
        )
        .unwrap();
        add_edge_with_engine(
            &mut engine,
            create_test_edge(203, 1, GraphEdgeKind::Related, vec![103, 101]),
            None,
        )
        .unwrap();

        let hops = bfs_traversal_cached(&engine, 1, 101, 3, None, &mut cache).unwrap();

        // With cycle prevention there should be no duplicate to_nodes at each depth.
        // depth1: 101->102, 101->103
        // depth2: 102->103 (already visited), 103->102 (already visited)
        // depth3: nothing new
        assert_eq!(hops.len(), 2);
    }

    #[test]
    fn test_bfs_traversal_subgraph() {
        let temp = tempfile::NamedTempFile::new().unwrap();
        let mut engine = crate::storage::StorageEngine::create(temp.path(), 768).unwrap();
        let mut cache = crate::l3::AdjacencyCache::new();
        let (node_ids, _edge_ids) = build_test_graph(&mut engine);

        let hops = bfs_traversal_cached(&engine, 1, 101, 2, None, &mut cache).unwrap();

        let mut returned_node_ids: HashSet<u64> = HashSet::new();
        let mut returned_edge_ids: HashSet<u64> = HashSet::new();
        returned_node_ids.insert(101); // start node
        for hop in &hops {
            returned_node_ids.insert(hop.from_node);
            returned_node_ids.insert(hop.to_node);
            returned_edge_ids.insert(u64::from_str_radix(&hop.edge.id, 16).unwrap());
        }

        // BFS discovers 4 of 5 edges: edge 202 (102-103) is never traversed
        // because hyperedge 204 discovers node 103 at depth 1, making the
        // 102->103 connection a back-edge that is skipped.
        let expected_edge_ids: HashSet<u64> = [201u64, 203, 204].iter().copied().collect();
        assert_eq!(returned_node_ids, node_ids.iter().copied().collect());
        assert_eq!(returned_edge_ids, expected_edge_ids);
    }

    #[test]
    fn test_bfs_traversal_start_node_only() {
        let temp = tempfile::NamedTempFile::new().unwrap();
        let mut engine = crate::storage::StorageEngine::create(temp.path(), 768).unwrap();

        add_node_with_engine(&mut engine, create_test_node(101, 1, "island"), None, None).unwrap();

        let mut cache = crate::l3::AdjacencyCache::new();
        let hops = bfs_traversal_cached(&engine, 1, 101, 2, None, &mut cache).unwrap();

        assert_eq!(hops.len(), 0); // No edges from isolated node
    }
}
