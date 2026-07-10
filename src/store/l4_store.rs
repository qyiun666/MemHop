// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L4 Archive CRUD — pure data operations.

use crate::layers::archive::ArchiveSlot;
use crate::shared::common::format_hash;
use crate::storage::record::REC_L4_ARCHIVE;
use crate::storage::StorageEngine;
use crate::store::{read_slot, write_slot};
use crate::MemHopError;

/// Read an ArchiveSlot by its id_hash.
pub fn read_archive(
    engine: &StorageEngine,
    id_hash: u64,
) -> Result<Option<ArchiveSlot>, MemHopError> {
    read_slot(engine, id_hash)
}

/// Write an ArchiveSlot. Returns the hex-formatted archive ID.
pub fn write_archive(engine: &mut StorageEngine, slot: ArchiveSlot) -> Result<String, MemHopError> {
    write_slot(engine, REC_L4_ARCHIVE, slot.id_hash, &slot)?;
    Ok(format_hash(slot.id_hash))
}

/// List all ArchiveSlots belonging to a given context (topic/scene).
pub fn list_archives_by_context(
    engine: &StorageEngine,
    context_id: u64,
) -> Result<Vec<ArchiveSlot>, MemHopError> {
    let mut results = Vec::new();

    for (&id_hash, &_offset) in engine.iter_index() {
        match engine.read_record(id_hash) {
            Ok(Some((record_type, data))) => {
                if record_type != REC_L4_ARCHIVE {
                    continue;
                }
                match bincode::deserialize::<ArchiveSlot>(data) {
                    Ok(archive) => {
                        if archive.context_id == context_id {
                            results.push(archive);
                        }
                    }
                    Err(e) => {
                        tracing::warn!("Failed to deserialize archive slot: {}", e);
                        continue;
                    }
                }
            }
            _ => continue,
        }
    }

    results.sort_by_key(|a| std::cmp::Reverse(a.created_at));
    Ok(results)
}
