//! organize/reflect — topic reflection and similarity-based merging.

use crate::brain::Brain;
use crate::error::{MemHopError, Result};

/// Reflect on a topic: aggregate L1 node content to update topic summary.
/// Returns the new summary text.
pub fn reflect_topic(brain: &mut Brain, topic_id: &str) -> Result<String> {
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

    if topic.node_ids.is_empty() {
        return Ok(topic.label.clone());
    }

    // v0.17.0: 如果 topic 已有 LLM 提供的非空 summary，跳过覆写（保护 keywords）
    if let Some(ref existing) = topic.summary
        && !existing.is_empty()
    {
        return Ok(existing.clone());
    }

    // Step 2: Aggregate all L1 node text and ngram weights
    let mut all_text = String::new();
    let mut keyword_freq: std::collections::HashMap<String, f32> =
        std::collections::HashMap::new();

    for nid in &topic.node_ids {
        if let Ok(Some(node)) = store.l1_get_node(nid) {
            all_text.push_str(&node.text);
            all_text.push(' ');
            for (kw, w) in &node.sparse {
                *keyword_freq.entry(kw.clone()).or_insert(0.0) += *w;
            }
        }
    }

    // Top 10 keywords by weight → summary
    let mut keywords: Vec<(String, f32)> = keyword_freq.drain().collect();
    keywords.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
    keywords.truncate(10);
    let kw_names: Vec<String> = keywords.iter().map(|(k, _)| k.clone()).collect();
    let summary = kw_names.join(", ");

    if summary.is_empty() {
        return Ok(topic.label.clone());
    }

    // Step 3: Write summary + keywords back to L2
    if let Ok(Some(mut t)) = store.l2_get_topic(topic_id) {
        t.summary = Some(summary.clone());
        t.keywords = kw_names.clone();
        t.updated_at = chrono::Utc::now().timestamp_millis();
        store.l2_store_topic(&t)?;
    }

    Ok(summary)
}

/// Merge similar topics: compare all topic labels, merge those with high ngram overlap.
/// When similarity >= threshold:
/// - Merge node_ids, doc_ids, linked_domain_ids from weaker topic into stronger
/// - Create TopicEdge::Evolution link
/// - Update summary from combined content
pub fn merge_similar_topics(brain: &mut Brain, threshold: f32) -> Result<u32> {
    let store = brain.redb_store.as_ref()
        .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;

    // Collect all topics
    let topic_list = store.l2_list_topics()?;

    if topic_list.len() < 2 {
        return Ok(0);
    }

    let mut merged = 0u32;
    let mut merged_away: std::collections::HashSet<String> = std::collections::HashSet::new();

    // We need a mutable copy for in-memory merging, then write back
    let mut topic_list = topic_list;

    for i in 0..topic_list.len() {
        if merged_away.contains(&topic_list[i].id) {
            continue;
        }
        for j in (i + 1)..topic_list.len() {
            if merged_away.contains(&topic_list[j].id) {
                continue;
            }
            let a = &topic_list[i];
            let b = &topic_list[j];

            // Ngram overlap on labels
            let label_a = a.label.to_lowercase();
            let label_b = b.label.to_lowercase();
            let shared: usize = label_a
                .chars()
                .collect::<std::collections::HashSet<char>>()
                .intersection(&label_b.chars().collect())
                .count();
            let union = label_a.len() + label_b.len();
            let jaccard = if union > 0 {
                shared as f32 / (union as f32 - shared as f32)
            } else {
                0.0
            };

            if jaccard >= threshold {
                // Determine which is "stronger" (more nodes)
                let (keeper, absorbed) = if a.node_ids.len() >= b.node_ids.len() {
                    (i, j)
                } else {
                    (j, i)
                };
                // Clone absorbed topic data to avoid borrow conflict
                let absorbed_data = topic_list[absorbed].clone();
                let keeper_topic = &mut topic_list[keeper];

                // Merge node_ids
                for nid in &absorbed_data.node_ids {
                    if !keeper_topic.node_ids.contains(nid) {
                        keeper_topic.node_ids.push(nid.clone());
                    }
                }
                // Merge doc_ids
                for did in &absorbed_data.doc_ids {
                    if !keeper_topic.doc_ids.contains(did) {
                        keeper_topic.doc_ids.push(did.clone());
                    }
                }
                // Merge linked_domain_ids
                for did in &absorbed_data.linked_domain_ids {
                    if !keeper_topic.linked_domain_ids.contains(did) {
                        keeper_topic.linked_domain_ids.push(did.clone());
                    }
                }
                // Merge keywords
                for kw in &absorbed_data.keywords {
                    if !keeper_topic.keywords.contains(kw) {
                        keeper_topic.keywords.push(kw.clone());
                    }
                }
                // Merge dialogue_range
                keeper_topic.dialogue_range =
                    merge_ranges(keeper_topic.dialogue_range, absorbed_data.dialogue_range);

                keeper_topic.updated_at = chrono::Utc::now().timestamp_millis();
                keeper_topic.version += 1;

                // Write merged topic back via redb
                store.l2_store_topic(keeper_topic)?;

                // Create Evolution edge from absorbed → keeper
                let edge = crate::engram::TopicEdge {
                    source_id: absorbed_data.id.clone(),
                    target_id: keeper_topic.id.clone(),
                    kind: crate::types::TopicEdgeKind::Evolution,
                    weight: jaccard,
                    created_at: chrono::Utc::now().timestamp_millis(),
                };
                store.l2_store_topic_edge(&edge)?;

                merged_away.insert(absorbed_data.id.clone());
                merged += 1;
            }
        }
    }

    Ok(merged)
}

/// Merge two optional dialogue ranges into the widest range.
fn merge_ranges(a: Option<(i64, i64)>, b: Option<(i64, i64)>) -> Option<(i64, i64)> {
    match (a, b) {
        (Some((a_start, a_end)), Some((b_start, b_end))) => {
            Some((a_start.min(b_start), a_end.max(b_end)))
        }
        (Some(r), None) | (None, Some(r)) => Some(r),
        (None, None) => None,
    }
}

pub fn create_cooccurrence_hyperedges(brain: &mut Brain) -> Result<u32> {
    let store = brain.redb_store.as_ref()
        .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;

    // Step 1: Collect topics from L2 via redb
    let topics = store.l2_list_topics()?;

    if topics.len() < 2 {
        return Ok(0);
    }

    // Step 2: Build session→topic mapping from L4 session_index
    let session_topics = {
        let mut map: std::collections::HashMap<String, Vec<String>> =
            std::collections::HashMap::new();
        for topic in &topics {
            for doc_id in &topic.doc_ids {
                // Look up which session this doc belongs to
                if let Ok(Some(doc)) = store.l4_get_doc(doc_id)
                    && let Some(ref sid) = doc.session_id
                {
                    map
                        .entry(sid.clone())
                        .or_default()
                        .push(topic.id.clone());
                }
            }
        }
        map
    };

    // Step 3: Find topic pairs that co-occur in same sessions
    let pair_count: std::collections::HashMap<(String, String), u32> = {
        let mut pc: std::collections::HashMap<(String, String), u32> =
            std::collections::HashMap::new();
        for tids in session_topics.values() {
            let unique_tids: Vec<&String> = tids
                .iter()
                .collect::<std::collections::HashSet<_>>()
                .into_iter()
                .collect();
            for i in 0..unique_tids.len() {
                for j in (i + 1)..unique_tids.len() {
                    let pair = if unique_tids[i] < unique_tids[j] {
                        (unique_tids[i].clone(), unique_tids[j].clone())
                    } else {
                        (unique_tids[j].clone(), unique_tids[i].clone())
                    };
                    *pc.entry(pair).or_insert(0) += 1;
                }
            }
        }
        pc
    };

    // Step 4: Create L1 hyperedges for pairs that co-occur >= 2 times
    let mut created = 0u32;
    if !pair_count.is_empty() {
        // Pre-build hyperedges with node→hyperedge updates in memory
        struct PendingHe {
            hyperedge: crate::engram::Hyperedge,
            node_ids: Vec<String>,
        }
        let mut pending: Vec<PendingHe> = Vec::new();

        for ((tid_a, tid_b), count) in &pair_count {
            if *count < 2 {
                continue;
            }
            // Find representative nodes from each topic
            let topic_a = topics.iter().find(|t| &t.id == tid_a);
            let topic_b = topics.iter().find(|t| &t.id == tid_b);
            if let (Some(ta), Some(tb)) = (topic_a, topic_b) {
                let mut cross_nodes: Vec<String> = Vec::new();
                if let Some(nid) = ta.node_ids.first() {
                    cross_nodes.push(nid.clone());
                }
                if let Some(nid) = tb.node_ids.first() {
                    cross_nodes.push(nid.clone());
                }
                if cross_nodes.len() == 2 {
                    let weight = (*count as f32).min(5.0) / 5.0;
                    let label = format!("cooccur:{}:{}", tid_a, tid_b);
                    let now = chrono::Utc::now().timestamp_millis();
                    let he = crate::engram::Hyperedge {
                        id: format!("he_cooccur_{}", now),
                        node_ids: cross_nodes.clone(),
                        kind: crate::types::HyperedgeKind::Association,
                        weight,
                        created_at: now,
                        updated_at: now,
                        version: 1,
                        history: Vec::new(),
                        meta: {
                            let mut m = std::collections::HashMap::new();
                            m.insert("label".to_string(), label.clone());
                            m
                        },
                        chain_prev: None,
                        chain_next: None,
                        chain_label: Some(label),
                    };
                    pending.push(PendingHe {
                        node_ids: cross_nodes,
                        hyperedge: he,
                    });
                }
            }
        }

        // Pre-read existing node→hyperedge indices
        let mut node_updates: Vec<(String, Vec<String>)> = Vec::new();
        for pe in &pending {
            for nid in &pe.node_ids {
                let existing = store
                    .l1_get_node_hyperedge_index(nid)?
                    .unwrap_or_default();
                let mut new_ids = existing;
                if !new_ids.contains(&pe.hyperedge.id) {
                    new_ids.push(pe.hyperedge.id.clone());
                }
                node_updates.push((nid.clone(), new_ids));
            }
        }

        // Write all hyperedges + indices in a single write transaction
        if !pending.is_empty() {
            let mut wtxn = store.begin_write()?;
            for pe in &pending {
                store.write_bincode(
                    &mut wtxn,
                    crate::storage::L1_HYPEREDGES,
                    &pe.hyperedge.id,
                    &pe.hyperedge,
                )?;
            }
            for (nid, ids) in &node_updates {
                store.write_bincode(
                    &mut wtxn,
                    crate::storage::L1_NODE_TO_HYPEREDGES,
                    nid,
                    ids,
                )?;
            }
            wtxn.commit()
                .map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;
            created = pending.len() as u32;
        }
    }

    Ok(created)
}
