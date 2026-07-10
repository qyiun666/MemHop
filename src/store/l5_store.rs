// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L5 ActionChain CRUD — pure data operations.

use crate::layers::action_chain::ActionChainSlot;
use crate::shared::common::now_ms;
use crate::storage::record::REC_L5_ACTION_CHAIN;
use crate::storage::StorageEngine;
use crate::store::{read_slot, write_slot};
use crate::MemHopError;

/// Read an ActionChainSlot by its id_hash.
pub fn read_action_chain(
    engine: &StorageEngine,
    id_hash: u64,
) -> Result<Option<ActionChainSlot>, MemHopError> {
    read_slot(engine, id_hash)
}

/// Write an ActionChainSlot. Returns the hex-formatted chain ID.
pub fn write_action_chain(
    engine: &mut StorageEngine,
    slot: ActionChainSlot,
) -> Result<String, MemHopError> {
    write_slot(engine, REC_L5_ACTION_CHAIN, slot.id_hash, &slot)?;
    Ok(crate::shared::common::format_hash(slot.id_hash))
}

/// Partially update an ActionChainSlot in-place (read-modify-write).
pub fn update_action_chain(
    engine: &mut StorageEngine,
    id_hash: u64,
    updates: crate::query::types::UpdateL5Fields,
) -> Result<(), MemHopError> {
    let mut chain: ActionChainSlot = match read_slot(engine, id_hash)? {
        Some(c) => c,
        None => return Err(MemHopError::NotFound(format!("action chain {}", id_hash))),
    };

    if let Some(title) = updates.title {
        chain.title = title;
    }
    if let Some(trigger) = updates.trigger {
        chain.trigger = trigger;
    }
    if let Some(status) = updates.status {
        chain.status = match status.to_lowercase().as_str() {
            "active" => crate::layers::action_chain::ChainStatus::Active,
            "deprecated" => crate::layers::action_chain::ChainStatus::Deprecated,
            _ => crate::layers::action_chain::ChainStatus::Draft,
        };
    }
    if let Some(confidence) = updates.confidence {
        chain.confidence = confidence;
    }
    if let Some(trigger_count) = updates.trigger_count {
        chain.trigger_count = trigger_count;
    }
    if let Some(last_triggered) = updates.last_triggered {
        chain.last_triggered = last_triggered;
    }

    chain.updated_at = now_ms();
    write_slot(engine, REC_L5_ACTION_CHAIN, id_hash, &chain)?;
    Ok(())
}
