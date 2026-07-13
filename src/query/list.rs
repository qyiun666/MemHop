// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! List and get query interfaces with pagination support.

use crate::layers::action_chain::{ActionChainSlot, ChainStatus};
use crate::layers::archive::ArchiveSlot;
use crate::layers::hypergraph::HypergraphSlot;
use crate::query::types::*;
use crate::shared::common::{self, format_hash, has_more, matches_keyword, pagination_params};
use crate::storage::record::*;
use crate::storage::StorageEngine;

use crate::MemHopError;

/// Generic engine-backed slot listing with record-type filtering, sorting and pagination.
#[allow(clippy::too_many_arguments)]
fn list_slots<T, F, S, M, R>(
    engine: &StorageEngine,
    record_type: u8,
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
    let mut all_items: Vec<T> = Vec::new();

    for (id_hash, _) in engine.iter_index() {
        let Ok(Some((rt, data))) = engine.read_record(*id_hash) else {
            continue;
        };
        if rt != record_type {
            continue;
        }
        if let Some(item) = deserialize(data) {
            if filter(&item) {
                all_items.push(item);
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
// Archive Queries
// ============================================================================

pub fn list_archives_by_topic(
    engine: &StorageEngine,
    topic_id: &str,
    query: ArchivePageQuery,
) -> Result<ArchiveListResult, MemHopError> {
    let topic_hash = common::parse_id_to_hash(topic_id);
    list_archives_with_filter(engine, query, |archive| archive.context_id == topic_hash)
}

fn list_archives_with_filter<F>(
    engine: &StorageEngine,
    query: ArchivePageQuery,
    filter: F,
) -> Result<ArchiveListResult, MemHopError>
where
    F: Fn(&ArchiveSlot) -> bool,
{
    let (items, total, has_more) = list_slots(
        engine,
        REC_L4_ARCHIVE,
        query.page,
        query.page_size,
        |slot_data| bincode::deserialize::<ArchiveSlot>(slot_data).ok(),
        |archive: &ArchiveSlot| {
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
            Some(Archive {
                id: format_hash(a.id_hash),
                content: a.content,
                content_type: a.content_type.as_str().to_string(),
                source_ref: None,
                topic_id: Some(format_hash(a.context_id)),
                engram_ids: vec![],
                created_at: a.created_at,
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
    engine: &StorageEngine,
    query: CrystalListQuery,
) -> Result<CrystalListResult, MemHopError> {
    let (items, total_count, _has_more) = list_slots(
        engine,
        REC_L5_ACTION_CHAIN,
        query.page as usize,
        query.page_size as usize,
        |slot_data| ActionChainSlot::deserialize(slot_data).ok(),
        |chain: &ActionChainSlot| {
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
        crystals: items,
        total: total_count as u32,
        page: query.page,
    })
}

// ============================================================================
// L3 Hypergraph Queries (Knowledge API)
// ============================================================================

pub fn list_knowledge(
    engine: &StorageEngine,
    query: KnowledgeListQuery,
) -> Result<KnowledgeListResult, MemHopError> {
    let (items, total, has_more) = list_slots(
        engine,
        REC_L3_GRAPH_SLOT,
        query.page,
        query.page_size,
        |slot_data| bincode::deserialize::<HypergraphSlot>(slot_data).ok(),
        |slot: &HypergraphSlot| {
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
