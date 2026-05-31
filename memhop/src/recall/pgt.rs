use std::collections::{HashMap, HashSet};

use crate::Brain;
use crate::error::{MemHopError, Result};
use crate::types::RecallRequest;

// ── PGT recall ────────────────────────────────────────────────

/// Plan-gated, four-layer associative recall.
pub(crate) fn pgt_recall(
    brain: &Brain,
    query_text: &str,
    query_emb: &[f32],
    req: &RecallRequest,
) -> (Vec<(String, f32)>, Option<String>) {
    let plan_id = match &req.active_plan_id {
        Some(pid) => pid,
        None => return (Vec::new(), None),
    };
    let need = req.limit;
    let mut results: Vec<(String, f32)> = Vec::new();
    let mut exclude: HashSet<String> = HashSet::new();

    // L0: Plan-scoped n-gram search
    let plan_candidates = brain.plan_index.borrow().candidates(Some(plan_id));
    if let Ok(l0) = recall_layer0(brain, query_text, &plan_candidates, need) {
        for (id, _) in &l0 { exclude.insert(id.clone()); }
        results.extend(l0);
    }
    if results.len() >= need {
        return (results, Some("L0".to_string()));
    }

    // L1: Graph BFS from L0 seeds
    let l1 = recall_layer1(brain, query_emb, &results, need - results.len(), &exclude);
    for (id, _) in &l1 { exclude.insert(id.clone()); }
    results.extend(l1);
    if results.len() >= need {
        return (results, Some("L1".to_string()));
    }

    // L2: Temporal recency
    if let Ok(l2) = recall_layer2(brain, plan_id, need - results.len(), &exclude) {
        for (id, _) in &l2 { exclude.insert(id.clone()); }
        results.extend(l2);
    }
    if results.len() >= need {
        return (results, Some("L2".to_string()));
    }

    // L3: Global n-gram fallback
    if let Ok(l3) = recall_layer3(brain, query_text, need - results.len(), &exclude) {
        results.extend(l3);
    }

    let layer = if results.is_empty() { "None" } else { "L3" };
    (results, Some(layer.to_string()))
}

/// L0: Plan-scoped n-gram — trigram Jaccard overlap within the plan's engrams.
pub(crate) fn recall_layer0(
    brain: &Brain,
    query_text: &str,
    candidates: &[String],
    need: usize,
) -> Result<Vec<(String, f32)>> {
    if candidates.is_empty() || need == 0 {
        return Ok(Vec::new());
    }
    let txn = brain
        .storage
        .begin_read()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;

    let mut scored: Vec<(String, f32)> = Vec::with_capacity(candidates.len().min(need * 4));
    for id in candidates.iter().take(candidates.len().min(need * 4)) {
        if let Ok(Some(engram)) = brain.storage.get_hippocampus(&txn, id) {
            let score = crate::brain::ngram_overlap(query_text, &engram.text);
            if score > 0.0 {
                scored.push((id.clone(), score));
            }
        }
    }
    drop(txn);

    scored.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
    scored.truncate(need);
    Ok(scored)
}

/// L1: Graph BFS — expand from seed IDs using graph edges.
pub(crate) fn recall_layer1(
    brain: &Brain,
    _query_emb: &[f32],
    seeds: &[(String, f32)],
    need: usize,
    exclude: &HashSet<String>,
) -> Vec<(String, f32)> {
    if seeds.is_empty() || need == 0 {
        return Vec::new();
    }
    let mut neighbor_scores: HashMap<String, f32> = HashMap::new();

    for (seed_id, seed_score) in seeds {
        for edge in brain.graph.edges_of(seed_id) {
            if exclude.contains(&edge.target_id) || edge.target_id == *seed_id {
                continue;
            }
            let score = edge.weight * seed_score;
            let entry = neighbor_scores.entry(edge.target_id.clone()).or_insert(0.0);
            *entry = entry.max(score);
        }
    }

    let mut scored: Vec<(String, f32)> = neighbor_scores.into_iter().collect();
    scored.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
    scored.truncate(need);
    scored
}

/// L2: Temporal recency — most recent engrams in the active plan.
pub(crate) fn recall_layer2(
    brain: &Brain,
    active_plan_id: &str,
    need: usize,
    exclude: &HashSet<String>,
) -> Result<Vec<(String, f32)>> {
    if need == 0 {
        return Ok(Vec::new());
    }
    let candidates = brain.plan_index.borrow().candidates(Some(active_plan_id));
    if candidates.is_empty() {
        return Ok(Vec::new());
    }

    let txn = brain
        .storage
        .begin_read()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;

    let now = crate::brain::now_millis();
    let mut with_times: Vec<(String, f32)> = Vec::with_capacity(candidates.len());

    for id in &candidates {
        if exclude.contains(id) {
            continue;
        }
        if let Ok(Some(engram)) = brain.storage.get_hippocampus(&txn, id) {
            let hours_ago = ((now - engram.created_at).max(0) as f64) / 3_600_000.0;
            let recency = 1.0f64 / (1.0 + hours_ago / 24.0);
            with_times.push((id.to_string(), recency as f32));
        }
    }
    drop(txn);

    with_times.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
    with_times.truncate(need);
    Ok(with_times)
}

/// L3: Global n-gram fallback — scan all engrams (not just active plan).
pub(crate) fn recall_layer3(
    brain: &Brain,
    query_text: &str,
    need: usize,
    exclude: &HashSet<String>,
) -> Result<Vec<(String, f32)>> {
    if need == 0 {
        return Ok(Vec::new());
    }
    let candidates = brain.plan_index.borrow().candidates(None);
    let filtered: Vec<String> = candidates
        .into_iter()
        .filter(|id| !exclude.contains(id))
        .collect();
    recall_layer0(brain, query_text, &filtered, need)
}

/// Hopfield fallback: recall among candidates within the active plan.
pub(crate) fn hopfield_candidates_in_plan(
    brain: &Brain,
    query_emb: &[f32],
    plan_id: &str,
    top_k: usize,
    exclude: &HashSet<String>,
) -> Vec<(String, f32)> {
    if brain.hopfield.is_empty() {
        return Vec::new();
    }
    let candidates = brain.plan_index.borrow().candidates(Some(plan_id));
    let candidate_refs: Vec<&str> = candidates.iter().map(|s: &String| s.as_str()).collect();

    brain.hopfield
        .recall_among_raw(query_emb, &candidate_refs)
        .into_iter()
        .filter(|(id, _)| !exclude.contains(id))
        .take(top_k)
        .collect()
}
