// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L0 ProfileSlot — agent identity (JSON format).
// Behavioral skills are NOT stored here; MemHop is a memory database.

use std::collections::HashMap;
use std::io;

/// L0 Agent profile. Extended fields for user language habits:
/// `lexicon`, `style_traits`, `emotion_patterns`.
#[derive(Debug, Clone, PartialEq)]
pub struct ProfileSlot {
    pub id_hash: u64,
    pub name: String,
    pub role: String,
    pub personality: String,
    pub worldview: String,
    pub preferences: HashMap<String, String>,
    /// User vocabulary: unique word → meaning mapping
    pub lexicon: HashMap<String, String>,
    /// Communication style trait tags
    pub style_traits: Vec<String>,
    /// Emotional expression patterns: expression → true meaning
    pub emotion_patterns: HashMap<String, String>,
    pub created_at: i64,
    pub updated_at: i64,
    pub version: u32,
}

#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
struct ProfileJson {
    id_hash: u64,
    name: String,
    role: String,
    personality: String,
    worldview: String,
    preferences: HashMap<String, String>,
    #[serde(default)]
    lexicon: HashMap<String, String>,
    #[serde(default)]
    style_traits: Vec<String>,
    #[serde(default)]
    emotion_patterns: HashMap<String, String>,
    created_at: i64,
    updated_at: i64,
    version: u32,
}

impl ProfileSlot {
    pub fn serialize(&self) -> io::Result<Vec<u8>> {
        let json = ProfileJson {
            id_hash: self.id_hash,
            name: self.name.clone(), role: self.role.clone(),
            personality: self.personality.clone(), worldview: self.worldview.clone(),
            preferences: self.preferences.clone(), lexicon: self.lexicon.clone(),
            style_traits: self.style_traits.clone(), emotion_patterns: self.emotion_patterns.clone(),
            created_at: self.created_at, updated_at: self.updated_at, version: self.version,
        };
        serde_json::to_vec(&json).map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))
    }

    pub fn deserialize(data: &[u8]) -> io::Result<Self> {
        // Find JSON end by brace matching — handles trailing zeros/garbage.
        let mut brace_count = 0i32;
        let mut json_end = data.len();
        for (i, &byte) in data.iter().enumerate() {
            if byte == b'{' {
                brace_count += 1;
            } else if byte == b'}' {
                brace_count -= 1;
                if brace_count == 0 { json_end = i + 1; break; }
            } else if byte == 0 && brace_count == 0 {
                return Err(io::Error::new(io::ErrorKind::InvalidData, "Invalid JSON data"));
            }
        }
        let json: ProfileJson = serde_json::from_slice(&data[..json_end])
            .map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?;
        Ok(ProfileSlot {
            id_hash: json.id_hash, name: json.name, role: json.role,
            personality: json.personality, worldview: json.worldview,
            preferences: json.preferences, lexicon: json.lexicon,
            style_traits: json.style_traits, emotion_patterns: json.emotion_patterns,
            created_at: json.created_at, updated_at: json.updated_at, version: json.version,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_profile_roundtrip() {
        let mut preferences = HashMap::new();
        preferences.insert("language".into(), "Rust".into());
        preferences.insert("style".into(), "concise".into());
        let mut lexicon = HashMap::new();
        lexicon.insert("6".into(), "厉害/牛".into());
        let mut emotion_patterns = HashMap::new();
        emotion_patterns.insert("呵呵".into(), "不满或敷衍".into());
        let profile = ProfileSlot {
            id_hash: 1, name: "Meow".into(), role: "assistant".into(),
            personality: "friendly, helpful, curious".into(),
            worldview: "knowledge should be accessible".into(),
            preferences, lexicon, style_traits: vec!["prefers_brevity".into()],
            emotion_patterns, created_at: 1000, updated_at: 2000, version: 1,
        };
        let data = profile.serialize().unwrap();
        assert_eq!(profile, ProfileSlot::deserialize(&data).unwrap());
    }

    #[test]
    fn test_profile_json_readable() {
        let profile = ProfileSlot {
            id_hash: 1, name: "Test".into(), role: "agent".into(),
            personality: "calm".into(), worldview: "neutral".into(),
            preferences: HashMap::new(), lexicon: HashMap::new(),
            style_traits: Vec::new(), emotion_patterns: HashMap::new(),
            created_at: 0, updated_at: 0, version: 0,
        };
        let json_str = String::from_utf8(profile.serialize().unwrap()).unwrap();
        assert!(json_str.contains("\"name\":\"Test\""));
        assert!(!json_str.contains("\"values\""));
    }

    #[test]
    fn test_profile_backward_compat() {
        // Old JSON without lexicon/style_traits/emotion_patterns should still deserialize
        let old_json = r#"{"id_hash":1,"name":"Old","role":"bot","personality":"calm","worldview":"","preferences":{},"created_at":0,"updated_at":0,"version":1}"#;
        let profile = ProfileSlot::deserialize(old_json.as_bytes()).unwrap();
        assert_eq!(profile.name, "Old");
        assert!(profile.lexicon.is_empty());
        assert!(profile.style_traits.is_empty());
        assert!(profile.emotion_patterns.is_empty());
    }
}
