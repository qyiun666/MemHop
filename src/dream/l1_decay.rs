// L1 associative decay stage for Dream Pipeline
//
// Time-decays ContextNode.importance and HyperedgeSlot.weight, pruning weak
// associations according to configurable thresholds. Emotional memories decay
// slower via apply_emotional_boost when emotion data is available.

use crate::dream::emotion::apply_emotional_boost;
use crate::file::free_list::free_page;
use crate::file::header::FileHeader;
use crate::file::page::write_page_data;
use crate::index::btree::BTreeIndex;
use crate::query::common::now_ms;
use crate::slot::context_node::ContextNode;
use crate::slot::hyperedge::HyperedgeSlot;
use crate::util::PageType;
use crate::MemHopError;
use memmap2::MmapMut;
use std::collections::{HashMap, HashSet};

/// Decay constants (per hour)
const LAMBDA_NODE: f32 = 0.01;
const LAMBDA_EDGE: f32 = 0.02;

/// Prune thresholds
const NODE_REMOVE_THRESHOLD: f32 = 0.05;
const NODE_PRUNE_EDGES_THRESHOLD: f32 = 0.15;
const EDGE_REMOVE_THRESHOLD: f32 = 0.05;
const MIN_EDGE_NODES: usize = 2;

/// Report produced by the L1 decay stage
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct L1DecayReport {
    /// Number of nodes whose importance was updated (including edge pruning)
    pub decayed_nodes: usize,
    /// Number of edge pointers removed from ContextNodes
    pub pruned_edges: usize,
    /// Number of ContextNodes removed due to low importance
    pub removed_nodes: usize,
    /// Number of HyperedgeSlots removed due to low weight or underpopulation
    pub removed_edges: usize,
}

/// Run time-based decay over the L1 hypergraph skeleton.
///
/// Scans all ContextNode and HyperedgeSlot pages in `btree`, updates their
/// importance/weight with exponential decay, and removes/prunes entries that
/// fall below the configured thresholds.
pub fn decay_l1_network(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
) -> Result<L1DecayReport, MemHopError> {
    let now = now_ms();
    let mut report = L1DecayReport {
        decayed_nodes: 0,
        pruned_edges: 0,
        removed_nodes: 0,
        removed_edges: 0,
    };

    let page_count = header.page_count;
    let entries: Vec<(u64, u64)> = btree.iter().map(|(k, v)| (*k, *v)).collect();

    // -------------------------------------------------------------------------
    // Phase 1: decay/prune ContextNodes
    // -------------------------------------------------------------------------
    let mut removed_node_ids: HashSet<u64> = HashSet::new();
    // Map from edge id to the set of node ids that cleared their reference to it.
    let mut cleared_edges: HashMap<u64, HashSet<u64>> = HashMap::new();

    for (id_hash, page_ref) in entries {
        let page_id = decode_page_id(page_ref);
        if page_id >= page_count {
            continue;
        }

        if page_type_of(&mmap[..], page_id) != Some(PageType::ContextNode) {
            continue;
        }

        let slot_data = match crate::query::slot_io::get_slot_data(&mmap[..], page_ref) {
            Some(d) => d,
            None => continue,
        };

        let mut node = match ContextNode::deserialize(slot_data) {
            Ok(n) => n,
            Err(e) => {
                return Err(MemHopError::Serialization(format!(
                    "ContextNode deserialize failed: {}",
                    e
                )));
            }
        };

        let dt_hours = dt_hours_from(now, node.updated_at);
        let lambda = apply_emotional_boost(LAMBDA_NODE, node.valence, node.arousal);
        let new_importance = node.importance * (-lambda * dt_hours).exp();

        if new_importance < NODE_REMOVE_THRESHOLD {
            // Remove the entire node and all its edge references.
            btree.remove(id_hash);
            zero_page(mmap, page_id)?;
            free_page(mmap, header, page_id)?;
            removed_node_ids.insert(id_hash);
            report.removed_nodes += 1;
            continue;
        }

        node.importance = new_importance;

        if new_importance < NODE_PRUNE_EDGES_THRESHOLD {
            report.pruned_edges += node.edge_ptrs.len();
            for edge_hash in &node.edge_ptrs {
                cleared_edges.entry(*edge_hash).or_default().insert(id_hash);
            }
            node.edge_ptrs.clear();
        }

        node.updated_at = now;
        write_node(mmap, page_id, &node)?;
        report.decayed_nodes += 1;
    }

    // -------------------------------------------------------------------------
    // Phase 2: decay/prune HyperedgeSlots and clean stale node references
    // -------------------------------------------------------------------------
    // First process edges whose references were cleared from nodes in Phase 1.
    let mut edges_removed_by_clear: HashSet<u64> = HashSet::new();
    for (edge_id, node_ids) in &cleared_edges {
        for node_id in node_ids {
            if remove_node_from_edge(mmap, btree, header, *edge_id, *node_id)? {
                edges_removed_by_clear.insert(*edge_id);
                break;
            }
        }
    }
    report.removed_edges += edges_removed_by_clear.len();

    let edge_entries: Vec<(u64, u64)> = btree.iter().map(|(k, v)| (*k, *v)).collect();

    for (id_hash, page_ref) in edge_entries {
        if edges_removed_by_clear.contains(&id_hash) {
            continue;
        }

        let page_id = decode_page_id(page_ref);
        if page_id >= page_count {
            continue;
        }

        if page_type_of(&mmap[..], page_id) != Some(PageType::Hyperedge) {
            continue;
        }

        let slot_data = match crate::query::slot_io::get_slot_data(&mmap[..], page_ref) {
            Some(d) => d,
            None => continue,
        };

        let mut edge = match HyperedgeSlot::deserialize(slot_data) {
            Ok(e) => e,
            Err(err) => {
                return Err(MemHopError::Serialization(format!(
                    "HyperedgeSlot deserialize failed: {}",
                    err
                )));
            }
        };

        let dt_hours = dt_hours_from(now, edge.updated_at);
        let new_weight = edge.weight * (-LAMBDA_EDGE * dt_hours).exp();

        // Clean references to nodes removed in phase 1.
        edge.node_ptrs.retain(|ptr| !removed_node_ids.contains(ptr));

        if edge.node_ptrs.len() < MIN_EDGE_NODES || new_weight < EDGE_REMOVE_THRESHOLD {
            // Before freeing the edge, clean references from surviving nodes.
            for &node_ptr in &edge.node_ptrs {
                remove_edge_from_node(mmap, btree, node_ptr, id_hash)?;
            }
            btree.remove(id_hash);
            zero_page(mmap, page_id)?;
            free_page(mmap, header, page_id)?;
            report.removed_edges += 1;
            continue;
        }

        edge.weight = new_weight;
        edge.updated_at = now;
        write_edge(mmap, page_id, &edge)?;
    }

    Ok(report)
}

#[inline]
fn decode_page_id(page_ref: u64) -> u32 {
    (page_ref >> 16) as u32
}

#[inline]
fn page_type_of(data: &[u8], page_id: u32) -> Option<PageType> {
    let offset = (page_id as usize) * crate::util::PAGE_SIZE + 4;
    if offset + 2 > data.len() {
        return None;
    }
    let pt = u16::from_le_bytes([data[offset], data[offset + 1]]);
    PageType::from_u16(pt)
}

#[inline]
fn dt_hours_from(now_ms: i64, updated_at_ms: i64) -> f32 {
    let dt_ms = now_ms.saturating_sub(updated_at_ms).max(0) as f32;
    dt_ms / 3_600_000.0
}

#[inline]
fn zero_page(mmap: &mut MmapMut, page_id: u32) -> Result<(), MemHopError> {
    let offset = (page_id as usize) * crate::util::PAGE_SIZE;
    if offset + crate::util::PAGE_SIZE > mmap.len() {
        return Err(MemHopError::PageNotFound(page_id));
    }
    mmap[offset..offset + crate::util::PAGE_SIZE].fill(0);
    Ok(())
}

#[inline]
fn write_node(mmap: &mut MmapMut, page_id: u32, node: &ContextNode) -> Result<(), MemHopError> {
    let data = node
        .serialize()
        .map_err(|e| MemHopError::Serialization(format!("ContextNode serialize failed: {}", e)))?;
    write_page_data(mmap, page_id, &data)
}

#[inline]
fn write_edge(mmap: &mut MmapMut, page_id: u32, edge: &HyperedgeSlot) -> Result<(), MemHopError> {
    let data = edge.serialize().map_err(|e| {
        MemHopError::Serialization(format!("HyperedgeSlot serialize failed: {}", e))
    })?;
    write_page_data(mmap, page_id, &data)
}

/// Remove `edge_id` from the `edge_ptrs` of the ContextNode identified by `node_id`.
/// If the node does not exist or does not reference the edge, this is a no-op.
pub(crate) fn remove_edge_from_node(
    mmap: &mut MmapMut,
    btree: &BTreeIndex,
    node_id: u64,
    edge_id: u64,
) -> Result<(), MemHopError> {
    if let Some(page_ref) = btree.search(node_id) {
        let page_id = decode_page_id(page_ref);
        if page_type_of(&mmap[..], page_id) != Some(PageType::ContextNode) {
            return Ok(());
        }
        if let Some(slot_data) = crate::query::slot_io::get_slot_data(&mmap[..], page_ref) {
            if let Ok(mut node) = ContextNode::deserialize(slot_data) {
                if node.edge_ptrs.contains(&edge_id) {
                    node.edge_ptrs.retain(|&e| e != edge_id);
                    write_node(mmap, page_id, &node)?;
                }
            }
        }
    }
    Ok(())
}

/// Remove `node_id` from the `node_ptrs` of the HyperedgeSlot identified by `edge_id`.
/// Returns `true` if the edge was removed entirely because it became underpopulated.
/// Returns `false` if the edge was modified but kept, or if it did not exist.
pub(crate) fn remove_node_from_edge(
    mmap: &mut MmapMut,
    btree: &mut BTreeIndex,
    header: &mut FileHeader,
    edge_id: u64,
    node_id: u64,
) -> Result<bool, MemHopError> {
    if let Some(page_ref) = btree.search(edge_id) {
        let page_id = decode_page_id(page_ref);
        if page_type_of(&mmap[..], page_id) != Some(PageType::Hyperedge) {
            return Ok(false);
        }
        if let Some(slot_data) = crate::query::slot_io::get_slot_data(&mmap[..], page_ref) {
            if let Ok(mut edge) = HyperedgeSlot::deserialize(slot_data) {
                if !edge.node_ptrs.contains(&node_id) {
                    return Ok(false);
                }
                edge.node_ptrs.retain(|&n| n != node_id);
                if edge.node_ptrs.len() < MIN_EDGE_NODES {
                    // Edge underpopulated: remove it and clean surviving nodes.
                    for &surviving_node in &edge.node_ptrs {
                        remove_edge_from_node(mmap, btree, surviving_node, edge_id)?;
                    }
                    btree.remove(edge_id);
                    zero_page(mmap, page_id)?;
                    free_page(mmap, header, page_id)?;
                    return Ok(true);
                }
                write_edge(mmap, page_id, &edge)?;
            }
        }
    }
    Ok(false)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::slot::hyperedge::HyperedgeKind;
    use crate::util::PAGE_SIZE;
    use memmap2::MmapMut;
    use std::fs::File;
    use std::io::Write;

    fn create_mmap(pages: usize) -> (MmapMut, FileHeader, BTreeIndex) {
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
        // Initialize two header pages so page_count starts after them.
        let mut header = FileHeader::new(768);
        header.page_count = pages as u32;
        crate::file::free_list::init_free_list(&mut header).unwrap();

        // Mark remaining pages as free so allocate_page can reuse them.
        for page_id in (2..pages as u32).rev() {
            crate::file::free_list::free_page(&mut mmap, &mut header, page_id).unwrap();
        }

        let btree = BTreeIndex::new();
        (mmap, header, btree)
    }

    fn allocate_context_node_page(
        mmap: &mut MmapMut,
        header: &mut FileHeader,
        btree: &mut BTreeIndex,
        id_hash: u64,
        importance: f32,
        updated_at: i64,
        edge_ptrs: Vec<u64>,
    ) -> u32 {
        let page_id =
            crate::file::page::allocate_page(mmap, header, PageType::ContextNode, 1, 0xFFFFFFFF)
                .unwrap();
        let node = ContextNode {
            id_hash,
            context_id: 1000,
            vector_page_ref: 0,
            importance,
            valence: 0.0,
            arousal: 0.0,
            created_at: updated_at,
            updated_at,
            version: 1,
            edge_ptrs,
        };
        write_page_data(mmap, page_id, &node.serialize().unwrap()).unwrap();
        btree.insert(id_hash, crate::file::page::encode_page_ref(page_id, 0));
        page_id
    }

    fn allocate_hyperedge_page(
        mmap: &mut MmapMut,
        header: &mut FileHeader,
        btree: &mut BTreeIndex,
        id_hash: u64,
        weight: f32,
        updated_at: i64,
        node_ptrs: Vec<u64>,
    ) -> u32 {
        let page_id =
            crate::file::page::allocate_page(mmap, header, PageType::Hyperedge, 2, 0xFFFFFFFF)
                .unwrap();
        let edge = HyperedgeSlot {
            id_hash,
            kind: HyperedgeKind::Semantic,
            node_ptrs,
            weight,
            created_at: updated_at,
            updated_at,
            version: 1,
            overflow_page: 0,
        };
        write_page_data(mmap, page_id, &edge.serialize().unwrap()).unwrap();
        btree.insert(id_hash, crate::file::page::encode_page_ref(page_id, 0));
        page_id
    }

    fn read_context_node(mmap: &MmapMut, page_id: u32) -> ContextNode {
        let offset = (page_id as usize) * PAGE_SIZE + 32;
        ContextNode::deserialize(&mmap[offset..offset + PAGE_SIZE - 32]).unwrap()
    }

    fn read_hyperedge(mmap: &MmapMut, page_id: u32) -> HyperedgeSlot {
        let offset = (page_id as usize) * PAGE_SIZE + 32;
        HyperedgeSlot::deserialize(&mmap[offset..offset + PAGE_SIZE - 32]).unwrap()
    }

    #[test]
    fn test_node_decay_and_update() {
        let (mut mmap, mut header, mut btree) = create_mmap(20);
        let old_time = now_ms() - 10 * 3_600_000; // 10 hours ago
        let page_id = allocate_context_node_page(
            &mut mmap,
            &mut header,
            &mut btree,
            1,
            0.5,
            old_time,
            vec![10, 11],
        );

        let report = decay_l1_network(&mut mmap, &mut header, &mut btree).unwrap();

        assert_eq!(report.decayed_nodes, 1);
        assert_eq!(report.removed_nodes, 0);
        assert_eq!(report.pruned_edges, 0);
        assert!(btree.search(1).is_some());

        let node = read_context_node(&mmap, page_id);
        let expected = 0.5 * (-LAMBDA_NODE * 10.0).exp();
        assert!((node.importance - expected).abs() < 1e-5);
        assert!(node.updated_at > old_time);
        assert_eq!(node.edge_ptrs, vec![10, 11]);
    }

    #[test]
    fn test_node_prune_edges() {
        let (mut mmap, mut header, mut btree) = create_mmap(20);
        // Set importance so that after decay it lands between 0.05 and 0.15.
        let old_time = now_ms() - 20 * 3_600_000;
        let target = (NODE_REMOVE_THRESHOLD + NODE_PRUNE_EDGES_THRESHOLD) / 2.0;
        let start_importance = target / (-LAMBDA_NODE * 20.0).exp();

        let page_id = allocate_context_node_page(
            &mut mmap,
            &mut header,
            &mut btree,
            2,
            start_importance,
            old_time,
            vec![10, 11, 12],
        );

        let report = decay_l1_network(&mut mmap, &mut header, &mut btree).unwrap();

        assert_eq!(report.decayed_nodes, 1);
        assert_eq!(report.pruned_edges, 3);
        assert_eq!(report.removed_nodes, 0);

        let node = read_context_node(&mmap, page_id);
        assert!(node.edge_ptrs.is_empty());
        assert!(node.importance < NODE_PRUNE_EDGES_THRESHOLD);
        assert!(node.importance >= NODE_REMOVE_THRESHOLD);
    }

    #[test]
    fn test_node_removal() {
        let (mut mmap, mut header, mut btree) = create_mmap(20);
        let old_time = now_ms() - 400 * 3_600_000; // very old
        let page_id = allocate_context_node_page(
            &mut mmap,
            &mut header,
            &mut btree,
            3,
            0.5,
            old_time,
            vec![10],
        );

        let report = decay_l1_network(&mut mmap, &mut header, &mut btree).unwrap();

        assert_eq!(report.removed_nodes, 1);
        assert_eq!(report.decayed_nodes, 0);
        assert!(btree.search(3).is_none());

        // Page should be on the free list.
        assert_eq!(header.free_list_head, page_id);
    }

    #[test]
    fn test_edge_decay_and_update() {
        let (mut mmap, mut header, mut btree) = create_mmap(20);
        let old_time = now_ms() - 10 * 3_600_000;
        let page_id = allocate_hyperedge_page(
            &mut mmap,
            &mut header,
            &mut btree,
            10,
            0.5,
            old_time,
            vec![1, 2],
        );

        let report = decay_l1_network(&mut mmap, &mut header, &mut btree).unwrap();

        assert_eq!(report.removed_edges, 0);
        assert!(btree.search(10).is_some());

        let edge = read_hyperedge(&mmap, page_id);
        let expected = 0.5 * (-LAMBDA_EDGE * 10.0).exp();
        assert!((edge.weight - expected).abs() < 1e-5);
        assert!(edge.updated_at > old_time);
    }

    #[test]
    fn test_edge_removal_by_weight() {
        let (mut mmap, mut header, mut btree) = create_mmap(20);
        let old_time = now_ms() - 200 * 3_600_000;
        allocate_hyperedge_page(
            &mut mmap,
            &mut header,
            &mut btree,
            11,
            0.5,
            old_time,
            vec![1, 2],
        );

        let report = decay_l1_network(&mut mmap, &mut header, &mut btree).unwrap();

        assert_eq!(report.removed_edges, 1);
        assert!(btree.search(11).is_none());
    }

    #[test]
    fn test_edge_removal_by_underpopulation() {
        let (mut mmap, mut header, mut btree) = create_mmap(20);
        let old_time = now_ms();
        allocate_hyperedge_page(
            &mut mmap,
            &mut header,
            &mut btree,
            12,
            1.0,
            old_time,
            vec![1],
        );

        let report = decay_l1_network(&mut mmap, &mut header, &mut btree).unwrap();

        assert_eq!(report.removed_edges, 1);
        assert!(btree.search(12).is_none());
    }

    #[test]
    fn test_edge_cleans_stale_node_references() {
        let (mut mmap, mut header, mut btree) = create_mmap(20);
        let old_time = now_ms() - 400 * 3_600_000;

        // Node will be removed.
        allocate_context_node_page(
            &mut mmap,
            &mut header,
            &mut btree,
            4,
            0.5,
            old_time,
            vec![20],
        );
        // Edge connects the removed node and a surviving node.
        let edge_page = allocate_hyperedge_page(
            &mut mmap,
            &mut header,
            &mut btree,
            20,
            1.0,
            now_ms(),
            vec![4, 5],
        );
        // Surviving node.
        allocate_context_node_page(
            &mut mmap,
            &mut header,
            &mut btree,
            5,
            1.0,
            now_ms(),
            vec![20],
        );

        let report = decay_l1_network(&mut mmap, &mut header, &mut btree).unwrap();

        assert_eq!(report.removed_nodes, 1);
        // Edge keeps only node 5, which is < MIN_EDGE_NODES, so it is removed.
        assert_eq!(report.removed_edges, 1);
        assert!(btree.search(20).is_none());
        assert!(btree.search(4).is_none());
        assert!(btree.search(5).is_some());

        // Edge page should be on the free list.
        assert_eq!(header.free_list_head, edge_page);
    }

    #[test]
    fn test_edge_survives_after_cleaning_stale_refs() {
        let (mut mmap, mut header, mut btree) = create_mmap(20);
        let old_time = now_ms() - 400 * 3_600_000;

        allocate_context_node_page(
            &mut mmap,
            &mut header,
            &mut btree,
            6,
            0.5,
            old_time,
            vec![21],
        );
        let edge_page = allocate_hyperedge_page(
            &mut mmap,
            &mut header,
            &mut btree,
            21,
            1.0,
            now_ms(),
            vec![6, 7, 8],
        );
        allocate_context_node_page(
            &mut mmap,
            &mut header,
            &mut btree,
            7,
            1.0,
            now_ms(),
            vec![21],
        );
        allocate_context_node_page(
            &mut mmap,
            &mut header,
            &mut btree,
            8,
            1.0,
            now_ms(),
            vec![21],
        );

        let report = decay_l1_network(&mut mmap, &mut header, &mut btree).unwrap();

        assert_eq!(report.removed_nodes, 1);
        assert_eq!(report.removed_edges, 0);
        assert!(btree.search(21).is_some());

        let edge = read_hyperedge(&mmap, edge_page);
        assert_eq!(edge.node_ptrs, vec![7, 8]);
    }

    #[test]
    fn test_empty_btree_does_nothing() {
        let (mut mmap, mut header, mut btree) = create_mmap(10);
        let report = decay_l1_network(&mut mmap, &mut header, &mut btree).unwrap();
        assert_eq!(report.decayed_nodes, 0);
        assert_eq!(report.pruned_edges, 0);
        assert_eq!(report.removed_nodes, 0);
        assert_eq!(report.removed_edges, 0);
    }

    #[test]
    fn test_pruned_node_clears_edge_reference() {
        let (mut mmap, mut header, mut btree) = create_mmap(20);
        let old_time = now_ms() - 20 * 3_600_000;
        let target = (NODE_REMOVE_THRESHOLD + NODE_PRUNE_EDGES_THRESHOLD) / 2.0;
        let start_importance = target / (-LAMBDA_NODE * 20.0).exp();

        // Node A will have its edges pruned in Phase 1.
        let node_a = allocate_context_node_page(
            &mut mmap,
            &mut header,
            &mut btree,
            100,
            start_importance,
            old_time,
            vec![50],
        );
        // Node B survives.
        let node_b = allocate_context_node_page(
            &mut mmap,
            &mut header,
            &mut btree,
            101,
            1.0,
            now_ms(),
            vec![50],
        );
        // Edge connects both nodes.
        allocate_hyperedge_page(
            &mut mmap,
            &mut header,
            &mut btree,
            50,
            1.0,
            now_ms(),
            vec![100, 101],
        );

        let report = decay_l1_network(&mut mmap, &mut header, &mut btree).unwrap();

        // Node A pruned its edge; edge became underpopulated and was removed.
        assert_eq!(report.pruned_edges, 1);
        assert_eq!(report.removed_edges, 1);
        assert!(btree.search(50).is_none());

        let a = read_context_node(&mmap, node_a);
        assert!(a.edge_ptrs.is_empty());

        let b = read_context_node(&mmap, node_b);
        assert!(!b.edge_ptrs.contains(&50));
    }

    #[test]
    fn test_removed_edge_clears_node_references() {
        let (mut mmap, mut header, mut btree) = create_mmap(20);
        let old_time = now_ms() - 200 * 3_600_000;

        let node_a = allocate_context_node_page(
            &mut mmap,
            &mut header,
            &mut btree,
            200,
            1.0,
            now_ms(),
            vec![60],
        );
        let node_b = allocate_context_node_page(
            &mut mmap,
            &mut header,
            &mut btree,
            201,
            1.0,
            now_ms(),
            vec![60],
        );
        // Edge weight will decay below threshold and be removed.
        allocate_hyperedge_page(
            &mut mmap,
            &mut header,
            &mut btree,
            60,
            0.5,
            old_time,
            vec![200, 201],
        );

        let report = decay_l1_network(&mut mmap, &mut header, &mut btree).unwrap();

        assert_eq!(report.removed_edges, 1);
        assert!(btree.search(60).is_none());

        let a = read_context_node(&mmap, node_a);
        assert!(!a.edge_ptrs.contains(&60));

        let b = read_context_node(&mmap, node_b);
        assert!(!b.edge_ptrs.contains(&60));
    }

    #[test]
    fn test_pruned_node_edge_survives_with_other_nodes() {
        let (mut mmap, mut header, mut btree) = create_mmap(20);
        let old_time = now_ms() - 20 * 3_600_000;
        let target = (NODE_REMOVE_THRESHOLD + NODE_PRUNE_EDGES_THRESHOLD) / 2.0;
        let start_importance = target / (-LAMBDA_NODE * 20.0).exp();

        // Node A will have its edges pruned.
        let node_a = allocate_context_node_page(
            &mut mmap,
            &mut header,
            &mut btree,
            300,
            start_importance,
            old_time,
            vec![70],
        );
        // Nodes B and C survive and keep the edge alive.
        let node_b = allocate_context_node_page(
            &mut mmap,
            &mut header,
            &mut btree,
            301,
            1.0,
            now_ms(),
            vec![70],
        );
        let node_c = allocate_context_node_page(
            &mut mmap,
            &mut header,
            &mut btree,
            302,
            1.0,
            now_ms(),
            vec![70],
        );
        let edge_page = allocate_hyperedge_page(
            &mut mmap,
            &mut header,
            &mut btree,
            70,
            1.0,
            now_ms(),
            vec![300, 301, 302],
        );

        let report = decay_l1_network(&mut mmap, &mut header, &mut btree).unwrap();

        assert_eq!(report.pruned_edges, 1);
        assert_eq!(report.removed_edges, 0);
        assert!(btree.search(70).is_some());

        let a = read_context_node(&mmap, node_a);
        assert!(a.edge_ptrs.is_empty());

        let b = read_context_node(&mmap, node_b);
        assert!(b.edge_ptrs.contains(&70));

        let c = read_context_node(&mmap, node_c);
        assert!(c.edge_ptrs.contains(&70));

        let edge = read_hyperedge(&mmap, edge_page);
        assert_eq!(edge.node_ptrs, vec![301, 302]);
    }
}
