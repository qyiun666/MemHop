// Search implementation for MemHop
//
// search_memory() interface with L2-centric retrieval model.
//
// Retrieval flow:
//   1. Triple retrieval (vector + BM25 + n-gram) on L2 ContextSlot titles (depth 1 & 2)
//   2. Via L1 hypergraph, find associated depth-1 contexts
//   3. Return L0 profile, L3 ID list, L4 archive references

use crate::file::header::FileHeader;
use crate::file::page::decode_page_ref;
use crate::index::btree::BTreeIndex;
use crate::index::sparse::SparseIndex;
use crate::index::vector::{cosine_similarity, read_vector};
use crate::query::common::{self, format_hash};
use crate::query::slot_io::get_slot_data;
use crate::query::types::*;
use crate::slot::archive::ArchiveSlot;
use crate::slot::context::ContextSlot;
use crate::slot::context_node::ContextNode;
use crate::slot::hyperedge::HyperedgeSlot;
use crate::util::hash_id;
use crate::MemHopError;
use memmap2::MmapMut;
use std::collections::{HashMap, HashSet};

const PAGE_SIZE: usize = 4096;

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
    ngram_weight: f32,
    bm25_weight: f32,
    vector_weight: f32,
    limit: usize,
    min_score: f32,
}

/// Merge ngram, BM25 and vector retrieval results using weighted fusion
fn merge_and_rank(
    ngram_results: Vec<(ContextSlot, f32)>,
    bm25_results: Vec<(ContextSlot, f32)>,
    vector_results: Vec<(ContextSlot, f32)>,
    config: MergeConfig,
) -> Vec<ContextSlot> {
    let ngram_max = ngram_results.iter().map(|(_, s)| *s).fold(0.0f32, f32::max);
    let bm25_max = bm25_results.iter().map(|(_, s)| *s).fold(0.0f32, f32::max);
    let vector_max = vector_results.iter().map(|(_, s)| *s).fold(0.0f32, f32::max);

    let mut score_map: HashMap<u64, (f32, f32, f32)> = HashMap::new();
    let mut ctx_map: HashMap<u64, ContextSlot> = HashMap::new();

    for (ctx, score) in ngram_results {
        let n = if ngram_max > 0.0 { score / ngram_max } else { 0.0 };
        score_map.entry(ctx.id_hash).or_insert((0.0, 0.0, 0.0)).0 = n;
        ctx_map.entry(ctx.id_hash).or_insert(ctx);
    }

    for (ctx, score) in bm25_results {
        let n = if bm25_max > 0.0 { score / bm25_max } else { 0.0 };
        score_map.entry(ctx.id_hash).or_insert((0.0, 0.0, 0.0)).1 = n;
        ctx_map.entry(ctx.id_hash).or_insert(ctx);
    }

    for (ctx, score) in vector_results {
        let n = if vector_max > 0.0 { score / vector_max } else { 0.0 };
        score_map.entry(ctx.id_hash).or_insert((0.0, 0.0, 0.0)).2 = n;
        ctx_map.entry(ctx.id_hash).or_insert(ctx);
    }

    let mut scored: Vec<(u64, f32)> = score_map
        .into_iter()
        .map(|(id, (ng, bm, vc))| {
            (id, config.ngram_weight * ng + config.bm25_weight * bm + config.vector_weight * vc)
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
///   4. default              → full triple retrieval on all depth-1/2 contexts
pub fn search_memory(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    query: SearchQuery,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    vector_dim: usize,
    encoder: Option<&(dyn crate::encoder::ipc::Encoder + Send + Sync)>,
) -> Result<SearchResult, MemHopError> {
    let _page_count = header.page_count;

    // ========================================================================
    // Route 1: auto_create — skip retrieval, create new L2
    // ========================================================================
    let filtered_l2 = if query.auto_create == 1 {
        let new_ctx = create_new_l2_context(
            mmap, header, btree, sparse_index,
            &query.dialogue, vector_dim,
        )?;
        vec![new_ctx]

    // ========================================================================
    // Route 2: context_id present — load specific L2, skip triple retrieval
    // ========================================================================
    } else if let Some(ref cid) = query.context_id {
        let target_hash = common::parse_id_to_hash(cid);
        let data: &[u8] = &mmap[..];

        // Try to load the L2 context by id_hash
        if let Some(slot_data) = btree.search(target_hash)
            .and_then(|pr| get_slot_data(data, pr))
        {
            match ContextSlot::deserialize_slot(slot_data) {
                Ok(ctx) => {
                    // Found: return just this one context, L1 association happens below
                    vec![ctx]
                },
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
            match enhance_query_with_llm(llm_config, &query.dialogue) {
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
            let l3_hash = hash_id(l3_id_str);
            let data: &[u8] = &mmap[..];
            Some(collect_l2_ids_with_l3(data, btree, l3_hash))
        } else {
            None
        };

        // Step 3: Triple retrieval on L2 ContextSlot (depth 1 & 2)
        let data: &[u8] = &mmap[..];
        let fetch_limit = query.context_limit * 2;

        let ngram_results = retrieve_l2_ngram(
            data, &search_text, sparse_index, btree, fetch_limit,
            l3_scope.as_ref(),
        )?;

        let bm25_results = retrieve_l2_bm25(
            data, &search_text, sparse_index, btree, fetch_limit,
            l3_scope.as_ref(),
        )?;

        let vector_results = if let Some(enc) = encoder {
            let output = enc.encode(&search_text);
            if !output.dense.is_empty() {
                retrieve_l2_vector(
                    data, &output.dense, btree, vector_dim,
                    fetch_limit, query.min_score,
                    l3_scope.as_ref(),
                )?
            } else {
                vec![]
            }
        } else {
            vec![]
        };

        // Step 4: Merge & rank (ngram 0.2, BM25 0.5, vector 0.3)
        let config = MergeConfig {
            ngram_weight: 0.2,
            bm25_weight: 0.5,
            vector_weight: 0.3,
            limit: query.context_limit,
            min_score: query.min_score,
        };
        merge_and_rank(
            ngram_results, bm25_results, vector_results,
            config,
        )
    };

    // Step 5: L1 association — find sibling depth-1 contexts
    let data: &[u8] = &mmap[..];
    let l1_associated = get_l1_associated_depth1(
        data, &filtered_l2, btree,
    )?;

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
// Retrieval: n-gram
// ============================================================================

/// Retrieve L2 contexts using n-gram character-level matching
///
/// If `l3_scope` is Some, only accept candidates whose id_hash is in the set.
fn retrieve_l2_ngram(
    data: &[u8],
    query_text: &str,
    sparse_index: &SparseIndex,
    btree: &BTreeIndex,
    limit: usize,
    l3_scope: Option<&HashSet<u64>>,
) -> Result<Vec<(ContextSlot, f32)>, MemHopError> {
    let ngrams = SparseIndex::tokenize_ngram(query_text, 2);
    if ngrams.is_empty() {
        return Ok(vec![]);
    }

    let hits = sparse_index.search(&ngrams, limit * 2);
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
                if ctx.depth <= 2 {
                    scored.push((ctx, score));
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
    let terms: Vec<String> = query_text.split_whitespace().map(|s| s.to_string()).collect();
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
                if ctx.depth <= 2 {
                    scored.push((ctx, score));
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
            // Only depth 1 & 2
            if ctx.depth > 2 {
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
                    candidates.push((ctx, score));
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
fn collect_l2_ids_with_l3(
    data: &[u8],
    btree: &BTreeIndex,
    l3_hash: u64,
) -> HashSet<u64> {
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
// L1 association: find sibling depth-1 contexts via hypergraph
// ============================================================================

/// Via L1 hypergraph, find depth-1 contexts associated with matched contexts.
///
/// Algorithm:
/// 1. Collect matched context id_hashes
/// 2. Scan btree for ContextNodes whose context_id ∈ matched set
/// 3. For each such node, traverse its hyperedge_ptrs
/// 4. For each hyperedge, collect sibling node_ptrs
/// 5. Look up sibling ContextNodes → get their context_id
/// 6. Load ContextSlot for that context_id, keep only depth=1
fn get_l1_associated_depth1(
    data: &[u8],
    matched: &[ContextSlot],
    btree: &BTreeIndex,
) -> Result<Vec<ContextSlot>, MemHopError> {
    if matched.is_empty() {
        return Ok(vec![]);
    }

    let matched_ids: HashSet<u64> = matched.iter().map(|c| c.id_hash).collect();
    let mut seen: HashSet<u64> = matched_ids.clone(); // exclude already-matched
    let mut result: Vec<ContextSlot> = Vec::new();

    // Step 1: Scan all btree entries for ContextNodes pointing to matched contexts
    let mut relevant_nodes: Vec<ContextNode> = Vec::new();
    for (&_id, &page_ref) in btree.iter() {
        if let Some(slot_data) = get_slot_data(data, page_ref) {
            if let Ok(node) = ContextNode::deserialize(slot_data) {
                if matched_ids.contains(&node.context_id) {
                    relevant_nodes.push(node);
                }
            }
        }
    }

    // Step 2: For each relevant node, traverse hyperedges
    for node in &relevant_nodes {
        for &edge_hash in &node.edge_ptrs {
            if let Some(edge_data) = btree.search(edge_hash).and_then(|pr| get_slot_data(data, pr)) {
                if let Ok(hyperedge) = HyperedgeSlot::deserialize(edge_data) {
                    // Step 3: For each sibling node in this hyperedge
                    for &sibling_hash in &hyperedge.node_ptrs {
                        if let Some(sib_data) = btree.search(sibling_hash)
                            .and_then(|pr| get_slot_data(data, pr))
                        {
                            if let Ok(sibling_node) = ContextNode::deserialize(sib_data) {
                                let ctx_id = sibling_node.context_id;
                                if seen.contains(&ctx_id) {
                                    continue;
                                }
                                // Load the L2 ContextSlot
                                if let Some(ctx_data) = btree.search(ctx_id)
                                    .and_then(|pr| get_slot_data(data, pr))
                                {
                                    if let Ok(ctx) = ContextSlot::deserialize(ctx_data) {
                                        if ctx.depth == 1 {
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

    // Also include parent depth-1 contexts of matched depth-2 contexts
    for ctx in matched {
        if ctx.depth == 2 {
            if let Some(parent_id) = ctx.parent_id {
                if seen.contains(&parent_id) {
                    continue;
                }
                if let Some(parent_data) = btree.search(parent_id)
                    .and_then(|pr| get_slot_data(data, pr))
                {
                    if let Ok(parent) = ContextSlot::deserialize(parent_data) {
                        if parent.depth == 1 {
                            seen.insert(parent_id);
                            result.push(parent);
                        }
                    }
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
    ids.into_iter()
        .map(format_hash)
        .collect()
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
            if let Some(slot_data) = btree.search(arc_hash).and_then(|pr| get_slot_data(data, pr)) {
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

                    let buf = c.serialize()
                        .map_err(|e| MemHopError::Serialization(e.to_string()))?;
                    if offset + buf.len() > mmap.len() {
                        return Err(MemHopError::Serialization(format!(
                            "ContextSlot activation update too large: {} > {}",
                            buf.len(), mmap.len() - offset
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
    let ctx_data = new_ctx.serialize()
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
            ctx_data.len(), mmap.len() - data_offset
        )));
    }
    mmap[data_offset..data_offset + ctx_data.len()].copy_from_slice(&ctx_data);

    // Update B-tree
    let page_ref = (page_id as u64) << 16;
    btree.insert(id_hash, page_ref);

    // Update sparse index (word tokens + ngram tokens)
    let mut terms: Vec<String> = new_ctx.title.split_whitespace().map(|s| s.to_string()).collect();
    let ngram_terms = SparseIndex::tokenize_ngram(&new_ctx.title, 2);
    terms.extend(ngram_terms);
    let doc_len = terms.len() as u32;
    sparse_index.add_document(id_hash, terms, doc_len);

    Ok(new_ctx)
}

// ============================================================================
// LLM query enhancement
// ============================================================================

/// Enhance query using LLM for keyword extraction and query expansion
fn enhance_query_with_llm(
    llm_config: &LlmConfig,
    dialogue: &str,
) -> Result<String, MemHopError> {
    let prompt = format!(
        "你是一个查询优化助手。请分析以下用户对话，提取核心关键词并扩展相关术语，\n\
         以便更好地检索相关记忆。\n\n\
         要求：\n\
         1. 提取3-5个核心关键词\n\
         2. 为每个关键词提供1-2个同义词或相关词\n\
         3. 返回格式：关键词1 同义词1 同义词2 | 关键词2 同义词1 | ...\n\
         4. 只返回优化后的查询字符串，不要其他解释\n\n\
         用户对话：{}",
        dialogue
    );

    let client = reqwest::blocking::Client::builder()
        .timeout(std::time::Duration::from_secs(10))
        .build()
        .map_err(|e| MemHopError::Serialization(format!("HTTP client failed: {}", e)))?;

    let body = serde_json::json!({
        "model": llm_config.model,
        "messages": [
            {"role": "system", "content": "You are a query optimization assistant."},
            {"role": "user", "content": prompt}
        ],
        "max_tokens": 128,
        "temperature": 0.3,
    });

    let response = client
        .post(&llm_config.api_url)
        .bearer_auth(&llm_config.api_key)
        .json(&body)
        .send()
        .map_err(|e| MemHopError::Serialization(format!("LLM API call failed: {}", e)))?;

    if !response.status().is_success() {
        return Err(MemHopError::Serialization(
            format!("LLM API error: {} - {}", response.status(), response.text().unwrap_or_default()),
        ));
    }

    let json: serde_json::Value = response.json()
        .map_err(|e| MemHopError::Serialization(format!("Parse LLM response failed: {}", e)))?;

    let enhanced = json["choices"][0]["message"]["content"]
        .as_str()
        .map(|s| s.trim().to_string())
        .ok_or_else(|| MemHopError::Serialization("No content in LLM response".to_string()))?;

    let parsed = enhanced
        .split('|')
        .flat_map(|part| part.split_whitespace())
        .collect::<Vec<_>>()
        .join(" ");

    Ok(if parsed.is_empty() {
        dialogue.to_string()
    } else {
        parsed
    })
}
