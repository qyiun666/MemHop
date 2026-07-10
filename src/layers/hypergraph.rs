// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L3 HypergraphSlot — universal hypergraph engine.
// Each L2 context can associate with multiple L3 hypergraphs (path/context/URL/manual).

use crate::api::MemHopError;
use serde::{Deserialize, Serialize, Serializer};

/// Serialize a u64 hash as a 16-char lowercase hex string.
pub fn serialize_hash_as_hex<S: Serializer>(hash: &u64, serializer: S) -> Result<S::Ok, S::Error> {
    serializer.serialize_str(&format!("{:016x}", hash))
}

/// Serialize a slice of u64 hashes as hex strings.
pub fn serialize_hashes_as_hex<S: Serializer>(
    hashes: &[u64],
    serializer: S,
) -> Result<S::Ok, S::Error> {
    let hex_vec: Vec<String> = hashes.iter().map(|h| format!("{:016x}", h)).collect();
    hex_vec.serialize(serializer)
}

pub fn deserialize_hash_from_hex<'de, D: serde::Deserializer<'de>>(
    deserializer: D,
) -> Result<u64, D::Error> {
    let s = String::deserialize(deserializer)?;
    u64::from_str_radix(&s, 16).map_err(serde::de::Error::custom)
}

pub fn deserialize_hashes_from_hex<'de, D: serde::Deserializer<'de>>(
    deserializer: D,
) -> Result<Vec<u64>, D::Error> {
    let vec: Vec<String> = Vec::deserialize(deserializer)?;
    vec.into_iter()
        .map(|s| u64::from_str_radix(&s, 16).map_err(serde::de::Error::custom))
        .collect()
}

// ============================================================================
// HypergraphSource — how the hypergraph was created
// ============================================================================

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[repr(u8)]
pub enum SourceKind {
    Path = 0,
    Context = 1,
    Url = 2,
    Manual = 3,
}

impl SourceKind {
    pub fn from_u8(v: u8) -> Self {
        match v {
            0 => Self::Path,
            1 => Self::Context,
            2 => Self::Url,
            3 => Self::Manual,
            _ => Self::Manual,
        }
    }
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub enum HypergraphSource {
    Path(String), // File path
    Context(u64), // L2 context id_hash
    Url(String),  // External URL
    Manual,
}

impl HypergraphSource {
    pub fn kind(&self) -> SourceKind {
        match self {
            Self::Path(_) => SourceKind::Path,
            Self::Context(_) => SourceKind::Context,
            Self::Url(_) => SourceKind::Url,
            Self::Manual => SourceKind::Manual,
        }
    }

    /// Domain name for display (not Rust debug format).
    pub fn domain_name(&self) -> &str {
        match self {
            Self::Path(_) => "file",
            Self::Context(_) => "context",
            Self::Url(_) => "url",
            Self::Manual => "manual",
        }
    }
}

// ============================================================================
// HypergraphSlot — container metadata
// ============================================================================

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct HypergraphSlot {
    pub id_hash: u64,
    pub name: String,
    pub source: HypergraphSource,
    pub node_count: u32,
    pub edge_count: u32,
    pub created_at: i64,
    pub updated_at: i64,
    pub version: u32,
}

impl HypergraphSlot {
    pub fn serialize(&self) -> Result<Vec<u8>, MemHopError> {
        bincode::serialize(self).map_err(|e| MemHopError::Serialization(e.to_string()))
    }

    pub fn deserialize(data: &[u8]) -> Result<Self, MemHopError> {
        bincode::deserialize(data).map_err(|e| MemHopError::Deserialization(e.to_string()))
    }
}

// ============================================================================
// HypergraphNode
// ============================================================================

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct HypergraphNode {
    #[serde(
        serialize_with = "serialize_hash_as_hex",
        deserialize_with = "deserialize_hash_from_hex"
    )]
    pub id_hash: u64,
    #[serde(
        serialize_with = "serialize_hash_as_hex",
        deserialize_with = "deserialize_hash_from_hex"
    )]
    pub graph_id: u64,
    pub title: String,
    pub node_type: String, // Generic type tag (e.g. "function", "concept", "file")
    pub content: String,
    pub keywords: Vec<String>,
    pub source_ref: Option<String>, // e.g. "/path/file.rs:L10-L50"
    pub importance: f32,
    pub valid_from: i64,
    pub valid_until: i64,
    pub summary: Option<String>,
    pub created_at: i64,
    pub updated_at: i64,
    pub version: u32,
}

impl HypergraphNode {
    pub fn serialize(&self) -> Result<Vec<u8>, MemHopError> {
        bincode::serialize(self).map_err(|e| MemHopError::Serialization(e.to_string()))
    }

    pub fn deserialize(data: &[u8]) -> Result<Self, MemHopError> {
        bincode::deserialize(data).map_err(|e| MemHopError::Deserialization(e.to_string()))
    }
}

// ============================================================================
// GraphEdgeKind
// ============================================================================

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[repr(u8)]
pub enum GraphEdgeKind {
    Related = 0,
    Causal = 1,
    PartOf = 2,
    Sequence = 3,
    Dependency = 4,
    Custom = 5,
}

impl GraphEdgeKind {
    pub fn from_u8(v: u8) -> Self {
        match v {
            0 => Self::Related,
            1 => Self::Causal,
            2 => Self::PartOf,
            3 => Self::Sequence,
            4 => Self::Dependency,
            5 => Self::Custom,
            _ => Self::Related,
        }
    }
}

// ============================================================================
// HypergraphEdge
// ============================================================================

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct HypergraphEdge {
    #[serde(
        serialize_with = "serialize_hash_as_hex",
        deserialize_with = "deserialize_hash_from_hex"
    )]
    pub id_hash: u64,
    #[serde(
        serialize_with = "serialize_hash_as_hex",
        deserialize_with = "deserialize_hash_from_hex"
    )]
    pub graph_id: u64,
    pub kind: GraphEdgeKind,
    #[serde(
        serialize_with = "serialize_hashes_as_hex",
        deserialize_with = "deserialize_hashes_from_hex"
    )]
    pub node_ids: Vec<u64>, // >=2 nodes, supports hyperedge
    pub weight: f32,
    pub label: Option<String>,
    pub description: Option<String>,
    pub confidence: f32,
    pub valid_from: i64,
    pub valid_until: i64,
    pub created_at: i64,
}

impl HypergraphEdge {
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
    fn test_hypergraph_slot_roundtrip_path() {
        let slot = HypergraphSlot {
            id_hash: 1,
            name: "memhop code graph".to_string(),
            source: HypergraphSource::Path("/src/lib.rs".to_string()),
            node_count: 42,
            edge_count: 100,
            created_at: 1000,
            updated_at: 2000,
            version: 1,
        };
        let data = slot.serialize().unwrap();
        assert_eq!(slot, HypergraphSlot::deserialize(&data).unwrap());
    }

    #[test]
    fn test_hypergraph_slot_roundtrip_context() {
        let slot = HypergraphSlot {
            id_hash: 2,
            name: "context graph".to_string(),
            source: HypergraphSource::Context(12345),
            node_count: 5,
            edge_count: 3,
            created_at: 0,
            updated_at: 0,
            version: 0,
        };
        let data = slot.serialize().unwrap();
        assert_eq!(slot, HypergraphSlot::deserialize(&data).unwrap());
    }

    #[test]
    fn test_hypergraph_slot_roundtrip_url() {
        let slot = HypergraphSlot {
            id_hash: 3,
            name: "external".to_string(),
            source: HypergraphSource::Url("https://example.com/graph".to_string()),
            node_count: 0,
            edge_count: 0,
            created_at: 0,
            updated_at: 0,
            version: 0,
        };
        let data = slot.serialize().unwrap();
        assert_eq!(slot, HypergraphSlot::deserialize(&data).unwrap());
    }

    #[test]
    fn test_hypergraph_slot_roundtrip_manual() {
        let slot = HypergraphSlot {
            id_hash: 4,
            name: "manual".to_string(),
            source: HypergraphSource::Manual,
            node_count: 1,
            edge_count: 0,
            created_at: 100,
            updated_at: 200,
            version: 1,
        };
        let data = slot.serialize().unwrap();
        assert_eq!(slot, HypergraphSlot::deserialize(&data).unwrap());
    }

    #[test]
    fn test_hypergraph_node_roundtrip() {
        let node = HypergraphNode {
            id_hash: 1,
            graph_id: 100,
            title: "MemHop::open".to_string(),
            node_type: "function".to_string(),
            content: "Opens or creates a MemHop database".to_string(),
            keywords: vec!["open".to_string(), "database".to_string()],
            source_ref: Some("/src/lib.rs:L114-L288".to_string()),
            importance: 0.9,
            summary: None,
            valid_from: 0,
            valid_until: 0,
            created_at: 1000,
            updated_at: 2000,
            version: 1,
        };
        let data = node.serialize().unwrap();
        assert_eq!(node, HypergraphNode::deserialize(&data).unwrap());
    }

    #[test]
    fn test_hypergraph_node_minimal() {
        let node = HypergraphNode {
            id_hash: 2,
            graph_id: 1,
            title: "concept".to_string(),
            node_type: "concept".to_string(),
            content: "six-layer architecture".to_string(),
            keywords: vec![],
            source_ref: None,
            importance: 0.5,
            summary: None,
            valid_from: 0,
            valid_until: 0,
            created_at: 0,
            updated_at: 0,
            version: 0,
        };
        let data = node.serialize().unwrap();
        assert_eq!(node, HypergraphNode::deserialize(&data).unwrap());
    }

    #[test]
    fn test_hypergraph_edge_roundtrip() {
        let edge = HypergraphEdge {
            id_hash: 1,
            graph_id: 100,
            kind: GraphEdgeKind::Dependency,
            node_ids: vec![10, 20, 30],
            weight: 0.8,
            label: Some("depends_on".to_string()),
            confidence: 0.9,
            description: None,
            valid_from: 0,
            valid_until: 0,
            created_at: 1000,
        };
        let data = edge.serialize().unwrap();
        assert_eq!(edge, HypergraphEdge::deserialize(&data).unwrap());
    }

    #[test]
    fn test_hypergraph_edge_binary() {
        let edge = HypergraphEdge {
            id_hash: 2,
            graph_id: 1,
            kind: GraphEdgeKind::Sequence,
            node_ids: vec![10, 20],
            weight: 1.0,
            label: None,
            confidence: 0.9,
            description: None,
            valid_from: 0,
            valid_until: 0,
            created_at: 0,
        };
        let data = edge.serialize().unwrap();
        assert_eq!(edge, HypergraphEdge::deserialize(&data).unwrap());
    }

    #[test]
    fn test_hypergraph_edge_all_kinds() {
        for kind in [
            GraphEdgeKind::Related,
            GraphEdgeKind::Causal,
            GraphEdgeKind::PartOf,
            GraphEdgeKind::Sequence,
            GraphEdgeKind::Dependency,
            GraphEdgeKind::Custom,
        ] {
            let edge = HypergraphEdge {
                id_hash: 99,
                graph_id: 1,
                kind,
                node_ids: vec![1, 2],
                weight: 0.5,
                label: None,
                confidence: 0.9,
                description: None,
                valid_from: 0,
                valid_until: 0,
                created_at: 0,
            };
            let data = edge.serialize().unwrap();
            assert_eq!(edge, HypergraphEdge::deserialize(&data).unwrap());
        }
    }

    #[test]
    fn test_hypergraph_slot_size_calculation() {
        let slot = HypergraphSlot {
            id_hash: 1,
            name: "test".to_string(),
            source: HypergraphSource::Manual,
            node_count: 0,
            edge_count: 0,
            created_at: 0,
            updated_at: 0,
            version: 0,
        };
        let data = slot.serialize().unwrap();
        let restored = HypergraphSlot::deserialize(&data).unwrap();
        assert_eq!(slot, restored);
    }
}

// v2 type aliases (end of file placeholder)

/// v2 alias for HypergraphSlot (internal use only; external consumers should use query::types::GraphSlot).
pub(crate) type GraphSlot = HypergraphSlot;
/// v2 alias for HypergraphNode (internal use only; external consumers should use query::types::GraphNode).
pub(crate) type GraphNode = HypergraphNode;
/// v2 alias for HypergraphEdge (internal use only; external consumers should use query::types::GraphEdge).
pub(crate) type GraphEdge = HypergraphEdge;
