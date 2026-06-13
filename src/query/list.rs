//! List query implementations for MemHop
//!
//! Implements L0-L5 list and get query interfaces with pagination support.

use crate::file::header::FileHeader;
use crate::index::btree::BTreeIndex;
use crate::query::types::*;
use crate::slot::archive::ArchiveSlot;
use crate::slot::crystal::CrystalSlot;
use crate::slot::engram::EngramSlot;
use crate::slot::knowledge::KnowledgeSlot;
use crate::slot::profile::ProfileSlot;
use crate::slot::topic::TopicSlot;
use crate::util::hash_id;
use crate::MemHopError;
use memmap2::MmapMut;

const PAGE_SIZE: usize = 4096;

/// Parse ID string to u64 hash
/// Supports both hex-encoded hashes (16 chars) and raw strings
fn parse_id_to_hash(id: &str) -> u64 {
    if id.len() == 16 {
        // Likely a hex-encoded hash (e.g., "1a2b3c4d5e6f7890")
        u64::from_str_radix(id, 16).unwrap_or_else(|_| hash_id(id))
    } else {
        hash_id(id)
    }
}

// ============================================================================
// L0 Profile Query
// ============================================================================

/// Get L0 profile
pub fn get_l0_profile(
    mmap: &MmapMut,
    btree: &BTreeIndex,
) -> Result<Option<L0Profile>, MemHopError> {
    // Delegate to unified L0 CRUD implementation
    crate::query::l0_crud::read_profile(mmap, btree)
}

// ============================================================================
// L1 Engram Queries
// ============================================================================

/// Get single L1 engram by ID
pub fn get_l1_engram(
    mmap: &MmapMut,
    btree: &BTreeIndex,
    id: &str,
) -> Result<Option<L1Engram>, MemHopError> {
    let data = &mmap[..];
    let id_hash = parse_id_to_hash(id);

    match btree.search(id_hash) {
        Some(page_ref) => {
            if let Some(slot_data) = crate::query::slot_io::get_slot_data(data, page_ref) {
                let engram = EngramSlot::deserialize(slot_data)
                    .map_err(|e| MemHopError::Serialization(e.to_string()))?;
                Ok(Some(convert_engram_to_detail(&engram)))
            } else {
                let page_id = crate::query::slot_io::decode_page_id(page_ref);
                Err(MemHopError::PageNotFound(page_id))
            }
        }
        None => Ok(None),
    }
}

/// List L1 engrams with pagination and filtering
pub fn list_l1_engrams(
    mmap: &MmapMut,
    header: &FileHeader,
    btree: &BTreeIndex,
    query: L1ListQuery,
) -> Result<L1ListResult, MemHopError> {
    let data = &mmap[..];
    let page_count = header.page_count;

    // Collect all L1 engrams
    let mut all_engrams: Vec<EngramSlot> = Vec::new();

    for (_, page_ref) in btree.iter() {
        let page_id = crate::query::slot_io::decode_page_id(*page_ref);
        if page_id >= page_count {
            continue;
        }

        if let Some(slot_data) = crate::query::slot_io::get_slot_data(data, *page_ref) {
            // Try to deserialize as EngramSlot
            if let Ok(engram) = EngramSlot::deserialize(slot_data) {
                // Apply filters
                if let Some(ref state_filter) = query.state_filter {
                    let state_str = match engram.memory_state {
                        1 => "Active",
                        2 => "Latent",
                        _ => "Dormant",
                    };
                    if state_str != state_filter {
                        continue;
                    }
                }

                if let Some(min_importance) = query.min_importance {
                    if engram.importance < min_importance {
                        continue;
                    }
                }

                if let Some(ref keyword) = query.keyword {
                    let keyword_lower = keyword.to_lowercase();
                    let text_lower = engram.text.to_lowercase();
                    if !text_lower.contains(&keyword_lower) {
                        continue;
                    }
                }

                all_engrams.push(engram);
            }
        }
    }

    // Sort by importance (descending)
    all_engrams.sort_by(|a, b| b.importance.partial_cmp(&a.importance).unwrap_or(std::cmp::Ordering::Equal));

    // Pagination
    let total_count = all_engrams.len();
    let skip = (query.page.saturating_sub(1)) * query.page_size;
    let take = query.page_size;
    let paged_engrams: Vec<L1Engram> = all_engrams
        .into_iter()
        .skip(skip)
        .take(take)
        .map(|e| convert_engram_to_detail(&e))
        .collect();

    Ok(L1ListResult {
        items: paged_engrams,
        total: total_count,
        page: query.page,
        page_size: query.page_size,
        has_more: skip + take < total_count,
    })
}

fn convert_engram_to_detail(engram: &EngramSlot) -> L1Engram {
    let state_str = match engram.memory_state {
        1 => "Active".to_string(),
        2 => "Latent".to_string(),
        _ => "Dormant".to_string(),
    };

    let source_type_str = match engram.source_type {
        0 => "User".to_string(),
        1 => "Agent".to_string(),
        2 => "System".to_string(),
        _ => "Unknown".to_string(),
    };

    L1Engram {
        id: format!("{:016x}", engram.id_hash),
        text: engram.text.clone(),
        summary: engram.summary.clone(),
        keywords: engram.keywords.clone(),
        created_at: engram.created_at,
        updated_at: engram.updated_at,
        memory_state: state_str,
        importance: engram.importance,
        source_type: source_type_str,
        edge_count: engram.edge_count as usize,
        // NOTE: associated_topics requires querying hyperedge relationships.
        // This is computationally expensive and not critical for most use cases.
        // Reserved for future optimization if needed.
        associated_topics: vec![], // TODO(v0.41+): Query from hyperedges if performance allows
    }
}

// ============================================================================
// L2 Topic Queries
// ============================================================================

/// Get single L2 topic by ID
pub fn get_l2_topic(
    mmap: &MmapMut,
    btree: &BTreeIndex,
    id: &str,
) -> Result<Option<L2TopicDetail>, MemHopError> {
    let data = &mmap[..];
    
    // Try to parse ID as hex hash first, fallback to hash_id()
    let id_hash = if id.len() == 16 {
        // Likely a hex-encoded hash (e.g., "1a2b3c4d5e6f7890")
        u64::from_str_radix(id, 16).unwrap_or_else(|_| hash_id(id))
    } else {
        hash_id(id)
    };

    match btree.search(id_hash) {
        Some(page_ref) => {
            if let Some(slot_data) = crate::query::slot_io::get_slot_data(data, page_ref) {
                let topic = TopicSlot::deserialize(slot_data)
                    .map_err(|e| MemHopError::Serialization(e.to_string()))?;
                Ok(Some(convert_topic_to_detail(&topic)))
            } else {
                let page_id = crate::query::slot_io::decode_page_id(page_ref);
                Err(MemHopError::PageNotFound(page_id))
            }
        }
        None => Ok(None),
    }
}

/// List L2 topics with pagination and filtering
pub fn list_l2_topics(
    mmap: &MmapMut,
    header: &FileHeader,
    btree: &BTreeIndex,
    query: L2ListQuery,
) -> Result<L2ListResult, MemHopError> {
    let data = &mmap[..];
    let page_count = header.page_count;

    let mut all_topics: Vec<TopicSlot> = Vec::new();

    for (_, page_ref) in btree.iter() {
        let page_id = crate::query::slot_io::decode_page_id(*page_ref);
        if page_id >= page_count {
            continue;
        }

        if let Some(slot_data) = crate::query::slot_io::get_slot_data(data, *page_ref) {
            if let Ok(topic) = TopicSlot::deserialize(slot_data) {
                // Apply filters
                if query.active_only && !topic.is_active {
                    continue;
                }

                if let Some(ref keyword) = query.keyword {
                    let keyword_lower = keyword.to_lowercase();
                    let title_lower = topic.title.to_lowercase();
                    if !title_lower.contains(&keyword_lower) {
                        continue;
                    }
                }

                all_topics.push(topic);
            }
        }
    }

    // Sort by activation_score (descending)
    all_topics.sort_by(|a, b| b.activation_score.partial_cmp(&a.activation_score).unwrap_or(std::cmp::Ordering::Equal));

    // Pagination
    let total_count = all_topics.len();
    let skip = (query.page.saturating_sub(1)) * query.page_size;
    let take = query.page_size;
    let paged_topics: Vec<L2TopicSummary> = all_topics
        .into_iter()
        .skip(skip)
        .take(take)
        .map(|t| convert_topic_to_summary(&t))
        .collect();

    Ok(L2ListResult {
        items: paged_topics,
        total: total_count,
        page: query.page,
        page_size: query.page_size,
        has_more: skip + take < total_count,
    })
}

fn convert_topic_to_detail(topic: &TopicSlot) -> L2TopicDetail {
    L2TopicDetail {
        id: format!("{:016x}", topic.id_hash),
        title: topic.title.clone(),
        summary: topic.summary.clone(),
        node_ids: topic.node_ids.iter().map(|id| format!("{:016x}", id)).collect(),
        l3_refs: topic.l3_refs.iter().map(|id| format!("{:016x}", id)).collect(),
        l4_refs: topic.l4_refs.iter().map(|id| format!("{:016x}", id)).collect(),
        parent_id: topic.parent_id.map(|id| format!("{:016x}", id)),
        is_active: topic.is_active,
        importance: topic.importance,
        activation_score: topic.activation_score,
        created_at: topic.created_at,
        updated_at: topic.updated_at,
    }
}

fn convert_topic_to_summary(topic: &TopicSlot) -> L2TopicSummary {
    L2TopicSummary {
        id: format!("{:016x}", topic.id_hash),
        title: topic.title.clone(),
        node_count: topic.node_ids.len(),
        is_active: topic.is_active,
        updated_at: topic.updated_at,
    }
}

// ============================================================================
// L3 Knowledge Queries
// ============================================================================

/// Get single L3 knowledge by ID
pub fn get_l3_domain(
    mmap: &MmapMut,
    btree: &BTreeIndex,
    id: &str,
) -> Result<Option<L3DomainDetail>, MemHopError> {
    let data = &mmap[..];
    let id_hash = parse_id_to_hash(id);

    match btree.search(id_hash) {
        Some(page_ref) => {
            if let Some(slot_data) = crate::query::slot_io::get_slot_data(data, page_ref) {
                let knowledge = KnowledgeSlot::deserialize(slot_data)
                    .map_err(|e| MemHopError::Serialization(e.to_string()))?;
                Ok(Some(convert_knowledge_to_detail(&knowledge)))
            } else {
                let page_id = crate::query::slot_io::decode_page_id(page_ref);
                Err(MemHopError::PageNotFound(page_id))
            }
        }
        None => Ok(None),
    }
}

/// List L3 knowledge domains with pagination and filtering
pub fn list_l3_domains(
    mmap: &MmapMut,
    header: &FileHeader,
    btree: &BTreeIndex,
    query: L3ListQuery,
) -> Result<L3ListResult, MemHopError> {
    let data = &mmap[..];
    let page_count = header.page_count;

    let mut all_knowledge: Vec<KnowledgeSlot> = Vec::new();

    for (_, page_ref) in btree.iter() {
        let page_id = crate::query::slot_io::decode_page_id(*page_ref);
        if page_id >= page_count {
            continue;
        }

        if let Some(slot_data) = crate::query::slot_io::get_slot_data(data, *page_ref) {
            if let Ok(knowledge) = KnowledgeSlot::deserialize(slot_data) {
                // Apply filters
                if let Some(ref domain_filter) = query.domain_filter {
                    if knowledge.domain != *domain_filter {
                        continue;
                    }
                }

                if let Some(ref type_filter) = query.knowledge_type {
                    let type_str = format!("{:?}", knowledge.knowledge_type);
                    if type_str != *type_filter {
                        continue;
                    }
                }

                if let Some(ref keyword) = query.keyword {
                    let keyword_lower = keyword.to_lowercase();
                    let title_lower = knowledge.title.to_lowercase();
                    if !title_lower.contains(&keyword_lower) {
                        continue;
                    }
                }

                all_knowledge.push(knowledge);
            }
        }
    }

    // Sort by importance (descending)
    all_knowledge.sort_by(|a, b| b.importance.partial_cmp(&a.importance).unwrap_or(std::cmp::Ordering::Equal));

    // Pagination
    let total_count = all_knowledge.len();
    let skip = (query.page.saturating_sub(1)) * query.page_size;
    let take = query.page_size;
    let paged_knowledge: Vec<L3DomainSummary> = all_knowledge
        .into_iter()
        .skip(skip)
        .take(take)
        .map(|k| convert_knowledge_to_summary(&k))
        .collect();

    Ok(L3ListResult {
        items: paged_knowledge,
        total: total_count,
        page: query.page,
        page_size: query.page_size,
        has_more: skip + take < total_count,
    })
}

fn convert_knowledge_to_detail(knowledge: &KnowledgeSlot) -> L3DomainDetail {
    L3DomainDetail {
        id: format!("{:016x}", knowledge.id_hash),
        title: knowledge.title.clone(),
        domain: knowledge.domain.clone(),
        knowledge_type: format!("{:?}", knowledge.knowledge_type),
        text: knowledge.text.clone(),
        summary: knowledge.summary.clone(),
        keywords: knowledge.keywords.clone(),
        edge_ptrs: knowledge.edge_ptrs.iter().map(|ptr| format!("{:016x}", ptr)).collect(),
        archive_refs: knowledge.archive_refs.iter().map(|id| format!("{:016x}", id)).collect(),
        source_ref: knowledge.source_ref.clone(),
        created_at: knowledge.created_at,
        updated_at: knowledge.updated_at,
        importance: knowledge.importance,
        confidence: knowledge.confidence,
    }
}

fn convert_knowledge_to_summary(knowledge: &KnowledgeSlot) -> L3DomainSummary {
    L3DomainSummary {
        id: format!("{:016x}", knowledge.id_hash),
        title: knowledge.title.clone(),
        domain: knowledge.domain.clone(),
        knowledge_type: format!("{:?}", knowledge.knowledge_type),
        updated_at: knowledge.updated_at,
        importance: knowledge.importance,
        confidence: knowledge.confidence,
    }
}

// ============================================================================
// L4 Archive Queries
// ============================================================================

/// List L4 archives by topic ID
pub fn list_l4_by_topic(
    mmap: &MmapMut,
    header: &FileHeader,
    btree: &BTreeIndex,
    topic_id: &str,
    query: L4PageQuery,
) -> Result<L4ListResult, MemHopError> {
    let topic_hash = parse_id_to_hash(topic_id);
    list_l4_with_filter(mmap, header, btree, query, |archive| {
        archive.topic_id == topic_hash
    })
}

/// List L4 archives by node IDs
pub fn list_l4_by_nodes(
    mmap: &MmapMut,
    header: &FileHeader,
    btree: &BTreeIndex,
    node_ids: &[String],
    query: L4PageQuery,
) -> Result<L4ListResult, MemHopError> {
    let node_hashes: Vec<u64> = node_ids.iter().map(|id| parse_id_to_hash(id)).collect();
    list_l4_with_filter(mmap, header, btree, query, |archive| {
        node_hashes.contains(&archive.topic_id)
    })
}

/// List all L4 archives
pub fn list_l4_all(
    mmap: &MmapMut,
    header: &FileHeader,
    btree: &BTreeIndex,
    query: L4PageQuery,
) -> Result<L4ListResult, MemHopError> {
    list_l4_with_filter(mmap, header, btree, query, |_| true)
}

fn list_l4_with_filter<F>(
    mmap: &MmapMut,
    header: &FileHeader,
    btree: &BTreeIndex,
    query: L4PageQuery,
    filter: F,
) -> Result<L4ListResult, MemHopError>
where
    F: Fn(&ArchiveSlot) -> bool,
{
    let data = &mmap[..];
    let page_count = header.page_count;

    let mut all_archives: Vec<ArchiveSlot> = Vec::new();

    for (_, page_ref) in btree.iter() {
        let page_id = crate::query::slot_io::decode_page_id(*page_ref);
        if page_id >= page_count {
            continue;
        }

        if let Some(slot_data) = crate::query::slot_io::get_slot_data(data, *page_ref) {
            if let Ok(archive) = ArchiveSlot::deserialize(slot_data) {
            // Apply time range filter
            if let Some(start_time) = query.start_time {
                if archive.created_at < start_time {
                    continue;
                }
            }

            if let Some(end_time) = query.end_time {
                if archive.created_at > end_time {
                    continue;
                }
            }

            // Apply custom filter
            if !filter(&archive) {
                continue;
            }

            all_archives.push(archive);
            }
        }
    }

    // Sort by created_at (descending - newest first)
    all_archives.sort_by(|a, b| b.created_at.cmp(&a.created_at));

    // Pagination
    let total_count = all_archives.len();
    let skip = (query.page.saturating_sub(1)) * query.page_size;
    let take = query.page_size;
    let paged_archives: Vec<L4Archive> = all_archives
        .into_iter()
        .skip(skip)
        .take(take)
        .map(|a| L4Archive {
            id: format!("{:016x}", a.id_hash),
            content: a.content,
            content_type: format!("{:?}", a.content_type),
            // NOTE: source_ref and node_ids require ArchiveSlot schema extension.
            // These fields are reserved for future implementation.
            // Current implementation returns None/empty as placeholders.
            source_ref: None, // TODO(v0.40+): Add source_ref field to ArchiveSlot
            topic_id: Some(format!("{:016x}", a.topic_id)),
            node_ids: vec![], // TODO(v0.40+): Query associated node IDs from hyperedges
            created_at: a.created_at,
        })
        .collect();

    Ok(L4ListResult {
        items: paged_archives,
        total: total_count,
        page: query.page,
        page_size: query.page_size,
        has_more: skip + take < total_count,
    })
}

// ============================================================================
// L5 Crystal Queries
// ============================================================================

/// List L5 crystals/skills with pagination and filtering
pub fn list_l5_skills(
    mmap: &MmapMut,
    header: &FileHeader,
    btree: &BTreeIndex,
    query: L5ListQuery,
) -> Result<L5ListResult, MemHopError> {
    let data = &mmap[..];
    let page_count = header.page_count;

    let mut all_crystals: Vec<CrystalSlot> = Vec::new();

    for (_, page_ref) in btree.iter() {
        let page_id = crate::query::slot_io::decode_page_id(*page_ref);
        if page_id >= page_count {
            continue;
        }

        if let Some(slot_data) = crate::query::slot_io::get_slot_data(data, *page_ref) {
            if let Ok(crystal) = CrystalSlot::deserialize(slot_data) {
            // Apply status filter
            if let Some(ref status_filter) = query.status_filter {
                let status_str = match crystal.status {
                    crate::slot::crystal::CrystalStatus::Crystallized => "active",
                    crate::slot::crystal::CrystalStatus::NotCrystallized => "inactive",
                };
                if status_str != status_filter {
                    continue;
                }
            }

            // Apply trigger count filter
            if let Some(min_trigger_count) = query.min_trigger_count {
                if crystal.trigger_count < min_trigger_count {
                    continue;
                }
            }

            // Apply keyword filter
            if let Some(ref keyword) = query.keyword {
                let keyword_lower = keyword.to_lowercase();
                let title_lower = crystal.title.to_lowercase();
                if !title_lower.contains(&keyword_lower) {
                    continue;
                }
            }

            all_crystals.push(crystal);
            }
        }
    }

    // Sort by trigger_count (descending)
    all_crystals.sort_by(|a, b| b.trigger_count.cmp(&a.trigger_count));

    // Pagination
    let total_count = all_crystals.len();
    let skip = (query.page.saturating_sub(1)) * query.page_size;
    let take = query.page_size;
    let paged_crystals: Vec<L5SkillSummary> = all_crystals
        .into_iter()
        .skip(skip)
        .take(take)
        .map(|c| L5SkillSummary {
            id: format!("{:016x}", c.id_hash),
            title: c.title,
            condition: c.condition,
            status: match c.status {
                crate::slot::crystal::CrystalStatus::Crystallized => "active".to_string(),
                crate::slot::crystal::CrystalStatus::NotCrystallized => "inactive".to_string(),
            },
            trigger_count: c.trigger_count,
            success_rate: c.confidence, // Use confidence as success_rate
            last_triggered: if c.last_triggered > 0 { Some(c.last_triggered) } else { None },
            created_at: c.created_at,
        })
        .collect();

    Ok(L5ListResult {
        items: paged_crystals,
        total: total_count,
        page: query.page,
        page_size: query.page_size,
        has_more: skip + take < total_count,
    })
}
