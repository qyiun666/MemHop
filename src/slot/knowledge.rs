// L3 KnowledgeSlot + KnowledgeEdge - domain knowledge hypergraph
//
// L3 stores knowledge per domain/scenario, connected via edges forming hypergraphs.

use crate::util::io_helpers::*;
use std::io::{self, Cursor, Write};

/// Knowledge node type
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[repr(u8)]
pub enum KnowledgeType {
    Factual = 0,
    Procedural = 1,
    Conceptual = 2,
    Contextual = 3,
}

impl KnowledgeType {
    pub fn from_u8(value: u8) -> Self {
        match value {
            0 => Self::Factual,
            1 => Self::Procedural,
            2 => Self::Conceptual,
            3 => Self::Contextual,
            _ => Self::Factual,
        }
    }
}

/// L3 knowledge node - belongs to a domain, connected to other nodes via edges
///
/// For project/local path derived nodes, source_ref stores the file path and position.
/// No need to form the entire project into a hypergraph - only selected parts.
#[derive(Debug, Clone, PartialEq)]
pub struct KnowledgeSlot {
    pub id_hash: u64,
    pub title: String,                   // Knowledge node title
    pub domain: String,                  // Domain/scenario name
    pub knowledge_type: KnowledgeType,
    pub text: String,
    pub summary: Option<String>,
    pub keywords: Vec<String>,
    pub edge_count: u16,
    pub edge_ptrs: [u64; 8],             // Edge pointers (max 8 inline)
    pub archive_refs: Vec<u64>,          // Associated L4 Archive indices
    pub source_ref: Option<String>,      // File path + position (e.g., "/path/file.rs:L10-L50")
    pub created_at: i64,
    pub updated_at: i64,
    pub version: u32,
    pub importance: f32,
    pub confidence: f32,
}

impl KnowledgeSlot {
    pub fn slot_size(&self) -> usize {
        // Fixed: 8 + 1 + 2 + 8 + 8 + 4 + 4 + 4 + 64 = 103
        const FIXED: usize = 103;
        FIXED
            + 2 + self.title.len()
            + 2 + self.domain.len()
            + 2 + self.text.len()
            + self.summary.as_ref().map_or(2, |s| 2 + s.len())
            + self.keywords.iter().map(|k| 2 + k.len()).sum::<usize>()
            + 2 + self.archive_refs.len() * 8
            + self.source_ref.as_ref().map_or(2, |s| 2 + s.len())
    }

    pub fn serialize(&self) -> io::Result<Vec<u8>> {
        let mut buf = Vec::with_capacity(self.slot_size());

        // Fixed part
        buf.write_all(&self.id_hash.to_le_bytes())?;
        buf.write_all(&[self.knowledge_type as u8])?;
        buf.write_all(&self.edge_count.min(8).to_le_bytes())?;
        buf.write_all(&self.created_at.to_le_bytes())?;
        buf.write_all(&self.updated_at.to_le_bytes())?;
        buf.write_all(&self.version.to_le_bytes())?;
        buf.write_all(&self.importance.to_le_bytes())?;
        buf.write_all(&self.confidence.to_le_bytes())?;
        for &ptr in &self.edge_ptrs {
            buf.write_all(&ptr.to_le_bytes())?;
        }

        // Variable part
        write_string(&mut buf, &self.title)?;
        write_string(&mut buf, &self.domain)?;
        write_string(&mut buf, &self.text)?;
        write_optional_string(&mut buf, &self.summary)?;
        write_string_vec(&mut buf, &self.keywords)?;

        // Archive refs
        buf.write_all(&(self.archive_refs.len() as u16).to_le_bytes())?;
        for &id in &self.archive_refs {
            buf.write_all(&id.to_le_bytes())?;
        }

        // Source ref
        write_optional_string(&mut buf, &self.source_ref)?;

        Ok(buf)
    }

    pub fn deserialize(data: &[u8]) -> io::Result<Self> {
        let mut c = Cursor::new(data);

        let id_hash = read_u64(&mut c)?;
        let knowledge_type = KnowledgeType::from_u8(read_u8(&mut c)?);
        let edge_count = read_u16(&mut c)?;
        let created_at = read_i64(&mut c)?;
        let updated_at = read_i64(&mut c)?;
        let version = read_u32(&mut c)?;
        let importance = read_f32(&mut c)?;
        let confidence = read_f32(&mut c)?;

        let mut edge_ptrs = [0u64; 8];
        for ptr in &mut edge_ptrs {
            *ptr = read_u64(&mut c)?;
        }

        let title = read_string(&mut c)?;
        let domain = read_string(&mut c)?;
        let text = read_string(&mut c)?;
        let summary = read_optional_string(&mut c)?;
        let keywords = read_string_vec(&mut c)?;

        let ref_count = read_u16(&mut c)? as usize;
        let mut archive_refs = Vec::with_capacity(ref_count);
        for _ in 0..ref_count {
            archive_refs.push(read_u64(&mut c)?);
        }

        let source_ref = read_optional_string(&mut c)?;

        Ok(KnowledgeSlot {
            id_hash, title, domain, knowledge_type, text, summary, keywords,
            edge_count, edge_ptrs, archive_refs, source_ref, created_at, updated_at,
            version, importance, confidence,
        })
    }
}

/// Edge type for L3 knowledge graph
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[repr(u8)]
pub enum KnowledgeEdgeKind {
    Related = 0,
    Causal = 1,
    PartOf = 2,
    Sequence = 3,
    Contradiction = 4,
}

impl KnowledgeEdgeKind {
    pub fn from_u8(value: u8) -> Self {
        match value {
            0 => Self::Related,
            1 => Self::Causal,
            2 => Self::PartOf,
            3 => Self::Sequence,
            4 => Self::Contradiction,
            _ => Self::Related,
        }
    }
}

/// L3 knowledge edge - connects nodes within the same domain
#[derive(Debug, Clone, PartialEq)]
pub struct KnowledgeEdge {
    pub id_hash: u64,
    pub kind: KnowledgeEdgeKind,
    pub source_id: u64,
    pub target_id: u64,
    pub weight: f32,
    pub created_at: i64,
    pub metadata: Option<String>,
}

impl KnowledgeEdge {
    pub fn slot_size(&self) -> usize {
        // Fixed: 8 + 1 + 8 + 8 + 4 + 8 = 37
        const FIXED: usize = 37;
        FIXED + self.metadata.as_ref().map_or(2, |m| 2 + m.len())
    }

    pub fn serialize(&self) -> io::Result<Vec<u8>> {
        let mut buf = Vec::with_capacity(self.slot_size());

        buf.write_all(&self.id_hash.to_le_bytes())?;
        buf.write_all(&[self.kind as u8])?;
        buf.write_all(&self.source_id.to_le_bytes())?;
        buf.write_all(&self.target_id.to_le_bytes())?;
        buf.write_all(&self.weight.to_le_bytes())?;
        buf.write_all(&self.created_at.to_le_bytes())?;
        write_optional_string(&mut buf, &self.metadata)?;

        Ok(buf)
    }

    pub fn deserialize(data: &[u8]) -> io::Result<Self> {
        let mut c = Cursor::new(data);

        let id_hash = read_u64(&mut c)?;
        let kind = KnowledgeEdgeKind::from_u8(read_u8(&mut c)?);
        let source_id = read_u64(&mut c)?;
        let target_id = read_u64(&mut c)?;
        let weight = read_f32(&mut c)?;
        let created_at = read_i64(&mut c)?;
        let metadata = read_optional_string(&mut c)?;

        Ok(KnowledgeEdge {
            id_hash, kind, source_id, target_id, weight, created_at, metadata,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_knowledge_slot_roundtrip() {
        let slot = KnowledgeSlot {
            id_hash: 1,
            title: "Pasta Cooking".to_string(),
            domain: "cooking".to_string(),
            knowledge_type: KnowledgeType::Procedural,
            text: "Boil pasta".to_string(),
            summary: Some("pasta instructions".to_string()),
            keywords: vec!["pasta".to_string()],
            edge_count: 1, edge_ptrs: [100, 0, 0, 0, 0, 0, 0, 0],
            archive_refs: vec![1001],
            source_ref: Some("/recipes/pasta.rs:L10-L25".to_string()),
            created_at: 1000, updated_at: 2000, version: 1,
            importance: 0.8, confidence: 0.9,
        };
        let data = slot.serialize().unwrap();
        assert_eq!(slot, KnowledgeSlot::deserialize(&data).unwrap());
    }

    #[test]
    fn test_knowledge_slot_minimal() {
        let slot = KnowledgeSlot {
            id_hash: 2,
            title: "Test".to_string(),
            domain: "test".to_string(),
            knowledge_type: KnowledgeType::Factual,
            text: "hello".to_string(), summary: None,
            keywords: vec![], edge_count: 0, edge_ptrs: [0; 8],
            archive_refs: vec![],
            source_ref: None,
            created_at: 0, updated_at: 0, version: 0,
            importance: 0.0, confidence: 0.0,
        };
        let data = slot.serialize().unwrap();
        assert_eq!(slot, KnowledgeSlot::deserialize(&data).unwrap());
    }

    #[test]
    fn test_knowledge_slot_size() {
        let slot = KnowledgeSlot {
            id_hash: 1,
            title: "title".to_string(),             // 5
            domain: "test".to_string(),               // 4
            knowledge_type: KnowledgeType::Factual,
            text: "hello".to_string(),                // 5
            summary: None,
            keywords: vec!["a".to_string()],           // 2+1
            edge_count: 0, edge_ptrs: [0; 8],
            archive_refs: vec![1],                      // 2+8
            source_ref: None,
            created_at: 0, updated_at: 0, version: 0,
            importance: 0.0, confidence: 0.0,
        };
        // 103 + (2+5) + (2+4) + (2+5) + 2 + (2+1) + (2+8) + 2 = 140
        assert_eq!(slot.slot_size(), 140);
    }

    #[test]
    fn test_knowledge_slot_with_source_ref() {
        let slot = KnowledgeSlot {
            id_hash: 3,
            title: "Code Module".to_string(),
            domain: "programming".to_string(),
            knowledge_type: KnowledgeType::Procedural,
            text: "Handler function".to_string(),
            summary: None,
            keywords: vec!["handler".to_string()],
            edge_count: 0, edge_ptrs: [0; 8],
            archive_refs: vec![],
            source_ref: Some("/src/handler.rs:L42-L80".to_string()),
            created_at: 0, updated_at: 0, version: 0,
            importance: 0.0, confidence: 0.0,
        };
        let data = slot.serialize().unwrap();
        assert_eq!(slot, KnowledgeSlot::deserialize(&data).unwrap());
    }

    #[test]
    fn test_knowledge_edge_roundtrip() {
        let edge = KnowledgeEdge {
            id_hash: 1, kind: KnowledgeEdgeKind::Causal,
            source_id: 10, target_id: 20, weight: 0.5,
            created_at: 1000, metadata: Some("test".to_string()),
        };
        let data = edge.serialize().unwrap();
        assert_eq!(edge, KnowledgeEdge::deserialize(&data).unwrap());
    }

    #[test]
    fn test_knowledge_edge_no_metadata() {
        let edge = KnowledgeEdge {
            id_hash: 2, kind: KnowledgeEdgeKind::Related,
            source_id: 0, target_id: 0, weight: 0.0,
            created_at: 0, metadata: None,
        };
        let data = edge.serialize().unwrap();
        assert_eq!(edge, KnowledgeEdge::deserialize(&data).unwrap());
    }

    #[test]
    fn test_knowledge_edge_size() {
        let edge = KnowledgeEdge {
            id_hash: 1, kind: KnowledgeEdgeKind::Related,
            source_id: 0, target_id: 0, weight: 0.0,
            created_at: 0, metadata: None,
        };
        // 37 + 2 = 39
        assert_eq!(edge.slot_size(), 39);
    }
}
