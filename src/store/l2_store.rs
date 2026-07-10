// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L2 TopicSlot CRUD — pure data operations.

use crate::layers::context::TopicSlot;
use crate::shared::common::format_hash;
use crate::storage::record::REC_L2_TOPIC;
use crate::storage::StorageEngine;
use crate::store::{read_slot, write_slot};
use crate::MemHopError;

/// Read a TopicSlot by its id_hash.
pub fn read_topic(engine: &StorageEngine, id_hash: u64) -> Result<Option<TopicSlot>, MemHopError> {
    read_slot(engine, id_hash)
}

/// Write a TopicSlot. Returns the hex-formatted topic ID.
pub fn write_topic(engine: &mut StorageEngine, slot: TopicSlot) -> Result<String, MemHopError> {
    write_slot(engine, REC_L2_TOPIC, slot.id, &slot)?;
    Ok(format_hash(slot.id))
}
