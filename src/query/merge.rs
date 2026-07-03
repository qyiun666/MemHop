// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! merge_topics(): merge multiple L2 contexts into one.
//! Secondary contexts are deleted after merge.

use crate::file::header::FileHeader;
use crate::index::btree::BTreeIndex;
use crate::index::sparse::SparseIndex;
use crate::layers::context::ContextSlot;
use crate::query::types::*;
use crate::shared::common::{now_ms, parse_id_to_hash};
use crate::util::PAGE_SIZE;
use crate::MemHopError;
use memmap2::MmapMut;
use std::collections::HashSet;

/// Merge multiple L2 contexts into a primary context.
/// Secondaries are deleted after merging archive_refs, l3_refs, summaries, and turn_counts.
pub fn merge_topics(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    primary_id: &str,
    secondary_ids: Vec<String>,
) -> Result<TopicDetail, MemHopError> {
    let now_ms = now_ms();

    let primary_hash = parse_id_to_hash(primary_id);
    let secondary_hashes: Vec<u64> = secondary_ids
        .iter()
        .map(|id| parse_id_to_hash(id))
        .collect();

    if btree.search(primary_hash).is_none() {
        return Err(MemHopError::PageNotFound(0));
    }
    for &sec_hash in &secondary_hashes {
        if btree.search(sec_hash).is_none() {
            return Err(MemHopError::PageNotFound(0));
        }
    }

    let primary_page_ref = btree.search(primary_hash).unwrap();
    let primary_page_id = (primary_page_ref >> 16) as u32;
    let primary_offset = (primary_page_id as usize) * PAGE_SIZE + 32;
    if primary_offset >= mmap.len() {
        return Err(MemHopError::PageNotFound(primary_page_id));
    }
    let mut primary_ctx = ContextSlot::deserialize(&mmap[primary_offset..])
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;

    let mut merged_archive_refs: HashSet<u64> = primary_ctx.archive_refs.iter().cloned().collect();
    let mut merged_l3_refs: HashSet<u64> = primary_ctx.l3_refs.iter().cloned().collect();
    let mut merged_turn_count = primary_ctx.turn_count;
    let mut min_ts = primary_ctx.dialogue_range.0;
    let mut max_ts = primary_ctx.dialogue_range.1;
    let mut secondary_summaries: Vec<String> = Vec::new();

    for &sec_hash in &secondary_hashes {
        let sec_page_ref = btree.search(sec_hash).unwrap();
        let sec_page_id = (sec_page_ref >> 16) as u32;
        let sec_offset = (sec_page_id as usize) * PAGE_SIZE + 32;
        if sec_offset >= mmap.len() {
            return Err(MemHopError::PageNotFound(sec_page_id));
        }
        let sec_ctx = ContextSlot::deserialize(&mmap[sec_offset..])
            .map_err(|e| MemHopError::Serialization(e.to_string()))?;

        merged_archive_refs.extend(sec_ctx.archive_refs.iter());

        merged_l3_refs.extend(sec_ctx.l3_refs.iter());

        merged_turn_count += sec_ctx.turn_count;

        min_ts = min_ts.min(sec_ctx.dialogue_range.0);
        max_ts = max_ts.max(sec_ctx.dialogue_range.1);

        if let Some(ref s) = sec_ctx.summary {
            secondary_summaries.push(s.clone());
        }
    }

    primary_ctx.archive_refs = merged_archive_refs.into_iter().collect();
    primary_ctx.l3_refs = merged_l3_refs.into_iter().collect();
    primary_ctx.turn_count = merged_turn_count;
    primary_ctx.dialogue_range = (min_ts, max_ts);

    if !secondary_summaries.is_empty() {
        let base = primary_ctx.summary.clone().unwrap_or_default();
        let mut combined = base;
        for s in &secondary_summaries {
            if !combined.is_empty() {
                combined.push_str(" | ");
            }
            combined.push_str(s);
        }
        primary_ctx.summary = Some(combined);
    }

    primary_ctx.updated_at = now_ms;
    primary_ctx.version += 1;

    let primary_data = primary_ctx
        .serialize()
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;

    if primary_offset + primary_data.len() <= mmap.len() {
        mmap[primary_offset..primary_offset + primary_data.len()].copy_from_slice(&primary_data);
    } else {
        return Err(MemHopError::PageNotFound(primary_page_id));
    }

    let mut terms: Vec<String> = primary_ctx
        .title
        .split_whitespace()
        .map(|s| s.to_string())
        .collect();
    if let Some(ref summary) = primary_ctx.summary {
        terms.extend(summary.split_whitespace().map(|s| s.to_string()));
    }
    sparse_index.remove_document(primary_ctx.id_hash);
    let doc_len = terms.len() as u32;
    sparse_index.add_document(primary_ctx.id_hash, terms, doc_len);

    for &sec_hash in &secondary_hashes {
        let sec_page_ref = btree.search(sec_hash).unwrap();
        let sec_page_id = (sec_page_ref >> 16) as u32;

        sparse_index.remove_document(sec_hash);

        crate::file::free_list::free_page(mmap, header, sec_page_id)?;

        btree.remove(sec_hash);
    }

    Ok(TopicDetail {
        id: format!("{:016x}", primary_ctx.id_hash),
        title: primary_ctx.title,
        summary: primary_ctx.summary,
        depth: primary_ctx.depth,
        archive_refs: primary_ctx
            .archive_refs
            .iter()
            .map(|id| format!("{:016x}", id))
            .collect(),
        l3_refs: primary_ctx
            .l3_refs
            .iter()
            .map(|id| format!("{:016x}", id))
            .collect(),
        turn_count: primary_ctx.turn_count,
        parent_id: primary_ctx.parent_id.map(|id| format!("{:016x}", id)),
        is_active: primary_ctx.is_active,
        importance: primary_ctx.importance,
        activation_score: primary_ctx.activation_score,
        activation_state: format!("{:?}", primary_ctx.activation_state),
        created_at: primary_ctx.created_at,
        updated_at: primary_ctx.updated_at,
        llm_params: Some(LlmParams {
            temperature: primary_ctx.llm_params.temperature,
            top_p: primary_ctx.llm_params.top_p,
            presence_penalty: primary_ctx.llm_params.presence_penalty,
            frequency_penalty: primary_ctx.llm_params.frequency_penalty,
        }),
    })
}
