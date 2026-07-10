// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L0 Profile CRUD — pure data operations.
//!
//! Reads/writes a single ProfileSlot identified by the fixed "profile" hash.

use crate::layers::profile::ProfileSlot;
use crate::shared::common::{format_hash, now_ms};
use crate::storage::record::REC_L0_PROFILE;
use crate::storage::StorageEngine;
use crate::store::{read_slot, write_slot};
use crate::util::hash_id;
use crate::MemHopError;

const PROFILE_ID: &str = "profile";

/// Read L0 ProfileSlot from storage engine.
/// Returns the raw slot, or `None` if no profile exists yet.
pub fn read_profile(engine: &StorageEngine) -> Result<Option<ProfileSlot>, MemHopError> {
    let profile_id_hash = hash_id(PROFILE_ID);
    read_slot(engine, profile_id_hash)
}

/// Write (create or overwrite) a ProfileSlot.
/// Returns the hex-formatted profile ID.
pub fn write_profile(
    engine: &mut StorageEngine,
    mut slot: ProfileSlot,
) -> Result<String, MemHopError> {
    let profile_id_hash = hash_id(PROFILE_ID);
    slot.id_hash = profile_id_hash;
    slot.updated_at = now_ms();
    if slot.created_at == 0 {
        slot.created_at = slot.updated_at;
    }
    write_slot(engine, REC_L0_PROFILE, profile_id_hash, &slot)?;
    Ok(format_hash(profile_id_hash))
}

/// Update an existing ProfileSlot in-place (read-modify-write).
pub fn update_profile(engine: &mut StorageEngine, slot: ProfileSlot) -> Result<(), MemHopError> {
    let profile_id_hash = hash_id(PROFILE_ID);
    match read_slot::<ProfileSlot>(engine, profile_id_hash)? {
        Some(mut existing) => {
            existing.name = slot.name;
            existing.role = slot.role;
            existing.personality = slot.personality;
            existing.preferences = slot.preferences;
            existing.lexicon = slot.lexicon;
            existing.style_traits = slot.style_traits;
            existing.emotion_patterns = slot.emotion_patterns;
            existing.updated_at = now_ms();
            if existing.created_at == 0 {
                existing.created_at = existing.updated_at;
            }
            write_slot(engine, REC_L0_PROFILE, profile_id_hash, &existing)?;
            Ok(())
        }
        None => {
            let _ = write_profile(engine, slot)?;
            Ok(())
        }
    }
}
