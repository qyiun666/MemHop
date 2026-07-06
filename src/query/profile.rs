// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L0 Profile CRUD internal implementation.

use crate::file::header::FileHeader;
use crate::index::btree::BTreeIndex;
use crate::layers::profile::ProfileSlot;
use crate::query::types::ProfileResult;
use crate::shared::common::{format_hash, now_ms};
use crate::shared::slot_io::{decode_page_id, get_slot_data};
use crate::util::{hash_id, PageType, PAGE_SIZE};
use crate::MemHopError;
use memmap2::MmapMut;
use std::fs::File;

const PROFILE_ID: &str = "profile";

/// Read L0 profile from memory-mapped file.
pub fn read_profile(
    mmap: &MmapMut,
    btree: &BTreeIndex,
) -> Result<Option<ProfileResult>, MemHopError> {
    let profile_id_hash = hash_id(PROFILE_ID);

    match btree.search(profile_id_hash) {
        Some(page_ref) => {
            let data: &[u8] = &mmap[..];
            let slot_data = get_slot_data(data, page_ref)
                .ok_or_else(|| MemHopError::PageNotFound(decode_page_id(page_ref)))?;
            let profile = ProfileSlot::deserialize_slot(slot_data)?;
            Ok(Some(to_profile_result(&profile)))
        }
        None => Ok(None),
    }
}

/// Write (create or update) a `ProfileSlot` to mmap.
///
/// Always allocates a fresh page so that arbitrarily large profiles are safe.
/// The old page is freed when updating.
pub fn write_profile(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    mut slot: ProfileSlot,
    file: &mut File,
) -> Result<(), MemHopError> {
    let profile_id_hash = hash_id(PROFILE_ID);

    slot.updated_at = now_ms();
    if slot.created_at == 0 {
        slot.created_at = slot.updated_at;
    }
    if slot.version == 0 {
        slot.version = 1;
    } else {
        slot.version += 1;
    }

    let data = slot
        .serialize()
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;

    if data.len() > PAGE_SIZE - 32 {
        return Err(MemHopError::Serialization(
            "ProfileSlot too large for a single page".to_string(),
        ));
    }

    // Free existing page if present.
    if let Some(old_page_ref) = btree.delete(profile_id_hash) {
        let old_page_id = decode_page_id(old_page_ref);
        crate::file::free_list::free_page(mmap, header, old_page_id)?;
    }

    let page_id = crate::file::page::allocate_page(
        mmap,
        header,
        PageType::Profile,
        0,
        crate::index::btree::EMPTY_PAGE,
        file,
    )?;
    crate::file::page::write_page_data(mmap, page_id, &data)?;

    btree.insert(profile_id_hash, (page_id as u64) << 16);
    Ok(())
}

fn to_profile_result(profile: &ProfileSlot) -> ProfileResult {
    ProfileResult {
        id: format_hash(profile.id_hash),
        name: profile.name.clone(),
        role: profile.role.clone(),
        personality: profile.personality.clone(),
        worldview: profile.worldview.clone(),
        preferences: profile.preferences.clone(),
        lexicon: profile.lexicon.clone(),
        style_traits: profile.style_traits.clone(),
        emotion_patterns: profile.emotion_patterns.clone(),
        created_at: profile.created_at,
        updated_at: profile.updated_at,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::test_helpers::create_test_mmap;

    #[test]
    fn test_profile_write_and_read() {
        let (mut mmap, mut header, mut btree, mut file) = create_test_mmap(64);
        let slot = ProfileSlot {
            id_hash: hash_id(PROFILE_ID),
            name: "Meow".into(),
            role: "assistant".into(),
            personality: "curious".into(),
            worldview: "open".into(),
            preferences: Default::default(),
            lexicon: Default::default(),
            style_traits: vec!["brevity".into()],
            emotion_patterns: Default::default(),
            created_at: 0,
            updated_at: 0,
            version: 0,
        };

        write_profile(&mut mmap, &mut header, &mut btree, slot, &mut file).unwrap();
        let result = read_profile(&mmap, &btree)
            .unwrap()
            .expect("profile should exist");
        assert_eq!(result.name, "Meow");
        assert_eq!(result.style_traits, vec!["brevity"]);
        assert!(result.updated_at > 0);
    }
}
