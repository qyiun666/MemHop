// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L2 ContextSlot CRUD internal implementation.

use crate::dream::llm::LlmProvider;
use crate::encoder::Encoder;
use crate::file::header::FileHeader;
use crate::index::btree::BTreeIndex;
use crate::index::l2_meta::L2MetaIndex;
use crate::index::sparse::SparseIndex;
use crate::layers::context::{ActivationState, ContextSlot};
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

fn to_topic_detail(ctx: &ContextSlot) -> TopicDetail {
    TopicDetail {
        id: format_hash(ctx.id_hash),
        title: ctx.title.clone(),
        summary: ctx.summary.clone(),
        depth: ctx.depth,
        scene_id: ctx.scene_id,
        children_ids: ctx.children_ids.clone(),
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
        scene_id: ctx.scene_id,
        children_ids: ctx.children_ids.clone(),
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

// ============================================================================
// Scene Tree Query
// ============================================================================

/// List the full tree of nodes within a scene.
///
/// Returns all nodes belonging to the given `scene_id`, sorted by `created_at`,
/// along with edge topology and depth distribution.
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
            edges.push((format_hash(parent_id), format_hash(ctx.id_hash)));
        }
        for &child_id in &ctx.children_ids {
            edges.push((format_hash(ctx.id_hash), format_hash(child_id)));
        }
    }

    // Deduplicate edges
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
// Merge Nodes (manual scene tree compaction)
// ============================================================================

/// Recursively sink a node's depth by 1 and update its parent.
///
/// If the new depth reaches 4, the node and its entire subtree are deleted.
#[allow(clippy::too_many_arguments)]
fn merge_nodes_sink(
    id_hash: u64,
    new_parent_id: u64,
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    l2_meta: &mut L2MetaIndex,
    file: &mut File,
    sunk_ids: &mut Vec<String>,
    removed_ids: &mut Vec<String>,
) -> Result<(), MemHopError> {
    let page_ref = match btree.search(id_hash) {
        Some(pr) => pr,
        None => return Ok(()),
    };
    let page_id = decode_page_id(page_ref);
    let slot_data = match get_slot_data(&mmap[..], page_ref) {
        Some(d) => d,
        None => return Ok(()),
    };
    let mut ctx = match ContextSlot::deserialize_slot(slot_data) {
        Ok(c) => c,
        Err(_) => return Ok(()),
    };

    ctx.depth += 1;
    let new_depth = ctx.depth;
    ctx.parent_id = Some(new_parent_id);
    ctx.updated_at = now_ms();

    if new_depth >= 4 {
        return merge_nodes_free_subtree(
            id_hash,
            mmap,
            header,
            btree,
            sparse_index,
            l2_meta,
            file,
            removed_ids,
        );
    }

    // Save updated node
    let data = ctx
        .serialize()
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;
    crate::file::page::write_page_data(mmap, page_id, &data)?;

    // Update in-memory L2 metadata index
    l2_meta.update_from_context(&ctx);
    sunk_ids.push(format_hash(ctx.id_hash));

    // Recursively sink children (they move under this node)
    let child_ids = ctx.children_ids.clone();
    for &child_id in &child_ids {
        merge_nodes_sink(
            child_id,
            id_hash,
            mmap,
            header,
            btree,
            sparse_index,
            l2_meta,
            file,
            sunk_ids,
            removed_ids,
        )?;
    }

    Ok(())
}

/// Recursively delete a node and all its descendants.
#[allow(clippy::too_many_arguments)]
fn merge_nodes_free_subtree(
    id_hash: u64,
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    l2_meta: &mut L2MetaIndex,
    file: &mut File,
    removed_ids: &mut Vec<String>,
) -> Result<(), MemHopError> {
    let page_ref = match btree.search(id_hash) {
        Some(pr) => pr,
        None => return Ok(()),
    };
    let page_id = decode_page_id(page_ref);

    // Load node to traverse children and free centroid vector page
    let ctx = {
        let slot_data = match get_slot_data(&mmap[..], page_ref) {
            Some(d) => d,
            None => return Ok(()),
        };
        match ContextSlot::deserialize_slot(slot_data) {
            Ok(c) => c,
            Err(_) => return Ok(()),
        }
    };

    // Recursively free children first (post-order traversal)
    let child_ids = ctx.children_ids.clone();
    for &child_id in &child_ids {
        merge_nodes_free_subtree(
            child_id,
            mmap,
            header,
            btree,
            sparse_index,
            l2_meta,
            file,
            removed_ids,
        )?;
    }

    // Free centroid vector page if present
    if ctx.centroid_page_ref != 0 {
        let v_page_id = decode_page_id(ctx.centroid_page_ref);
        if v_page_id > 0 {
            let v_offset = (v_page_id as usize) * PAGE_SIZE;
            if v_offset + PAGE_SIZE <= mmap.len() {
                mmap[v_offset..v_offset + PAGE_SIZE].fill(0);
                let _ = crate::file::free_list::free_page(mmap, header, v_page_id);
            }
        }
    }

    // Remove from indices
    btree.remove(id_hash);
    sparse_index.remove_document(id_hash);
    l2_meta.remove(id_hash);

    // Zero and free the page
    let page_offset = crate::shared::slot_io::page_offset(page_id);
    let page_end = page_offset + PAGE_SIZE;
    if page_end <= mmap.len() {
        mmap[page_offset..page_end].fill(0);
    }
    crate::file::free_list::free_page(mmap, header, page_id)?;

    removed_ids.push(format_hash(id_hash));

    let _ = file; // keep signature symmetric
    Ok(())
}

/// Manually merge multiple depth-1 nodes under the same scene into a new parent node.
///
/// # Arguments
/// * `mmap` - Mutable memory-mapped file.
/// * `header` - File header for page allocation / free list.
/// * `btree` - B-tree index.
/// * `sparse_index` - Sparse (BM25) index.
/// * `l2_meta` - In-memory L2 metadata index (mutable).
/// * `llm` - LLM provider for merge summarization.
/// * `node_ids` - Depth-1 node IDs to merge (must all belong to the same scene).
/// * `scene_id` - Scene identifier.
/// * `file` - Backing file for mmap extension.
/// * `encoder` - Optional encoder for centroid vectors.
#[allow(clippy::too_many_arguments)]
pub fn merge_nodes(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    l2_meta: &mut L2MetaIndex,
    llm: &dyn LlmProvider,
    node_ids: &[u64],
    scene_id: u64,
    file: &mut File,
    encoder: Option<&(dyn Encoder + Send + Sync)>,
) -> Result<MergeNodesResult, MemHopError> {
    if node_ids.is_empty() {
        return Err(MemHopError::InvalidQuery("No nodes to merge".to_string()));
    }

    // Load and validate all nodes: must exist, depth=1, same scene_id
    let mut nodes: Vec<ContextSlot> = Vec::with_capacity(node_ids.len());
    for &id_hash in node_ids {
        let page_ref = btree.search(id_hash).ok_or_else(|| {
            MemHopError::PageNotFound(crate::shared::slot_io::decode_page_id(id_hash << 16))
        })?;
        let slot_data = get_slot_data(&mmap[..], page_ref)
            .ok_or_else(|| MemHopError::PageNotFound(decode_page_id(page_ref)))?;
        let ctx = ContextSlot::deserialize_slot(slot_data)?;

        if ctx.depth != 1 {
            return Err(MemHopError::InvalidQuery(format!(
                "Node {} is not depth=1",
                format_hash(id_hash)
            )));
        }
        if ctx.scene_id != scene_id {
            return Err(MemHopError::InvalidQuery(format!(
                "Node {} does not belong to scene {}",
                format_hash(id_hash),
                format_hash(scene_id)
            )));
        }
        nodes.push(ctx);
    }

    // Sort by created_at (earliest first)
    nodes.sort_by_key(|n| n.created_at);

    // Collect texts for LLM merge summarization
    let texts: Vec<String> = nodes
        .iter()
        .map(|n| {
            format!(
                "Title: {}\nSummary: {}",
                n.title,
                n.summary.as_deref().unwrap_or("(none)")
            )
        })
        .collect();

    let (new_title, new_summary) = llm.merge_summarize(&texts)?;

    // Compute centroid from the merged summary text (if encoder available)
    let centroid_text = nodes
        .iter()
        .filter_map(|n| n.summary.as_deref())
        .collect::<Vec<&str>>()
        .join(" ");

    let centroid_page_ref = if let Some(enc) = encoder {
        match enc.encode(&centroid_text) {
            Ok(output) => {
                let v_page_id = crate::file::page::allocate_page(
                    mmap,
                    header,
                    PageType::VectorMatrix,
                    2,
                    0,
                    file,
                )?;
                let v_offset = crate::shared::slot_io::slot_offset(v_page_id);
                let v_bytes: Vec<u8> = output.dense.iter().flat_map(|v| v.to_ne_bytes()).collect();
                if v_offset + v_bytes.len() > mmap.len() {
                    tracing::warn!("Centroid page allocation failed, centroid omitted");
                    let _ = crate::file::free_list::free_page(mmap, header, v_page_id);
                    0
                } else {
                    mmap[v_offset..v_offset + v_bytes.len()].copy_from_slice(&v_bytes);
                    crate::file::page::encode_page_ref(v_page_id, 0)
                }
            }
            Err(e) => {
                tracing::warn!("Failed to encode merged centroid: {}", e);
                0
            }
        }
    } else {
        0
    };

    // Merge archive_refs (deduplicated)
    let mut archive_refs: Vec<u64> = Vec::new();
    for node in &nodes {
        for &rid in &node.archive_refs {
            if !archive_refs.contains(&rid) {
                archive_refs.push(rid);
            }
        }
    }

    // Merge l3_refs (deduplicated)
    let mut l3_refs: Vec<u64> = Vec::new();
    for node in &nodes {
        for &rid in &node.l3_refs {
            if !l3_refs.contains(&rid) {
                l3_refs.push(rid);
            }
        }
    }

    let now = get_current_timestamp();
    let children_ids: Vec<u64> = nodes.iter().map(|n| n.id_hash).collect();
    let total_turn_count: u32 = nodes.iter().map(|n| n.turn_count).sum();
    let first_created = nodes.iter().map(|n| n.created_at).min().unwrap_or(now);

    // Generate a deterministic-but-unique id for the parent (first node id ensures uniqueness)
    let parent_id_hash = crate::util::hash_id(&format!(
        "merged_parent_{}_{}_{}",
        scene_id, now, node_ids[0]
    ));

    let parent_node = ContextSlot {
        id_hash: parent_id_hash,
        scene_id,
        parent_id: None,
        children_ids,
        depth: 1,
        title: new_title,
        summary: Some(new_summary),
        archive_refs,
        l3_refs,
        turn_count: total_turn_count,
        created_at: first_created,
        updated_at: now,
        version: 3,
        importance: 0.5,
        activation_score: 0.0,
        is_active: false,
        activation_state: ActivationState::Dormant,
        centroid_page_ref,
        dialogue_range: (first_created, now),
        llm_params: crate::layers::context::LlmParams::default(),
    };

    // Write parent node to disk
    let parent_page_id =
        crate::file::page::allocate_page(mmap, header, PageType::Context, 2, 0, file)?;
    let parent_data = parent_node
        .serialize()
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;
    crate::file::page::write_page_data(mmap, parent_page_id, &parent_data)?;
    let parent_ref = crate::file::page::encode_page_ref(parent_page_id, 0);
    btree.insert(parent_node.id_hash, parent_ref);

    // Index parent for BM25
    let mut index_text = parent_node.title.clone();
    if let Some(ref s) = parent_node.summary {
        index_text.push(' ');
        index_text.push_str(s);
    }
    let index_terms = crate::index::sparse::tokenize(&index_text);
    let doc_len = index_terms.len() as u32;
    sparse_index.add_document(parent_node.id_hash, index_terms, doc_len);

    // Register parent in the L2 meta index
    l2_meta.update_from_context(&parent_node);

    // Sink each original node under the new parent
    let mut sunk_ids: Vec<String> = Vec::new();
    let mut removed_ids: Vec<String> = Vec::new();

    for child in &nodes {
        merge_nodes_sink(
            child.id_hash,
            parent_node.id_hash,
            mmap,
            header,
            btree,
            sparse_index,
            l2_meta,
            file,
            &mut sunk_ids,
            &mut removed_ids,
        )?;
    }

    Ok(MergeNodesResult {
        new_parent_node_id: format_hash(parent_node.id_hash),
        sunk_node_ids: sunk_ids,
        removed_node_ids: removed_ids,
    })
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
            scene_id: 0,
            parent_id: None,
            children_ids: vec![],
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
