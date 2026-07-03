// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! update_memory() interface with multi-level联动 updates.

use crate::config::MemHopConfig;
use crate::file::free_list::allocate_or_extend;
use crate::file::header::FileHeader;
use crate::file::page::PageHeader;
use crate::index::btree::BTreeIndex;
use crate::index::sparse::SparseIndex;
use crate::l3;
use crate::layers::archive::ArchiveSlot;
use crate::layers::hypergraph::{HypergraphNode, HypergraphSlot, HypergraphSource};
use crate::organize::extract_keywords;
use crate::query::types::*;
use crate::shared::common::{format_hash, now_ms, parse_id_to_hash};
use crate::util::{hash_id, PageType, DEFAULT_GROW_PAGES, PAGE_SIZE, SENTINEL_PAGE_ID};
use crate::MemHopError;
use memmap2::MmapMut;
use std::fs::File;

/// Write a proper PageHeader for a newly allocated data page
fn write_slot_page_header(
    mmap: &mut MmapMut,
    page_id: u32,
    page_type: PageType,
    layer_id: u16,
    data_len: usize,
) {
    let mut header = PageHeader::new(page_id, page_type, layer_id, SENTINEL_PAGE_ID);
    header.slot_count = 1;
    header.free_bytes = (PAGE_SIZE - 32).saturating_sub(data_len) as u16;
    let header_bytes = header.to_bytes();
    let offset = crate::shared::slot_io::page_offset(page_id);
    mmap[offset..offset + 32].copy_from_slice(&header_bytes);
}

/// Core update implementation: writes L4 archive + L5 crystal, updates L2 context.
#[allow(clippy::too_many_arguments)]
pub fn update_memory(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    request: UpdateRequest,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    file: &mut File,
    config: &MemHopConfig,
    mut tracker: Option<&mut crate::l3::DegreeTracker>,
    mut index_map: Option<&mut std::collections::HashMap<u64, crate::l3::L3Index>>,
) -> Result<UpdateResult, MemHopError> {
    let now_ms = now_ms();

    // NOTE: topic_id comes from format_hash(id_hash) — a hex string.
    // parse_id_to_hash reverses format_hash; hash_id would hash the hex string itself.
    let topic_hash = parse_id_to_hash(&request.topic_id);
    let page_ref = btree
        .search(topic_hash)
        .ok_or(MemHopError::PageNotFound(0))?;

    let l4_id_hash = hash_id(&format!("L4-{}-{}", topic_hash, now_ms));
    allocate_and_write_l4_archive(
        mmap,
        header,
        l4_id_hash,
        &request.dialogue_text,
        topic_hash,
        now_ms,
        btree,
        request.source.to_metadata_json(),
        file,
    )?;
    let archive_id = format_hash(l4_id_hash);

    if let Some(ref action_chain) = request.action_chain {
        for action in action_chain {
            let crystal_id_hash = hash_id(&format!(
                "{}-{:?}-{}",
                topic_hash, action.action_type, now_ms
            ));
            allocate_and_write_l5_crystal(
                mmap,
                header,
                crystal_id_hash,
                &action.title,
                &action.description,
                now_ms,
                btree,
                file,
            )?;
        }
    }

    let data = &mmap[..];
    let page_id = crate::shared::slot_io::decode_page_id(page_ref);
    let slot_data = crate::shared::slot_io::get_slot_data(data, page_ref)
        .ok_or(MemHopError::PageNotFound(page_id))?;

    let mut ctx = crate::layers::context::ContextSlot::deserialize_slot(slot_data)?;

    // A brand-new context (e.g. from auto_create) has no archives and no turns yet.
    let is_fresh_context = ctx.turn_count == 0 && ctx.archive_refs.is_empty();

    if !ctx.archive_refs.contains(&l4_id_hash) {
        ctx.archive_refs.push(l4_id_hash);
        ctx.archive_refs.sort();
    }

    ctx.turn_count += 1;

    if let Some(ref summary) = request.summary {
        match ctx.summary {
            Some(ref existing) => {
                ctx.summary = Some(format!("{}\n{}", existing, summary));
            }
            None => {
                ctx.summary = Some(summary.clone());
            }
        }
        ctx.updated_at = now_ms;
    }

    if request.instant_distill {
        let keywords = extract_keywords(&request.dialogue_text, 10);
        let mut graphs_to_link: Vec<u64> = Vec::new();
        let data: &[u8] = &mmap[..];
        for kw in &keywords {
            let hits = sparse_index.entity_search_nodes(kw);
            for (node_hash, _l2_ids) in &hits {
                if let Some(slot_data) = btree
                    .search(*node_hash)
                    .and_then(|pr| crate::shared::slot_io::get_slot_data(data, pr))
                {
                    if let Ok(node) =
                        crate::layers::hypergraph::HypergraphNode::deserialize(slot_data)
                    {
                        if !ctx.l3_refs.contains(&node.graph_id) {
                            graphs_to_link.push(node.graph_id);
                        }
                    }
                }
            }
        }

        if graphs_to_link.is_empty() && !keywords.is_empty() {
            let distilled_id = hash_id(&format!("distilled_{}_{}", topic_hash, now_ms));
            let graph_name = format!(
                "distilled:{}",
                &request.dialogue_text.chars().take(40).collect::<String>()
            );

            let slot = HypergraphSlot {
                id_hash: distilled_id,
                name: graph_name,
                source: HypergraphSource::Manual,
                node_count: keywords.len() as u32,
                edge_count: 0,
                created_at: now_ms,
                updated_at: now_ms,
                version: 1,
            };

            let slot_data = slot
                .serialize()
                .map_err(|e| MemHopError::Serialization(e.to_string()))?;

            let slot_page_id = allocate_or_extend(mmap, header, file, DEFAULT_GROW_PAGES)?;
            let slot_offset = crate::shared::slot_io::page_offset(slot_page_id);

            let page_hdr =
                PageHeader::new(slot_page_id, PageType::HypergraphSlot, 3, SENTINEL_PAGE_ID);
            mmap[slot_offset..slot_offset + 32].copy_from_slice(&page_hdr.to_bytes());

            let data_offset = slot_offset + 32;
            if data_offset + slot_data.len() <= mmap.len() {
                mmap[data_offset..data_offset + slot_data.len()].copy_from_slice(&slot_data);
            }

            btree.insert(distilled_id, (slot_page_id as u64) << 16);

            for kw in &keywords {
                let node_hash = hash_id(&format!("distilled_node_{}_{}", distilled_id, kw));
                let node = HypergraphNode {
                    id_hash: node_hash,
                    graph_id: distilled_id,
                    title: kw.clone(),
                    node_type: "concept".to_string(),
                    content: String::new(),
                    keywords: vec![kw.clone()],
                    source_ref: None,
                    importance: 0.5,
                    created_at: now_ms,
                    updated_at: now_ms,
                    version: 1,
                };
                l3::store::add_node(
                    mmap,
                    header,
                    btree,
                    node,
                    file,
                    tracker.as_deref_mut(),
                    index_map.as_deref_mut(),
                )?;
            }

            graphs_to_link.push(distilled_id);
        }

        // Deduplicate and append to l3_refs
        graphs_to_link.sort();
        graphs_to_link.dedup();
        ctx.l3_refs.extend(graphs_to_link);
    }

    let serialized = ctx
        .serialize()
        .map_err(|e| MemHopError::Serialization(format!("ContextSlot serialize: {}", e)))?;
    let write_offset = crate::shared::slot_io::slot_offset(page_id);
    if write_offset + serialized.len() > mmap.len() {
        return Err(MemHopError::Serialization(format!(
            "ContextSlot too large for page: {} > {}",
            serialized.len(),
            PAGE_SIZE - 32
        )));
    }
    mmap[write_offset..write_offset + serialized.len()].copy_from_slice(&serialized);

    if let Some(ref summary) = request.summary {
        let terms: Vec<String> = summary
            .split_whitespace()
            .map(|s| s.to_lowercase())
            .filter(|s| !s.is_empty())
            .collect();
        sparse_index.add_document(topic_hash, terms, summary.len() as u32);
    }

    // Determine update status based on what actually changed.
    let status = if is_fresh_context {
        UpdateStatus::Created
    } else if request.summary.is_some() || request.action_chain.is_some() || request.instant_distill
    {
        UpdateStatus::Updated
    } else {
        UpdateStatus::Archived
    };

    // Check if archive/summary thresholds exceeded for auto-dream trigger
    // Thresholds are read from config; default to 20 archives / 2048 summary bytes.
    let archive_threshold = config.auto_dream_archive_threshold.unwrap_or(20);
    let summary_threshold = config.auto_dream_summary_bytes.unwrap_or(2048);
    let dream_triggered = ctx.archive_refs.len() >= archive_threshold
        || ctx.summary.as_ref().map(|s| s.len()).unwrap_or(0) >= summary_threshold;

    Ok(UpdateResult {
        topic_id: format_hash(topic_hash),
        archive_id,
        status,
        dream_triggered,
    })
}

#[allow(clippy::too_many_arguments)]
fn allocate_and_write_l4_archive(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    id_hash: u64,
    content: &str,
    topic_id: u64,
    now_ms: i64,
    btree: &mut BTreeIndex,
    metadata: Option<String>,
    file: &mut File,
) -> Result<u64, MemHopError> {
    let page_id = allocate_or_extend(mmap, header, file, DEFAULT_GROW_PAGES)?;
    let offset = crate::shared::slot_io::slot_offset(page_id);

    use crate::layers::archive::ContentType;
    let archive = ArchiveSlot {
        id_hash,
        content_type: ContentType::Text,
        role: 0, // user
        context_id: topic_id,
        created_at: now_ms,
        content: content.to_string(),
        metadata,
    };

    let data = archive
        .serialize()
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;
    write_slot_page_header(mmap, page_id, PageType::Archive, 4, data.len());
    if offset + data.len() > mmap.len() {
        return Err(MemHopError::Serialization(format!(
            "ArchiveSlot data too large for page: {} > {}",
            data.len(),
            mmap.len() - offset
        )));
    }
    mmap[offset..offset + data.len()].copy_from_slice(&data);

    let page_ref = (page_id as u64) << 16;
    btree.insert(id_hash, page_ref);

    Ok(page_ref)
}

/// Allocate page and write L5 ActionChainSlot
#[allow(clippy::too_many_arguments)]
fn allocate_and_write_l5_crystal(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    id_hash: u64,
    action_title: &str,
    action_description: &str,
    now_ms: i64,
    btree: &mut BTreeIndex,
    file: &mut File,
) -> Result<u64, MemHopError> {
    let page_id = allocate_or_extend(mmap, header, file, DEFAULT_GROW_PAGES)?;
    let offset = crate::shared::slot_io::slot_offset(page_id);

    use crate::layers::action_chain::ActionChainSlot;
    let chain = ActionChainSlot {
        id_hash,
        title: action_title.to_string(),
        trigger: action_description.to_string(),
        status: crate::layers::action_chain::ChainStatus::Active,
        confidence: 0.8,
        success_rate: 1.0,
        trigger_count: 0,
        last_triggered: 0,
        created_at: now_ms,
        updated_at: now_ms,
        version: 1,
    };

    let data = chain
        .serialize()
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;
    write_slot_page_header(mmap, page_id, PageType::ActionChain, 5, data.len());
    if offset + data.len() > mmap.len() {
        return Err(MemHopError::Serialization(format!(
            "ActionChainSlot data too large for page: {} > {}",
            data.len(),
            mmap.len() - offset
        )));
    }
    mmap[offset..offset + data.len()].copy_from_slice(&data);

    let page_ref = (page_id as u64) << 16;
    btree.insert(id_hash, page_ref);

    Ok(page_ref)
}
