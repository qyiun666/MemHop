// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L1 HyperedgeSlot — edges in the hypergraph skeleton.
// No metadata payload; the `kind` enum carries semantic meaning.

use crate::api::MemHopError;
use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[repr(u8)]
pub enum HyperedgeKind {
    CoOccurrence = 0, // Frequently co-occurring contexts
    Causal = 1,       // Causal/influence relationship
    Semantic = 2,     // Semantically related
    Temporal = 3,     // Time-ordered
    Hierarchical = 4, // Parent-child
    Sequence = 5,     // Ordered sequence
}

impl HyperedgeKind {
    #[cfg(test)]
    pub fn from_u8(value: u8) -> Self {
        match value {
            0 => Self::CoOccurrence,
            1 => Self::Causal,
            2 => Self::Semantic,
            3 => Self::Temporal,
            4 => Self::Hierarchical,
            5 => Self::Sequence,
            _ => Self::CoOccurrence,
        }
    }
}

/// L1 hyperedge — connects multiple ContextNodes (true hyperedges).
/// Inline: up to 8 node_ptrs; `overflow_page` for larger sets.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct HyperedgeSlot {
    pub id_hash: u64,
    pub kind: HyperedgeKind,
    pub node_ptrs: Vec<u64>,
    pub weight: f32,
    pub created_at: i64,
    pub updated_at: i64,
    pub version: u32,
    pub overflow_page: u32,
}

impl HyperedgeSlot {
    #[cfg(test)]
    pub fn serialize(&self) -> Result<Vec<u8>, MemHopError> {
        bincode::serialize(self).map_err(|e| MemHopError::Serialization(e.to_string()))
    }

    pub fn deserialize(data: &[u8]) -> Result<Self, MemHopError> {
        bincode::deserialize(data).map_err(|e| MemHopError::Deserialization(e.to_string()))
    }
}

// ============================================================================
// SceneEdge — renamed v2 type (replaces HyperedgeSlot for L1 hyperedges)
// ============================================================================

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct SceneEdge {
    pub id_hash: u64,
    pub kind: HyperedgeKind,
    pub node_ids: Vec<u64>,
    pub weight: f32,
    pub created_at: i64,
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_hyperedge_roundtrip() {
        let slot = HyperedgeSlot {
            id_hash: 1234567890,
            kind: HyperedgeKind::Semantic,
            node_ptrs: vec![100, 200, 300],
            weight: 0.85,
            created_at: 1234567890,
            updated_at: 1234567891,
            version: 1,
            overflow_page: 0,
        };
        let data = slot.serialize().unwrap();
        assert_eq!(slot, HyperedgeSlot::deserialize(&data).unwrap());
    }

    #[test]
    fn test_hyperedge_max_inline_nodes() {
        let slot = HyperedgeSlot {
            id_hash: 9876543210,
            kind: HyperedgeKind::CoOccurrence,
            node_ptrs: vec![1, 2, 3, 4, 5, 6, 7, 8],
            weight: 1.0,
            created_at: 1000000000,
            updated_at: 1000000001,
            version: 1,
            overflow_page: 0,
        };
        assert_eq!(
            slot,
            HyperedgeSlot::deserialize(&slot.serialize().unwrap()).unwrap()
        );
    }

    #[test]
    fn test_hyperedge_overflow_nodes() {
        let slot = HyperedgeSlot {
            id_hash: 1111111111,
            kind: HyperedgeKind::Causal,
            node_ptrs: vec![10, 20, 30, 40, 50, 60, 70, 80, 90, 100],
            weight: 0.5,
            created_at: 1234567890,
            updated_at: 1234567891,
            version: 1,
            overflow_page: 123,
        };
        // Bincode preserves all nodes (no inline limit).
        let d = HyperedgeSlot::deserialize(&slot.serialize().unwrap()).unwrap();
        assert_eq!(d.node_ptrs.len(), 10);
        assert_eq!(d.overflow_page, 123);
    }

    #[test]
    fn test_hyperedge_empty_nodes() {
        let slot = HyperedgeSlot {
            id_hash: 2222222222,
            kind: HyperedgeKind::Temporal,
            node_ptrs: vec![],
            weight: 0.0,
            created_at: 0,
            updated_at: 0,
            version: 0,
            overflow_page: 0,
        };
        assert_eq!(
            slot,
            HyperedgeSlot::deserialize(&slot.serialize().unwrap()).unwrap()
        );
    }

    #[test]
    fn test_hyperedge_slot_size() {
        let slot = HyperedgeSlot {
            id_hash: 1,
            kind: HyperedgeKind::Temporal,
            node_ptrs: vec![100, 200],
            weight: 0.75,
            created_at: 0,
            updated_at: 0,
            version: 0,
            overflow_page: 0,
        };
        let data = slot.serialize().unwrap();
        assert_eq!(slot, HyperedgeSlot::deserialize(&data).unwrap());
    }

    #[test]
    fn test_hyperedge_all_kinds() {
        let kinds = vec![
            HyperedgeKind::CoOccurrence,
            HyperedgeKind::Causal,
            HyperedgeKind::Semantic,
            HyperedgeKind::Temporal,
            HyperedgeKind::Hierarchical,
            HyperedgeKind::Sequence,
        ];
        for kind in kinds {
            let slot = HyperedgeSlot {
                id_hash: 4444444444,
                kind,
                node_ptrs: vec![1],
                weight: 1.0,
                created_at: 0,
                updated_at: 0,
                version: 0,
                overflow_page: 0,
            };
            assert_eq!(
                slot,
                HyperedgeSlot::deserialize(&slot.serialize().unwrap()).unwrap()
            );
        }
    }
}
