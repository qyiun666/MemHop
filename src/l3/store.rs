//! L3 Hypergraph Storage Layer
//!
//! Provides CRUD operations for HypergraphNode and HypergraphEdge,
//! and graph-level management (list, delete, count).

use crate::file::free_list::{allocate_from_free_list, free_page};
use crate::file::header::FileHeader;
use crate::file::page::PageHeader;
use crate::index::btree::BTreeIndex;
use crate::query::common::{format_hash, has_more, matches_keyword, pagination_params};
use crate::query::slot_io::get_slot_data;
use crate::query::types::*;
use crate::slot::hypergraph::{GraphEdgeKind, HypergraphEdge, HypergraphNode};
use crate::util::{PageType, PAGE_SIZE};
use crate::MemHopError;
use memmap2::MmapMut;
use std::collections::{hash_map::Entry, HashMap, HashSet, VecDeque};

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
///
/// # Behavior
/// - Allocates a page from the free list
/// - Writes PageHeader (type=HypergraphNode, layer=3)
/// - Serializes and writes node data after the header
/// - Registers node.id_hash → page_ref in the BTreeIndex
///
/// # Returns
/// The hex-formatted node ID string.
pub fn add_node(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    node: HypergraphNode,
) -> Result<String, MemHopError> {
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
    let page_id = allocate_from_free_list(mmap, header)?;
    let offset = (page_id as usize) * PAGE_SIZE;

    // Write page header
    let page_hdr = PageHeader::new(page_id, PageType::HypergraphNode, 3, 0xFFFFFFFF);
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

    // Register in BTreeIndex
    btree.insert(node.id_hash, (page_id as u64) << 16);

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
    let id_hash = crate::query::common::parse_id_to_hash(node_id);

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
///
/// # Behavior
/// 1. Find all edges in the same graph that reference this node
/// 2. Delete those edges (free pages + remove from BTree)
/// 3. Delete the node itself (free page + remove from BTree)
pub fn delete_node(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    node_id: &str,
) -> Result<(), MemHopError> {
    let id_hash = crate::query::common::parse_id_to_hash(node_id);

    // Find the node to get its graph_id
    let node = match get_node(mmap, btree, node_id)? {
        Some(n) => n,
        None => return Ok(()), // Already gone
    };

    let graph_id = node.graph_id;

    // Collect edges in the same graph that reference this node
    // Also collect page_ref to avoid second BTree lookup when deleting
    let mut edges_to_delete: Vec<(u64, u64)> = Vec::new(); // (edge_hash, page_ref)
    for (&eid, &page_ref) in btree.iter() {
        let data: &[u8] = &mmap[..];
        // Skip non-edge pages
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

    // Delete the edges (use stored page_ref to avoid second BTree lookup)
    for (edge_hash, page_ref) in &edges_to_delete {
        btree.remove(*edge_hash);
        let page_id = (page_ref >> 16) as u32;
        crate::file::free_list::free_page(mmap, header, page_id)?;
    }

    // Delete the node
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
    let page_id = allocate_from_free_list(mmap, header)?;
    let offset = (page_id as usize) * PAGE_SIZE;

    // Write page header
    let page_hdr = PageHeader::new(page_id, PageType::HypergraphEdge, 3, 0xFFFFFFFF);
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

    // Register in BTreeIndex
    btree.insert(edge.id_hash, (page_id as u64) << 16);

    Ok(format_hash(edge.id_hash))
}

/// Read a HypergraphEdge by its ID string.
pub fn get_edge(
    mmap: &MmapMut,
    btree: &BTreeIndex,
    edge_id: &str,
) -> Result<Option<HypergraphEdge>, MemHopError> {
    let id_hash = crate::query::common::parse_id_to_hash(edge_id);

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
) -> Result<(), MemHopError> {
    let id_hash = crate::query::common::parse_id_to_hash(edge_id);

    if let Some(page_ref) = btree.delete(id_hash) {
        let page_id = (page_ref >> 16) as u32;
        crate::file::free_list::free_page(mmap, header, page_id)?;
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
        // Skip non-node pages
        if page_type_of(data, page_ref) != Some(PageType::HypergraphNode as u16) {
            continue;
        }
        if let Some(slot_data) = get_slot_data(data, page_ref) {
            // Try to deserialize as HypergraphNode (skip non-node types)
            if let Ok(node) = HypergraphNode::deserialize(slot_data) {
                if node.graph_id != graph_id {
                    continue;
                }

                // Apply node_type filter
                if let Some(ref nt) = query.node_type {
                    if &node.node_type != nt {
                        continue;
                    }
                }

                // Apply keyword filter (match against title + content)
                if let Some(ref keyword) = query.keyword {
                    let combined = format!("{} {}", node.title, node.content);
                    if !matches_keyword(&combined, keyword) {
                        continue;
                    }
                }

                // Apply importance filter
                if let Some(min_imp) = query.min_importance {
                    if node.importance < min_imp {
                        continue;
                    }
                }

                all_nodes.push(node);
            }
        }
    }

    // Sort by importance descending
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
        // Skip non-edge pages
        if page_type_of(data, page_ref) != Some(PageType::HypergraphEdge as u16) {
            continue;
        }
        if let Some(slot_data) = get_slot_data(data, page_ref) {
            if let Ok(edge) = HypergraphEdge::deserialize(slot_data) {
                if edge.graph_id != graph_id {
                    continue;
                }

                // Apply kind filter
                if let Some(kind) = query.kind {
                    if edge.kind != kind {
                        continue;
                    }
                }

                // Apply node_id filter (edges containing a specific node)
                if let Some(ref nid) = query.node_id {
                    let node_hash = crate::query::common::parse_id_to_hash(nid);
                    if !edge.node_ids.contains(&node_hash) {
                        continue;
                    }
                }

                all_edges.push(edge);
            }
        }
    }

    // Sort by created_at descending
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

/// Count the number of nodes and edges belonging to a graph.
pub fn count_graph_elements(mmap: &MmapMut, btree: &BTreeIndex, graph_id: u64) -> (u32, u32) {
    let data: &[u8] = &mmap[..];
    let mut node_count: u32 = 0;
    let mut edge_count: u32 = 0;

    for (&_id, &page_ref) in btree.iter() {
        let pt = page_type_of(data, page_ref).unwrap_or(0);
        // Only process node and edge pages
        if pt != PageType::HypergraphNode as u16 && pt != PageType::HypergraphEdge as u16 {
            continue;
        }
        if let Some(slot_data) = get_slot_data(data, page_ref) {
            if pt == PageType::HypergraphNode as u16 {
                if let Ok(node) = HypergraphNode::deserialize(slot_data) {
                    if node.graph_id == graph_id {
                        node_count += 1;
                    }
                }
            } else if let Ok(edge) = HypergraphEdge::deserialize(slot_data) {
                if edge.graph_id == graph_id {
                    edge_count += 1;
                }
            }
        }
    }

    (node_count, edge_count)
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
    let graph_hash = crate::query::common::parse_id_to_hash(l3_id);

    // Verify graph exists
    if btree.search(graph_hash).is_none() {
        return Ok(()); // Already gone
    }

    // Step 1: Collect all node and edge IDs for this graph
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

    // Step 2: Delete all edges
    for edge_hash in &edge_hashes {
        if let Some(page_ref) = btree.delete(*edge_hash) {
            let page_id = (page_ref >> 16) as u32;
            free_page(mmap, header, page_id)?;
        }
    }

    // Step 3: Delete all nodes
    for node_hash in &node_hashes {
        if let Some(page_ref) = btree.delete(*node_hash) {
            let page_id = (page_ref >> 16) as u32;
            free_page(mmap, header, page_id)?;
        }
    }

    // Step 4: Delete the HypergraphSlot itself
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
        // Skip non-ContextSlot pages
        if page_type_of(data, page_ref) != Some(PageType::Context as u16) {
            continue;
        }
        if let Some(slot_data) = get_slot_data(data, page_ref) {
            if let Ok(ctx) = crate::slot::context::ContextSlot::deserialize_slot(slot_data) {
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

    // Read the full page
    let mut page_buf = vec![0u8; PAGE_SIZE];
    page_buf.copy_from_slice(&mmap[offset..offset + PAGE_SIZE]);

    // Deserialize ContextSlot from the data region (offset 32)
    let slot_data = &page_buf[32..];
    // Find the actual end of slot data (slot is bincode-serialized, end is padded)
    if let Ok(mut ctx) = crate::slot::context::ContextSlot::deserialize_slot(slot_data) {
        if ctx.l3_refs.contains(&graph_hash) {
            ctx.l3_refs.retain(|&h| h != graph_hash);
            ctx.updated_at = crate::query::common::now_ms();

            let ctx_bytes = ctx
                .serialize()
                .map_err(|e| MemHopError::Serialization(e.to_string()))?;
            if ctx_bytes.len() > PAGE_SIZE - 32 {
                return Err(MemHopError::Serialization(
                    "ContextSlot too large after l3_refs update".to_string(),
                ));
            }

            // Write the modified slot data back into the page buffer
            page_buf[32..32 + ctx_bytes.len()].copy_from_slice(&ctx_bytes);
            // Clear the rest of the data region
            if 32 + ctx_bytes.len() < PAGE_SIZE {
                page_buf[32 + ctx_bytes.len()..].fill(0);
            }

            // Write the full page back atomically
            mmap[offset..offset + PAGE_SIZE].copy_from_slice(&page_buf);
            return Ok(true);
        }
    }

    Ok(false)
}

/// Read neighbors of a node: find all edges referencing the node, collect other endpoints.
///
/// # Arguments
/// * `edge_kinds` - Optional filter for edge kinds. If None, all edge kinds are included.
pub fn read_node_neighbors(
    mmap: &MmapMut,
    btree: &BTreeIndex,
    node_id: &str,
    graph_id: u64,
    edge_kinds: Option<&[GraphEdgeKind]>,
) -> Result<Vec<HypergraphNode>, MemHopError> {
    let id_hash = crate::query::common::parse_id_to_hash(node_id);
    let data: &[u8] = &mmap[..];

    // Collect all edges in the graph that reference this node
    let mut neighbor_hashes: Vec<u64> = Vec::new();

    for (&_eid, &page_ref) in btree.iter() {
        // Skip non-edge pages
        if page_type_of(data, page_ref) != Some(PageType::HypergraphEdge as u16) {
            continue;
        }
        if let Some(slot_data) = get_slot_data(data, page_ref) {
            if let Ok(edge) = HypergraphEdge::deserialize(slot_data) {
                if edge.graph_id != graph_id || !edge.node_ids.contains(&id_hash) {
                    continue;
                }

                // Apply kind filter
                if let Some(kinds) = edge_kinds {
                    if !kinds.contains(&edge.kind) {
                        continue;
                    }
                }

                // Collect other node IDs in this edge
                for &nid in &edge.node_ids {
                    if nid != id_hash && !neighbor_hashes.contains(&nid) {
                        neighbor_hashes.push(nid);
                    }
                }
            }
        }
    }

    // Load neighbor nodes
    let mut neighbors = Vec::new();
    for &nh in &neighbor_hashes {
        if let Some(page_ref) = btree.search(nh) {
            if let Some(slot_data) = get_slot_data(data, page_ref) {
                if let Ok(node) = HypergraphNode::deserialize(slot_data) {
                    if node.graph_id == graph_id {
                        neighbors.push(node);
                    }
                }
            }
        }
    }

    Ok(neighbors)
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
pub fn bfs_traversal(
    data: &[u8],
    btree: &BTreeIndex,
    graph_id: u64,
    start_node: u64,
    max_depth: usize,
    edge_kinds: Option<&[GraphEdgeKind]>,
) -> Result<Vec<TraversalHop>, MemHopError> {
    let mut hops = Vec::new();
    if max_depth == 0 {
        return Ok(hops);
    }

    // 1. Pre-build adjacency index: scan btree once.
    // For each hyperedge in the target graph (and matching edge_kinds),
    // register the edge under every node it connects.
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

    // Track the BFS discovery depth of each node (start_node is depth 0).
    let mut node_depth: HashMap<u64, usize> = HashMap::new();
    // Track which edges have already been traversed (by id_hash) so each
    // edge is processed at most once across the entire traversal.
    let mut visited_edges: HashSet<u64> = HashSet::new();
    let mut queue: VecDeque<(u64, usize)> = VecDeque::new();

    node_depth.insert(start_node, 0);
    queue.push_back((start_node, 0));

    // 2. BFS using the pre-built adjacency index.
    while let Some((current_node, current_depth)) = queue.pop_front() {
        if current_depth >= max_depth {
            continue;
        }

        if let Some(edges) = adjacency.get(&current_node) {
            for (edge, other_ids) in edges {
                // Skip edges that have already been traversed from any endpoint
                if !visited_edges.insert(edge.id_hash) {
                    continue;
                }

                // Emit a hop to every other endpoint of the hyperedge,
                // but only if the target was discovered at a depth >= the
                // hop depth (current_depth + 1).  This prevents back-edges
                // to ancestors (shallower nodes) from producing hops.
                let hop_depth = current_depth + 1;
                for &to_node in other_ids {
                    if to_node == current_node {
                        continue;
                    }

                    match node_depth.get(&to_node) {
                        Some(&d) if d < hop_depth => {
                            // Back-edge to an ancestor — skip
                        }
                        _ => {
                            hops.push(TraversalHop {
                                depth: hop_depth,
                                from_node: current_node,
                                edge: edge.clone(),
                                to_node,
                            });
                        }
                    }

                    // Enqueue only newly-discovered nodes
                    if let Entry::Vacant(e) = node_depth.entry(to_node) {
                        e.insert(hop_depth);
                        queue.push_back((to_node, hop_depth));
                    }
                }
            }
        }
    }

    Ok(hops)
}

/// Extract a `Subgraph` reachable from `start_node` within `max_depth` hops.
///
/// Performs an unfiltered BFS traversal, then loads all referenced nodes and
/// deduplicates edges by `id_hash`.  The start node is always included in the
/// result, even if the traversal produces no hops.
pub fn extract_subgraph(
    data: &[u8],
    btree: &BTreeIndex,
    graph_id: u64,
    start_node: u64,
    max_depth: usize,
) -> Result<Subgraph, MemHopError> {
    let hops = bfs_traversal(data, btree, graph_id, start_node, max_depth, None)?;

    let mut node_hashes: HashSet<u64> = HashSet::new();
    let mut edge_ids: HashSet<u64> = HashSet::new();
    let mut edges: Vec<HypergraphEdge> = Vec::new();

    node_hashes.insert(start_node);

    for hop in &hops {
        node_hashes.insert(hop.from_node);
        node_hashes.insert(hop.to_node);

        if edge_ids.insert(hop.edge.id_hash) {
            edges.push(hop.edge.clone());
        }
    }

    // Load unique nodes
    let mut nodes: Vec<HypergraphNode> = Vec::new();
    for &node_hash in &node_hashes {
        if let Some(page_ref) = btree.search(node_hash) {
            if let Some(slot_data) = get_slot_data(data, page_ref) {
                if let Ok(node) = HypergraphNode::deserialize(slot_data) {
                    if node.graph_id == graph_id {
                        nodes.push(node);
                    }
                }
            }
        }
    }

    Ok(Subgraph { nodes, edges })
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::file::header::FileHeader;
    use crate::util::PAGE_SIZE;
    use memmap2::MmapMut;
    use std::fs::File;
    use std::io::Write;

    fn create_test_mmap(pages: usize) -> (MmapMut, FileHeader, BTreeIndex) {
        let temp_file = tempfile::NamedTempFile::new().unwrap();
        let path = temp_file.path();
        let mut file = File::create(path).unwrap();
        file.write_all(&vec![0u8; PAGE_SIZE * pages]).unwrap();
        drop(file);

        let file = std::fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open(path)
            .unwrap();

        let mut mmap = unsafe { MmapMut::map_mut(&file).unwrap() };
        let mut header = FileHeader::new(768);
        header.page_count = pages as u32;
        crate::file::free_list::init_free_list(&mut header).unwrap();

        for page_id in (2..pages as u32).rev() {
            crate::file::free_list::free_page(&mut mmap, &mut header, page_id).unwrap();
        }

        let btree = BTreeIndex::new();
        (mmap, header, btree)
    }

    fn create_test_node(id_hash: u64, graph_id: u64, title: &str) -> HypergraphNode {
        HypergraphNode {
            id_hash,
            graph_id,
            title: title.to_string(),
            node_type: "concept".to_string(),
            content: format!("content of {}", title),
            keywords: vec![],
            source_ref: None,
            importance: 0.5,
            created_at: 0,
            updated_at: 0,
            version: 1,
        }
    }

    fn create_test_edge(
        id_hash: u64,
        graph_id: u64,
        kind: GraphEdgeKind,
        node_ids: Vec<u64>,
    ) -> HypergraphEdge {
        HypergraphEdge {
            id_hash,
            graph_id,
            kind,
            node_ids,
            weight: 1.0,
            label: None,
            created_at: 0,
        }
    }

    /// Build a small graph:
    /// n1 --e1(Related)--> n2 --e2(Related)--> n3 --e3(Dependency)--> n4
    /// n1 --e4(Causal)--> n3, n5   (hyperedge, 3 nodes)
    fn build_test_graph(
        mmap: &mut MmapMut,
        header: &mut FileHeader,
        btree: &mut BTreeIndex,
    ) -> (Vec<u64>, Vec<u64>) {
        let graph_id = 1u64;
        let node_ids = vec![101u64, 102, 103, 104, 105];
        let edge_ids = vec![201u64, 202, 203, 204];

        for &nid in &node_ids {
            add_node(
                mmap,
                header,
                btree,
                create_test_node(nid, graph_id, &format!("node{}", nid)),
            )
            .unwrap();
        }

        add_edge(
            mmap,
            header,
            btree,
            create_test_edge(201, graph_id, GraphEdgeKind::Related, vec![101, 102]),
        )
        .unwrap();
        add_edge(
            mmap,
            header,
            btree,
            create_test_edge(202, graph_id, GraphEdgeKind::Related, vec![102, 103]),
        )
        .unwrap();
        add_edge(
            mmap,
            header,
            btree,
            create_test_edge(203, graph_id, GraphEdgeKind::Dependency, vec![103, 104]),
        )
        .unwrap();
        add_edge(
            mmap,
            header,
            btree,
            create_test_edge(204, graph_id, GraphEdgeKind::Causal, vec![101, 103, 105]),
        )
        .unwrap();

        (node_ids, edge_ids)
    }

    #[test]
    fn test_bfs_traversal_one_hop() {
        let (mut mmap, mut header, mut btree) = create_test_mmap(64);
        let (_nodes, _edges) = build_test_graph(&mut mmap, &mut header, &mut btree);

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
        let (mut mmap, mut header, mut btree) = create_test_mmap(64);
        let (_nodes, _edges) = build_test_graph(&mut mmap, &mut header, &mut btree);

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
        let (mut mmap, mut header, mut btree) = create_test_mmap(64);
        let (_nodes, _edges) = build_test_graph(&mut mmap, &mut header, &mut btree);

        let data: &[u8] = &mmap[..];
        let hops = bfs_traversal(data, &btree, 1, 101, 2, Some(&[GraphEdgeKind::Related])).unwrap();

        // Only Related edges: 101->102, 102->103
        assert_eq!(hops.len(), 2);
        assert!(hops.iter().all(|h| h.edge.kind == GraphEdgeKind::Related));
    }

    #[test]
    fn test_bfs_avoids_cycles() {
        let (mut mmap, mut header, mut btree) = create_test_mmap(64);

        // Create a triangle: 101 <-> 102 <-> 103 <-> 101
        for &nid in &[101u64, 102, 103] {
            add_node(
                &mut mmap,
                &mut header,
                &mut btree,
                create_test_node(nid, 1, &format!("node{}", nid)),
            )
            .unwrap();
        }
        add_edge(
            &mut mmap,
            &mut header,
            &mut btree,
            create_test_edge(201, 1, GraphEdgeKind::Related, vec![101, 102]),
        )
        .unwrap();
        add_edge(
            &mut mmap,
            &mut header,
            &mut btree,
            create_test_edge(202, 1, GraphEdgeKind::Related, vec![102, 103]),
        )
        .unwrap();
        add_edge(
            &mut mmap,
            &mut header,
            &mut btree,
            create_test_edge(203, 1, GraphEdgeKind::Related, vec![103, 101]),
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
    fn test_extract_subgraph() {
        let (mut mmap, mut header, mut btree) = create_test_mmap(64);
        let (node_ids, _edge_ids) = build_test_graph(&mut mmap, &mut header, &mut btree);

        let data: &[u8] = &mmap[..];
        let subgraph = extract_subgraph(data, &btree, 1, 101, 2).unwrap();

        let returned_node_ids: HashSet<u64> = subgraph.nodes.iter().map(|n| n.id_hash).collect();
        let returned_edge_ids: HashSet<u64> = subgraph.edges.iter().map(|e| e.id_hash).collect();

        // BFS discovers 4 of 5 edges: edge 202 (102-103) is never traversed
        // because hyperedge 204 discovers node 103 at depth 1, making the
        // 102->103 connection a back-edge that is skipped.
        let expected_edge_ids: HashSet<u64> = [201u64, 203, 204].iter().copied().collect();
        assert_eq!(returned_node_ids, node_ids.iter().copied().collect());
        assert_eq!(returned_edge_ids, expected_edge_ids);
    }

    #[test]
    fn test_extract_subgraph_start_node_only() {
        let (mut mmap, mut header, mut btree) = create_test_mmap(64);

        add_node(
            &mut mmap,
            &mut header,
            &mut btree,
            create_test_node(101, 1, "island"),
        )
        .unwrap();

        let data: &[u8] = &mmap[..];
        let subgraph = extract_subgraph(data, &btree, 1, 101, 2).unwrap();

        assert_eq!(subgraph.nodes.len(), 1);
        assert_eq!(subgraph.nodes[0].id_hash, 101);
        assert!(subgraph.edges.is_empty());
    }
}
