//! organize/plan — plan management mapped to L2 Topic operations.
//! Plan = L2 Topic in the v0.15.0 architecture.

use crate::brain::Brain;
use crate::error::{MemHopError, Result};
use std::collections::HashMap;

/// Set/update a plan name (maps to Topic label).
pub fn set_plan_name(brain: &mut Brain, topic_id: &str, name: &str) -> Result<()> {
    brain.ensure_l2()?;
    let l2 = brain.l2.as_mut().unwrap();
    let l2_env = brain.l2_env.as_ref().unwrap();
    let txn = l2_env
        .env
        .read_txn()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    let _topic = match l2.get_topic_by_id(&txn, l2_env, topic_id)? {
        Some(t) => t,
        None => {
            return Err(MemHopError::NotFound(format!(
                "topic {} not found",
                topic_id
            )));
        }
    };
    drop(txn);

    let env = l2_env.env.clone();
    let mut wtxn = env
        .write_txn()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    let key = format!("topic:{}:meta", topic_id);
    if let Some(bytes) = l2_env
        .topics
        .get(&wtxn, &key)
        .map_err(|e| MemHopError::Storage(e.to_string()))?
        && let Ok(mut t) = bincode::deserialize::<crate::engram::Topic>(bytes)
    {
        t.label = name.to_string();
        t.updated_at = chrono::Utc::now().timestamp_millis();
        let new_bytes = bincode::serialize(&t).map_err(|e| MemHopError::Storage(e.to_string()))?;
        l2_env
            .topics
            .put(&mut wtxn, &key, &new_bytes)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
    }
    wtxn.commit()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    Ok(())
}

/// Get plan tree: return all topics for a given session.
pub fn get_plan_tree(brain: &mut Brain, session_id: &str) -> Result<Vec<crate::engram::Topic>> {
    // Find node_ids for this session from L4 session_index
    brain.ensure_l4_env()?;
    let l4_env_ref = brain.l4_env.as_ref().unwrap();
    let txn = l4_env_ref
        .env
        .read_txn()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    let session_key = format!("session:{}", session_id);
    let node_ids: Vec<String> = match l4_env_ref
        .session_index
        .get(&txn, &session_key)
        .map_err(|e| MemHopError::Storage(e.to_string()))?
    {
        Some(bytes) => bincode::deserialize(bytes).unwrap_or_default(),
        None => Vec::new(),
    };
    drop(txn);

    // For each node, find which topic it belongs to
    let topic_ids: Vec<String> = {
        brain.ensure_l1()?;
        let l1 = brain.l1.as_mut().unwrap();
        let l1_env = brain.l1_env.as_ref().unwrap();
        let txn = l1_env
            .env
            .read_txn()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let mut ids: Vec<String> = Vec::new();
        for nid in &node_ids {
            if let Ok(Some(_node)) = l1.get_node(&txn, l1_env, nid) {
                // Look up hyperedges to find topic association
                if let Ok(Some(bytes)) = l1_env.node_to_hyperedges.get(&txn, nid)
                    && let Ok(he_ids) = bincode::deserialize::<Vec<String>>(bytes)
                {
                    for he_id in &he_ids {
                        if he_id.starts_with("he_") {
                            // Check if this hyperedge connects to a topic
                            if let Ok(Some(he)) = l1.get_hyperedge(&txn, l1_env, he_id)
                                && !ids.contains(&he.id)
                            {
                                ids.push(he.id.clone());
                            }
                        }
                    }
                }
            }
        }
        drop(txn);
        ids
    };

    // Collect all topics
    {
        brain.ensure_l2()?;
        let l2 = brain.l2.as_mut().unwrap();
        let l2_env = brain.l2_env.as_ref().unwrap();
        let txn = l2_env
            .env
            .read_txn()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let mut topics = Vec::new();
        for tid in &topic_ids {
            if let Ok(Some(t)) = l2.get_topic_by_id(&txn, l2_env, tid) {
                topics.push(t);
            }
        }
        Ok(topics)
    }
}

/// Complete a plan: mark it by creating an archival hyperedge.
pub fn complete_plan(brain: &mut Brain, topic_id: &str) -> Result<()> {
    // Step 1: Read topic from L2 (block-scoped to release L2 borrows)
    let node_ids = {
        brain.ensure_l2()?;
        let l2 = brain.l2.as_mut().unwrap();
        let l2_env = brain.l2_env.as_ref().unwrap();
        let txn = l2_env
            .env
            .read_txn()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let topic = match l2.get_topic_by_id(&txn, l2_env, topic_id)? {
            Some(t) => t,
            None => {
                return Err(MemHopError::NotFound(format!(
                    "topic {} not found",
                    topic_id
                )));
            }
        };
        let ids = topic.node_ids.clone();
        drop(txn);
        ids
    };

    // Step 2: Write archiving hyperedge to L1
    brain.ensure_l1()?;
    let l1 = brain.l1.as_mut().unwrap();
    let l1_env = brain.l1_env.as_ref().unwrap();
    let env = l1_env.env.clone();
    let mut wtxn = env
        .write_txn()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;

    if !node_ids.is_empty() {
        l1.add_hyperedge(
            &mut wtxn,
            l1_env,
            node_ids,
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
    // Step 1: Read all topics from L2
    let topics = {
        brain.ensure_l2_env()?;
        let l2_env_ref = brain.l2_env.as_ref().unwrap();
        let env = l2_env_ref.env.clone();
        let txn = env
            .read_txn()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;

        let mut list = Vec::new();
        if let Ok(iter) = l2_env_ref.topics.iter(&txn) {
            for (key, bytes) in iter.flatten() {
                if !key.starts_with("topic:") || !key.ends_with(":meta") {
                    continue;
                }
                if let Ok(t) = bincode::deserialize::<crate::engram::Topic>(bytes) {
                    list.push(t);
                }
            }
        }
        drop(txn);
        list
    };

    if topics.is_empty() {
        return Ok(0);
    }

    // Step 2: Process each topic with L1 and L2
    let mut consolidated = 0u32;

    // First: ensure L2 env and create write txn (block-scoped to release l2_env borrow)
    let wtxn_env: heed::Env;
    let mut wtxn: heed::RwTxn<'_>;
    {
        brain.ensure_l2_env()?;
        let l2_env = brain.l2_env.as_ref().unwrap();
        wtxn_env = l2_env.env.clone();
        wtxn = wtxn_env
            .write_txn()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        // l2_env dropped here — brain.l2_env borrow released
    }

    // Then: ensure L1
    brain.ensure_l1()?;
    let l1 = brain.l1.as_mut().unwrap();
    let l1_env = brain.l1_env.as_ref().unwrap();

    // Re-borrow l2_env (compatible with l1 borrow — different fields)
    let l2_env = brain.l2_env.as_ref().unwrap();

    for topic in &topics {
        if topic.node_ids.is_empty() {
            continue;
        }

        // Aggregate L1 node summaries
        let mut summary_parts: Vec<String> = Vec::new();
        let mut all_keywords: HashMap<String, f32> = HashMap::new();
        let l1_txn = l1_env
            .env
            .read_txn()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;

        for nid in &topic.node_ids {
            if let Ok(Some(node)) = l1.get_node(&l1_txn, l1_env, nid) {
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

        // Write back to L2
        let key = format!("topic:{}:meta", &topic.id);
        if let Some(bytes) = l2_env
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
            l2_env
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
