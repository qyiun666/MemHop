// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L3 HypergraphSlot — universal hypergraph engine.
// Each L2 context can associate with multiple L3 hypergraphs (path/context/URL/manual).

use crate::util::io_helpers::*;
use serde::{Deserialize, Serialize, Serializer};
use std::io::{self, Cursor, Write};

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

    fn data_bytes(&self) -> Vec<u8> {
        match self {
            Self::Path(p) => p.as_bytes().to_vec(),
            Self::Context(id) => id.to_le_bytes().to_vec(),
            Self::Url(u) => u.as_bytes().to_vec(),
            Self::Manual => vec![],
        }
    }

    fn from_data(kind: SourceKind, data: &[u8]) -> io::Result<Self> {
        match kind {
            SourceKind::Path => {
                let s = String::from_utf8(data.to_vec())
                    .map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?;
                Ok(Self::Path(s))
            }
            SourceKind::Context => {
                if data.len() < 8 {
                    return Err(io::Error::new(
                        io::ErrorKind::InvalidData,
                        "Context source needs 8 bytes",
                    ));
                }
                let id = u64::from_le_bytes(data[..8].try_into().map_err(|_| {
                    io::Error::new(io::ErrorKind::InvalidData, "truncated context source")
                })?);
                Ok(Self::Context(id))
            }
            SourceKind::Url => {
                let s = String::from_utf8(data.to_vec())
                    .map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?;
                Ok(Self::Url(s))
            }
            SourceKind::Manual => Ok(Self::Manual),
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
    /// Fixed 41 bytes + name + source_data variable.
    pub fn slot_size(&self) -> usize {
        let source_data = self.source.data_bytes();
        41 + self.name.len() + source_data.len()
    }

    pub fn serialize(&self) -> io::Result<Vec<u8>> {
        let mut buf = Vec::with_capacity(self.slot_size());
        let source_data = self.source.data_bytes();
        buf.write_all(&self.id_hash.to_le_bytes())?;
        write_string(&mut buf, &self.name)?;
        buf.write_all(&[self.source.kind() as u8])?;
        buf.write_all(&(source_data.len() as u16).to_le_bytes())?;
        buf.write_all(&source_data)?;
        buf.write_all(&self.node_count.to_le_bytes())?;
        buf.write_all(&self.edge_count.to_le_bytes())?;
        buf.write_all(&self.created_at.to_le_bytes())?;
        buf.write_all(&self.updated_at.to_le_bytes())?;
        buf.write_all(&self.version.to_le_bytes())?;
        Ok(buf)
    }

    pub fn deserialize(data: &[u8]) -> io::Result<Self> {
        let mut c = Cursor::new(data);
        let id_hash = read_u64(&mut c)?;
        let name = read_string(&mut c)?;
        let source_kind = SourceKind::from_u8(read_u8(&mut c)?);
        let source_data_len = read_u16(&mut c)? as usize;
        let remaining = data.len() - c.position() as usize;
        if source_data_len > remaining {
            return Err(io::Error::new(
                io::ErrorKind::UnexpectedEof,
                "HypergraphSlot source data length exceeds data",
            ));
        }
        let mut source_data = vec![0u8; source_data_len];
        std::io::Read::read_exact(&mut c, &mut source_data)?;
        let source = HypergraphSource::from_data(source_kind, &source_data)?;
        let node_count = read_u32(&mut c)?;
        let edge_count = read_u32(&mut c)?;
        let created_at = read_i64(&mut c)?;
        let updated_at = read_i64(&mut c)?;
        let version = read_u32(&mut c)?;
        Ok(HypergraphSlot {
            id_hash,
            name,
            source,
            node_count,
            edge_count,
            created_at,
            updated_at,
            version,
        })
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
    #[serde(skip_serializing_if = "Option::is_none")]
    pub source_ref: Option<String>, // e.g. "/path/file.rs:L10-L50"
    pub importance: f32,
    pub created_at: i64,
    pub updated_at: i64,
    pub version: u32,
}

impl HypergraphNode {
    /// Fixed 40 bytes + title + node_type + content + keywords + source_ref variable.
    pub fn slot_size(&self) -> usize {
        40 + 2
            + self.title.len()
            + 2
            + self.node_type.len()
            + 2
            + self.content.len()
            + self.keywords.iter().map(|k| 2 + k.len()).sum::<usize>()
            + 2
            + self.source_ref.as_ref().map_or(2, |s| 2 + s.len())
    }

    pub fn serialize(&self) -> io::Result<Vec<u8>> {
        let mut buf = Vec::with_capacity(self.slot_size());
        buf.write_all(&self.id_hash.to_le_bytes())?;
        buf.write_all(&self.graph_id.to_le_bytes())?;
        buf.write_all(&self.importance.to_le_bytes())?;
        buf.write_all(&self.created_at.to_le_bytes())?;
        buf.write_all(&self.updated_at.to_le_bytes())?;
        buf.write_all(&self.version.to_le_bytes())?;
        write_string(&mut buf, &self.title)?;
        write_string(&mut buf, &self.node_type)?;
        write_string(&mut buf, &self.content)?;
        write_string_vec(&mut buf, &self.keywords)?;
        write_optional_string(&mut buf, &self.source_ref)?;
        Ok(buf)
    }

    pub fn deserialize(data: &[u8]) -> io::Result<Self> {
        let mut c = Cursor::new(data);
        let id_hash = read_u64(&mut c)?;
        let graph_id = read_u64(&mut c)?;
        let importance = read_f32(&mut c)?;
        let created_at = read_i64(&mut c)?;
        let updated_at = read_i64(&mut c)?;
        let version = read_u32(&mut c)?;
        let title = read_string(&mut c)?;
        let node_type = read_string(&mut c)?;
        let content = read_string(&mut c)?;
        let keywords = read_string_vec(&mut c)?;
        let source_ref = read_optional_string(&mut c)?;
        Ok(HypergraphNode {
            id_hash,
            graph_id,
            title,
            node_type,
            content,
            keywords,
            source_ref,
            importance,
            created_at,
            updated_at,
            version,
        })
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
    #[serde(skip_serializing_if = "Option::is_none")]
    pub label: Option<String>,
    pub created_at: i64,
}

impl HypergraphEdge {
    /// Fixed 31 bytes + `node_ids.len() * 8` + label variable.
    pub fn slot_size(&self) -> usize {
        31 + self.node_ids.len() * 8 + self.label.as_ref().map_or(2, |l| 2 + l.len())
    }

    pub fn serialize(&self) -> io::Result<Vec<u8>> {
        let mut buf = Vec::with_capacity(self.slot_size());
        buf.write_all(&self.id_hash.to_le_bytes())?;
        buf.write_all(&self.graph_id.to_le_bytes())?;
        buf.write_all(&[self.kind as u8])?;
        buf.write_all(&(self.node_ids.len() as u16).to_le_bytes())?;
        buf.write_all(&self.weight.to_le_bytes())?;
        buf.write_all(&self.created_at.to_le_bytes())?;
        for &id in &self.node_ids {
            buf.write_all(&id.to_le_bytes())?;
        }
        write_optional_string(&mut buf, &self.label)?;
        Ok(buf)
    }

    pub fn deserialize(data: &[u8]) -> io::Result<Self> {
        let mut c = Cursor::new(data);
        let id_hash = read_u64(&mut c)?;
        let graph_id = read_u64(&mut c)?;
        let kind = GraphEdgeKind::from_u8(read_u8(&mut c)?);
        let node_count = read_u16(&mut c)? as usize;
        let weight = read_f32(&mut c)?;
        let created_at = read_i64(&mut c)?;
        const EDGE_FIXED: usize = 31;
        let variable_len = node_count * 8;
        if EDGE_FIXED + variable_len > data.len() {
            return Err(io::Error::new(
                io::ErrorKind::UnexpectedEof,
                "HypergraphEdge node_ids length exceeds data",
            ));
        }
        let mut node_ids = Vec::with_capacity(node_count);
        for _ in 0..node_count {
            node_ids.push(read_u64(&mut c)?);
        }
        let label = read_optional_string(&mut c)?;
        Ok(HypergraphEdge {
            id_hash,
            graph_id,
            kind,
            node_ids,
            weight,
            label,
            created_at,
        })
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
        assert_eq!(data.len(), slot.slot_size());
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
            created_at: 1000,
            updated_at: 2000,
            version: 1,
        };
        let data = node.serialize().unwrap();
        assert_eq!(data.len(), node.slot_size());
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
            created_at: 1000,
        };
        let data = edge.serialize().unwrap();
        assert_eq!(data.len(), edge.slot_size());
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
        // 41 + 4 + 0 = 45
        assert_eq!(slot.slot_size(), 45);
    }
}
