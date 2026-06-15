//! L3 Hypergraph View
//!
//! Provides graph traversal and query capabilities for L3 hypergraphs.
//! Includes neighbor queries, BFS, path finding, and subgraph extraction.

use crate::index::btree::BTreeIndex;
use crate::l3::store::page_type_of;
use crate::query::slot_io::get_slot_data;
use crate::query::types::{Subgraph, TraversalHop};
use crate::slot::hypergraph::{GraphEdgeKind, HypergraphEdge, HypergraphNode};
use crate::util::PageType;
use crate::MemHopError;
use memmap2::MmapMut;
use std::collections::{HashSet, VecDeque};

/// Get all edges that reference a specific node
pub fn get_node_edges(
    mmap: &MmapMut,
    btree: &BTreeIndex,
    node_id: u64,
    graph_id: u64,
) -> Result<Vec<HypergraphEdge>, MemHopError> {
    let data: &[u8] = &mmap[..];
    let mut edges = Vec::new();

    for (&_eid, &page_ref) in btree.iter() {
        // Skip non-edge pages
        if page_type_of(data, page_ref) != Some(PageType::HypergraphEdge as u16) {
            continue;
        }
        if let Some(slot_data) = get_slot_data(data, page_ref) {
            if let Ok(edge) = HypergraphEdge::deserialize(slot_data) {
                if edge.graph_id == graph_id && edge.node_ids.contains(&node_id) {
                    edges.push(edge);
                }
            }
        }
    }

    Ok(edges)
}

/// BFS traversal from a start node
///
/// # Arguments
/// * `start` - Starting node ID
/// * `graph_id` - Graph ID to constrain traversal
/// * `max_depth` - Maximum depth to traverse
/// * `max_nodes` - Maximum number of nodes to visit
/// * `edge_kinds` - Optional filter for edge kinds
///
/// # Returns
/// Vector of TraversalHop representing the BFS tree
pub fn bfs(
    mmap: &MmapMut,
    btree: &BTreeIndex,
    start: u64,
    graph_id: u64,
    max_depth: usize,
    max_nodes: usize,
    edge_kinds: Option<&[GraphEdgeKind]>,
) -> Result<Vec<TraversalHop>, MemHopError> {
    let data: &[u8] = &mmap[..];
    let mut visited: HashSet<u64> = HashSet::new();
    let mut queue: VecDeque<(u64, usize)> = VecDeque::new(); // (node_id, depth)
    let mut hops: Vec<TraversalHop> = Vec::new();

    visited.insert(start);
    queue.push_back((start, 0));

    while let Some((current_node, depth)) = queue.pop_front() {
        if depth >= max_depth {
            continue;
        }

        // Find all edges referencing this node
        for (&_eid, &page_ref) in btree.iter() {
            // Skip non-edge pages
            if page_type_of(data, page_ref) != Some(PageType::HypergraphEdge as u16) {
                continue;
            }
            if let Some(slot_data) = get_slot_data(data, page_ref) {
                if let Ok(edge) = HypergraphEdge::deserialize(slot_data) {
                    if edge.graph_id != graph_id || !edge.node_ids.contains(&current_node) {
                        continue;
                    }

                    // Apply kind filter
                    if let Some(kinds) = edge_kinds {
                        if !kinds.contains(&edge.kind) {
                            continue;
                        }
                    }

                    // Process other nodes in this edge
                    for &neighbor_id in &edge.node_ids {
                        if neighbor_id == current_node {
                            continue;
                        }

                        if !visited.contains(&neighbor_id) {
                            // Check max_nodes BEFORE inserting to avoid overshooting
                            if visited.len() >= max_nodes {
                                break;
                            }
                            visited.insert(neighbor_id);
                            queue.push_back((neighbor_id, depth + 1));

                            hops.push(TraversalHop {
                                depth: depth + 1,
                                from_node: current_node,
                                edge: edge.clone(),
                                to_node: neighbor_id,
                            });
                        }
                    }
                }
            }
        }
    }

    Ok(hops)
}

/// Find shortest path between two nodes using BFS
///
/// # Arguments
/// * `from` - Starting node ID
/// * `to` - Target node ID
/// * `graph_id` - Graph ID to constrain search
/// * `max_hops` - Maximum number of hops to search
///
/// # Returns
/// Optional vector of TraversalHop representing the path, or None if no path exists
pub fn find_path(
    mmap: &MmapMut,
    btree: &BTreeIndex,
    from: u64,
    to: u64,
    graph_id: u64,
    max_hops: usize,
) -> Result<Option<Vec<TraversalHop>>, MemHopError> {
    let data: &[u8] = &mmap[..];
    let mut visited: HashSet<u64> = HashSet::new();
    let mut queue: VecDeque<(u64, Vec<TraversalHop>)> = VecDeque::new();

    visited.insert(from);
    queue.push_back((from, Vec::new()));

    while let Some((current_node, path)) = queue.pop_front() {
        if path.len() >= max_hops {
            continue;
        }

        // Find all edges referencing this node
        for (&_eid, &page_ref) in btree.iter() {
            // Skip non-edge pages
            if page_type_of(data, page_ref) != Some(PageType::HypergraphEdge as u16) {
                continue;
            }
            if let Some(slot_data) = get_slot_data(data, page_ref) {
                if let Ok(edge) = HypergraphEdge::deserialize(slot_data) {
                    if edge.graph_id != graph_id || !edge.node_ids.contains(&current_node) {
                        continue;
                    }

                    // Process other nodes in this edge
                    for &neighbor_id in &edge.node_ids {
                        if neighbor_id == current_node {
                            continue;
                        }

                        if neighbor_id == to {
                            // Found target
                            let mut final_path = path.clone();
                            final_path.push(TraversalHop {
                                depth: path.len() + 1,
                                from_node: current_node,
                                edge: edge.clone(),
                                to_node: neighbor_id,
                            });
                            return Ok(Some(final_path));
                        }

                        if !visited.contains(&neighbor_id) {
                            visited.insert(neighbor_id);
                            let mut new_path = path.clone();
                            new_path.push(TraversalHop {
                                depth: path.len() + 1,
                                from_node: current_node,
                                edge: edge.clone(),
                                to_node: neighbor_id,
                            });
                            queue.push_back((neighbor_id, new_path));
                        }
                    }
                }
            }
        }
    }

    Ok(None)
}

/// Extract subgraph from seed nodes within specified depth
///
/// # Arguments
/// * `seed_nodes` - Starting node IDs
/// * `graph_id` - Graph ID to constrain extraction
/// * `depth` - Maximum depth to extract
///
/// # Returns
/// Subgraph containing all nodes and edges within the specified depth
pub fn subgraph(
    mmap: &MmapMut,
    btree: &BTreeIndex,
    seed_nodes: &[u64],
    graph_id: u64,
    depth: usize,
) -> Result<Subgraph, MemHopError> {
    let data: &[u8] = &mmap[..];
    let mut visited_nodes: HashSet<u64> = HashSet::new();
    let mut visited_edges: HashSet<u64> = HashSet::new();
    let mut queue: VecDeque<(u64, usize)> = VecDeque::new();

    // Initialize with seed nodes
    for &seed in seed_nodes {
        visited_nodes.insert(seed);
        queue.push_back((seed, 0));
    }

    // BFS to collect nodes and edges
    while let Some((current_node, current_depth)) = queue.pop_front() {
        if current_depth >= depth {
            continue;
        }

        // Find all edges referencing this node
        for (&eid, &page_ref) in btree.iter() {
            if let Some(slot_data) = get_slot_data(data, page_ref) {
                if let Ok(edge) = HypergraphEdge::deserialize(slot_data) {
                    if edge.graph_id != graph_id || !edge.node_ids.contains(&current_node) {
                        continue;
                    }

                    if !visited_edges.contains(&eid) {
                        visited_edges.insert(eid);

                        // Add other nodes in this edge
                        for &neighbor_id in &edge.node_ids {
                            if !visited_nodes.contains(&neighbor_id) {
                                visited_nodes.insert(neighbor_id);
                                queue.push_back((neighbor_id, current_depth + 1));
                            }
                        }
                    }
                }
            }
        }
    }

    // Load all visited nodes
    let mut nodes = Vec::new();
    for &node_id in &visited_nodes {
        if let Some(page_ref) = btree.search(node_id) {
            if let Some(slot_data) = get_slot_data(data, page_ref) {
                if let Ok(node) = HypergraphNode::deserialize(slot_data) {
                    if node.graph_id == graph_id {
                        nodes.push(node);
                    }
                }
            }
        }
    }

    // Load all visited edges
    let mut edges = Vec::new();
    for &edge_id in &visited_edges {
        if let Some(page_ref) = btree.search(edge_id) {
            if let Some(slot_data) = get_slot_data(data, page_ref) {
                if let Ok(edge) = HypergraphEdge::deserialize(slot_data) {
                    if edge.graph_id == graph_id {
                        edges.push(edge);
                    }
                }
            }
        }
    }

    Ok(Subgraph { nodes, edges })
}

/// Export hypergraph to JSON format
pub fn export_json(
    mmap: &MmapMut,
    btree: &BTreeIndex,
    graph_id: u64,
) -> Result<String, MemHopError> {
    let data: &[u8] = &mmap[..];
    let mut nodes = Vec::new();
    let mut edges = Vec::new();

    // Collect all nodes and edges in the graph
    for (&_id, &page_ref) in btree.iter() {
        let pt = page_type_of(data, page_ref).unwrap_or(0);
        match pt {
            t if t == PageType::HypergraphNode as u16 => {
                if let Some(slot_data) = get_slot_data(data, page_ref) {
                    if let Ok(node) = HypergraphNode::deserialize(slot_data) {
                        if node.graph_id == graph_id {
                            nodes.push(node);
                        }
                    }
                }
            }
            t if t == PageType::HypergraphEdge as u16 => {
                if let Some(slot_data) = get_slot_data(data, page_ref) {
                    if let Ok(edge) = HypergraphEdge::deserialize(slot_data) {
                        if edge.graph_id == graph_id {
                            edges.push(edge);
                        }
                    }
                }
            }
            _ => {}
        }
    }

    // Build JSON structure
    let json = serde_json::json!({
        "graph_id": format!("{:016x}", graph_id),
        "nodes": nodes.iter().map(|n| {
            serde_json::json!({
                "id": format!("{:016x}", n.id_hash),
                "title": n.title,
                "type": n.node_type,
                "content": n.content,
                "keywords": n.keywords,
                "importance": n.importance,
            })
        }).collect::<Vec<_>>(),
        "edges": edges.iter().map(|e| {
            serde_json::json!({
                "id": format!("{:016x}", e.id_hash),
                "kind": format!("{:?}", e.kind),
                "node_ids": e.node_ids.iter().map(|id| format!("{:016x}", id)).collect::<Vec<_>>(),
                "weight": e.weight,
                "label": e.label,
            })
        }).collect::<Vec<_>>(),
    });

    serde_json::to_string_pretty(&json).map_err(|e| MemHopError::Serialization(e.to_string()))
}

/// Export hypergraph to DOT format (Graphviz)
pub fn export_dot(
    mmap: &MmapMut,
    btree: &BTreeIndex,
    graph_id: u64,
) -> Result<String, MemHopError> {
    let data: &[u8] = &mmap[..];
    let mut nodes = Vec::new();
    let mut edges = Vec::new();

    // Collect all nodes and edges in the graph
    for (&_id, &page_ref) in btree.iter() {
        let pt = page_type_of(data, page_ref).unwrap_or(0);
        match pt {
            t if t == PageType::HypergraphNode as u16 => {
                if let Some(slot_data) = get_slot_data(data, page_ref) {
                    if let Ok(node) = HypergraphNode::deserialize(slot_data) {
                        if node.graph_id == graph_id {
                            nodes.push(node);
                        }
                    }
                }
            }
            t if t == PageType::HypergraphEdge as u16 => {
                if let Some(slot_data) = get_slot_data(data, page_ref) {
                    if let Ok(edge) = HypergraphEdge::deserialize(slot_data) {
                        if edge.graph_id == graph_id {
                            edges.push(edge);
                        }
                    }
                }
            }
            _ => {}
        }
    }

    // Build DOT format
    let mut dot = String::new();
    dot.push_str("digraph L3Hypergraph {\n");
    dot.push_str("  rankdir=LR;\n");
    dot.push_str("  node [shape=box];\n\n");

    // Collect exported node ID set for dangling edge validation
    let exported_nodes: HashSet<u64> = nodes.iter().map(|n| n.id_hash).collect();

    // Add nodes
    for node in &nodes {
        let label = format!("{} ({})", node.title, node.node_type);
        dot.push_str(&format!(
            "  \"{:016x}\" [label=\"{}\"];\n",
            node.id_hash,
            label.replace('"', "\\\"")
        ));
    }

    dot.push('\n');

    // Add edges (filter dangling references)
    for edge in &edges {
        // Filter node_ids to only those present in the exported node set
        let valid_nodes: Vec<u64> = edge
            .node_ids
            .iter()
            .filter(|id| exported_nodes.contains(id))
            .copied()
            .collect();

        if valid_nodes.len() >= 2 {
            // For hyperedges (more than 2 nodes), create a virtual node
            if valid_nodes.len() > 2 {
                let edge_label = edge
                    .label
                    .clone()
                    .unwrap_or_else(|| format!("{:?}", edge.kind));
                dot.push_str(&format!(
                    "  \"edge_{:016x}\" [label=\"{}\", shape=ellipse];\n",
                    edge.id_hash,
                    edge_label.replace('"', "\\\"")
                ));
                for &node_id in &valid_nodes {
                    dot.push_str(&format!(
                        "  \"{:016x}\" -> \"edge_{:016x}\";\n",
                        node_id, edge.id_hash
                    ));
                }
            } else {
                // Regular edge between two nodes
                let edge_label = edge
                    .label
                    .clone()
                    .unwrap_or_else(|| format!("{:?}", edge.kind));
                dot.push_str(&format!(
                    "  \"{:016x}\" -> \"{:016x}\" [label=\"{}\"];\n",
                    valid_nodes[0],
                    valid_nodes[1],
                    edge_label.replace('"', "\\\"")
                ));
            }
        }
    }

    dot.push_str("}\n");
    Ok(dot)
}
