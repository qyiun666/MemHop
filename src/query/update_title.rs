//! Update title implementations for MemHop
//!
//! Implements title/profile update interfaces with sparse index synchronization.

use crate::file::header::FileHeader;
use crate::index::btree::BTreeIndex;
use crate::index::sparse::SparseIndex;
use crate::query::common::{self, format_hash, now_ms};
use crate::query::types::*;
use crate::slot::action_chain::{ActionChainSlot, ChainStatus};
use crate::slot::context::ContextSlot;
use crate::slot::profile::ProfileSlot;
use crate::util::{hash_id, PAGE_SIZE};
use crate::MemHopError;
use memmap2::MmapMut;

// ============================================================================
// Profile Update
// ============================================================================

/// Update profile with partial fields (merge strategy)
pub fn update_profile(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    request: UpdateProfileRequest,
) -> Result<ProfileResult, MemHopError> {
    let now_ms = now_ms();

    let profile_id_hash = hash_id("profile");

    match btree.search(profile_id_hash) {
        Some(page_ref) => {
            let page_id = (page_ref >> 16) as u32;
            let offset = (page_id as usize) * PAGE_SIZE + 32;

            // Deserialize existing profile
            let mut profile = ProfileSlot::deserialize_slot(&mmap[offset..])?;

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
            let data = profile
                .serialize()
                .map_err(|e| MemHopError::Serialization(e.to_string()))?;

            if offset + data.len() <= mmap.len() {
                mmap[offset..offset + data.len()].copy_from_slice(&data);
            } else {
                return Err(MemHopError::PageNotFound(page_id));
            }

            // Return updated profile
            Ok(ProfileResult {
                id: format_hash(profile.id_hash),
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
                worldview: request.worldview.unwrap_or_default(),
                preferences: request.preferences.unwrap_or_default(),
                created_at: now_ms,
                updated_at: now_ms,
                version: 1,
            };

            // Serialize and write to page
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

            // Insert into B-tree index
            let page_ref = (page_id as u64) << 16; // layer=0, offset=0
            btree.insert(profile_id_hash, page_ref);

            // Return new profile
            Ok(ProfileResult {
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
// L2 Context Title Update
// ============================================================================

/// Update L2 context title with sparse index synchronization
pub fn update_topic_title(
    mmap: &mut MmapMut,
    _header: &mut FileHeader,
    btree: &BTreeIndex,
    sparse_index: &mut SparseIndex,
    id: &str,
    new_title: String,
) -> Result<TopicSummary, MemHopError> {
    let now_ms = now_ms();

    let id_hash = common::parse_id_to_hash(id);

    match btree.search(id_hash) {
        Some(page_ref) => {
            let page_id = (page_ref >> 16) as u32;
            let offset = (page_id as usize) * PAGE_SIZE + 32;

            let mut ctx = ContextSlot::deserialize_slot(&mmap[offset..])?;

            // Update sparse index: remove old terms
            sparse_index.remove_document(ctx.id_hash);

            // Update title
            ctx.title = new_title.clone();

            // Add new terms to sparse index
            let mut new_terms = Vec::new();
            new_terms.extend(ctx.title.split_whitespace().map(|s| s.to_lowercase()));
            if let Some(ref summary) = ctx.summary {
                new_terms.extend(summary.split_whitespace().map(|s| s.to_lowercase()));
            }
            let doc_len = ctx.title.len() + ctx.summary.as_ref().map_or(0, |s| s.len());
            sparse_index.add_document(ctx.id_hash, new_terms, doc_len as u32);

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
                id: format!("{:016x}", ctx.id_hash),
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

/// Update L5 action chain title
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
                id: format!("{:016x}", chain.id_hash),
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

/// Update L3 knowledge hypergraph title
pub fn update_knowledge_title(
    mmap: &mut MmapMut,
    btree: &BTreeIndex,
    id: &str,
    new_title: String,
) -> Result<KnowledgeSummary, MemHopError> {
    use crate::slot::hypergraph::HypergraphSlot;
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

            // Aggregate node data via l3::store for meaningful KnowledgeSummary
            let (importance, knowledge_type) = compute_knowledge_meta(mmap, btree, id_hash);

            Ok(KnowledgeSummary {
                id: format!("{:016x}", slot.id_hash),
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

/// Compute importance and knowledge_type from graph nodes via l3 store
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

    // Derive knowledge_type from most common node_type
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
