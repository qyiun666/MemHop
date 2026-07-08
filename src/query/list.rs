// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! List and get query interfaces with pagination support.

use crate::file::header::FileHeader;
use crate::index::btree::BTreeIndex;
use crate::layers::action_chain::{ActionChainSlot, ChainStatus};
use crate::layers::archive::ArchiveSlot;
use crate::layers::context::ContextSlot;
use crate::layers::context_node::ContextNode;
use crate::layers::hypergraph::HypergraphSlot;
use crate::query::types::*;
use crate::shared::common::{
    self, format_hash, has_more, matches_keyword, pagination_params, sort_by_score,
};
use crate::util::{PageType, PAGE_SIZE};

/// Check if a page has the expected page_type.
#[inline]
fn is_page_type(data: &[u8], page_id: u32, expected: PageType) -> bool {
    let offset = (page_id as usize) * PAGE_SIZE + 4; // page_type is at offset 4
    if offset + 2 > data.len() {
        return false;
    }
    let pt = u16::from_le_bytes([data[offset], data[offset + 1]]);
    pt == expected.to_u16()
}
use crate::MemHopError;
use memmap2::MmapMut;

/// Generic btree-backed slot listing with page-type filtering, sorting and pagination.
#[allow(clippy::too_many_arguments)]
fn list_slots<T, F, S, M, R>(
    mmap: &MmapMut,
    header: &FileHeader,
    btree: &BTreeIndex,
    page_type: PageType,
    page: usize,
    page_size: usize,
    deserialize: impl Fn(&[u8]) -> Option<T>,
    mut filter: F,
    mut sort_fn: S,
    map_fn: M,
) -> (Vec<R>, usize, bool)
where
    F: FnMut(&T) -> bool,
    S: FnMut(&mut Vec<T>),
    M: FnMut(T) -> Option<R>,
{
    let data = &mmap[..];
    let page_count = header.page_count;

    let mut all_items: Vec<T> = Vec::new();

    for (_, page_ref) in btree.iter_unsorted() {
        let page_id = crate::shared::slot_io::decode_page_id(*page_ref);
        if page_id >= page_count {
            continue;
        }

        if !is_page_type(data, page_id, page_type) {
            continue;
        }

        if let Some(slot_data) = crate::shared::slot_io::get_slot_data(data, *page_ref) {
            if let Some(item) = deserialize(slot_data) {
                if filter(&item) {
                    all_items.push(item);
                }
            }
        }
    }

    sort_fn(&mut all_items);

    let (skip, take) = pagination_params(page, page_size);
    let total_count = all_items.len();
    let paged_items: Vec<R> = all_items
        .into_iter()
        .skip(skip)
        .take(take)
        .filter_map(map_fn)
        .collect();

    (paged_items, total_count, has_more(skip, take, total_count))
}

// ============================================================================
// L1 ContextNode Queries (Engram API)
// ============================================================================

/// Get single L1 node by ID (text/summary read from linked L2)
pub fn get_engram(
    mmap: &MmapMut,
    btree: &BTreeIndex,
    id: &str,
) -> Result<Option<EngramResult>, MemHopError> {
    let data = &mmap[..];
    let id_hash = common::parse_id_to_hash(id);

    match btree.search(id_hash) {
        Some(page_ref) => {
            if let Some(slot_data) = crate::shared::slot_io::get_slot_data(data, page_ref) {
                let node = ContextNode::deserialize_slot(slot_data)?;
                Ok(Some(build_engram_result_from_node(mmap, btree, &node)?))
            } else {
                let page_id = crate::shared::slot_io::decode_page_id(page_ref);
                Err(MemHopError::PageNotFound(page_id))
            }
        }
        None => Ok(None),
    }
}

/// List L1 nodes with pagination and filtering
pub fn list_engrams(
    mmap: &MmapMut,
    header: &FileHeader,
    btree: &BTreeIndex,
    query: EngramListQuery,
) -> Result<EngramListResult, MemHopError> {
    let (items, total, has_more) = list_slots(
        mmap,
        header,
        btree,
        PageType::ContextNode,
        query.page,
        query.page_size,
        |slot_data| ContextNode::deserialize(slot_data).ok(),
        |node| {
            if let Some(min_importance) = query.min_importance {
                if node.importance < min_importance {
                    return false;
                }
            }

            if let Some(ref keyword) = query.keyword {
                if let Ok(title) = load_context_title(mmap, btree, node.context_id) {
                    if !matches_keyword(&title, keyword) {
                        return false;
                    }
                }
            }

            // L1 ContextNodes always have memory_state="Active".
            if let Some(ref state_filter) = query.state_filter {
                if state_filter != "Active" {
                    return false;
                }
            }

            true
        },
        |nodes| sort_by_score(nodes, |node| node.importance),
        |node| build_engram_result_from_node(mmap, btree, &node).ok(),
    );

    Ok(EngramListResult {
        items,
        total,
        page: query.page,
        page_size: query.page_size,
        has_more,
    })
}

fn load_context_title(
    mmap: &MmapMut,
    btree: &BTreeIndex,
    context_id: u64,
) -> Result<String, MemHopError> {
    if let Some(page_ref) = btree.search(context_id) {
        let data = &mmap[..];
        if let Some(slot_data) = crate::shared::slot_io::get_slot_data(data, page_ref) {
            if let Ok(ctx) = ContextSlot::deserialize_slot(slot_data) {
                return Ok(ctx.user_keywords.join(", "));
            }
        }
    }
    Ok(String::new())
}

fn build_engram_result_from_node(
    mmap: &MmapMut,
    btree: &BTreeIndex,
    node: &ContextNode,
) -> Result<EngramResult, MemHopError> {
    let (text, summary, keywords) = if let Some(page_ref) = btree.search(node.context_id) {
        let data = &mmap[..];
        if let Some(slot_data) = crate::shared::slot_io::get_slot_data(data, page_ref) {
            match ContextSlot::deserialize_slot(slot_data) {
                Ok(ctx) => {
                    let kw = crate::index::sparse::tokenize(&ctx.user_keywords.join(", "));
                    (ctx.user_keywords.join(", "), ctx.fused_summary, kw)
                }
                Err(_) => (String::new(), None, vec![]),
            }
        } else {
            (String::new(), None, vec![])
        }
    } else {
        (String::new(), None, vec![])
    };

    Ok(EngramResult {
        id: format_hash(node.id_hash),
        text,
        summary,
        keywords,
        created_at: node.created_at,
        updated_at: node.updated_at,
        memory_state: "Active".to_string(),
        importance: node.importance,
        source_type: "Agent".to_string(),
        edge_count: node.edge_ptrs.len(),
        associated_topics: vec![format_hash(node.context_id)],
    })
}

// ============================================================================
// Archive Queries
// ============================================================================

pub fn get_archive(
    mmap: &MmapMut,
    btree: &BTreeIndex,
    id: &str,
) -> Result<Option<Archive>, MemHopError> {
    let id_hash = common::parse_id_to_hash(id);
    let data: &[u8] = &mmap[..];

    match btree.search(id_hash) {
        Some(page_ref) => {
            if let Some(slot_data) = crate::shared::slot_io::get_slot_data(data, page_ref) {
                if let Ok(archive) = ArchiveSlot::deserialize(slot_data) {
                    let src = archive.request_source();
                    return Ok(Some(Archive {
                        id: format_hash(archive.id_hash),
                        content: archive.content,
                        content_type: archive.content_type.as_str().to_string(),
                        source_ref: None,
                        topic_id: Some(format_hash(archive.context_id)),
                        engram_ids: vec![],
                        created_at: archive.created_at,
                        source_agent: src.source_agent,
                        source_platform: src.source_platform,
                    }));
                }
            }
            Ok(None)
        }
        None => Ok(None),
    }
}

pub fn list_archives_by_topic(
    mmap: &MmapMut,
    header: &FileHeader,
    btree: &BTreeIndex,
    topic_id: &str,
    query: ArchivePageQuery,
) -> Result<ArchiveListResult, MemHopError> {
    let topic_hash = common::parse_id_to_hash(topic_id);
    list_archives_with_filter(mmap, header, btree, query, |archive| {
        archive.context_id == topic_hash
    })
}

pub fn list_archives_by_nodes(
    mmap: &MmapMut,
    header: &FileHeader,
    btree: &BTreeIndex,
    node_ids: &[String],
    query: ArchivePageQuery,
) -> Result<ArchiveListResult, MemHopError> {
    let node_hashes: Vec<u64> = node_ids
        .iter()
        .map(|id| common::parse_id_to_hash(id))
        .collect();
    list_archives_with_filter(mmap, header, btree, query, |archive| {
        node_hashes.contains(&archive.context_id)
    })
}

pub fn list_all_archives(
    mmap: &MmapMut,
    header: &FileHeader,
    btree: &BTreeIndex,
    query: ArchivePageQuery,
) -> Result<ArchiveListResult, MemHopError> {
    list_archives_with_filter(mmap, header, btree, query, |_| true)
}

fn list_archives_with_filter<F>(
    mmap: &MmapMut,
    header: &FileHeader,
    btree: &BTreeIndex,
    query: ArchivePageQuery,
    filter: F,
) -> Result<ArchiveListResult, MemHopError>
where
    F: Fn(&ArchiveSlot) -> bool,
{
    let (items, total, has_more) = list_slots(
        mmap,
        header,
        btree,
        PageType::Archive,
        query.page,
        query.page_size,
        |slot_data| ArchiveSlot::deserialize(slot_data).ok(),
        |archive| {
            if let Some(start_time) = query.start_time {
                if archive.created_at < start_time {
                    return false;
                }
            }

            if let Some(end_time) = query.end_time {
                if archive.created_at > end_time {
                    return false;
                }
            }

            if let Some(ref ct) = query.content_type {
                let archive_ct = archive.content_type.as_str();
                if !archive_ct.eq_ignore_ascii_case(ct) {
                    return false;
                }
            }

            filter(archive)
        },
        |archives| archives.sort_by_key(|a| std::cmp::Reverse(a.created_at)),
        |a| {
            let src = a.request_source();
            Some(Archive {
                id: format_hash(a.id_hash),
                content: a.content,
                content_type: a.content_type.as_str().to_string(),
                source_ref: None,
                topic_id: Some(format_hash(a.context_id)),
                engram_ids: vec![],
                created_at: a.created_at,
                source_agent: src.source_agent,
                source_platform: src.source_platform,
            })
        },
    );

    Ok(ArchiveListResult {
        items,
        total,
        page: query.page,
        page_size: query.page_size,
        has_more,
    })
}

// ============================================================================
// L5 ActionChain Queries (Crystal API)
// ============================================================================

pub fn list_crystals(
    mmap: &MmapMut,
    header: &FileHeader,
    btree: &BTreeIndex,
    query: CrystalListQuery,
) -> Result<CrystalListResult, MemHopError> {
    let (items, total, has_more) = list_slots(
        mmap,
        header,
        btree,
        PageType::ActionChain,
        query.page,
        query.page_size,
        |slot_data| ActionChainSlot::deserialize(slot_data).ok(),
        |chain| {
            if let Some(ref status_filter) = query.status_filter {
                let status_str = match chain.status {
                    ChainStatus::Active => "active",
                    ChainStatus::Deprecated => "deprecated",
                    ChainStatus::Draft => "draft",
                };
                if status_str != status_filter {
                    return false;
                }
            }

            if let Some(min_trigger_count) = query.min_trigger_count {
                if chain.trigger_count < min_trigger_count {
                    return false;
                }
            }

            if let Some(ref keyword) = query.keyword {
                if !matches_keyword(&chain.title, keyword) {
                    return false;
                }
            }

            true
        },
        |chains| chains.sort_by_key(|c| std::cmp::Reverse(c.trigger_count)),
        |c| {
            Some(CrystalSummary {
                id: format_hash(c.id_hash),
                title: c.title,
                condition: c.trigger,
                status: match c.status {
                    ChainStatus::Active => "active".to_string(),
                    ChainStatus::Deprecated => "deprecated".to_string(),
                    ChainStatus::Draft => "draft".to_string(),
                },
                trigger_count: c.trigger_count,
                success_rate: c.success_rate,
                last_triggered: if c.last_triggered > 0 {
                    Some(c.last_triggered)
                } else {
                    None
                },
                created_at: c.created_at,
            })
        },
    );

    Ok(CrystalListResult {
        items,
        total,
        page: query.page,
        page_size: query.page_size,
        has_more,
    })
}

// ============================================================================
// L3 Hypergraph Queries (Knowledge API)
// ============================================================================

pub fn list_knowledge(
    mmap: &MmapMut,
    header: &FileHeader,
    btree: &BTreeIndex,
    query: KnowledgeListQuery,
) -> Result<KnowledgeListResult, MemHopError> {
    let (items, total, has_more) = list_slots(
        mmap,
        header,
        btree,
        PageType::HypergraphSlot,
        query.page,
        query.page_size,
        |slot_data| HypergraphSlot::deserialize(slot_data).ok(),
        |slot| {
            if let Some(ref keyword) = query.keyword {
                if !matches_keyword(&slot.name, keyword) {
                    return false;
                }
            }

            true
        },
        |slots| slots.sort_by_key(|s| std::cmp::Reverse(s.updated_at)),
        |s| Some(convert_hypergraph_to_summary(&s)),
    );

    Ok(KnowledgeListResult {
        items,
        total,
        page: query.page,
        page_size: query.page_size,
        has_more,
    })
}

fn convert_hypergraph_to_summary(slot: &HypergraphSlot) -> KnowledgeSummary {
    KnowledgeSummary {
        id: format_hash(slot.id_hash),
        title: slot.name.clone(),
        domain: slot.source.domain_name().to_string(),
        knowledge_type: "Generic".to_string(),
        importance: 0.5,
        confidence: 1.0,
        updated_at: slot.updated_at,
    }
}
