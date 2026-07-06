// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! API-4: L0 Profile operations.

use crate::layers::profile::ProfileSlot;
use crate::query::types::{ProfileResult, UpdateProfileRequest};
use crate::util::hash_id;
use crate::{MemHop, Result};

impl MemHop {
    /// Get the L0 agent profile.
    pub fn get_profile(&self) -> Result<Option<ProfileResult>> {
        crate::query::profile::read_profile(&self.mmap, &self.btree)
    }

    /// Update the L0 agent profile (merge strategy).
    pub fn update_profile(&mut self, update: UpdateProfileRequest) -> Result<ProfileResult> {
        let profile_id_hash = hash_id("profile");
        let mut profile = match self.btree.search(profile_id_hash) {
            Some(page_ref) => {
                let data: &[u8] = &self.mmap[..];
                let slot_data = crate::shared::slot_io::get_slot_data(data, page_ref).ok_or(
                    crate::MemHopError::PageNotFound(crate::shared::slot_io::decode_page_id(
                        page_ref,
                    )),
                )?;
                crate::layers::profile::ProfileSlot::deserialize_slot(slot_data)?
            }
            None => ProfileSlot {
                id_hash: profile_id_hash,
                name: String::new(),
                role: String::new(),
                personality: String::new(),
                worldview: String::new(),
                preferences: std::collections::HashMap::new(),
                lexicon: std::collections::HashMap::new(),
                style_traits: Vec::new(),
                emotion_patterns: std::collections::HashMap::new(),
                created_at: 0,
                updated_at: 0,
                version: 0,
            },
        };

        if let Some(name) = update.name {
            profile.name = name;
        }
        if let Some(role) = update.role {
            profile.role = role;
        }
        if let Some(personality) = update.personality {
            profile.personality = personality;
        }
        if let Some(worldview) = update.worldview {
            profile.worldview = worldview;
        }
        if let Some(preferences) = update.preferences {
            profile.preferences = preferences;
        }
        if let Some(lexicon) = update.lexicon {
            for (k, v) in lexicon {
                profile.lexicon.insert(k, v);
            }
            if profile.lexicon.len() > 30 {
                let excess: Vec<String> = profile.lexicon.keys().skip(30).cloned().collect();
                for k in excess {
                    profile.lexicon.remove(&k);
                }
            }
        }
        if let Some(style_traits) = update.style_traits {
            profile.style_traits = style_traits;
            profile.style_traits.dedup();
            profile.style_traits.truncate(10);
        }
        if let Some(emotion_patterns) = update.emotion_patterns {
            for (k, v) in emotion_patterns {
                profile.emotion_patterns.insert(k, v);
            }
            if profile.emotion_patterns.len() > 10 {
                let excess: Vec<String> =
                    profile.emotion_patterns.keys().skip(10).cloned().collect();
                for k in excess {
                    profile.emotion_patterns.remove(&k);
                }
            }
        }

        crate::query::profile::write_profile(
            &mut self.mmap,
            &mut self.header,
            &mut self.btree,
            profile,
            &mut self.file,
        )?;

        self.get_profile()
            .map(|opt| opt.expect("profile just written"))
    }
}
