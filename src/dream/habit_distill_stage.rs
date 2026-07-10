// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Stage: User Language Habit Distillation — extract habits from L4 archives, merge into L0 Profile.

use crate::dream::llm::HabitAnalysis;
use crate::layers::archive::ArchiveSlot;
use crate::layers::profile::ProfileSlot;
use crate::storage::record::{REC_L0_PROFILE, REC_L4_ARCHIVE};
use crate::storage::StorageEngine;
use crate::util::hash_id;
use crate::MemHopError;

/// Maximum number of recent archives to analyze
const MAX_DIALOGUES: usize = 30;

/// Maximum lexicon entries (enforce page size limit)
const MAX_LEXICON: usize = 30;

/// Maximum style traits
const MAX_STYLE_TRAITS: usize = 10;

/// Maximum emotion patterns
const MAX_EMOTION_PATTERNS: usize = 10;

/// Extract recent dialogue texts from L4 Archive slots
pub fn extract_recent_dialogues_inner(engine: &StorageEngine, max_count: usize) -> Vec<String> {
    let mut dialogues = extract_recent_dialogues(engine);
    dialogues.truncate(max_count);
    dialogues
}

/// Extract recent dialogue texts from L4 Archive slots (internal).
fn extract_recent_dialogues(engine: &StorageEngine) -> Vec<String> {
    let mut archives: Vec<(i64, String)> = Vec::new();

    for (&id_hash, _) in engine.iter_index() {
        if let Ok(Some((record_type, data))) = engine.read_record(id_hash) {
            if record_type != REC_L4_ARCHIVE {
                continue;
            }
            if let Ok(archive) = bincode::deserialize::<ArchiveSlot>(data) {
                // Only include user messages (role=0) with non-empty content
                if archive.role == 0 && !archive.content.is_empty() {
                    archives.push((archive.created_at, archive.content));
                }
            }
        }
    }

    archives.sort_by_key(|(ts, _)| std::cmp::Reverse(*ts));

    archives
        .into_iter()
        .take(MAX_DIALOGUES)
        .map(|(_, content)| content)
        .collect()
}

/// Merge habit analysis results into the existing L0 Profile.
/// Returns (new_lexicon_count, new_style_count, new_emotion_count).
pub fn merge_habits_into_profile(
    engine: &mut StorageEngine,
    analysis: &HabitAnalysis,
) -> Result<(usize, usize, usize), MemHopError> {
    let profile_id_hash = hash_id("profile");

    let (_, data) = engine
        .read_record(profile_id_hash)?
        .ok_or(MemHopError::PageNotFound(0))?;

    let mut profile: ProfileSlot =
        bincode::deserialize(data).map_err(|e| MemHopError::Serialization(e.to_string()))?;

    let mut new_lexicon = 0;
    let mut new_style = 0;
    let mut new_emotion = 0;

    // New entries override old; old entries preserved if not in new
    for (word, meaning) in &analysis.lexicon {
        if !profile.lexicon.contains_key(word) {
            new_lexicon += 1;
        }
        profile.lexicon.insert(word.clone(), meaning.clone());
    }
    if profile.lexicon.len() > MAX_LEXICON {
        let excess: Vec<String> = profile.lexicon.keys().skip(MAX_LEXICON).cloned().collect();
        for k in excess {
            profile.lexicon.remove(&k);
        }
    }

    for trait_tag in &analysis.style_traits {
        if !profile.style_traits.contains(trait_tag) {
            profile.style_traits.push(trait_tag.clone());
            new_style += 1;
        }
    }
    profile.style_traits.truncate(MAX_STYLE_TRAITS);

    for (expr, meaning) in &analysis.emotion_patterns {
        if !profile.emotion_patterns.contains_key(expr) {
            new_emotion += 1;
        }
        profile
            .emotion_patterns
            .insert(expr.clone(), meaning.clone());
    }
    if profile.emotion_patterns.len() > MAX_EMOTION_PATTERNS {
        let excess: Vec<String> = profile
            .emotion_patterns
            .keys()
            .skip(MAX_EMOTION_PATTERNS)
            .cloned()
            .collect();
        for k in excess {
            profile.emotion_patterns.remove(&k);
        }
    }

    profile.updated_at = crate::shared::common::now_ms();
    profile.version += 1;

    let data =
        bincode::serialize(&profile).map_err(|e| MemHopError::Serialization(e.to_string()))?;

    engine.write_record(REC_L0_PROFILE, profile_id_hash, &data)?;

    Ok((new_lexicon, new_style, new_emotion))
}

/// Result of habit distillation
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct HabitUpdate {
    /// Number of new lexicon entries added
    pub new_lexicon: usize,
    /// Number of new style traits added
    pub new_style_traits: usize,
    /// Number of new emotion patterns added
    pub new_emotion_patterns: usize,
    /// Total dialogues analyzed
    pub total_dialogues_analyzed: usize,
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
    fn test_extract_recent_dialogues_empty() {
        let engine = create_engine();
        let dialogues = extract_recent_dialogues(&engine);
        assert!(dialogues.is_empty());
    }
}
