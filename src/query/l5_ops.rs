// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L5 ActionChain CRUD internal implementation.

use crate::layers::action_chain::{ActionChainSlot, ActionStep};
use crate::query::types::UpdateL5Fields;
use crate::shared::common::{now_ms, parse_id_to_hash};
use crate::storage::record::{REC_L5_ACTION_CHAIN, REC_L5_ACTION_STEP};
use crate::storage::StorageEngine;
use crate::MemHopError;

/// Get an L5 action chain by ID.
pub fn get_l5(engine: &StorageEngine, id: &str) -> Result<Option<ActionChainSlot>, MemHopError> {
    let id_hash = parse_id_to_hash(id);
    match engine.read_record(id_hash)? {
        Some((rt, data)) => {
            if rt != REC_L5_ACTION_CHAIN {
                return Ok(None);
            }
            let chain = bincode::deserialize::<ActionChainSlot>(data)
                .map_err(|e| MemHopError::Serialization(e.to_string()))?;
            Ok(Some(chain))
        }
        None => Ok(None),
    }
}

/// Partially update an L5 action chain.
pub fn update_l5(
    engine: &mut StorageEngine,
    id: &str,
    fields: UpdateL5Fields,
) -> Result<(), MemHopError> {
    let id_hash = parse_id_to_hash(id);
    let Some((_rt, data)) = engine.read_record(id_hash)? else {
        return Err(MemHopError::PageNotFound(0));
    };
    let mut chain = bincode::deserialize::<ActionChainSlot>(data)
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;

    if let Some(title) = fields.title {
        chain.title = title;
    }
    if let Some(trigger) = fields.trigger {
        chain.trigger = trigger;
    }
    if let Some(status) = fields.status {
        chain.status = parse_chain_status(&status);
    }
    if let Some(confidence) = fields.confidence {
        chain.confidence = confidence;
    }
    if let Some(success_rate) = fields.success_rate {
        chain.success_rate = success_rate;
    }
    if let Some(trigger_count) = fields.trigger_count {
        chain.trigger_count = trigger_count;
    }
    if let Some(last_triggered) = fields.last_triggered {
        chain.last_triggered = last_triggered;
    }

    chain.updated_at = now_ms();
    chain.version += 1;

    let new_data =
        bincode::serialize(&chain).map_err(|e| MemHopError::Serialization(e.to_string()))?;
    engine.write_record(REC_L5_ACTION_CHAIN, id_hash, &new_data)?;

    Ok(())
}

fn parse_chain_status(s: &str) -> crate::layers::action_chain::ChainStatus {
    match s.to_lowercase().as_str() {
        "active" => crate::layers::action_chain::ChainStatus::Active,
        "deprecated" => crate::layers::action_chain::ChainStatus::Deprecated,
        _ => crate::layers::action_chain::ChainStatus::Draft,
    }
}

/// Delete an L5 action chain and all its steps.
pub fn delete_l5(engine: &mut StorageEngine, id: &str) -> Result<(), MemHopError> {
    let chain_id = parse_id_to_hash(id);

    // Delete the chain record
    engine.delete_record(chain_id)?;

    // Find and delete all associated steps
    let mut step_hashes: Vec<u64> = Vec::new();
    for (id_hash, _) in engine.iter_index() {
        let Some((rt, data)) = engine.read_record(*id_hash)? else {
            continue;
        };
        if rt != REC_L5_ACTION_STEP {
            continue;
        }
        if let Ok(step) = bincode::deserialize::<ActionStep>(data) {
            if step.chain_id == chain_id {
                step_hashes.push(*id_hash);
            }
        }
    }

    for step_hash in step_hashes {
        engine.delete_record(step_hash)?;
    }

    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::layers::action_chain::ChainStatus;
    use tempfile::NamedTempFile;

    #[test]
    fn test_l5_crud() {
        let temp = NamedTempFile::new().unwrap();
        let mut engine = StorageEngine::create(temp.path(), 768).unwrap();

        let chain = ActionChainSlot {
            id_hash: 5001,
            title: "deploy".into(),
            trigger: "keyword deploy".into(),
            status: ChainStatus::Draft,
            confidence: 0.5,
            success_rate: 0.9,
            trigger_count: 0,
            last_triggered: 0,
            created_at: 0,
            updated_at: 0,
            version: 1,
        };
        let data = bincode::serialize(&chain).unwrap();
        engine
            .write_record(REC_L5_ACTION_CHAIN, 5001, &data)
            .unwrap();

        let got = get_l5(&engine, "0000000000001389")
            .unwrap()
            .expect("chain should exist");
        assert_eq!(got.title, "deploy");

        update_l5(
            &mut engine,
            "0000000000001389",
            UpdateL5Fields {
                title: Some("deploy service".into()),
                status: Some("active".into()),
                ..Default::default()
            },
        )
        .unwrap();

        let updated = get_l5(&engine, "0000000000001389").unwrap().unwrap();
        assert_eq!(updated.title, "deploy service");
        assert_eq!(updated.status, ChainStatus::Active);

        delete_l5(&mut engine, "0000000000001389").unwrap();
        assert!(get_l5(&engine, "0000000000001389").unwrap().is_none());

        let _ = temp;
    }
}
