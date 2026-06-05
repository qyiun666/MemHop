//! dream/rem — REM stages: topic merging + reflection + plan consolidation.

use crate::brain::Brain;
use crate::error::{Result, MemHopError};
use crate::types::ConsolidateReport;
use crate::organize;

/// Merge similar topics based on label ngram overlap.
/// Creates TopicEdge::Related edges between similar topics.
pub fn rem_merge_topics(brain: &mut Brain, report: &mut ConsolidateReport) -> Result<()> {
    let threshold = 0.7;
    let merged = organize::reflect::merge_similar_topics(brain, threshold)?;
    report.topics_merged = merged;
    report.schemas_emerged = merged;
    Ok(())
}

/// Reflect: update each topic summary from its L1 nodes.
/// v0.17.0: 如果 topic 已有 LLM 提供的非空 summary，reflect_topic 内部跳过覆写。
pub fn rem_reflect_topics(brain: &mut Brain, report: &mut ConsolidateReport) -> Result<()> {
    let topic_ids: Vec<String> = {
        let txn = brain.l2_env.env.read_txn()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let mut ids = Vec::new();
        if let Ok(iter) = brain.l2_env.topics.iter(&txn) {
            for item in iter {
                if let Ok((key, _bytes)) = item {
                    if !key.starts_with("topic:") || !key.ends_with(":meta") { continue; }
                    // Extract topic_id from "topic:{id}:meta"
                    let id = key.trim_start_matches("topic:").trim_end_matches(":meta");
                    ids.push(id.to_string());
                }
            }
        }
        ids
    };

    let mut reflected = 0u32;
    for tid in &topic_ids {
        if organize::reflect::reflect_topic(brain, tid).is_ok() {
            reflected += 1;
        }
    }
    report.topics_reflected = reflected;
    Ok(())
}

/// v0.17.0: Plan consolidation 已废弃——改由 LLM 通过 memhop_update_topic 完成。
/// 保留函数签名避免编译错误，但不再被 consolidate 调用。
pub fn rem_plan_consolidate(_brain: &mut Brain, _report: &mut ConsolidateReport) -> Result<()> {
    // v0.17.0: plan compression 由 LLM 负责，此处不再执行
    Ok(())
}
