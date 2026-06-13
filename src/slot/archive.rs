// L4 ArchiveSlot - raw conversation storage
//
// Stores conversation text inline; media files store file paths.

use crate::util::io_helpers::*;
use std::io::{self, Cursor, Write};

/// Content type for archive entries
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[repr(u8)]
pub enum ContentType {
    Text = 0,
    Image = 1,
    Video = 2,
    Document = 3,
    Audio = 4,
    Code = 5,
    Other = 0xFF,
}

impl ContentType {
    pub fn from_u8(value: u8) -> Self {
        match value {
            0 => ContentType::Text,
            1 => ContentType::Image,
            2 => ContentType::Video,
            3 => ContentType::Document,
            4 => ContentType::Audio,
            5 => ContentType::Code,
            _ => ContentType::Other,
        }
    }
}

/// L4 Archive slot - stores raw text or file path references
#[derive(Debug, Clone, PartialEq)]
pub struct ArchiveSlot {
    pub id_hash: u64,
    pub content_type: ContentType,
    pub role: u8,           // 0=user, 1=agent, 2=system
    pub session_id: u64,
    pub topic_id: u64,
    pub created_at: i64,
    pub version: u32,
    pub content: String,    // inline text or file path
    pub metadata: Option<String>,
}

impl ArchiveSlot {
    /// Calculate total serialized size
    pub fn slot_size(&self) -> usize {
        // Fixed: 8 + 1 + 1 + 8 + 8 + 8 + 4 = 38 bytes
        const FIXED: usize = 38;
        let content_size = 2 + self.content.len();
        let metadata_size = match &self.metadata {
            Some(m) => 2 + m.len(),
            None => 2,
        };
        FIXED + content_size + metadata_size
    }

    /// Serialize to bytes
    pub fn serialize(&self) -> io::Result<Vec<u8>> {
        let mut buf = Vec::with_capacity(self.slot_size());

        // Fixed part
        buf.write_all(&self.id_hash.to_le_bytes())?;
        buf.write_all(&[self.content_type as u8, self.role])?;
        buf.write_all(&self.session_id.to_le_bytes())?;
        buf.write_all(&self.topic_id.to_le_bytes())?;
        buf.write_all(&self.created_at.to_le_bytes())?;
        buf.write_all(&self.version.to_le_bytes())?;

        // Variable part
        write_string(&mut buf, &self.content)?;
        write_optional_string(&mut buf, &self.metadata)?;

        Ok(buf)
    }

    /// Deserialize from bytes
    pub fn deserialize(data: &[u8]) -> io::Result<Self> {
        let mut c = Cursor::new(data);

        let id_hash = read_u64(&mut c)?;
        let content_type = ContentType::from_u8(read_u8(&mut c)?);
        let role = read_u8(&mut c)?;
        let session_id = read_u64(&mut c)?;
        let topic_id = read_u64(&mut c)?;
        let created_at = read_i64(&mut c)?;
        let version = read_u32(&mut c)?;
        let content = read_string(&mut c)?;
        let metadata = read_optional_string(&mut c)?;

        Ok(ArchiveSlot {
            id_hash, content_type, role, session_id, topic_id,
            created_at, version, content, metadata,
        })
    }

    /// Is this inline text (not a file path)?
    pub fn is_text(&self) -> bool {
        matches!(self.content_type, ContentType::Text | ContentType::Code)
    }

    /// Is this a file path reference?
    pub fn is_file_ref(&self) -> bool {
        !self.is_text()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_archive_text() {
        let slot = ArchiveSlot {
            id_hash: 1, content_type: ContentType::Text, role: 0,
            session_id: 10, topic_id: 20, created_at: 1000, version: 1,
            content: "hello".to_string(), metadata: None,
        };
        let data = slot.serialize().unwrap();
        assert_eq!(slot, ArchiveSlot::deserialize(&data).unwrap());
        assert!(slot.is_text());
    }

    #[test]
    fn test_archive_image_path() {
        let slot = ArchiveSlot {
            id_hash: 2, content_type: ContentType::Image, role: 0,
            session_id: 10, topic_id: 20, created_at: 1000, version: 1,
            content: "/img.png".to_string(),
            metadata: Some(r#"{"w":1920}"#.to_string()),
        };
        let data = slot.serialize().unwrap();
        assert_eq!(slot, ArchiveSlot::deserialize(&data).unwrap());
        assert!(slot.is_file_ref());
    }

    #[test]
    fn test_archive_slot_size() {
        let slot = ArchiveSlot {
            id_hash: 1, content_type: ContentType::Text, role: 0,
            session_id: 0, topic_id: 0, created_at: 0, version: 0,
            content: "test".to_string(), metadata: None,
        };
        // 38 + (2+4) + 2 = 46
        assert_eq!(slot.slot_size(), 46);
    }
}
