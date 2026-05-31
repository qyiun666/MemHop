use std::collections::{HashMap, HashSet};
use std::time::Instant;
use half::f16;

use crate::Brain;
use crate::encoder::Encoder;
use crate::engram::{AssociationKind, Engram, EngramKind};
use crate::error::Result;
use crate::types::{
    ConflictItem, GraphAssociation, RecallRequest, RecallResponse, RecallTrace, TreeContext,
};
use crate::context::Phase;

/// v0.9.0: Retrieval mode — HNSW + RRF fusion.
///
/// Returns items sorted by Reciprocal Rank Fusion score (k=60)
/// combining HNSW cosine rank + SparseIndex ngram rank.
pub(crate) fn recall_retrieval(
    brain: &Brain,
    req: &RecallRequest,
    query_vector: &[f16],
    start: Instant,
) -> Result<RecallResponse> {
    const HNSW_SEARCH_K: usize = 80;

    // Step 1: HNSW search — get candidates
    let hnsw_results = brain.hnsw.search(query_vector, HNSW_SEARCH_K);

    // Step 2: Map HNSW results to string IDs with rank
    let hnsw_strings: Vec<(String, f32)> = hnsw_results
        .iter()
        .filter_map(|(node_id, sim)| {
            brain
                .hnsw_id_map
                .get(node_id)
                .map(|sid| (sid.clone(), *sim))
        })
        .collect();

    // Step 3: SparseIndex BM25 search (v0.10.0: replaces IDF-weighted search)
    let query_sparse = brain.ngram_encoder.encode(&req.query).sparse;
    let idf = brain.sparse_index.idf_map();
    let sparse_results = brain.sparse_index.bm25_search(&query_sparse, &idf, HNSW_SEARCH_K);

    // Step 4: BM25 score-based fusion (v0.10.0: replaces RRF rank-based)
    let mut bm25_map: HashMap<String, f32> = sparse_results.into_iter().collect();
    let hnsw_map: HashMap<String, f32> = hnsw_strings.iter().cloned().collect();

    // Min-max normalize BM25 scores
    let bm25_min = bm25_map.values().cloned().fold(f32::MAX, f32::min);
    let bm25_max = bm25_map.values().cloned().fold(f32::MIN, f32::max);
    for score in bm25_map.values_mut() {
        if (bm25_max - bm25_min).abs() < f32::EPSILON {
            *score = 0.5;
        } else {
            *score = (*score - bm25_min) / (bm25_max - bm25_min);
        }
    }

    // Fuse scores: 0.4 * BM25 + 0.6 * HNSW cosine similarity
    let mut fused: HashMap<String, f32> = HashMap::new();
    for (id, norm_score) in &bm25_map {
        let cos_sim = hnsw_map.get(id).copied().unwrap_or(0.0);
        fused.insert(id.clone(), 0.4 * norm_score + 0.6 * cos_sim);
    }
    for (id, cos_sim) in &hnsw_map {
        if !fused.contains_key(id) {
            fused.insert(id.clone(), 0.6 * *cos_sim);
        }
    }

    let mut sorted: Vec<(String, f32)> = fused.into_iter().collect();
    sorted.sort_by(|a, b| {
        b.1.partial_cmp(&a.1)
            .unwrap_or(std::cmp::Ordering::Equal)
    });
    sorted.truncate(req.limit);

    // v0.9.0: Optional Cross-Encoder reranking (feature-gated behind `onnx`)
    if req.use_reranker {
        #[cfg(feature = "onnx")]
        {
            if let Some(reranker) = brain.reranker.as_ref() {
                if let Ok(rtxn) = brain.storage.begin_read() {
                    let candidate_texts: Vec<String> = sorted
                        .iter()
                        .map(|(id, _)| {
                            brain
                                .storage
                                .get_hippocampus(&rtxn, id)
                                .ok()
                                .flatten()
                                .map(|e| e.text)
                                .unwrap_or_default()
                        })
                        .collect();
                    let candidate_refs: Vec<&str> =
                        candidate_texts.iter().map(|s| s.as_str()).collect();

                    let reranked = reranker
                        .rerank(&req.query, &candidate_refs)
                        .unwrap_or_else(|e| {
                            eprintln!("memhop: reranker error, falling back: {e}");
                            sorted.iter().enumerate().map(|(i, _)| (i, 0.0_f32)).collect()
                        });

                    let original = std::mem::take(&mut sorted);
                    let mut reordered = Vec::with_capacity(reranked.len());
                    for (orig_idx, _) in reranked {
                        if orig_idx < original.len() {
                            reordered.push(original[orig_idx].clone());
                        }
                    }
                    sorted = reordered;
                } else {
                    eprintln!("memhop: failed to open read txn for reranker, skipping");
                }
            } else {
                // Warn only once about missing reranker to avoid log spam
                static WARNED: std::sync::OnceLock<bool> = std::sync::OnceLock::new();
                WARNED.get_or_init(|| {
                    eprintln!("memhop: reranker not loaded (model path not configured or load failed), skipping rerank");
                    true
                });
            }
        }
        #[cfg(not(feature = "onnx"))]
        {
            eprintln!("memhop: reranker requires `onnx` feature, skipping");
        }
    }

    // v0.10.0: Archived penalty — multiply score by 0.3 for archived engrams
    if let Ok(rtxn) = brain.storage.begin_read() {
        for (id, score) in sorted.iter_mut() {
            if let Ok(Some(engram)) = brain.storage.get_hippocampus(&rtxn, id)
                && engram.is_archived
            {
                *score *= 0.3;
            }
        }
    }
    sorted.sort_by(|a, b| {
        b.1.partial_cmp(&a.1)
            .unwrap_or(std::cmp::Ordering::Equal)
    });

    // Step 5: Load engrams from storage, classify by kind
    let mut associations: Vec<Engram> = Vec::new();
    let mut schemas: Vec<Engram> = Vec::new();
    let mut emotional_echoes: Vec<Engram> = Vec::new();
    let mut knowledge_memories: Vec<Engram> = Vec::new();
    let conflicts: Vec<ConflictItem> = Vec::new();
    // v0.9.1: Capture scores for turn-level aggregation
    let score_map: HashMap<String, f32> = sorted.iter().cloned().collect();

    if let Ok(rtxn) = brain.storage.begin_read() {
        for (id, _score) in &sorted {
            // v0.9.0: Try cache first, fall back to storage
            let engram = brain.engram_cache.borrow().get(id).cloned();
            let engram = match engram {
                Some(e) => e,
                None => {
                    if let Ok(Some(e)) = brain.storage.get_hippocampus(&rtxn, id) {
                        brain.engram_cache.borrow_mut().insert(id.clone(), e.clone());
                        e
                    } else {
                        continue;
                    }
                }
            };
            // v0.11.0: Apply kind_filter
            if !req.kind_filter.is_empty() && !req.kind_filter.contains(&engram.kind) {
                continue;
            }
            // v0.11.0: Apply tree filter
            if let Some(ref tree_path) = req.tree
                && engram.kind == EngramKind::Knowledge
                && engram.tree_path.as_deref() != Some(tree_path.as_str())
            {
                continue;
            }
            // v0.12.1: Apply tree_id filter (via tree_ref)
            if let Some(ref tree_id) = req.tree_id
                && engram.tree_ref.as_ref().map(|tr| &tr.tree_id) != Some(tree_id)
            {
                continue;
            }
            // v0.12.0: Apply time filter
            if req.time_from.is_some() || req.time_to.is_some() {
                let after = req.time_from.is_none_or(|t| engram.created_at >= t);
                let before = req.time_to.is_none_or(|t| engram.created_at <= t);
                if !(after && before) {
                    continue;
                }
            }
            match engram.kind {
                EngramKind::Knowledge => knowledge_memories.push(engram),
                EngramKind::Schema => schemas.push(engram),
                _ => {
                    if engram.arousal > 0.7 {
                        emotional_echoes.push(engram.clone());
                    }
                    associations.push(engram);
                }
            }
        }
    }

    // v0.12.0: 知识自动附带 — 从书架检索附加知识
    if req.attach_knowledge && brain.phase != Phase::Warmup {
        knowledge_memories = super::knowledge::recall_knowledge_attached(brain, query_vector);
    }

    // v0.11.0: Build tree_contexts from knowledge_memories
    let mut tree_contexts: Vec<TreeContext> = Vec::new();
    for e in &knowledge_memories {
        if let Some(ref tree_path) = e.tree_path {
            let domain = e.meta.get("domain")
                .and_then(|v| v.as_str())
                .unwrap_or("generic");
            if !tree_contexts.iter().any(|tc: &TreeContext| tc.tree_path == *tree_path) {
                let source_count = knowledge_memories.iter()
                    .filter(|ke| ke.tree_path.as_deref() == Some(tree_path.as_str()))
                    .count();
                tree_contexts.push(TreeContext {
                    tree_path: tree_path.clone(),
                    domain: domain.to_string(),
                    source_count,
                });
            }
        }
    }

    // v0.11.0: Build graph_associations from top results
    let mut graph_associations: Vec<GraphAssociation> = Vec::new();
    let mut seen: HashSet<String> = HashSet::new();
    for (id, _score) in &sorted {
        let edges = brain.graph.edges_of(id);
        for edge in edges {
            let pair_key = if *id < edge.target_id {
                format!("{}|{}", id, edge.target_id)
            } else {
                format!("{}|{}", edge.target_id, id)
            };
            if seen.contains(&pair_key) { continue; }
            seen.insert(pair_key);

            if edge.kind == AssociationKind::CoShelf {
                graph_associations.push(GraphAssociation {
                    source_id: id.clone(),
                    target_id: edge.target_id.clone(),
                    kind: edge.kind.clone(),
                    weight: edge.weight,
                    description: "CoShelf: same knowledge tree".to_string(),
                });
            }
        }
    }

    // Step 6: Record recalled IDs for Dream reconsolidation
    {
        let mut buf = brain.recalled_buffer.borrow_mut();
        for (id, _) in &sorted {
            if !buf.contains(id) {
                buf.push(id.clone());
            }
        }
    }

    // v0.12.1: 检测跨树命中 → 创建纠缠事件
    if brain.phase == Phase::Full {
        let mut tree_ids_set: HashSet<String> = HashSet::new();
        let mut node_ids: Vec<String> = Vec::new();
        for eng in associations.iter().chain(knowledge_memories.iter()) {
            if let Some(ref tr) = eng.tree_ref {
                tree_ids_set.insert(tr.tree_id.clone());
                node_ids.push(eng.id.clone());
            }
        }
        if tree_ids_set.len() >= 2 && node_ids.len() >= 2 {
            let context = "记忆在查询中跨树关联".to_string();
            let tree_ids: Vec<String> = tree_ids_set.into_iter().collect();
            crate::entanglement::create_or_update_entanglement(
                brain, node_ids, tree_ids, context, crate::entanglement::EntanglementTrigger::RecallCrossTree,
            );
        }
    }

    // v0.12.1: 展开纠缠事件节点
    crate::entanglement::expand_entangled_results(brain, &mut associations);

    // v0.12.1: 三观模式介入
    let (worldview_context, cognitive_conflicts) =
        crate::worldview::extract_worldview_context(brain, &req.query);

    // v0.9.1: Build turn-level hits from associated engrams
    let (hit_turns, aggregated_sessions) = brain.build_turn_hits(&associations, &score_map)
        .unwrap_or_default();

    // Step 7: L0 Cortex (working memory)
    let working_memory = brain.cortex.recent(&req.session_id, req.recent_limit);

    let latency_us = start.elapsed().as_micros() as u64;

    Ok(RecallResponse {
        working_memory,
        associations,
        schemas,
        emotional_echoes,
        conflicts,
        archive_results: None,
        hit_turns,
        aggregated_sessions,
        knowledge_memories,
        tree_contexts,
        graph_associations,
        worldview_context,
        cognitive_conflicts,
        trace: RecallTrace {
            latency_us,
            gated_anchors: req.attention_anchors.clone(),
            hopfield_candidates: sorted.len(),
            spread_steps: 0,
            post_inhibition_count: sorted.len(),
            pgt_layer: None,
        },
    })
}
