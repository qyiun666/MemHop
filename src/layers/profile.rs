// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L0 ProfileSlot — agent identity (JSON format).
// Behavioral skills are NOT stored here; MemHop is a memory database.

use crate::api::MemHopError;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;

/// L0 Agent profile. Extended fields for user language habits:
/// `lexicon`, `style_traits`, `emotion_patterns`.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
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

impl ProfileSlot {
    pub fn serialize(&self) -> Result<Vec<u8>, MemHopError> {
        bincode::serialize(self).map_err(|e| MemHopError::Serialization(e.to_string()))
    }

    pub fn deserialize(data: &[u8]) -> Result<Self, MemHopError> {
        bincode::deserialize(data).map_err(|e| MemHopError::Deserialization(e.to_string()))
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
            id_hash: 1,
            name: "Meow".into(),
            role: "assistant".into(),
            personality: "friendly, helpful, curious".into(),
            worldview: "knowledge should be accessible".into(),
            preferences,
            lexicon,
            style_traits: vec!["prefers_brevity".into()],
            emotion_patterns,
            created_at: 1000,
            updated_at: 2000,
            version: 1,
        };
        let data = profile.serialize().unwrap();
        assert_eq!(profile, ProfileSlot::deserialize(&data).unwrap());
    }
}
