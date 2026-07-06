// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L3 Hypergraph CRUD internal implementation.

use crate::file::header::FileHeader;
use crate::index::btree::BTreeIndex;
use crate::l3::AdjacencyCache;
use crate::layers::hypergraph::{HypergraphEdge, HypergraphNode, HypergraphSlot};
use crate::query::types::{L3Detail, UpdateL3Fields};
use crate::shared::common::{format_hash, now_ms, parse_id_to_hash};
use crate::shared::slot_io::{decode_page_id, get_slot_data};
use crate::util::{PageType, PAGE_SIZE};
use crate::MemHopError;
use memmap2::MmapMut;

/// Get an L3 hypergraph by ID, including all nodes and edges.
pub fn get_l3(
    mmap: &MmapMut,
    btree: &BTreeIndex,
    id: &str,
) -> Result<Option<L3Detail>, MemHopError> {
    let graph_hash = parse_id_to_hash(id);
    let data: &[u8] = &mmap[..];

    let slot = match btree.search(graph_hash) {
        Some(page_ref) => {
            let slot_data = get_slot_data(data, page_ref)
                .ok_or_else(|| MemHopError::PageNotFound(decode_page_id(page_ref)))?;
            HypergraphSlot::deserialize_slot(slot_data)?
        }
        None => return Ok(None),
    };

    let mut nodes: Vec<HypergraphNode> = Vec::new();
    let mut edges: Vec<HypergraphEdge> = Vec::new();

    for (_, page_ref) in btree.iter_unsorted() {
        let page_id = decode_page_id(*page_ref);
        if page_id >= mmap.len() as u32 / PAGE_SIZE as u32 {
            continue;
        }
        match page_type(data, page_id) {
            Some(t) if t == PageType::HypergraphNode as u16 => {
                if let Some(slot_data) = get_slot_data(data, *page_ref) {
                    if let Ok(node) = HypergraphNode::deserialize(slot_data) {
                        if node.graph_id == graph_hash {
                            nodes.push(node);
                        }
                    }
                }
            }
            Some(t) if t == PageType::HypergraphEdge as u16 => {
                if let Some(slot_data) = get_slot_data(data, *page_ref) {
                    if let Ok(edge) = HypergraphEdge::deserialize(slot_data) {
                        if edge.graph_id == graph_hash {
                            edges.push(edge);
                        }
                    }
                }
            }
            _ => {}
        }
    }

    Ok(Some(L3Detail { slot, nodes, edges }))
}

/// Partially update an L3 hypergraph container.
pub fn update_l3(
    mmap: &mut MmapMut,
    _header: &mut FileHeader,
    btree: &BTreeIndex,
    id: &str,
    fields: UpdateL3Fields,
) -> Result<(), MemHopError> {
    let graph_hash = parse_id_to_hash(id);
    let page_ref = btree
        .search(graph_hash)
        .ok_or(MemHopError::PageNotFound(0))?;
    let page_id = decode_page_id(page_ref);
    let offset = crate::shared::slot_io::slot_offset(page_id);

    let mut slot = HypergraphSlot::deserialize_slot(&mmap[offset..])?;

    if let Some(name) = fields.name {
        slot.name = name;
    }

    slot.updated_at = now_ms();
    slot.version += 1;

    let data = slot
        .serialize()
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;
    if offset + data.len() > mmap.len() {
        return Err(MemHopError::PageNotFound(page_id));
    }
    mmap[offset..offset + data.len()].copy_from_slice(&data);

    Ok(())
}

/// Delete an L3 hypergraph and clean up L2 references.
pub fn delete_l3(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    adjacency_cache: &mut AdjacencyCache,
    l3_id: &str,
) -> Result<(), MemHopError> {
    let graph_hash = parse_id_to_hash(l3_id);
    let l3_id_str = format_hash(graph_hash);

    let l2_refs = crate::l3::store::collect_l2_refs(&*mmap, btree, graph_hash)?;

    crate::l3::store::delete_graph(mmap, header, btree, &l3_id_str)?;

    for (page_id, _id_hash) in l2_refs {
        crate::l3::store::remove_l3_ref_from_context(mmap, page_id, graph_hash)?;
    }

    adjacency_cache.invalidate(graph_hash);

    Ok(())
}

#[inline]
fn page_type(data: &[u8], page_id: u32) -> Option<u16> {
    let offset = (page_id as usize) * PAGE_SIZE + 4;
    if offset + 2 > data.len() {
        return None;
    }
    Some(u16::from_le_bytes([data[offset], data[offset + 1]]))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::l3::store::{add_edge, add_node};
    use crate::layers::hypergraph::{GraphEdgeKind, HypergraphSource};
    use crate::test_helpers::{create_test_edge, create_test_mmap, create_test_node};

    #[test]
    fn test_l3_get_update_delete() {
        let (mut mmap, mut header, mut btree, mut file) = create_test_mmap(64);
        let mut adjacency_cache = AdjacencyCache::new();

        let graph_id = 1u64;
        let slot = HypergraphSlot {
            id_hash: graph_id,
            name: "test graph".into(),
            source: HypergraphSource::Manual,
            node_count: 0,
            edge_count: 0,
            created_at: 0,
            updated_at: 0,
            version: 1,
        };
        let slot_page = crate::file::page::allocate_page(
            &mut mmap,
            &mut header,
            PageType::HypergraphSlot,
            3,
            crate::index::btree::EMPTY_PAGE,
            &mut file,
        )
        .unwrap();
        crate::file::page::write_page_data(&mut mmap, slot_page, &slot.serialize().unwrap())
            .unwrap();
        btree.insert(graph_id, (slot_page as u64) << 16);

        add_node(
            &mut mmap,
            &mut header,
            &mut btree,
            create_test_node(101, graph_id, "node101"),
            &mut file,
            None,
            None,
        )
        .unwrap();
        add_edge(
            &mut mmap,
            &mut header,
            &mut btree,
            create_test_edge(201, graph_id, GraphEdgeKind::Related, vec![101]),
            &mut file,
            None,
        )
        .unwrap();

        let detail = get_l3(&mmap, &btree, "0000000000000001")
            .unwrap()
            .expect("graph should exist");
        assert_eq!(detail.slot.name, "test graph");
        assert_eq!(detail.nodes.len(), 1);
        assert_eq!(detail.edges.len(), 1);

        update_l3(
            &mut mmap,
            &mut header,
            &btree,
            "0000000000000001",
            UpdateL3Fields {
                name: Some("renamed graph".into()),
            },
        )
        .unwrap();

        let updated = get_l3(&mmap, &btree, "0000000000000001").unwrap().unwrap();
        assert_eq!(updated.slot.name, "renamed graph");

        delete_l3(
            &mut mmap,
            &mut header,
            &mut btree,
            &mut adjacency_cache,
            "0000000000000001",
        )
        .unwrap();
        assert!(get_l3(&mmap, &btree, "0000000000000001").unwrap().is_none());
    }
}
