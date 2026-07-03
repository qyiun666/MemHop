// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L1 HyperedgeSlot — edges in the hypergraph skeleton.
// No metadata payload; the `kind` enum carries semantic meaning.

use std::io::{self, Cursor, Read, Write};

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
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
#[derive(Debug, Clone, PartialEq)]
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
    /// Fixed 102 bytes: id(8)+kind(1)+count(1)+weight(4)+timestamps(16)+version(4)+overflow(4)+inline(64).
    pub fn slot_size(&self) -> usize {
        102
    }

    pub fn serialize(&self) -> io::Result<Vec<u8>> {
        let mut buffer = Vec::with_capacity(self.slot_size());
        buffer.write_all(&self.id_hash.to_le_bytes())?;
        buffer.write_all(&[self.kind as u8])?;
        let node_count = self.node_ptrs.len().min(8) as u8;
        buffer.write_all(&[node_count])?;
        buffer.write_all(&self.weight.to_le_bytes())?;
        buffer.write_all(&self.created_at.to_le_bytes())?;
        buffer.write_all(&self.updated_at.to_le_bytes())?;
        buffer.write_all(&self.version.to_le_bytes())?;
        buffer.write_all(&self.overflow_page.to_le_bytes())?;
        // Always 8 inline slots, padded with zeros
        for i in 0..8 {
            let ptr = if i < self.node_ptrs.len() {
                self.node_ptrs[i]
            } else {
                0
            };
            buffer.write_all(&ptr.to_le_bytes())?;
        }
        Ok(buffer)
    }

    pub fn deserialize(data: &[u8]) -> io::Result<Self> {
        let mut cursor = Cursor::new(data);
        let id_hash = read_u64(&mut cursor)?;
        let kind = HyperedgeKind::from_u8(read_u8(&mut cursor)?);
        let node_count = read_u8(&mut cursor)?;
        let weight = read_f32(&mut cursor)?;
        let created_at = read_i64(&mut cursor)?;
        let updated_at = read_i64(&mut cursor)?;
        let version = read_u32(&mut cursor)?;
        let overflow_page = read_u32(&mut cursor)?;
        let mut node_ptrs = Vec::with_capacity(node_count as usize);
        for _ in 0..8 {
            node_ptrs.push(read_u64(&mut cursor)?);
        }
        node_ptrs.truncate(node_count as usize);
        Ok(HyperedgeSlot {
            id_hash,
            kind,
            node_ptrs,
            weight,
            created_at,
            updated_at,
            version,
            overflow_page,
        })
    }
}

// ---------------------------------------------------------------------------
// Inline read helpers
// ---------------------------------------------------------------------------

fn read_u64(c: &mut Cursor<&[u8]>) -> io::Result<u64> {
    let mut b = [0u8; 8];
    c.read_exact(&mut b)?;
    Ok(u64::from_le_bytes(b))
}
fn read_i64(c: &mut Cursor<&[u8]>) -> io::Result<i64> {
    let mut b = [0u8; 8];
    c.read_exact(&mut b)?;
    Ok(i64::from_le_bytes(b))
}
fn read_u32(c: &mut Cursor<&[u8]>) -> io::Result<u32> {
    let mut b = [0u8; 4];
    c.read_exact(&mut b)?;
    Ok(u32::from_le_bytes(b))
}
fn read_u8(c: &mut Cursor<&[u8]>) -> io::Result<u8> {
    let mut b = [0u8; 1];
    c.read_exact(&mut b)?;
    Ok(b[0])
}
fn read_f32(c: &mut Cursor<&[u8]>) -> io::Result<f32> {
    let mut b = [0u8; 4];
    c.read_exact(&mut b)?;
    Ok(f32::from_le_bytes(b))
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
        let serialized = slot.serialize().unwrap();
        assert_eq!(serialized.len(), 102);
        assert_eq!(slot, HyperedgeSlot::deserialize(&serialized).unwrap());
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
        let d = HyperedgeSlot::deserialize(&slot.serialize().unwrap()).unwrap();
        // Only first 8 nodes stored inline
        assert_eq!(d.node_ptrs.len(), 8);
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
        assert_eq!(slot.slot_size(), 102);
        assert_eq!(slot.serialize().unwrap().len(), 102);
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
