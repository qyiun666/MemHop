// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Stage: L0 Profile Generation — extract agent persona from topic keyword distribution.

use crate::index::sparse::SparseIndex;
use crate::layers::profile::ProfileSlot;
use crate::storage::record::REC_L0_PROFILE;
use crate::storage::StorageEngine;
use crate::util::{get_current_timestamp, hash_id};
use crate::MemHopError;
use std::collections::HashMap;

/// Generate L0 profile from topic keyword distribution.
pub fn generate_profile(
    engine: &mut StorageEngine,
    sparse_index: &SparseIndex,
) -> Result<(), MemHopError> {
    let top_keywords_with_freq = sparse_index.top_terms(20);
    let top_keywords: Vec<String> = top_keywords_with_freq
        .iter()
        .map(|(term, _)| term.clone())
        .collect();

    let total_engrams = engine.record_count() as usize;

    let now_ms = get_current_timestamp();

    let mut preferences = HashMap::new();
    preferences.insert("top_keywords".to_string(), top_keywords.join(","));
    preferences.insert("total_engrams".to_string(), total_engrams.to_string());

    let profile_id_hash = hash_id("profile");

    let existing_profile: Option<ProfileSlot> = match engine.read_record(profile_id_hash)? {
        Some((_rt, data)) => bincode::deserialize(data).ok(),
        None => None,
    };

    if let Some(mut existing) = existing_profile {
        // Only update personality and preferences; preserve name/role/worldview and habit fields
        existing.personality = top_keywords
            .iter()
            .take(5)
            .cloned()
            .collect::<Vec<_>>()
            .join(", ");
        existing.preferences = preferences;
        existing.updated_at = now_ms;
        existing.version += 1;
        // NOTE: lexicon, style_traits, emotion_patterns are preserved (updated by habit_distill_stage)

        let data =
            bincode::serialize(&existing).map_err(|e| MemHopError::Serialization(e.to_string()))?;
        engine.write_record(REC_L0_PROFILE, profile_id_hash, &data)?;
    } else {
        let slot = ProfileSlot {
            id_hash: profile_id_hash,
            name: "Agent".to_string(),
            role: "assistant".to_string(),
            personality: top_keywords
                .iter()
                .take(5)
                .cloned()
                .collect::<Vec<_>>()
                .join(", "),
            worldview: String::new(),
            preferences,
            lexicon: HashMap::new(),
            style_traits: Vec::new(),
            emotion_patterns: HashMap::new(),
            created_at: now_ms,
            updated_at: now_ms,
            version: 1,
        };

        let data =
            bincode::serialize(&slot).map_err(|e| MemHopError::Serialization(e.to_string()))?;
        engine.write_record(REC_L0_PROFILE, profile_id_hash, &data)?;
    }

    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::NamedTempFile;

    fn create_engine() -> StorageEngine {
        let temp = NamedTempFile::new().unwrap();
        StorageEngine::create(temp.path(), 768).unwrap()
    }

    #[test]
    fn test_generate_profile_empty() {
        let mut engine = create_engine();
        let sparse_index = SparseIndex::new();
        let result = generate_profile(&mut engine, &sparse_index);
        assert!(result.is_ok());
        assert!(engine.contains(hash_id("profile")));
    }
}
