// L1 HyperedgeSlot - edges in the hypergraph skeleton
//
// L1 is a pure structural layer. Hyperedges connect ContextNodes (L1 nodes)
// to express relationships between L2 contexts (scenes).
// No metadata payload — the `kind` enum carries the semantic meaning.

use std::io::{self, Cursor, Read, Write};

/// Hyperedge kind — describes the relationship type between connected contexts
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[repr(u8)]
pub enum HyperedgeKind {
    CoOccurrence = 0, // Contexts that frequently appear together
    Causal = 1,       // One context causes/influences another
    Semantic = 2,     // Semantically related contexts
    Temporal = 3,     // Time-ordered contexts
    Hierarchical = 4, // Parent-child context relationship
    Sequence = 5,     // Ordered sequence of contexts
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
            _ => Self::CoOccurrence, // fallback
        }
    }
}

/// L1 hyperedge — connects multiple ContextNodes (supports true hyperedges)
///
/// Inline storage: up to 8 node_ptrs stored directly. If more than 8
/// nodes are needed, `overflow_page` points to an overflow page.
#[derive(Debug, Clone, PartialEq)]
pub struct HyperedgeSlot {
    pub id_hash: u64,
    pub kind: HyperedgeKind,
    pub node_ptrs: Vec<u64>, // Connected ContextNode id_hash list
    pub weight: f32,
    pub created_at: i64,
    pub updated_at: i64,
    pub version: u32,
    pub overflow_page: u32,
}

impl HyperedgeSlot {
    /// Calculate the total serialized size in bytes
    ///
    /// Fixed: 8 (id_hash) + 1 (kind) + 1 (node_count) + 4 (weight) +
    ///        8 (created_at) + 8 (updated_at) + 4 (version) + 4 (overflow_page) +
    ///        64 (8 × u64 inline node_ptrs) = 102 bytes
    pub fn slot_size(&self) -> usize {
        102
    }

    /// Serialize to bytes
    pub fn serialize(&self) -> io::Result<Vec<u8>> {
        let mut buffer = Vec::with_capacity(self.slot_size());

        buffer.write_all(&self.id_hash.to_le_bytes())?;
        buffer.write_all(&[self.kind as u8])?;

        let node_count = if self.node_ptrs.len() > 8 {
            8
        } else {
            self.node_ptrs.len()
        } as u8;
        buffer.write_all(&[node_count])?;

        buffer.write_all(&self.weight.to_le_bytes())?;
        buffer.write_all(&self.created_at.to_le_bytes())?;
        buffer.write_all(&self.updated_at.to_le_bytes())?;
        buffer.write_all(&self.version.to_le_bytes())?;
        buffer.write_all(&self.overflow_page.to_le_bytes())?;

        // Inline node pointers (always 8 slots, pad with zeros)
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

    /// Deserialize from bytes
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

        // Read 8 inline node pointers
        let mut node_ptrs = Vec::with_capacity(node_count as usize);
        for _ in 0..8 {
            let ptr = read_u64(&mut cursor)?;
            node_ptrs.push(ptr);
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
    let mut buf = [0u8; 8];
    c.read_exact(&mut buf)?;
    Ok(u64::from_le_bytes(buf))
}

fn read_i64(c: &mut Cursor<&[u8]>) -> io::Result<i64> {
    let mut buf = [0u8; 8];
    c.read_exact(&mut buf)?;
    Ok(i64::from_le_bytes(buf))
}

fn read_u32(c: &mut Cursor<&[u8]>) -> io::Result<u32> {
    let mut buf = [0u8; 4];
    c.read_exact(&mut buf)?;
    Ok(u32::from_le_bytes(buf))
}

fn read_u8(c: &mut Cursor<&[u8]>) -> io::Result<u8> {
    let mut buf = [0u8; 1];
    c.read_exact(&mut buf)?;
    Ok(buf[0])
}

fn read_f32(c: &mut Cursor<&[u8]>) -> io::Result<f32> {
    let mut buf = [0u8; 4];
    c.read_exact(&mut buf)?;
    Ok(f32::from_le_bytes(buf))
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
        let deserialized = HyperedgeSlot::deserialize(&serialized).unwrap();
        assert_eq!(slot, deserialized);
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

        let serialized = slot.serialize().unwrap();
        let deserialized = HyperedgeSlot::deserialize(&serialized).unwrap();
        assert_eq!(slot, deserialized);
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

        let serialized = slot.serialize().unwrap();
        let deserialized = HyperedgeSlot::deserialize(&serialized).unwrap();

        // Only first 8 nodes stored inline
        assert_eq!(deserialized.node_ptrs.len(), 8);
        assert_eq!(deserialized.node_ptrs, vec![10, 20, 30, 40, 50, 60, 70, 80]);
        assert_eq!(deserialized.overflow_page, 123);
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

        let serialized = slot.serialize().unwrap();
        let deserialized = HyperedgeSlot::deserialize(&serialized).unwrap();
        assert_eq!(slot, deserialized);
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

            let serialized = slot.serialize().unwrap();
            let deserialized = HyperedgeSlot::deserialize(&serialized).unwrap();
            assert_eq!(slot, deserialized);
        }
    }
}
