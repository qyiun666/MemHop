//! Import memory implementation for MemHop
//!
//! Implements the import_memory() interface to batch import memories into L0/L2/L3 layers.

use crate::file::free_list::allocate_from_free_list;
use crate::file::header::FileHeader;
use crate::index::btree::BTreeIndex;
use crate::index::sparse::SparseIndex;
use crate::query::types::*;
use crate::slot::knowledge::{KnowledgeSlot, KnowledgeType};
use crate::slot::profile::ProfileSlot;
use crate::slot::topic::TopicSlot;
use crate::util::hash_id;
use crate::MemHopError;
use memmap2::MmapMut;
use std::time::{SystemTime, UNIX_EPOCH};

/// Helper function to calculate search terms and doc_len for L2 topic
/// This eliminates code duplication between create and update paths
fn calculate_l2_sparse_index_data(
    topic: &TopicSlot,
    mmap: &MmapMut,
    btree: &BTreeIndex,
) -> (Vec<String>, u32) {
    let mut terms = Vec::new();
    
    // Primary key: title
    terms.extend(topic.title.split_whitespace().map(|s| s.to_lowercase()));
    
    // Secondary keys: summary
    if let Some(ref summary) = topic.summary {
        terms.extend(summary.split_whitespace().map(|s| s.to_lowercase()));
    }
    
    // Secondary keys: L3 knowledge contents (if available)
    let mut l3_doc_len = 0;
    for &l3_id_hash in &topic.l3_refs {
        if let Some(page_ref) = btree.search(l3_id_hash) {
            let l3_page_id = (page_ref >> 16) as u32;
            let l3_offset = (l3_page_id as usize) * PAGE_SIZE + 32;
            
            if l3_offset < mmap.len() {
                if let Ok(knowledge) = KnowledgeSlot::deserialize(&mmap[l3_offset..]) {
                    terms.extend(knowledge.title.split_whitespace().map(|s| s.to_lowercase()));
                    terms.extend(knowledge.text.split_whitespace().map(|s| s.to_lowercase()));
                    if let Some(ref summary) = knowledge.summary {
                        terms.extend(summary.split_whitespace().map(|s| s.to_lowercase()));
                    }
                    l3_doc_len += knowledge.title.len() 
                        + knowledge.text.len()
                        + knowledge.summary.as_ref().map_or(0, |s| s.len());
                }
            }
        }
    }
    
    let doc_len = topic.title.len() 
        + topic.summary.as_ref().map_or(0, |s| s.len())
        + l3_doc_len;
    
    (terms, doc_len as u32)
}

/// Helper function to calculate search terms and doc_len for L3 knowledge
/// This eliminates code duplication between create and update paths
fn calculate_l3_sparse_index_data(knowledge: &KnowledgeSlot) -> (Vec<String>, u32) {
    let mut terms = Vec::new();
    terms.extend(knowledge.title.split_whitespace().map(|s| s.to_lowercase()));
    terms.extend(knowledge.text.split_whitespace().map(|s| s.to_lowercase()));
    if let Some(ref summary) = knowledge.summary {
        terms.extend(summary.split_whitespace().map(|s| s.to_lowercase()));
    }
    for keyword in &knowledge.keywords {
        terms.extend(keyword.split_whitespace().map(|s| s.to_lowercase()));
    }
    let doc_len = knowledge.title.len() + knowledge.text.len()
        + knowledge.summary.as_ref().map_or(0, |s| s.len())
        + knowledge.keywords.iter().map(|k| k.len()).sum::<usize>();
    
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
        TargetLayer::L0 => import_l0_profile(mmap, header, btree, request.data, request.mode),
        TargetLayer::L2 => import_l2_topics(mmap, header, btree, sparse_index, request.data, request.mode, request.l3_title),
        TargetLayer::L3 => import_l3_knowledge(mmap, header, btree, sparse_index, request.data, request.mode),
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

    if let ImportData::L0Profile { name, role, personality, worldview, preferences } = data {
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

                        if offset + data_bytes.len() <= mmap.len() {
                            mmap[offset..offset + data_bytes.len()].copy_from_slice(&data_bytes);
                        }

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
                    values: String::new(),
                    worldview: worldview.unwrap_or_default(),
                    preferences: preferences.unwrap_or_default(),
                    created_at: now_ms,
                    updated_at: now_ms,
                    version: 1,
                };

                let data_bytes = profile.serialize()
                    .map_err(|e| MemHopError::Serialization(e.to_string()))?;

                if offset + data_bytes.len() <= mmap.len() {
                    mmap[offset..offset + data_bytes.len()].copy_from_slice(&data_bytes);
                }

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
    l3_title: Option<String>,
) -> Result<ImportResult, MemHopError> {
    let now_ms = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as i64;

    if let ImportData::L2Topics(items) = data {
        let mut created_ids = Vec::new();
        let mut updated_ids = Vec::new();
        let mut skipped_count = 0;
        let errors = Vec::new();

        // Find L3 domain if specified
        let l3_hash = if let Some(ref title) = l3_title {
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
                    // Topic exists
                    match mode {
                        ImportMode::Merge | ImportMode::Overwrite => {
                            let page_id = (page_ref >> 16) as u32;
                            let offset = (page_id as usize) * PAGE_SIZE + 32;

                            let mut topic = TopicSlot::deserialize(&mmap[offset..])
                                .map_err(|e| MemHopError::Serialization(e.to_string()))?;

                            // Update fields
                            topic.title = item.title.clone();
                            topic.summary = item.summary.clone();

                            // Update L3 reference if provided
                            if let Some(l3_h) = l3_hash {
                                if !topic.l3_refs.contains(&l3_h) {
                                    topic.l3_refs.push(l3_h);
                                }
                            }

                            topic.updated_at = now_ms;
                            topic.version += 1;

                            // Update sparse index using title + L3 knowledge contents
                            sparse_index.remove_document(topic.id_hash);
                            let (terms, doc_len) = calculate_l2_sparse_index_data(&topic, mmap, btree);
                            sparse_index.add_document(topic.id_hash, terms, doc_len);

                            let data_bytes = topic.serialize()
                                .map_err(|e| MemHopError::Serialization(e.to_string()))?;

                            if offset + data_bytes.len() <= mmap.len() {
                                mmap[offset..offset + data_bytes.len()].copy_from_slice(&data_bytes);
                            }

                            updated_ids.push(format!("{:016x}", id_hash));
                        }
                        ImportMode::Skip => {
                            skipped_count += 1;
                        }
                    }
                }
                None => {
                    // Create new topic
                    let page_id = allocate_from_free_list(mmap, header)?;
                    let offset = (page_id as usize) * PAGE_SIZE + 32;

                    let mut l3_refs = Vec::new();
                    if let Some(l3_h) = l3_hash {
                        l3_refs.push(l3_h);
                    }

                    let topic = TopicSlot {
                        id_hash,
                        title: item.title.clone(),
                        summary: item.summary.clone(),
                        node_ids: vec![],
                        l3_refs,
                        l4_refs: vec![],
                        parent_id: None,
                        created_at: now_ms,
                        updated_at: now_ms,
                        version: 1,
                        importance: 0.5,
                        activation_score: 0.0,
                        is_active: false,
                        activation_state: crate::slot::topic::ActivationState::Dormant,
                        centroid_vector: None,
                        domain_weights: vec![],
                        dialogue_range: (now_ms, now_ms),
                        reserved: [0; 16],
                    };

                    let data_bytes = topic.serialize()
                        .map_err(|e| MemHopError::Serialization(e.to_string()))?;

                    if offset + data_bytes.len() <= mmap.len() {
                        mmap[offset..offset + data_bytes.len()].copy_from_slice(&data_bytes);
                    }

                    // Add to sparse index using title + L3 knowledge contents
                    let (terms, doc_len) = calculate_l2_sparse_index_data(&topic, mmap, btree);
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
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    data: ImportData,
    mode: ImportMode,
) -> Result<ImportResult, MemHopError> {
    let now_ms = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as i64;

    if let ImportData::L3Knowledge(items) = data {
        let mut created_ids = Vec::new();
        let mut updated_ids = Vec::new();
        let mut skipped_count = 0;
        let errors = Vec::new();

        for item in items.iter() {
            let id_hash = hash_id(&item.title);

            match btree.search(id_hash) {
                Some(page_ref) => {
                    // Knowledge exists
                    match mode {
                        ImportMode::Merge | ImportMode::Overwrite => {
                            let page_id = (page_ref >> 16) as u32;
                            let offset = (page_id as usize) * PAGE_SIZE + 32;

                            let mut knowledge = KnowledgeSlot::deserialize(&mmap[offset..])
                                .map_err(|e| MemHopError::Serialization(e.to_string()))?;

                            // Update fields
                            knowledge.title = item.title.clone();
                            knowledge.domain = item.domain.clone();
                            knowledge.text = item.text.clone();
                            knowledge.summary = item.summary.clone();
                            knowledge.keywords = item.keywords.clone();
                            knowledge.source_ref = item.source_ref.clone();

                            // Parse knowledge type
                            knowledge.knowledge_type = match item.knowledge_type.as_str() {
                                "Factual" => KnowledgeType::Factual,
                                "Procedural" => KnowledgeType::Procedural,
                                "Conceptual" => KnowledgeType::Conceptual,
                                "Contextual" => KnowledgeType::Contextual,
                                _ => KnowledgeType::Factual,
                            };

                            knowledge.updated_at = now_ms;
                            knowledge.version += 1;

                            // Update sparse index using primary + secondary keys
                            sparse_index.remove_document(knowledge.id_hash);
                            let (terms, doc_len) = calculate_l3_sparse_index_data(&knowledge);
                            sparse_index.add_document(knowledge.id_hash, terms, doc_len);

                            let data_bytes = knowledge.serialize()
                                .map_err(|e| MemHopError::Serialization(e.to_string()))?;

                            if offset + data_bytes.len() <= mmap.len() {
                                mmap[offset..offset + data_bytes.len()].copy_from_slice(&data_bytes);
                            }

                            updated_ids.push(format!("{:016x}", id_hash));
                        }
                        ImportMode::Skip => {
                            skipped_count += 1;
                        }
                    }
                }
                None => {
                    // Create new knowledge
                    let page_id = allocate_from_free_list(mmap, header)?;
                    let offset = (page_id as usize) * PAGE_SIZE + 32;

                    let knowledge_type = match item.knowledge_type.as_str() {
                        "Factual" => KnowledgeType::Factual,
                        "Procedural" => KnowledgeType::Procedural,
                        "Conceptual" => KnowledgeType::Conceptual,
                        "Contextual" => KnowledgeType::Contextual,
                        _ => KnowledgeType::Factual,
                    };

                    let knowledge = KnowledgeSlot {
                        id_hash,
                        title: item.title.clone(),
                        domain: item.domain.clone(),
                        knowledge_type,
                        text: item.text.clone(),
                        summary: item.summary.clone(),
                        keywords: item.keywords.clone(),
                        edge_count: 0,
                        edge_ptrs: [0; 8],
                        archive_refs: vec![],
                        source_ref: item.source_ref.clone(),
                        created_at: now_ms,
                        updated_at: now_ms,
                        version: 1,
                        importance: 0.5,
                        confidence: 0.8,
                    };

                    let data_bytes = knowledge.serialize()
                        .map_err(|e| MemHopError::Serialization(e.to_string()))?;

                    if offset + data_bytes.len() <= mmap.len() {
                        mmap[offset..offset + data_bytes.len()].copy_from_slice(&data_bytes);
                    }

                    // Add to sparse index using primary + secondary keys
                    let (terms, doc_len) = calculate_l3_sparse_index_data(&knowledge);
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
        Err(MemHopError::ConfigError("Invalid import data for L3".to_string()))
    }
}
