//! organize/plan — plan management mapped to L2 Topic operations.
//! Plan = L2 Topic in the v0.15.0 architecture.

use crate::brain::Brain;
use crate::error::{MemHopError, Result};
use std::collections::HashMap;

/// Set/update a plan name (maps to Topic label).
pub fn set_plan_name(brain: &mut Brain, topic_id: &str, name: &str) -> Result<()> {
    let store = brain.redb_store.as_ref()
        .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;

    let mut topic = match store.l2_get_topic(topic_id)? {
        Some(t) => t,
        None => {
            return Err(MemHopError::NotFound(format!(
                "topic {} not found",
                topic_id
            )));
        }
    };

    topic.label = name.to_string();
    topic.updated_at = chrono::Utc::now().timestamp_millis();
    store.l2_store_topic(&topic)?;
    Ok(())
}

/// Get plan tree: return all topics for a given session.
pub fn get_plan_tree(brain: &mut Brain, session_id: &str) -> Result<Vec<crate::engram::Topic>> {
    let store = brain.redb_store.as_ref()
        .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;

    // Get doc_ids for this session from L4 session_index
    let txn = store.begin_read()?;
    let session_key = format!("session:{}", session_id);
    let session_doc_ids: Vec<String> = store
        .read_bincode(&txn, crate::storage::L4_SESSION_INDEX, &session_key)?
        .unwrap_or_default();
    drop(txn);

    if session_doc_ids.is_empty() {
        return Ok(Vec::new());
    }

    let session_doc_set: std::collections::HashSet<String> =
        session_doc_ids.into_iter().collect();

    // List all topics and filter by doc_id overlap with session
    let all_topics = store.l2_list_topics()?;
    let mut plan_topics = Vec::new();
    for topic in all_topics {
        if topic.doc_ids.iter().any(|did| session_doc_set.contains(did)) {
            plan_topics.push(topic);
        }
    }

    Ok(plan_topics)
}

/// Complete a plan: mark it by creating an archival hyperedge.
pub fn complete_plan(brain: &mut Brain, topic_id: &str) -> Result<()> {
    let store = brain.redb_store.as_ref()
        .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;

    let topic = match store.l2_get_topic(topic_id)? {
        Some(t) => t,
        None => {
            return Err(MemHopError::NotFound(format!(
                "topic {} not found",
                topic_id
            )));
        }
    };

    if !topic.node_ids.is_empty() {
        let now = chrono::Utc::now().timestamp_millis();
        let he = crate::engram::Hyperedge {
            id: format!("he_plan_complete_{}", now),
            node_ids: topic.node_ids.clone(),
            kind: crate::types::HyperedgeKind::Merged,
            weight: 1.0,
            created_at: now,
            updated_at: now,
            version: 1,
            history: Vec::new(),
            meta: HashMap::new(),
            chain_prev: None,
            chain_next: None,
            chain_label: Some("plan_complete".to_string()),
        };

        // Pre-read existing node→hyperedge indices (using separate read txns)
        let mut node_existing: Vec<(String, Vec<String>)> = Vec::new();
        for nid in &he.node_ids {
            let existing = store
                .l1_get_node_hyperedge_index(nid)?
                .unwrap_or_default();
            let mut new_ids = existing;
            if !new_ids.contains(&he.id) {
                new_ids.push(he.id.clone());
            }
            node_existing.push((nid.clone(), new_ids));
        }

        // Write hyperedge + node indices in a single write transaction
        let mut wtxn = store.begin_write()?;
        store.write_bincode(&mut wtxn, crate::storage::L1_HYPEREDGES, &he.id, &he)?;
        for (nid, ids) in &node_existing {
            store.write_bincode(
                &mut wtxn,
                crate::storage::L1_NODE_TO_HYPEREDGES,
                nid,
                ids,
            )?;
        }
        wtxn.commit()
            .map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;
    }

    Ok(())
}

/// Consolidate plan summaries: for each L2 topic, aggregate all member node
/// summaries into a single compressed summary, and consolidate L4 doc_ids.
/// Returns the number of topics consolidated.
pub fn consolidate_plan_summaries(brain: &mut Brain) -> Result<u32> {
    let store = brain.redb_store.as_ref()
        .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;

    let topics = store.l2_list_topics()?;

    if topics.is_empty() {
        return Ok(0);
    }

    let mut consolidated = 0u32;

    for topic in &topics {
        if topic.node_ids.is_empty() {
            continue;
        }

        // Aggregate L1 node summaries
        let mut summary_parts: Vec<String> = Vec::new();
        let mut all_keywords: HashMap<String, f32> = HashMap::new();

        for nid in &topic.node_ids {
            if let Ok(Some(node)) = store.l1_get_node(nid) {
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

        // Read-modify-write the topic
        if let Ok(Some(mut t)) = store.l2_get_topic(&topic.id) {
            t.summary = Some(consolidated_summary);
            t.keywords = kw_str;
            t.updated_at = chrono::Utc::now().timestamp_millis();
            store.l2_store_topic(&t)?;
            consolidated += 1;
        }
    }

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
