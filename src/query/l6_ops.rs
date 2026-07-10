// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L6 PathwayWeight CRUD internal implementation.

use crate::layers::pathway::PathwayWeightSlot;
use crate::query::types::{L6Filter, UpdateL6Fields};
use crate::shared::common::{now_ms, parse_id_to_hash};
use crate::storage::record::REC_L6_PATHWAY;
use crate::storage::StorageEngine;
use crate::MemHopError;

/// Read all L6 pathway weights.
pub(crate) fn read_pathways(engine: &StorageEngine) -> Result<Vec<PathwayWeightSlot>, MemHopError> {
    let mut pathways = Vec::new();
    for (id_hash, _) in engine.iter_index() {
        let Some((rt, data)) = engine.read_record(*id_hash)? else {
            continue;
        };
        if rt != REC_L6_PATHWAY {
            continue;
        }
        if let Ok(p) = bincode::deserialize::<PathwayWeightSlot>(data) {
            pathways.push(p);
        }
    }
    Ok(pathways)
}

/// Write all L6 pathway weights (replace existing).
pub(crate) fn write_pathways(
    engine: &mut StorageEngine,
    pathways: &[PathwayWeightSlot],
) -> Result<(), MemHopError> {
    // Delete all existing pathway records
    let to_delete: Vec<u64> = {
        let mut keys = Vec::new();
        for (id_hash, _) in engine.iter_index() {
            keys.push(*id_hash);
        }
        keys
    };
    for key in to_delete {
        engine.delete_record(key)?;
    }
    // Write all pathway records
    for p in pathways {
        let data = bincode::serialize(p).map_err(|e| MemHopError::Serialization(e.to_string()))?;
        engine.write_record(REC_L6_PATHWAY, p.id_hash, &data)?;
    }
    Ok(())
}

/// Get an L6 pathway weight by ID.
pub fn get_l6(engine: &StorageEngine, id: &str) -> Result<Option<PathwayWeightSlot>, MemHopError> {
    let id_hash = parse_id_to_hash(id);
    match engine.read_record(id_hash)? {
        Some((rt, data)) => {
            if rt != REC_L6_PATHWAY {
                return Ok(None);
            }
            let p = bincode::deserialize::<PathwayWeightSlot>(data)
                .map_err(|e| MemHopError::Serialization(e.to_string()))?;
            Ok(Some(p))
        }
        None => Ok(None),
    }
}

/// Partially update an L6 pathway weight.
pub fn update_l6(
    engine: &mut StorageEngine,
    id: &str,
    fields: UpdateL6Fields,
) -> Result<PathwayWeightSlot, MemHopError> {
    let id_hash = parse_id_to_hash(id);
    let Some((_rt, data)) = engine.read_record(id_hash)? else {
        return Err(MemHopError::PageNotFound(0));
    };
    let mut p = bincode::deserialize::<PathwayWeightSlot>(data)
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;

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

    let result = p.clone();
    let new_data = bincode::serialize(&p).map_err(|e| MemHopError::Serialization(e.to_string()))?;
    engine.write_record(REC_L6_PATHWAY, id_hash, &new_data)?;
    Ok(result)
}

/// Delete an L6 pathway weight by ID.
pub fn delete_l6(engine: &mut StorageEngine, id: &str) -> Result<(), MemHopError> {
    let id_hash = parse_id_to_hash(id);
    engine.delete_record(id_hash)?;
    Ok(())
}

/// List L6 pathway weights with optional filters.
pub fn list_l6(
    engine: &StorageEngine,
    filter: Option<L6Filter>,
) -> Result<Vec<PathwayWeightSlot>, MemHopError> {
    let pathways = read_pathways(engine)?;
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

/// Batch upsert L6 pathway weights.
pub fn add_l6(
    engine: &mut StorageEngine,
    slots: Vec<PathwayWeightSlot>,
) -> Result<usize, MemHopError> {
    if slots.is_empty() {
        return Ok(0);
    }
    let now = now_ms();
    for mut slot in slots {
        if slot.created_at == 0 {
            slot.created_at = now;
        }
        slot.updated_at = now;
        let data =
            bincode::serialize(&slot).map_err(|e| MemHopError::Serialization(e.to_string()))?;
        engine.write_record(REC_L6_PATHWAY, slot.id_hash, &data)?;
    }
    // Count total pathways after upsert
    let count = read_pathways(engine)?.len();
    Ok(count)
}

/// Increment/decrement an L6 pathway weight by delta.
pub fn update_l6_weight(
    engine: &mut StorageEngine,
    id: &str,
    delta: f32,
) -> Result<PathwayWeightSlot, MemHopError> {
    let id_hash = parse_id_to_hash(id);
    let Some((_rt, data)) = engine.read_record(id_hash)? else {
        return Err(MemHopError::PageNotFound(0));
    };
    let mut p = bincode::deserialize::<PathwayWeightSlot>(data)
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;

    p.weight = (p.weight + delta).clamp(0.0, 1.0);
    p.updated_at = now_ms();
    p.version += 1;
    let result = p.clone();
    let new_data = bincode::serialize(&p).map_err(|e| MemHopError::Serialization(e.to_string()))?;
    engine.write_record(REC_L6_PATHWAY, id_hash, &new_data)?;
    Ok(result)
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::NamedTempFile;

    #[test]
    fn test_l6_crud() {
        let temp = NamedTempFile::new().unwrap();
        let mut engine = StorageEngine::create(temp.path(), 768).unwrap();

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

        add_l6(&mut engine, vec![slot]).unwrap();

        let got = get_l6(&engine, "0000000000001771")
            .unwrap()
            .expect("pathway should exist");
        assert_eq!(got.weight, 0.5);

        let updated = update_l6(
            &mut engine,
            "0000000000001771",
            UpdateL6Fields {
                weight: Some(0.7),
                ..Default::default()
            },
        )
        .unwrap();
        assert_eq!(updated.weight, 0.7);

        let weighted = update_l6_weight(&mut engine, "0000000000001771", 0.2).unwrap();
        assert!((weighted.weight - 0.9).abs() < f32::EPSILON);

        let list = list_l6(
            &engine,
            Some(L6Filter {
                source_prefix: Some("condition:".into()),
                ..Default::default()
            }),
        )
        .unwrap();
        assert_eq!(list.len(), 1);

        delete_l6(&mut engine, "0000000000001771").unwrap();
        assert!(get_l6(&engine, "0000000000001771").unwrap().is_none());

        let _ = temp;
    }
}
