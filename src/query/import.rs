//! Import memory implementation for MemHop
//!
//! Implements the import_memory() interface to batch import memories into L0/L2/L3 layers.
//! Also provides import_l3_from_path() for file-based L3 import with auto L2 creation.

use crate::file::free_list::allocate_from_free_list;
use crate::file::header::FileHeader;
use crate::index::btree::BTreeIndex;
use crate::index::sparse::SparseIndex;
use crate::query::types::*;
use crate::slot::context::{ActivationState, ContextSlot};
use crate::slot::profile::ProfileSlot;
use crate::util::hash_id;
use crate::MemHopError;
use memmap2::MmapMut;
use std::path::Path;
use std::time::{SystemTime, UNIX_EPOCH};

/// Helper function to calculate search terms and doc_len for L2 context
fn calculate_l2_sparse_index_data(
    ctx: &ContextSlot,
    mmap: &MmapMut,
    btree: &BTreeIndex,
) -> (Vec<String>, u32) {
    let mut terms = Vec::new();

    // Primary key: title
    terms.extend(ctx.title.split_whitespace().map(|s| s.to_lowercase()));

    // Secondary keys: summary
    if let Some(ref summary) = ctx.summary {
        terms.extend(summary.split_whitespace().map(|s| s.to_lowercase()));
    }

    // Secondary keys: L3 refs (if available)
    let mut l3_doc_len = 0;
    for &l3_id_hash in &ctx.l3_refs {
        if let Some(page_ref) = btree.search(l3_id_hash) {
            let l3_page_id = (page_ref >> 16) as u32;
            let l3_offset = (l3_page_id as usize) * PAGE_SIZE + 32;

            if l3_offset < mmap.len() {
                // Try to read L3 hypergraph node for additional search terms
                if let Ok(node) = crate::slot::hypergraph::HypergraphSlot::deserialize(&mmap[l3_offset..]) {
                    terms.extend(node.name.split_whitespace().map(|s| s.to_lowercase()));
                    l3_doc_len += node.name.len();
                }
            }
        }
    }

    let doc_len = ctx.title.len()
        + ctx.summary.as_ref().map_or(0, |s| s.len())
        + l3_doc_len;

    (terms, doc_len as u32)
}

const PAGE_SIZE: usize = 4096;

/// Import memory into specified layer
pub fn import_memory(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    request: ImportRequest,
) -> Result<ImportResult, MemHopError> {
    match request.target_layer {
        TargetLayer::Profile => import_l0_profile(mmap, header, btree, request.data, request.mode),
        TargetLayer::Topic => import_l2_topics(mmap, header, btree, sparse_index, request.data, request.mode, request.knowledge_title),
        TargetLayer::Knowledge => import_l3_knowledge(mmap, header, btree, sparse_index, request.data, request.mode),
    }
}

// ============================================================================
// L0 Profile Import
// ============================================================================

fn import_l0_profile(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    data: ImportData,
    mode: ImportMode,
) -> Result<ImportResult, MemHopError> {
    let now_ms = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as i64;

    if let ImportData::Profile { name, role, personality, worldview, preferences } = data {
        let profile_id_hash = hash_id("profile");

        match btree.search(profile_id_hash) {
            Some(page_ref) => {
                // Profile exists
                match mode {
                    ImportMode::Merge | ImportMode::Overwrite => {
                        let page_id = (page_ref >> 16) as u32;
                        let offset = (page_id as usize) * PAGE_SIZE + 32;

                        let mut profile = ProfileSlot::deserialize(&mmap[offset..])
                            .map_err(|e| MemHopError::Serialization(e.to_string()))?;

                        // Update fields
                        if let Some(n) = name { profile.name = n; }
                        if let Some(r) = role { profile.role = r; }
                        if let Some(p) = personality { profile.personality = p; }
                        if let Some(w) = worldview { profile.worldview = w; }
                        if let Some(pref) = preferences { profile.preferences = pref; }

                        profile.updated_at = now_ms;
                        profile.version += 1;

                        let data_bytes = profile.serialize()
                            .map_err(|e| MemHopError::Serialization(e.to_string()))?;

                        if offset + data_bytes.len() > mmap.len() {
                            return Err(MemHopError::Serialization(format!(
                                "ProfileSlot data too large for page: {} > {}",
                                data_bytes.len(), mmap.len() - offset
                            )));
                        }
                        mmap[offset..offset + data_bytes.len()].copy_from_slice(&data_bytes);

                        Ok(ImportResult {
                            status: ImportStatus::Success,
                            created_ids: vec![],
                            updated_ids: vec![format!("{:016x}", profile_id_hash)],
                            skipped_count: 0,
                            errors: vec![],
                        })
                    }
                    ImportMode::Skip => {
                        Ok(ImportResult {
                            status: ImportStatus::Success,
                            created_ids: vec![],
                            updated_ids: vec![],
                            skipped_count: 1,
                            errors: vec![],
                        })
                    }
                }
            }
            None => {
                // Profile doesn't exist, create new
                let page_id = allocate_from_free_list(mmap, header)?;
                let offset = (page_id as usize) * PAGE_SIZE + 32;

                let profile = ProfileSlot {
                    id_hash: profile_id_hash,
                    name: name.unwrap_or_else(|| "Agent".to_string()),
                    role: role.unwrap_or_else(|| "Assistant".to_string()),
                    personality: personality.unwrap_or_default(),
                    worldview: worldview.unwrap_or_default(),
                    preferences: preferences.unwrap_or_default(),
                    created_at: now_ms,
                    updated_at: now_ms,
                    version: 1,
                };

                let data_bytes = profile.serialize()
                    .map_err(|e| MemHopError::Serialization(e.to_string()))?;

                if offset + data_bytes.len() > mmap.len() {
                    return Err(MemHopError::Serialization(format!(
                        "ProfileSlot data too large for page: {} > {}",
                        data_bytes.len(), mmap.len() - offset
                    )));
                }
                mmap[offset..offset + data_bytes.len()].copy_from_slice(&data_bytes);

                btree.insert(profile_id_hash, (page_id as u64) << 16);

                Ok(ImportResult {
                    status: ImportStatus::Success,
                    created_ids: vec![format!("{:016x}", profile_id_hash)],
                    updated_ids: vec![],
                    skipped_count: 0,
                    errors: vec![],
                })
            }
        }
    } else {
        Err(MemHopError::ConfigError("Invalid import data for L0".to_string()))
    }
}

// ============================================================================
// L2 Topics Import
// ============================================================================

fn import_l2_topics(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    data: ImportData,
    mode: ImportMode,
    knowledge_title: Option<String>,
) -> Result<ImportResult, MemHopError> {
    let now_ms = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as i64;

    if let ImportData::Topics(items) = data {
        let mut created_ids = Vec::new();
        let mut updated_ids = Vec::new();
        let mut skipped_count = 0;
        let errors = Vec::new();

        // Find L3 domain if specified
        let l3_hash = if let Some(ref title) = knowledge_title {
            let hash = hash_id(title);
            if btree.search(hash).is_some() {
                Some(hash)
            } else {
                None
            }
        } else {
            None
        };

        for item in items.iter() {
            let id_hash = hash_id(&item.title);

            match btree.search(id_hash) {
                Some(page_ref) => {
                    // L2 context exists
                    match mode {
                        ImportMode::Merge | ImportMode::Overwrite => {
                            let page_id = (page_ref >> 16) as u32;
                            let offset = (page_id as usize) * PAGE_SIZE + 32;

                            let mut ctx = ContextSlot::deserialize(&mmap[offset..])
                                .map_err(|e| MemHopError::Serialization(e.to_string()))?;

                            // Update fields
                            ctx.title = item.title.clone();
                            ctx.summary = item.summary.clone();

                            // Update L3 reference if provided
                            if let Some(l3_h) = l3_hash {
                                if !ctx.l3_refs.contains(&l3_h) {
                                    ctx.l3_refs.push(l3_h);
                                }
                            }

                            ctx.updated_at = now_ms;
                            ctx.version += 1;

                            // Update sparse index
                            sparse_index.remove_document(ctx.id_hash);
                            let (terms, doc_len) = calculate_l2_sparse_index_data(&ctx, mmap, btree);
                            sparse_index.add_document(ctx.id_hash, terms, doc_len);

                            let data_bytes = ctx.serialize()
                                .map_err(|e| MemHopError::Serialization(e.to_string()))?;

                            if offset + data_bytes.len() > mmap.len() {
                                return Err(MemHopError::Serialization(format!(
                                    "ContextSlot data too large for page: {} > {}",
                                    data_bytes.len(), mmap.len() - offset
                                )));
                            }
                            mmap[offset..offset + data_bytes.len()].copy_from_slice(&data_bytes);

                            updated_ids.push(format!("{:016x}", id_hash));
                        }
                        ImportMode::Skip => {
                            skipped_count += 1;
                        }
                    }
                }
                None => {
                    // Create new L2 context
                    let page_id = allocate_from_free_list(mmap, header)?;
                    let offset = (page_id as usize) * PAGE_SIZE + 32;

                    let mut l3_refs = Vec::new();
                    if let Some(l3_h) = l3_hash {
                        l3_refs.push(l3_h);
                    }

                    let ctx = ContextSlot {
                        id_hash,
                        title: item.title.clone(),
                        summary: item.summary.clone(),
                        depth: 1,
                        archive_refs: vec![],
                        l3_refs,
                        turn_count: 0,
                        parent_id: None,
                        created_at: now_ms,
                        updated_at: now_ms,
                        version: 1,
                        importance: 0.5,
                        activation_score: 0.0,
                        is_active: false,
                        activation_state: ActivationState::Dormant,
                        centroid_page_ref: 0,
                        dialogue_range: (now_ms, now_ms),
                    };

                    let data_bytes = ctx.serialize()
                        .map_err(|e| MemHopError::Serialization(e.to_string()))?;

                    if offset + data_bytes.len() > mmap.len() {
                        return Err(MemHopError::Serialization(format!(
                            "ContextSlot data too large for page: {} > {}",
                            data_bytes.len(), mmap.len() - offset
                        )));
                    }
                    mmap[offset..offset + data_bytes.len()].copy_from_slice(&data_bytes);

                    // Add to sparse index
                    let (terms, doc_len) = calculate_l2_sparse_index_data(&ctx, mmap, btree);
                    sparse_index.add_document(id_hash, terms, doc_len);

                    btree.insert(id_hash, (page_id as u64) << 16);

                    created_ids.push(format!("{:016x}", id_hash));
                }
            }
        }

        let status = if errors.is_empty() {
            ImportStatus::Success
        } else {
            ImportStatus::PartialSuccess
        };

        Ok(ImportResult {
            status,
            created_ids,
            updated_ids,
            skipped_count,
            errors,
        })
    } else {
        Err(MemHopError::ConfigError("Invalid import data for L2".to_string()))
    }
}

// ============================================================================
// L3 Knowledge Import
// ============================================================================

fn import_l3_knowledge(
    _mmap: &mut MmapMut,
    _header: &mut FileHeader,
    _btree: &mut BTreeIndex,
    _sparse_index: &mut SparseIndex,
    _data: ImportData,
    _mode: ImportMode,
) -> Result<ImportResult, MemHopError> {
    // L3 Knowledge import is not supported in current architecture
    // L3 uses HypergraphSlot, not KnowledgeSlot
    Err(MemHopError::ConfigError("L3 Knowledge import not supported; use L3 Hypergraph API".to_string()))
}

// ============================================================================
// File-based L3 Hypergraph Builder
// ============================================================================

/// Build L3 hypergraph edges from file path
///
/// NOTE: L3 Knowledge layer (KnowledgeSlot) is not available in current architecture.
/// L3 uses HypergraphSlot. This function is retained as a stub for API compatibility.
pub fn build_l3_hypergraph_from_path(
    _mmap: &mut MmapMut,
    _header: &mut FileHeader,
    _btree: &mut BTreeIndex,
    _sparse_index: &mut SparseIndex,
    _path: &Path,
) -> Result<ImportResult, MemHopError> {
    Err(MemHopError::ConfigError(
        "L3 Knowledge layer not available; use Hypergraph API".to_string()
    ))
}
