//! Recall dispatch — associative mode (PGT + Hopfield + graph spread) and retrieval mode dispatch.

use std::collections::{HashMap, HashSet};
use std::time::Instant;

use half::f16;

use crate::activation;
use crate::brain::Brain;
use crate::context::Phase;
use crate::engram::{AssociationKind, Engram, EngramKind};
use crate::entanglement::EntanglementTrigger;
use crate::scene_gating::SceneGate;
use crate::types::{
    ConflictItem, GraphAssociation, RecallMode, RecallRequest, RecallResponse, RecallTrace, TreeContext,
};
use crate::error::Result;

const HOPFIELD_TOP_K: usize = 200;

/// Dispatch recall by mode. Retrieval → recall_retrieval(), default → associative path.
pub(crate) fn recall_associative(brain: &Brain, req: &RecallRequest) -> Result<RecallResponse> {
    let start = Instant::now();

    // 1. Query vector
    let query_vector: Vec<f16> = match &req.query_vector {
        Some(v) => v.clone(),
        None => brain.encode_text(&req.query),
    };
    let query_f32: Vec<f32> = query_vector.iter().map(|&x| x.to_f32()).collect();

    // v0.13.2: Semantic Gate — auto-select best matching context when none specified
    // Uses read-only context matching; match_context() requires &mut which is not
    // available in recall_associative(&self).
    let context_id = req.context_id.clone().or_else(|| {
        let now = crate::brain::now_millis();
        let best = brain.active_contexts.contexts().iter()
            .filter(|ctx| ctx.hit_count > 0)
            .max_by(|a, b| {
                let score_a = a.match_score(&query_f32, brain.config.context_half_life_hours, now);
                let score_b = b.match_score(&query_f32, brain.config.context_half_life_hours, now);
                score_a.partial_cmp(&score_b).unwrap_or(std::cmp::Ordering::Equal)
            });
        best.map(|ctx| ctx.id.clone())
    });
    let req_with_ctx = RecallRequest {
        context_id: context_id.clone(),
        ..req.clone()
    };

    // v0.9.0: Mode dispatch
    match req_with_ctx.mode {
        RecallMode::Retrieval => {
            return crate::recall::retrieval::recall_retrieval(brain, &req_with_ctx, &query_vector, start);
        }
        RecallMode::Associative => {}
    }

    // 2. L0 Cortex
    let working_memory = brain.cortex.recent(&req_with_ctx.session_id, req_with_ctx.recent_limit);

    // 3. PGT recall
    let (pgt_results, pgt_layer) = if req_with_ctx.active_plan_id.is_some() {
        crate::recall::pgt::pgt_recall(brain, &req_with_ctx.query, &query_f32, &req_with_ctx)
    } else {
        (Vec::new(), None)
    };

    // 4. Build seeds
    let mut hopfield_count: usize = 0;
    let seeds: HashMap<String, f32> = if pgt_results.len() >= req.limit {
        pgt_results.into_iter().take(req.spread_top_k * 2).collect()
    } else if let Some(ref plan_id) = req.active_plan_id {
        let exclude: HashSet<String> =
            pgt_results.iter().map(|(id, _)| id.clone()).collect();
        let remaining = req.spread_top_k * 2 - pgt_results.len();
        let hopfield_supp = crate::recall::pgt::hopfield_candidates_in_plan(
            brain, &query_f32, plan_id, remaining, &exclude,
        );
        hopfield_count = hopfield_supp.len();
        pgt_results.into_iter().chain(hopfield_supp).take(req.spread_top_k * 2).collect()
    } else {
        let hopfield_candidates: Vec<(String, f32)> = if brain.hopfield.is_empty() {
            Vec::new()
        } else {
            brain.hopfield.recall_topk(&query_f32, HOPFIELD_TOP_K)
        };

        let hopfield_candidates = if !req.attention_anchors.is_empty() {
            if let Ok(Some(candidates)) =
                SceneGate::get_candidates(&brain.storage, &req.attention_anchors)
            {
                hopfield_candidates.into_iter().filter(|(id, _)| candidates.contains(id)).collect()
            } else {
                hopfield_candidates
            }
        } else {
            hopfield_candidates
        };
        hopfield_count = hopfield_candidates.len();
        hopfield_candidates.into_iter().take(req.spread_top_k * 2).collect()
    };

    // 5. Competitive spread activation
    let spread_result = activation::competitive_spread(
        &brain.graph, &seeds, &brain.personality, req.spread_top_k,
    );
    let score_map: HashMap<String, f32> = spread_result.activated.iter().cloned().collect();

    // 6. Load activated engrams
    let mut associations: Vec<Engram> = Vec::new();
    let mut schemas: Vec<Engram> = Vec::new();
    let mut emotional_echoes: Vec<Engram> = Vec::new();
    let mut knowledge_memories: Vec<Engram> = Vec::new();
    let mut conflicts: Vec<ConflictItem> = Vec::new();

    if let Ok(rtxn) = brain.storage.begin_read() {
        let activated_ids: Vec<String> = spread_result.activated.iter().map(|(id, _)| id.clone()).collect();

        // Batch load: first check cache, then batch-read missed IDs from LMDB
        let mut all_engrams: Vec<(String, Engram)> = Vec::with_capacity(activated_ids.len());
        let mut missed: Vec<String> = Vec::new();
        for id in &activated_ids {
            if let Some(e) = brain.engram_cache.borrow().get(id).cloned() {
                all_engrams.push((id.clone(), e));
            } else {
                missed.push(id.clone());
            }
        }
        if !missed.is_empty() {
            if let Ok(batch) = brain.storage.get_hippocampus_batch(&rtxn, &missed) {
                let mut cache = brain.engram_cache.borrow_mut();
                for (id, e) in &batch {
                    cache.insert(id.clone(), e.clone());
                    all_engrams.push((id.clone(), e.clone()));
                }
            }
        }

        for (_id, engram) in all_engrams {
            if !req.kind_filter.is_empty() && !req.kind_filter.contains(&engram.kind) { continue; }
            if let Some(ref tree_path) = req.tree
                && engram.kind == EngramKind::Knowledge
                && engram.tree_path.as_deref() != Some(tree_path.as_str()) { continue; }
            if let Some(ref tree_id) = req.tree_id
                && engram.tree_ref.as_ref().map(|tr| &tr.tree_id) != Some(tree_id) { continue; }
            if req.time_from.is_some() || req.time_to.is_some() {
                let after = req.time_from.is_none_or(|t| engram.created_at >= t);
                let before = req.time_to.is_none_or(|t| engram.created_at <= t);
                if !(after && before) { continue; }
            }
            // v0.13.0: Apply context_id filter
            if let Some(ref ctx_id) = req.context_id
                && engram.context_id.as_deref() != Some(ctx_id.as_str())
            {
                continue;
            }
            match engram.kind {
                EngramKind::Knowledge => knowledge_memories.push(engram),
                EngramKind::Schema => schemas.push(engram),
                _ => {
                    if engram.arousal > 0.7 { emotional_echoes.push(engram.clone()); }
                    associations.push(engram);
                }
            }
        }

        associations.sort_by(|a, b| {
            let score_a = activation::emotional_alignment(req.emotional_state.valence, req.emotional_state.arousal, a);
            let score_b = activation::emotional_alignment(req.emotional_state.valence, req.emotional_state.arousal, b);
            score_b.partial_cmp(&score_a).unwrap_or(std::cmp::Ordering::Equal)
        });

        let id_set: HashSet<String> = activated_ids.into_iter().collect();
        for (a, b) in brain.graph.contradiction_pairs_in(&id_set) {
            conflicts.push(ConflictItem { memory_a_id: a, memory_b_id: b, conflict_type: "contradiction".to_string() });
        }
    }

    // v0.11.0: tree_contexts
    let mut tree_contexts: Vec<TreeContext> = Vec::new();
    for e in &knowledge_memories {
        if let Some(ref tree_path) = e.tree_path {
            let domain = e.meta.get("domain").and_then(|v| v.as_str()).unwrap_or("generic");
            if !tree_contexts.iter().any(|tc| tc.tree_path == *tree_path) {
                let source_count = knowledge_memories.iter()
                    .filter(|ke| ke.tree_path.as_deref() == Some(tree_path.as_str())).count();
                tree_contexts.push(TreeContext { tree_path: tree_path.clone(), domain: domain.to_string(), source_count });
            }
        }
    }

    // graph_associations
    let mut graph_associations: Vec<GraphAssociation> = Vec::new();
    let mut seen: HashSet<String> = HashSet::new();
    for (id, _score) in &spread_result.activated {
        let edges = brain.graph.edges_of(id);
        for edge in edges {
            let pair_key = if *id < edge.target_id { format!("{}|{}", id, edge.target_id) } else { format!("{}|{}", edge.target_id, id) };
            if seen.contains(&pair_key) { continue; }
            seen.insert(pair_key);
            if edge.kind == AssociationKind::CoShelf {
                graph_associations.push(GraphAssociation {
                    source_id: id.clone(), target_id: edge.target_id.clone(),
                    kind: edge.kind.clone(), weight: edge.weight,
                    description: "CoShelf: same knowledge tree".to_string(),
                });
            }
        }
    }

    // recalled_buffer
    {
        let mut buf = brain.recalled_buffer.borrow_mut();
        for (id, _) in &spread_result.activated {
            if !buf.contains(id) { buf.push(id.clone()); }
        }
    }

    // v0.12.1: entanglement detection
    if brain.phase == Phase::Full {
        let mut tree_ids_set: HashSet<String> = HashSet::new();
        let mut node_ids: Vec<String> = Vec::new();
        let mut context_ids: Vec<String> = Vec::new();
        for eng in associations.iter().chain(knowledge_memories.iter()) {
            if let Some(ref tr) = eng.tree_ref {
                tree_ids_set.insert(tr.tree_id.clone());
                node_ids.push(eng.id.clone());
            }
            // v0.13.0: collect context IDs
            if let Some(ref ctx_id) = eng.context_id
                && !context_ids.contains(ctx_id)
            {
                context_ids.push(ctx_id.clone());
            }
        }
        if tree_ids_set.len() >= 2 && node_ids.len() >= 2 {
            let tree_ids: Vec<String> = tree_ids_set.into_iter().collect();
            crate::entanglement::create_or_update_entanglement(
                brain, node_ids, tree_ids,
                "记忆在查询中跨树关联".to_string(),
                EntanglementTrigger::RecallCrossTree,
                context_ids,
            );
        }

        // v0.13.0: Push pending tree edges for context↔tree association
        for eng in associations.iter().chain(knowledge_memories.iter()) {
            if let Some(ref ctx_id) = eng.context_id
                && let Some(ref tr) = eng.tree_ref
            {
                brain.pending_tree_edges.borrow_mut().push(
                    crate::brain::PendingTreeEdge {
                        context_id: ctx_id.clone(),
                        tree_id: tr.tree_id.clone(),
                        delta: 0.1,
                    }
                );
            }
        }
    }

    crate::entanglement::expand_entangled_results(brain, &mut associations);
    let (worldview_context, cognitive_conflicts) = crate::worldview::extract_worldview_context(brain, &req.query, &query_vector);
    let (hit_turns, aggregated_sessions) = crate::query::build_turn_hits(brain, &associations, &score_map).unwrap_or_default();
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
        scores: score_map,
        trace: RecallTrace {
            latency_us,
            gated_anchors: req.attention_anchors.clone(),
            hopfield_candidates: hopfield_count,
            spread_steps: 3,
            post_inhibition_count: spread_result.activated.len(),
            pgt_layer,
        },
    })
}
