// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Title/profile update interfaces with sparse index synchronization.

use crate::file::header::FileHeader;
use crate::index::btree::BTreeIndex;
use crate::index::sparse::SparseIndex;
use crate::layers::action_chain::{ActionChainSlot, ChainStatus};
use crate::layers::context::ContextSlot;
use crate::layers::profile::ProfileSlot;
use crate::query::types::*;
use crate::shared::common::{self, format_hash, now_ms};
use crate::util::{hash_id, DEFAULT_GROW_PAGES, PAGE_SIZE};
use crate::MemHopError;
use memmap2::MmapMut;
use std::fs::File;

// ============================================================================
// Profile Update
// ============================================================================

/// Update profile with partial fields (merge strategy)
pub fn update_profile(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    request: UpdateProfileRequest,
    file: &mut File,
) -> Result<ProfileResult, MemHopError> {
    let now_ms = now_ms();

    let profile_id_hash = hash_id("profile");

    match btree.search(profile_id_hash) {
        Some(page_ref) => {
            let page_id = (page_ref >> 16) as u32;
            let offset = (page_id as usize) * PAGE_SIZE + 32;

            let mut profile = ProfileSlot::deserialize_slot(&mmap[offset..])?;

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
            if let Some(lexicon) = request.lexicon {
                // Merge: new entries override old, old entries preserved
                for (k, v) in lexicon {
                    profile.lexicon.insert(k, v);
                }
                // Enforce max 30 entries
                if profile.lexicon.len() > 30 {
                    let excess: Vec<String> = profile.lexicon.keys().skip(30).cloned().collect();
                    for k in excess {
                        profile.lexicon.remove(&k);
                    }
                }
            }
            if let Some(style_traits) = request.style_traits {
                profile.style_traits = style_traits;
                profile.style_traits.dedup();
                profile.style_traits.truncate(10);
            }
            if let Some(emotion_patterns) = request.emotion_patterns {
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

            profile.updated_at = now_ms;
            profile.version += 1;

            let data = profile
                .serialize()
                .map_err(|e| MemHopError::Serialization(e.to_string()))?;

            if offset + data.len() <= mmap.len() {
                mmap[offset..offset + data.len()].copy_from_slice(&data);
            } else {
                return Err(MemHopError::PageNotFound(page_id));
            }

            Ok(ProfileResult {
                id: format_hash(profile.id_hash),
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
            })
        }
        None => {
            use crate::file::free_list::allocate_or_extend;

            let page_id = allocate_or_extend(mmap, header, file, DEFAULT_GROW_PAGES)?;
            let offset = (page_id as usize) * PAGE_SIZE + 32;

            let profile = ProfileSlot {
                id_hash: profile_id_hash,
                name: request.name.unwrap_or_default(),
                role: request.role.unwrap_or_default(),
                personality: request.personality.unwrap_or_default(),
                worldview: request.worldview.unwrap_or_default(),
                preferences: request.preferences.unwrap_or_default(),
                lexicon: request.lexicon.unwrap_or_default(),
                style_traits: request.style_traits.unwrap_or_default(),
                emotion_patterns: request.emotion_patterns.unwrap_or_default(),
                created_at: now_ms,
                updated_at: now_ms,
                version: 1,
            };

            let data = profile
                .serialize()
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

            let page_ref = (page_id as u64) << 16; // layer=0, offset=0
            btree.insert(profile_id_hash, page_ref);

            Ok(ProfileResult {
                id: format_hash(profile.id_hash),
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
            })
        }
    }
}

// ============================================================================
// L2 Context Title Update
// ============================================================================

pub fn update_topic_title(
    mmap: &mut MmapMut,
    _header: &mut FileHeader,
    btree: &BTreeIndex,
    sparse_index: &mut SparseIndex,
    id: &str,
    new_title: String,
) -> Result<TopicSummary, MemHopError> {
    update_topic_title_inner(mmap, btree, sparse_index, id, new_title, None)
}

pub fn update_topic_title_with_refs(
    mmap: &mut MmapMut,
    _header: &mut FileHeader,
    btree: &BTreeIndex,
    sparse_index: &mut SparseIndex,
    id: &str,
    new_title: String,
    l3_refs: Option<Vec<String>>,
) -> Result<TopicSummary, MemHopError> {
    update_topic_title_inner(mmap, btree, sparse_index, id, new_title, l3_refs)
}

fn update_topic_title_inner(
    mmap: &mut MmapMut,
    btree: &BTreeIndex,
    sparse_index: &mut SparseIndex,
    id: &str,
    new_title: String,
    l3_refs: Option<Vec<String>>,
) -> Result<TopicSummary, MemHopError> {
    let now_ms = now_ms();

    let id_hash = common::parse_id_to_hash(id);

    match btree.search(id_hash) {
        Some(page_ref) => {
            let page_id = (page_ref >> 16) as u32;
            let offset = (page_id as usize) * PAGE_SIZE + 32;

            let mut ctx = ContextSlot::deserialize_slot(&mmap[offset..])?;

            sparse_index.remove_document(ctx.id_hash);

            ctx.title = new_title.clone();

            if let Some(ref refs) = l3_refs {
                let l3_hashes: Vec<u64> =
                    refs.iter().map(|s| common::parse_id_to_hash(s)).collect();
                ctx.l3_refs = l3_hashes;
            }

            let (new_terms, doc_len) = common::build_l2_sparse_terms(&ctx.title, &ctx.summary);
            sparse_index.add_document(ctx.id_hash, new_terms, doc_len);

            ctx.updated_at = now_ms;
            ctx.version += 1;

            let data = ctx
                .serialize()
                .map_err(|e| MemHopError::Serialization(e.to_string()))?;

            if offset + data.len() <= mmap.len() {
                mmap[offset..offset + data.len()].copy_from_slice(&data);
            } else {
                return Err(MemHopError::PageNotFound(page_id));
            }

            Ok(TopicSummary {
                id: format_hash(ctx.id_hash),
                title: ctx.title,
                depth: ctx.depth,
                archive_count: ctx.archive_refs.len(),
                turn_count: ctx.turn_count,
                is_active: ctx.is_active,
                updated_at: ctx.updated_at,
            })
        }
        None => Err(MemHopError::PageNotFound(0)),
    }
}

// ============================================================================
// L5 ActionChain Title Update
// ============================================================================

pub fn update_crystal_title(
    mmap: &mut MmapMut,
    btree: &BTreeIndex,
    id: &str,
    new_title: String,
) -> Result<CrystalSummary, MemHopError> {
    let now_ms = now_ms();

    let id_hash = common::parse_id_to_hash(id);

    match btree.search(id_hash) {
        Some(page_ref) => {
            let page_id = (page_ref >> 16) as u32;
            let offset = (page_id as usize) * PAGE_SIZE + 32;

            let mut chain = ActionChainSlot::deserialize_slot(&mmap[offset..])?;

            chain.title = new_title.clone();
            chain.updated_at = now_ms;
            chain.version += 1;

            let data = chain
                .serialize()
                .map_err(|e| MemHopError::Serialization(e.to_string()))?;

            if offset + data.len() <= mmap.len() {
                mmap[offset..offset + data.len()].copy_from_slice(&data);
            } else {
                return Err(MemHopError::PageNotFound(page_id));
            }

            Ok(CrystalSummary {
                id: format_hash(chain.id_hash),
                title: chain.title,
                condition: chain.trigger,
                status: match chain.status {
                    ChainStatus::Active => "active".to_string(),
                    ChainStatus::Deprecated => "deprecated".to_string(),
                    ChainStatus::Draft => "draft".to_string(),
                },
                trigger_count: chain.trigger_count,
                success_rate: chain.success_rate,
                last_triggered: if chain.last_triggered > 0 {
                    Some(chain.last_triggered)
                } else {
                    None
                },
                created_at: chain.created_at,
            })
        }
        None => Err(MemHopError::PageNotFound(0)),
    }
}

// ============================================================================
// L3 Knowledge Title Update (Interface 15)
// ============================================================================

pub fn update_knowledge_title(
    mmap: &mut MmapMut,
    btree: &BTreeIndex,
    id: &str,
    new_title: String,
) -> Result<KnowledgeSummary, MemHopError> {
    use crate::layers::hypergraph::HypergraphSlot;
    let now_ms = now_ms();
    let id_hash = common::parse_id_to_hash(id);

    match btree.search(id_hash) {
        Some(page_ref) => {
            let page_id = (page_ref >> 16) as u32;
            let offset = (page_id as usize) * crate::util::PAGE_SIZE + 32;

            let mut slot = HypergraphSlot::deserialize_slot(&mmap[offset..])?;
            slot.name = new_title.clone();
            slot.updated_at = now_ms;
            slot.version += 1;

            let data = slot
                .serialize()
                .map_err(|e| MemHopError::Serialization(e.to_string()))?;

            if offset + data.len() <= mmap.len() {
                mmap[offset..offset + data.len()].copy_from_slice(&data);
            } else {
                return Err(MemHopError::PageNotFound(page_id));
            }

            let (importance, knowledge_type) = compute_knowledge_meta(mmap, btree, id_hash);

            Ok(KnowledgeSummary {
                id: format_hash(slot.id_hash),
                title: slot.name,
                domain: slot.source.domain_name().to_string(),
                knowledge_type,
                importance,
                confidence: 1.0,
                updated_at: slot.updated_at,
            })
        }
        None => Err(MemHopError::PageNotFound(0)),
    }
}

fn compute_knowledge_meta(mmap: &mut MmapMut, btree: &BTreeIndex, graph_id: u64) -> (f32, String) {
    let query = NodeListQuery {
        page: 1,
        page_size: 1000,
        node_type: None,
        keyword: None,
        min_importance: None,
    };
    let nodes = match crate::l3::store::list_nodes_by_graph(mmap, btree, graph_id, &query) {
        Ok(result) => result.items,
        Err(_) => return (0.5, "Generic".to_string()),
    };

    let count = nodes.len();
    if count == 0 {
        return (0.5, "Generic".to_string());
    }

    let imp_sum: f32 = nodes.iter().map(|n| n.importance).sum();
    let importance = imp_sum / count as f32;

    use std::collections::HashMap;
    let mut type_counts: HashMap<&str, usize> = HashMap::new();
    for node in &nodes {
        *type_counts.entry(&node.node_type).or_default() += 1;
    }
    let knowledge_type = type_counts
        .into_iter()
        .max_by_key(|&(_, count)| count)
        .map(|(t, _)| t.to_string())
        .unwrap_or_else(|| "Generic".to_string());

    (importance, knowledge_type)
}
