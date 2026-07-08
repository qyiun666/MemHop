// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L2 ContextSlot CRUD internal implementation.

use crate::dream::llm::LlmProvider;
use crate::encoder::Encoder;
use crate::file::header::FileHeader;
use crate::index::btree::BTreeIndex;
use crate::index::l2_meta::L2MetaIndex;
use crate::index::sparse::SparseIndex;
use crate::layers::context::{ContextSlot, SceneSlot};
use crate::query::search::L1ReverseIndex;
use crate::query::types::{
    MergeNodesResult, MergeResult, SceneTreeResult, TopicDetail, TopicListQuery, TopicListResult,
    TopicSummary, UpdateL2Fields,
};
use crate::shared::common::{format_hash, now_ms, parse_id_to_hash};
use crate::shared::slot_io::{decode_page_id, get_slot_data};
use crate::util::{get_current_timestamp, PageType, PAGE_SIZE};
use crate::MemHopError;
use memmap2::MmapMut;
use std::collections::HashSet;
use std::fs::File;

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

fn load_scene(
    mmap: &MmapMut,
    btree: &BTreeIndex,
    scene_id: u64,
) -> Result<Option<SceneSlot>, MemHopError> {
    match btree.search(scene_id) {
        Some(page_ref) => {
            let data: &[u8] = &mmap[..];
            let slot_data = get_slot_data(data, page_ref)
                .ok_or_else(|| MemHopError::PageNotFound(decode_page_id(page_ref)))?;
            Ok(Some(
                SceneSlot::deserialize(slot_data)
                    .map_err(|e| MemHopError::Serialization(e.to_string()))?,
            ))
        }
        None => Ok(None),
    }
}

pub(crate) fn to_topic_detail(ctx: &ContextSlot) -> TopicDetail {
    TopicDetail {
        id: format_hash(ctx.id),
        parent_id: ctx.parent_id.map(format_hash),
        depth: ctx.depth,
        scene_id: ctx.scene_id,
        user_keywords: ctx.user_keywords.clone(),
        user_timestamp: ctx.user_timestamp,
        agent_keywords: ctx.agent_keywords.clone(),
        agent_timestamp: ctx.agent_timestamp,
        fused_keywords: ctx.fused_keywords.clone(),
        fused_summary: ctx.fused_summary.clone(),
        children_ids: ctx.children_ids.clone(),
        user_l4_refs: ctx.user_l4_refs.iter().map(|h| format_hash(*h)).collect(),
        user_l3_refs: ctx.user_l3_refs.iter().map(|h| format_hash(*h)).collect(),
        agent_l4_refs: ctx.agent_l4_refs.iter().map(|h| format_hash(*h)).collect(),
        agent_l3_refs: ctx.agent_l3_refs.iter().map(|h| format_hash(*h)).collect(),
        created_at: ctx.created_at,
        updated_at: ctx.updated_at,
    }
}

fn to_topic_summary(ctx: &ContextSlot) -> TopicSummary {
    TopicSummary {
        id: format_hash(ctx.id),
        depth: ctx.depth,
        scene_id: ctx.scene_id,
        user_keywords: ctx.user_keywords.clone(),
        agent_keywords: ctx.agent_keywords.clone(),
        fused_keywords: ctx.fused_keywords.clone(),
        l4_count: ctx.user_l4_refs.len() + ctx.agent_l4_refs.len(),
        l3_count: ctx.user_l3_refs.len() + ctx.agent_l3_refs.len(),
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
                if let Some(ref keyword) = query.keyword {
                    let kw_text: String = ctx.user_keywords.join(" ");
                    if !crate::shared::common::matches_keyword(&kw_text, keyword) {
                        continue;
                    }
                }
                all.push(ctx);
            }
        }
    }

    // Sort by updated_at descending (most recently updated first)
    all.sort_by(|a, b| b.updated_at.cmp(&a.updated_at));

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

    let mut index_changed = false;
    if let Some(kws) = fields.user_keywords {
        if ctx.user_keywords != kws {
            ctx.user_keywords = kws;
            index_changed = true;
        }
    }
    if let Some(kws) = fields.agent_keywords {
        ctx.agent_keywords = kws;
    }
    if let Some(summary) = fields.fused_summary {
        if ctx.fused_summary.as_ref() != Some(&summary) {
            ctx.fused_summary = Some(summary);
            index_changed = true;
        }
    }
    if let Some(l3_refs) = fields.l3_refs {
        ctx.user_l3_refs = l3_refs.iter().map(|s| parse_id_to_hash(s)).collect();
        ctx.user_l3_refs.sort();
        ctx.user_l3_refs.dedup();
        // Agent-side l3_refs are cleared when user provides new refs
        ctx.agent_l3_refs.clear();
    }

    if index_changed {
        sparse_index.remove_document(ctx.id);
        let kw_text: String = ctx.user_keywords.join(" ");
        let summary = ctx.fused_summary.clone();
        let (terms, doc_len) = crate::shared::common::build_l2_sparse_terms(&kw_text, &summary);
        sparse_index.add_document(ctx.id, terms, doc_len);
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

    // Free all L4 archive pages referenced by both user and agent tracks
    for &arc_hash in ctx.user_l4_refs.iter().chain(ctx.agent_l4_refs.iter()) {
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

    // Combine both user and agent L4 refs for turn deletion
    let mut all_l4_refs: Vec<u64> = ctx.user_l4_refs.clone();
    all_l4_refs.extend(&ctx.agent_l4_refs);
    let len = all_l4_refs.len();

    if range.start > len || range.end > len || range.start > range.end {
        return Err(MemHopError::InvalidQuery(
            "turn range out of bounds".to_string(),
        ));
    }

    // Delete archive pages for removed refs
    let removed: Vec<u64> = all_l4_refs.drain(range).collect();
    for &arc_hash in &removed {
        // Remove from whichever track it belongs to
        if let Some(pos) = ctx.user_l4_refs.iter().position(|&h| h == arc_hash) {
            ctx.user_l4_refs.remove(pos);
        }
        if let Some(pos) = ctx.agent_l4_refs.iter().position(|&h| h == arc_hash) {
            ctx.agent_l4_refs.remove(pos);
        }
        if let Some(arc_page_ref) = btree.delete(arc_hash) {
            crate::file::free_list::free_page(mmap, header, decode_page_id(arc_page_ref))?;
        }
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

    let mut merged_user_l4: HashSet<u64> = primary_ctx.user_l4_refs.iter().copied().collect();
    let mut merged_agent_l4: HashSet<u64> = primary_ctx.agent_l4_refs.iter().copied().collect();
    let mut merged_user_l3: HashSet<u64> = primary_ctx.user_l3_refs.iter().copied().collect();
    let mut merged_agent_l3: HashSet<u64> = primary_ctx.agent_l3_refs.iter().copied().collect();
    let mut secondary_summaries: Vec<String> = Vec::new();

    for &sec_hash in &merge_hashes {
        let sec_page_ref = btree.search(sec_hash).unwrap();
        let sec_page_id = decode_page_id(sec_page_ref);
        let sec_offset = crate::shared::slot_io::slot_offset(sec_page_id);
        let sec_ctx = ContextSlot::deserialize_slot(&mmap[sec_offset..])?;

        merged_user_l4.extend(sec_ctx.user_l4_refs.iter());
        merged_agent_l4.extend(sec_ctx.agent_l4_refs.iter());
        merged_user_l3.extend(sec_ctx.user_l3_refs.iter());
        merged_agent_l3.extend(sec_ctx.agent_l3_refs.iter());
        if let Some(ref s) = sec_ctx.fused_summary {
            secondary_summaries.push(s.clone());
        }

        sparse_index.remove_document(sec_hash);
        crate::file::free_list::free_page(mmap, header, sec_page_id)?;
        btree.remove(sec_hash);
    }

    primary_ctx.user_l4_refs = merged_user_l4.into_iter().collect();
    primary_ctx.user_l4_refs.sort();
    primary_ctx.agent_l4_refs = merged_agent_l4.into_iter().collect();
    primary_ctx.agent_l4_refs.sort();
    primary_ctx.user_l3_refs = merged_user_l3.into_iter().collect();
    primary_ctx.user_l3_refs.sort();
    primary_ctx.user_l3_refs.dedup();
    primary_ctx.agent_l3_refs = merged_agent_l3.into_iter().collect();
    primary_ctx.agent_l3_refs.sort();
    primary_ctx.agent_l3_refs.dedup();

    if !secondary_summaries.is_empty() {
        let mut combined = primary_ctx.fused_summary.unwrap_or_default();
        for s in &secondary_summaries {
            if !combined.is_empty() {
                combined.push_str(" | ");
            }
            combined.push_str(s);
        }
        primary_ctx.fused_summary = Some(combined);
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
    let kw_text: String = primary_ctx.user_keywords.join(" ");
    let summary = primary_ctx.fused_summary.clone();
    let (terms, doc_len) = crate::shared::common::build_l2_sparse_terms(&kw_text, &summary);
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

// ============================================================================
// Scene CRUD
// ============================================================================

/// Create a scene. Idempotent — returns scene_id of existing scene if name matches.
pub fn create_scene(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    file: &mut File,
    name: &str,
) -> Result<u64, MemHopError> {
    let scene = SceneSlot::new(name);
    let scene_id = scene.scene_id;

    // Idempotent: return existing scene_id if already created
    if let Some(_) = load_scene(mmap, btree, scene_id)? {
        return Ok(scene_id);
    }

    let data = scene
        .serialize()
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;

    let page_id = crate::file::page::allocate_page(mmap, header, PageType::Scene, 2, 0, file)?;
    crate::file::page::write_page_data(mmap, page_id, &data)?;
    let page_ref = crate::file::page::encode_page_ref(page_id, 0);
    btree.insert(scene_id, page_ref);

    Ok(scene_id)
}

/// Read a scene by its numeric id.
pub fn get_scene(
    mmap: &MmapMut,
    btree: &BTreeIndex,
    scene_id: u64,
) -> Result<Option<SceneSlot>, MemHopError> {
    load_scene(mmap, btree, scene_id)
}

/// List all scenes as (scene_id, scene_name) pairs.
pub fn list_scenes(mmap: &MmapMut, header: &FileHeader) -> Result<Vec<(u64, String)>, MemHopError> {
    let data: &[u8] = &mmap[..];
    let mut results: Vec<(u64, String)> = Vec::new();

    for page_id in 2..header.page_count {
        if page_type(data, page_id) != Some(PageType::Scene as u16) {
            continue;
        }
        let offset = crate::shared::slot_io::slot_offset(page_id);
        if offset >= data.len() {
            continue;
        }
        if let Ok(scene) = SceneSlot::deserialize(&data[offset..])
            .map_err(|e| MemHopError::Serialization(e.to_string()))
        {
            results.push((scene.scene_id, scene.scene_name));
        }
    }

    Ok(results)
}

// ============================================================================
// Scene Tree Query
// ============================================================================

/// List the full tree of nodes within a scene.
pub fn list_scene_tree(
    mmap: &[u8],
    btree: &BTreeIndex,
    l2_meta: &L2MetaIndex,
    scene_id: u64,
) -> Result<SceneTreeResult, MemHopError> {
    let node_ids = match l2_meta.get_by_scene(scene_id) {
        Some(ids) => ids.clone(),
        None => {
            return Ok(SceneTreeResult {
                scene_id: format_hash(scene_id),
                total_turns: 0,
                depth_distribution: [0; 4],
                nodes: vec![],
                edges: vec![],
            });
        }
    };

    let mut nodes: Vec<ContextSlot> = Vec::with_capacity(node_ids.len());
    for &id_hash in &node_ids {
        let page_ref = match btree.search(id_hash) {
            Some(pr) => pr,
            None => continue,
        };
        let slot_data = match get_slot_data(mmap, page_ref) {
            Some(d) => d,
            None => continue,
        };
        if let Ok(ctx) = ContextSlot::deserialize_slot(slot_data) {
            nodes.push(ctx);
        }
    }

    nodes.sort_by_key(|n| n.created_at);

    let total_turns = nodes.len() as u32;
    let mut depth_distribution = [0u32; 4];
    let mut edges: Vec<(String, String)> = Vec::new();

    for ctx in &nodes {
        let depth_idx = (ctx.depth.saturating_sub(1).min(3)) as usize;
        depth_distribution[depth_idx] += 1;

        if let Some(parent_id) = ctx.parent_id {
            edges.push((format_hash(parent_id), format_hash(ctx.id)));
        }
        for &child_id in &ctx.children_ids {
            edges.push((format_hash(ctx.id), format_hash(child_id)));
        }
    }

    edges.sort();
    edges.dedup();

    let topic_details: Vec<TopicDetail> = nodes.iter().map(to_topic_detail).collect();

    Ok(SceneTreeResult {
        scene_id: format_hash(scene_id),
        total_turns,
        depth_distribution,
        nodes: topic_details,
        edges,
    })
}

// ============================================================================
// Merge Nodes (scene reassignment)
// ============================================================================

/// Merge secondary scenes into a main scene.
///
/// All nodes from `secondary_scene_ids` have their `scene_id` changed to
/// `main_scene_id`.  No other metadata is modified — pure scene reassignment.
pub fn merge_nodes(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    _sparse_index: &mut SparseIndex,
    l2_meta: &mut L2MetaIndex,
    main_scene_id: u64,
    secondary_scene_ids: &[u64],
    _file: &mut File,
) -> Result<MergeNodesResult, MemHopError> {
    let mut merged_count: u32 = 0;

    for &sec_id in secondary_scene_ids {
        // Clone the list — update_from_context mutates by_scene during iteration
        let node_ids = l2_meta.get_by_scene(sec_id).cloned().unwrap_or_default();

        for &id_hash in &node_ids {
            let page_ref = match btree.search(id_hash) {
                Some(pr) => pr,
                None => continue,
            };
            let page_id = decode_page_id(page_ref);
            let slot_data = match get_slot_data(&mmap[..], page_ref) {
                Some(d) => d,
                None => continue,
            };
            let mut ctx = match ContextSlot::deserialize_slot(slot_data) {
                Ok(c) => c,
                Err(_) => continue,
            };

            // Only reassign if not already in the main scene
            if ctx.scene_id == main_scene_id {
                continue;
            }

            ctx.scene_id = main_scene_id;
            ctx.updated_at = now_ms();

            let data = ctx
                .serialize()
                .map_err(|e| MemHopError::Serialization(e.to_string()))?;
            crate::file::page::write_page_data(mmap, page_id, &data)?;

            // update_from_context handles removing from old scene index
            // and adding to new scene index
            l2_meta.update_from_context(&ctx);

            merged_count += 1;
        }

        // After reassigning all nodes from this secondary scene,
        // free its SceneSlot page (no nodes remain under sec_id).
        if l2_meta
            .get_by_scene(sec_id)
            .map_or(true, |ids| ids.is_empty())
        {
            if let Some(scene_page_ref) = btree.delete(sec_id) {
                let _ =
                    crate::file::free_list::free_page(mmap, header, decode_page_id(scene_page_ref));
            }
        }
    }

    Ok(MergeNodesResult {
        main_scene_id: format_hash(main_scene_id),
        merged_node_count: merged_count,
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::index::sparse::SparseIndex;
    use crate::layers::archive::{ArchiveSlot, ContentType};
    use crate::layers::context::ContextSlot;
    use crate::query::search::L1ReverseIndex;
    use crate::test_helpers::{create_test_context, create_test_mmap, insert_test_context};

    fn make_ctx(id_hash: u64, title: &str) -> ContextSlot {
        create_test_context(id_hash, title, vec![])
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
        assert!(!got.user_keywords.is_empty());
        assert_eq!(got.user_keywords[0], "Rust refactoring");

        let detail = update_l2(
            &mut mmap,
            &mut header,
            &btree,
            &mut sparse,
            "00000000000003e9",
            UpdateL2Fields {
                user_keywords: Some(vec!["Updated title".into()]),
                ..Default::default()
            },
        )
        .unwrap();
        assert_eq!(detail.user_keywords, vec!["Updated title"]);

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

        let mut primary = make_ctx(2001, "primary");
        let archive_id = 3001u64;
        primary.user_l4_refs.push(archive_id);
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
        secondary.fused_summary = Some("secondary summary".into());
        secondary.user_l4_refs.push(4001);
        secondary.user_l3_refs.push(5001);
        insert_test_context(
            &mut mmap,
            &mut header,
            &mut btree,
            &mut sparse,
            secondary,
            &mut file,
        );

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
        assert!(primary_after.user_l4_refs.is_empty());
        assert!(primary_after.agent_l4_refs.is_empty());

        let merged = merge_l2(
            &mut mmap,
            &mut header,
            &mut btree,
            &mut sparse,
            "00000000000007d1",
            vec!["00000000000007d2".into()],
        )
        .unwrap();
        assert_eq!(merged.primary.user_l3_refs.len(), 1);
        assert!(merged.primary.fused_summary.is_some());
        assert!(get_l2(&mmap, &btree, "00000000000007d2").unwrap().is_none());
    }
}
