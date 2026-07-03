// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// search_memory() interface with L2-centric retrieval model.
// Triple retrieval (vector + BM25 + entity) on L2 ContextSlot titles.

#![cfg_attr(not(feature = "grpc-encoder"), allow(dead_code, unused_imports))]

use crate::config::SearchWeights;
use crate::file::header::FileHeader;
use crate::file::page::decode_page_ref;
use crate::index::btree::BTreeIndex;
use crate::index::sparse::SparseIndex;
use crate::index::vector::{cosine_similarity, ivf_knn, read_vector, IVFIndex};
use crate::l3::store::page_type_of;
use crate::layers::archive::ArchiveSlot;
use crate::layers::context::ContextSlot;
use crate::layers::context_node::ContextNode;
use crate::layers::hyperedge::HyperedgeSlot;
use crate::layers::hypergraph::{HypergraphNode, HypergraphSlot};
use crate::query::types::*;
use crate::shared::common::{self, format_hash};
use crate::shared::slot_io::{decode_page_id, get_slot_data};
use crate::util::{hash_id, PageType, PAGE_SIZE};
use crate::util::{DEFAULT_GROW_PAGES, SENTINEL_PAGE_ID};
use crate::MemHopError;
use memmap2::MmapMut;
use std::collections::{HashMap, HashSet};
use std::fs::File;

/// Safely slice a UTF-8 string by character count, not byte count.
fn safe_char_slice(s: &str, max_chars: usize) -> String {
    if s.chars().count() <= max_chars {
        s.to_string()
    } else {
        s.chars().take(max_chars).collect()
    }
}

// ============================================================================
// 3-way merge & rank
// ============================================================================

/// Configuration for merge_and_rank function
struct MergeConfig {
    entity_weight: f32,
    bm25_weight: f32,
    vector_weight: f32,
    limit: usize,
    min_score: f32,
}

/// Merge entity, BM25 and vector retrieval results using weighted fusion
fn merge_and_rank(
    entity_results: Vec<(ContextSlot, f32)>,
    bm25_results: Vec<(ContextSlot, f32)>,
    vector_results: Vec<(ContextSlot, f32)>,
    config: MergeConfig,
) -> Vec<(ContextSlot, f32)> {
    let entity_max = entity_results
        .iter()
        .map(|(_, s)| *s)
        .fold(0.0f32, f32::max);
    let bm25_max = bm25_results.iter().map(|(_, s)| *s).fold(0.0f32, f32::max);
    let vector_max = vector_results
        .iter()
        .map(|(_, s)| *s)
        .fold(0.0f32, f32::max);

    let mut score_map: HashMap<u64, (f32, f32, f32)> = HashMap::new();
    let mut ctx_map: HashMap<u64, ContextSlot> = HashMap::new();

    for (ctx, score) in entity_results {
        let n = if entity_max > 0.0 {
            score / entity_max
        } else {
            0.0
        };
        score_map.entry(ctx.id_hash).or_insert((0.0, 0.0, 0.0)).0 = n;
        ctx_map.entry(ctx.id_hash).or_insert(ctx);
    }

    for (ctx, score) in bm25_results {
        let n = if bm25_max > 0.0 {
            score / bm25_max
        } else {
            0.0
        };
        score_map.entry(ctx.id_hash).or_insert((0.0, 0.0, 0.0)).1 = n;
        ctx_map.entry(ctx.id_hash).or_insert(ctx);
    }

    for (ctx, score) in vector_results {
        let n = if vector_max > 0.0 {
            score / vector_max
        } else {
            0.0
        };
        score_map.entry(ctx.id_hash).or_insert((0.0, 0.0, 0.0)).2 = n;
        ctx_map.entry(ctx.id_hash).or_insert(ctx);
    }

    let mut scored: Vec<(u64, f32)> = score_map
        .into_iter()
        .map(|(id, (en, bm, vc))| {
            (
                id,
                config.entity_weight * en + config.bm25_weight * bm + config.vector_weight * vc,
            )
        })
        .filter(|(_, s)| *s >= config.min_score)
        .collect();

    scored.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
    scored.truncate(config.limit);

    scored
        .into_iter()
        .filter_map(|(id, score)| ctx_map.remove(&id).map(|ctx| (ctx, score)))
        .collect()
}

// ============================================================================
// Core search
// ============================================================================

/// Core search implementation
///
/// Routing priority:
///   1. `auto_create == 1`  → skip retrieval, create new L2
///   2. `context_id` present → load that L2, skip triple retrieval, L1-associate only
///   3. `l3_id` present      → restrict candidate pool to L2s containing that L3, then triple retrieve
///   4. default              → full triple retrieval on all depth-1/2/3 contexts
#[allow(clippy::too_many_arguments)]
#[cfg(feature = "grpc-encoder")]
pub fn search_memory(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    query: SearchQuery,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    vector_dim: usize,
    encoder: Option<&(dyn crate::encoder::Encoder + Send + Sync)>,
    l1_reverse: &L1ReverseIndex,
    file: &mut File,
    search_weights: &SearchWeights,
    ivf_index: Option<&IVFIndex>,
) -> Result<SearchResult, MemHopError> {
    let _page_count = header.page_count;

    // ========================================================================
    // Route 1: auto_create
    // ========================================================================
    let filtered_l2 = if query.auto_create == 1 {
        let new_ctx = create_new_l2_context(
            mmap,
            header,
            btree,
            sparse_index,
            &query.dialogue,
            vector_dim,
            file,
            encoder,
        )?;
        vec![(new_ctx, 1.0)]

    // ========================================================================
    // Route 2: context_id
    // ========================================================================
    } else if let Some(ref cid) = query.context_id {
        let target_hash = common::parse_id_to_hash(cid);
        let data: &[u8] = &mmap[..];

        if let Some(slot_data) = btree
            .search(target_hash)
            .and_then(|pr| get_slot_data(data, pr))
        {
            match ContextSlot::deserialize_slot(slot_data) {
                Ok(ctx) => {
                    vec![(ctx, 1.0)]
                }
                Err(_) => {
                    vec![] // deserialization failed, treat as not found
                }
            }
        } else {
            vec![] // not found in btree
        }

    // ========================================================================
    // Route 3 & 4: triple retrieval (optionally scoped by l3_id)
    // ========================================================================
    } else {
        let search_text = query.dialogue.clone();

        // If l3_id is set, restrict retrieval to L2 candidates containing this L3.
        let l3_scope: Option<HashSet<u64>> = if let Some(ref l3_id_str) = query.l3_id {
            // l3_id is a hex string; parse to raw id_hash, not re-hash.
            let l3_hash = common::parse_id_to_hash(l3_id_str);
            let data: &[u8] = &mmap[..];
            Some(collect_l2_ids_with_l3(data, btree, l3_hash))
        } else {
            None
        };

        let data: &[u8] = &mmap[..];
        let fetch_limit = query.context_limit * 2;

        if !sparse_index.has_entity_index() {
            sparse_index.build_entity_index(data, btree)?;
        }

        let entity_results = retrieve_l2_entity(
            data,
            &search_text,
            sparse_index,
            btree,
            fetch_limit,
            l3_scope.as_ref(),
        )?;

        let bm25_results = retrieve_l2_bm25(
            data,
            &search_text,
            sparse_index,
            btree,
            fetch_limit,
            l3_scope.as_ref(),
        )?;

        let vector_results = if let Some(enc) = encoder {
            let output = enc.encode(&search_text)?;
            if !output.dense.is_empty() {
                retrieve_l2_vector(
                    data,
                    &output.dense,
                    btree,
                    vector_dim,
                    fetch_limit,
                    query.min_score,
                    l3_scope.as_ref(),
                    ivf_index,
                    search_weights.n_probes,
                )?
            } else {
                vec![]
            }
        } else {
            return Err(MemHopError::EncoderError(
                "No encoder configured for vector search".to_string(),
            ));
        };

        let config = MergeConfig {
            entity_weight: search_weights.entity_weight,
            bm25_weight: search_weights.bm25_weight,
            vector_weight: search_weights.vector_weight,
            limit: query.context_limit,
            min_score: query.min_score,
        };
        merge_and_rank(entity_results, bm25_results, vector_results, config)
    };

    let data: &[u8] = &mmap[..];
    let l1_associated = get_l1_associated_depth1(data, &filtered_l2, btree, l1_reverse)?;

    // Merge L1 associated into filtered_l2 for maximum recall (Deep mode is always active).
    let mut filtered_l2 = filtered_l2;
    filtered_l2.extend(l1_associated.clone());

    let l0_profile = crate::query::l0_crud::read_profile(mmap, btree)?;

    let (l3_ids, l3_previews) = collect_l3_previews(mmap, &filtered_l2, btree)?;
    let archive_refs = collect_archive_refs(data, &filtered_l2, btree)?;

    let filtered_l2_slots: Vec<ContextSlot> =
        filtered_l2.iter().map(|(ctx, _)| ctx.clone()).collect();
    update_activation_scores(mmap, &filtered_l2_slots, btree)?;
    boost_l1_importance_on_retrieval(mmap, &filtered_l2_slots, btree, l1_reverse)?;

    let result = SearchResult {
        profile: l0_profile,
        contexts: convert_contexts(&filtered_l2),
        associated_contexts: convert_contexts(&l1_associated),
        l3_ids,
        l3_previews,
        archive_refs,
    };

    Ok(result)
}

// ============================================================================
// Retrieval: entity matching
// ============================================================================

/// Retrieve L2 contexts using entity matching against the L3 hypergraph.
///
/// If `l3_scope` is Some, only accept candidates whose id_hash is in the set.
fn retrieve_l2_entity(
    data: &[u8],
    query_text: &str,
    sparse_index: &SparseIndex,
    btree: &BTreeIndex,
    limit: usize,
    l3_scope: Option<&HashSet<u64>>,
) -> Result<Vec<(ContextSlot, f32)>, MemHopError> {
    let hits = sparse_index.entity_search(query_text);
    let mut scored = Vec::new();

    for (id_hash, score) in hits {
        if let Some(scope) = l3_scope {
            if !scope.contains(&id_hash) {
                continue;
            }
        }
        if let Some(slot_data) = btree.search(id_hash).and_then(|pr| get_slot_data(data, pr)) {
            if let Ok(ctx) = ContextSlot::deserialize(slot_data) {
                if ctx.depth <= 3 {
                    let weighted_score = if ctx.depth == 3 { score * 0.5 } else { score };
                    scored.push((ctx, weighted_score));
                }
            }
        }
    }

    scored.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
    scored.truncate(limit);
    Ok(scored)
}

// ============================================================================
// Retrieval: BM25
// ============================================================================

/// Retrieve L2 contexts using BM25 word-level scoring
///
/// If `l3_scope` is Some, only accept candidates whose id_hash is in the set.
fn retrieve_l2_bm25(
    data: &[u8],
    query_text: &str,
    sparse_index: &SparseIndex,
    btree: &BTreeIndex,
    limit: usize,
    l3_scope: Option<&HashSet<u64>>,
) -> Result<Vec<(ContextSlot, f32)>, MemHopError> {
    let terms: Vec<String> = query_text
        .split_whitespace()
        .map(|s| s.to_string())
        .collect();
    if terms.is_empty() {
        return Ok(vec![]);
    }

    let hits = sparse_index.search(&terms, limit * 2);
    let mut scored = Vec::new();

    for (id_hash, score) in hits {
        if let Some(scope) = l3_scope {
            if !scope.contains(&id_hash) {
                continue;
            }
        }
        let page_ref = match btree.search(id_hash) {
            Some(pr) => pr,
            None => continue,
        };
        if page_type_of(data, page_ref) == Some(PageType::HypergraphNode as u16) {
            // L3 virtual document hit: resolve to associated L2 contexts
            let l2_ids = sparse_index.entity_index().l2_ids_for_node(id_hash);
            for l2_id in l2_ids {
                if let Some(l2_data) = btree.search(l2_id).and_then(|pr| get_slot_data(data, pr)) {
                    if let Ok(ctx) = ContextSlot::deserialize(l2_data) {
                        if ctx.depth <= 3 {
                            let weighted = if ctx.depth == 3 { score * 0.5 } else { score };
                            scored.push((ctx, weighted));
                        }
                    }
                }
            }
            continue;
        }
        if let Some(slot_data) = get_slot_data(data, page_ref) {
            if let Ok(ctx) = ContextSlot::deserialize(slot_data) {
                if ctx.depth <= 3 {
                    let weighted_score = if ctx.depth == 3 { score * 0.5 } else { score };
                    scored.push((ctx, weighted_score));
                }
            }
        }
    }

    scored.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
    scored.truncate(limit);
    Ok(scored)
}

// ============================================================================
// Retrieval: Vector similarity
// ============================================================================

/// Retrieve L2 contexts using vector cosine similarity on centroid vectors
///
/// If `l3_scope` is Some, only accept candidates whose id_hash is in the set.
#[allow(clippy::too_many_arguments)]
fn retrieve_l2_vector(
    data: &[u8],
    query_vector: &[half::f16],
    btree: &BTreeIndex,
    vector_dim: usize,
    limit: usize,
    min_score: f32,
    l3_scope: Option<&HashSet<u64>>,
    ivf_index: Option<&IVFIndex>,
    n_probes: usize,
) -> Result<Vec<(ContextSlot, f32)>, MemHopError> {
    // Fast path: use IVF index if available and non-empty
    if let Some(ivf) = ivf_index {
        if !ivf.centroids.is_empty() && !ivf.buckets.is_empty() {
            let effective_probes = if n_probes > 0 { n_probes } else { 8 };
            let candidates =
                ivf_knn(ivf, data, query_vector, limit * 2, effective_probes).unwrap_or_default();

            let mut results = Vec::new();
            for (id_hash, score) in candidates {
                if min_score > 0.0 && score < min_score {
                    continue;
                }
                if let Some(page_ref) = btree.search(id_hash) {
                    if let Some(slot_data) = get_slot_data(data, page_ref) {
                        if let Ok(ctx) = ContextSlot::deserialize_slot(slot_data) {
                            let weighted_score = if ctx.depth == 3 { score * 0.5 } else { score };
                            results.push((ctx, weighted_score));
                        }
                    }
                }
            }

            results.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
            results.truncate(limit);
            return Ok(results);
        }
    }

    // Fallback: brute-force linear scan (original logic)
    let mut candidates = Vec::new();

    for (&id_hash, &page_ref) in btree.iter() {
        if let Some(scope) = l3_scope {
            if !scope.contains(&id_hash) {
                continue;
            }
        }

        let (page_id, _slot_idx) = decode_page_ref(page_ref);
        let offset = crate::shared::slot_io::slot_offset(page_id);

        if offset >= data.len() {
            continue;
        }

        if let Ok(ctx) = ContextSlot::deserialize(&data[offset..]) {
            if ctx.depth > 3 {
                continue;
            }

            if ctx.centroid_page_ref == 0 {
                continue;
            }

            let (vec_page, vec_slot) = decode_page_ref(ctx.centroid_page_ref);
            if let Ok(centroid) = read_vector(data, vec_page, vec_slot, vector_dim) {
                if centroid.len() == vector_dim {
                    let score = cosine_similarity(query_vector, &centroid);
                    if min_score > 0.0 && score < min_score {
                        continue;
                    }
                    let weighted_score = if ctx.depth == 3 { score * 0.5 } else { score };
                    candidates.push((ctx, weighted_score));
                }
            }
        }
    }

    candidates.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
    candidates.truncate(limit);
    Ok(candidates)
}

// ============================================================================
// l3_id pre-filter
// ============================================================================

/// Scan all BTree entries, deserialize as ContextSlot, collect id_hashes
/// whose `l3_refs` contain the target L3 hash.
///
/// This is used to pre-filter the retrieval candidate pool when `l3_id` is set.
fn collect_l2_ids_with_l3(data: &[u8], btree: &BTreeIndex, l3_hash: u64) -> HashSet<u64> {
    let mut result = HashSet::new();

    for (&id_hash, &page_ref) in btree.iter() {
        // 2-byte page_type read is much lighter than full deserialization
        if page_type_of(data, page_ref) != Some(PageType::Context as u16) {
            continue;
        }
        if let Some(slot_data) = get_slot_data(data, page_ref) {
            if let Ok(ctx) = ContextSlot::deserialize(slot_data) {
                if ctx.l3_refs.contains(&l3_hash) {
                    result.insert(id_hash);
                }
            }
        }
    }

    result
}

// ============================================================================
// L1 reverse index
// ============================================================================

/// L1 reverse index: maps an L2 `context_id` to the L1 `ContextNode`(s) that
/// point to it. Avoids O(N) btree scan for associated context lookups.
#[derive(Debug, Clone, Default)]
pub struct L1ReverseIndex {
    index: HashMap<u64, Vec<(u64, u64)>>,
}

impl L1ReverseIndex {
    pub fn new() -> Self {
        Self {
            index: HashMap::new(),
        }
    }

    /// Build the reverse index by scanning the btree once.
    pub fn build(data: &[u8], btree: &BTreeIndex) -> Result<Self, MemHopError> {
        let mut idx = Self::new();
        for (&id_hash, &page_ref) in btree.iter() {
            let page_id = decode_page_id(page_ref);
            let page_header = match crate::file::page::read_page_header(data, page_id) {
                Ok(h) => h,
                Err(_) => continue,
            };
            if page_header.page_type != crate::util::PageType::ContextNode as u16 {
                continue;
            }
            if let Some(slot_data) = get_slot_data(data, page_ref) {
                if let Ok(node) = ContextNode::deserialize(slot_data) {
                    if node.context_id != 0 {
                        idx.add(node.context_id, id_hash, page_ref);
                    }
                }
            }
        }
        Ok(idx)
    }

    /// Add (or refresh) a node entry for a given `context_id`.
    pub fn add(&mut self, context_id: u64, node_id_hash: u64, node_page_ref: u64) {
        let entry = self.index.entry(context_id).or_default();
        entry.retain(|(nid, _)| *nid != node_id_hash);
        entry.push((node_id_hash, node_page_ref));
    }

    pub fn remove_context(&mut self, context_id: u64) {
        self.index.remove(&context_id);
    }

    pub fn remove_node(&mut self, node_id_hash: u64) {
        for nodes in self.index.values_mut() {
            nodes.retain(|(nid, _)| *nid != node_id_hash);
        }
    }

    /// O(1) lookup: given `context_id`s, return associated L1 nodes (deduplicated).
    pub fn find_associated(&self, context_ids: &HashSet<u64>) -> Vec<(u64, u64)> {
        let mut seen = HashSet::new();
        let mut result = Vec::new();
        for &ctx_id in context_ids {
            if let Some(nodes) = self.index.get(&ctx_id) {
                for &(node_id, page_ref) in nodes {
                    if seen.insert(node_id) {
                        result.push((node_id, page_ref));
                    }
                }
            }
        }
        result
    }

    pub fn serialize(&self) -> Result<Vec<u8>, MemHopError> {
        bincode::serialize(&self.index).map_err(|e| MemHopError::Serialization(e.to_string()))
    }

    pub fn deserialize(data: &[u8]) -> Result<Self, MemHopError> {
        let index: HashMap<u64, Vec<(u64, u64)>> =
            bincode::deserialize(data).map_err(|e| MemHopError::Serialization(e.to_string()))?;
        Ok(Self { index })
    }
}

// ============================================================================
// L1 association
// ============================================================================

/// Via L1 hypergraph, find associated L2 contexts for matched contexts.
///
/// Uses L1 reverse index to find ContextNodes, traverses hyperedges to
/// discover sibling nodes, then loads their associated L2 ContextSlots.
fn get_l1_associated_depth1(
    data: &[u8],
    matched: &[(ContextSlot, f32)],
    btree: &BTreeIndex,
    l1_reverse: &L1ReverseIndex,
) -> Result<Vec<(ContextSlot, f32)>, MemHopError> {
    if matched.is_empty() {
        return Ok(vec![]);
    }

    let matched_ids: HashSet<u64> = matched.iter().map(|(c, _)| c.id_hash).collect();
    let mut seen: HashSet<u64> = matched_ids.clone(); // exclude already-matched
    let mut weighted_results: Vec<(ContextSlot, f32)> = Vec::new();

    let associated_nodes = l1_reverse.find_associated(&matched_ids);
    for (_node_hash, page_ref) in associated_nodes {
        if let Some(slot_data) = get_slot_data(data, page_ref) {
            if let Ok(node) = ContextNode::deserialize(slot_data) {
                for &edge_hash in &node.edge_ptrs {
                    if let Some(edge_data) = btree
                        .search(edge_hash)
                        .and_then(|pr| get_slot_data(data, pr))
                    {
                        if let Ok(hyperedge) = HyperedgeSlot::deserialize(edge_data) {
                            for &sibling_hash in &hyperedge.node_ptrs {
                                if let Some(sib_data) = btree
                                    .search(sibling_hash)
                                    .and_then(|pr| get_slot_data(data, pr))
                                {
                                    if let Ok(sibling_node) = ContextNode::deserialize(sib_data) {
                                        let ctx_id = sibling_node.context_id;
                                        if seen.contains(&ctx_id) {
                                            continue;
                                        }
                                        if let Some(ctx_data) = btree
                                            .search(ctx_id)
                                            .and_then(|pr| get_slot_data(data, pr))
                                        {
                                            if let Ok(ctx) = ContextSlot::deserialize(ctx_data) {
                                                seen.insert(ctx_id);
                                                let assoc_weight =
                                                    hyperedge.weight * sibling_node.importance;
                                                weighted_results.push((ctx, assoc_weight));
                                            }
                                        }
                                    }
                                }
                            }
                        }
                    }
                }
            }
        }
    }

    // Also include parent contexts of matched contexts (weight = parent importance)
    for (ctx, _) in matched {
        if let Some(parent_id) = ctx.parent_id {
            if seen.contains(&parent_id) {
                continue;
            }
            if let Some(parent_data) = btree
                .search(parent_id)
                .and_then(|pr| get_slot_data(data, pr))
            {
                if let Ok(parent) = ContextSlot::deserialize(parent_data) {
                    seen.insert(parent_id);
                    let parent_importance = parent.importance;
                    weighted_results.push((parent, parent_importance));
                }
            }
        }
    }

    weighted_results.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));

    Ok(weighted_results)
}

// ============================================================================
// L3 previews
// ============================================================================

/// Collect L3 previews from matched contexts (single BTree traversal)
fn collect_l3_previews(
    mmap: &MmapMut,
    contexts: &[(ContextSlot, f32)],
    btree: &BTreeIndex,
) -> Result<(Vec<String>, Vec<L3Preview>), MemHopError> {
    let data: &[u8] = &mmap[..];

    let mut graph_ids: HashSet<u64> = HashSet::new();
    for (ctx, _) in contexts {
        for &l3_hash in &ctx.l3_refs {
            graph_ids.insert(l3_hash);
        }
    }
    let l3_ids: Vec<String> = graph_ids.iter().map(|h| format_hash(*h)).collect();

    if graph_ids.is_empty() {
        return Ok((l3_ids, Vec::new()));
    }

    // Single BTree traversal: collect HypergraphSlot and HypergraphNode
    let mut slots: HashMap<u64, HypergraphSlot> = HashMap::new();
    let mut nodes_by_graph: HashMap<u64, Vec<HypergraphNode>> = HashMap::new();

    for (&_id, &page_ref) in btree.iter() {
        let pt = page_type_of(data, page_ref);
        if pt == Some(PageType::HypergraphSlot as u16) {
            if let Some(slot_data) = get_slot_data(data, page_ref) {
                if let Ok(slot) = HypergraphSlot::deserialize(slot_data) {
                    if graph_ids.contains(&slot.id_hash) {
                        slots.insert(slot.id_hash, slot);
                    }
                }
            }
        } else if pt == Some(PageType::HypergraphNode as u16) {
            if let Some(slot_data) = get_slot_data(data, page_ref) {
                if let Ok(node) = HypergraphNode::deserialize(slot_data) {
                    if graph_ids.contains(&node.graph_id) {
                        nodes_by_graph.entry(node.graph_id).or_default().push(node);
                    }
                }
            }
        }
    }

    let mut previews = Vec::new();
    for &gid in &graph_ids {
        if let Some(slot) = slots.get(&gid) {
            let mut nodes = nodes_by_graph.remove(&gid).unwrap_or_default();
            nodes.sort_by(|a, b| {
                b.importance
                    .partial_cmp(&a.importance)
                    .unwrap_or(std::cmp::Ordering::Equal)
            });
            let top_nodes: Vec<String> = nodes.iter().take(5).map(|n| n.title.clone()).collect();
            let mut keywords: Vec<String> = nodes
                .iter()
                .flat_map(|n| n.keywords.clone())
                .collect::<HashSet<_>>()
                .into_iter()
                .collect();
            keywords.sort(); // Ensure deterministic output order
            previews.push(L3Preview {
                id: format_hash(gid),
                title: slot.name.clone(),
                top_nodes,
                keywords,
                node_count: slot.node_count,
            });
        }
    }
    Ok((l3_ids, previews))
}

// ============================================================================
// L4 archive references
// ============================================================================

/// Collect L4 archive references from matched contexts, loading metadata
fn collect_archive_refs(
    data: &[u8],
    contexts: &[(ContextSlot, f32)],
    btree: &BTreeIndex,
) -> Result<Vec<ArchiveRef>, MemHopError> {
    let mut refs = Vec::new();
    let mut seen = HashSet::new();

    for (ctx, _) in contexts {
        for &arc_hash in &ctx.archive_refs {
            if !seen.insert(arc_hash) {
                continue;
            }
            if let Some(slot_data) = btree
                .search(arc_hash)
                .and_then(|pr| get_slot_data(data, pr))
            {
                if let Ok(arc) = ArchiveSlot::deserialize_slot(slot_data) {
                    let src = arc.request_source();
                    refs.push(ArchiveRef {
                        id: format_hash(arc.id_hash),
                        context_id: format_hash(arc.context_id),
                        content_type: arc.content_type.as_str().to_string(),
                        created_at: arc.created_at,
                        source_agent: src.source_agent,
                        source_platform: src.source_platform,
                    });
                }
            }
        }
    }

    Ok(refs)
}

// ============================================================================
// Activation score update
// ============================================================================

/// Implements "spacing effect" (retrieval strengthens memory) and "decay"
/// (unretrieved memories fade) from human memory models.
///
/// - Matched: activation_score +0.1 (capped at 1.0)
/// - Unmatched active: decay handled by periodic lightweight L1 decay
fn update_activation_scores(
    mmap: &mut MmapMut,
    contexts: &[ContextSlot],
    btree: &BTreeIndex,
) -> Result<(), MemHopError> {
    let now_ms = common::now_ms();

    // Global decay of unmatched active contexts is handled by the periodic
    // lightweight L1 decay in maybe_run_lightweight_decay, not on every search.
    for ctx in contexts {
        if let Some(page_ref) = btree.search(ctx.id_hash) {
            let (page_id, _) = decode_page_ref(page_ref);
            let offset = crate::shared::slot_io::slot_offset(page_id);

            if offset + 100 <= mmap.len() {
                if let Ok(mut c) = ContextSlot::deserialize_slot(&mmap[offset..]) {
                    c.activation_score = (c.activation_score + 0.1).min(1.0);
                    c.updated_at = now_ms;

                    let buf = c
                        .serialize()
                        .map_err(|e| MemHopError::Serialization(e.to_string()))?;
                    if offset + buf.len() > mmap.len() {
                        return Err(MemHopError::Serialization(format!(
                            "ContextSlot activation update too large: {} > {}",
                            buf.len(),
                            mmap.len() - offset
                        )));
                    }
                    mmap[offset..offset + buf.len()].copy_from_slice(&buf);
                }
            }
        }
    }

    Ok(())
}

// ============================================================================
// Spacing effect: boost L1 importance on retrieval
// ============================================================================

/// Boost L1 ContextNode importance when their associated L2 contexts are retrieved.
///
/// Spacing effect: each retrieval strengthens the associative pathway.
/// Only nodes above prune threshold (0.05) are boosted.
fn boost_l1_importance_on_retrieval(
    mmap: &mut MmapMut,
    contexts: &[ContextSlot],
    _btree: &BTreeIndex,
    l1_reverse: &L1ReverseIndex,
) -> Result<(), MemHopError> {
    const BOOST_DELTA: f32 = 0.05;
    const BOOST_CAP: f32 = 1.0;
    const PRUNE_THRESHOLD: f32 = 0.05; // same as l1_decay::NODE_REMOVE_THRESHOLD

    let matched_ids: HashSet<u64> = contexts.iter().map(|c| c.id_hash).collect();
    let associated_nodes = l1_reverse.find_associated(&matched_ids);

    let now_ms = common::now_ms();

    for (_node_hash, page_ref) in associated_nodes {
        let (page_id, _slot_idx) = decode_page_ref(page_ref);
        if let Some(slot_data) = get_slot_data(&mmap[..], page_ref) {
            if let Ok(mut node) = ContextNode::deserialize_slot(slot_data) {
                if node.importance >= PRUNE_THRESHOLD {
                    node.importance = (node.importance + BOOST_DELTA).min(BOOST_CAP);
                    node.updated_at = now_ms;

                    let buf = node.serialize().map_err(|e| {
                        MemHopError::Serialization(format!("ContextNode boost serialize: {}", e))
                    })?;
                    let offset = crate::shared::slot_io::slot_offset(page_id);
                    if offset + buf.len() <= mmap.len() {
                        mmap[offset..offset + buf.len()].copy_from_slice(&buf);
                    }
                }
            }
        }
    }

    Ok(())
}

// ============================================================================
// Type conversion helpers
// ============================================================================

fn convert_contexts(contexts: &[(ContextSlot, f32)]) -> Vec<ContextResult> {
    contexts
        .iter()
        .map(|(c, score)| ContextResult {
            id: format_hash(c.id_hash),
            parent_id: c.parent_id.map(format_hash),
            depth: c.depth,
            title: c.title.clone(),
            summary: c.summary.clone(),
            activation_score: c.activation_score,
            turn_count: c.turn_count,
            l3_refs: c.l3_refs.iter().map(|h| format_hash(*h)).collect(),
            archive_refs: c.archive_refs.iter().map(|h| format_hash(*h)).collect(),
            llm_params: Some(LlmParams {
                temperature: c.llm_params.temperature,
                top_p: c.llm_params.top_p,
                presence_penalty: c.llm_params.presence_penalty,
                frequency_penalty: c.llm_params.frequency_penalty,
            }),
            retrieval_score: *score,
        })
        .collect()
}

// ============================================================================
// Auto-create L2 context
// ============================================================================

/// Create a new L2 ContextSlot from dialogue content (auto_create fast path)
#[allow(clippy::too_many_arguments)]
#[cfg(feature = "grpc-encoder")]
fn create_new_l2_context(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    dialogue: &str,
    vector_dim: usize,
    file: &mut File,
    encoder: Option<&(dyn crate::encoder::Encoder + Send + Sync)>,
) -> Result<ContextSlot, MemHopError> {
    use std::sync::atomic::{AtomicU64, Ordering};
    static COUNTER: AtomicU64 = AtomicU64::new(0);

    let now_ms = common::now_ms();

    let counter = COUNTER.fetch_add(1, Ordering::SeqCst);
    let id_str = format!(
        "ctx_{}_{}_{}",
        now_ms,
        counter,
        dialogue.chars().take(10).collect::<String>()
    );
    let id_hash = hash_id(&id_str);
    let title = safe_char_slice(dialogue, 50);

    // Encode dialogue to vector if encoder is available
    // Encoding failure is non-fatal: falls back to BM25 + Entity retrieval
    let centroid_page_ref = if let Some(enc) = encoder {
        match enc.encode(dialogue) {
            Ok(output) if !output.dense.is_empty() => {
                match crate::file::free_list::allocate_or_extend(
                    mmap,
                    header,
                    file,
                    DEFAULT_GROW_PAGES,
                ) {
                    Ok(vec_page_id) => {
                        let vec_slot_index = 0u16;
                        match crate::index::vector::write_vector(
                            mmap,
                            vec_page_id,
                            vec_slot_index,
                            id_hash,
                            &output.dense,
                            vector_dim,
                        ) {
                            Ok(()) => ((vec_page_id as u64) << 16) | (vec_slot_index as u64),
                            Err(_) => 0,
                        }
                    }
                    Err(_) => 0,
                }
            }
            _ => 0,
        }
    } else {
        0
    };

    let new_ctx = ContextSlot {
        id_hash,
        parent_id: None,
        depth: 1,
        title,
        summary: None,
        archive_refs: Vec::new(),
        l3_refs: Vec::new(),
        turn_count: 0,
        created_at: now_ms,
        updated_at: now_ms,
        version: 1,
        importance: 0.5,
        activation_score: 0.8,
        is_active: true,
        activation_state: crate::layers::context::ActivationState::Active,
        centroid_page_ref,
        dialogue_range: (now_ms, now_ms),
        llm_params: crate::layers::context::LlmParams::default(),
    };

    let ctx_data = new_ctx
        .serialize()
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;

    let page_id =
        crate::file::free_list::allocate_or_extend(mmap, header, file, DEFAULT_GROW_PAGES)?;
    let page_offset = crate::shared::slot_io::page_offset(page_id);

    let mut page_header = crate::file::page::PageHeader::new(
        page_id,
        crate::util::PageType::Context,
        2,
        SENTINEL_PAGE_ID,
    );
    page_header.slot_count = 1;
    page_header.free_bytes = (PAGE_SIZE - 32 - ctx_data.len()) as u16;
    crate::file::page::write_page_header(mmap, page_id, &page_header)?;

    let data_offset = page_offset + 32;
    if data_offset + ctx_data.len() > mmap.len() {
        return Err(MemHopError::Serialization(format!(
            "ContextSlot data too large for page: {} > {}",
            ctx_data.len(),
            mmap.len() - data_offset
        )));
    }
    mmap[data_offset..data_offset + ctx_data.len()].copy_from_slice(&ctx_data);

    let page_ref = (page_id as u64) << 16;
    btree.insert(id_hash, page_ref);

    let terms: Vec<String> = new_ctx
        .title
        .split_whitespace()
        .map(|s| s.to_string())
        .collect();
    let doc_len = terms.len() as u32;
    sparse_index.add_document(id_hash, terms, doc_len);

    Ok(new_ctx)
}

// ============================================================================
// Tests
// ============================================================================

#[cfg(test)]
mod tests {
    use super::*;
    use crate::layers::context::{ActivationState, ContextSlot};
    use crate::test_helpers::*;

    #[test]
    fn test_depth3_retrieval_weighting() {
        let (_temp, mut mmap, mut header, mut file) = create_test_mmap_with_tempfile(10);
        let mut btree = BTreeIndex::new();
        let mut sparse_index = SparseIndex::new();

        let base = ContextSlot {
            id_hash: 0,
            parent_id: None,
            depth: 1,
            title: "rust memory search".to_string(),
            summary: None,
            archive_refs: vec![],
            l3_refs: vec![],
            turn_count: 1,
            created_at: 0,
            updated_at: 0,
            version: 1,
            importance: 0.5,
            activation_score: 0.5,
            is_active: true,
            activation_state: ActivationState::Active,
            centroid_page_ref: 0,
            dialogue_range: (0, 0),
            llm_params: crate::layers::context::LlmParams::default(),
        };

        let ctx_depth1 = ContextSlot {
            id_hash: 101,
            ..base.clone()
        };
        let ctx_depth3 = ContextSlot {
            id_hash: 103,
            depth: 3,
            ..base.clone()
        };

        insert_test_context(
            &mut mmap,
            &mut header,
            &mut btree,
            &mut sparse_index,
            ctx_depth1,
            &mut file,
        );
        insert_test_context(
            &mut mmap,
            &mut header,
            &mut btree,
            &mut sparse_index,
            ctx_depth3,
            &mut file,
        );

        let data: &[u8] = &mmap[..];
        let results =
            retrieve_l2_bm25(data, "rust memory search", &sparse_index, &btree, 10, None).unwrap();

        assert_eq!(
            results.len(),
            2,
            "depth-3 contexts should be included in retrieval"
        );

        let score_depth1 = results
            .iter()
            .find(|(ctx, _)| ctx.id_hash == 101)
            .map(|(_, s)| *s)
            .unwrap();
        let score_depth3 = results
            .iter()
            .find(|(ctx, _)| ctx.id_hash == 103)
            .map(|(_, s)| *s)
            .unwrap();

        assert!(
            score_depth1 > 0.0 && score_depth3 > 0.0,
            "both contexts should have positive scores"
        );
        assert!(
            (score_depth1 - score_depth3 * 2.0).abs() < 1e-6,
            "depth-3 score should be 0.5x the raw score ({} vs {})",
            score_depth1,
            score_depth3
        );
    }

    #[test]
    fn test_l1_reverse_index_serialize_roundtrip() {
        let mut idx = L1ReverseIndex::new();
        idx.add(2000, 1000, 1);
        idx.add(2000, 1001, 2);
        idx.add(2001, 1002, 3);

        let serialized = idx.serialize().unwrap();
        let restored = L1ReverseIndex::deserialize(&serialized).unwrap();

        let ctx2000 = HashSet::from([2000u64]);
        let ctx2001 = HashSet::from([2001u64]);
        let both = HashSet::from([2000u64, 2001u64]);

        assert_eq!(restored.find_associated(&ctx2000).len(), 2);
        assert_eq!(restored.find_associated(&ctx2001).len(), 1);
        assert_eq!(restored.find_associated(&both).len(), 3);

        // Refreshing an existing node should still behave correctly after roundtrip.
        let mut restored_mut = restored;
        restored_mut.add(2000, 1000, 10);
        let nodes = restored_mut.find_associated(&ctx2000);
        assert_eq!(nodes.len(), 2);
        let page_refs: HashSet<u64> = nodes.iter().map(|(_, pr)| *pr).collect();
        assert!(page_refs.contains(&10));
    }

    #[test]
    fn test_l1_reverse_index_build() {
        let (_temp, mut mmap, mut header, mut file) = create_test_mmap_with_tempfile(20);
        let mut btree = BTreeIndex::new();

        let node1 = ContextNode {
            id_hash: 1000,
            context_id: 2000,
            vector_page_ref: 0,
            importance: 0.5,
            valence: 0.0,
            arousal: 0.0,
            created_at: 0,
            updated_at: 0,
            version: 1,
            edge_ptrs: vec![],
        };
        let node2 = ContextNode {
            id_hash: 1001,
            context_id: 2000,
            ..node1.clone()
        };
        let node3 = ContextNode {
            id_hash: 1002,
            context_id: 2001,
            ..node1.clone()
        };

        insert_test_context_node(&mut mmap, &mut header, &mut btree, node1, &mut file);
        insert_test_context_node(&mut mmap, &mut header, &mut btree, node2, &mut file);
        insert_test_context_node(&mut mmap, &mut header, &mut btree, node3, &mut file);

        let data: &[u8] = &mmap[..];
        let idx = L1ReverseIndex::build(data, &btree).unwrap();

        let ctx2000 = HashSet::from([2000u64]);
        let ctx2001 = HashSet::from([2001u64]);
        let both = HashSet::from([2000u64, 2001u64]);

        assert_eq!(idx.find_associated(&ctx2000).len(), 2);
        assert_eq!(idx.find_associated(&ctx2001).len(), 1);
        assert_eq!(idx.find_associated(&both).len(), 3);
    }

    #[test]
    fn test_l1_reverse_index_add_and_remove() {
        let mut idx = L1ReverseIndex::new();
        idx.add(2000, 1000, 1);
        idx.add(2000, 1001, 2);
        idx.add(2001, 1002, 3);

        let ctx2000 = HashSet::from([2000u64]);
        let ctx2001 = HashSet::from([2001u64]);
        let both = HashSet::from([2000u64, 2001u64]);

        assert_eq!(idx.find_associated(&ctx2000).len(), 2);
        assert_eq!(idx.find_associated(&ctx2001).len(), 1);
        assert_eq!(idx.find_associated(&both).len(), 3);

        // Refreshing an existing node should update its page_ref, not duplicate.
        idx.add(2000, 1000, 10);
        let nodes = idx.find_associated(&ctx2000);
        assert_eq!(nodes.len(), 2);
        let page_refs: HashSet<u64> = nodes.iter().map(|(_, pr)| *pr).collect();
        assert!(page_refs.contains(&10));
        assert!(!page_refs.contains(&1));

        // Remove a single node.
        idx.remove_node(1001);
        assert_eq!(idx.find_associated(&ctx2000).len(), 1);
        assert_eq!(idx.find_associated(&both).len(), 2);

        // Remove an entire context bucket.
        idx.remove_context(2001);
        assert_eq!(idx.find_associated(&ctx2001).len(), 0);
        assert_eq!(idx.find_associated(&both).len(), 1);
    }

    #[test]
    fn test_l3_preview_keywords_deterministic_order() {
        use std::collections::HashSet;

        // Verify that keywords are sorted deterministically
        let keywords_input = vec![
            "zebra".to_string(),
            "apple".to_string(),
            "mango".to_string(),
            "apple".to_string(), // duplicate
            "banana".to_string(),
        ];

        let mut keywords: Vec<String> = keywords_input
            .into_iter()
            .collect::<HashSet<_>>()
            .into_iter()
            .collect();
        keywords.sort();

        assert_eq!(keywords, vec!["apple", "banana", "mango", "zebra"]);
    }

    #[test]
    fn test_collect_l2_ids_with_l3_page_type_filter() {
        let (_temp, mut mmap, mut header, mut file) = create_test_mmap_with_tempfile(20);
        let mut btree = BTreeIndex::new();

        // Insert a Context with l3_refs
        let ctx = ContextSlot {
            id_hash: 100,
            parent_id: None,
            depth: 1,
            title: "test".to_string(),
            summary: None,
            archive_refs: vec![],
            l3_refs: vec![999],
            turn_count: 1,
            created_at: 0,
            updated_at: 0,
            centroid_page_ref: 0,
            activation_score: 0.5,
            is_active: true,
            activation_state: ActivationState::Active,
            importance: 0.5,
            dialogue_range: (0, 0),
            version: 1,
            llm_params: crate::layers::context::LlmParams::default(),
        };

        let page_id = crate::file::page::allocate_page(
            &mut mmap,
            &mut header,
            crate::util::PageType::Context,
            2,
            0,
            &mut file,
        )
        .unwrap();
        let serialized = ctx.serialize().unwrap();
        crate::file::page::write_page_data(&mut mmap, page_id, &serialized).unwrap();
        let page_ref = crate::file::page::encode_page_ref(page_id, 0);
        btree.insert(100, page_ref);

        // Insert a non-Context page (HypergraphNode) with same l3_ref
        let node_page_id = crate::file::page::allocate_page(
            &mut mmap,
            &mut header,
            crate::util::PageType::HypergraphNode,
            3,
            0,
            &mut file,
        )
        .unwrap();
        // Write some dummy data
        crate::file::page::write_page_data(&mut mmap, node_page_id, &[0u8; 100]).unwrap();
        let node_page_ref = crate::file::page::encode_page_ref(node_page_id, 0);
        btree.insert(200, node_page_ref);

        let data: &[u8] = &mmap[..];
        let result = collect_l2_ids_with_l3(data, &btree, 999);

        // Should only contain the Context, not the HypergraphNode
        assert_eq!(result.len(), 1);
        assert!(result.contains(&100));
        assert!(!result.contains(&200));
    }
}
