// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Stage: User Language Habit Distillation — extract habits from L4 archives, merge into L0 Profile.

use crate::dream::llm::{HabitAnalysis, LlmProvider};
use crate::file::header::FileHeader;
use crate::file::page::PageHeader;
use crate::index::btree::BTreeIndex;
use crate::layers::archive::ArchiveSlot;
use crate::layers::profile::ProfileSlot;
use crate::util::{hash_id, PageType, PAGE_SIZE};
use crate::MemHopError;
use memmap2::MmapMut;

/// Maximum number of recent archives to analyze
const MAX_DIALOGUES: usize = 30;

/// Maximum lexicon entries (enforce page size limit)
const MAX_LEXICON: usize = 30;

/// Maximum style traits
const MAX_STYLE_TRAITS: usize = 10;

/// Maximum emotion patterns
const MAX_EMOTION_PATTERNS: usize = 10;

/// Distill user language habits from recent dialogue history.
pub fn distill_user_habits(
    mmap: &mut MmapMut,
    header: &FileHeader,
    btree: &BTreeIndex,
    llm: &dyn LlmProvider,
) -> Result<HabitUpdate, MemHopError> {
    let dialogues = extract_recent_dialogues(mmap, header, btree);

    if dialogues.is_empty() {
        return Ok(HabitUpdate {
            new_lexicon: 0,
            new_style_traits: 0,
            new_emotion_patterns: 0,
            total_dialogues_analyzed: 0,
        });
    }

    let total_analyzed = dialogues.len();

    let analysis = match llm.analyze_user_habits(&dialogues) {
        Ok(result) => result,
        Err(_) => llm.fallback_analyze_user_habits(&dialogues),
    };

    let merge_result = merge_habits_into_profile(mmap, btree, &analysis)?;

    Ok(HabitUpdate {
        new_lexicon: merge_result.0,
        new_style_traits: merge_result.1,
        new_emotion_patterns: merge_result.2,
        total_dialogues_analyzed: total_analyzed,
    })
}

/// Extract recent dialogue texts from L4 Archive slots
fn extract_recent_dialogues(
    mmap: &MmapMut,
    header: &FileHeader,
    btree: &BTreeIndex,
) -> Vec<String> {
    let data = &mmap[..];
    let page_count = header.page_count;
    let mut archives: Vec<(i64, String)> = Vec::new();

    for (_, page_ref) in btree.iter_unsorted() {
        let page_id = (*page_ref >> 16) as u32;
        if page_id >= page_count {
            continue;
        }

        let page_offset = (page_id as usize) * PAGE_SIZE;
        if page_offset + PAGE_SIZE > data.len() {
            continue;
        }

        if page_offset + 32 > data.len() {
            continue;
        }
        let mut hdr_bytes = [0u8; 32];
        hdr_bytes.copy_from_slice(&data[page_offset..page_offset + 32]);
        if let Ok(page_hdr) = PageHeader::from_bytes(&hdr_bytes) {
            if page_hdr.page_type != PageType::Archive as u16 {
                continue;
            }
        } else {
            continue;
        }

        if let Some(slot_data) = crate::shared::slot_io::get_slot_data(data, *page_ref) {
            if let Ok(archive) = ArchiveSlot::deserialize(slot_data) {
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
fn merge_habits_into_profile(
    mmap: &mut MmapMut,
    btree: &BTreeIndex,
    analysis: &HabitAnalysis,
) -> Result<(usize, usize, usize), MemHopError> {
    let profile_id_hash = hash_id("profile");

    let page_ref = btree
        .search(profile_id_hash)
        .ok_or(MemHopError::PageNotFound(0))?;

    let page_id = (page_ref >> 16) as u32;
    let offset = (page_id as usize) * PAGE_SIZE + 32;

    if offset >= mmap.len() {
        return Err(MemHopError::PageNotFound(page_id));
    }

    let mut profile = ProfileSlot::deserialize(&mmap[offset..])
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;

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

    let data = profile
        .serialize()
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;

    if offset + data.len() > mmap.len() {
        return Err(MemHopError::Serialization(format!(
            "ProfileSlot with habits too large for page: {} > {}",
            data.len(),
            mmap.len() - offset
        )));
    }
    mmap[offset..offset + data.len()].copy_from_slice(&data);

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

    #[test]
    fn test_extract_recent_dialogues_empty() {
        let btree = BTreeIndex::new();
        let header = crate::file::header::FileHeader::new(768);

        let temp_file = tempfile::NamedTempFile::new().unwrap();
        let path = temp_file.path();
        std::fs::write(path, vec![0u8; 4096 * 10]).unwrap();
        let file = std::fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open(path)
            .unwrap();
        let mmap = unsafe { MmapMut::map_mut(&file).unwrap() };

        let dialogues = extract_recent_dialogues(&mmap, &header, &btree);
        assert!(dialogues.is_empty());
    }
}
