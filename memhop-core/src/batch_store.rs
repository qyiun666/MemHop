//! batch_store — 批量存储（唯一写入接口）。
//! 一次 RPC 完成：L4 原文 → L1 超图 → L2 话题 → L3 领域。

use crate::brain::Brain;
use crate::error::{MemHopError, Result};
use crate::lmdb::space_usage_impl;
use crate::types::{BatchReport, HyperedgeKind, NodeSource, StoreBatch};
use std::collections::HashMap;
use std::sync::atomic::{AtomicU64, Ordering};

/// 全局单调递增计数器，确保同毫秒内 ID 不碰撞。
static ID_SEQ: AtomicU64 = AtomicU64::new(0);

/// 检查 LMDB 环境空间使用率，超过阈值时返回 StorageFull 错误或打印警告。
fn check_space(env: &heed::Env, layer: &str) -> Result<()> {
    match space_usage_impl(env) {
        Ok(usage) => {
            let pct = usage.usage_pct;
            if pct > 95.0 {
                return Err(MemHopError::StorageFull(format!(
                    "{} storage {:.0}% full",
                    layer, pct
                )));
            } else if pct > 80.0 {
                eprintln!("[memhop] WARNING: {} storage {:.0}% full", layer, pct);
            }
        }
        Err(e) => {
            eprintln!("[memhop] WARNING: {} space_usage check failed: {}", layer, e);
        }
    }
    Ok(())
}

/// 生成唯一 ID：前缀 + 时间戳 + 序号后缀。
fn unique_id(prefix: &str) -> String {
    let ts = chrono::Utc::now().timestamp_millis();
    let seq = ID_SEQ.fetch_add(1, Ordering::Relaxed);
    format!("{}_{}_{}", prefix, ts, seq)
}

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
        chain_parent_id: Option<String>,
        chain_label: Option<String>,
        domain_id: Option<String>,
        turn_id: Option<String>,
        session_id: Option<String>,
        source: String,
        importance: f32,
        /// 原始输入项的索引，用于建立 engram_id 映射
        input_index: usize,
    }

    let mut encoded: Vec<Encoded> = Vec::with_capacity(batch.items.len());
    for (idx, item) in batch.items.iter().enumerate() {
        // 长文本分段：超过 512 字符的文本按段落/句子切分
        let chunks = crate::splitter::split_text(&item.text, 512);
        for chunk in chunks {
            let output = brain.encoder.encode(&chunk);
            encoded.push(Encoded {
                text: chunk,
                sparse: output.sparse,
                vector: output.dense,
                topic_label: item.topic_label.clone(),
                llm_keywords: item.llm_keywords.clone(),
                llm_compressed_summary: item.llm_compressed_summary.clone(),
                chain_parent_id: item.chain_parent_id.clone(),
                chain_label: item.chain_label.clone(),
                domain_id: item.domain_id.clone(),
                turn_id: item.turn_id.clone(),
                session_id: item.session_id.clone(),
                source: item.source.clone(),
                importance: item.importance.unwrap_or(0.5),
                input_index: idx,
            });
        }
    }

    // Phase 1.5: L1 node IDs cache (shared across Phases 3-4)
    let mut node_ids: Vec<String> = Vec::new();
    // 用于追踪每个输入项的第一个 L1 节点 ID
    let mut input_first_node: HashMap<usize, String> = HashMap::new();
    // 用于追踪每个输入项的第一个 L3 节点 ID
    let mut input_first_l3_node: HashMap<usize, String> = HashMap::new();

    // Phase 2: L4 write — 原文纯文本存储
    let mut l4_doc_ids: Vec<String> = Vec::new();
    {
        brain.ensure_l4()?;
        // P1-1: 写入前检查 L4 空间使用率
        check_space(&brain.l4_env.as_ref().unwrap().env, "L4")?;
        let l4 = brain.l4.as_mut().unwrap();
        let l4_env = brain.l4_env.as_ref().unwrap();
        let env = l4_env.env.clone();
        let mut wtxn = env
            .write_txn()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        for item in &encoded {
            let doc_id = l4.store_with_id(
                &mut wtxn,
                l4_env,
                &unique_id("l4d"),
                &item.text,
                &item.source,
                item.turn_id.as_deref(),
                item.session_id.as_deref(),
                item.vector.clone(),
            )?;
            l4_doc_ids.push(doc_id);
            report.l4_docs_stored += 1;
        }
        wtxn.commit()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
    }

    // Phase 3: L1 hypergraph write — 带版本历史 + dedup
    {
        brain.ensure_l1()?;
        // P1-1: 写入前检查 L1 空间使用率
        check_space(&brain.l1_env.as_ref().unwrap().env, "L1")?;
        let l1 = brain.l1.as_mut().unwrap();
        let l1_env = brain.l1_env.as_ref().unwrap();
        let env = l1_env.env.clone();
        let mut wtxn = env
            .write_txn()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;

        for item in &encoded {
            // v0.16.0: Dedup check — if a semantically similar node exists, skip
            let mut dedup_found = false;
            if !item.vector.is_empty() && !l1.vector_index.is_empty() {
                let candidates = l1.vector_index.cosine_search(&item.vector, 5);
                for (cand_id, sim) in &candidates {
                    if *sim > 0.95 {
                        // Check ngram overlap
                        if let Ok(Some(cand_node)) =
                            l1.get_node(&wtxn, l1_env, cand_id)
                        {
                            let intersection = item
                                .sparse
                                .keys()
                                .filter(|k| cand_node.sparse.contains_key(*k))
                                .count() as f32;
                            let union =
                                (item.sparse.len() + cand_node.sparse.len()) as f32 - intersection;
                            let jaccard = if union > 0.0 {
                                intersection / union
                            } else {
                                0.0
                            };
                            if jaccard > 0.8 {
                                // Duplicate found — update existing node instead
                                let mut updated = cand_node;
                                updated.vector = item.vector.clone();
                                updated.updated_at = chrono::Utc::now().timestamp_millis();
                                updated.version += 1;
                                if let Some(ref kw) = item.llm_keywords {
                                    updated.keywords = kw.clone();
                                }
                                let bytes = bincode::serialize(&updated)
                                    .map_err(|e| MemHopError::Storage(e.to_string()))?;
                                l1_env
                                    .nodes
                                    .put(&mut wtxn, cand_id, &bytes)
                                    .map_err(|e| MemHopError::Storage(e.to_string()))?;
                                l1.vector_index.update(cand_id, &item.vector);
                                node_ids.push(cand_id.clone());
                                report.l1_dedup_skipped += 1;
                                dedup_found = true;
                                break;
                            }
                        }
                    }
                }
            }

            if !dedup_found {
                let node_id = l1.add_node_with_id(
                    &mut wtxn,
                    l1_env,
                    &unique_id("kn"),
                    &item.text,
                    &item.sparse,
                    item.vector.clone(),
                    item.llm_keywords.clone().unwrap_or_default(),
                    NodeSource::Perception,
                    item.importance,
                )?;
                node_ids.push(node_id.clone());
                report.l1_nodes_created += 1;
                // 记录输入项到 L1 节点 ID 的映射（只记录每个输入项的第一个分段）
                input_first_node.entry(item.input_index).or_insert(node_id);
            } else if let Some(last_id) = node_ids.last() {
                // 去重情况下，也记录映射
                input_first_node
                    .entry(item.input_index)
                    .or_insert(last_id.clone());
            }
        }

        // 建立节点间超边
        if node_ids.len() > 1 {
            l1.add_hyperedge(
                &mut wtxn,
                l1_env,
                node_ids.clone(),
                HyperedgeKind::Association,
                1.0,
                None,
                None,
            )?;
            report.l1_hyperedges_created += 1;
        }

        // 超边链：更新事件
        for (i, item) in encoded.iter().enumerate() {
            if let Some(ref parent_id) = item.chain_parent_id {
                l1.add_hyperedge(
                    &mut wtxn,
                    l1_env,
                    vec![node_ids[i].clone()],
                    HyperedgeKind::Evolution,
                    1.0,
                    Some(parent_id.clone()),
                    item.chain_label.clone(),
                )?;
                report.chains_created += 1;
            }
        }

        wtxn.commit()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
    }

    // Phase 3.5: 将 llm_compressed_summary 写入 L1 node.summary
    {
        let l1_env = brain.l1_env.as_ref().unwrap();
        let env = l1_env.env.clone();
        let mut wtxn = env
            .write_txn()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        for (i, item) in encoded.iter().enumerate() {
            if let Some(ref summary) = item.llm_compressed_summary
                && i < node_ids.len()
                && let Ok(Some(bytes)) = l1_env.nodes.get(&wtxn, &node_ids[i])
                && let Ok(mut node) = bincode::deserialize::<crate::engram::KnowledgeNode>(bytes)
            {
                node.summary = Some(summary.clone());
                node.updated_at = chrono::Utc::now().timestamp_millis();
                if let Ok(new_bytes) = bincode::serialize(&node) {
                    l1_env
                        .nodes
                        .put(&mut wtxn, &node_ids[i], &new_bytes)
                        .map_err(|e| MemHopError::Storage(e.to_string()))?;
                }
            }
        }
        wtxn.commit()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
    }

    // Phase 4: L2 topic update — 带 llm_compressed_summary + 真实 node_id + doc_ids + linked_domain_ids + centroid
    {
        brain.ensure_l2()?;
        // P1-1: 写入前检查 L2 空间使用率
        check_space(&brain.l2_env.as_ref().unwrap().env, "L2")?;
        let l2 = brain.l2.as_mut().unwrap();
        let l2_env = brain.l2_env.as_ref().unwrap();
        let env = l2_env.env.clone();
        let mut wtxn = env
            .write_txn()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let mut topic_cache: HashMap<String, crate::engram::Topic> = HashMap::new();
        // 收集每个 topic 的新 node 向量（用于 centroid 更新）
        let mut topic_new_nodes: HashMap<String, Vec<(String, Vec<half::f16>)>> = HashMap::new();

        for (i, item) in encoded.iter().enumerate() {
            if let Some(ref label) = item.topic_label {
                let (topic_id, is_new) =
                    l2
                        .find_or_create_topic(&mut wtxn, l2_env, label)?;
                if is_new {
                    report.l2_topics_created += 1;
                }

                // 获取 topic（优先从缓存）
                let topic = if let Some(cached) = topic_cache.get(&topic_id) {
                    cached.clone()
                } else if let Ok(Some(t)) =
                    l2.get_topic_by_id(&wtxn, l2_env, &topic_id)
                {
                    t
                } else {
                    continue;
                };
                let mut topic = topic;
                let mut topic_changed = false;

                // 写入 llm_compressed_summary
                if let Some(ref summary) = item.llm_compressed_summary {
                    topic.summary = Some(summary.clone());
                    topic_changed = true;
                }

                // 填充 doc_ids (L4 文档 ID)
                if i < l4_doc_ids.len()
                    && !l4_doc_ids[i].is_empty()
                    && !topic.doc_ids.contains(&l4_doc_ids[i])
                {
                    topic.doc_ids.push(l4_doc_ids[i].clone());
                    topic_changed = true;
                }

                // 填充 linked_domain_ids (L3 领域 ID)
                if let Some(ref domain_id) = item.domain_id
                    && !topic.linked_domain_ids.contains(domain_id)
                {
                    topic.linked_domain_ids.push(domain_id.clone());
                    topic_changed = true;
                }

                if topic_changed {
                    // v0.18.0: 计算关联强度
                    // 1. 领域关联强度：基于该话题下有多少节点属于该领域
                    if let Some(ref domain_id) = item.domain_id {
                        let current_weight =
                            topic.domain_weights.get(domain_id).copied().unwrap_or(0.0);
                        topic
                            .domain_weights
                            .insert(domain_id.clone(), current_weight + 1.0);
                    }
                    // 2. 节点关联强度：基于节点与话题的相关性（这里简化为1.0）
                    if i < node_ids.len() {
                        let node_id = &node_ids[i];
                        let current_weight =
                            topic.node_weights.get(node_id).copied().unwrap_or(0.0);
                        topic
                            .node_weights
                            .insert(node_id.clone(), current_weight + 1.0);
                    }

                    // 自动维护 dialogue_range
                    let now_ms = chrono::Utc::now().timestamp_millis();
                    match topic.dialogue_range {
                        Some((start, end)) => {
                            if now_ms < start {
                                topic.dialogue_range = Some((now_ms, end));
                            }
                            if now_ms > end {
                                topic.dialogue_range = Some((start, now_ms));
                            }
                        }
                        None => {
                            topic.dialogue_range = Some((now_ms, now_ms));
                        }
                    }
                    topic.updated_at = now_ms;
                    let key = format!("topic:{}:meta", &topic_id);
                    let bytes = bincode::serialize(&topic)
                        .map_err(|e| MemHopError::Storage(e.to_string()))?;
                    l2_env
                        .topics
                        .put(&mut wtxn, &key, &bytes)
                        .map_err(|e| MemHopError::Storage(e.to_string()))?;
                    topic_cache.insert(topic_id.clone(), topic);
                }

                // 传入真实 node_id（来自 Phase 3）
                if let Some(ref keywords) = item.llm_keywords {
                    let mut kw_sparse = HashMap::new();
                    for kw in keywords {
                        kw_sparse.insert(kw.clone(), 1.0f32);
                    }
                    let nid = if i < node_ids.len() { &node_ids[i] } else { "" };
                    l2.add_node_to_topic(
                        &mut wtxn,
                        l2_env,
                        &topic_id,
                        nid,
                        &kw_sparse,
                    )?;
                }

                // 收集 node 向量用于 centroid 计算
                if i < node_ids.len() && !item.vector.is_empty() {
                    topic_new_nodes
                        .entry(topic_id.clone())
                        .or_default()
                        .push((node_ids[i].clone(), item.vector.clone()));
                }
            }
        }

        // 更新各 topic 的 centroid 向量
        for (tid, nodes) in &topic_new_nodes {
            if let Err(e) = l2
                .update_topic_centroid(&mut wtxn, l2_env, tid, nodes)
            {
                eprintln!("[batch_store] centroid update error for {}: {}", tid, e);
            }
        }
        // 持久化 topic 向量索引
        if !topic_new_nodes.is_empty()
            && let Err(e) = l2.persist_topic_vectors(&mut wtxn, l2_env)
        {
            eprintln!("[batch_store] persist topic vectors error: {}", e);
        }

        wtxn.commit()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
    }

    // Phase 5: L3 domain update — 自动生成 L3 领域
    {
        brain.ensure_l3()?;
        // P1-1: 写入前检查 L3 空间使用率
        check_space(&brain.l3_env.as_ref().unwrap().env, "L3")?;
        let l3 = brain.l3.as_mut().unwrap();
        let l3_env = brain.l3_env.as_ref().unwrap();
        let env = l3_env.env.clone();
        let mut wtxn = env
            .write_txn()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;

        // 缓存已创建的 domain_id，避免重复创建
        let mut domain_cache: std::collections::HashSet<String> = std::collections::HashSet::new();

        for item in &encoded {
            // 确定 domain_id：优先使用用户指定的，否则根据 topic_label 自动生成
            let domain_id = if let Some(ref did) = item.domain_id {
                did.clone()
            } else if let Some(ref label) = item.topic_label {
                // 根据 topic_label 生成 domain_id
                format!("domain_{}", label.chars().take(32).collect::<String>())
            } else {
                // 无 topic_label 时归入默认领域
                "domain_default".to_string()
            };

            // 确保领域存在（如果不存在则创建）
            if !domain_cache.contains(&domain_id) {
                let meta_key = format!("meta:{}", domain_id);
                if l3_env
                    .domain_meta
                    .get(&wtxn, &meta_key)
                    .map_err(|e| MemHopError::Storage(e.to_string()))?
                    .is_none()
                {
                    // 领域不存在，创建新领域
                    let name = if domain_id == "domain_default" {
                        "默认领域".to_string()
                    } else {
                        domain_id
                            .strip_prefix("domain_")
                            .unwrap_or(&domain_id)
                            .to_string()
                    };
                    l3.mount_domain(&mut wtxn, l3_env, &name)?;
                }
                domain_cache.insert(domain_id.clone());
            }

            // 添加节点到 L3
            let l3_id = unique_id("l3n");
            l3.add_node_with_id(
                &mut wtxn,
                l3_env,
                &l3_id,
                &domain_id,
                &item.text,
                &item.sparse,
                "",
                item.vector.clone(),
            )?;
            report.l3_nodes_created += 1;
            // v0.17.3: 记录输入项到 L3 节点 ID 的映射
            input_first_l3_node
                .entry(item.input_index)
                .or_insert(l3_id.clone());
            #[cfg(debug_assertions)]
            eprintln!(
                "[batch_store] L3 node created: l3_id={}, input_index={}",
                l3_id, item.input_index
            );
        }
        wtxn.commit()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
    }

    report.total_duration_us = start.elapsed().as_micros() as u64;

    // 将输入索引映射转换为字符串键值对
    for (idx, node_id) in input_first_node {
        report.engram_ids.insert(idx.to_string(), node_id);
    }
    // v0.17.3: 将输入索引映射转换为 L3 节点 ID
    for (idx, node_id) in input_first_l3_node {
        report.l3_engram_ids.insert(idx.to_string(), node_id);
    }

    // Debug: 打印映射信息 (仅 debug build)
    #[cfg(debug_assertions)]
    eprintln!(
        "[batch_store] engram_ids: {:?}, l3_engram_ids: {:?}",
        report.engram_ids, report.l3_engram_ids
    );

    Ok(report)
}
