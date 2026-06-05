//! query_engine — 按层检索引擎。
//! L1 → L2 → L4 级联检索 + per-layer RRF 融合。

use std::collections::HashMap;
use half::f16;
use crate::error::Result;
use crate::types::{RecallRequest, RecallResponse, RecallResult, Layer};
use crate::brain::Brain;

/// v0.18.0: 计算动态 RRF k 值
/// 根据结果数量调整 k 值：结果越多，k 值越大，避免长尾结果过度影响
fn dynamic_rrf_k(result_count: usize) -> f64 {
    match result_count {
        0..=10 => 60.0,
        11..=50 => 80.0,
        51..=100 => 100.0,
        _ => 120.0,
    }
}

pub(crate) fn execute(brain: &Brain, req: &RecallRequest) -> Result<RecallResponse> {
    if req.query.trim().is_empty() {
        return Ok(RecallResponse {
            results: vec![], total_count: 0,
            l0_profile: None, confidence: None, activated_topics: Vec::new(),
        });
    }
    let encoded = brain.encoder.encode(&req.query);
    let sparse = &encoded.sparse;
    let dense = &encoded.dense;

    let layers = if req.target_layers.is_empty() {
        vec![Layer::L1, Layer::L2, Layer::L4]
    } else {
        req.target_layers.clone()
    };

    // 收集各层结果并分别排名
    let mut layers_map: HashMap<Layer, Vec<RecallResult>> = HashMap::new();
    for layer in &layers {
        let layer_results = match layer {
            Layer::L1 => search_l1(brain, sparse, dense, req.max_results)?,
            Layer::L2 => search_l2(brain, sparse, dense, req.max_results)?,
            Layer::L3 => search_l3(brain, sparse, dense, req.max_results)?,
            Layer::L4 => search_l4(brain, sparse, dense, req.max_results)?,
            Layer::L0 => Vec::new(),
        };
        layers_map.entry(*layer).or_default().extend(layer_results);
    }

    // 级联路由：如果有 l2_topic_id，将 L1 结果限制到该 Topic 的 node_ids
    if let Some(ref topic_id) = req.l2_topic_id {
        let allowed_ids = get_topic_node_ids(brain, topic_id)?;
        if let Some(l1_results) = layers_map.get_mut(&Layer::L1) {
            l1_results.retain(|r| allowed_ids.contains(&r.id));
        }
    }

    // 级联路由：如果有 l3_domain_id，找到关联 L2 Topic，再限制 L1 范围
    if let Some(ref domain_id) = req.l3_domain_id {
        let txn = brain.l2_env.env.read_txn()
            .map_err(|e| crate::error::MemHopError::Storage(e.to_string()))?;
        let topics = brain.l2.get_topics_by_domain(&txn, &brain.l2_env, domain_id)?;
        drop(txn);
        let mut allowed_ids: std::collections::HashSet<String> = std::collections::HashSet::new();
        for t in &topics {
            allowed_ids.extend(t.node_ids.iter().cloned());
        }
        if let Some(l1_results) = layers_map.get_mut(&Layer::L1) {
            l1_results.retain(|r| allowed_ids.contains(&r.id));
        }
    }

    // Per-layer RRF 融合（各层独立 rank，保证跨层公平）
    let mut rrf_scores: HashMap<String, f64> = HashMap::new();
    let mut id_to_result: HashMap<String, RecallResult> = HashMap::new();
    let k = 60.0;

    for (_layer, mut layer_results) in layers_map {
        layer_results.sort_by(|a, b| b.score.partial_cmp(&a.score).unwrap_or(std::cmp::Ordering::Equal));
        for (rank, r) in layer_results.into_iter().enumerate() {
            *rrf_scores.entry(r.id.clone()).or_insert(0.0) += 1.0 / (k + rank as f64);
            id_to_result.entry(r.id.clone()).or_insert(r);
        }
    }

    let mut ranked: Vec<(String, f64)> = rrf_scores.into_iter().collect();
    ranked.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
    ranked.truncate(req.max_results);

    let mut results: Vec<RecallResult> = ranked.into_iter()
        .filter_map(|(id, _)| id_to_result.remove(&id))
        .filter(|r| {
            // exclude_ids 过滤
            if !req.exclude_ids.is_empty() && req.exclude_ids.contains(&r.id) {
                return false;
            }
            // exclude_topic_ids 过滤
            if !req.exclude_topic_ids.is_empty()
                && let Some(ref label) = r.topic_label
                    && req.exclude_topic_ids.iter().any(|t| label.contains(t)) {
                        return false;
                    }
            true
        })
        .collect();

    let total_count = results.len();

    // v0.16.0: Time-aware decay: score *= exp(-λ * hours_since_creation)
    if let Some(lambda) = req.time_decay_lambda {
        let now_ms = chrono::Utc::now().timestamp_millis();
        for r in &mut results {
            let hours = (now_ms - r.created_at).max(0) as f32 / 3_600_000.0;
            let decay = (-lambda * hours).exp();
            r.score *= decay;
        }
        // Re-sort after decay
        results.sort_by(|a, b| b.score.partial_cmp(&a.score).unwrap_or(std::cmp::Ordering::Equal));
    }

    // v0.16.0: Improved confidence calculation
    // 双通道一致性(40%) + 结果数量因子(30%) + 最高分绝对值(30%)
    let confidence = if results.is_empty() {
        None
    } else if results.len() == 1 {
        Some(0.4f32 + 0.3 * results[0].score.clamp(0.0, 0.5)) // single result: base + score factor
    } else {
        let top_score = results[0].score;
        // Consistency: how close is top-2 score to top-1
        let consistency = if results.len() >= 2 {
            let gap = (results[0].score - results[1].score).abs();
            (1.0 - gap / (top_score.max(1e-6))).clamp(0.0, 1.0)
        } else {
            0.5
        };
        // Count factor: more results = more confident (up to max)
        let count_factor = (results.len() as f32 / req.max_results as f32).min(1.0);
        // Top score normalization
        let score_factor = top_score.clamp(0.0, 1.0);
        Some((0.4 * consistency + 0.3 * count_factor + 0.3 * score_factor).clamp(0.0, 1.0))
    };

    Ok(RecallResponse {
        results,
        total_count,
        l0_profile: None,
        confidence,
        activated_topics: Vec::new(),
    })
}

/// L1 超图检索 — BM25（稀疏）+ 余弦（稠密）双通道 RRF 融合
pub(crate) fn search_l1(brain: &Brain, sparse: &HashMap<String, f32>, dense: &[f16], max: usize) -> Result<Vec<RecallResult>> {
    let txn = brain.l1_env.env.read_txn()
        .map_err(|e| crate::error::MemHopError::Storage(e.to_string()))?;

    // ── BM25 通道（独立列表，不与 cosine 混用）───────────
    let bm25_hits = brain.l1.search(sparse, max)?;
    let mut bm25_results: Vec<RecallResult> = Vec::with_capacity(bm25_hits.len());
    for (node_id, bm25_score) in &bm25_hits {
        if let Ok(Some(node)) = brain.l1.get_node(&txn, &brain.l1_env, node_id) {
            bm25_results.push(RecallResult {
                layer: Layer::L1,
                id: node_id.clone(),
                text: node.summary.unwrap_or(node.text).chars().take(200).collect(),
                score: *bm25_score * node.importance,
                topic_label: None,
                created_at: node.created_at,
                version: node.version,
            });
        }
    }

    // ── Cosine 通道（独立列表） ──────────────────────────
    let has_cosine = !brain.l1.vector_index.is_empty()
        && !dense.is_empty()
        && dense.iter().any(|v| v.to_f32().abs() > 1e-8);

    let mut cos_results: Vec<RecallResult> = Vec::new();
    if has_cosine {
        let cosine_hits = brain.l1.vector_index.cosine_search(dense, max);
        for (node_id, cos_sim) in &cosine_hits {
            if let Ok(Some(node)) = brain.l1.get_node(&txn, &brain.l1_env, node_id) {
                cos_results.push(RecallResult {
                    layer: Layer::L1,
                    id: node_id.clone(),
                    text: node.summary.unwrap_or(node.text).chars().take(200).collect(),
                    score: *cos_sim * node.importance,
                    topic_label: None,
                    created_at: node.created_at,
                    version: node.version,
                });
            }
        }
    }

    // ── 双通道内层 RRF 融合 ────────────────────────────────
    if !cos_results.is_empty() {
        let rrf_k = dynamic_rrf_k(bm25_results.len() + cos_results.len());
        let mut rrf_scores: HashMap<String, f64> = HashMap::new();
        let mut id_to_result: HashMap<String, RecallResult> = HashMap::new();

        // BM25 通道排 rank
        bm25_results.sort_by(|a, b| b.score.partial_cmp(&a.score).unwrap_or(std::cmp::Ordering::Equal));
        for (rank, r) in bm25_results.iter().enumerate() {
            *rrf_scores.entry(r.id.clone()).or_insert(0.0) += 1.0 / (rrf_k + rank as f64);
            id_to_result.entry(r.id.clone()).or_insert(r.clone());
        }

        // Cosine 通道排 rank
        cos_results.sort_by(|a, b| b.score.partial_cmp(&a.score).unwrap_or(std::cmp::Ordering::Equal));
        for (rank, r) in cos_results.iter().enumerate() {
            *rrf_scores.entry(r.id.clone()).or_insert(0.0) += 1.0 / (rrf_k + rank as f64);
            id_to_result.entry(r.id.clone()).or_insert(r.clone());
        }

        // 按 RRF 分数排序并写回 score 字段
        let mut ranked: Vec<(String, f64)> = rrf_scores.into_iter().collect();
        ranked.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
        ranked.truncate(max);

        let results: Vec<RecallResult> = ranked.into_iter()
            .filter_map(|(id, rrf_score)| id_to_result.remove(&id).map(|mut r| { r.score = rrf_score as f32; r }))
            .collect();
        return Ok(results);
    }

    // ── 单通道（纯 BM25）：排序 + 截断 ────────────────────
    bm25_results.sort_by(|a, b| b.score.partial_cmp(&a.score).unwrap_or(std::cmp::Ordering::Equal));
    bm25_results.truncate(max);
    Ok(bm25_results)
}

/// L2 话题检索（含向量通道）：Cosine 粗筛 + ngram 重叠双通道 RRF。
pub(crate) fn search_l2(brain: &Brain, sparse: &HashMap<String, f32>, dense: &[half::f16], max: usize) -> Result<Vec<RecallResult>> {
    let txn = brain.l2_env.env.read_txn()
        .map_err(|e| crate::error::MemHopError::Storage(e.to_string()))?;

    // ── 通道 1: ngram 重叠（始终可用）───────────────────
    let mut ngram_results: Vec<RecallResult> = Vec::new();
    if let Ok(iter) = brain.l2_env.topics.iter(&txn) {
        for (key, bytes) in iter.flatten() {
            if !key.starts_with("topic:") { continue; }
            if let Ok(topic) = bincode::deserialize::<crate::engram::Topic>(bytes) {
                let mut overlap = 0.0f32;
                let label_lower = topic.label.to_lowercase();
                for ngram in sparse.keys() {
                    if label_lower.contains(ngram) { overlap += 1.0; }
                    for kw in &topic.keywords {
                        if kw.to_lowercase().contains(ngram) { overlap += 0.5; }
                    }
                    // v0.17.0: topic.summary 也参与 ngram 匹配
                    if let Some(ref summary) = topic.summary
                        && summary.to_lowercase().contains(ngram) { overlap += 0.3; }
                }
                if overlap > 0.0 {
                    let label = topic.label.clone();
                    // v0.18.0: 计算关联强度权重
                    let domain_weight_sum: f32 = topic.domain_weights.values().sum();
                    let node_weight_sum: f32 = topic.node_weights.values().sum();
                    let association_weight = 1.0 + (domain_weight_sum + node_weight_sum).ln().max(0.0);
                    
                    let score = (overlap * 0.1).min(1.0) * association_weight;
                    ngram_results.push(RecallResult {
                        layer: Layer::L2,
                        id: topic.id,
                        text: topic.summary.unwrap_or(label.clone()),
                        score,
                        topic_label: Some(label.clone()),
                        created_at: topic.created_at,
                        version: topic.version,
                    });
                }
            }
        }
    }

    // ── 通道 2: 向量 Cosine 粗筛（当 centroid 索引可用时）────
    let has_cosine = !brain.l2.topic_vectors.is_empty()
        && !dense.is_empty()
        && dense.iter().any(|v| v.to_f32().abs() > 1e-8);

    if has_cosine {
        let cos_hits = brain.l2.search_by_vector(dense, max * 2);
        let mut cos_results: Vec<RecallResult> = Vec::new();
        for (topic_id, cos_sim) in &cos_hits {
            if let Ok(Some(topic)) = brain.l2.get_topic_by_id(&txn, &brain.l2_env, topic_id) {
                let label = topic.label.clone();
                cos_results.push(RecallResult {
                    layer: Layer::L2,
                    id: topic_id.clone(),
                    text: topic.summary.unwrap_or(label.clone()),
                    score: *cos_sim,
                    topic_label: Some(label),
                    created_at: topic.created_at,
                    version: topic.version,
                });
            }
        }

        // 双通道 RRF 融合
        if !cos_results.is_empty() {
            let rrf_k = dynamic_rrf_k(ngram_results.len() + cos_results.len());
            let mut rrf_scores: HashMap<String, f64> = HashMap::new();
            let mut id_to_result: HashMap<String, RecallResult> = HashMap::new();

            ngram_results.sort_by(|a, b| b.score.partial_cmp(&a.score).unwrap_or(std::cmp::Ordering::Equal));
            for (rank, r) in ngram_results.iter().enumerate() {
                *rrf_scores.entry(r.id.clone()).or_insert(0.0) += 1.0 / (rrf_k + rank as f64);
                id_to_result.entry(r.id.clone()).or_insert(r.clone());
            }

            cos_results.sort_by(|a, b| b.score.partial_cmp(&a.score).unwrap_or(std::cmp::Ordering::Equal));
            for (rank, r) in cos_results.iter().enumerate() {
                *rrf_scores.entry(r.id.clone()).or_insert(0.0) += 1.0 / (rrf_k + rank as f64);
                id_to_result.entry(r.id.clone()).or_insert(r.clone());
            }

            let mut ranked: Vec<(String, f64)> = rrf_scores.into_iter().collect();
            ranked.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
            ranked.truncate(max);

            let results: Vec<RecallResult> = ranked.into_iter()
                .filter_map(|(id, rrf_score)| id_to_result.remove(&id).map(|mut r| { r.score = rrf_score as f32; r }))
                .collect();
            return Ok(results);
        }
    }

    // ── 单通道回退 ────────────────────────────────────
    ngram_results.sort_by(|a, b| b.score.partial_cmp(&a.score).unwrap_or(std::cmp::Ordering::Equal));
    ngram_results.truncate(max);
    Ok(ngram_results)
}

/// L3 领域检索 — ngram 重叠 + dense cosine 双通道 RRF 融合
pub(crate) fn search_l3(brain: &Brain, sparse: &HashMap<String, f32>, dense: &[f16], max: usize) -> Result<Vec<RecallResult>> {
    let txn = brain.l3_env.env.read_txn()
        .map_err(|e| crate::error::MemHopError::Storage(e.to_string()))?;

    // ── 通道 1: BM25 搜索（替代全量扫描）────────────────────
    let mut ngram_results: Vec<RecallResult> = Vec::new();
    
    // v0.18.0: 计算查询ngram平均长度权重
    let avg_ngram_len = if sparse.is_empty() {
        1.0
    } else {
        let total_len: usize = sparse.keys().map(|k| k.len()).sum();
        (total_len as f32 / sparse.len() as f32).max(1.0)
    };
    // 长ngram权重更高，但限制在1.0-2.0范围内
    let ngram_len_weight = (avg_ngram_len / 10.0).clamp(1.0, 2.0);
    
    let bm25_hits = brain.l3.search_by_bm25(sparse, max * 2);
    for (node_id, bm25_score) in &bm25_hits {
        // 从 LMDB 获取节点详情
        let key_prefix = "node:".to_string();
        if let Ok(iter) = brain.l3_env.domain_nodes.iter(&txn) {
            for item in iter {
                if let Ok((key, bytes)) = item
                    && key.starts_with(&key_prefix) && key.ends_with(&format!(":{}", node_id))
                        && let Ok(node) = bincode::deserialize::<crate::engram::KnowledgeNode>(bytes) {
                            // v0.18.0: 优化score计算，考虑ngram长度权重
                            let score = *bm25_score * node.importance * ngram_len_weight;
                            ngram_results.push(RecallResult {
                                layer: Layer::L3,
                                id: node_id.clone(),
                                text: node.summary.unwrap_or(node.text).chars().take(200).collect(),
                                score,
                                topic_label: None,
                                created_at: node.created_at,
                                version: node.version,
                            });
                            break;
                        }
            }
        }
    }

    // ── 通道 2: dense cosine ──────────────────────────────
    let has_cosine = !brain.l3.vector_index.is_empty()
        && !dense.is_empty()
        && dense.iter().any(|v| v.to_f32().abs() > 1e-8);

    if has_cosine {
        let cos_hits = brain.l3.search_by_vector(dense, max * 2);
        // 构建 id → node 映射（L3 key 是 "node:{domain_id}:{node_id}"）
        let hit_ids: std::collections::HashSet<&str> = cos_hits.iter().map(|(id, _)| id.as_str()).collect();
        let mut node_map: HashMap<String, crate::engram::KnowledgeNode> = HashMap::new();
        if let Ok(iter) = brain.l3_env.domain_nodes.iter(&txn) {
            for item in iter {
                if let Ok((_key, bytes)) = item
                    && let Ok(node) = bincode::deserialize::<crate::engram::KnowledgeNode>(bytes)
                        && hit_ids.contains(node.id.as_str()) {
                            node_map.insert(node.id.clone(), node);
                        }
            }
        }
        let mut cos_results: Vec<RecallResult> = Vec::new();
        for (node_id, cos_sim) in &cos_hits {
            if let Some(node) = node_map.get(node_id) {
                cos_results.push(RecallResult {
                    layer: Layer::L3,
                    id: node_id.clone(),
                    text: node.summary.clone().unwrap_or_else(|| node.text.clone()).chars().take(200).collect(),
                    score: *cos_sim * node.importance,
                    topic_label: None,
                    created_at: node.created_at,
                    version: node.version,
                });
            }
        }

        // 双通道 RRF 融合
        if !cos_results.is_empty() {
            let rrf_k = dynamic_rrf_k(ngram_results.len() + cos_results.len());
            let mut rrf_scores: HashMap<String, f64> = HashMap::new();
            let mut id_to_result: HashMap<String, RecallResult> = HashMap::new();

            ngram_results.sort_by(|a, b| b.score.partial_cmp(&a.score).unwrap_or(std::cmp::Ordering::Equal));
            for (rank, r) in ngram_results.iter().enumerate() {
                *rrf_scores.entry(r.id.clone()).or_insert(0.0) += 1.0 / (rrf_k + rank as f64);
                id_to_result.entry(r.id.clone()).or_insert(r.clone());
            }
            cos_results.sort_by(|a, b| b.score.partial_cmp(&a.score).unwrap_or(std::cmp::Ordering::Equal));
            for (rank, r) in cos_results.iter().enumerate() {
                *rrf_scores.entry(r.id.clone()).or_insert(0.0) += 1.0 / (rrf_k + rank as f64);
                id_to_result.entry(r.id.clone()).or_insert(r.clone());
            }

            let mut ranked: Vec<(String, f64)> = rrf_scores.into_iter().collect();
            ranked.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
            ranked.truncate(max);
            let results: Vec<RecallResult> = ranked.into_iter()
                .filter_map(|(id, rrf_score)| id_to_result.remove(&id).map(|mut r| { r.score = rrf_score as f32; r }))
                .collect();
            return Ok(results);
        }
    }

    // ── 单通道回退 ────────────────────────────────────
    ngram_results.sort_by(|a, b| b.score.partial_cmp(&a.score).unwrap_or(std::cmp::Ordering::Equal));
    ngram_results.truncate(max);
    Ok(ngram_results)
}

/// L4 原文检索 — ngram 重叠 + dense cosine 双通道 RRF 融合
pub(crate) fn search_l4(brain: &Brain, sparse: &HashMap<String, f32>, dense: &[f16], max: usize) -> Result<Vec<RecallResult>> {
    let txn = brain.l4_env.env.read_txn()
        .map_err(|e| crate::error::MemHopError::Storage(e.to_string()))?;

    // ── 通道 1: ngram 重叠 ────────────────────────────
    let mut ngram_results: Vec<RecallResult> = Vec::new();
    if let Ok(iter) = brain.l4_env.docs.iter(&txn) {
        for item in iter {
            if let Ok((_key, bytes)) = item
                && let Ok(doc) = bincode::deserialize::<crate::engram::RawDocument>(bytes) {
                    let text_lower = doc.text.to_lowercase();
                    let overlap: f32 = sparse.keys()
                        .filter(|k| text_lower.contains(*k))
                        .count() as f32;
                    if overlap > 0.0 {
                        ngram_results.push(RecallResult {
                            layer: Layer::L4,
                            id: doc.id,
                            text: doc.text.chars().take(200).collect(),
                            score: (overlap * 0.15).min(1.0),
                            topic_label: None,
                            created_at: doc.created_at,
                            version: doc.version,
                        });
                    }
                }
        }
    }

    // ── 通道 2: dense cosine ──────────────────────────────
    let has_cosine = !brain.l4.vector_index.is_empty()
        && !dense.is_empty()
        && dense.iter().any(|v| v.to_f32().abs() > 1e-8);

    if has_cosine {
        let cos_hits = brain.l4.search_by_vector(dense, max * 2);
        let mut cos_results: Vec<RecallResult> = Vec::new();
        for (doc_id, cos_sim) in &cos_hits {
            if let Ok(Some(bytes)) = brain.l4_env.docs.get(&txn, doc_id)
                && let Ok(doc) = bincode::deserialize::<crate::engram::RawDocument>(bytes) {
                    cos_results.push(RecallResult {
                        layer: Layer::L4,
                        id: doc_id.clone(),
                        text: doc.text.chars().take(200).collect(),
                        score: *cos_sim,
                        topic_label: None,
                        created_at: doc.created_at,
                        version: doc.version,
                    });
                }
        }

        // 双通道 RRF 融合
        if !cos_results.is_empty() {
            let rrf_k = dynamic_rrf_k(ngram_results.len() + cos_results.len());
            let mut rrf_scores: HashMap<String, f64> = HashMap::new();
            let mut id_to_result: HashMap<String, RecallResult> = HashMap::new();

            ngram_results.sort_by(|a, b| b.score.partial_cmp(&a.score).unwrap_or(std::cmp::Ordering::Equal));
            for (rank, r) in ngram_results.iter().enumerate() {
                *rrf_scores.entry(r.id.clone()).or_insert(0.0) += 1.0 / (rrf_k + rank as f64);
                id_to_result.entry(r.id.clone()).or_insert(r.clone());
            }
            cos_results.sort_by(|a, b| b.score.partial_cmp(&a.score).unwrap_or(std::cmp::Ordering::Equal));
            for (rank, r) in cos_results.iter().enumerate() {
                *rrf_scores.entry(r.id.clone()).or_insert(0.0) += 1.0 / (rrf_k + rank as f64);
                id_to_result.entry(r.id.clone()).or_insert(r.clone());
            }

            let mut ranked: Vec<(String, f64)> = rrf_scores.into_iter().collect();
            ranked.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
            ranked.truncate(max);
            let results: Vec<RecallResult> = ranked.into_iter()
                .filter_map(|(id, rrf_score)| id_to_result.remove(&id).map(|mut r| { r.score = rrf_score as f32; r }))
                .collect();
            return Ok(results);
        }
    }

    // ── 单通道回退 ────────────────────────────────────
    ngram_results.sort_by(|a, b| b.score.partial_cmp(&a.score).unwrap_or(std::cmp::Ordering::Equal));
    ngram_results.truncate(max);
    Ok(ngram_results)
}

/// 获取指定 Topic 的 node_ids 集合（用于级联路由过滤）
fn get_topic_node_ids(brain: &Brain, topic_id: &str) -> Result<std::collections::HashSet<String>> {
    let txn = brain.l2_env.env.read_txn()
        .map_err(|e| crate::error::MemHopError::Storage(e.to_string()))?;
    match brain.l2.get_topic_by_id(&txn, &brain.l2_env, topic_id)? {
        Some(topic) => Ok(topic.node_ids.into_iter().collect()),
        None => Ok(std::collections::HashSet::new()),
    }
}

/// v0.18.0: 跨层结果验证
/// 计算结果在各层的一致性分数，用于调整最终排序
/// 一致性分数基于：
/// 1. 结果在多少个不同的层中出现
/// 2. 结果在各层中的排名一致性
/// 3. 结果的跨层关联强度
pub(crate) fn cross_layer_validation(results: &mut [RecallResult], _brain: &Brain) {
    if results.is_empty() {
        return;
    }
    
    // 统计每个结果在各层中的出现次数和排名
    let mut layer_counts: HashMap<String, usize> = HashMap::new();
    let mut layer_ranks: HashMap<String, Vec<usize>> = HashMap::new();
    
    for (rank, result) in results.iter().enumerate() {
        let id = result.id.clone();
        *layer_counts.entry(id.clone()).or_insert(0) += 1;
        layer_ranks.entry(id.clone()).or_default().push(rank);
    }
    
    // 计算一致性分数
    for result in results.iter_mut() {
        let id = &result.id;
        let count = layer_counts.get(id).copied().unwrap_or(1);
        let ranks = layer_ranks.get(id).cloned().unwrap_or_default();
        
        // 1. 跨层出现次数权重（出现次数越多，一致性越高）
        let layer_weight = (count as f32).ln().max(0.0);
        
        // 2. 排名一致性权重（排名越接近，一致性越高）
        let rank_variance = if ranks.len() > 1 {
            let mean_rank: f32 = ranks.iter().map(|r| *r as f32).sum::<f32>() / ranks.len() as f32;
            let variance: f32 = ranks.iter().map(|r| {
                let diff = *r as f32 - mean_rank;
                diff * diff
            }).sum::<f32>() / ranks.len() as f32;
            variance.sqrt() / (ranks.len() as f32).max(1.0)
        } else {
            0.0
        };
        let rank_consistency = 1.0 / (1.0 + rank_variance);
        
        // 3. 综合一致性分数
        let consistency_score = 1.0 + layer_weight * rank_consistency;
        
        // 调整结果分数
        result.score *= consistency_score;
    }
}
