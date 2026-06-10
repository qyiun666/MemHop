//! dream/rem — REM stages: topic merging + reflection + plan consolidation.

use crate::brain::Brain;
use crate::error::{MemHopError, Result};
use crate::organize;
use crate::types::ConsolidateReport;

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
    let store = brain.redb_store.as_ref()
        .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;
    let topics = store.l2_list_topics()?;
    let mut reflected = 0u32;
    for topic in &topics {
        if organize::reflect::reflect_topic(brain, &topic.id).is_ok() {
            reflected += 1;
        }
    }
    report.topics_reflected = reflected;
    Ok(())
}

/// v0.25.0: Plan consolidation — 调用 organize::plan::consolidate_plan_summaries 压缩并聚合话题摘要。
pub fn rem_plan_consolidate(brain: &mut Brain, report: &mut ConsolidateReport) -> Result<()> {
    match organize::plan::consolidate_plan_summaries(brain) {
        Ok(n) => {
            report.topics_consolidated = n;
        }
        Err(e) => {
            eprintln!("[dream] plan consolidate error: {}", e);
        }
    }
    Ok(())
}
