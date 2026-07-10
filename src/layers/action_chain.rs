// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L5 ActionChain — procedural knowledge as ordered action sequences.
//! Replaces the old CrystalSlot which crammed everything into a `raw_steps` blob.

use crate::api::MemHopError;
use serde::{Deserialize, Serialize};

// ============================================================================
// ChainStatus
// ============================================================================

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[repr(u8)]
pub enum ChainStatus {
    Draft = 0,      // Not yet validated
    Active = 1,     // Verified and available
    Deprecated = 2, // No longer recommended
}

impl ChainStatus {
    pub fn from_u8(v: u8) -> Self {
        match v {
            0 => Self::Draft,
            1 => Self::Active,
            2 => Self::Deprecated,
            _ => Self::Draft,
        }
    }
}

// ============================================================================
// ActionChainSlot — chain metadata
// ============================================================================

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct ActionChainSlot {
    pub id_hash: u64,
    pub title: String,
    pub trigger: String, // Trigger condition (DSL or natural language)
    pub status: ChainStatus,
    pub confidence: f32,
    pub success_rate: f32,
    pub trigger_count: u32,
    pub last_triggered: i64,
    pub created_at: i64,
    pub updated_at: i64,
    pub version: u32,
}

impl ActionChainSlot {
    pub fn serialize(&self) -> Result<Vec<u8>, MemHopError> {
        bincode::serialize(self).map_err(|e| MemHopError::Serialization(e.to_string()))
    }

    pub fn deserialize(data: &[u8]) -> Result<Self, MemHopError> {
        bincode::deserialize(data).map_err(|e| MemHopError::Deserialization(e.to_string()))
    }
}

// ============================================================================
// ActionStep — individual step within a chain
// ============================================================================

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct ActionStep {
    pub id_hash: u64,
    pub chain_id: u64,
    pub step_order: u16,
    pub action: String,
    pub parameters: Option<String>, // JSON format
    pub created_at: i64,
}

impl ActionStep {
    pub fn serialize(&self) -> Result<Vec<u8>, MemHopError> {
        bincode::serialize(self).map_err(|e| MemHopError::Serialization(e.to_string()))
    }

    pub fn deserialize(data: &[u8]) -> Result<Self, MemHopError> {
        bincode::deserialize(data).map_err(|e| MemHopError::Deserialization(e.to_string()))
    }
}

// ============================================================================
// Tests
// ============================================================================

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_action_chain_roundtrip() {
        let chain = ActionChainSlot {
            id_hash: 123456789,
            title: "Deploy Service".into(),
            trigger: "keyword:deploy AND service:production".into(),
            status: ChainStatus::Active,
            confidence: 0.85,
            success_rate: 0.92,
            trigger_count: 5,
            last_triggered: 1000000,
            created_at: 900000,
            updated_at: 950000,
            version: 1,
        };
        let data = chain.serialize().unwrap();
        assert_eq!(chain, ActionChainSlot::deserialize(&data).unwrap());
    }

    #[test]
    fn test_action_chain_empty_strings() {
        let chain = ActionChainSlot {
            id_hash: 1,
            title: "".into(),
            trigger: "".into(),
            status: ChainStatus::Draft,
            confidence: 0.0,
            success_rate: 0.0,
            trigger_count: 0,
            last_triggered: 0,
            created_at: 0,
            updated_at: 0,
            version: 0,
        };
        assert_eq!(
            chain,
            ActionChainSlot::deserialize(&chain.serialize().unwrap()).unwrap()
        );
    }

    #[test]
    fn test_action_chain_all_statuses() {
        for status in [
            ChainStatus::Draft,
            ChainStatus::Active,
            ChainStatus::Deprecated,
        ] {
            let chain = ActionChainSlot {
                id_hash: 1,
                title: "test".into(),
                trigger: "always".into(),
                status,
                confidence: 1.0,
                success_rate: 1.0,
                trigger_count: 1,
                last_triggered: 100,
                created_at: 0,
                updated_at: 0,
                version: 0,
            };
            assert_eq!(
                ActionChainSlot::deserialize(&chain.serialize().unwrap())
                    .unwrap()
                    .status,
                status
            );
        }
    }

    #[test]
    fn test_action_step_roundtrip() {
        let step = ActionStep {
            id_hash: 1,
            chain_id: 100,
            step_order: 1,
            action: "search".into(),
            parameters: Some(r#"{"query":"Rust docs"}"#.into()),
            created_at: 1000,
        };
        assert_eq!(
            step,
            ActionStep::deserialize(&step.serialize().unwrap()).unwrap()
        );
    }

    #[test]
    fn test_action_step_no_params() {
        let step = ActionStep {
            id_hash: 2,
            chain_id: 100,
            step_order: 2,
            action: "summarize".into(),
            parameters: None,
            created_at: 2000,
        };
        let restored = ActionStep::deserialize(&step.serialize().unwrap()).unwrap();
        assert_eq!(step, restored);
        assert_eq!(restored.parameters, None);
    }

    #[test]
    fn test_action_step_slot_size() {
        let step = ActionStep {
            id_hash: 1,
            chain_id: 1,
            step_order: 0,
            action: "act".into(),
            parameters: Some("{}".into()),
            created_at: 0,
        };
        let data = step.serialize().unwrap();
        let restored = ActionStep::deserialize(&data).unwrap();
        assert_eq!(step, restored);
    }

    #[test]
    fn test_action_chain_has_updated_at() {
        let chain = ActionChainSlot {
            id_hash: 1,
            title: "test".into(),
            trigger: "t".into(),
            status: ChainStatus::Active,
            confidence: 0.5,
            success_rate: 0.8,
            trigger_count: 3,
            last_triggered: 500,
            created_at: 100,
            updated_at: 600,
            version: 2,
        };
        let r = ActionChainSlot::deserialize(&chain.serialize().unwrap()).unwrap();
        assert_eq!(r.updated_at, 600);
        assert_eq!(r.success_rate, 0.8);
    }
}
