// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L4 ArchiveSlot — raw conversation storage.
// Immutable ground truth; no version field needed.

use crate::util::io_helpers::*;
use std::io::{self, Cursor, Write};

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

    /// Returns the lowercase string representation used by the API.
    pub fn as_str(&self) -> &'static str {
        match self {
            ContentType::Text => "text",
            ContentType::Image => "image",
            ContentType::Video => "video",
            ContentType::Document => "document",
            ContentType::Audio => "audio",
            ContentType::Code => "code",
            ContentType::Other => "other",
        }
    }
}

/// Text content stored inline; non-text media store file paths.
#[derive(Debug, Clone, PartialEq)]
pub struct ArchiveSlot {
    pub id_hash: u64,
    pub content_type: ContentType,
    pub role: u8, // 0=user, 1=agent, 2=system
    pub context_id: u64,
    pub created_at: i64,
    pub content: String, // Inline text or file path
    pub metadata: Option<String>,
}

impl ArchiveSlot {
    pub fn request_source(&self) -> crate::query::types::RequestSource {
        self.metadata
            .as_deref()
            .map(crate::query::types::RequestSource::from_metadata_json)
            .unwrap_or_default()
    }

    /// Fixed 26 bytes + `content.len()` + metadata variable.
    pub fn slot_size(&self) -> usize {
        const FIXED: usize = 26;
        let content_size = 2 + self.content.len();
        let metadata_size = match &self.metadata {
            Some(m) => 2 + m.len(),
            None => 2,
        };
        FIXED + content_size + metadata_size
    }

    pub fn serialize(&self) -> io::Result<Vec<u8>> {
        let mut buf = Vec::with_capacity(self.slot_size());
        buf.write_all(&self.id_hash.to_le_bytes())?;
        buf.write_all(&[self.content_type as u8, self.role])?;
        buf.write_all(&self.context_id.to_le_bytes())?;
        buf.write_all(&self.created_at.to_le_bytes())?;
        write_string(&mut buf, &self.content)?;
        write_optional_string(&mut buf, &self.metadata)?;
        Ok(buf)
    }

    pub fn deserialize(data: &[u8]) -> io::Result<Self> {
        let mut c = Cursor::new(data);
        let id_hash = read_u64(&mut c)?;
        let content_type = ContentType::from_u8(read_u8(&mut c)?);
        let role = read_u8(&mut c)?;
        let context_id = read_u64(&mut c)?;
        let created_at = read_i64(&mut c)?;
        let content = read_string(&mut c)?;
        let metadata = read_optional_string(&mut c)?;
        Ok(ArchiveSlot {
            id_hash,
            content_type,
            role,
            context_id,
            created_at,
            content,
            metadata,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_archive_text() {
        let slot = ArchiveSlot {
            id_hash: 1,
            content_type: ContentType::Text,
            role: 0,
            context_id: 20,
            created_at: 1000,
            content: "hello".to_string(),
            metadata: None,
        };
        let data = slot.serialize().unwrap();
        assert_eq!(data.len(), slot.slot_size());
        assert_eq!(slot, ArchiveSlot::deserialize(&data).unwrap());
        assert!(matches!(
            slot.content_type,
            ContentType::Text | ContentType::Code
        ));
    }

    #[test]
    fn test_archive_code() {
        let slot = ArchiveSlot {
            id_hash: 2,
            content_type: ContentType::Code,
            role: 1,
            context_id: 30,
            created_at: 2000,
            content: "fn main() {}".to_string(),
            metadata: Some(r#"{"lang":"rust"}"#.to_string()),
        };
        let data = slot.serialize().unwrap();
        assert_eq!(slot, ArchiveSlot::deserialize(&data).unwrap());
        assert!(matches!(
            slot.content_type,
            ContentType::Text | ContentType::Code
        ));
    }

    #[test]
    fn test_archive_image_path() {
        let slot = ArchiveSlot {
            id_hash: 3,
            content_type: ContentType::Image,
            role: 0,
            context_id: 20,
            created_at: 1000,
            content: "/img/screenshot.png".to_string(),
            metadata: Some(r#"{"w":1920,"h":1080}"#.to_string()),
        };
        let data = slot.serialize().unwrap();
        assert_eq!(slot, ArchiveSlot::deserialize(&data).unwrap());
        assert!(matches!(
            slot.content_type,
            ContentType::Image | ContentType::Document | ContentType::Audio
        ));
    }

    #[test]
    fn test_archive_slot_size() {
        let slot = ArchiveSlot {
            id_hash: 1,
            content_type: ContentType::Text,
            role: 0,
            context_id: 0,
            created_at: 0,
            content: "test".to_string(),
            metadata: None,
        };
        // 26 + (2+4) + 2 = 34
        assert_eq!(slot.slot_size(), 34);
    }

    #[test]
    fn test_archive_no_version_field() {
        // Verify the struct has no `version` field
        let slot = ArchiveSlot {
            id_hash: 1,
            content_type: ContentType::Text,
            role: 0,
            context_id: 0,
            created_at: 0,
            content: "".to_string(),
            metadata: None,
        };
        let _ = slot;
    }
}
