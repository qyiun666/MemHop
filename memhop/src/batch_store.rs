//! batch_store — 批量存储（唯一写入接口）。
//! 一次 RPC 完成：L4 原文 → L1 超图 → L2 话题 → L3 领域。

use std::collections::HashMap;
use crate::error::{Result, MemHopError};
use crate::types::{StoreBatch, BatchReport, NodeSource, HyperedgeKind};
use crate::brain::Brain;
use crate::encoder::Encoder;

pub(crate) fn execute(brain: &mut Brain, batch: StoreBatch) -> Result<BatchReport> {
    let start = std::time::Instant::now();
    if batch.items.is_empty() {
        return Ok(BatchReport::default());
    }

    let mut report = BatchReport::default();

    // Phase 1: Encode all items
    struct Encoded {
        text: String,
        sparse: HashMap<String, f32>,
        vector: Vec<half::f16>,
        topic_label: Option<String>,
        llm_keywords: Option<Vec<String>>,
        llm_compressed_summary: Option<String>,
        #[allow(dead_code)]
        valence: Option<f64>,
        #[allow(dead_code)]
        arousal: Option<f64>,
        chain_parent_id: Option<String>,
        chain_label: Option<String>,
        domain_id: Option<String>,
        turn_id: Option<String>,
        session_id: Option<String>,
        source: String,
    }

    let mut encoded: Vec<Encoded> = Vec::with_capacity(batch.items.len());
    for item in &batch.items {
        let output = brain.encoder.encode(&item.text);
        encoded.push(Encoded {
            text: item.text.clone(),
            sparse: output.sparse,
            vector: output.dense,
            topic_label: item.topic_label.clone(),
            llm_keywords: item.llm_keywords.clone(),
            llm_compressed_summary: item.llm_compressed_summary.clone(),
            valence: item.valence,
            arousal: item.arousal,
            chain_parent_id: item.chain_parent_id.clone(),
            chain_label: item.chain_label.clone(),
            domain_id: item.domain_id.clone(),
            turn_id: item.turn_id.clone(),
            session_id: item.session_id.clone(),
            source: item.source.clone(),
        });
    }

    // Phase 1.5: L1 node IDs cache (shared across Phases 3-4)
    let mut node_ids: Vec<String> = Vec::new();

    // Phase 2: L4 write — 原文纯文本存储
    {
        let env = brain.l4_env.env.clone();
        let mut wtxn = env.write_txn().map_err(|e| MemHopError::Storage(e.to_string()))?;
        for item in &encoded {
            brain.l4.store(&mut wtxn, &brain.l4_env, &item.text, &item.source,
                item.turn_id.as_deref(), item.session_id.as_deref())?;
            report.l4_docs_stored += 1;
        }
        wtxn.commit().map_err(|e| MemHopError::Storage(e.to_string()))?;
    }

    // Phase 3: L1 hypergraph write — 带版本历史
    {
        let env = brain.l1_env.env.clone();
        let mut wtxn = env.write_txn().map_err(|e| MemHopError::Storage(e.to_string()))?;

        for item in &encoded {
            let node_id = brain.l1.add_node(&mut wtxn, &brain.l1_env,
                &item.text, &item.sparse, item.vector.clone(),
                item.llm_keywords.clone().unwrap_or_default(),
                NodeSource::Perception)?;
            node_ids.push(node_id);
            report.l1_nodes_created += 1;
        }

        // 建立节点间超边
        if node_ids.len() > 1 {
            brain.l1.add_hyperedge(&mut wtxn, &brain.l1_env, node_ids.clone(),
                HyperedgeKind::Association, 1.0, None, None)?;
            report.l1_hyperedges_created += 1;
        }

        // 超边链：更新事件
        for (i, item) in encoded.iter().enumerate() {
            if let Some(ref parent_id) = item.chain_parent_id {
                brain.l1.add_hyperedge(&mut wtxn, &brain.l1_env, vec![node_ids[i].clone()],
                    HyperedgeKind::Evolution, 1.0,
                    Some(parent_id.clone()), item.chain_label.clone())?;
                report.chains_created += 1;
            }
        }

        wtxn.commit().map_err(|e| MemHopError::Storage(e.to_string()))?;
    }

    // Phase 4: L2 topic update — 带 llm_compressed_summary + 真实 node_id
    {
        let env = brain.l2_env.env.clone();
        let mut wtxn = env.write_txn().map_err(|e| MemHopError::Storage(e.to_string()))?;
        let mut topic_cache: HashMap<String, crate::engram::Topic> = HashMap::new();
        for (i, item) in encoded.iter().enumerate() {
            if let Some(ref label) = item.topic_label {
                let (topic_id, is_new) = brain.l2.find_or_create_topic(&mut wtxn, &brain.l2_env, label)?;
                if is_new { report.l2_topics_created += 1; }

                // 写入 llm_compressed_summary（用缓存避免同 topic 竞态）
                if let Some(ref summary) = item.llm_compressed_summary {
                    let topic = if let Some(cached) = topic_cache.get(&topic_id) {
                        cached.clone()
                    } else if let Ok(Some(t)) = brain.l2.get_topic_by_id(&wtxn, &brain.l2_env, &topic_id) {
                        t
                    } else { continue; };
                    let mut topic = topic;
                    topic.summary = Some(summary.clone());
                    topic.updated_at = chrono::Utc::now().timestamp_millis();
                    let key = format!("topic:{}:meta", &topic_id);
                    let bytes = bincode::serialize(&topic).map_err(|e| MemHopError::Storage(e.to_string()))?;
                    brain.l2_env.topics.put(&mut wtxn, &key, &bytes).map_err(|e| MemHopError::Storage(e.to_string()))?;
                    topic_cache.insert(topic_id.clone(), topic);
                }

                // 传入真实 node_id（来自 Phase 3）
                if let Some(ref keywords) = item.llm_keywords {
                    let mut kw_sparse = HashMap::new();
                    for kw in keywords { kw_sparse.insert(kw.clone(), 1.0f32); }
                    let nid = if i < node_ids.len() { &node_ids[i] } else { "" };
                    brain.l2.add_node_to_topic(&mut wtxn, &brain.l2_env, &topic_id, nid, &kw_sparse)?;
                }
            }
        }
        wtxn.commit().map_err(|e| MemHopError::Storage(e.to_string()))?;
    }

    // Phase 5: L3 domain update
    {
        let env = brain.l3_env.env.clone();
        let mut wtxn = env.write_txn().map_err(|e| MemHopError::Storage(e.to_string()))?;
        for item in &encoded {
            if let Some(ref domain_id) = item.domain_id {
                brain.l3.add_node(&mut wtxn, &brain.l3_env, domain_id, &item.text, &item.sparse, "")?;
                report.l3_nodes_created += 1;
            }
        }
        wtxn.commit().map_err(|e| MemHopError::Storage(e.to_string()))?;
    }

    report.total_duration_us = start.elapsed().as_micros() as u64;
    Ok(report)
}
