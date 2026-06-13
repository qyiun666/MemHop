//! Update title implementations for MemHop
//!
//! Implements L0-L5 title/profile update interfaces with sparse index synchronization.

use crate::file::header::FileHeader;
use crate::index::btree::BTreeIndex;
use crate::index::sparse::SparseIndex;
use crate::query::types::*;
use crate::slot::crystal::CrystalSlot;
use crate::slot::knowledge::KnowledgeSlot;
use crate::slot::profile::ProfileSlot;
use crate::slot::topic::TopicSlot;
use crate::util::hash_id;
use crate::MemHopError;
use memmap2::MmapMut;
use std::time::{SystemTime, UNIX_EPOCH};

const PAGE_SIZE: usize = 4096;

// ============================================================================
// L0 Profile Update
// ============================================================================

/// Update L0 profile with partial fields (merge strategy)
pub fn update_l0_profile(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    request: UpdateL0Request,
) -> Result<L0Profile, MemHopError> {
    let now_ms = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as i64;

    let profile_id_hash = hash_id("profile");

    match btree.search(profile_id_hash) {
        Some(page_ref) => {
            let page_id = (page_ref >> 16) as u32;
            let offset = (page_id as usize) * PAGE_SIZE + 32;

            // Deserialize existing profile
            let mut profile = ProfileSlot::deserialize(&mmap[offset..])
                .map_err(|e| MemHopError::Serialization(e.to_string()))?;

            // Merge update fields (only update Some values)
            if let Some(name) = request.name {
                profile.name = name;
            }
            if let Some(role) = request.role {
                profile.role = role;
            }
            if let Some(personality) = request.personality {
                profile.personality = personality;
            }
            if let Some(worldview) = request.worldview {
                profile.worldview = worldview;
            }
            if let Some(preferences) = request.preferences {
                profile.preferences = preferences;
            }

            // Update timestamp and version
            profile.updated_at = now_ms;
            profile.version += 1;

            // Serialize and write back
            let data = profile.serialize()
                .map_err(|e| MemHopError::Serialization(e.to_string()))?;

            if offset + data.len() <= mmap.len() {
                mmap[offset..offset + data.len()].copy_from_slice(&data);
            } else {
                return Err(MemHopError::PageNotFound(page_id));
            }

            // Return updated profile
            Ok(L0Profile {
                id: format!("{:016x}", profile.id_hash),
                name: profile.name,
                role: profile.role,
                personality: profile.personality,
                worldview: profile.worldview,
                preferences: profile.preferences.clone(),
                created_at: profile.created_at,
                updated_at: profile.updated_at,
            })
        }
        None => {
            // Create new profile if not exists
            use crate::file::free_list::allocate_from_free_list;
            
            // Allocate a new page for the profile
            let page_id = allocate_from_free_list(mmap, header)?;
            let offset = (page_id as usize) * PAGE_SIZE + 32;
            
            // Create new profile with provided values
            let profile = ProfileSlot {
                id_hash: profile_id_hash,
                name: request.name.unwrap_or_default(),
                role: request.role.unwrap_or_default(),
                personality: request.personality.unwrap_or_default(),
                values: String::new(), // Default empty values
                worldview: request.worldview.unwrap_or_default(),
                preferences: request.preferences.unwrap_or_default(),
                created_at: now_ms,
                updated_at: now_ms,
                version: 1,
            };
            
            // Serialize and write to page
            let data = profile.serialize()
                .map_err(|e| MemHopError::Serialization(e.to_string()))?;
            
            if offset + data.len() <= mmap.len() {
                mmap[offset..offset + data.len()].copy_from_slice(&data);
                // Fill remaining page space with zeros to avoid garbage data
                let page_end = ((page_id as usize) + 1) * PAGE_SIZE;
                if offset + data.len() < page_end && page_end <= mmap.len() {
                    for byte in &mut mmap[offset + data.len()..page_end] {
                        *byte = 0;
                    }
                }
            } else {
                return Err(MemHopError::PageNotFound(page_id));
            }
            
            // Insert into B-tree index
            let page_ref = ((page_id as u64) << 16) | 0; // layer=0, offset=0
            btree.insert(profile_id_hash, page_ref);
            
            // Return new profile
            Ok(L0Profile {
                id: format!("{:016x}", profile.id_hash),
                name: profile.name,
                role: profile.role,
                personality: profile.personality,
                worldview: profile.worldview,
                preferences: profile.preferences.clone(),
                created_at: profile.created_at,
                updated_at: profile.updated_at,
            })
        }
    }
}

// ============================================================================
// L2 Topic Title Update
// ============================================================================

/// Update L2 topic title with sparse index synchronization
pub fn update_l2_title(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &BTreeIndex,
    sparse_index: &mut SparseIndex,
    id: &str,
    new_title: String,
) -> Result<L2TopicSummary, MemHopError> {
    let now_ms = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as i64;

    // Parse ID as hex hash or compute hash from string
    let id_hash = if id.len() == 16 {
        u64::from_str_radix(id, 16).unwrap_or_else(|_| hash_id(id))
    } else {
        hash_id(id)
    };

    match btree.search(id_hash) {
        Some(page_ref) => {
            let page_id = (page_ref >> 16) as u32;
            let offset = (page_id as usize) * PAGE_SIZE + 32;

            // Deserialize existing topic
            let mut topic = TopicSlot::deserialize(&mmap[offset..])
                .map_err(|e| MemHopError::Serialization(e.to_string()))?;

            // Update sparse index: remove old terms
            sparse_index.remove_document(topic.id_hash);

            // Update title
            topic.title = new_title.clone();

            // Add new terms to sparse index using primary + secondary keys
            let mut new_terms = Vec::new();
            new_terms.extend(topic.title.split_whitespace().map(|s| s.to_lowercase()));
            if let Some(ref summary) = topic.summary {
                new_terms.extend(summary.split_whitespace().map(|s| s.to_lowercase()));
            }
            let doc_len = topic.title.len() 
                + topic.summary.as_ref().map_or(0, |s| s.len());
            sparse_index.add_document(topic.id_hash, new_terms, doc_len as u32);

            // Update timestamp and version
            topic.updated_at = now_ms;
            topic.version += 1;

            // Serialize and write back
            let data = topic.serialize()
                .map_err(|e| MemHopError::Serialization(e.to_string()))?;

            if offset + data.len() <= mmap.len() {
                mmap[offset..offset + data.len()].copy_from_slice(&data);
            } else {
                return Err(MemHopError::PageNotFound(page_id));
            }

            // Return updated summary
            Ok(L2TopicSummary {
                id: format!("{:016x}", topic.id_hash),
                title: topic.title,
                node_count: topic.node_ids.len(),
                is_active: topic.is_active,
                updated_at: topic.updated_at,
            })
        }
        None => Err(MemHopError::PageNotFound(0)),
    }
}

// ============================================================================
// L3 Knowledge Title Update
// ============================================================================

/// Update L3 knowledge title with sparse index synchronization
pub fn update_l3_title(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &BTreeIndex,
    sparse_index: &mut SparseIndex,
    id: &str,
    new_title: String,
) -> Result<L3DomainSummary, MemHopError> {
    let now_ms = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as i64;

    // Parse ID as hex hash or compute hash from string
    let id_hash = if id.len() == 16 {
        u64::from_str_radix(id, 16).unwrap_or_else(|_| hash_id(id))
    } else {
        hash_id(id)
    };

    match btree.search(id_hash) {
        Some(page_ref) => {
            let page_id = (page_ref >> 16) as u32;
            let offset = (page_id as usize) * PAGE_SIZE + 32;

            // Deserialize existing knowledge
            let mut knowledge = KnowledgeSlot::deserialize(&mmap[offset..])
                .map_err(|e| MemHopError::Serialization(e.to_string()))?;

            // Update sparse index: remove old title terms
            sparse_index.remove_document(knowledge.id_hash);

            // Update title
            knowledge.title = new_title.clone();

            // Add new title terms to sparse index
            let new_terms: Vec<String> = knowledge.title.split_whitespace().map(|s| s.to_string()).collect();
            sparse_index.add_document(knowledge.id_hash, new_terms, knowledge.title.len() as u32);

            // Update timestamp and version
            knowledge.updated_at = now_ms;
            knowledge.version += 1;

            // Serialize and write back
            let data = knowledge.serialize()
                .map_err(|e| MemHopError::Serialization(e.to_string()))?;

            if offset + data.len() <= mmap.len() {
                mmap[offset..offset + data.len()].copy_from_slice(&data);
            } else {
                return Err(MemHopError::PageNotFound(page_id));
            }

            // Return updated summary
            Ok(L3DomainSummary {
                id: format!("{:016x}", knowledge.id_hash),
                title: knowledge.title,
                domain: knowledge.domain,
                knowledge_type: format!("{:?}", knowledge.knowledge_type),
                updated_at: knowledge.updated_at,
                importance: knowledge.importance,
                confidence: knowledge.confidence,
            })
        }
        None => Err(MemHopError::PageNotFound(0)),
    }
}

// ============================================================================
// L5 Crystal Title Update
// ============================================================================

/// Update L5 crystal/skill title
pub fn update_l5_title(
    mmap: &mut MmapMut,
    btree: &BTreeIndex,
    id: &str,
    new_title: String,
) -> Result<L5SkillSummary, MemHopError> {
    let now_ms = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as i64;

    // Parse ID as hex hash or compute hash from string
    let id_hash = if id.len() == 16 {
        u64::from_str_radix(id, 16).unwrap_or_else(|_| hash_id(id))
    } else {
        hash_id(id)
    };

    match btree.search(id_hash) {
        Some(page_ref) => {
            let page_id = (page_ref >> 16) as u32;
            let offset = (page_id as usize) * PAGE_SIZE + 32;

            // Deserialize existing crystal
            let mut crystal = CrystalSlot::deserialize(&mmap[offset..])
                .map_err(|e| MemHopError::Serialization(e.to_string()))?;

            // Update title
            crystal.title = new_title.clone();

            // Update timestamp and version
            crystal.created_at = now_ms; // Crystal uses created_at as last modified
            crystal.version += 1;

            // Serialize and write back
            let data = crystal.serialize()
                .map_err(|e| MemHopError::Serialization(e.to_string()))?;

            if offset + data.len() <= mmap.len() {
                mmap[offset..offset + data.len()].copy_from_slice(&data);
            } else {
                return Err(MemHopError::PageNotFound(page_id));
            }

            // Return updated summary
            Ok(L5SkillSummary {
                id: format!("{:016x}", crystal.id_hash),
                title: crystal.title,
                condition: crystal.condition,
                status: match crystal.status {
                    crate::slot::crystal::CrystalStatus::Crystallized => "active".to_string(),
                    crate::slot::crystal::CrystalStatus::NotCrystallized => "inactive".to_string(),
                },
                trigger_count: crystal.trigger_count,
                success_rate: crystal.confidence,
                last_triggered: if crystal.last_triggered > 0 { Some(crystal.last_triggered) } else { None },
                created_at: crystal.created_at,
            })
        }
        None => Err(MemHopError::PageNotFound(0)),
    }
}
