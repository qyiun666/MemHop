// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L6 PathwayWeight CRUD — StorageEngine-backed per-slot storage.

use crate::layers::pathway::PathwayWeightSlot;
use crate::shared::common::{format_hash, now_ms};
use crate::storage::record::REC_L6_PATHWAY;
use crate::storage::StorageEngine;
use crate::store::{delete_slot, read_slot, write_slot};
use crate::MemHopError;

/// Read a single PathwayWeightSlot by id_hash.
pub fn read_pathway(
    engine: &StorageEngine,
    id_hash: u64,
) -> Result<Option<PathwayWeightSlot>, MemHopError> {
    read_slot(engine, id_hash)
}

/// Write (upsert) a single PathwayWeightSlot.
/// Returns the hex-formatted pathway ID.
pub fn write_pathway(
    engine: &mut StorageEngine,
    mut slot: PathwayWeightSlot,
) -> Result<String, MemHopError> {
    let now = now_ms();
    if slot.created_at == 0 {
        slot.created_at = now;
    }
    slot.updated_at = now;
    write_slot(engine, REC_L6_PATHWAY, slot.id_hash, &slot)?;
    Ok(format_hash(slot.id_hash))
}

/// Delete a PathwayWeightSlot by id_hash.
pub fn delete_pathway(engine: &mut StorageEngine, id_hash: u64) -> Result<(), MemHopError> {
    delete_slot(engine, id_hash)?;
    Ok(())
}

/// List all PathwayWeightSlots stored in the engine.
/// Iterates all engine entries and filters by record type.
pub fn list_pathways(engine: &StorageEngine) -> Result<Vec<PathwayWeightSlot>, MemHopError> {
    let mut result = Vec::new();
    for (&id_hash, &_offset) in engine.iter_index() {
        match engine.read_record(id_hash) {
            Ok(Some((record_type, data))) => {
                if record_type != REC_L6_PATHWAY {
                    continue;
                }
                match bincode::deserialize::<PathwayWeightSlot>(data) {
                    Ok(slot) => result.push(slot),
                    Err(e) => {
                        tracing::warn!("Failed to deserialize pathway slot: {}", e);
                        continue;
                    }
                }
            }
            _ => continue,
        }
    }
    Ok(result)
}

/// Batch upsert PathwayWeightSlots.
/// Returns the total number of stored pathways.
pub fn batch_upsert_pathways(
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
        write_pathway(engine, slot)?;
    }
    let count = list_pathways(engine)?.len();
    Ok(count)
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::NamedTempFile;

    #[test]
    fn test_l6_store_crud() {
        let temp = NamedTempFile::new().unwrap();
        let mut engine = StorageEngine::create(temp.path(), 768).unwrap();

        let slot = PathwayWeightSlot {
            id_hash: 6001,
            source_node: format!("{:x}", 0xABCD),
            target_node: format!("{:x}", 0xDEAD),
            weight: 0.5,
            trigger_count: 1,
            success_rate: 0.0,
            last_accessed: 100,
            metadata: String::new(),
            created_at: 0,
            updated_at: 0,
            version: 1,
        };
        let id_str = write_pathway(&mut engine, slot.clone()).unwrap();
        assert_eq!(id_str, "0000000000001771");

        // Read back
        let got = read_pathway(&engine, 6001)
            .unwrap()
            .expect("pathway should exist");
        assert_eq!(got.weight, 0.5);
        assert_eq!(got.id_hash, 6001);

        // List
        let list = list_pathways(&engine).unwrap();
        assert_eq!(list.len(), 1);
        assert_eq!(list[0].weight, 0.5);

        // Upsert (update weight)
        let mut updated = slot;
        updated.weight = 0.8;
        let _ = write_pathway(&mut engine, updated).unwrap();
        let got = read_pathway(&engine, 6001)
            .unwrap()
            .expect("pathway should exist after update");
        assert!((got.weight - 0.8).abs() < f32::EPSILON);

        // Delete
        delete_pathway(&mut engine, 6001).unwrap();
        assert!(read_pathway(&engine, 6001).unwrap().is_none());
        assert!(list_pathways(&engine).unwrap().is_empty());
    }

    #[test]
    fn test_l6_store_batch_upsert() {
        let temp = NamedTempFile::new().unwrap();
        let mut engine = StorageEngine::create(temp.path(), 768).unwrap();

        let slots = vec![
            PathwayWeightSlot {
                id_hash: 1,
                source_node: "100".to_string(),
                target_node: "200".to_string(),
                weight: 0.5,
                trigger_count: 3,
                success_rate: 0.0,
                last_accessed: 100,
                metadata: String::new(),
                created_at: 0,
                updated_at: 0,
                version: 1,
            },
            PathwayWeightSlot {
                id_hash: 2,
                source_node: "300".to_string(),
                target_node: "400".to_string(),
                weight: 0.7,
                trigger_count: 5,
                success_rate: 0.0,
                last_accessed: 200,
                metadata: String::new(),
                created_at: 0,
                updated_at: 0,
                version: 1,
            },
        ];

        let count = batch_upsert_pathways(&mut engine, slots).unwrap();
        assert_eq!(count, 2);

        let list = list_pathways(&engine).unwrap();
        assert_eq!(list.len(), 2);
    }
}
