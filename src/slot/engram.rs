// Engram slot serialization
use std::io::{self, Cursor, Read, Write};

/// Engram slot structure (design doc section 3.3)
#[derive(Debug, Clone, PartialEq)]
pub struct EngramSlot {
    pub id_hash: u64,
    pub text: String,
    pub summary: Option<String>,
    pub keywords: Vec<String>,
    pub created_at: i64,
    pub updated_at: i64,
    pub version: u32,
    pub edge_count: u16,
    pub doc_len: u16,
    pub vector_page_ref: u64,
    pub is_structural: bool,
    pub source_type: u8,
    pub memory_state: u8, // v0.31+ reserved, default 0
    pub emotion_type: u8, // v0.31+ reserved, default 0
    pub valence: f32,     // v0.31+ reserved, default 0.0
    pub arousal: f32,     // v0.31+ reserved, default 0.0
    pub importance: f32,  // v0.31+ reserved, default 0.0
    pub edge_ptrs: [u64; 8],
}

impl EngramSlot {
    /// Calculate the total serialized size in bytes
    pub fn slot_size(&self) -> usize {
        // Fixed part: 8 + 2 + 2 + 2 + 2 + 8 + 8 + 4 + 2 + 2 + 8 + 1 + 1 + 1 + 1 + 4 + 4 + 4 + 64 = 128 bytes
        let fixed_size = 128;

        // Variable part: text + summary + keywords
        let text_size = self.text.len();
        let summary_size = match &self.summary {
            Some(s) => s.len(),
            None => 0,
        };

        // Keywords: each keyword has u16 length prefix + content
        let keywords_size: usize = self.keywords.iter().map(|k| 2 + k.len()).sum();

        fixed_size + text_size + summary_size + keywords_size
    }

    /// Serialize the EngramSlot to bytes
    pub fn serialize(&self) -> io::Result<Vec<u8>> {
        let mut buffer = Vec::with_capacity(self.slot_size());

        // Fixed part
        buffer.write_all(&self.id_hash.to_le_bytes())?;

        // Text length and content
        let text_len = self.text.len() as u16;
        buffer.write_all(&text_len.to_le_bytes())?;

        // Summary length and content
        let summary_len = match &self.summary {
            Some(s) => s.len() as u16,
            None => 0,
        };
        buffer.write_all(&summary_len.to_le_bytes())?;

        // Keywords count and total length
        let keywords_count = self.keywords.len() as u16;
        let keywords_total_len: u16 = self.keywords.iter().map(|k| k.len() as u16).sum();
        buffer.write_all(&keywords_count.to_le_bytes())?;
        buffer.write_all(&keywords_total_len.to_le_bytes())?;

        // Timestamps and metadata
        buffer.write_all(&self.created_at.to_le_bytes())?;
        buffer.write_all(&self.updated_at.to_le_bytes())?;
        buffer.write_all(&self.version.to_le_bytes())?;
        buffer.write_all(&self.edge_count.to_le_bytes())?;
        buffer.write_all(&self.doc_len.to_le_bytes())?;
        buffer.write_all(&self.vector_page_ref.to_le_bytes())?;

        // Flags and types
        buffer.write_all(&[if self.is_structural { 1 } else { 0 }])?;
        buffer.write_all(&[self.source_type])?;
        buffer.write_all(&[self.memory_state])?;
        buffer.write_all(&[self.emotion_type])?;

        // Float fields (v0.31+ reserved)
        buffer.write_all(&self.valence.to_le_bytes())?;
        buffer.write_all(&self.arousal.to_le_bytes())?;
        buffer.write_all(&self.importance.to_le_bytes())?;

        // Edge pointers
        for ptr in &self.edge_ptrs {
            buffer.write_all(&ptr.to_le_bytes())?;
        }

        // Variable part: text
        buffer.write_all(self.text.as_bytes())?;

        // Variable part: summary
        if let Some(ref summary) = self.summary {
            buffer.write_all(summary.as_bytes())?;
        }

        // Variable part: keywords (each with u16 length prefix)
        for keyword in &self.keywords {
            let kw_len = keyword.len() as u16;
            buffer.write_all(&kw_len.to_le_bytes())?;
            buffer.write_all(keyword.as_bytes())?;
        }

        Ok(buffer)
    }

    /// Deserialize an EngramSlot from bytes
    pub fn deserialize(data: &[u8]) -> io::Result<Self> {
        let mut cursor = Cursor::new(data);

        // Read fixed part
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
        let text_len = read_u16(&mut cursor)?;
        let summary_len = read_u16(&mut cursor)?;
        let keywords_count = read_u16(&mut cursor)?;
        let _keywords_total_len = read_u16(&mut cursor)?; // Reserved for validation

        let created_at = read_i64(&mut cursor)?;
        let updated_at = read_i64(&mut cursor)?;
        let version = read_u32(&mut cursor)?;
        let edge_count = read_u16(&mut cursor)?;
        let doc_len = read_u16(&mut cursor)?;
        let vector_page_ref = read_u64(&mut cursor)?;

        let is_structural = read_u8(&mut cursor)? != 0;
        let source_type = read_u8(&mut cursor)?;
        let memory_state = read_u8(&mut cursor)?;
        let emotion_type = read_u8(&mut cursor)?;

        let valence = read_f32(&mut cursor)?;
        let arousal = read_f32(&mut cursor)?;
        let importance = read_f32(&mut cursor)?;

        let mut edge_ptrs = [0u64; 8];
        for ptr in &mut edge_ptrs {
            *ptr = read_u64(&mut cursor)?;
        }

        // Read variable part: text
        let mut text_buf = vec![0u8; text_len as usize];
        cursor.read_exact(&mut text_buf)?;
        let text = String::from_utf8(text_buf)
            .map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?;

        // Read variable part: summary
        let summary = if summary_len > 0 {
            let mut summary_buf = vec![0u8; summary_len as usize];
            cursor.read_exact(&mut summary_buf)?;
            let summary_str = String::from_utf8(summary_buf)
                .map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?;
            Some(summary_str)
        } else {
            None
        };

        // Read variable part: keywords
        let mut keywords = Vec::with_capacity(keywords_count as usize);
        for _ in 0..keywords_count {
            let kw_len = read_u16(&mut cursor)?;
            let mut kw_buf = vec![0u8; kw_len as usize];
            cursor.read_exact(&mut kw_buf)?;
            let keyword = String::from_utf8(kw_buf)
                .map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?;
            keywords.push(keyword);
        }

        Ok(EngramSlot {
            id_hash,
            text,
            summary,
            keywords,
            created_at,
            updated_at,
            version,
            edge_count,
            doc_len,
            vector_page_ref,
            is_structural,
            source_type,
            memory_state,
            emotion_type,
            valence,
            arousal,
            importance,
            edge_ptrs,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_engram_serialize_deserialize_basic() {
        let slot = EngramSlot {
            id_hash: 1234567890,
            text: "This is a test engram".to_string(),
            summary: Some("Test summary".to_string()),
            keywords: vec!["test".to_string(), "engram".to_string()],
            created_at: 1234567890,
            updated_at: 1234567891,
            version: 1,
            edge_count: 2,
            doc_len: 100,
            vector_page_ref: 999,
            is_structural: false,
            source_type: 0,
            memory_state: 0,
            emotion_type: 0,
            valence: 0.0,
            arousal: 0.0,
            importance: 0.0,
            edge_ptrs: [1, 2, 3, 4, 5, 6, 7, 8],
        };

        let serialized = slot.serialize().unwrap();
        let deserialized = EngramSlot::deserialize(&serialized).unwrap();

        assert_eq!(slot, deserialized);
    }

    #[test]
    fn test_engram_serialize_deserialize_no_summary() {
        let slot = EngramSlot {
            id_hash: 9876543210,
            text: "No summary here".to_string(),
            summary: None,
            keywords: vec![],
            created_at: 1000000000,
            updated_at: 1000000001,
            version: 2,
            edge_count: 0,
            doc_len: 50,
            vector_page_ref: 0,
            is_structural: true,
            source_type: 1,
            memory_state: 0,
            emotion_type: 0,
            valence: 0.5,
            arousal: 0.3,
            importance: 0.8,
            edge_ptrs: [0; 8],
        };

        let serialized = slot.serialize().unwrap();
        let deserialized = EngramSlot::deserialize(&serialized).unwrap();

        assert_eq!(slot, deserialized);
    }

    #[test]
    fn test_engram_slot_size() {
        let slot = EngramSlot {
            id_hash: 123,
            text: "Hello".to_string(),                         // 5 bytes
            summary: Some("Hi".to_string()),                   // 2 bytes
            keywords: vec!["a".to_string(), "bc".to_string()], // (2+1) + (2+2) = 7 bytes
            created_at: 0,
            updated_at: 0,
            version: 0,
            edge_count: 0,
            doc_len: 0,
            vector_page_ref: 0,
            is_structural: false,
            source_type: 0,
            memory_state: 0,
            emotion_type: 0,
            valence: 0.0,
            arousal: 0.0,
            importance: 0.0,
            edge_ptrs: [0; 8],
        };

        // Fixed: 128, Text: 5, Summary: 2, Keywords: 7 = 142
        assert_eq!(slot.slot_size(), 142);
    }

    #[test]
    fn test_engram_unicode_text() {
        let slot = EngramSlot {
            id_hash: 456,
            text: "你好世界 🦀 Rust".to_string(),
            summary: Some("测试摘要".to_string()),
            keywords: vec!["关键词".to_string(), "测试".to_string()],
            created_at: 1234567890,
            updated_at: 1234567891,
            version: 1,
            edge_count: 0,
            doc_len: 0,
            vector_page_ref: 0,
            is_structural: false,
            source_type: 0,
            memory_state: 0,
            emotion_type: 0,
            valence: 0.0,
            arousal: 0.0,
            importance: 0.0,
            edge_ptrs: [0; 8],
        };

        let serialized = slot.serialize().unwrap();
        let deserialized = EngramSlot::deserialize(&serialized).unwrap();

        assert_eq!(slot, deserialized);
    }

    #[test]
    fn test_engram_edge_cases() {
        // Test with empty text
        let slot = EngramSlot {
            id_hash: 789,
            text: "".to_string(),
            summary: None,
            keywords: vec![],
            created_at: 0,
            updated_at: 0,
            version: 0,
            edge_count: 0,
            doc_len: 0,
            vector_page_ref: 0,
            is_structural: false,
            source_type: 0,
            memory_state: 0,
            emotion_type: 0,
            valence: 0.0,
            arousal: 0.0,
            importance: 0.0,
            edge_ptrs: [0; 8],
        };

        let serialized = slot.serialize().unwrap();
        let deserialized = EngramSlot::deserialize(&serialized).unwrap();

        assert_eq!(slot, deserialized);
    }
}
