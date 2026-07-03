// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L0 Profile CRUD: unified read/write for the Profile layer.

use crate::index::btree::BTreeIndex;
use crate::layers::profile::ProfileSlot;
use crate::query::types::ProfileResult;
use crate::util::hash::hash_id;
use crate::util::PAGE_SIZE;
use crate::MemHopError;
use memmap2::MmapMut;
use std::result::Result;

/// Read L0 profile from memory-mapped file.
pub fn read_profile(
    mmap: &MmapMut,
    btree: &BTreeIndex,
) -> Result<Option<ProfileResult>, MemHopError> {
    let data = &mmap[..];
    let profile_id_hash = hash_id("profile");

    match btree.search(profile_id_hash) {
        Some(page_ref) => {
            let page_id = (page_ref >> 16) as u32;
            let offset = (page_id as usize) * PAGE_SIZE + 32;

            if offset + 100 <= data.len() {
                let profile = ProfileSlot::deserialize(&data[offset..])
                    .map_err(|e| MemHopError::Serialization(e.to_string()))?;

                Ok(Some(ProfileResult {
                    id: format!("{:016x}", profile.id_hash),
                    name: profile.name,
                    role: profile.role,
                    personality: profile.personality,
                    worldview: profile.worldview,
                    preferences: profile.preferences.clone(),
                    lexicon: profile.lexicon.clone(),
                    style_traits: profile.style_traits.clone(),
                    emotion_patterns: profile.emotion_patterns.clone(),
                    created_at: profile.created_at,
                    updated_at: profile.updated_at,
                }))
            } else {
                Err(MemHopError::PageNotFound(page_id))
            }
        }
        None => Ok(None),
    }
}
