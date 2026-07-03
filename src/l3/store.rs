// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L3 Hypergraph Storage Layer — CRUD for HypergraphNode/Edge and graph-level management.

use crate::file::free_list::{allocate_or_extend, free_page};
use crate::file::header::FileHeader;
use crate::file::page::PageHeader;
use crate::index::btree::BTreeIndex;
use crate::layers::hypergraph::{GraphEdgeKind, HypergraphEdge, HypergraphNode};
use crate::query::types::*;
use crate::shared::common::{format_hash, has_more, matches_keyword, pagination_params};
use crate::shared::slot_io::get_slot_data;
use crate::util::{PageType, DEFAULT_GROW_PAGES, PAGE_SIZE, SENTINEL_PAGE_ID};
use crate::MemHopError;
use memmap2::MmapMut;
use std::collections::{hash_map::Entry, HashMap, HashSet, VecDeque};
use std::fs::File;

// ============================================================================
// Inline helpers
// ============================================================================

/// Quick-read page type from a page header without full deserialization.
#[inline]
pub(crate) fn page_type_of(mmap: &[u8], page_ref: u64) -> Option<u16> {
    let page_id = (page_ref >> 16) as usize;
    let offset = page_id * PAGE_SIZE;
    if offset + 6 > mmap.len() {
        return None;
    }
    Some(u16::from_le_bytes([mmap[offset + 4], mmap[offset + 5]]))
}

// ============================================================================
// Node CRUD
// ============================================================================

/// Add a HypergraphNode to the graph, write to mmap, and register in BTreeIndex.
/// Returns the hex-formatted node ID string.
pub fn add_node(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    mut node: HypergraphNode,
    file: &mut File,
    tracker: Option<&mut crate::l3::DegreeTracker>,
    index_map: Option<&mut HashMap<u64, crate::l3::L3Index>>,
) -> Result<String, MemHopError> {
    // L3 is a knowledge graph — content should be a short summary/index,
    // not the original text. Enforce a 200-char cap to keep nodes small.
    if node.content.len() > 200 {
        node.content = node.content.chars().take(200).collect();
    }

    let data_bytes = node
        .serialize()
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;

    let data_size = data_bytes.len();
    if data_size > PAGE_SIZE - 32 {
        return Err(MemHopError::Serialization(format!(
            "HypergraphNode too large for page: {} > {}",
            data_size,
            PAGE_SIZE - 32
        )));
    }

    // Allocate page BEFORE writing — if allocation fails, no cleanup needed
    let page_id = allocate_or_extend(mmap, header, file, DEFAULT_GROW_PAGES)?;
    let offset = (page_id as usize) * PAGE_SIZE;

    let page_hdr = PageHeader::new(page_id, PageType::HypergraphNode, 3, SENTINEL_PAGE_ID);
    let hdr_bytes = page_hdr.to_bytes();
    mmap[offset..offset + 32].copy_from_slice(&hdr_bytes);

    // Write node data — if this fails, free the allocated page
    let data_offset = offset + 32;
    if data_offset + data_size > mmap.len() {
        // Page leak guard: return page to free list before erroring
        free_page(mmap, header, page_id)?;
        return Err(MemHopError::Serialization(format!(
            "HypergraphNode write beyond mmap: {} + {} > {}",
            data_offset,
            data_size,
            mmap.len()
        )));
    }
    mmap[data_offset..data_offset + data_size].copy_from_slice(&data_bytes);

    btree.insert(node.id_hash, (page_id as u64) << 16);

    if let Some(tracker) = tracker {
        tracker.on_node_added(node.graph_id, node.id_hash);
    }
    if let Some(index_map) = index_map {
        index_map.entry(node.graph_id).or_default().add_node(&node);
    }

    Ok(format_hash(node.id_hash))
}

/// Read a HypergraphNode by its ID string.
///
/// Returns `Ok(None)` if not found in the BTreeIndex.
pub fn get_node(
    mmap: &MmapMut,
    btree: &BTreeIndex,
    node_id: &str,
) -> Result<Option<HypergraphNode>, MemHopError> {
    let id_hash = crate::shared::common::parse_id_to_hash(node_id);

    match btree.search(id_hash) {
        Some(page_ref) => {
            let data: &[u8] = &mmap[..];
            if let Some(slot_data) = get_slot_data(data, page_ref) {
                let node = HypergraphNode::deserialize(slot_data)?;
                Ok(Some(node))
            } else {
                Err(MemHopError::PageNotFound((page_ref >> 16) as u32))
            }
        }
        None => Ok(None),
    }
}

/// Delete a HypergraphNode and cascade-delete all edges that reference it.
pub fn delete_node(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    node_id: &str,
    tracker: Option<&mut crate::l3::DegreeTracker>,
    index_map: Option<&mut HashMap<u64, crate::l3::L3Index>>,
) -> Result<(), MemHopError> {
    let id_hash = crate::shared::common::parse_id_to_hash(node_id);

    let node = match get_node(mmap, btree, node_id)? {
        Some(n) => n,
        None => return Ok(()), // Already gone
    };

    let graph_id = node.graph_id;

    // Also collect page_ref to avoid second BTree lookup when deleting
    let mut edges_to_delete: Vec<(u64, u64)> = Vec::new(); // (edge_hash, page_ref)
    for (&eid, &page_ref) in btree.iter() {
        let data: &[u8] = &mmap[..];
        if page_type_of(data, page_ref) != Some(PageType::HypergraphEdge as u16) {
            continue;
        }
        if let Some(slot_data) = get_slot_data(data, page_ref) {
            if let Ok(edge) = HypergraphEdge::deserialize(slot_data) {
                if edge.graph_id == graph_id && edge.node_ids.contains(&id_hash) {
                    edges_to_delete.push((eid, page_ref));
                }
            }
        }
    }

    for (edge_hash, page_ref) in &edges_to_delete {
        btree.remove(*edge_hash);
        let page_id = (page_ref >> 16) as u32;
        crate::file::free_list::free_page(mmap, header, page_id)?;
    }

    if let Some(tracker) = tracker {
        tracker.on_node_deleted(graph_id, id_hash);
    }
    if let Some(index_map) = index_map {
        if let Some(index) = index_map.get_mut(&graph_id) {
            index.remove_node(id_hash, &node);
        }
    }

    if let Some(page_ref) = btree.delete(id_hash) {
        let page_id = (page_ref >> 16) as u32;
        crate::file::free_list::free_page(mmap, header, page_id)?;
    }

    Ok(())
}

// ============================================================================
// Edge CRUD
// ============================================================================

/// Add a HypergraphEdge to the graph, write to mmap, and register in BTreeIndex.
pub fn add_edge(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    edge: HypergraphEdge,
    file: &mut File,
    tracker: Option<&mut crate::l3::DegreeTracker>,
) -> Result<String, MemHopError> {
    let data_bytes = edge
        .serialize()
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;

    let data_size = data_bytes.len();
    if data_size > PAGE_SIZE - 32 {
        return Err(MemHopError::Serialization(format!(
            "HypergraphEdge too large for page: {} > {}",
            data_size,
            PAGE_SIZE - 32
        )));
    }

    // Allocate page BEFORE writing — if allocation fails, no cleanup needed
    let page_id = allocate_or_extend(mmap, header, file, DEFAULT_GROW_PAGES)?;
    let offset = (page_id as usize) * PAGE_SIZE;

    let page_hdr = PageHeader::new(page_id, PageType::HypergraphEdge, 3, SENTINEL_PAGE_ID);
    let hdr_bytes = page_hdr.to_bytes();
    mmap[offset..offset + 32].copy_from_slice(&hdr_bytes);

    // Write edge data — if this fails, free the allocated page
    let data_offset = offset + 32;
    if data_offset + data_size > mmap.len() {
        // Page leak guard: return page to free list before erroring
        free_page(mmap, header, page_id)?;
        return Err(MemHopError::Serialization(format!(
            "HypergraphEdge write beyond mmap: {} + {} > {}",
            data_offset,
            data_size,
            mmap.len()
        )));
    }
    mmap[data_offset..data_offset + data_size].copy_from_slice(&data_bytes);

    btree.insert(edge.id_hash, (page_id as u64) << 16);

    if let Some(tracker) = tracker {
        tracker.on_edge_added(edge.graph_id, &edge.node_ids);
    }

    Ok(format_hash(edge.id_hash))
}

/// Read a HypergraphEdge by its ID string.
pub fn get_edge(
    mmap: &MmapMut,
    btree: &BTreeIndex,
    edge_id: &str,
) -> Result<Option<HypergraphEdge>, MemHopError> {
    let id_hash = crate::shared::common::parse_id_to_hash(edge_id);

    match btree.search(id_hash) {
        Some(page_ref) => {
            let data: &[u8] = &mmap[..];
            if let Some(slot_data) = get_slot_data(data, page_ref) {
                let edge = HypergraphEdge::deserialize(slot_data)?;
                Ok(Some(edge))
            } else {
                Err(MemHopError::PageNotFound((page_ref >> 16) as u32))
            }
        }
        None => Ok(None),
    }
}

/// Delete a HypergraphEdge by its ID string.
pub fn delete_edge(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    edge_id: &str,
    tracker: Option<&mut crate::l3::DegreeTracker>,
) -> Result<(), MemHopError> {
    let id_hash = crate::shared::common::parse_id_to_hash(edge_id);

    // Read edge before deleting for tracker notification
    let edge_opt = get_edge(mmap, btree, edge_id).ok().flatten();

    if let Some(page_ref) = btree.delete(id_hash) {
        let page_id = (page_ref >> 16) as u32;
        crate::file::free_list::free_page(mmap, header, page_id)?;
    }

    if let (Some(tracker), Some(edge)) = (tracker, edge_opt) {
        tracker.on_edge_deleted(edge.graph_id, &edge.node_ids);
    }

    Ok(())
}

// ============================================================================
// Graph-level operations
// ============================================================================

/// List all HypergraphNodes belonging to a specific graph, with pagination and filtering.
pub fn list_nodes_by_graph(
    mmap: &MmapMut,
    btree: &BTreeIndex,
    graph_id: u64,
    query: &NodeListQuery,
) -> Result<NodeListResult, MemHopError> {
    let data: &[u8] = &mmap[..];
    let mut all_nodes: Vec<HypergraphNode> = Vec::new();

    for (&_id, &page_ref) in btree.iter() {
        if page_type_of(data, page_ref) != Some(PageType::HypergraphNode as u16) {
            continue;
        }
        if let Some(slot_data) = get_slot_data(data, page_ref) {
            if let Ok(node) = HypergraphNode::deserialize(slot_data) {
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
    }

    all_nodes.sort_by(|a, b| {
        b.importance
            .partial_cmp(&a.importance)
            .unwrap_or(std::cmp::Ordering::Equal)
    });

    let (skip, take) = pagination_params(query.page, query.page_size);
    let total = all_nodes.len();
    let items: Vec<HypergraphNode> = all_nodes.into_iter().skip(skip).take(take).collect();

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
    mmap: &MmapMut,
    btree: &BTreeIndex,
    graph_id: u64,
    query: &EdgeListQuery,
) -> Result<EdgeListResult, MemHopError> {
    let data: &[u8] = &mmap[..];
    let mut all_edges: Vec<HypergraphEdge> = Vec::new();

    for (&_id, &page_ref) in btree.iter() {
        if page_type_of(data, page_ref) != Some(PageType::HypergraphEdge as u16) {
            continue;
        }
        if let Some(slot_data) = get_slot_data(data, page_ref) {
            if let Ok(edge) = HypergraphEdge::deserialize(slot_data) {
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
    }

    all_edges.sort_by_key(|e| std::cmp::Reverse(e.created_at));

    let (skip, take) = pagination_params(query.page, query.page_size);
    let total = all_edges.len();
    let items: Vec<HypergraphEdge> = all_edges.into_iter().skip(skip).take(take).collect();

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
/// L2 cleanup separately with journal protection (see MemHop::delete_knowledge).
pub fn delete_graph(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    l3_id: &str,
) -> Result<(), MemHopError> {
    let graph_hash = crate::shared::common::parse_id_to_hash(l3_id);

    if btree.search(graph_hash).is_none() {
        return Ok(()); // Already gone
    }

    let mut node_hashes: Vec<u64> = Vec::new();
    let mut edge_hashes: Vec<u64> = Vec::new();

    for (&id_hash, &page_ref) in btree.iter() {
        let data: &[u8] = &mmap[..];
        let pt = page_type_of(data, page_ref).unwrap_or(0);
        if pt == PageType::HypergraphNode as u16 {
            if let Some(slot_data) = get_slot_data(data, page_ref) {
                if let Ok(node) = HypergraphNode::deserialize(slot_data) {
                    if node.graph_id == graph_hash {
                        node_hashes.push(id_hash);
                    }
                }
            }
        } else if pt == PageType::HypergraphEdge as u16 {
            if let Some(slot_data) = get_slot_data(data, page_ref) {
                if let Ok(edge) = HypergraphEdge::deserialize(slot_data) {
                    if edge.graph_id == graph_hash {
                        edge_hashes.push(id_hash);
                    }
                }
            }
        }
    }

    for edge_hash in &edge_hashes {
        if let Some(page_ref) = btree.delete(*edge_hash) {
            let page_id = (page_ref >> 16) as u32;
            free_page(mmap, header, page_id)?;
        }
    }

    for node_hash in &node_hashes {
        if let Some(page_ref) = btree.delete(*node_hash) {
            let page_id = (page_ref >> 16) as u32;
            free_page(mmap, header, page_id)?;
        }
    }

    if let Some(page_ref) = btree.delete(graph_hash) {
        let page_id = (page_ref >> 16) as u32;
        free_page(mmap, header, page_id)?;
    }

    Ok(())
}

/// Collect all L2 ContextSlot IDs that reference a given L3 graph.
/// Returns a list of (page_id, id_hash) tuples for ContextSlots that need l3_refs cleanup.
pub fn collect_l2_refs(
    mmap: &MmapMut,
    btree: &BTreeIndex,
    graph_hash: u64,
) -> Result<Vec<(u32, u64)>, MemHopError> {
    let data: &[u8] = &mmap[..];
    let mut refs = Vec::new();

    for (&id_hash, &page_ref) in btree.iter() {
        if page_type_of(data, page_ref) != Some(PageType::Context as u16) {
            continue;
        }
        if let Some(slot_data) = get_slot_data(data, page_ref) {
            if let Ok(ctx) = crate::layers::context::ContextSlot::deserialize_slot(slot_data) {
                if ctx.l3_refs.contains(&graph_hash) {
                    let page_id = (page_ref >> 16) as u32;
                    refs.push((page_id, id_hash));
                }
            }
        }
    }

    Ok(refs)
}

/// Update a L2 ContextSlot's l3_refs by removing a graph hash, with journal-safe full-page write.
///
/// # Safety
/// - Reads the full 4096-byte page, modifies the ContextSlot data region,
///   and writes the full page back atomically.
/// - Journal entry must be written BEFORE calling this function to ensure crash recovery.
pub fn remove_l3_ref_from_context(
    mmap: &mut MmapMut,
    page_id: u32,
    graph_hash: u64,
) -> Result<bool, MemHopError> {
    let offset = (page_id as usize) * PAGE_SIZE;
    if offset + PAGE_SIZE > mmap.len() {
        return Err(MemHopError::PageNotFound(page_id));
    }

    let mut page_buf = vec![0u8; PAGE_SIZE];
    page_buf.copy_from_slice(&mmap[offset..offset + PAGE_SIZE]);

    let slot_data = &page_buf[32..];
    if let Ok(mut ctx) = crate::layers::context::ContextSlot::deserialize_slot(slot_data) {
        if ctx.l3_refs.contains(&graph_hash) {
            ctx.l3_refs.retain(|&h| h != graph_hash);
            ctx.updated_at = crate::shared::common::now_ms();

            let ctx_bytes = ctx
                .serialize()
                .map_err(|e| MemHopError::Serialization(e.to_string()))?;
            if ctx_bytes.len() > PAGE_SIZE - 32 {
                return Err(MemHopError::Serialization(
                    "ContextSlot too large after l3_refs update".to_string(),
                ));
            }

            page_buf[32..32 + ctx_bytes.len()].copy_from_slice(&ctx_bytes);
            if 32 + ctx_bytes.len() < PAGE_SIZE {
                page_buf[32 + ctx_bytes.len()..].fill(0);
            }

            mmap[offset..offset + PAGE_SIZE].copy_from_slice(&page_buf);
            return Ok(true);
        }
    }

    Ok(false)
}

/// BFS traversal of an L3 hypergraph starting from `start_node`.
///
/// Returns a flat list of `TraversalHop` records, one per traversed edge
/// endpoint.  Hyperedges are supported: a single edge containing the current
/// node may produce multiple hops to every other endpoint in the same edge.
///
/// # Arguments
/// * `data`      - Read-only view of the mmap backing store.
/// * `btree`     - Global BTreeIndex mapping id_hash → page_ref.
/// * `graph_id`  - Restrict traversal to edges belonging to this graph.
/// * `start_node`- id_hash of the node where traversal begins.
/// * `max_depth` - Maximum number of edge traversals (0 means no hops).
/// * `edge_kinds`- Optional whitelist of edge kinds.
///
/// Build adjacency index from BTree for a specific graph and edge_kinds.
fn build_adjacency_index(
    data: &[u8],
    btree: &BTreeIndex,
    graph_id: u64,
    edge_kinds: Option<&[GraphEdgeKind]>,
) -> HashMap<u64, Vec<(HypergraphEdge, Vec<u64>)>> {
    let mut adjacency: HashMap<u64, Vec<(HypergraphEdge, Vec<u64>)>> = HashMap::new();
    for (&_eid, &page_ref) in btree.iter() {
        if page_type_of(data, page_ref) != Some(PageType::HypergraphEdge as u16) {
            continue;
        }
        if let Some(slot_data) = get_slot_data(data, page_ref) {
            if let Ok(edge) = HypergraphEdge::deserialize(slot_data) {
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
    }
    adjacency
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
                                edge: edge.clone(),
                                to_node,
                            });
                        }
                    }

                    if let Entry::Vacant(e) = node_depth.entry(to_node) {
                        e.insert(hop_depth);
                        queue.push_back((to_node, hop_depth));
                    }
                }
            }
        }
    }

    hops
}

pub fn bfs_traversal(
    data: &[u8],
    btree: &BTreeIndex,
    graph_id: u64,
    start_node: u64,
    max_depth: usize,
    edge_kinds: Option<&[GraphEdgeKind]>,
) -> Result<Vec<TraversalHop>, MemHopError> {
    if max_depth == 0 {
        return Ok(Vec::new());
    }
    let adjacency = build_adjacency_index(data, btree, graph_id, edge_kinds);
    Ok(bfs_with_adjacency(&adjacency, start_node, max_depth))
}

/// BFS traversal with adjacency cache support.
///
/// If the cache contains a valid adjacency list for the graph, it is reused.
/// Otherwise, the adjacency list is built from scratch and stored in the cache.
pub fn bfs_traversal_cached(
    data: &[u8],
    btree: &BTreeIndex,
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
        let adjacency = build_adjacency_index(data, btree, graph_id, edge_kinds);
        cache.insert(graph_id, edge_kinds, adjacency.clone());
        adjacency
    };

    Ok(bfs_with_adjacency(&adjacency, start_node, max_depth))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::test_helpers::*;

    #[test]
    fn test_bfs_traversal_one_hop() {
        let (mut mmap, mut header, mut btree, mut file) = create_test_mmap(64);
        let (_nodes, _edges) = build_test_graph(&mut mmap, &mut header, &mut btree, &mut file);

        let data: &[u8] = &mmap[..];
        let hops = bfs_traversal(data, &btree, 1, 101, 1, None).unwrap();

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
        let (mut mmap, mut header, mut btree, mut file) = create_test_mmap(64);
        let (_nodes, _edges) = build_test_graph(&mut mmap, &mut header, &mut btree, &mut file);

        let data: &[u8] = &mmap[..];
        let hops = bfs_traversal(data, &btree, 1, 101, 2, None).unwrap();

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
        let (mut mmap, mut header, mut btree, mut file) = create_test_mmap(64);
        let (_nodes, _edges) = build_test_graph(&mut mmap, &mut header, &mut btree, &mut file);

        let data: &[u8] = &mmap[..];
        let hops = bfs_traversal(data, &btree, 1, 101, 2, Some(&[GraphEdgeKind::Related])).unwrap();

        // Only Related edges: 101->102, 102->103
        assert_eq!(hops.len(), 2);
        assert!(hops.iter().all(|h| h.edge.kind == GraphEdgeKind::Related));
    }

    #[test]
    fn test_bfs_avoids_cycles() {
        let (mut mmap, mut header, mut btree, mut file) = create_test_mmap(64);

        // Create a triangle: 101 <-> 102 <-> 103 <-> 101
        for &nid in &[101u64, 102, 103] {
            add_node(
                &mut mmap,
                &mut header,
                &mut btree,
                create_test_node(nid, 1, &format!("node{}", nid)),
                &mut file,
                None,
                None,
            )
            .unwrap();
        }
        add_edge(
            &mut mmap,
            &mut header,
            &mut btree,
            create_test_edge(201, 1, GraphEdgeKind::Related, vec![101, 102]),
            &mut file,
            None,
        )
        .unwrap();
        add_edge(
            &mut mmap,
            &mut header,
            &mut btree,
            create_test_edge(202, 1, GraphEdgeKind::Related, vec![102, 103]),
            &mut file,
            None,
        )
        .unwrap();
        add_edge(
            &mut mmap,
            &mut header,
            &mut btree,
            create_test_edge(203, 1, GraphEdgeKind::Related, vec![103, 101]),
            &mut file,
            None,
        )
        .unwrap();

        let data: &[u8] = &mmap[..];
        let hops = bfs_traversal(data, &btree, 1, 101, 3, None).unwrap();

        // With cycle prevention there should be no duplicate to_nodes at each depth.
        // depth1: 101->102, 101->103
        // depth2: 102->103 (already visited), 103->102 (already visited)
        // depth3: nothing new
        assert_eq!(hops.len(), 2);
    }

    #[test]
    fn test_bfs_traversal_subgraph() {
        let (mut mmap, mut header, mut btree, mut file) = create_test_mmap(64);
        let (node_ids, _edge_ids) = build_test_graph(&mut mmap, &mut header, &mut btree, &mut file);

        let data: &[u8] = &mmap[..];
        let hops = bfs_traversal(data, &btree, 1, 101, 2, None).unwrap();

        let mut returned_node_ids: HashSet<u64> = HashSet::new();
        let mut returned_edge_ids: HashSet<u64> = HashSet::new();
        returned_node_ids.insert(101); // start node
        for hop in &hops {
            returned_node_ids.insert(hop.from_node);
            returned_node_ids.insert(hop.to_node);
            returned_edge_ids.insert(hop.edge.id_hash);
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
        let (mut mmap, mut header, mut btree, mut file) = create_test_mmap(64);

        add_node(
            &mut mmap,
            &mut header,
            &mut btree,
            create_test_node(101, 1, "island"),
            &mut file,
            None,
            None,
        )
        .unwrap();

        let data: &[u8] = &mmap[..];
        let hops = bfs_traversal(data, &btree, 1, 101, 2, None).unwrap();

        assert_eq!(hops.len(), 0); // No edges from isolated node
    }
}
