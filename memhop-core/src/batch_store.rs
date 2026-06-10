//! batch_store — 外部输入写入接口。
//! 一次 RPC 完成：L4 原文 → L1 超图 → L2 话题 → L3 领域。
//! 职责：用户/Agent 主动写入的数据，含编码/去重/建边/建索引。
//!
//! 内部维护写入（Dream NREM/REM/Reconsolidation）直接操作 redb table，
//! 不经过 batch_store。两者写入路径分离：batch_store = 外部摄入，
//! Dream = 内部巩固。
//!
//! v0.25.0: 使用 redb 单文件存储引擎，单事务原子写入。

use crate::brain::Brain;
use crate::engram::{Hyperedge, KnowledgeNode, RawDocument, Topic};
use crate::error::{MemHopError, Result};
use crate::storage::*;
use crate::types::{BatchReport, HyperedgeKind, NodeSource, StoreBatch};
use redb::ReadableTable;
use std::collections::HashMap;
use std::sync::atomic::{AtomicU64, Ordering};

/// 全局单调递增计数器，确保同毫秒内 ID 不碰撞。
static ID_SEQ: AtomicU64 = AtomicU64::new(0);

/// 生成唯一 ID：前缀 + 时间戳 + 序号后缀。
pub(crate) fn unique_id(prefix: &str) -> String {
    let ts = chrono::Utc::now().timestamp_millis();
    let seq = ID_SEQ.fetch_add(1, Ordering::Relaxed);
    format!("{}_{}_{}", prefix, ts, seq)
}

/// v0.24.0: 从 valence/arousal 推断 Ekman 情感分类。
pub(crate) fn infer_emotion(valence: f64, arousal: f64) -> crate::types::Emotion {
    use crate::types::Emotion;
    if valence > 0.3 && arousal > 0.5 {
        Emotion::Joy
    } else if valence < -0.3 && arousal < 0.3 {
        Emotion::Sadness
    } else if valence < -0.3 && arousal > 0.5 {
        Emotion::Anger
    } else if valence < -0.2 && arousal > 0.7 {
        Emotion::Fear
    } else if arousal > 0.8 {
        Emotion::Surprise
    } else if valence < -0.4 {
        Emotion::Disgust
    } else {
        Emotion::Neutral
    }
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
        e5_vector: Vec<half::f16>,
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
        valence: Option<f64>,
        arousal: Option<f64>,
        /// 原始输入项的索引，用于建立 engram_id 映射
        input_index: usize,
    }

    let mut encoded: Vec<Encoded> = Vec::with_capacity(batch.items.len());
    for (idx, item) in batch.items.iter().enumerate() {
        // 长文本分段：超过 512 字符的文本按段落/句子切分
        let chunks = crate::splitter::split_text(&item.text, 512);
        for chunk in chunks {
            let output = brain.encoder.encode(&chunk);
            // 新增：如果有 E5 编码器，编码 E5 向量
            let e5_vector = if let Some(ref e5_encoder) = brain.encoder_e5 {
                let e5_output = e5_encoder.encode(&chunk);
                e5_output.dense
            } else {
                Vec::new()
            };
            encoded.push(Encoded {
                text: chunk,
                sparse: output.sparse,
                vector: output.dense,
                e5_vector,
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
                valence: item.valence,
                arousal: item.arousal,
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

    // 获取 redb store 并开始单写事务
    let store = brain.redb_store.as_ref()
        .ok_or_else(|| MemHopError::Storage("redb not available for batch_store".into()))?;

    let wtxn = store.begin_write()
        .map_err(|e| MemHopError::Storage(format!("begin write: {}", e)))?;

    // Phase 2: L4 write — 原文纯文本存储
    let mut l4_doc_ids: Vec<String> = Vec::new();
    {
        let mut docs_table = wtxn.open_table(L4_DOCS)
            .map_err(|e| MemHopError::Storage(format!("open L4_DOCS: {}", e)))?;
        let mut turn_table = wtxn.open_table(L4_TURN_INDEX)
            .map_err(|e| MemHopError::Storage(format!("open L4_TURN_INDEX: {}", e)))?;
        let mut session_table = wtxn.open_table(L4_SESSION_INDEX)
            .map_err(|e| MemHopError::Storage(format!("open L4_SESSION_INDEX: {}", e)))?;

        for item in &encoded {
            let doc_id = unique_id("l4d");
            let doc = RawDocument {
                id: doc_id.clone(),
                text: item.text.clone(),
                turn_id: item.turn_id.clone(),
                session_id: item.session_id.clone(),
                source: item.source.clone(),
                created_at: chrono::Utc::now().timestamp_millis(),
                version: 1,
                history: Vec::new(),
                vector: item.vector.clone(),
            };
            let doc_key = format!("doc:{}", doc_id);
            let bytes = bincode::serialize(&doc)
                .map_err(|e| MemHopError::Internal(format!("serialize doc: {}", e)))?;
            docs_table.insert(doc_key.as_str(), bytes.as_slice())
                .map_err(|e| MemHopError::Storage(format!("insert doc: {}", e)))?;

            // Turn index
            if let Some(ref turn_id) = item.turn_id {
                let turn_key = format!("turn:{}", turn_id);
                let existing: Vec<String> = match turn_table.get(turn_key.as_str())
                    .map_err(|e| MemHopError::Storage(format!("get turn: {}", e)))?
                {
                    Some(bytes) => bincode::deserialize(bytes.value()).unwrap_or_default(),
                    None => Vec::new(),
                };
                let mut updated = existing;
                updated.push(doc_id.clone());
                let bytes = bincode::serialize(&updated)
                    .map_err(|e| MemHopError::Internal(format!("serialize turn index: {}", e)))?;
                turn_table.insert(turn_key.as_str(), bytes.as_slice())
                    .map_err(|e| MemHopError::Storage(format!("insert turn index: {}", e)))?;
            }

            // Session index
            if let Some(ref session_id) = item.session_id {
                let session_key = format!("session:{}", session_id);
                let existing: Vec<String> = match session_table.get(session_key.as_str())
                    .map_err(|e| MemHopError::Storage(format!("get session: {}", e)))?
                {
                    Some(bytes) => bincode::deserialize(bytes.value()).unwrap_or_default(),
                    None => Vec::new(),
                };
                let mut updated = existing;
                updated.push(doc_id.clone());
                let bytes = bincode::serialize(&updated)
                    .map_err(|e| MemHopError::Internal(format!("serialize session index: {}", e)))?;
                session_table.insert(session_key.as_str(), bytes.as_slice())
                    .map_err(|e| MemHopError::Storage(format!("insert session index: {}", e)))?;
            }

            l4_doc_ids.push(doc_id);
            report.l4_docs_stored += 1;
        }
    }

    // Phase 3: L1 hypergraph write — 带版本历史 + dedup
    // Phase 3.5 (summary) merged into Phase 3
    {
        let mut nodes_table = wtxn.open_table(L1_NODES)
            .map_err(|e| MemHopError::Storage(format!("open L1_NODES: {}", e)))?;
        let mut hyperedges_table = wtxn.open_table(L1_HYPEREDGES)
            .map_err(|e| MemHopError::Storage(format!("open L1_HYPEREDGES: {}", e)))?;
        let mut node_to_he_table = wtxn.open_table(L1_NODE_TO_HYPEREDGES)
            .map_err(|e| MemHopError::Storage(format!("open L1_NODE_TO_HYPEREDGES: {}", e)))?;
        // BM25 稀疏前向索引表和 doc_len 表（通过主事务直接写入）
        let mut sparse_forward_table = wtxn.open_table(L1_SPARSE_FORWARD)
            .map_err(|e| MemHopError::Storage(format!("open L1_SPARSE_FORWARD: {}", e)))?;
        let mut sparse_doc_len_table = wtxn.open_table(L1_SPARSE_DOC_LEN)
            .map_err(|e| MemHopError::Storage(format!("open L1_SPARSE_DOC_LEN: {}", e)))?;

        for item in &encoded {
            // v0.16.0: Dedup check — if a semantically similar node exists, skip
            let mut dedup_found = false;
            if let Some(ref l1) = brain.l1
                && !item.vector.is_empty() && !l1.vector_index.is_empty() {
                    let candidates = l1.vector_index.cosine_search(&item.vector, 5);
                    for (cand_id, sim) in &candidates {
                        if *sim > 0.95 {
                            // Check ngram overlap — read node from redb
                            let cand_node_opt: Option<KnowledgeNode> = match nodes_table.get(cand_id.as_str()) {
                                Ok(Some(bytes)) => bincode::deserialize(bytes.value()).ok(),
                                _ => None,
                            };
                            if let Some(cand_node) = cand_node_opt {
                                let intersection = item.sparse.keys()
                                    .filter(|k| cand_node.sparse.contains_key(*k))
                                    .count() as f32;
                                let union = (item.sparse.len() + cand_node.sparse.len()) as f32 - intersection;
                                let jaccard = if union > 0.0 { intersection / union } else { 0.0 };
                                if jaccard > 0.8 {
                                    // Duplicate found — update existing node
                                    let mut updated = cand_node;
                                    updated.vector = item.vector.clone();
                                    updated.vector_e5 = item.e5_vector.clone();
                                    updated.updated_at = chrono::Utc::now().timestamp_millis();
                                    updated.version += 1;
                                    if let Some(ref kw) = item.llm_keywords {
                                        updated.keywords = kw.clone();
                                    }
                                    let bytes = bincode::serialize(&updated)
                                        .map_err(|e| MemHopError::Internal(format!("serialize updated node: {}", e)))?;
                                    nodes_table.insert(cand_id.as_str(), bytes.as_slice())
                                        .map_err(|e| MemHopError::Storage(format!("insert updated node: {}", e)))?;
                                    if !item.vector.is_empty()
                                        && let Some(ref mut l1) = brain.l1 {
                                            l1.vector_index.update(cand_id, &item.vector);
                                        }
                                    // 更新 E5 向量
                                    if !item.e5_vector.is_empty()
                                        && item.e5_vector.len() > 1
                                        && let Some(ref mut l1) = brain.l1 {
                                            l1.vector_index_e5.update(cand_id, &item.e5_vector);
                                        }
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
                let node_id = unique_id("kn");
                let mut node = KnowledgeNode::new(
                    node_id.clone(),
                    item.text.clone(),
                    item.sparse.clone(),
                    item.vector.clone(),
                    crate::types::Layer::L1,
                    NodeSource::Perception,
                );
                node.memory.importance = item.importance;
                // v0.24.0: 情感字段
                node.memory.valence = item.valence.unwrap_or(0.0) as f32;
                node.memory.arousal = item.arousal.unwrap_or(0.0) as f32;
                node.memory.emotion = infer_emotion(node.memory.valence as f64, node.memory.arousal as f64);
                node.memory.emotion_intensity = (node.memory.valence.abs() + node.memory.arousal) / 2.0;
                // Phase 3.5 merged: set summary at creation time
                if let Some(ref summary) = item.llm_compressed_summary {
                    node.summary = Some(summary.clone());
                }

                // v1.0: set E5 vector
                node.vector_e5 = item.e5_vector.clone();

                // 序列化并写入 L1_NODES 表
                let bytes = bincode::serialize(&node)
                    .map_err(|e| MemHopError::Internal(format!("serialize node: {}", e)))?;
                nodes_table.insert(node_id.as_str(), bytes.as_slice())
                    .map_err(|e| MemHopError::Storage(format!("insert node: {}", e)))?;

                // 写入 BM25 稀疏前向索引（通过主事务直接写入，避免嵌套事务冲突）
                let sparse_bytes = bincode::serialize(&item.sparse)
                    .map_err(|e| MemHopError::Internal(format!("serialize sparse: {}", e)))?;
                sparse_forward_table.insert(node_id.as_str(), sparse_bytes.as_slice())
                    .map_err(|e| MemHopError::Storage(format!("insert sparse forward: {}", e)))?;
                sparse_doc_len_table.insert(node_id.as_str(), item.text.len() as u32)
                    .map_err(|e| MemHopError::Storage(format!("insert doc_len: {}", e)))?;

                // 更新 in-memory 向量索引
                if let Some(ref mut l1) = brain.l1
                    && !item.vector.is_empty() {
                        l1.vector_index.add(&node_id, &item.vector);
                    }

                // 更新 E5 向量索引
                if let Some(ref mut l1) = brain.l1
                    && !item.e5_vector.is_empty()
                    && item.e5_vector.len() > 1 {
                        l1.vector_index_e5.add(&node_id, &item.e5_vector);
                    }

                // 更新 emotion_index
                brain.emotion_index
                    .entry(node.memory.emotion)
                    .or_default()
                    .push(node_id.clone());

                node_ids.push(node_id.clone());
                report.l1_nodes_created += 1;
                input_first_node.entry(item.input_index).or_insert(node_id);
            } else if let Some(last_id) = node_ids.last() {
                input_first_node.entry(item.input_index).or_insert(last_id.clone());
            }
        }

        // 建立节点间超边
        if node_ids.len() > 1 {
            let now = chrono::Utc::now().timestamp_millis();
            let he_id = format!("he_{}", now);
            let he = Hyperedge {
                id: he_id.clone(),
                node_ids: node_ids.clone(),
                kind: HyperedgeKind::Association,
                weight: 1.0,
                created_at: now,
                updated_at: now,
                version: 1,
                history: Vec::new(),
                meta: HashMap::new(),
                chain_prev: None,
                chain_next: None,
                chain_label: None,
            };
            let bytes = bincode::serialize(&he)
                .map_err(|e| MemHopError::Internal(format!("serialize hyperedge: {}", e)))?;
            hyperedges_table.insert(he_id.as_str(), bytes.as_slice())
                .map_err(|e| MemHopError::Storage(format!("insert hyperedge: {}", e)))?;

            for nid in &node_ids {
                let existing: Vec<String> = match node_to_he_table.get(nid.as_str())
                    .map_err(|e| MemHopError::Storage(format!("get node_to_he: {}", e)))?
                {
                    Some(b) => bincode::deserialize(b.value()).unwrap_or_default(),
                    None => Vec::new(),
                };
                let mut ids = existing;
                ids.push(he_id.clone());
                let bytes = bincode::serialize(&ids)
                    .map_err(|e| MemHopError::Internal(format!("serialize node_to_he: {}", e)))?;
                node_to_he_table.insert(nid.as_str(), bytes.as_slice())
                    .map_err(|e| MemHopError::Storage(format!("insert node_to_he: {}", e)))?;
            }
            report.l1_hyperedges_created += 1;
        }

        // 超边链：更新事件
        for (i, item) in encoded.iter().enumerate() {
            if let Some(ref parent_id) = item.chain_parent_id {
                let now = chrono::Utc::now().timestamp_millis();
                let he_id = format!("he_{}_{}", now, i);
                let he = Hyperedge {
                    id: he_id.clone(),
                    node_ids: vec![node_ids[i].clone()],
                    kind: HyperedgeKind::Evolution,
                    weight: 1.0,
                    created_at: now,
                    updated_at: now,
                    version: 1,
                    history: Vec::new(),
                    meta: HashMap::new(),
                    chain_prev: Some(parent_id.clone()),
                    chain_next: None,
                    chain_label: item.chain_label.clone(),
                };
                let bytes = bincode::serialize(&he)
                    .map_err(|e| MemHopError::Internal(format!("serialize chain hyperedge: {}", e)))?;
                hyperedges_table.insert(he_id.as_str(), bytes.as_slice())
                    .map_err(|e| MemHopError::Storage(format!("insert chain hyperedge: {}", e)))?;
                report.chains_created += 1;
            }
        }
    }

    // Phase 4: L2 topic update — 带 llm_compressed_summary + 真实 node_id + doc_ids + linked_domain_ids + centroid
    {
        let mut topics_table = wtxn.open_table(L2_TOPICS)
            .map_err(|e| MemHopError::Storage(format!("open L2_TOPICS: {}", e)))?;

        let mut topic_cache: HashMap<String, Topic> = HashMap::new();
        // 收集每个 topic 的新 node 向量（用于 centroid 更新）
        let mut topic_new_nodes: HashMap<String, Vec<(String, Vec<half::f16>)>> = HashMap::new();

        for (i, item) in encoded.iter().enumerate() {
            if let Some(ref label) = item.topic_label {
                // 根据 label 查找或创建 topic
                let lookup_key = format!("label:{}", label);
                // 先查 label→id，提权到 Option<String> 以释放 AccessGuard 的借用
                let lookup_result = topics_table.get(lookup_key.as_str())
                    .map_err(|e| MemHopError::Storage(format!("get topic lookup: {}", e)))?;
                let existing_id: Option<String> = lookup_result
                    .and_then(|bytes| bincode::deserialize(bytes.value()).ok());

                let (topic_id, _is_new) = if let Some(id) = existing_id {
                    (id, false)
                } else {
                    // 创建新 topic（AccessGuard 已借用到 existing_id 后释放）
                    let now = chrono::Utc::now().timestamp_millis();
                    let id = format!("topic_{}", now);
                    let meta_key = format!("topic:{}:meta", &id);
                    let topic = Topic {
                        id: id.clone(),
                        label: label.clone(),
                        summary: None,
                        keywords: Vec::new(),
                        centroid: Vec::new(),
                        node_ids: Vec::new(),
                        linked_domain_ids: Vec::new(),
                        doc_ids: Vec::new(),
                        dialogue_range: None,
                        created_at: now,
                        updated_at: now,
                        version: 1,
                        history: Vec::new(),
                        extended_meta: HashMap::new(),
                        domain_weights: HashMap::new(),
                        node_weights: HashMap::new(),
                    };
                    let bytes = bincode::serialize(&topic)
                        .map_err(|e| MemHopError::Internal(format!("serialize topic: {}", e)))?;
                    topics_table.insert(meta_key.as_str(), bytes.as_slice())
                        .map_err(|e| MemHopError::Storage(format!("insert topic: {}", e)))?;
                    // 写 label→id 映射
                    let id_bytes = bincode::serialize(&id)
                        .map_err(|e| MemHopError::Internal(format!("serialize topic id: {}", e)))?;
                    topics_table.insert(lookup_key.as_str(), id_bytes.as_slice())
                        .map_err(|e| MemHopError::Storage(format!("insert topic lookup: {}", e)))?;
                    report.l2_topics_created += 1;
                    (id, true)
                };

                // 获取 topic（优先从缓存）
                let topic = if let Some(cached) = topic_cache.get(&topic_id) {
                    cached.clone()
                } else {
                    let meta_key = format!("topic:{}:meta", &topic_id);
                    let meta_result = topics_table.get(meta_key.as_str())
                        .map_err(|e| MemHopError::Storage(format!("get topic meta: {}", e)))?;
                    match meta_result {
                        Some(bytes) => bincode::deserialize(bytes.value())
                            .map_err(|e| MemHopError::Internal(format!("deserialize topic: {}", e)))?,
                        None => continue,
                    }
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
                    && !topic.linked_domain_ids.contains(domain_id) {
                        topic.linked_domain_ids.push(domain_id.clone());
                        topic_changed = true;
                    }

                if topic_changed {
                    // v0.18.0: 计算关联强度
                    // 1. 领域关联强度
                    if let Some(ref domain_id) = item.domain_id {
                        let current_weight =
                            topic.domain_weights.get(domain_id).copied().unwrap_or(0.0);
                        topic.domain_weights.insert(domain_id.clone(), current_weight + 1.0);
                    }
                    // 2. 节点关联强度
                    if i < node_ids.len() {
                        let node_id = &node_ids[i];
                        let current_weight =
                            topic.node_weights.get(node_id).copied().unwrap_or(0.0);
                        topic.node_weights.insert(node_id.clone(), current_weight + 1.0);
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
                    let meta_key = format!("topic:{}:meta", &topic_id);
                    let bytes = bincode::serialize(&topic)
                        .map_err(|e| MemHopError::Internal(format!("serialize updated topic: {}", e)))?;
                    topics_table.insert(meta_key.as_str(), bytes.as_slice())
                        .map_err(|e| MemHopError::Storage(format!("insert updated topic: {}", e)))?;
                    topic_cache.insert(topic_id.clone(), topic);
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
            if nodes.is_empty() {
                continue;
            }
            let dim = nodes[0].1.len();
            if dim == 0 {
                continue;
            }
            let mut sum = vec![0.0f64; dim];
            for (_, v) in nodes {
                for (i, val) in v.iter().enumerate() {
                    sum[i] += val.to_f64();
                }
            }
            let n = nodes.len() as f64;
            let centroid: Vec<half::f16> = sum.iter()
                .map(|s| half::f16::from_f64(*s / n))
                .collect();

            let meta_key = format!("topic:{}:meta", tid);
            // 分离 get 和 insert 以避免 AccessGuard 活锁
            let existing_topic: Option<Topic> = match topics_table.get(meta_key.as_str()) {
                Ok(Some(bytes)) => bincode::deserialize(bytes.value()).ok(),
                _ => None,
            };
            if let Some(mut t) = existing_topic {
                t.centroid = centroid;
                t.updated_at = chrono::Utc::now().timestamp_millis();
                if let Ok(new_bytes) = bincode::serialize(&t) {
                    let _ = topics_table.insert(meta_key.as_str(), new_bytes.as_slice());
                }
            }
        }
    }

    // Phase 5: L3 domain write — v0.24.0: 仅在显式指定 domain_id 且 domain 已存在时写入
    {
        let domain_meta_table = wtxn.open_table(L3_DOMAIN_META)
            .map_err(|e| MemHopError::Storage(format!("open L3_DOMAIN_META: {}", e)))?;
        let mut domain_nodes_table = wtxn.open_table(L3_DOMAIN_NODES)
            .map_err(|e| MemHopError::Storage(format!("open L3_DOMAIN_NODES: {}", e)))?;

        for item in &encoded {
            // v0.24.0: 仅当显式指定 domain_id 且 domain 已存在时写入 L3
            let Some(ref domain_id) = item.domain_id else {
                continue;
            };

            let meta_key = format!("meta:{}", domain_id);
            if domain_meta_table.get(meta_key.as_str())
                .map_err(|e| MemHopError::Storage(format!("get domain meta: {}", e)))?
                .is_none()
            {
                // domain 不存在，跳过（L3 仅接受已 mount 或已 crystallize 的 domain）
                #[cfg(debug_assertions)]
                eprintln!("[batch_store] skip L3: domain '{}' not found", domain_id);
                continue;
            }

            let l3_id = unique_id("l3n");
            let domain_node_key = format!("node:{}:{}", domain_id, l3_id);
            let now = chrono::Utc::now().timestamp_millis();
            let domain_node_bytes = bincode::serialize(&serde_json::json!({
                "id": l3_id,
                "text": item.text,
                "sparse": item.sparse,
                "vector": item.vector,
                "created_at": now,
            }))
                .map_err(|e| MemHopError::Internal(format!("serialize domain node: {}", e)))?;
            domain_nodes_table.insert(domain_node_key.as_str(), domain_node_bytes.as_slice())
                .map_err(|e| MemHopError::Storage(format!("insert domain node: {}", e)))?;
            report.l3_nodes_created += 1;
            input_first_l3_node.entry(item.input_index).or_insert(l3_id.clone());
        }
    }

    // 提交单事务
    wtxn.commit()
        .map_err(|e| MemHopError::Storage(format!("redb commit: {}", e)))?;

    // 事务提交后，重建 BM25 in-memory 索引（直接从 redb 读取最新数据）
    if let Some(ref mut l1) = brain.l1 {
        l1.bm25.rebuild_from_redb()
            .map_err(|e| MemHopError::Storage(format!("rebuild BM25: {}", e)))?;
    }

    report.total_duration_us = start.elapsed().as_micros() as u64;

    // 将输入索引映射转换为字符串键值对
    for (idx, node_id) in input_first_node {
        report.engram_ids.insert(idx.to_string(), node_id);
    }
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
