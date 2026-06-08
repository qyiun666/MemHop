//! recall — enhanced recall modes: associative diffusion, topic-filtered search,
//! activated-topic priority.

use crate::brain::Brain;
use crate::error::Result;
use crate::types::{Layer, RecallRequest, RecallResponse, RecallResult};

pub mod associative;
pub mod cascade;

/// Enhanced recall dispatch.
/// v0.23.1: 默认使用级联检索模式（仿人脑激活优先）
/// Priority order:
/// 1. Associative diffusion (if spread_depth > 0)
/// 2. CascadingRecall (默认模式：激活 L2 → 扩展 L2 → L3 → L1)
pub fn enhanced_recall(brain: &mut Brain, req: &RecallRequest) -> Result<RecallResponse> {
    // If associative mode is requested, delegate to associative module
    if req.spread_depth.is_some() && req.spread_depth.unwrap_or(0) > 0 {
        return associative::associative_recall(brain, req);
    }

    // 默认使用级联模式（仿人脑激活优先）
    cascade::cascade_recall(brain, req)
}

/// Boost recall results that belong to currently activated topics.
/// Strategy: search L1 nodes within activated topics, then RRF-merge with
/// standard results giving activated results a rank boost.
#[allow(dead_code)]
fn boost_activated_topics(
    brain: &mut Brain,
    mut resp: RecallResponse,
    active_topic_ids: &[String],
    max: usize,
) -> Result<RecallResponse> {
    // Collect all node_ids from activated topics
    brain.ensure_l2()?;
    let l2 = brain.l2.as_mut().unwrap();
    let l2_env = brain.l2_env.as_ref().unwrap();
    let txn = l2_env
        .env
        .read_txn()
        ?;
    let mut activated_node_ids: Vec<String> = Vec::new();
    for tid in active_topic_ids {
        if let Ok(Some(topic)) = l2.get_topic_by_id(&txn, l2_env, tid) {
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
        b.score
            .partial_cmp(&a.score)
            .unwrap_or(std::cmp::Ordering::Equal)
    });
    resp.results.truncate(max);

    Ok(resp)
}

/// Filter recall results to only include nodes belonging to a specific L2 topic.
#[allow(dead_code)]
fn filter_by_topic(
    brain: &mut Brain,
    results: Vec<RecallResult>,
    topic_id: &str,
) -> Result<Vec<RecallResult>> {
    brain.ensure_l2()?;
    let l2 = brain.l2.as_mut().unwrap();
    let l2_env = brain.l2_env.as_ref().unwrap();
    let txn = l2_env
        .env
        .read_txn()
        ?;

    let topic = match l2.get_topic_by_id(&txn, l2_env, topic_id)? {
        Some(t) => t,
        None => {
            return Err(crate::error::MemHopError::NotFound(format!(
                "topic {} not found",
                topic_id
            )));
        }
    };

    // Only keep results whose IDs are in the topic's node_ids list
    let filtered = results
        .into_iter()
        .filter(|r| topic.node_ids.contains(&r.id))
        .collect();

    Ok(filtered)
}
