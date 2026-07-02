// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! List and get query interfaces with pagination support.

use crate::file::header::FileHeader;
use crate::index::btree::BTreeIndex;
use crate::query::common::{
    self, format_hash, has_more, matches_keyword, pagination_params, sort_by_score,
};
use crate::query::types::*;
use crate::slot::action_chain::{ActionChainSlot, ChainStatus};
use crate::slot::archive::{ArchiveSlot, ContentType};
use crate::slot::context::ContextSlot;
use crate::slot::context_node::ContextNode;
use crate::slot::hypergraph::HypergraphSlot;
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

// ============================================================================
// Profile Query
// ============================================================================

pub fn get_profile(
    mmap: &MmapMut,
    btree: &BTreeIndex,
) -> Result<Option<ProfileResult>, MemHopError> {
    crate::query::l0_crud::read_profile(mmap, btree)
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
            if let Some(slot_data) = crate::query::slot_io::get_slot_data(data, page_ref) {
                let node = ContextNode::deserialize_slot(slot_data)?;
                Ok(Some(build_engram_result_from_node(mmap, btree, &node)?))
            } else {
                let page_id = crate::query::slot_io::decode_page_id(page_ref);
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
    let data = &mmap[..];
    let page_count = header.page_count;

    let mut all_nodes: Vec<ContextNode> = Vec::new();

    for (_, page_ref) in btree.iter() {
        let page_id = crate::query::slot_io::decode_page_id(*page_ref);
        if page_id >= page_count {
            continue;
        }

        if !is_page_type(data, page_id, PageType::ContextNode) {
            continue;
        }

        if let Some(slot_data) = crate::query::slot_io::get_slot_data(data, *page_ref) {
            if let Ok(node) = ContextNode::deserialize(slot_data) {
                if let Some(min_importance) = query.min_importance {
                    if node.importance < min_importance {
                        continue;
                    }
                }

                if let Some(ref keyword) = query.keyword {
                    let title = load_context_title(mmap, btree, node.context_id)?;
                    if !matches_keyword(&title, keyword) {
                        continue;
                    }
                }

                // L1 ContextNodes always have memory_state="Active".
                if let Some(ref state_filter) = query.state_filter {
                    if state_filter != "Active" {
                        continue;
                    }
                }

                all_nodes.push(node);
            }
        }
    }

    sort_by_score(&mut all_nodes, |node| node.importance);

    let (skip, take) = pagination_params(query.page, query.page_size);
    let total_count = all_nodes.len();
    let paged_engrams: Vec<EngramResult> = all_nodes
        .into_iter()
        .skip(skip)
        .take(take)
        .filter_map(|node| build_engram_result_from_node(mmap, btree, &node).ok())
        .collect();

    Ok(EngramListResult {
        items: paged_engrams,
        total: total_count,
        page: query.page,
        page_size: query.page_size,
        has_more: has_more(skip, take, total_count),
    })
}

fn load_context_title(
    mmap: &MmapMut,
    btree: &BTreeIndex,
    context_id: u64,
) -> Result<String, MemHopError> {
    if let Some(page_ref) = btree.search(context_id) {
        let data = &mmap[..];
        if let Some(slot_data) = crate::query::slot_io::get_slot_data(data, page_ref) {
            if let Ok(ctx) = ContextSlot::deserialize_slot(slot_data) {
                return Ok(ctx.title);
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
        if let Some(slot_data) = crate::query::slot_io::get_slot_data(data, page_ref) {
            match ContextSlot::deserialize_slot(slot_data) {
                Ok(ctx) => {
                    let kw: Vec<String> = ctx
                        .title
                        .split_whitespace()
                        .map(|s| s.to_lowercase())
                        .collect();
                    (ctx.title, ctx.summary, kw)
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
// L2 Context Queries (Topic API)
// ============================================================================

pub fn get_topic(
    mmap: &MmapMut,
    btree: &BTreeIndex,
    id: &str,
) -> Result<Option<TopicDetail>, MemHopError> {
    let data = &mmap[..];
    let id_hash = common::parse_id_to_hash(id);

    match btree.search(id_hash) {
        Some(page_ref) => {
            if let Some(slot_data) = crate::query::slot_io::get_slot_data(data, page_ref) {
                let ctx = ContextSlot::deserialize_slot(slot_data)?;
                Ok(Some(convert_context_to_detail(&ctx)))
            } else {
                let page_id = crate::query::slot_io::decode_page_id(page_ref);
                Err(MemHopError::PageNotFound(page_id))
            }
        }
        None => Ok(None),
    }
}

pub fn list_topics(
    mmap: &MmapMut,
    header: &FileHeader,
    btree: &BTreeIndex,
    query: TopicListQuery,
) -> Result<TopicListResult, MemHopError> {
    let data = &mmap[..];
    let page_count = header.page_count;

    let mut all_contexts: Vec<ContextSlot> = Vec::new();

    for (_, page_ref) in btree.iter() {
        let page_id = crate::query::slot_io::decode_page_id(*page_ref);
        if page_id >= page_count {
            continue;
        }

        if !is_page_type(data, page_id, PageType::Context) {
            continue;
        }

        if let Some(slot_data) = crate::query::slot_io::get_slot_data(data, *page_ref) {
            if let Ok(ctx) = ContextSlot::deserialize(slot_data) {
                if query.active_only && !ctx.is_active {
                    continue;
                }

                if let Some(ref keyword) = query.keyword {
                    if !matches_keyword(&ctx.title, keyword) {
                        continue;
                    }
                }

                all_contexts.push(ctx);
            }
        }
    }

    sort_by_score(&mut all_contexts, |ctx| ctx.activation_score);

    let (skip, take) = pagination_params(query.page, query.page_size);
    let total_count = all_contexts.len();
    let paged_topics: Vec<TopicSummary> = all_contexts
        .into_iter()
        .skip(skip)
        .take(take)
        .map(|ctx| convert_context_to_summary(&ctx))
        .collect();

    Ok(TopicListResult {
        items: paged_topics,
        total: total_count,
        page: query.page,
        page_size: query.page_size,
        has_more: has_more(skip, take, total_count),
    })
}

fn convert_context_to_detail(ctx: &ContextSlot) -> TopicDetail {
    TopicDetail {
        id: format_hash(ctx.id_hash),
        title: ctx.title.clone(),
        summary: ctx.summary.clone(),
        depth: ctx.depth,
        archive_refs: ctx.archive_refs.iter().map(|id| format_hash(*id)).collect(),
        l3_refs: ctx.l3_refs.iter().map(|id| format_hash(*id)).collect(),
        turn_count: ctx.turn_count,
        parent_id: ctx.parent_id.map(format_hash),
        is_active: ctx.is_active,
        importance: ctx.importance,
        activation_score: ctx.activation_score,
        activation_state: format!("{:?}", ctx.activation_state),
        created_at: ctx.created_at,
        updated_at: ctx.updated_at,
        llm_params: Some(LlmParamsDto {
            temperature: ctx.llm_params.temperature,
            top_p: ctx.llm_params.top_p,
            presence_penalty: ctx.llm_params.presence_penalty,
            frequency_penalty: ctx.llm_params.frequency_penalty,
        }),
    }
}

fn convert_context_to_summary(ctx: &ContextSlot) -> TopicSummary {
    TopicSummary {
        id: format_hash(ctx.id_hash),
        title: ctx.title.clone(),
        depth: ctx.depth,
        archive_count: ctx.archive_refs.len(),
        turn_count: ctx.turn_count,
        is_active: ctx.is_active,
        updated_at: ctx.updated_at,
    }
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
            if let Some(slot_data) = crate::query::slot_io::get_slot_data(data, page_ref) {
                if let Ok(archive) = ArchiveSlot::deserialize(slot_data) {
                    let src = archive.request_source();
                    return Ok(Some(Archive {
                        id: format_hash(archive.id_hash),
                        content: archive.content,
                        content_type: content_type_to_string(archive.content_type),
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
    let data = &mmap[..];
    let page_count = header.page_count;

    let mut all_archives: Vec<ArchiveSlot> = Vec::new();

    for (_, page_ref) in btree.iter() {
        let page_id = crate::query::slot_io::decode_page_id(*page_ref);
        if page_id >= page_count {
            continue;
        }

        if !is_page_type(data, page_id, PageType::Archive) {
            continue;
        }

        if let Some(slot_data) = crate::query::slot_io::get_slot_data(data, *page_ref) {
            if let Ok(archive) = ArchiveSlot::deserialize(slot_data) {
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

                if let Some(ref ct) = query.content_type {
                    let archive_ct = content_type_to_string(archive.content_type);
                    if !archive_ct.eq_ignore_ascii_case(ct) {
                        continue;
                    }
                }

                if !filter(&archive) {
                    continue;
                }

                all_archives.push(archive);
            }
        }
    }

    all_archives.sort_by_key(|b| std::cmp::Reverse(b.created_at));

    let (skip, take) = pagination_params(query.page, query.page_size);
    let total_count = all_archives.len();
    let paged_archives: Vec<Archive> = all_archives
        .into_iter()
        .skip(skip)
        .take(take)
        .map(|a| {
            let src = a.request_source();
            Archive {
                id: format_hash(a.id_hash),
                content: a.content,
                content_type: content_type_to_string(a.content_type),
                source_ref: None,
                topic_id: Some(format_hash(a.context_id)),
                engram_ids: vec![],
                created_at: a.created_at,
                source_agent: src.source_agent,
                source_platform: src.source_platform,
            }
        })
        .collect();

    Ok(ArchiveListResult {
        items: paged_archives,
        total: total_count,
        page: query.page,
        page_size: query.page_size,
        has_more: has_more(skip, take, total_count),
    })
}

fn content_type_to_string(ct: ContentType) -> String {
    match ct {
        ContentType::Text => "text".to_string(),
        ContentType::Image => "image".to_string(),
        ContentType::Video => "video".to_string(),
        ContentType::Document => "document".to_string(),
        ContentType::Audio => "audio".to_string(),
        ContentType::Code => "code".to_string(),
        ContentType::Other => "other".to_string(),
    }
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
    let data = &mmap[..];
    let page_count = header.page_count;

    let mut all_chains: Vec<ActionChainSlot> = Vec::new();

    for (_, page_ref) in btree.iter() {
        let page_id = crate::query::slot_io::decode_page_id(*page_ref);
        if page_id >= page_count {
            continue;
        }

        if !is_page_type(data, page_id, PageType::ActionChain) {
            continue;
        }

        if let Some(slot_data) = crate::query::slot_io::get_slot_data(data, *page_ref) {
            if let Ok(chain) = ActionChainSlot::deserialize(slot_data) {
                if let Some(ref status_filter) = query.status_filter {
                    let status_str = match chain.status {
                        ChainStatus::Active => "active",
                        ChainStatus::Deprecated => "deprecated",
                        ChainStatus::Draft => "draft",
                    };
                    if status_str != status_filter {
                        continue;
                    }
                }

                if let Some(min_trigger_count) = query.min_trigger_count {
                    if chain.trigger_count < min_trigger_count {
                        continue;
                    }
                }

                if let Some(ref keyword) = query.keyword {
                    if !matches_keyword(&chain.title, keyword) {
                        continue;
                    }
                }

                all_chains.push(chain);
            }
        }
    }

    all_chains.sort_by_key(|b| std::cmp::Reverse(b.trigger_count));

    let (skip, take) = pagination_params(query.page, query.page_size);
    let total_count = all_chains.len();
    let paged_crystals: Vec<CrystalSummary> = all_chains
        .into_iter()
        .skip(skip)
        .take(take)
        .map(|c| CrystalSummary {
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
        .collect();

    Ok(CrystalListResult {
        items: paged_crystals,
        total: total_count,
        page: query.page,
        page_size: query.page_size,
        has_more: has_more(skip, take, total_count),
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
    let data = &mmap[..];
    let page_count = header.page_count;

    let mut all_slots: Vec<HypergraphSlot> = Vec::new();

    for (_, page_ref) in btree.iter() {
        let page_id = crate::query::slot_io::decode_page_id(*page_ref);
        if page_id >= page_count {
            continue;
        }

        let hdr_offset = (page_id as usize) * crate::util::PAGE_SIZE;
        if hdr_offset + 32 <= data.len() {
            let mut hdr_bytes = [0u8; 32];
            hdr_bytes.copy_from_slice(&data[hdr_offset..hdr_offset + 32]);
            if let Ok(page_hdr) = crate::file::page::PageHeader::from_bytes(&hdr_bytes) {
                if page_hdr.page_type != PageType::HypergraphSlot.to_u16() {
                    continue;
                }
            } else {
                continue;
            }
        } else {
            continue;
        }

        if let Some(slot_data) = crate::query::slot_io::get_slot_data(data, *page_ref) {
            if let Ok(slot) = HypergraphSlot::deserialize(slot_data) {
                if let Some(ref keyword) = query.keyword {
                    if !matches_keyword(&slot.name, keyword) {
                        continue;
                    }
                }

                // Note: domain_filter and knowledge_type filters are not directly
                // applicable to HypergraphSlot (no domain/type fields yet).
                // These can be added when the slot schema is extended.
                // For now, if filters are provided, we skip them gracefully.

                all_slots.push(slot);
            }
        }
    }

    all_slots.sort_by_key(|s| std::cmp::Reverse(s.updated_at));

    let (skip, take) = pagination_params(query.page, query.page_size);
    let total_count = all_slots.len();
    let paged_items: Vec<KnowledgeSummary> = all_slots
        .into_iter()
        .skip(skip)
        .take(take)
        .map(|s| convert_hypergraph_to_summary(&s))
        .collect();

    Ok(KnowledgeListResult {
        items: paged_items,
        total: total_count,
        page: query.page,
        page_size: query.page_size,
        has_more: has_more(skip, take, total_count),
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
