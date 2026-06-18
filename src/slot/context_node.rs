// L1 ContextNode - lightweight graph node in the hypergraph skeleton
//
// L1 is a pure structural layer. Each ContextNode points to exactly one
// L2 Context (via context_id). The node carries only a vector reference
// (for similarity search) and importance weight — no text content.
//
// Text content belongs to L2 (summary) and L4 (raw archive).

use std::io::{self, Cursor, Read, Write};

/// L1 graph node — points to one L2 context
#[derive(Debug, Clone, PartialEq)]
pub struct ContextNode {
    pub id_hash: u64,
    pub context_id: u64,      // Points to L2 ContextSlot id_hash
    pub vector_page_ref: u64, // Vector page reference for similarity search
    pub importance: f32,      // Node importance weight
    pub valence: f64,         // Emotional valence (-1.0 to 1.0), used for decay modulation
    pub arousal: f64,         // Emotional arousal (0.0 to 1.0), used for decay modulation
    pub created_at: i64,
    pub updated_at: i64,
    pub version: u32,
    pub edge_ptrs: Vec<u64>, // Associated Hyperedge id_hash list
}

impl ContextNode {
    /// Calculate the total serialized size in bytes
    ///
    /// Fixed: 8 (id_hash) + 8 (context_id) + 8 (vector_page_ref) +
    ///        4 (importance) + 8 (valence) + 8 (arousal) +
    ///        8 (created_at) + 8 (updated_at) + 4 (version) +
    ///        2 (edge_count) = 66 bytes
    /// Variable: edge_ptrs.len() * 8
    pub fn slot_size(&self) -> usize {
        66 + self.edge_ptrs.len() * 8
    }

    /// Serialize to bytes
    ///
    /// # Binary format
    /// | Field           | Type    | Bytes |
    /// |-----------------|---------|-------|
    /// | id_hash         | u64 LE  | 8     |
    /// | context_id      | u64 LE  | 8     |
    /// | vector_page_ref | u64 LE  | 8     |
    /// | importance      | f32 LE  | 4     |
    /// | valence         | f64 LE  | 8     |
    /// | arousal         | f64 LE  | 8     |
    /// | created_at      | i64 LE  | 8     |
    /// | updated_at      | i64 LE  | 8     |
    /// | version         | u32 LE  | 4     |
    /// | edge_count      | u16 LE  | 2     |
    /// | edge_ptrs       | [u64]   | N*8   |
    pub fn serialize(&self) -> io::Result<Vec<u8>> {
        let mut buf = Vec::with_capacity(self.slot_size());

        buf.write_all(&self.id_hash.to_le_bytes())?;
        buf.write_all(&self.context_id.to_le_bytes())?;
        buf.write_all(&self.vector_page_ref.to_le_bytes())?;
        buf.write_all(&self.importance.to_le_bytes())?;
        buf.write_all(&self.valence.to_le_bytes())?;
        buf.write_all(&self.arousal.to_le_bytes())?;
        buf.write_all(&self.created_at.to_le_bytes())?;
        buf.write_all(&self.updated_at.to_le_bytes())?;
        buf.write_all(&self.version.to_le_bytes())?;
        buf.write_all(&(self.edge_ptrs.len() as u16).to_le_bytes())?;

        for &ptr in &self.edge_ptrs {
            buf.write_all(&ptr.to_le_bytes())?;
        }

        Ok(buf)
    }

    /// Deserialize from bytes
    pub fn deserialize(data: &[u8]) -> io::Result<Self> {
        let mut c = Cursor::new(data);

        let id_hash = read_u64(&mut c)?;
        let context_id = read_u64(&mut c)?;
        let vector_page_ref = read_u64(&mut c)?;
        let importance = read_f32(&mut c)?;
        let valence = read_f64(&mut c)?;
        let arousal = read_f64(&mut c)?;
        let created_at = read_i64(&mut c)?;
        let updated_at = read_i64(&mut c)?;
        let version = read_u32(&mut c)?;
        let edge_count = read_u16(&mut c)? as usize;

        let mut edge_ptrs = Vec::with_capacity(edge_count);
        for _ in 0..edge_count {
            edge_ptrs.push(read_u64(&mut c)?);
        }

        Ok(ContextNode {
            id_hash,
            context_id,
            vector_page_ref,
            importance,
            valence,
            arousal,
            created_at,
            updated_at,
            version,
            edge_ptrs,
        })
    }
}

// ---------------------------------------------------------------------------
// Inline read helpers (same pattern as other slot modules)
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

fn read_u16(c: &mut Cursor<&[u8]>) -> io::Result<u16> {
    let mut buf = [0u8; 2];
    c.read_exact(&mut buf)?;
    Ok(u16::from_le_bytes(buf))
}

fn read_f32(c: &mut Cursor<&[u8]>) -> io::Result<f32> {
    let mut buf = [0u8; 4];
    c.read_exact(&mut buf)?;
    Ok(f32::from_le_bytes(buf))
}

fn read_f64(c: &mut Cursor<&[u8]>) -> io::Result<f64> {
    let mut buf = [0u8; 8];
    c.read_exact(&mut buf)?;
    Ok(f64::from_le_bytes(buf))
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
        assert_eq!(data.len(), node.slot_size());
        // 66 + 24 = 90
        assert_eq!(data.len(), 90);
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

        let data = node.serialize().unwrap();
        // Fixed size only: 66 bytes
        assert_eq!(data.len(), 66);
        let restored = ContextNode::deserialize(&data).unwrap();
        assert_eq!(node, restored);
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
        // 66 + 2*8 = 82
        assert_eq!(node.slot_size(), 82);
    }
}
