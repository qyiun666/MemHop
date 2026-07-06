// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L2 two-channel retrieval: BM25 keyword + vector similarity, with optional
//! cross-encoder reranker. The main entry point is [`search_l2_candidates`].

use crate::config::SearchWeights;
use crate::file::page::decode_page_ref;
use crate::index::btree::BTreeIndex;
use crate::index::l2_meta::L2MetaIndex;
use crate::index::sparse::SparseIndex;
use crate::index::vector::{cosine_similarity, ivf_knn, read_vector, IVFIndex};
use crate::layers::context::ContextSlot;
use crate::shared::common;
use crate::shared::slot_io::get_slot_data;
use crate::MemHopError;
use std::collections::{HashMap, HashSet};

// ============================================================================
// 2-way merge & rank, with optional rerank
// ============================================================================

/// Configuration for [`merge_and_rank`].
struct MergeConfig {
    bm25_weight: f32,
    vector_weight: f32,
    limit: usize,
    min_score: f32,
}

/// Merge BM25 and vector retrieval results using weighted fusion.
///
/// Returns a recall pool of up to `limit` entries. When reranking is enabled,
/// callers should set `limit` larger than the final desired count and pass the
/// pooled results through [`rerank_candidates`] to produce the final ranking.
fn merge_and_rank(
    bm25_results: Vec<(ContextSlot, f32)>,
    vector_results: Vec<(ContextSlot, f32)>,
    config: MergeConfig,
) -> Vec<(ContextSlot, f32)> {
    let bm25_max = bm25_results.iter().map(|(_, s)| *s).fold(0.0f32, f32::max);
    let vector_max = vector_results
        .iter()
        .map(|(_, s)| *s)
        .fold(0.0f32, f32::max);

    let mut score_map: HashMap<u64, (f32, f32)> = HashMap::new();
    let mut ctx_map: HashMap<u64, ContextSlot> = HashMap::new();

    for (ctx, score) in bm25_results {
        let n = if bm25_max > 0.0 {
            score / bm25_max
        } else {
            0.0
        };
        score_map.entry(ctx.id_hash).or_insert((0.0, 0.0)).0 = n;
        ctx_map.entry(ctx.id_hash).or_insert(ctx);
    }

    for (ctx, score) in vector_results {
        let n = if vector_max > 0.0 {
            score / vector_max
        } else {
            0.0
        };
        score_map.entry(ctx.id_hash).or_insert((0.0, 0.0)).1 = n;
        ctx_map.entry(ctx.id_hash).or_insert(ctx);
    }

    let mut scored: Vec<(u64, f32)> = score_map
        .into_iter()
        .map(|(id, (bm, vc))| (id, config.bm25_weight * bm + config.vector_weight * vc))
        .filter(|(_, s)| *s >= config.min_score)
        .collect();

    scored.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
    scored.truncate(config.limit);

    scored
        .into_iter()
        .filter_map(|(id, score)| ctx_map.remove(&id).map(|ctx| (ctx, score)))
        .collect()
}

/// Rerank a recall pool of candidates against the query using a cross-encoder.
///
/// On success, returns up to `limit` candidates ordered by the reranker score.
/// On failure, falls back to the original fusion order and truncates to `limit`.
pub(crate) fn rerank_candidates(
    query: &str,
    candidates: &[(ContextSlot, f32)],
    encoder: &dyn crate::encoder::Encoder,
    max_candidates: usize,
    limit: usize,
) -> Result<Vec<(ContextSlot, f32)>, MemHopError> {
    let candidates = &candidates[..candidates.len().min(max_candidates)];
    let docs: Vec<String> = candidates
        .iter()
        .map(|(ctx, _)| {
            let mut text = ctx.title.clone();
            if let Some(ref s) = ctx.summary {
                text.push(' ');
                text.push_str(s);
            }
            text
        })
        .collect();

    match encoder.rerank(query, &docs) {
        Ok(scores) => {
            let mut scored: Vec<_> = candidates
                .iter()
                .zip(scores)
                .map(|((ctx, _), score)| (ctx.clone(), score))
                .collect();
            scored.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
            scored.truncate(limit);
            Ok(scored)
        }
        Err(e) => {
            tracing::warn!("rerank failed, falling back to fusion ranking: {}", e);
            Ok(candidates.iter().take(limit).cloned().collect())
        }
    }
}

// ============================================================================
// Retrieval: BM25
// ============================================================================

/// Retrieve L2 contexts using BM25 word-level scoring.
///
/// If `candidates` is Some, only accept candidates whose id_hash is in the set.
pub(crate) fn retrieve_l2_bm25(
    data: &[u8],
    query_text: &str,
    sparse_index: &SparseIndex,
    btree: &BTreeIndex,
    limit: usize,
    candidates: Option<&HashSet<u64>>,
) -> Result<Vec<(ContextSlot, f32)>, MemHopError> {
    let terms: Vec<String> = crate::index::sparse::tokenize(query_text);
    if terms.is_empty() {
        return Ok(vec![]);
    }

    let hits = sparse_index.search(&terms, limit * 2);
    let mut scored = Vec::new();

    for (id_hash, score) in hits {
        if let Some(scope) = candidates {
            if !scope.contains(&id_hash) {
                continue;
            }
        }
        let page_ref = match btree.search(id_hash) {
            Some(pr) => pr,
            None => continue,
        };
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

/// Retrieve L2 contexts using vector cosine similarity on centroid vectors.
///
/// If `candidates` is Some, only accept candidates whose id_hash is in the set.
#[allow(clippy::too_many_arguments)]
pub(crate) fn retrieve_l2_vector(
    data: &[u8],
    query_vector: &[half::f16],
    btree: &BTreeIndex,
    vector_dim: usize,
    limit: usize,
    min_score: f32,
    candidates: Option<&HashSet<u64>>,
    ivf_index: Option<&IVFIndex>,
    n_probes: usize,
) -> Result<Vec<(ContextSlot, f32)>, MemHopError> {
    // Fast path: use IVF index if available and non-empty
    if let Some(ivf) = ivf_index {
        if !ivf.centroids.is_empty() && !ivf.buckets.is_empty() {
            let effective_probes = if n_probes > 0 { n_probes } else { 8 };
            let ivf_candidates =
                ivf_knn(ivf, data, query_vector, limit * 2, effective_probes).unwrap_or_default();

            let mut results = Vec::new();
            for (id_hash, score) in ivf_candidates {
                if min_score > 0.0 && score < min_score {
                    continue;
                }
                if let Some(scope) = candidates {
                    if !scope.contains(&id_hash) {
                        continue;
                    }
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

    // Fallback: brute-force linear scan over btree entries.
    let mut results = Vec::new();

    for (&id_hash, &page_ref) in btree.iter_unsorted() {
        if let Some(scope) = candidates {
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
                    results.push((ctx, weighted_score));
                }
            }
        }
    }

    results.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
    results.truncate(limit);
    Ok(results)
}

// ============================================================================
// Public API: search_l2_candidates
// ============================================================================

/// Two-channel L2 retrieval: BM25 + vector similarity, with optional reranker.
///
/// Performs BM25 and vector retrieval (scoped to `candidates` if provided),
/// merges with weighted fusion, and optionally reranks with a cross-encoder.
/// Returns up to `context_limit` ranked `(ContextSlot, score)` pairs.
#[allow(clippy::too_many_arguments)]
pub fn search_l2_candidates(
    data: &[u8],
    query_text: &str,
    sparse_index: &SparseIndex,
    btree: &BTreeIndex,
    _l2_meta: &L2MetaIndex,
    vector_dim: usize,
    encoder: Option<&(dyn crate::encoder::Encoder + Send + Sync)>,
    search_weights: &SearchWeights,
    ivf_index: Option<&IVFIndex>,
    context_limit: usize,
    min_score: f32,
    candidates: Option<&HashSet<u64>>,
) -> Result<Vec<(ContextSlot, f32)>, MemHopError> {
    let fetch_limit = context_limit * 2;

    let bm25_results = retrieve_l2_bm25(
        data,
        query_text,
        sparse_index,
        btree,
        fetch_limit,
        candidates,
    )?;

    let vector_results = if let Some(enc) = encoder {
        // Gracefully degrade if encoder fails (e.g. dimension mismatch)
        match enc.encode(query_text) {
            Ok(output) if !output.dense.is_empty() => retrieve_l2_vector(
                data,
                &output.dense,
                btree,
                vector_dim,
                fetch_limit,
                min_score,
                candidates,
                ivf_index,
                search_weights.n_probes,
            )?,
            Ok(_) => vec![],
            Err(e) => {
                tracing::warn!("encoder encode failed, skipping vector retrieval: {}", e);
                vec![]
            }
        }
    } else {
        vec![]
    };

    let config = MergeConfig {
        bm25_weight: search_weights.bm25_weight,
        vector_weight: search_weights.vector_weight,
        limit: fetch_limit,
        min_score,
    };
    let merged = merge_and_rank(bm25_results, vector_results, config);

    if search_weights.enable_reranker {
        if let Some(enc) = encoder {
            rerank_candidates(
                query_text,
                &merged,
                enc,
                search_weights.rerank_max_candidates,
                context_limit,
            )
        } else {
            Ok(merged.into_iter().take(context_limit).collect())
        }
    } else {
        Ok(merged.into_iter().take(context_limit).collect())
    }
}

/// Build a candidate set for two-channel retrieval.
///
/// - If `l3_id` is specified, restricts to L2 contexts referencing that L3 graph.
/// - Otherwise, uses BM25 prescreen to find the top candidates.
pub fn build_candidate_set(
    l2_meta: &L2MetaIndex,
    query_text: &str,
    sparse_index: &SparseIndex,
    fetch_limit: usize,
    l3_id: Option<&str>,
) -> Option<HashSet<u64>> {
    if let Some(l3_id_str) = l3_id {
        let l3_hash = common::parse_id_to_hash(l3_id_str);
        let ids = l2_meta.get_l2_ids_by_l3(l3_hash);
        if ids.is_empty() {
            None
        } else {
            Some(ids.into_iter().collect())
        }
    } else {
        let prescreen = l2_meta.bm25_prescreen(query_text, sparse_index, fetch_limit);
        if prescreen.is_empty() {
            None
        } else {
            Some(prescreen.into_iter().collect())
        }
    }
}
