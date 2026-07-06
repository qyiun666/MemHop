// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L6 PathwayWeight CRUD internal implementation.

use crate::api::page_chain;
use crate::file::header::{FileHeader, LAYER_ROOT_L6};
use crate::index::btree::BTreeIndex;
use crate::layers::pathway::PathwayWeightSlot;
use crate::query::types::{L6Filter, UpdateL6Fields};
use crate::shared::common::{now_ms, parse_id_to_hash};
use crate::util::PageType;
use crate::MemHopError;
use memmap2::MmapMut;

pub(crate) fn read_pathways(
    mmap: &MmapMut,
    header: &FileHeader,
) -> Result<Vec<PathwayWeightSlot>, MemHopError> {
    if header.layer_roots[LAYER_ROOT_L6] == 0 {
        return Ok(Vec::new());
    }
    let data = page_chain::read_magic_chain(
        mmap,
        header,
        header.layer_roots[LAYER_ROOT_L6],
        page_chain::PATHWAY_MAGIC,
    )?;
    PathwayWeightSlot::deserialize_pathways(&data)
        .map_err(|e| MemHopError::Serialization(e.to_string()))
}

pub(crate) fn write_pathways(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    file: &mut std::fs::File,
    pathways: &[PathwayWeightSlot],
) -> Result<(), MemHopError> {
    if pathways.is_empty() {
        if header.layer_roots[LAYER_ROOT_L6] != 0 {
            page_chain::free_magic_chain(mmap, header, header.layer_roots[LAYER_ROOT_L6])?;
            header.layer_roots[LAYER_ROOT_L6] = 0;
        }
        return Ok(());
    }

    let data = PathwayWeightSlot::serialize_pathways(pathways)
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;

    if header.layer_roots[LAYER_ROOT_L6] != 0 {
        page_chain::free_magic_chain(mmap, header, header.layer_roots[LAYER_ROOT_L6])?;
    }
    let root = page_chain::write_magic_chain(
        mmap,
        header,
        file,
        &data,
        PageType::PathwayWeight,
        page_chain::PATHWAY_MAGIC,
    )?;
    header.layer_roots[LAYER_ROOT_L6] = root;
    Ok(())
}

/// Get an L6 pathway weight by ID.
pub fn get_l6(
    mmap: &MmapMut,
    header: &FileHeader,
    _btree: &BTreeIndex,
    id: &str,
) -> Result<Option<PathwayWeightSlot>, MemHopError> {
    let id_hash = parse_id_to_hash(id);
    Ok(read_pathways(mmap, header)?
        .iter()
        .find(|p| p.id_hash == id_hash)
        .cloned())
}

/// Partially update an L6 pathway weight.
pub fn update_l6(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &BTreeIndex,
    file: &mut std::fs::File,
    id: &str,
    fields: UpdateL6Fields,
) -> Result<PathwayWeightSlot, MemHopError> {
    let id_hash = parse_id_to_hash(id);
    let mut pathways = read_pathways(mmap, header)?;
    let idx = pathways
        .iter()
        .position(|p| p.id_hash == id_hash)
        .ok_or(MemHopError::PageNotFound(0))?;

    {
        let p = &mut pathways[idx];
        if let Some(source_node) = fields.source_node {
            p.source_node = source_node;
        }
        if let Some(target_node) = fields.target_node {
            p.target_node = target_node;
        }
        if let Some(weight) = fields.weight {
            p.weight = weight;
        }
        if let Some(success_rate) = fields.success_rate {
            p.success_rate = success_rate;
        }
        if let Some(trigger_count) = fields.trigger_count {
            p.trigger_count = trigger_count;
        }
        if let Some(last_accessed) = fields.last_accessed {
            p.last_accessed = last_accessed;
        }
        if let Some(metadata) = fields.metadata {
            p.metadata = metadata;
        }
        p.updated_at = now_ms();
        p.version += 1;
    }

    let result = pathways[idx].clone();
    write_pathways(mmap, header, file, &pathways)?;
    let _ = btree;
    Ok(result)
}

/// Delete an L6 pathway weight by ID.
pub fn delete_l6(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &BTreeIndex,
    file: &mut std::fs::File,
    id: &str,
) -> Result<(), MemHopError> {
    let id_hash = parse_id_to_hash(id);
    let mut pathways = read_pathways(mmap, header)?;
    let old_len = pathways.len();
    pathways.retain(|p| p.id_hash != id_hash);
    if pathways.len() == old_len {
        return Ok(());
    }
    write_pathways(mmap, header, file, &pathways)?;
    let _ = btree;
    Ok(())
}

/// List L6 pathway weights with optional filters.
pub fn list_l6(
    mmap: &MmapMut,
    header: &FileHeader,
    _btree: &BTreeIndex,
    filter: Option<L6Filter>,
) -> Result<Vec<PathwayWeightSlot>, MemHopError> {
    let pathways = read_pathways(mmap, header)?;
    let filter = filter.unwrap_or_default();
    let result: Vec<PathwayWeightSlot> = pathways
        .into_iter()
        .filter(|p| {
            if let Some(ref prefix) = filter.source_prefix {
                if !p.source_node.starts_with(prefix) {
                    return false;
                }
            }
            if let Some(ref prefix) = filter.target_prefix {
                if !p.target_node.starts_with(prefix) {
                    return false;
                }
            }
            if let Some(min) = filter.min_weight {
                if p.weight < min {
                    return false;
                }
            }
            true
        })
        .collect();
    Ok(result)
}

/// Add a new L6 pathway weight.
pub fn add_l6(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &BTreeIndex,
    file: &mut std::fs::File,
    mut slot: PathwayWeightSlot,
) -> Result<(), MemHopError> {
    let mut pathways = read_pathways(mmap, header)?;
    let now = now_ms();
    if slot.created_at == 0 {
        slot.created_at = now;
    }
    slot.updated_at = now;
    pathways.retain(|p| p.id_hash != slot.id_hash);
    pathways.push(slot);
    write_pathways(mmap, header, file, &pathways)?;
    let _ = btree;
    Ok(())
}

/// Increment/decrement an L6 pathway weight by delta.
pub fn update_l6_weight(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &BTreeIndex,
    file: &mut std::fs::File,
    id: &str,
    delta: f32,
) -> Result<PathwayWeightSlot, MemHopError> {
    let id_hash = parse_id_to_hash(id);
    let mut pathways = read_pathways(mmap, header)?;
    let idx = pathways
        .iter()
        .position(|p| p.id_hash == id_hash)
        .ok_or(MemHopError::PageNotFound(0))?;
    pathways[idx].weight = (pathways[idx].weight + delta).clamp(0.0, 1.0);
    pathways[idx].updated_at = now_ms();
    pathways[idx].version += 1;
    let result = pathways[idx].clone();
    write_pathways(mmap, header, file, &pathways)?;
    let _ = btree;
    Ok(result)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::test_helpers::create_test_mmap;

    #[test]
    fn test_l6_crud() {
        let (mut mmap, mut header, btree, mut file) = create_test_mmap(64);

        let slot = PathwayWeightSlot {
            id_hash: 6001,
            source_node: "condition:deploy".into(),
            target_node: "action:restart".into(),
            weight: 0.5,
            trigger_count: 1,
            success_rate: 0.9,
            last_accessed: 100,
            metadata: "{}".into(),
            created_at: 0,
            updated_at: 0,
            version: 1,
        };
        add_l6(&mut mmap, &mut header, &btree, &mut file, slot).unwrap();

        let got = get_l6(&mmap, &header, &btree, "0000000000001771")
            .unwrap()
            .expect("pathway should exist");
        assert_eq!(got.weight, 0.5);

        let updated = update_l6(
            &mut mmap,
            &mut header,
            &btree,
            &mut file,
            "0000000000001771",
            UpdateL6Fields {
                weight: Some(0.7),
                ..Default::default()
            },
        )
        .unwrap();
        assert_eq!(updated.weight, 0.7);

        let weighted = update_l6_weight(
            &mut mmap,
            &mut header,
            &btree,
            &mut file,
            "0000000000001771",
            0.2,
        )
        .unwrap();
        assert!((weighted.weight - 0.9).abs() < f32::EPSILON);

        let list = list_l6(
            &mmap,
            &header,
            &btree,
            Some(L6Filter {
                source_prefix: Some("condition:".into()),
                ..Default::default()
            }),
        )
        .unwrap();
        assert_eq!(list.len(), 1);

        delete_l6(
            &mut mmap,
            &mut header,
            &btree,
            &mut file,
            "0000000000001771",
        )
        .unwrap();
        assert!(get_l6(&mmap, &header, &btree, "0000000000001771")
            .unwrap()
            .is_none());

        // file is kept alive for mmap lifetime
        let _ = file;
    }
}
