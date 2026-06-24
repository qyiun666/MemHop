// Search implementation for MemHop
//
// search_memory() interface with L2-centric retrieval model.
//
// Retrieval flow:
//   1. Triple retrieval (vector + BM25 + entity) on L2 ContextSlot titles (depth 1, 2 & 3;
//      depth-3 results are weighted by 0.5)
//   2. Via L1 hypergraph, find associated L2 contexts
//   3. Return L0 profile, L3 ID list, L4 archive references

use crate::file::header::FileHeader;
use crate::file::page::decode_page_ref;
use crate::index::btree::BTreeIndex;
use crate::index::sparse::SparseIndex;
use crate::index::vector::{cosine_similarity, read_vector};
use crate::query::common::{self, format_hash};
use crate::query::slot_io::{decode_page_id, get_slot_data};
use crate::query::types::*;
use crate::slot::archive::ArchiveSlot;
use crate::slot::context::ContextSlot;
use crate::slot::context_node::ContextNode;
use crate::slot::hyperedge::HyperedgeSlot;
use crate::util::{hash_id, PAGE_SIZE};
use crate::MemHopError;
use memmap2::MmapMut;
use std::collections::{HashMap, HashSet};

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
) -> Vec<ContextSlot> {
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
        .filter_map(|(id, _)| ctx_map.remove(&id))
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
pub fn search_memory(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    query: SearchQuery,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    vector_dim: usize,
    encoder: Option<&(dyn crate::encoder::Encoder + Send + Sync)>,
    l1_reverse: &L1ReverseIndex,
) -> Result<SearchResult, MemHopError> {
    let _page_count = header.page_count;

    // ========================================================================
    // Route 1: auto_create — skip retrieval, create new L2
    // ========================================================================
    let filtered_l2 = if query.auto_create == 1 {
        let new_ctx = create_new_l2_context(
            mmap,
            header,
            btree,
            sparse_index,
            &query.dialogue,
            vector_dim,
        )?;
        vec![new_ctx]

    // ========================================================================
    // Route 2: context_id present — load specific L2, skip triple retrieval
    // ========================================================================
    } else if let Some(ref cid) = query.context_id {
        let target_hash = common::parse_id_to_hash(cid);
        let data: &[u8] = &mmap[..];

        // Try to load the L2 context by id_hash
        if let Some(slot_data) = btree
            .search(target_hash)
            .and_then(|pr| get_slot_data(data, pr))
        {
            match ContextSlot::deserialize_slot(slot_data) {
                Ok(ctx) => {
                    // Found: return just this one context, L1 association happens below
                    vec![ctx]
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
        // Step 1: LLM enhancement (optional)
        let search_text = if let Some(llm_config) = &query.llm_enhance {
            match enhance_query_with_llm(
                llm_config,
                &query.dialogue,
                query.context_history.as_deref(),
            ) {
                Ok(enhanced) => {
                    eprintln!(
                        "[LLM Enhancement] {} → {}",
                        safe_char_slice(&query.dialogue, 50),
                        safe_char_slice(&enhanced, 50)
                    );
                    enhanced
                }
                Err(e) => {
                    eprintln!("[LLM Enhancement] Failed: {}, using original", e);
                    query.dialogue.clone()
                }
            }
        } else {
            query.dialogue.clone()
        };

        // Step 2: Pre-filter by l3_id (if provided)
        //
        // If l3_id is set, we first collect the id_hash set of L2 contexts
        // that contain this L3 in their l3_refs. The three retrieval channels
        // will then only accept candidates within this set.
        let l3_scope: Option<HashSet<u64>> = if let Some(ref l3_id_str) = query.l3_id {
            // l3_id is a hex string like "1a2b3c..."; parse it back to the raw
            // id_hash rather than hashing the string again.
            let l3_hash = common::parse_id_to_hash(l3_id_str);
            let data: &[u8] = &mmap[..];
            Some(collect_l2_ids_with_l3(data, btree, l3_hash))
        } else {
            None
        };

        // Step 3: Triple retrieval on L2 ContextSlot (depth 1 & 2)
        let data: &[u8] = &mmap[..];
        let fetch_limit = query.context_limit * 2;

        // Ensure entity index is built from L3 before entity retrieval.
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
                )?
            } else {
                vec![]
            }
        } else {
            return Err(MemHopError::EncoderError(
                "No encoder configured for vector search".to_string(),
            ));
        };

        // Step 4: Merge & rank (entity 0.15, BM25 0.5, vector 0.35)
        let config = MergeConfig {
            entity_weight: 0.15,
            bm25_weight: 0.5,
            vector_weight: 0.35,
            limit: query.context_limit,
            min_score: query.min_score,
        };
        merge_and_rank(entity_results, bm25_results, vector_results, config)
    };

    // Step 5: L1 association — find sibling depth-1 contexts
    let data: &[u8] = &mmap[..];
    let l1_associated = get_l1_associated_depth1(data, &filtered_l2, btree, l1_reverse)?;

    // Step 6: L0 profile
    let l0_profile = crate::query::l0_crud::read_profile(mmap, btree)?;

    // Step 7: Collect L3 IDs & L4 archive refs from matched contexts
    let l3_ids = collect_l3_ids(&filtered_l2);
    let archive_refs = collect_archive_refs(data, &filtered_l2, btree)?;

    // Step 8: Update activation scores
    update_activation_scores(mmap, &filtered_l2, btree)?;

    // Step 9: Convert to public types
    let result = SearchResult {
        profile: l0_profile,
        contexts: convert_contexts(&filtered_l2),
        associated_contexts: convert_contexts(&l1_associated),
        l3_ids,
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
        // l3_id pre-filter: skip if not in scope
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
// Retrieval: Vector similarity
// ============================================================================

/// Retrieve L2 contexts using vector cosine similarity on centroid vectors
///
/// If `l3_scope` is Some, only accept candidates whose id_hash is in the set.
fn retrieve_l2_vector(
    data: &[u8],
    query_vector: &[half::f16],
    btree: &BTreeIndex,
    vector_dim: usize,
    limit: usize,
    min_score: f32,
    l3_scope: Option<&HashSet<u64>>,
) -> Result<Vec<(ContextSlot, f32)>, MemHopError> {
    let mut candidates = Vec::new();

    for (&id_hash, &page_ref) in btree.iter() {
        // l3_id pre-filter: skip if not in scope
        if let Some(scope) = l3_scope {
            if !scope.contains(&id_hash) {
                continue;
            }
        }

        let (page_id, _slot_idx) = decode_page_ref(page_ref);
        let offset = (page_id as usize) * PAGE_SIZE + 32;

        if offset >= data.len() {
            continue;
        }

        // Try to deserialize as ContextSlot
        if let Ok(ctx) = ContextSlot::deserialize(&data[offset..]) {
            // Accept depth 1, 2 & 3 (depth-3 results are weighted by 0.5)
            if ctx.depth > 3 {
                continue;
            }

            // Read centroid vector from centroid_page_ref
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
// l3_id pre-filter: collect L2 id_hash set containing a specific L3
// ============================================================================

/// Scan all BTree entries, deserialize as ContextSlot, collect id_hashes
/// whose `l3_refs` contain the target L3 hash.
///
/// This is used to pre-filter the retrieval candidate pool when `l3_id` is set.
fn collect_l2_ids_with_l3(data: &[u8], btree: &BTreeIndex, l3_hash: u64) -> HashSet<u64> {
    let mut result = HashSet::new();

    for (&id_hash, &page_ref) in btree.iter() {
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
// Filter by context_id (removed — handled in Route 2)
// ============================================================================

// ============================================================================
// L1 reverse index
// ============================================================================

/// L1 reverse index: maps an L2 `context_id` to the L1 `ContextNode`(s) that
/// point to it. Used to avoid the O(N) btree scan when looking up associated
/// contexts.
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

    /// Build the reverse index by scanning the btree once. Only pages whose
    /// header type is `PageType::ContextNode` and whose `context_id` is non-zero
    /// are indexed.
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

    /// Remove all entries for a given `context_id`.
    pub fn remove_context(&mut self, context_id: u64) {
        self.index.remove(&context_id);
    }

    /// Remove a single node entry across all `context_id` buckets.
    pub fn remove_node(&mut self, node_id_hash: u64) {
        for nodes in self.index.values_mut() {
            nodes.retain(|(nid, _)| *nid != node_id_hash);
        }
    }

    /// O(1) lookup: given a set of `context_id`s, return all associated L1
    /// nodes as `(node_id_hash, node_page_ref)`. Results are deduplicated by
    /// node id.
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

    /// Serialize the reverse index to a byte vector using bincode.
    pub fn serialize(&self) -> Result<Vec<u8>, MemHopError> {
        bincode::serialize(&self.index)
            .map_err(|e| MemHopError::Serialization(e.to_string()))
    }

    /// Deserialize the reverse index from a byte vector using bincode.
    pub fn deserialize(data: &[u8]) -> Result<Self, MemHopError> {
        let index: HashMap<u64, Vec<(u64, u64)>> = bincode::deserialize(data)
            .map_err(|e| MemHopError::Serialization(e.to_string()))?;
        Ok(Self { index })
    }

    /// Return true if the index contains no entries.
    pub fn is_empty(&self) -> bool {
        self.index.is_empty()
    }
}

// ============================================================================
// L1 association: find sibling depth-1 contexts via hypergraph
// ============================================================================

/// Via L1 hypergraph, find associated L2 contexts for matched contexts.
///
/// Algorithm:
/// 1. Collect matched context id_hashes
/// 2. Scan btree for ContextNodes whose context_id ∈ matched set
/// 3. For each such node, traverse its hyperedge_ptrs
/// 4. For each hyperedge, collect sibling node_ptrs
/// 5. Look up sibling ContextNodes → get their context_id
/// 6. Load ContextSlot for that context_id (no depth filtering)
fn get_l1_associated_depth1(
    data: &[u8],
    matched: &[ContextSlot],
    btree: &BTreeIndex,
    l1_reverse: &L1ReverseIndex,
) -> Result<Vec<ContextSlot>, MemHopError> {
    if matched.is_empty() {
        return Ok(vec![]);
    }

    let matched_ids: HashSet<u64> = matched.iter().map(|c| c.id_hash).collect();
    let mut seen: HashSet<u64> = matched_ids.clone(); // exclude already-matched
    let mut result: Vec<ContextSlot> = Vec::new();

    // Step 1: Use the L1 reverse index to find ContextNodes pointing to matched contexts
    let associated_nodes = l1_reverse.find_associated(&matched_ids);
    for (_node_hash, page_ref) in associated_nodes {
        if let Some(slot_data) = get_slot_data(data, page_ref) {
            if let Ok(node) = ContextNode::deserialize(slot_data) {
                // Step 2: For each relevant node, traverse hyperedges
                for &edge_hash in &node.edge_ptrs {
                    if let Some(edge_data) = btree
                        .search(edge_hash)
                        .and_then(|pr| get_slot_data(data, pr))
                    {
                        if let Ok(hyperedge) = HyperedgeSlot::deserialize(edge_data) {
                            // Step 3: For each sibling node in this hyperedge
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
                                        // Load the L2 ContextSlot
                                        if let Some(ctx_data) = btree
                                            .search(ctx_id)
                                            .and_then(|pr| get_slot_data(data, pr))
                                        {
                                            if let Ok(ctx) = ContextSlot::deserialize(ctx_data) {
                                                seen.insert(ctx_id);
                                                result.push(ctx);
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

    // Also include parent contexts of matched contexts
    for ctx in matched {
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
                    result.push(parent);
                }
            }
        }
    }

    Ok(result)
}

// ============================================================================
// Collect L3 IDs
// ============================================================================

/// Collect unique L3 hypergraph IDs from matched contexts
fn collect_l3_ids(contexts: &[ContextSlot]) -> Vec<String> {
    let mut ids = HashSet::new();
    for ctx in contexts {
        for &l3_hash in &ctx.l3_refs {
            ids.insert(l3_hash);
        }
    }
    ids.into_iter().map(format_hash).collect()
}

// ============================================================================
// Collect L4 archive references
// ============================================================================

/// Collect L4 archive references from matched contexts, loading metadata
fn collect_archive_refs(
    data: &[u8],
    contexts: &[ContextSlot],
    btree: &BTreeIndex,
) -> Result<Vec<ArchiveRef>, MemHopError> {
    let mut refs = Vec::new();
    let mut seen = HashSet::new();

    for ctx in contexts {
        for &arc_hash in &ctx.archive_refs {
            if !seen.insert(arc_hash) {
                continue;
            }
            if let Some(slot_data) = btree
                .search(arc_hash)
                .and_then(|pr| get_slot_data(data, pr))
            {
                if let Ok(arc) = ArchiveSlot::deserialize_slot(slot_data) {
                    refs.push(ArchiveRef {
                        id: format_hash(arc.id_hash),
                        context_id: format_hash(arc.context_id),
                        content_type: format!("{:?}", arc.content_type),
                        created_at: arc.created_at,
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

/// Update activation scores for retrieved contexts
fn update_activation_scores(
    mmap: &mut MmapMut,
    contexts: &[ContextSlot],
    btree: &BTreeIndex,
) -> Result<(), MemHopError> {
    let now_ms = common::now_ms();

    for ctx in contexts {
        if let Some(page_ref) = btree.search(ctx.id_hash) {
            let (page_id, _) = decode_page_ref(page_ref);
            let offset = (page_id as usize) * PAGE_SIZE + 32;

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
// Type conversion helpers
// ============================================================================

/// Convert internal ContextSlot to public ContextResult
fn convert_contexts(contexts: &[ContextSlot]) -> Vec<ContextResult> {
    contexts
        .iter()
        .map(|c| ContextResult {
            id: format_hash(c.id_hash),
            parent_id: c.parent_id.map(format_hash),
            depth: c.depth,
            title: c.title.clone(),
            summary: c.summary.clone(),
            activation_score: c.activation_score,
            turn_count: c.turn_count,
            l3_refs: c.l3_refs.iter().map(|h| format_hash(*h)).collect(),
            archive_refs: c.archive_refs.iter().map(|h| format_hash(*h)).collect(),
        })
        .collect()
}

// ============================================================================
// Auto-create L2 context
// ============================================================================

/// Create a new L2 ContextSlot from dialogue content (auto_create fast path)
fn create_new_l2_context(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    dialogue: &str,
    _vector_dim: usize,
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
        activation_state: crate::slot::context::ActivationState::Active,
        centroid_page_ref: 0,
        dialogue_range: (now_ms, now_ms),
    };

    // Serialize
    let ctx_data = new_ctx
        .serialize()
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;

    // Allocate page
    let page_id = crate::file::free_list::allocate_from_free_list(mmap, header)?;
    let page_offset = (page_id as usize) * PAGE_SIZE;

    // Write page header
    let page_header = crate::file::page::PageHeader {
        page_id,
        page_type: crate::util::PageType::Context.to_u16(),
        slot_count: 1,
        free_bytes: (PAGE_SIZE - 32 - ctx_data.len()) as u16,
        layer_id: 2,
        next_page: 0xFFFFFFFF,
        prev_page: 0xFFFFFFFF,
        reserved: [0u8; 12],
    };
    crate::file::page::write_page_header(mmap, page_id, &page_header)?;

    // Write slot data
    let data_offset = page_offset + 32;
    if data_offset + ctx_data.len() > mmap.len() {
        return Err(MemHopError::Serialization(format!(
            "ContextSlot data too large for page: {} > {}",
            ctx_data.len(),
            mmap.len() - data_offset
        )));
    }
    mmap[data_offset..data_offset + ctx_data.len()].copy_from_slice(&ctx_data);

    // Update B-tree
    let page_ref = (page_id as u64) << 16;
    btree.insert(id_hash, page_ref);

    // Update sparse index (word tokens only)
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
// LLM query enhancement
// ============================================================================

/// Enhanced query using LLM with content-aware classification
///
/// Handles different input types intelligently:
/// - Code snippets: extract language and functional description
/// - Long text (>200 chars): compress before keyword extraction
/// - Error messages: extract error codes and stack context
/// - Knowledge statements: extract core concepts
/// - Follow-up queries: leverage context_history for anaphora resolution
fn enhance_query_with_llm(
    llm_config: &crate::config::LlmConfig,
    dialogue: &str,
    context_history: Option<&str>,
) -> Result<String, MemHopError> {
    let char_count = dialogue.chars().count();

    // Build context block if history is available
    let history_block = if let Some(history) = context_history {
        let truncated = safe_char_slice(history, 500);
        format!(
            "\n之前的对话历史（最近{}字）：\n{}\n",
            truncated.chars().count(),
            truncated
        )
    } else {
        String::new()
    };

    // Detect input characteristics to guide prompt
    let has_code = dialogue.contains("```")
        || dialogue.contains("fn ")
        || dialogue.contains("impl ")
        || dialogue.contains("pub ")
        || dialogue.contains('{')
        || dialogue.contains(';');
    let has_path = dialogue.contains('/')
        && (dialogue.contains(".rs")
            || dialogue.contains(".py")
            || dialogue.contains(".go")
            || dialogue.contains(".ts")
            || dialogue.contains(".js"));
    let has_error = dialogue.contains("error[")
        || dialogue.contains("Error[")
        || dialogue.contains("panic")
        || dialogue.contains("failed");
    let is_verbose = char_count > 300;

    let length_hint = if is_verbose {
        format!("（输入较长，约{}字）", char_count)
    } else {
        String::new()
    };
    let code_hint = if has_code {
        "\n检测到代码片段，请识别代码语言和功能后用自然语言描述其作用，不要将代码符号直接作为关键词。"
    } else {
        ""
    };
    let path_hint = if has_path {
        "\n检测到文件路径，请提取路径中的技术栈关键词和查询意图。"
    } else {
        ""
    };
    let error_hint = if has_error {
        "\n检测到错误信息，请提取错误码、错误类型和相关技术栈。"
    } else {
        ""
    };
    let history_hint = if context_history.is_some() {
        "\n检测到历史对话，如当前问题是追问/指代（如'它'、'这个'），请结合历史还原完整语义。"
    } else {
        ""
    };

    let prompt = format!(
        "你是一个AI记忆检索系统的查询优化器。{}请分析用户输入，生成最优检索字符串。\n\
         要求：\n\
         1. 提取2-5个最能代表查询意图的核心术语（中文/英文均可）\n\
         2. 核心术语之间用空格分隔，不要用标点或分隔符\n\
         3. {}{}{}{}{}\n\
         4. 只返回检索字符串，不要解释、不要前缀、不要引号\n\
         用户输入：{}",
        length_hint,
        if is_verbose {
            "如果输入是长文本（>300字），先在心里做30字压缩摘要，再从摘要中提取关键词。"
        } else {
            "如果输入是知识性陈述，提取核心概念和领域术语。"
        },
        code_hint,
        path_hint,
        error_hint,
        history_hint,
        dialogue
    );

    let messages = if context_history.is_some() {
        serde_json::json!([
            {"role": "system", "content": "You are a memory retrieval query optimizer. \
                 You classify input types (code/error/article/question/path/knowledge/followup) \
                 and produce clean search terms for each type."},
            {"role": "user", "content": format!("{}{}", history_block, prompt)}
        ])
    } else {
        serde_json::json!([
            {"role": "system", "content": "You are a memory retrieval query optimizer. \
             You classify input types and produce clean search terms for each type."},
            {"role": "user", "content": prompt}
        ])
    };

    // Longer timeout for complex inputs
    let timeout_secs = if is_verbose || has_code || has_error {
        30u64
    } else {
        15u64
    };

    let client = reqwest::blocking::Client::builder()
        .timeout(std::time::Duration::from_secs(timeout_secs))
        .build()
        .map_err(|e| MemHopError::Serialization(format!("HTTP client failed: {}", e)))?;

    let body = serde_json::json!({
        "model": llm_config.model,
        "messages": messages,
        "max_tokens": 256,
        "temperature": 0.2,
    });

    let response = client
        .post(&llm_config.api_url)
        .bearer_auth(&llm_config.api_key)
        .json(&body)
        .send()
        .map_err(|e| MemHopError::Serialization(format!("LLM API call failed: {}", e)))?;

    if !response.status().is_success() {
        return Err(MemHopError::Serialization(format!(
            "LLM API error: {} - {}",
            response.status(),
            response.text().unwrap_or_default()
        )));
    }

    let json: serde_json::Value = response
        .json()
        .map_err(|e| MemHopError::Serialization(format!("Parse LLM response failed: {}", e)))?;

    let enhanced = json["choices"][0]["message"]["content"]
        .as_str()
        .map(|s| s.trim().to_string())
        .ok_or_else(|| MemHopError::Serialization("No content in LLM response".to_string()))?;

    // Clean up: remove quotes, prefixes, unnecessary punctuation
    let cleaned = enhanced
        .trim_start_matches('"')
        .trim_end_matches('"')
        .trim_start_matches("QUERY:")
        .trim_start_matches("query:")
        .split_whitespace()
        .filter(|s| s.len() >= 2 || s.chars().all(|c| c.is_alphanumeric()))
        .collect::<Vec<_>>()
        .join(" ");

    Ok(if cleaned.is_empty() || cleaned.len() < 5 {
        // Fallback: use simple keyword extraction from original text
        extract_fallback_keywords(dialogue)
    } else {
        cleaned
    })
}

/// Simple fallback keyword extraction when LLM enhancement fails
fn extract_fallback_keywords(text: &str) -> String {
    // Remove common stop words, code artifacts, and join meaningful terms
    let stop_words = [
        "的", "了", "是", "在", "有", "和", "就", "不", "人", "都", "一", "a", "an", "the", "is",
        "are", "was", "were", "be", "been", "being", "have", "has", "had", "do", "does", "did",
        "will", "would", "could", "should", "to", "of", "in", "for", "on", "with", "at", "by",
        "from", "as", "into",
    ];

    text.split_whitespace()
        .filter(|w| w.len() >= 3 && !stop_words.contains(&w.to_lowercase().as_str()))
        .map(|w| w.trim_matches(|c: char| !c.is_alphanumeric() && c != '_' && c != '-'))
        .filter(|w| !w.is_empty())
        .take(20)
        .collect::<Vec<_>>()
        .join(" ")
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::file::header::FileHeader;
    use crate::slot::context::{ActivationState, ContextSlot};
    use std::io::Write;

    fn create_test_mmap(page_count: usize) -> (tempfile::NamedTempFile, MmapMut, FileHeader) {
        let temp_file = tempfile::NamedTempFile::new().unwrap();
        let path = temp_file.path();
        let mut file = std::fs::File::create(path).unwrap();
        file.write_all(&vec![0u8; PAGE_SIZE * page_count]).unwrap();
        drop(file);
        let file = std::fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open(path)
            .unwrap();
        let mut mmap = unsafe { MmapMut::map_mut(&file).unwrap() };
        let mut header = FileHeader::new(768);
        for page_id in 2..page_count as u32 {
            let offset = page_id as usize * PAGE_SIZE;
            let next_free = if page_id + 1 < page_count as u32 {
                page_id + 1
            } else {
                0xFFFFFFFF
            };
            mmap[offset..offset + 4].copy_from_slice(&next_free.to_le_bytes());
        }
        header.page_count = page_count as u32;
        header.free_list_head = 2;
        (temp_file, mmap, header)
    }

    fn insert_test_context(
        mmap: &mut MmapMut,
        header: &mut FileHeader,
        btree: &mut BTreeIndex,
        sparse_index: &mut SparseIndex,
        ctx: ContextSlot,
    ) {
        let page_id =
            crate::file::page::allocate_page(mmap, header, crate::util::PageType::Context, 2, 0)
                .unwrap();
        let serialized = ctx.serialize().unwrap();
        crate::file::page::write_page_data(mmap, page_id, &serialized).unwrap();
        let page_ref = crate::file::page::encode_page_ref(page_id, 0);
        btree.insert(ctx.id_hash, page_ref);
        let terms: Vec<String> = ctx
            .title
            .split_whitespace()
            .map(|s| s.to_lowercase())
            .collect();
        sparse_index.add_document(
            ctx.id_hash,
            terms,
            ctx.title.split_whitespace().count() as u32,
        );
    }

    #[test]
    fn test_depth3_retrieval_weighting() {
        let (_temp, mut mmap, mut header) = create_test_mmap(10);
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
        );
        insert_test_context(
            &mut mmap,
            &mut header,
            &mut btree,
            &mut sparse_index,
            ctx_depth3,
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

    fn insert_test_context_node(
        mmap: &mut MmapMut,
        header: &mut FileHeader,
        btree: &mut BTreeIndex,
        node: ContextNode,
    ) -> u64 {
        let page_id = crate::file::page::allocate_page(
            mmap,
            header,
            crate::util::PageType::ContextNode,
            1,
            0,
        )
        .unwrap();
        let serialized = node.serialize().unwrap();
        crate::file::page::write_page_data(mmap, page_id, &serialized).unwrap();
        let page_ref = crate::file::page::encode_page_ref(page_id, 0);
        btree.insert(node.id_hash, page_ref);
        page_ref
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
        let (_temp, mut mmap, mut header) = create_test_mmap(20);
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

        insert_test_context_node(&mut mmap, &mut header, &mut btree, node1);
        insert_test_context_node(&mut mmap, &mut header, &mut btree, node2);
        insert_test_context_node(&mut mmap, &mut header, &mut btree, node3);

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
}
