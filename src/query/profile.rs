// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L0 Profile CRUD internal implementation (v2 engine).

use crate::layers::profile::ProfileSlot;
use crate::query::types::ProfileResult;
use crate::shared::common::format_hash;
#[cfg(test)]
use crate::storage::record::REC_L0_PROFILE;
use crate::storage::StorageEngine;
use crate::util::hash_id;
use crate::MemHopError;

const PROFILE_ID: &str = "profile";

/// Read L0 profile from engine.
pub fn read_profile(engine: &StorageEngine) -> Result<Option<ProfileResult>, MemHopError> {
    let profile_id_hash = hash_id(PROFILE_ID);

    match engine.read_record(profile_id_hash)? {
        Some((_rt, data)) => {
            let profile = bincode::deserialize::<ProfileSlot>(data)
                .map_err(|e| MemHopError::Serialization(e.to_string()))?;
            Ok(Some(to_profile_result(&profile)))
        }
        None => Ok(None),
    }
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
    use crate::shared::common::now_ms;
    use crate::store::write_slot;
    use tempfile::NamedTempFile;

    fn write_profile(engine: &mut StorageEngine, mut slot: ProfileSlot) -> Result<(), MemHopError> {
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

        let data =
            bincode::serialize(&slot).map_err(|e| MemHopError::Serialization(e.to_string()))?;

        engine.write_record(REC_L0_PROFILE, profile_id_hash, &data)?;
        Ok(())
    }

    #[test]
    fn test_profile_write_and_read() {
        let temp = NamedTempFile::new().unwrap();
        let mut engine = StorageEngine::create(temp.path(), 768).unwrap();
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

        write_profile(&mut engine, slot).unwrap();
        let result = read_profile(&engine)
            .unwrap()
            .expect("profile should exist");
        assert_eq!(result.name, "Meow");
        assert_eq!(result.style_traits, vec!["brevity"]);
        assert!(result.updated_at > 0);
    }
}
