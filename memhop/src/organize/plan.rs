//! organize/plan — plan management mapped to L2 Topic operations.
//! Plan = L2 Topic in the v0.15.0 architecture.

use crate::brain::Brain;
use crate::error::{MemHopError, Result};
use std::collections::HashMap;

/// Set/update a plan name (maps to Topic label).
pub fn set_plan_name(brain: &mut Brain, topic_id: &str, name: &str) -> Result<()> {
    let txn = brain
        .l2_env
        .env
        .read_txn()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    let _topic = match brain.l2.get_topic_by_id(&txn, &brain.l2_env, topic_id)? {
        Some(t) => t,
        None => {
            return Err(MemHopError::NotFound(format!(
                "topic {} not found",
                topic_id
            )));
        }
    };
    drop(txn);

    let env = brain.l2_env.env.clone();
    let mut wtxn = env
        .write_txn()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    let key = format!("topic:{}:meta", topic_id);
    if let Some(bytes) = brain
        .l2_env
        .topics
        .get(&wtxn, &key)
        .map_err(|e| MemHopError::Storage(e.to_string()))?
        && let Ok(mut t) = bincode::deserialize::<crate::engram::Topic>(bytes)
    {
        t.label = name.to_string();
        t.updated_at = chrono::Utc::now().timestamp_millis();
        let new_bytes = bincode::serialize(&t).map_err(|e| MemHopError::Storage(e.to_string()))?;
        brain
            .l2_env
            .topics
            .put(&mut wtxn, &key, &new_bytes)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
    }
    wtxn.commit()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    Ok(())
}

/// Get plan tree: return all topics for a given session.
pub fn get_plan_tree(brain: &Brain, session_id: &str) -> Result<Vec<crate::engram::Topic>> {
    // Find node_ids for this session from L4 session_index
    let txn = brain
        .l4_env
        .env
        .read_txn()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    let session_key = format!("session:{}", session_id);
    let node_ids: Vec<String> = match brain
        .l4_env
        .session_index
        .get(&txn, &session_key)
        .map_err(|e| MemHopError::Storage(e.to_string()))?
    {
        Some(bytes) => bincode::deserialize(bytes).unwrap_or_default(),
        None => Vec::new(),
    };
    drop(txn);

    // For each node, find which topic it belongs to
    let txn = brain
        .l1_env
        .env
        .read_txn()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    let mut topic_ids: Vec<String> = Vec::new();
    for nid in &node_ids {
        if let Ok(Some(_node)) = brain.l1.get_node(&txn, &brain.l1_env, nid) {
            // Look up hyperedges to find topic association
            if let Ok(Some(bytes)) = brain.l1_env.node_to_hyperedges.get(&txn, nid)
                && let Ok(he_ids) = bincode::deserialize::<Vec<String>>(bytes)
            {
                for he_id in &he_ids {
                    if he_id.starts_with("he_") {
                        // Check if this hyperedge connects to a topic
                        if let Ok(Some(he)) = brain.l1.get_hyperedge(&txn, &brain.l1_env, he_id)
                            && !topic_ids.contains(&he.id)
                        {
                            topic_ids.push(he.id.clone());
                        }
                    }
                }
            }
        }
    }
    drop(txn);

    // Collect all topics
    let mut topics = Vec::new();
    let txn = brain
        .l2_env
        .env
        .read_txn()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    for tid in &topic_ids {
        if let Ok(Some(t)) = brain.l2.get_topic_by_id(&txn, &brain.l2_env, tid) {
            topics.push(t);
        }
    }

    Ok(topics)
}

/// Complete a plan: mark it by creating an archival hyperedge.
pub fn complete_plan(brain: &mut Brain, topic_id: &str) -> Result<()> {
    // Add a Merged hyperedge to L1 to signal plan completion
    let env = brain.l1_env.env.clone();
    let mut wtxn = env
        .write_txn()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;

    let txn = brain
        .l2_env
        .env
        .read_txn()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    let topic = match brain.l2.get_topic_by_id(&txn, &brain.l2_env, topic_id)? {
        Some(t) => t,
        None => {
            return Err(MemHopError::NotFound(format!(
                "topic {} not found",
                topic_id
            )));
        }
    };
    drop(txn);

    if !topic.node_ids.is_empty() {
        brain.l1.add_hyperedge(
            &mut wtxn,
            &brain.l1_env,
            topic.node_ids.clone(),
            crate::types::HyperedgeKind::Merged,
            1.0,
            None,
            Some("plan_complete".to_string()),
        )?;
    }

    wtxn.commit()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    Ok(())
}

/// Consolidate plan summaries: for each L2 topic, aggregate all member node
/// summaries into a single compressed summary, and consolidate L4 doc_ids.
/// Returns the number of topics consolidated.
pub fn consolidate_plan_summaries(brain: &mut Brain) -> Result<u32> {
    let env = brain.l2_env.env.clone();
    let txn = env
        .read_txn()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;

    // Collect all topics
    let mut topics = Vec::new();
    if let Ok(iter) = brain.l2_env.topics.iter(&txn) {
        for (key, bytes) in iter.flatten() {
            if !key.starts_with("topic:") || !key.ends_with(":meta") {
                continue;
            }
            if let Ok(t) = bincode::deserialize::<crate::engram::Topic>(bytes) {
                topics.push(t);
            }
        }
    }
    drop(txn);

    if topics.is_empty() {
        return Ok(0);
    }

    let mut consolidated = 0u32;
    let mut wtxn = env
        .write_txn()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;

    for topic in &topics {
        if topic.node_ids.is_empty() {
            continue;
        }

        // Aggregate L1 node summaries
        let mut summary_parts: Vec<String> = Vec::new();
        let mut all_keywords: HashMap<String, f32> = HashMap::new();
        let l1_txn = brain
            .l1_env
            .env
            .read_txn()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;

        for nid in &topic.node_ids {
            if let Ok(Some(node)) = brain.l1.get_node(&l1_txn, &brain.l1_env, nid) {
                // Use node summary if available, else first 100 chars of text
                let text = node
                    .summary
                    .unwrap_or_else(|| node.text.chars().take(100).collect());
                if !text.is_empty() && !summary_parts.contains(&text) {
                    summary_parts.push(text);
                }
                // Aggregate keywords
                for kw in &node.keywords {
                    *all_keywords.entry(kw.clone()).or_insert(0.0) += 1.0;
                }
                for (k, v) in &node.sparse {
                    *all_keywords.entry(k.clone()).or_insert(0.0) += *v;
                }
            }
        }
        drop(l1_txn);

        if summary_parts.is_empty() {
            continue;
        }

        // Build consolidated summary: top keywords + time range
        let mut top_kw: Vec<(String, f32)> = all_keywords.into_iter().collect();
        top_kw.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
        top_kw.truncate(15);
        let kw_str: Vec<String> = top_kw.iter().map(|(k, _)| k.clone()).collect();

        let time_info = match topic.dialogue_range {
            Some((start, end)) => {
                let start_str = format_timestamp(start);
                let end_str = format_timestamp(end);
                format!("时间范围: {} ~ {}", start_str, end_str)
            }
            None => String::new(),
        };

        let consolidated_summary = format!(
            "[{}] 关键词: {} | {} | L4原文数: {}",
            topic.label,
            kw_str.join(", "),
            time_info,
            topic.doc_ids.len(),
        );

        // Write back
        let key = format!("topic:{}:meta", &topic.id);
        if let Some(bytes) = brain
            .l2_env
            .topics
            .get(&wtxn, &key)
            .map_err(|e| MemHopError::Storage(e.to_string()))?
            && let Ok(mut t) = bincode::deserialize::<crate::engram::Topic>(bytes)
        {
            t.summary = Some(consolidated_summary);
            t.keywords = kw_str;
            t.updated_at = chrono::Utc::now().timestamp_millis();
            let new_bytes =
                bincode::serialize(&t).map_err(|e| MemHopError::Storage(e.to_string()))?;
            brain
                .l2_env
                .topics
                .put(&mut wtxn, &key, &new_bytes)
                .map_err(|e| MemHopError::Storage(e.to_string()))?;
            consolidated += 1;
        }
    }

    wtxn.commit()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    Ok(consolidated)
}

/// Format a millisecond timestamp to a human-readable date string.
fn format_timestamp(ms: i64) -> String {
    if ms <= 0 {
        return "未知".to_string();
    }
    // Simple formatting: YYYY-MM-DD HH:MM
    let secs = ms / 1000;
    let days = secs / 86400;
    // Approximate (not accounting for leap years perfectly)
    let year = 1970 + days / 365;
    let month = ((days % 365) / 30) + 1;
    let day = (days % 30) + 1;
    let hour = (secs % 86400) / 3600;
    let min = (secs % 3600) / 60;
    format!("{:04}-{:02}-{:02} {:02}:{:02}", year, month, day, hour, min)
}
