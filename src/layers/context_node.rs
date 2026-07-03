// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L1 ContextNode — lightweight graph node in the hypergraph skeleton.
// Points to one L2 Context; carries only vector ref + importance, no text.

use std::io::{self, Cursor, Read, Write};

#[derive(Debug, Clone, PartialEq)]
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
    /// Fixed 66 bytes + `edge_ptrs.len() * 8`.
    pub fn slot_size(&self) -> usize {
        66 + self.edge_ptrs.len() * 8
    }

    /// Binary format: `[id_hash:u64][context_id:u64][vector_page_ref:u64]`
    /// `[importance:f32][valence:f64][arousal:f64][timestamps:i64*2][version:u32][edge_count:u16][edge_ptrs:[u64]]`
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
fn read_u16(c: &mut Cursor<&[u8]>) -> io::Result<u16> {
    let mut b = [0u8; 2];
    c.read_exact(&mut b)?;
    Ok(u16::from_le_bytes(b))
}
fn read_f32(c: &mut Cursor<&[u8]>) -> io::Result<f32> {
    let mut b = [0u8; 4];
    c.read_exact(&mut b)?;
    Ok(f32::from_le_bytes(b))
}
fn read_f64(c: &mut Cursor<&[u8]>) -> io::Result<f64> {
    let mut b = [0u8; 8];
    c.read_exact(&mut b)?;
    Ok(f64::from_le_bytes(b))
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
        assert_eq!(data.len(), 90); // 66 + 3*8
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
        assert_eq!(node.serialize().unwrap().len(), 66);
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
        assert_eq!(node.slot_size(), 82); // 66 + 2*8
    }
}
