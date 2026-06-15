// L0 Profile CRUD operations
//
// This module provides unified read/write operations for L0 (Profile) layer.
// All other modules should use these functions instead of duplicating the logic.

use crate::index::btree::BTreeIndex;
use crate::query::types::ProfileResult;
use crate::slot::profile::ProfileSlot;
use crate::util::hash::hash_id;
use crate::util::PAGE_SIZE;
use crate::MemHopError;
use memmap2::MmapMut;
use std::result::Result;

/// Read L0 profile from memory-mapped file
///
/// This is the canonical implementation for reading the agent's profile.
/// Used by: list.rs, search.rs, import.rs, update_title.rs
///
/// # Arguments
/// * `mmap` - Memory-mapped file reference
/// * `btree` - B-tree index for ID lookup
///
/// # Returns
/// * `Ok(Some(ProfileResult))` - Profile found and deserialized
/// * `Ok(None)` - Profile not found
/// * `Err(MemHopError)` - IO or serialization error
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

/// Update L0 profile in memory-mapped file
///
/// Writes the updated profile back to the same page location.
///
/// # Arguments
/// * `mmap` - Mutable memory-mapped file reference
/// * `btree` - B-tree index for ID lookup
/// * `profile_data` - Updated profile data to write
///
/// # Returns
/// * `Ok(())` - Profile updated successfully
/// * `Err(MemHopError)` - IO, serialization, or page not found error
pub fn update_profile(
    mmap: &mut MmapMut,
    btree: &BTreeIndex,
    profile_data: &ProfileResult,
) -> Result<(), MemHopError> {
    let profile_id_hash = hash_id("profile");

    match btree.search(profile_id_hash) {
        Some(page_ref) => {
            let page_id = (page_ref >> 16) as u32;
            let offset = (page_id as usize) * PAGE_SIZE + 32;

            // Create ProfileSlot from ProfileResult
            let profile_slot = ProfileSlot {
                id_hash: profile_id_hash,
                name: profile_data.name.clone(),
                role: profile_data.role.clone(),
                personality: profile_data.personality.clone(),
                worldview: profile_data.worldview.clone(),
                preferences: profile_data.preferences.clone(),
                created_at: profile_data.created_at,
                updated_at: profile_data.updated_at,
                version: 1,
            };

            // Serialize and write to mmap
            let buffer = profile_slot
                .serialize()
                .map_err(|e| MemHopError::Serialization(e.to_string()))?;

            if offset + buffer.len() <= mmap.len() {
                mmap[offset..offset + buffer.len()].copy_from_slice(&buffer);
                Ok(())
            } else {
                Err(MemHopError::PageNotFound(page_id))
            }
        }
        None => Err(MemHopError::Io(std::io::Error::new(
            std::io::ErrorKind::NotFound,
            "Profile not found in index",
        ))),
    }
}

#[cfg(test)]
mod tests {
    #[allow(unused_imports)]
    use super::*;

    #[test]
    fn test_read_profile_not_found() {
        // This test would require mocking mmap and btree
        // For now, we rely on integration tests
    }
}
