// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Stage: L5 Crystallization — generate procedural knowledge crystals from repeated patterns.

use crate::dream::llm::{ChainData, CrystalDef};
use crate::layers::action_chain::{ActionChainSlot, ActionStep, ChainStatus};
use crate::storage::record::{REC_L5_ACTION_CHAIN, REC_L5_ACTION_STEP};
use crate::storage::StorageEngine;
use crate::util::hash::hash_id;
use crate::MemHopError;

/// Extract existing ActionChain slots for the consolidation input.
pub fn extract_existing_chains(engine: &StorageEngine) -> Vec<ChainData> {
    let mut chains: Vec<ChainData> = Vec::new();
    for (&id_hash, _) in engine.iter_index() {
        if let Ok(Some((record_type, data))) = engine.read_record(id_hash) {
            if record_type != REC_L5_ACTION_CHAIN {
                continue;
            }
            if let Ok(chain) = bincode::deserialize::<ActionChainSlot>(data) {
                chains.push(ChainData {
                    title: chain.title.clone(),
                    trigger: chain.trigger.clone(),
                    trigger_count: chain.trigger_count,
                    confidence: chain.confidence,
                });
            }
        }
    }

    chains.sort_by_key(|c| std::cmp::Reverse(c.trigger_count));
    chains.truncate(20);
    chains
}

/// Write pre-computed crystals from the consolidated LLM call into L5 action chains.
pub fn apply_precomputed_crystals(
    crystals: &[CrystalDef],
    engine: &mut StorageEngine,
) -> Result<Vec<String>, MemHopError> {
    let mut new_ids = Vec::new();

    for crystal in crystals {
        let now = chrono::Utc::now().timestamp_millis();
        let crystal_chain_id = hash_id(&format!("crystal_{}_{}", crystal.condition, now));

        let chain = ActionChainSlot {
            id_hash: crystal_chain_id,
            title: format!(
                "crystal_{}",
                crystal.condition.chars().take(30).collect::<String>()
            ),
            trigger: crystal.condition.clone(),
            status: ChainStatus::Draft,
            confidence: crystal.confidence,
            success_rate: 0.0,
            trigger_count: 0,
            last_triggered: 0,
            created_at: now,
            updated_at: now,
            version: 1,
        };

        let chain_data =
            bincode::serialize(&chain).map_err(|e| MemHopError::Serialization(e.to_string()))?;
        engine.write_record(REC_L5_ACTION_CHAIN, crystal_chain_id, &chain_data)?;

        for (i, step_def) in crystal.steps.iter().enumerate() {
            let step_id_hash = hash_id(&format!("step_{}_{}_{}", crystal_chain_id, i, now));
            let step = ActionStep {
                id_hash: step_id_hash,
                chain_id: crystal_chain_id,
                step_order: i as u16,
                action: step_def.action.clone(),
                parameters: step_def.parameters.clone(),
                created_at: now,
            };
            let step_data =
                bincode::serialize(&step).map_err(|e| MemHopError::Serialization(e.to_string()))?;
            engine.write_record(REC_L5_ACTION_STEP, step_id_hash, &step_data)?;
        }

        new_ids.push(crate::shared::common::format_hash(crystal_chain_id));
    }

    Ok(new_ids)
}

#[cfg(test)]
pub fn activate_crystal(engine: &mut StorageEngine, chain_id: u64) -> Result<(), MemHopError> {
    let (record_type, data) = engine.read_record(chain_id)?.ok_or_else(|| {
        MemHopError::Serialization(format!("ActionChain {} not found in index", chain_id))
    })?;

    if record_type != REC_L5_ACTION_CHAIN {
        return Err(MemHopError::InvalidPageType);
    }

    let mut chain: ActionChainSlot =
        bincode::deserialize(data).map_err(|e| MemHopError::Serialization(e.to_string()))?;

    if chain.confidence < 0.5 {
        return Err(MemHopError::Serialization(format!(
            "Cannot activate chain {}: confidence {} < 0.5",
            chain_id, chain.confidence
        )));
    }

    let mut step_count = 0;
    for (&step_hash, _) in engine.iter_index() {
        if let Ok(Some((rt, step_data))) = engine.read_record(step_hash) {
            if rt != REC_L5_ACTION_STEP {
                continue;
            }
            if let Ok(step) = bincode::deserialize::<ActionStep>(step_data) {
                if step.chain_id == chain_id {
                    step_count += 1;
                }
            }
        }
    }

    if step_count == 0 {
        return Err(MemHopError::Serialization(format!(
            "Cannot activate chain {}: no action steps found",
            chain_id
        )));
    }

    chain.status = ChainStatus::Active;
    chain.updated_at = chrono::Utc::now().timestamp_millis();

    let chain_data =
        bincode::serialize(&chain).map_err(|e| MemHopError::Serialization(e.to_string()))?;
    engine.write_record(REC_L5_ACTION_CHAIN, chain_id, &chain_data)?;

    Ok(())
}

/// Prune low-quality action chains during dream pipeline.
/// Removes chains with low confidence (< 0.3) and low trigger counts (< 5).
pub fn prune_low_quality_crystals(engine: &mut StorageEngine) -> Result<Vec<String>, MemHopError> {
    let mut pruned = Vec::new();
    let entries: Vec<(u64, u64)> = engine.iter_index().map(|(k, v)| (*k, *v)).collect();

    for (id_hash, _) in &entries {
        if let Ok(Some((record_type, data))) = engine.read_record(*id_hash) {
            if record_type != REC_L5_ACTION_CHAIN {
                continue;
            }
            if let Ok(chain) = bincode::deserialize::<ActionChainSlot>(data) {
                // Low confidence + low trigger count → prune
                if chain.confidence < 0.3 && chain.trigger_count < 5 {
                    engine.delete_record(*id_hash)?;
                    pruned.push(format!("{:016x}", id_hash));
                }
            }
        }
    }

    Ok(pruned)
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::NamedTempFile;

    fn create_engine() -> StorageEngine {
        let temp = NamedTempFile::new().unwrap();
        StorageEngine::create(temp.path(), 768).unwrap()
    }

    fn write_chain_slot(engine: &mut StorageEngine, chain: ActionChainSlot) -> u64 {
        let data = bincode::serialize(&chain).unwrap();
        engine
            .write_record(REC_L5_ACTION_CHAIN, chain.id_hash, &data)
            .unwrap();
        chain.id_hash
    }

    fn write_action_step(engine: &mut StorageEngine, step: ActionStep) -> u64 {
        let data = bincode::serialize(&step).unwrap();
        engine
            .write_record(REC_L5_ACTION_STEP, step.id_hash, &data)
            .unwrap();
        step.id_hash
    }

    fn count_steps_for_chain(engine: &StorageEngine, chain_id: u64) -> Vec<ActionStep> {
        let mut steps = Vec::new();
        for (&step_hash, _) in engine.iter_index() {
            if let Ok(Some((rt, data))) = engine.read_record(step_hash) {
                if rt != REC_L5_ACTION_STEP {
                    continue;
                }
                if let Ok(step) = bincode::deserialize::<ActionStep>(data) {
                    if step.chain_id == chain_id {
                        steps.push(step);
                    }
                }
            }
        }
        steps.sort_by_key(|s| s.step_order);
        steps
    }

    fn read_chain(engine: &StorageEngine, chain_id: u64) -> ActionChainSlot {
        let (_, data) = engine.read_record(chain_id).unwrap().unwrap();
        bincode::deserialize(data).unwrap()
    }

    #[test]
    fn test_activate_crystal_success() {
        let mut engine = create_engine();
        let now = chrono::Utc::now().timestamp_millis();

        let chain_id = hash_id("activate_chain");
        let chain = ActionChainSlot {
            id_hash: chain_id,
            title: "activate".to_string(),
            trigger: "test".to_string(),
            status: ChainStatus::Draft,
            confidence: 0.8,
            success_rate: 0.0,
            trigger_count: 0,
            last_triggered: 0,
            created_at: now,
            updated_at: now,
            version: 1,
        };
        write_chain_slot(&mut engine, chain);

        let step = ActionStep {
            id_hash: hash_id("activate_step"),
            chain_id,
            step_order: 0,
            action: "do something".to_string(),
            parameters: None,
            created_at: now,
        };
        write_action_step(&mut engine, step);

        activate_crystal(&mut engine, chain_id).unwrap();

        let activated = read_chain(&engine, chain_id);
        assert_eq!(activated.status, ChainStatus::Active);
    }

    #[test]
    fn test_activate_crystal_low_confidence_fails() {
        let mut engine = create_engine();
        let now = chrono::Utc::now().timestamp_millis();

        let chain_id = hash_id("low_conf_chain");
        let chain = ActionChainSlot {
            id_hash: chain_id,
            title: "low".to_string(),
            trigger: "test".to_string(),
            status: ChainStatus::Draft,
            confidence: 0.4,
            success_rate: 0.0,
            trigger_count: 0,
            last_triggered: 0,
            created_at: now,
            updated_at: now,
            version: 1,
        };
        write_chain_slot(&mut engine, chain);

        let step = ActionStep {
            id_hash: hash_id("low_conf_step"),
            chain_id,
            step_order: 0,
            action: "do something".to_string(),
            parameters: None,
            created_at: now,
        };
        write_action_step(&mut engine, step);

        assert!(activate_crystal(&mut engine, chain_id).is_err());

        let chain = read_chain(&engine, chain_id);
        assert_eq!(chain.status, ChainStatus::Draft);
    }

    #[test]
    fn test_activate_crystal_no_steps_fails() {
        let mut engine = create_engine();
        let now = chrono::Utc::now().timestamp_millis();

        let chain_id = hash_id("no_step_chain");
        let chain = ActionChainSlot {
            id_hash: chain_id,
            title: "no_steps".to_string(),
            trigger: "test".to_string(),
            status: ChainStatus::Draft,
            confidence: 0.8,
            success_rate: 0.0,
            trigger_count: 0,
            last_triggered: 0,
            created_at: now,
            updated_at: now,
            version: 1,
        };
        write_chain_slot(&mut engine, chain);

        assert!(activate_crystal(&mut engine, chain_id).is_err());
    }
}
