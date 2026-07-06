// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L2 ContextSlot CRUD internal implementation.

use crate::file::header::FileHeader;
use crate::index::btree::BTreeIndex;
use crate::index::sparse::SparseIndex;
use crate::layers::context::{ActivationState, ContextSlot};
use crate::query::search::L1ReverseIndex;
use crate::query::types::{
    MergeResult, TopicDetail, TopicListQuery, TopicListResult, TopicSummary, UpdateL2Fields,
};
use crate::shared::common::{format_hash, now_ms, parse_id_to_hash};
use crate::shared::slot_io::{decode_page_id, get_slot_data};
use crate::util::{PageType, PAGE_SIZE};
use crate::MemHopError;
use memmap2::MmapMut;
use std::collections::HashSet;

// ============================================================================
// Read helpers
// ============================================================================

fn load_context(
    mmap: &MmapMut,
    btree: &BTreeIndex,
    id_hash: u64,
) -> Result<Option<ContextSlot>, MemHopError> {
    match btree.search(id_hash) {
        Some(page_ref) => {
            let data: &[u8] = &mmap[..];
            let slot_data = get_slot_data(data, page_ref)
                .ok_or_else(|| MemHopError::PageNotFound(decode_page_id(page_ref)))?;
            Ok(Some(ContextSlot::deserialize_slot(slot_data)?))
        }
        None => Ok(None),
    }
}

fn to_topic_detail(ctx: &ContextSlot) -> TopicDetail {
    TopicDetail {
        id: format_hash(ctx.id_hash),
        title: ctx.title.clone(),
        summary: ctx.summary.clone(),
        depth: ctx.depth,
        archive_refs: ctx.archive_refs.iter().map(|h| format_hash(*h)).collect(),
        l3_refs: ctx.l3_refs.iter().map(|h| format_hash(*h)).collect(),
        turn_count: ctx.turn_count,
        parent_id: ctx.parent_id.map(format_hash),
        is_active: ctx.is_active,
        importance: ctx.importance,
        activation_score: ctx.activation_score,
        activation_state: format!("{:?}", ctx.activation_state),
        created_at: ctx.created_at,
        updated_at: ctx.updated_at,
        llm_params: Some(ctx.llm_params),
    }
}

fn to_topic_summary(ctx: &ContextSlot) -> TopicSummary {
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
// L2 CRUD
// ============================================================================

/// Get a single L2 context by ID.
pub fn get_l2(
    mmap: &MmapMut,
    btree: &BTreeIndex,
    id: &str,
) -> Result<Option<ContextSlot>, MemHopError> {
    load_context(mmap, btree, parse_id_to_hash(id))
}

/// List L2 contexts with pagination and filtering.
pub fn list_l2(
    mmap: &MmapMut,
    header: &FileHeader,
    btree: &BTreeIndex,
    query: TopicListQuery,
) -> Result<TopicListResult, MemHopError> {
    let data: &[u8] = &mmap[..];
    let mut all: Vec<ContextSlot> = Vec::new();

    for (_, page_ref) in btree.iter_unsorted() {
        let page_id = decode_page_id(*page_ref);
        if page_id >= header.page_count {
            continue;
        }
        if page_type(data, page_id) != Some(PageType::Context as u16) {
            continue;
        }
        if let Some(slot_data) = get_slot_data(data, *page_ref) {
            if let Ok(ctx) = ContextSlot::deserialize_slot(slot_data) {
                if query.active_only && !ctx.is_active {
                    continue;
                }
                if let Some(ref keyword) = query.keyword {
                    if !crate::shared::common::matches_keyword(&ctx.title, keyword) {
                        continue;
                    }
                }
                all.push(ctx);
            }
        }
    }

    crate::shared::common::sort_by_score(&mut all, |ctx| ctx.activation_score);

    let (skip, take) = crate::shared::common::pagination_params(query.page, query.page_size);
    let total = all.len();
    let items: Vec<TopicSummary> = all
        .into_iter()
        .skip(skip)
        .take(take)
        .map(|ctx| to_topic_summary(&ctx))
        .collect();

    Ok(TopicListResult {
        items,
        total,
        page: query.page,
        page_size: query.page_size,
        has_more: crate::shared::common::has_more(skip, take, total),
    })
}

/// Partially update an L2 context.
pub fn update_l2(
    mmap: &mut MmapMut,
    _header: &mut FileHeader,
    btree: &BTreeIndex,
    sparse_index: &mut SparseIndex,
    id: &str,
    fields: UpdateL2Fields,
) -> Result<TopicDetail, MemHopError> {
    let id_hash = parse_id_to_hash(id);
    let page_ref = btree.search(id_hash).ok_or(MemHopError::PageNotFound(0))?;
    let page_id = decode_page_id(page_ref);
    let offset = crate::shared::slot_io::slot_offset(page_id);

    let mut ctx = ContextSlot::deserialize_slot(&mmap[offset..])?;

    let mut title_changed = false;
    if let Some(title) = fields.title {
        if ctx.title != title {
            ctx.title = title;
            title_changed = true;
        }
    }
    if let Some(summary) = fields.summary {
        if ctx.summary.as_ref() != Some(&summary) {
            ctx.summary = Some(summary);
            title_changed = true;
        }
    }
    if let Some(is_active) = fields.is_active {
        ctx.is_active = is_active;
    }
    if let Some(importance) = fields.importance {
        ctx.importance = importance;
    }
    if let Some(activation_score) = fields.activation_score {
        ctx.activation_score = activation_score.clamp(0.0, 1.0);
    }
    if let Some(state) = fields.activation_state {
        ctx.activation_state = parse_activation_state(&state);
    }
    if let Some(l3_refs) = fields.l3_refs {
        ctx.l3_refs = l3_refs.iter().map(|s| parse_id_to_hash(s)).collect();
        ctx.l3_refs.sort();
        ctx.l3_refs.dedup();
    }
    if let Some(llm_params) = fields.llm_params {
        ctx.llm_params = llm_params;
    }

    if title_changed {
        sparse_index.remove_document(ctx.id_hash);
        let (terms, doc_len) =
            crate::shared::common::build_l2_sparse_terms(&ctx.title, &ctx.summary);
        sparse_index.add_document(ctx.id_hash, terms, doc_len);
    }

    ctx.updated_at = now_ms();
    ctx.version += 1;

    let data = ctx
        .serialize()
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;
    if offset + data.len() > mmap.len() {
        return Err(MemHopError::PageNotFound(page_id));
    }
    mmap[offset..offset + data.len()].copy_from_slice(&data);

    Ok(to_topic_detail(&ctx))
}

fn parse_activation_state(s: &str) -> ActivationState {
    match s.to_lowercase().as_str() {
        "active" => ActivationState::Active,
        "crystallized" => ActivationState::Crystallized,
        _ => ActivationState::Dormant,
    }
}

/// Delete an L2 context and all associated data.
pub fn delete_l2(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    l1_reverse_index: &mut L1ReverseIndex,
    sparse_index: &mut SparseIndex,
    id: &str,
) -> Result<(), MemHopError> {
    let id_hash = parse_id_to_hash(id);
    let page_ref = match btree.search(id_hash) {
        Some(pr) => pr,
        None => return Ok(()),
    };

    let ctx = {
        let data: &[u8] = &mmap[..];
        let slot_data = get_slot_data(data, page_ref)
            .ok_or_else(|| MemHopError::PageNotFound(decode_page_id(page_ref)))?;
        ContextSlot::deserialize_slot(slot_data)?
    };

    let data: &[u8] = &mmap[..];
    let l1_nodes: Vec<(u64, u64)> = l1_reverse_index
        .find_associated(&std::iter::once(id_hash).collect())
        .into_iter()
        .filter(|(_, page_ref)| {
            let page_id = decode_page_id(*page_ref);
            if page_id >= header.page_count {
                return false;
            }
            if let Ok(page_hdr) = crate::file::page::read_page_header(data, page_id) {
                page_hdr.page_type == PageType::ContextNode as u16
            } else {
                false
            }
        })
        .collect();

    for (node_hash, node_page_ref) in l1_nodes {
        btree.delete(node_hash);
        crate::file::free_list::free_page(mmap, header, decode_page_id(node_page_ref))?;
        l1_reverse_index.remove_node(node_hash);
    }

    for &arc_hash in &ctx.archive_refs {
        if let Some(arc_page_ref) = btree.delete(arc_hash) {
            crate::file::free_list::free_page(mmap, header, decode_page_id(arc_page_ref))?;
        }
    }

    if ctx.centroid_page_ref != 0 {
        let centroid_page_id = decode_page_id(ctx.centroid_page_ref);
        crate::file::free_list::free_page(mmap, header, centroid_page_id)?;
    }

    btree.delete(id_hash);
    crate::file::free_list::free_page(mmap, header, decode_page_id(page_ref))?;

    sparse_index.remove_document(id_hash);
    l1_reverse_index.remove_context(id_hash);

    Ok(())
}

/// Delete a range of L4 archives associated with an L2 context.
pub fn delete_turn(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    l2_id: &str,
    range: std::ops::Range<usize>,
) -> Result<(), MemHopError> {
    let id_hash = parse_id_to_hash(l2_id);
    let page_ref = btree.search(id_hash).ok_or(MemHopError::PageNotFound(0))?;
    let page_id = decode_page_id(page_ref);
    let offset = crate::shared::slot_io::slot_offset(page_id);

    let mut ctx = ContextSlot::deserialize_slot(&mmap[offset..])?;

    let len = ctx.archive_refs.len();
    if range.start > len || range.end > len || range.start > range.end {
        return Err(MemHopError::InvalidQuery(
            "turn range out of bounds".to_string(),
        ));
    }

    let removed: Vec<u64> = ctx.archive_refs.drain(range).collect();
    for &arc_hash in &removed {
        if let Some(arc_page_ref) = btree.delete(arc_hash) {
            crate::file::free_list::free_page(mmap, header, decode_page_id(arc_page_ref))?;
        }
    }

    ctx.turn_count = ctx.archive_refs.len() as u32;
    ctx.updated_at = now_ms();
    ctx.version += 1;

    let data = ctx
        .serialize()
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;
    if offset + data.len() > mmap.len() {
        return Err(MemHopError::PageNotFound(page_id));
    }
    mmap[offset..offset + data.len()].copy_from_slice(&data);

    // Rebuild sparse terms since summary/title unchanged; no need to update sparse_index,
    // but the document id is still valid. Archive ids are gone from the doc indirectly.
    let _ = sparse_index;

    Ok(())
}

/// Merge multiple L2 contexts into a primary context.
pub fn merge_l2(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    primary_id: &str,
    merge_ids: Vec<String>,
) -> Result<MergeResult, MemHopError> {
    let primary_hash = parse_id_to_hash(primary_id);
    let merge_hashes: Vec<u64> = merge_ids.iter().map(|id| parse_id_to_hash(id)).collect();

    if btree.search(primary_hash).is_none() {
        return Err(MemHopError::PageNotFound(0));
    }
    for &hash in &merge_hashes {
        if btree.search(hash).is_none() {
            return Err(MemHopError::PageNotFound(0));
        }
    }

    let primary_page_ref = btree.search(primary_hash).unwrap();
    let primary_page_id = decode_page_id(primary_page_ref);
    let primary_offset = crate::shared::slot_io::slot_offset(primary_page_id);
    let mut primary_ctx = ContextSlot::deserialize_slot(&mmap[primary_offset..])?;

    let mut merged_archive_refs: HashSet<u64> = primary_ctx.archive_refs.iter().copied().collect();
    let mut merged_l3_refs: HashSet<u64> = primary_ctx.l3_refs.iter().copied().collect();
    let mut merged_turn_count = primary_ctx.turn_count;
    let mut min_ts = primary_ctx.dialogue_range.0;
    let mut max_ts = primary_ctx.dialogue_range.1;
    let mut secondary_summaries: Vec<String> = Vec::new();

    for &sec_hash in &merge_hashes {
        let sec_page_ref = btree.search(sec_hash).unwrap();
        let sec_page_id = decode_page_id(sec_page_ref);
        let sec_offset = crate::shared::slot_io::slot_offset(sec_page_id);
        let sec_ctx = ContextSlot::deserialize_slot(&mmap[sec_offset..])?;

        merged_archive_refs.extend(sec_ctx.archive_refs.iter());
        merged_l3_refs.extend(sec_ctx.l3_refs.iter());
        merged_turn_count += sec_ctx.turn_count;
        min_ts = min_ts.min(sec_ctx.dialogue_range.0);
        max_ts = max_ts.max(sec_ctx.dialogue_range.1);
        if let Some(ref s) = sec_ctx.summary {
            secondary_summaries.push(s.clone());
        }

        sparse_index.remove_document(sec_hash);
        crate::file::free_list::free_page(mmap, header, sec_page_id)?;
        btree.remove(sec_hash);
    }

    primary_ctx.archive_refs = merged_archive_refs.into_iter().collect();
    primary_ctx.archive_refs.sort();
    primary_ctx.l3_refs = merged_l3_refs.into_iter().collect();
    primary_ctx.l3_refs.sort();
    primary_ctx.l3_refs.dedup();
    primary_ctx.turn_count = merged_turn_count;
    primary_ctx.dialogue_range = (min_ts, max_ts);

    if !secondary_summaries.is_empty() {
        let mut combined = primary_ctx.summary.unwrap_or_default();
        for s in &secondary_summaries {
            if !combined.is_empty() {
                combined.push_str(" | ");
            }
            combined.push_str(s);
        }
        primary_ctx.summary = Some(combined);
    }

    primary_ctx.updated_at = now_ms();
    primary_ctx.version += 1;

    let data = primary_ctx
        .serialize()
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;
    if primary_offset + data.len() > mmap.len() {
        return Err(MemHopError::PageNotFound(primary_page_id));
    }
    mmap[primary_offset..primary_offset + data.len()].copy_from_slice(&data);

    sparse_index.remove_document(primary_hash);
    let (terms, doc_len) =
        crate::shared::common::build_l2_sparse_terms(&primary_ctx.title, &primary_ctx.summary);
    sparse_index.add_document(primary_hash, terms, doc_len);

    Ok(MergeResult {
        primary: to_topic_detail(&primary_ctx),
        merged_ids: merge_ids,
    })
}

#[inline]
fn page_type(data: &[u8], page_id: u32) -> Option<u16> {
    let offset = (page_id as usize) * PAGE_SIZE + 4;
    if offset + 2 > data.len() {
        return None;
    }
    Some(u16::from_le_bytes([data[offset], data[offset + 1]]))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::index::sparse::SparseIndex;
    use crate::layers::archive::{ArchiveSlot, ContentType};
    use crate::layers::context::{ActivationState, LlmParams};
    use crate::query::search::L1ReverseIndex;
    use crate::test_helpers::{create_test_mmap, insert_test_context};

    fn make_ctx(id_hash: u64, title: &str) -> ContextSlot {
        ContextSlot {
            id_hash,
            parent_id: None,
            depth: 1,
            title: title.to_string(),
            summary: None,
            archive_refs: vec![],
            l3_refs: vec![],
            turn_count: 0,
            created_at: 0,
            updated_at: 0,
            version: 1,
            importance: 0.5,
            activation_score: 0.0,
            is_active: true,
            activation_state: ActivationState::Active,
            centroid_page_ref: 0,
            dialogue_range: (0, 0),
            llm_params: LlmParams::default(),
        }
    }

    #[test]
    fn test_l2_crud_roundtrip() {
        let (mut mmap, mut header, mut btree, mut file) = create_test_mmap(64);
        let mut sparse = SparseIndex::new();
        let mut l1_reverse = L1ReverseIndex::new();

        let ctx = make_ctx(1001, "Rust refactoring");
        insert_test_context(
            &mut mmap,
            &mut header,
            &mut btree,
            &mut sparse,
            ctx,
            &mut file,
        );

        let got = get_l2(&mmap, &btree, "00000000000003e9")
            .unwrap()
            .expect("L2 should exist");
        assert_eq!(got.title, "Rust refactoring");

        let detail = update_l2(
            &mut mmap,
            &mut header,
            &btree,
            &mut sparse,
            "00000000000003e9",
            UpdateL2Fields {
                title: Some("Updated title".into()),
                importance: Some(0.9),
                ..Default::default()
            },
        )
        .unwrap();
        assert_eq!(detail.title, "Updated title");
        assert_eq!(detail.importance, 0.9);

        let list = list_l2(
            &mmap,
            &header,
            &btree,
            TopicListQuery {
                page: 1,
                page_size: 10,
                active_only: false,
                keyword: Some("Updated".into()),
            },
        )
        .unwrap();
        assert_eq!(list.total, 1);

        delete_l2(
            &mut mmap,
            &mut header,
            &mut btree,
            &mut l1_reverse,
            &mut sparse,
            "00000000000003e9",
        )
        .unwrap();
        assert!(get_l2(&mmap, &btree, "00000000000003e9").unwrap().is_none());
    }

    #[test]
    fn test_delete_turn_and_merge() {
        let (mut mmap, mut header, mut btree, mut file) = create_test_mmap(64);
        let mut sparse = SparseIndex::new();

        // Insert two contexts and one archive shared by the primary.
        let mut primary = make_ctx(2001, "primary");
        let archive_id = 3001u64;
        primary.archive_refs.push(archive_id);
        primary.turn_count = 1;
        insert_test_context(
            &mut mmap,
            &mut header,
            &mut btree,
            &mut sparse,
            primary,
            &mut file,
        );

        let archive = ArchiveSlot {
            id_hash: archive_id,
            content_type: ContentType::Text,
            role: 0,
            context_id: 2001,
            created_at: 1000,
            content: "hello".into(),
            metadata: None,
        };
        let arc_page = crate::file::page::allocate_page(
            &mut mmap,
            &mut header,
            PageType::Archive,
            4,
            crate::index::btree::EMPTY_PAGE,
            &mut file,
        )
        .unwrap();
        crate::file::page::write_page_data(&mut mmap, arc_page, &archive.serialize().unwrap())
            .unwrap();
        btree.insert(archive_id, (arc_page as u64) << 16);

        let mut secondary = make_ctx(2002, "secondary");
        secondary.summary = Some("secondary summary".into());
        secondary.turn_count = 3;
        insert_test_context(
            &mut mmap,
            &mut header,
            &mut btree,
            &mut sparse,
            secondary,
            &mut file,
        );

        // delete_turn on primary removes archive at index 0.
        delete_turn(
            &mut mmap,
            &mut header,
            &mut btree,
            &mut sparse,
            "00000000000007d1",
            0..1,
        )
        .unwrap();
        let primary_after = get_l2(&mmap, &btree, "00000000000007d1").unwrap().unwrap();
        assert!(primary_after.archive_refs.is_empty());
        assert_eq!(primary_after.turn_count, 0);

        // Merge secondary into primary.
        let merged = merge_l2(
            &mut mmap,
            &mut header,
            &mut btree,
            &mut sparse,
            "00000000000007d1",
            vec!["00000000000007d2".into()],
        )
        .unwrap();
        assert_eq!(merged.primary.turn_count, 3);
        assert!(merged.primary.summary.is_some());
        assert!(get_l2(&mmap, &btree, "00000000000007d2").unwrap().is_none());
    }
}
