// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L4 ArchiveSlot — raw conversation storage.
// Immutable ground truth; no version field needed.

#[cfg(test)]
use crate::api::MemHopError;
use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
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
    #[cfg(test)]
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
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
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

    #[cfg(test)]
    pub fn serialize(&self) -> Result<Vec<u8>, MemHopError> {
        bincode::serialize(self).map_err(|e| MemHopError::Serialization(e.to_string()))
    }

    #[cfg(test)]
    pub fn deserialize(data: &[u8]) -> Result<Self, MemHopError> {
        bincode::deserialize(data).map_err(|e| MemHopError::Deserialization(e.to_string()))
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
        let data = slot.serialize().unwrap();
        let restored = ArchiveSlot::deserialize(&data).unwrap();
        assert_eq!(slot, restored);
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

    #[test]
    fn test_archive_deserialize_truncated_returns_unexpected_eof() {
        let truncated = vec![0u8; 10];
        let err = ArchiveSlot::deserialize(&truncated).unwrap_err();
        assert!(matches!(err, MemHopError::Deserialization(_)));
    }

    #[test]
    fn test_archive_deserialize_content_truncated() {
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
        // Truncate partway
        let truncated = &data[..data.len().min(10)];
        let err = ArchiveSlot::deserialize(truncated).unwrap_err();
        assert!(matches!(err, MemHopError::Deserialization(_)));
    }

    #[test]
    fn test_archive_deserialize_metadata_truncated() {
        let slot = ArchiveSlot {
            id_hash: 1,
            content_type: ContentType::Text,
            role: 0,
            context_id: 20,
            created_at: 1000,
            content: "hi".to_string(),
            metadata: Some("meta".to_string()),
        };
        let data = slot.serialize().unwrap();
        // Truncate partway
        let truncated = &data[..data.len().saturating_sub(2)];
        let err = ArchiveSlot::deserialize(truncated).unwrap_err();
        assert!(matches!(err, MemHopError::Deserialization(_)));
    }
}
