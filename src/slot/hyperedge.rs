// Hyperedge slot serialization
use std::io::{self, Cursor, Read, Write};

/// Hyperedge kind enumeration (v0.33)
#[derive(Debug, Clone, Copy, PartialEq)]
#[repr(u8)]
pub enum HyperedgeKind {
    CoOccurrence = 0, // 共现关系
    Causal = 1,       // 因果关系
    Semantic = 2,     // 语义关系
    Temporal = 3,     // 时序关系（v0.33 新增）
    Hierarchical = 4, // 层级关系
    Association = 5,  // 批次关联（v0.33 新增，用于 batch_store）
    Evolution = 6,    // 演化链（v0.33 新增，chain_parent_id 链式关系）
    Custom = 7,
}

impl HyperedgeKind {
    /// Convert from u8 to HyperedgeKind
    pub fn from_u8(value: u8) -> Self {
        match value {
            0 => HyperedgeKind::CoOccurrence,
            1 => HyperedgeKind::Causal,
            2 => HyperedgeKind::Semantic,
            3 => HyperedgeKind::Temporal,
            4 => HyperedgeKind::Hierarchical,
            5 => HyperedgeKind::Association,
            6 => HyperedgeKind::Evolution,
            _ => HyperedgeKind::Custom,
        }
    }
}

/// Hyperedge slot structure (design doc section 3.4)
#[derive(Debug, Clone, PartialEq)]
pub struct HyperedgeSlot {
    pub id_hash: u64,
    pub kind: HyperedgeKind, // v0.33: Strongly typed enum
    pub node_ptrs: Vec<u64>, // Node pointer list
    pub meta: Vec<u8>,       // Metadata
    pub weight: f32,
    pub created_at: i64,
    pub updated_at: i64,
    pub version: u32,
    pub overflow_page: u32,
}

impl HyperedgeSlot {
    /// Calculate the total serialized size in bytes
    pub fn slot_size(&self) -> usize {
        // Fixed part: 8 + 1 + 1 + 2 + 4 + 8 + 8 + 4 + 4 + 64 = 104 bytes
        let fixed_size = 104;

        // Variable part: meta
        let meta_size = self.meta.len();

        fixed_size + meta_size
    }

    /// Serialize the HyperedgeSlot to bytes
    pub fn serialize(&self) -> io::Result<Vec<u8>> {
        let mut buffer = Vec::with_capacity(self.slot_size());

        // Fixed part
        buffer.write_all(&self.id_hash.to_le_bytes())?;
        buffer.write_all(&[self.kind as u8])?; // v0.33: Convert enum to u8

        // Node count (max 8 for inline storage in v0.30)
        let node_count = if self.node_ptrs.len() > 8 {
            8
        } else {
            self.node_ptrs.len()
        } as u8;
        buffer.write_all(&[node_count])?;

        // Meta length and content
        let meta_len = self.meta.len() as u16;
        buffer.write_all(&meta_len.to_le_bytes())?;

        // Weight and timestamps
        buffer.write_all(&self.weight.to_le_bytes())?;
        buffer.write_all(&self.created_at.to_le_bytes())?;
        buffer.write_all(&self.updated_at.to_le_bytes())?;
        buffer.write_all(&self.version.to_le_bytes())?;
        buffer.write_all(&self.overflow_page.to_le_bytes())?;

        // Inline node pointers (always 8, pad with zeros if needed)
        for i in 0..8 {
            let ptr = if i < self.node_ptrs.len() {
                self.node_ptrs[i]
            } else {
                0
            };
            buffer.write_all(&ptr.to_le_bytes())?;
        }

        // Variable part: meta
        buffer.write_all(&self.meta)?;

        Ok(buffer)
    }

    /// Deserialize a HyperedgeSlot from bytes
    pub fn deserialize(data: &[u8]) -> io::Result<Self> {
        let mut cursor = Cursor::new(data);

        // Helper functions for reading
        let read_u64 = |cursor: &mut Cursor<&[u8]>| -> io::Result<u64> {
            let mut buf = [0u8; 8];
            cursor.read_exact(&mut buf)?;
            Ok(u64::from_le_bytes(buf))
        };

        let read_i64 = |cursor: &mut Cursor<&[u8]>| -> io::Result<i64> {
            let mut buf = [0u8; 8];
            cursor.read_exact(&mut buf)?;
            Ok(i64::from_le_bytes(buf))
        };

        let read_u32 = |cursor: &mut Cursor<&[u8]>| -> io::Result<u32> {
            let mut buf = [0u8; 4];
            cursor.read_exact(&mut buf)?;
            Ok(u32::from_le_bytes(buf))
        };

        let read_u16 = |cursor: &mut Cursor<&[u8]>| -> io::Result<u16> {
            let mut buf = [0u8; 2];
            cursor.read_exact(&mut buf)?;
            Ok(u16::from_le_bytes(buf))
        };

        let read_f32 = |cursor: &mut Cursor<&[u8]>| -> io::Result<f32> {
            let mut buf = [0u8; 4];
            cursor.read_exact(&mut buf)?;
            Ok(f32::from_le_bytes(buf))
        };

        let read_u8 = |cursor: &mut Cursor<&[u8]>| -> io::Result<u8> {
            let mut buf = [0u8; 1];
            cursor.read_exact(&mut buf)?;
            Ok(buf[0])
        };

        let id_hash = read_u64(&mut cursor)?;
        let kind_u8 = read_u8(&mut cursor)?;
        let kind = HyperedgeKind::from_u8(kind_u8); // v0.33: Convert u8 to enum
        let node_count = read_u8(&mut cursor)?;
        let meta_len = read_u16(&mut cursor)?;

        let weight = read_f32(&mut cursor)?;
        let created_at = read_i64(&mut cursor)?;
        let updated_at = read_i64(&mut cursor)?;
        let version = read_u32(&mut cursor)?;
        let overflow_page = read_u32(&mut cursor)?;

        // Read inline node pointers (always 8)
        let mut node_ptrs = Vec::with_capacity(node_count as usize);
        for _ in 0..8 {
            let ptr = read_u64(&mut cursor)?;
            node_ptrs.push(ptr);
        }

        // Trim to actual node count
        node_ptrs.truncate(node_count as usize);

        // Read variable part: meta
        let mut meta = vec![0u8; meta_len as usize];
        cursor.read_exact(&mut meta)?;

        Ok(HyperedgeSlot {
            id_hash,
            kind,
            node_ptrs,
            meta,
            weight,
            created_at,
            updated_at,
            version,
            overflow_page,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_hyperedge_serialize_deserialize_basic() {
        let slot = HyperedgeSlot {
            id_hash: 1234567890,
            kind: HyperedgeKind::Semantic, // v0.33: Use enum
            node_ptrs: vec![100, 200, 300],
            meta: vec![1, 2, 3, 4, 5],
            weight: 0.85,
            created_at: 1234567890,
            updated_at: 1234567891,
            version: 1,
            overflow_page: 0,
        };

        let serialized = slot.serialize().unwrap();
        let deserialized = HyperedgeSlot::deserialize(&serialized).unwrap();

        assert_eq!(slot, deserialized);
    }

    #[test]
    fn test_hyperedge_max_inline_nodes() {
        let slot = HyperedgeSlot {
            id_hash: 9876543210,
            kind: HyperedgeKind::CoOccurrence, // v0.33: Use enum
            node_ptrs: vec![1, 2, 3, 4, 5, 6, 7, 8], // Max 8 inline nodes
            meta: vec![],
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
        // Test with more than 8 nodes (v0.30 only stores first 8 inline)
        let slot = HyperedgeSlot {
            id_hash: 1111111111,
            kind: HyperedgeKind::Causal,
            node_ptrs: vec![10, 20, 30, 40, 50, 60, 70, 80, 90, 100], // 10 nodes
            meta: vec![0xFF, 0xFE],
            weight: 0.5,
            created_at: 1234567890,
            updated_at: 1234567891,
            version: 1,
            overflow_page: 123, // Points to overflow page for remaining nodes
        };

        let serialized = slot.serialize().unwrap();
        let deserialized = HyperedgeSlot::deserialize(&serialized).unwrap();

        // Only first 8 nodes should be stored inline
        assert_eq!(deserialized.node_ptrs.len(), 8);
        assert_eq!(deserialized.node_ptrs, vec![10, 20, 30, 40, 50, 60, 70, 80]);
        assert_eq!(deserialized.overflow_page, 123);
    }

    #[test]
    fn test_hyperedge_empty_nodes() {
        let slot = HyperedgeSlot {
            id_hash: 2222222222,
            kind: HyperedgeKind::Association,
            node_ptrs: vec![],
            meta: vec![10, 20, 30],
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
            id_hash: 3333333333,
            kind: HyperedgeKind::Temporal,
            node_ptrs: vec![100, 200],
            meta: vec![1, 2, 3, 4, 5, 6], // 6 bytes
            weight: 0.75,
            created_at: 1234567890,
            updated_at: 1234567891,
            version: 1,
            overflow_page: 0,
        };

        // Fixed: 104, Meta: 6 = 110
        assert_eq!(slot.slot_size(), 110);

        let serialized = slot.serialize().unwrap();
        assert_eq!(serialized.len(), 110);
    }

    #[test]
    fn test_hyperedge_different_kinds() {
        // Test all edge kinds
        let kinds = vec![
            HyperedgeKind::CoOccurrence,
            HyperedgeKind::Causal,
            HyperedgeKind::Semantic,
            HyperedgeKind::Temporal,
            HyperedgeKind::Hierarchical,
            HyperedgeKind::Association,
        ];

        for kind in kinds {
            let slot = HyperedgeSlot {
                id_hash: 4444444444,
                kind,
                node_ptrs: vec![1],
                meta: vec![],
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
