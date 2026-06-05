//! recall — enhanced recall modes: associative diffusion, topic-filtered search,
//! activated-topic priority.

use crate::brain::Brain;
use crate::error::Result;
use crate::types::{RecallRequest, RecallResponse, RecallResult, Layer};
use crate::query_engine;
use crate::query_engine::cross_layer_validation;

pub mod associative;

/// Enhanced recall dispatch.
/// Priority order:
/// 1. Activated topic priority search (if session_id + activated topics exist)
/// 2. Associative diffusion (if spread_depth > 0)
/// 3. Standard layer-by-layer search
/// 4. Topic filter post-processing
pub fn enhanced_recall(brain: &Brain, req: &RecallRequest) -> Result<RecallResponse> {
    // If associative mode is requested, delegate to associative module
    if req.spread_depth.is_some() && req.spread_depth.unwrap_or(0) > 0 {
        return associative::associative_recall(brain, req);
    }

    // Standard recall
    let mut resp = query_engine::execute(brain, req)?;
    
    // v0.18.0: 跨层结果验证
    cross_layer_validation(&mut resp.results, brain);

    // Activated topic priority: boost results from activated topics
    if let Some(ref session_id) = req.session_id {
        let active_topic_ids = brain.session_mgr.get_active_topic_ids(session_id);
        if !active_topic_ids.is_empty() {
            resp = boost_activated_topics(brain, resp, &active_topic_ids, req.max_results)?;
        }
    }

    // Apply topic filter if specified
    if let Some(ref filter) = req.topic_filter {
        resp.results = filter_by_topic(brain, resp.results, filter)?;
        resp.total_count = resp.results.len();
    }

    Ok(resp)
}

/// Boost recall results that belong to currently activated topics.
/// Strategy: search L1 nodes within activated topics, then RRF-merge with
/// standard results giving activated results a rank boost.
fn boost_activated_topics(
    brain: &Brain,
    mut resp: RecallResponse,
    active_topic_ids: &[String],
    max: usize,
) -> Result<RecallResponse> {
    // Collect all node_ids from activated topics
    let txn = brain.l2_env.env.read_txn()
        .map_err(|e| crate::error::MemHopError::Storage(e.to_string()))?;
    let mut activated_node_ids: Vec<String> = Vec::new();
    for tid in active_topic_ids {
        if let Ok(Some(topic)) = brain.l2.get_topic_by_id(&txn, &brain.l2_env, tid) {
            activated_node_ids.extend(topic.node_ids);
        }
    }
    drop(txn);

    if activated_node_ids.is_empty() {
        return Ok(resp);
    }

    // Find overlap: standard results that are in activated node set
    let activated_set: std::collections::HashSet<String> =
        activated_node_ids.iter().cloned().collect();

    // Boost score of results that belong to activated topics
    for r in &mut resp.results {
        if r.layer == Layer::L1 && activated_set.contains(&r.id) {
            r.score *= 1.5; // 1.5x boost for activated context
        }
    }

    // Re-sort by boosted score
    resp.results.sort_by(|a, b| {
        b.score.partial_cmp(&a.score).unwrap_or(std::cmp::Ordering::Equal)
    });
    resp.results.truncate(max);

    Ok(resp)
}

/// Filter recall results to only include nodes belonging to a specific L2 topic.
fn filter_by_topic(brain: &Brain, results: Vec<RecallResult>, topic_id: &str) -> Result<Vec<RecallResult>> {
    let txn = brain.l2_env.env.read_txn()
        .map_err(|e| crate::error::MemHopError::Storage(e.to_string()))?;

    let topic = match brain.l2.get_topic_by_id(&txn, &brain.l2_env, topic_id)? {
        Some(t) => t,
        None => return Err(crate::error::MemHopError::NotFound(format!("topic {} not found", topic_id))),
    };

    // Only keep results whose IDs are in the topic's node_ids list
    let filtered = results.into_iter()
        .filter(|r| topic.node_ids.contains(&r.id))
        .collect();

    Ok(filtered)
}
