// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L6 PathwayWeight — procedural memory as weighted condition-action pathways.
//!
//! Each slot stores the empirical weight of a transition from a source
//! condition node to a target action node, along with usage statistics and
//! optional metadata.

use crate::api::MemHopError;
use serde::{Deserialize, Serialize};
use std::io::{self, Cursor, Read, Write};

// ============================================================================
// PathwayWeightSlot
// ============================================================================

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct PathwayWeightSlot {
    pub id_hash: u64,
    pub source_node: String,
    pub target_node: String,
    pub weight: f32,
    pub trigger_count: u32,
    pub success_rate: f32,
    pub last_accessed: u64,
    pub metadata: String,
    pub created_at: i64,
    pub updated_at: i64,
    pub version: u32,
}

impl PathwayWeightSlot {
    pub fn serialize(&self) -> Result<Vec<u8>, MemHopError> {
        bincode::serialize(self).map_err(|e| MemHopError::Serialization(e.to_string()))
    }

    pub fn deserialize(data: &[u8]) -> Result<Self, MemHopError> {
        bincode::deserialize(data).map_err(|e| MemHopError::Deserialization(e.to_string()))
    }

    /// Serialize a vector of pathway slots into a single byte stream.
    /// Format: [count: u32][len: u32][slot bytes]...
    pub fn serialize_pathways(pathways: &[PathwayWeightSlot]) -> io::Result<Vec<u8>> {
        let mut buf = Vec::new();
        buf.write_all(&(pathways.len() as u32).to_le_bytes())?;
        for p in pathways {
            let slot_bytes =
                bincode::serialize(p).map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?;
            buf.write_all(&(slot_bytes.len() as u32).to_le_bytes())?;
            buf.write_all(&slot_bytes)?;
        }
        Ok(buf)
    }

    /// Deserialize a vector of pathway slots from a byte stream.
    pub fn deserialize_pathways(data: &[u8]) -> io::Result<Vec<PathwayWeightSlot>> {
        let mut c = Cursor::new(data);
        let count = read_u32_le(&mut c)? as usize;
        if 4 + count * 4 > data.len() {
            return Err(io::Error::new(
                io::ErrorKind::UnexpectedEof,
                "PathwayWeightSlot list header exceeds data",
            ));
        }
        let mut pathways = Vec::with_capacity(count);
        for _ in 0..count {
            let len = read_u32_le(&mut c)? as usize;
            if c.position() as usize + len > data.len() {
                return Err(io::Error::new(
                    io::ErrorKind::UnexpectedEof,
                    "PathwayWeightSlot entry length exceeds data",
                ));
            }
            let mut slot_buf = vec![0u8; len];
            c.read_exact(&mut slot_buf)?;
            pathways.push(
                bincode::deserialize(&slot_buf)
                    .map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?,
            );
        }
        Ok(pathways)
    }
}

#[inline]
fn read_u32_le(cursor: &mut Cursor<&[u8]>) -> io::Result<u32> {
    let mut buf = [0u8; 4];
    cursor.read_exact(&mut buf)?;
    Ok(u32::from_le_bytes(buf))
}

// ============================================================================
// Tests
// ============================================================================

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_pathway_weight_roundtrip() {
        let pw = PathwayWeightSlot {
            id_hash: 123456789,
            source_node: "condition:deploy".into(),
            target_node: "action:restart_service".into(),
            weight: 0.92,
            trigger_count: 47,
            success_rate: 0.88,
            last_accessed: 1700000000000,
            metadata: r#"{"strategy":"react"}"#.into(),
            created_at: 1000,
            updated_at: 2000,
            version: 1,
        };
        let data = pw.serialize().unwrap();
        assert_eq!(pw, PathwayWeightSlot::deserialize(&data).unwrap());
    }

    #[test]
    fn test_pathway_weight_empty_strings() {
        let pw = PathwayWeightSlot {
            id_hash: 1,
            source_node: "".into(),
            target_node: "".into(),
            weight: 0.0,
            trigger_count: 0,
            success_rate: 0.0,
            last_accessed: 0,
            metadata: "".into(),
            created_at: 0,
            updated_at: 0,
            version: 0,
        };
        assert_eq!(
            pw,
            PathwayWeightSlot::deserialize(&pw.serialize().unwrap()).unwrap()
        );
    }

    #[test]
    fn test_pathway_weight_list_roundtrip() {
        let pathways = vec![
            PathwayWeightSlot {
                id_hash: 1,
                source_node: "a".into(),
                target_node: "b".into(),
                weight: 0.5,
                trigger_count: 3,
                success_rate: 0.9,
                last_accessed: 100,
                metadata: "meta1".into(),
                created_at: 10,
                updated_at: 20,
                version: 1,
            },
            PathwayWeightSlot {
                id_hash: 2,
                source_node: "c".into(),
                target_node: "d".into(),
                weight: 0.7,
                trigger_count: 5,
                success_rate: 0.8,
                last_accessed: 200,
                metadata: "meta2".into(),
                created_at: 30,
                updated_at: 40,
                version: 2,
            },
        ];
        let data = PathwayWeightSlot::serialize_pathways(&pathways).unwrap();
        let restored = PathwayWeightSlot::deserialize_pathways(&data).unwrap();
        assert_eq!(pathways, restored);
    }

    #[test]
    fn test_pathway_weight_empty_list() {
        let pathways: Vec<PathwayWeightSlot> = Vec::new();
        let data = PathwayWeightSlot::serialize_pathways(&pathways).unwrap();
        assert_eq!(data.len(), 4);
        let restored = PathwayWeightSlot::deserialize_pathways(&data).unwrap();
        assert!(restored.is_empty());
    }
}
