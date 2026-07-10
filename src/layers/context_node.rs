// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L1 ContextNode — lightweight graph node in the hypergraph skeleton.
// Points to one L2 Context; carries only vector ref + importance, no text.

use crate::api::MemHopError;
use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct ContextNode {
    pub id_hash: u64,
    pub context_id: u64,      // Points to L2 ContextSlot id_hash
    pub vector_page_ref: u64, // For similarity search
    pub importance: f32,
    pub valence: f64, // Emotional valence (-1..1), for decay modulation
    pub arousal: f64, // Emotional arousal (0..1), for decay modulation
    pub created_at: i64,
    pub updated_at: i64,
    pub version: u32,
    pub edge_ptrs: Vec<u64>,
}

impl ContextNode {
    #[cfg(test)]
    pub fn serialize(&self) -> Result<Vec<u8>, MemHopError> {
        bincode::serialize(self).map_err(|e| MemHopError::Serialization(e.to_string()))
    }

    pub fn deserialize(data: &[u8]) -> Result<Self, MemHopError> {
        bincode::deserialize(data).map_err(|e| MemHopError::Deserialization(e.to_string()))
    }
}

// ============================================================================
// SceneNode — renamed v2 type (replaces ContextNode)
// ============================================================================

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct SceneNode {
    pub id_hash: u64,
    pub scene_id: u64,
    pub topic_ids: Vec<u64>,
    pub depth: u32,
    pub vector_page_ref: u64,
    pub importance: f32,
    pub valence: f64,
    pub arousal: f64,
    pub created_at: i64,
    pub updated_at: i64,
    pub edge_ids: Vec<u64>,
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_context_node_roundtrip() {
        let node = ContextNode {
            id_hash: 12345,
            context_id: 67890,
            vector_page_ref: 42,
            importance: 0.85,
            valence: 0.0,
            arousal: 0.0,
            created_at: 1000000,
            updated_at: 2000000,
            version: 1,
            edge_ptrs: vec![100, 200, 300],
        };
        let data = node.serialize().unwrap();
        assert_eq!(node, ContextNode::deserialize(&data).unwrap());
    }

    #[test]
    fn test_context_node_empty_edges() {
        let node = ContextNode {
            id_hash: 1,
            context_id: 2,
            vector_page_ref: 0,
            importance: 0.0,
            valence: 0.0,
            arousal: 0.0,
            created_at: 0,
            updated_at: 0,
            version: 0,
            edge_ptrs: vec![],
        };
        assert_eq!(
            node,
            ContextNode::deserialize(&node.serialize().unwrap()).unwrap()
        );
    }

    #[test]
    fn test_context_node_slot_size() {
        let node = ContextNode {
            id_hash: 1,
            context_id: 2,
            vector_page_ref: 3,
            importance: 0.5,
            valence: -0.3,
            arousal: 0.7,
            created_at: 0,
            updated_at: 0,
            version: 0,
            edge_ptrs: vec![10, 20],
        };
        let data = node.serialize().unwrap();
        let restored = ContextNode::deserialize(&data).unwrap();
        assert_eq!(node, restored);
    }
}
