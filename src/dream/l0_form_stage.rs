//! L0 Profile Generation - Extract agent persona from topic keywords
//!
//! This stage analyzes the distribution of topic keywords to generate
//! an L0 Profile representing the agent's personality and expertise areas.

use crate::file::free_list::allocate_from_free_list;
use crate::file::header::FileHeader;
use crate::file::page::PageHeader;
use crate::index::btree::BTreeIndex;
use crate::index::sparse::SparseIndex;
use crate::slot::profile::ProfileSlot;
use crate::util::{get_current_timestamp, hash_id, PageType, PAGE_SIZE};
use crate::MemHopError;
use memmap2::MmapMut;
use std::collections::HashMap;

/// Generate L0 profile from topic keyword distribution
///
/// # Arguments
/// * `mmap` - Mutable memory-mapped file
/// * `header` - File header for page allocation
/// * `btree` - B-tree index for profile storage and topic lookup
/// * `sparse_index` - Sparse index for keyword frequency analysis
///
/// # Returns
/// Ok(()) on success
pub fn generate_profile(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    sparse_index: &SparseIndex,
) -> Result<(), MemHopError> {
    // 1. Get top keywords from sparse index
    let top_keywords_with_freq = sparse_index.top_terms(20);
    let top_keywords: Vec<String> = top_keywords_with_freq
        .iter()
        .map(|(term, _)| term.clone())
        .collect();

    // 2. Count total engrams
    let total_engrams = btree.len();

    // 3. Generate profile from keyword distribution
    let now_ms = get_current_timestamp();

    // Build preferences from top keywords
    let mut preferences = HashMap::new();
    preferences.insert("top_keywords".to_string(), top_keywords.join(","));
    preferences.insert("total_engrams".to_string(), total_engrams.to_string());

    let profile_id_hash = hash_id("profile");

    // 4. Check if profile already exists in B-tree
    let existing_profile = if let Some(page_ref) = btree.search(profile_id_hash) {
        let page_id = (page_ref >> 16) as u32;
        let offset = (page_id as usize) * PAGE_SIZE + 32;
        if offset < mmap.len() {
            ProfileSlot::deserialize(&mmap[offset..]).ok()
        } else {
            None
        }
    } else {
        None
    };

    let (page_id, profile_slot) = if let Some(mut existing) = existing_profile {
        // Profile exists — only update personality and preferences (preserve name/role/worldview and habit fields)
        let pid = (btree.search(profile_id_hash).unwrap() >> 16) as u32;
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
        (pid, existing)
    } else {
        // Profile doesn't exist — create new with defaults
        let pid = allocate_from_free_list(mmap, header)?;
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
        (pid, slot)
    };

    // 5. Serialize and write ProfileSlot to page
    let data = profile_slot
        .serialize()
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;

    let offset = (page_id as usize) * PAGE_SIZE;

    // Write page header (always write for new pages, update for existing)
    let page_header = PageHeader {
        page_id,
        page_type: PageType::Profile.to_u16(),
        slot_count: 1,
        free_bytes: (PAGE_SIZE - 32).saturating_sub(data.len()) as u16,
        layer_id: 0,
        next_page: 0xFFFFFFFF,
        prev_page: 0xFFFFFFFF,
        reserved: [0u8; 12],
    };
    let header_bytes = page_header.to_bytes();
    mmap[offset..offset + 32].copy_from_slice(&header_bytes);

    let data_offset = offset + 32;
    if data_offset + data.len() > mmap.len() {
        return Err(MemHopError::Serialization(format!(
            "ProfileSlot data too large for page: {} > {}",
            data.len(),
            mmap.len() - data_offset
        )));
    }
    mmap[data_offset..data_offset + data.len()].copy_from_slice(&data);

    // 6. Insert into B-tree so l0_crud.rs can find it
    let page_ref = (page_id as u64) << 16;
    btree.insert(profile_id_hash, page_ref);

    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;

    #[test]
    fn test_generate_profile_empty() {
        // Test returns Ok when btree is empty (no keywords to extract)
        // With empty free_list, allocate_from_free_list will fail, which is expected
        let temp_file = tempfile::NamedTempFile::new().unwrap();
        let path = temp_file.path();

        // Create a file large enough for 500 pages (like MemHop::open creates)
        let mut file = std::fs::File::create(path).unwrap();
        file.write_all(&vec![0u8; 4096 * 500]).unwrap();
        drop(file);

        let file = std::fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open(path)
            .unwrap();

        let mut mmap = unsafe { MmapMut::map_mut(&file).unwrap() };
        let mut header = crate::file::header::FileHeader::new(768);
        // Initialize free list with some pages
        crate::file::free_list::init_free_list(&mut header).unwrap();
        for page_id in (18..500).rev() {
            crate::file::free_list::free_page(&mut mmap, &mut header, page_id).unwrap();
        }
        header.page_count = 500;

        let mut btree = BTreeIndex::new();
        let sparse_index = SparseIndex::new();
        let result = generate_profile(&mut mmap, &mut header, &mut btree, &sparse_index);
        assert!(result.is_ok());
        // Verify profile was stored in B-tree
        assert!(btree.search(hash_id("profile")).is_some());
    }
}
