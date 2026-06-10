//! v0.25.0: query_engine — 按层检索引擎（L4 默认移除，仅精确匹配）。
//! L1 → L2 级联检索 + per-layer RRF 融合。
//!
//! v0.25.0: L1 节点详情从 redb 读取。

use crate::brain::Brain;
use crate::error::{MemHopError, Result};
use crate::index::RrfWeights;
use crate::recall::build_prefetch_hints;
use crate::storage::L1_NODES;
use crate::types::{Layer, RecallRequest, RecallResponse, RecallResult};
use half::f16;
use redb::ReadableTable;
use std::collections::HashMap;
use std::collections::HashSet;

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

/// Reciprocal Rank Fusion 融合多个排序通道
/// 每个通道独立排序后按 RRF 公式计算融合分数，结果去重
fn rrf_merge(
    channels: Vec<Vec<RecallResult>>,
    k: f64,
    max: usize,
) -> Vec<RecallResult> {
    let mut rrf_scores: HashMap<String, f64> = HashMap::new();
    let mut id_to_result: HashMap<String, RecallResult> = HashMap::new();

    for mut results in channels {
        results.sort_by(|a, b| {
            b.score
                .partial_cmp(&a.score)
                .unwrap_or(std::cmp::Ordering::Equal)
        });
        for (rank, r) in results.into_iter().enumerate() {
            *rrf_scores.entry(r.id.clone()).or_insert(0.0) += 1.0 / (k + rank as f64);
            id_to_result.entry(r.id.clone()).or_insert(r);
        }
    }

    let mut ranked: Vec<(String, f64)> = rrf_scores.into_iter().collect();
    ranked.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
    ranked.truncate(max);

    ranked
        .into_iter()
        .filter_map(|(id, rrf_score)| {
            id_to_result.remove(&id).map(|mut r| {
                r.score = rrf_score as f32;
                r
            })
        })
        .collect()
}

/// v1.0: 三通道 RRF 融合 — BM25 + HNSW + E5
#[allow(dead_code)]
fn rrf_merge_triple(
    channel_bm25: Vec<RecallResult>,
    channel_hnsw: Vec<RecallResult>,
    channel_e5: Vec<RecallResult>,
    k: f64,
    max: usize,
    weights: &RrfWeights,
) -> Vec<RecallResult> {
    let mut rrf_scores: HashMap<String, f64> = HashMap::new();
    let mut id_to_result: HashMap<String, RecallResult> = HashMap::new();

    // 通道 1: BM25
    let mut sorted_bm25 = channel_bm25;
    sorted_bm25.sort_by(|a, b| b.score.partial_cmp(&a.score).unwrap_or(std::cmp::Ordering::Equal));
    for (rank, r) in sorted_bm25.into_iter().enumerate() {
        *rrf_scores.entry(r.id.clone()).or_insert(0.0) += weights.bm25 / (k + rank as f64);
        id_to_result.entry(r.id.clone()).or_insert(r);
    }

    // 通道 2: HNSW
    let mut sorted_hnsw = channel_hnsw;
    sorted_hnsw.sort_by(|a, b| b.score.partial_cmp(&a.score).unwrap_or(std::cmp::Ordering::Equal));
    for (rank, r) in sorted_hnsw.into_iter().enumerate() {
        *rrf_scores.entry(r.id.clone()).or_insert(0.0) += weights.hnsw / (k + rank as f64);
        id_to_result.entry(r.id.clone()).or_insert(r);
    }

    // 通道 3: E5
    let mut sorted_e5 = channel_e5;
    sorted_e5.sort_by(|a, b| b.score.partial_cmp(&a.score).unwrap_or(std::cmp::Ordering::Equal));
    for (rank, r) in sorted_e5.into_iter().enumerate() {
        *rrf_scores.entry(r.id.clone()).or_insert(0.0) += weights.e5 / (k + rank as f64);
        id_to_result.entry(r.id.clone()).or_insert(r);
    }

    let mut ranked: Vec<(String, f64)> = rrf_scores.into_iter().collect();
    ranked.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
    ranked.truncate(max);

    ranked
        .into_iter()
        .filter_map(|(id, _rrf_score)| {
            id_to_result.remove(&id).map(|mut r| {
                r.score = _rrf_score as f32;
                r
            })
        })
        .collect()
}

#[allow(dead_code)]
pub(crate) fn execute(brain: &mut Brain, req: &RecallRequest) -> Result<RecallResponse> {
    if req.query.trim().is_empty() {
        return Ok(RecallResponse {
            results: vec![],
            total_count: 0,
            l0_profile: None,
            confidence: None,
            activated_topics: Vec::new(),
            recommended_crystals: Vec::new(),
        prefetch: Vec::new(),
        });
    }
    let encoded = brain.encoder.encode(&req.query);
    let sparse = &encoded.sparse;
    let dense = &encoded.dense;

    let layers = if req.target_layers.is_empty() {
        vec![Layer::L1, Layer::L2]
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
            Layer::L4 => {
                // v0.23.1: L4 从检索管线移除，仅保留存储服务
                // 使用 brain.get_l4_by_session/topic() 直接获取
                Vec::new()
            }
            Layer::L0 | Layer::L5 => Vec::new(),
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
        let store = match brain.redb_store.as_ref() {
            Some(s) => s,
            None => return Ok(RecallResponse {
                results: vec![],
                total_count: 0,
                l0_profile: None,
                confidence: None,
                activated_topics: Vec::new(),
                recommended_crystals: Vec::new(),
                prefetch: Vec::new(),
            }),
        };
        let topics = store.l2_list_topics()?;
        let mut allowed_ids: std::collections::HashSet<String> = std::collections::HashSet::new();
        for t in &topics {
            if t.linked_domain_ids.contains(domain_id) {
                allowed_ids.extend(t.node_ids.iter().cloned());
            }
        }
        if let Some(l1_results) = layers_map.get_mut(&Layer::L1) {
            l1_results.retain(|r| allowed_ids.contains(&r.id));
        }
    }

    // Per-layer RRF 融合（各层独立 rank，保证跨层公平）
    let channels: Vec<Vec<RecallResult>> = layers_map.into_values().collect();
    let mut results = rrf_merge(channels, 60.0, req.max_results);

    // 过滤排除项
    results.retain(|r| {
            // exclude_ids 过滤
            if !req.exclude_ids.is_empty() && req.exclude_ids.contains(&r.id) {
                return false;
            }
            // exclude_topic_ids 过滤
            if !req.exclude_topic_ids.is_empty()
                && let Some(ref label) = r.topic_label
                && req.exclude_topic_ids.iter().any(|t| label.contains(t))
            {
                return false;
            }
            true
        });

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
        results.sort_by(|a, b| {
            b.score
                .partial_cmp(&a.score)
                .unwrap_or(std::cmp::Ordering::Equal)
        });
    }

    // v0.16.0: Improved confidence calculation
    let confidence = if results.is_empty() {
        None
    } else if results.len() == 1 {
        Some(0.4f32 + 0.3 * results[0].score.clamp(0.0, 0.5))
    } else {
        let top_score = results[0].score;
        let consistency = if results.len() >= 2 {
            let gap = (results[0].score - results[1].score).abs();
            (1.0 - gap / (top_score.max(1e-6))).clamp(0.0, 1.0)
        } else {
            0.5
        };
        let count_factor = (results.len() as f32 / req.max_results as f32).min(1.0);
        let score_factor = top_score.clamp(0.0, 1.0);
        Some((0.4 * consistency + 0.3 * count_factor + 0.3 * score_factor).clamp(0.0, 1.0))
    };

    // 构建 prefetch 提示
    let seen: HashSet<String> = results.iter().map(|r| r.id.clone()).collect();
    let prefetch = if !results.is_empty() {
        build_prefetch_hints(brain, &results, &seen, 5)
    } else {
        Vec::new()
    };

    Ok(RecallResponse {
        results,
        total_count,
        l0_profile: None,
        confidence,
        activated_topics: Vec::new(),
        recommended_crystals: Vec::new(),
        prefetch,
    })
}

/// 从 redb 读取 L1 节点详情（v0.25.0: 替代 LMDB 读取）。
fn get_l1_node_from_redb(
    redb: &crate::storage::store::RedbStore,
    node_id: &str,
) -> Option<crate::engram::KnowledgeNode> {
    let rtxn = redb.begin_read().ok()?;
    let table = rtxn.open_table(L1_NODES).ok()?;
    match table.get(node_id) {
        Ok(Some(bytes)) => bincode::deserialize(bytes.value()).ok(),
        _ => None,
    }
}

/// L1 超图检索 — BM25（稀疏）+ 余弦（稠密）双通道 RRF 融合。
/// v0.25.0: 节点详情从 redb 读取（LMDB 已废弃）。
pub(crate) fn search_l1(
    brain: &mut Brain,
    sparse: &HashMap<String, f32>,
    dense: &[f16],
    max: usize,
) -> Result<Vec<RecallResult>> {
    brain.ensure_l1()?;
    let l1 = brain.l1.as_mut().unwrap();

    // ── BM25 通道 ────────────────────────────
    let bm25_hits = l1.search(sparse, max)?;
    let mut bm25_results: Vec<RecallResult> = Vec::with_capacity(bm25_hits.len());
    for (node_id, bm25_score) in &bm25_hits {
        let store = brain.redb_store.as_ref()
            .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;
        if let Some(node) = get_l1_node_from_redb(store, node_id) {
            bm25_results.push(RecallResult {
                layer: Layer::L1,
                id: node_id.clone(),
                text: node
                    .summary
                    .unwrap_or(node.text)
                    .chars()
                    .take(200)
                    .collect(),
                score: *bm25_score * node.memory.importance,
                topic_label: None,
                created_at: node.created_at,
                version: node.version,
                emotion: None,
                domain_id: None,
            });
        }
    }

    // ── Cosine 通道（独立列表） ──────────────────────────
    let has_cosine = !l1.vector_index.is_empty()
        && !dense.is_empty()
        && dense.iter().any(|v| v.to_f32().abs() > 1e-8);

    let mut cos_results: Vec<RecallResult> = Vec::new();
    if has_cosine {
        let cosine_hits = l1.vector_index.cosine_search(dense, max);
        for (node_id, cos_sim) in &cosine_hits {
            let store = brain.redb_store.as_ref()
                .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;
            if let Some(node) = get_l1_node_from_redb(store, node_id) {
                cos_results.push(RecallResult {
                    layer: Layer::L1,
                    id: node_id.clone(),
                    text: node
                        .summary
                        .unwrap_or(node.text)
                        .chars()
                        .take(200)
                        .collect(),
                    score: *cos_sim * node.memory.importance,
                    topic_label: None,
                    created_at: node.created_at,
                    version: node.version,
                    emotion: None,
                    domain_id: None,
                });
            }
        }
    }

    // ── E5 通道（条件可用） ────────────────────────────────
    let mut e5_results: Vec<RecallResult> = Vec::new();
    let e5_available = brain.encoder_e5.is_some()
        && brain.l1.as_ref().map(|l| l.has_e5_index()).unwrap_or(false)
        && !dense.is_empty()
        && dense.iter().any(|v| v.to_f32().abs() > 1e-8);

    #[allow(clippy::collapsible_if)]
    if e5_available {
        if let Some(ref l1) = brain.l1 {
            let e5_hits = l1.vector_index_e5.cosine_search(dense, max);
            for (node_id, cos_sim) in &e5_hits {
                let store = brain.redb_store.as_ref()
                    .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;
                if let Some(node) = get_l1_node_from_redb(store, node_id) {
                    e5_results.push(RecallResult {
                        layer: Layer::L1,
                        id: node_id.clone(),
                        text: node
                            .summary
                            .unwrap_or(node.text)
                            .chars()
                            .take(200)
                            .collect(),
                        score: *cos_sim * node.memory.importance,
                        topic_label: None,
                        created_at: node.created_at,
                        version: node.version,
                        emotion: None,
                        domain_id: None,
                    });
                }
            }
        }
    }

    // ── 三通道可用性检查 ────────────────────────────────
    let channels_available = [
        !bm25_results.is_empty(),
        !cos_results.is_empty(),
        !e5_results.is_empty(),
    ];
    let active_channels = channels_available.iter().filter(|&&c| c).count();

    // ── 多通道 RRF 融合 ────────────────────────────────
    if active_channels >= 2 {
        let rrf_k = dynamic_rrf_k(bm25_results.len() + cos_results.len() + e5_results.len());
        let weights = RrfWeights::default().normalize(&channels_available);
        return Ok(rrf_merge_triple(bm25_results, cos_results, e5_results, rrf_k, max, &weights));
    }

    // ── 单通道（纯 BM25）：排序 + 截断 ────────────────────
    bm25_results.sort_by(|a, b| {
        b.score
            .partial_cmp(&a.score)
            .unwrap_or(std::cmp::Ordering::Equal)
    });
    bm25_results.truncate(max);
    Ok(bm25_results)
}

// ── search_l1_v2: 三通道增强版（BM25 + HNSW + E5）───────────

/// v1.0: L1 三通道检索 — BM25（稀疏）+ HNSW（稠密）+ E5（稠密）
#[allow(dead_code)]
pub(crate) fn search_l1_v2(
    brain: &mut Brain,
    sparse: &HashMap<String, f32>,
    dense: &[f16],
    max: usize,
) -> Result<Vec<RecallResult>> {
    brain.ensure_l1()?;
    let l1 = brain.l1.as_mut().unwrap();

    // ── BM25 通道 ────────────────────────────
    let bm25_hits = l1.search(sparse, max)?;
    let mut bm25_results: Vec<RecallResult> = Vec::with_capacity(bm25_hits.len());
    for (node_id, bm25_score) in &bm25_hits {
        let store = brain.redb_store.as_ref()
            .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;
        if let Some(node) = get_l1_node_from_redb(store, node_id) {
            bm25_results.push(RecallResult {
                layer: Layer::L1,
                id: node_id.clone(),
                text: node
                    .summary
                    .unwrap_or(node.text)
                    .chars()
                    .take(200)
                    .collect(),
                score: *bm25_score * node.memory.importance,
                topic_label: None,
                created_at: node.created_at,
                version: node.version,
                emotion: None,
                domain_id: None,
            });
        }
    }

    // ── Cosine 通道（NgramEncoder dense） ──────────────────
    let has_cosine = !l1.vector_index.is_empty()
        && !dense.is_empty()
        && dense.iter().any(|v| v.to_f32().abs() > 1e-8);

    let mut cos_results: Vec<RecallResult> = Vec::new();
    if has_cosine {
        let cosine_hits = l1.vector_index.cosine_search(dense, max);
        for (node_id, cos_sim) in &cosine_hits {
            let store = brain.redb_store.as_ref()
                .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;
            if let Some(node) = get_l1_node_from_redb(store, node_id) {
                cos_results.push(RecallResult {
                    layer: Layer::L1,
                    id: node_id.clone(),
                    text: node
                        .summary
                        .unwrap_or(node.text)
                        .chars()
                        .take(200)
                        .collect(),
                    score: *cos_sim * node.memory.importance,
                    topic_label: None,
                    created_at: node.created_at,
                    version: node.version,
                    emotion: None,
                    domain_id: None,
                });
            }
        }
    }

    // ── E5 通道（条件可用） ────────────────────────────────
    let mut e5_results: Vec<RecallResult> = Vec::new();
    let e5_available = brain.encoder_e5.is_some()
        && brain.l1.as_ref().map(|l| l.has_e5_index()).unwrap_or(false)
        && !dense.is_empty()
        && dense.iter().any(|v| v.to_f32().abs() > 1e-8);

    if e5_available {
        // 使用 E5 编码器重新编码查询文本以获取 E5 向量
        // 注意：这里 dense 是 NgramEncoder 的向量，E5 通道需要 E5 向量
        // 实际环境中，查询时的 E5 编码应由调用方完成
        // 此处作为 fallback，使用 NgramEncoder 的 dense 向量搜索 E5 索引
        if let Some(ref l1) = brain.l1 {
            let e5_hits = l1.vector_index_e5.cosine_search(dense, max);
            for (node_id, cos_sim) in &e5_hits {
                let store = brain.redb_store.as_ref()
                    .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;
                if let Some(node) = get_l1_node_from_redb(store, node_id) {
                    e5_results.push(RecallResult {
                        layer: Layer::L1,
                        id: node_id.clone(),
                        text: node
                            .summary
                            .unwrap_or(node.text)
                            .chars()
                            .take(200)
                            .collect(),
                        score: *cos_sim * node.memory.importance,
                        topic_label: None,
                        created_at: node.created_at,
                        version: node.version,
                        emotion: None,
                        domain_id: None,
                    });
                }
            }
        }
    }

    // ── 三通道 RRF 融合 ────────────────────────────────────
    let channels_available = [
        !bm25_results.is_empty(),
        !cos_results.is_empty(),
        !e5_results.is_empty(),
    ];
    let active_channels = channels_available.iter().filter(|&&c| c).count();

    if active_channels >= 2 {
        let rrf_k = dynamic_rrf_k(
            bm25_results.len() + cos_results.len() + e5_results.len()
        );
        let weights = RrfWeights::default().normalize(&channels_available);
        return Ok(rrf_merge_triple(bm25_results, cos_results, e5_results, rrf_k, max, &weights));
    }

    // ── 单通道回退 ────────────────────────────────────────
    let mut single = if !bm25_results.is_empty() {
        bm25_results
    } else if !cos_results.is_empty() {
        cos_results
    } else {
        e5_results
    };
    single.sort_by(|a, b| b.score.partial_cmp(&a.score).unwrap_or(std::cmp::Ordering::Equal));
    single.truncate(max);
    Ok(single)
}

/// L1 作用域检索 — 仅搜索指定 node_ids 范围内的节点
/// 用于级联检索的 Stage 1 和 Stage 2
/// v0.25.0: 节点详情从 redb 读取（LMDB 已废弃）。
pub(crate) fn search_l1_scoped(
    brain: &mut Brain,
    sparse: &HashMap<String, f32>,
    dense: &[f16],
    allowed_ids: &std::collections::HashSet<String>,
    max: usize,
) -> Result<Vec<RecallResult>> {
    brain.ensure_l1()?;
    let l1 = brain.l1.as_mut().unwrap();

    // BM25 搜索
    let bm25_hits = l1.search(sparse, max * 2)?;
    let mut results: Vec<RecallResult> = Vec::new();
    for (node_id, bm25_score) in &bm25_hits {
        if !allowed_ids.contains(node_id) {
            continue;
        }
        let store = brain.redb_store.as_ref()
            .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;
        if let Some(node) = get_l1_node_from_redb(store, node_id) {
            results.push(RecallResult {
                layer: Layer::L1,
                id: node_id.clone(),
                text: node
                    .summary
                    .unwrap_or(node.text)
                    .chars()
                    .take(200)
                    .collect(),
                score: *bm25_score * node.memory.importance,
                topic_label: None,
                created_at: node.created_at,
                version: node.version,
                emotion: None,
                domain_id: None,
            });
        }
    }

    // Cosine 搜索（如果有向量索引）
    let has_cosine = !l1.vector_index.is_empty()
        && !dense.is_empty()
        && dense.iter().any(|v| v.to_f32().abs() > 1e-8);

    if has_cosine {
        let cosine_hits = l1.vector_index.cosine_search(dense, max * 2);
        for (node_id, cos_sim) in &cosine_hits {
            if !allowed_ids.contains(node_id) {
                continue;
            }
            let store = brain.redb_store.as_ref()
                .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;
            if let Some(node) = get_l1_node_from_redb(store, node_id) {
                results.push(RecallResult {
                    layer: Layer::L1,
                    id: node_id.clone(),
                    text: node
                        .summary
                        .unwrap_or(node.text)
                        .chars()
                        .take(200)
                        .collect(),
                    score: *cos_sim * node.memory.importance,
                    topic_label: None,
                    created_at: node.created_at,
                    version: node.version,
                    emotion: None,
                    domain_id: None,
                });
            }
        }
    }

    // ── E5 搜索（如果有 E5 索引）──────────────────────────────────
    let e5_available = brain.encoder_e5.is_some()
        && brain.l1.as_ref().map(|l| l.has_e5_index()).unwrap_or(false)
        && !dense.is_empty()
        && dense.iter().any(|v| v.to_f32().abs() > 1e-8);

    if e5_available
        && let Some(ref l1) = brain.l1
    {
        let e5_hits = l1.vector_index_e5.cosine_search(dense, max * 2);
            for (node_id, cos_sim) in &e5_hits {
                if !allowed_ids.contains(node_id) {
                    continue;
                }
                let store = brain.redb_store.as_ref()
                    .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;
                if let Some(node) = get_l1_node_from_redb(store, node_id) {
                    results.push(RecallResult {
                        layer: Layer::L1,
                        id: node_id.clone(),
                        text: node
                            .summary
                            .unwrap_or(node.text)
                            .chars()
                            .take(200)
                            .collect(),
                        score: *cos_sim * node.memory.importance,
                        topic_label: None,
                        created_at: node.created_at,
                        version: node.version,
                        emotion: None,
                        domain_id: None,
                    });
                }
            }
        }

    // 去重并排序
    results.sort_by(|a, b| {
        b.score
            .partial_cmp(&a.score)
            .unwrap_or(std::cmp::Ordering::Equal)
    });
    results.truncate(max);
    Ok(results)
}

/// L1 作用域检索 — 共享事务变体
/// 传入 _txn 供未来使用，当前委托给原始函数
pub(crate) fn search_l1_scoped_with_txn(
    brain: &mut Brain,
    sparse: &HashMap<String, f32>,
    dense: &[f16],
    allowed_ids: &std::collections::HashSet<String>,
    max: usize,
    _txn: &redb::ReadTransaction,
) -> Result<Vec<RecallResult>> {
    search_l1_scoped(brain, sparse, dense, allowed_ids, max)
}

/// L2 话题检索（含向量通道）：Cosine 粗筛 + ngram 重叠双通道 RRF。
pub(crate) fn search_l2(
    brain: &mut Brain,
    sparse: &HashMap<String, f32>,
    dense: &[half::f16],
    max: usize,
) -> Result<Vec<RecallResult>> {
    brain.ensure_l2()?;
    let l2 = brain.l2.as_mut().unwrap();
    let store = brain.redb_store.as_ref()
        .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;

    // ── 通道 1: ngram 倒排索引（SparseIndexV2 BM25）────
    let mut ngram_results: Vec<RecallResult> = Vec::new();

    // 使用 ngram_index 替代线性扫描
    let ngram_hits = l2.search_by_ngram_index(sparse, max * 2)?;
    for (topic_id, bm25_score) in &ngram_hits {
        if let Ok(Some(topic)) = store.l2_get_topic(topic_id) {
            let label = topic.label.clone();
            let association_weight = {
                let domain_weight_sum: f32 = topic.domain_weights.values().sum();
                let node_weight_sum: f32 = topic.node_weights.values().sum();
                1.0 + (domain_weight_sum + node_weight_sum).ln().max(0.0)
            };
            let score = (*bm25_score * 0.1).min(1.0) * association_weight;
            ngram_results.push(RecallResult {
                layer: Layer::L2,
                id: topic_id.clone(),
                text: topic.summary.clone().unwrap_or(label.clone()),
                score,
                topic_label: Some(label),
                created_at: topic.created_at,
                version: topic.version,
                emotion: None,
                domain_id: None,
            });
        }
    }

    // ── 通道 2: 向量 Cosine 粗筛（当 centroid 索引可用时）────
    let has_cosine = !l2.topic_vectors.is_empty()
        && !dense.is_empty()
        && dense.iter().any(|v| v.to_f32().abs() > 1e-8);

    if has_cosine {
        let cos_hits = l2.search_by_vector(dense, max * 2);
        let mut cos_results: Vec<RecallResult> = Vec::new();
        for (topic_id, cos_sim) in &cos_hits {
            if let Ok(Some(topic)) = store.l2_get_topic(topic_id) {
                let label = topic.label.clone();
                cos_results.push(RecallResult {
                    layer: Layer::L2,
                    id: topic_id.clone(),
                    text: topic.summary.unwrap_or(label.clone()),
                    score: *cos_sim,
                    topic_label: Some(label),
                    created_at: topic.created_at,
                    version: topic.version,
                    emotion: None,
                    domain_id: None,
                });
            }
        }

        // 双通道 RRF 融合
        if !cos_results.is_empty() {
            let rrf_k = dynamic_rrf_k(ngram_results.len() + cos_results.len());
            return Ok(rrf_merge(vec![ngram_results, cos_results], rrf_k, max));
        }
    }

    // ── 单通道回退 ────────────────────────────────────
    ngram_results.sort_by(|a, b| {
        b.score
            .partial_cmp(&a.score)
            .unwrap_or(std::cmp::Ordering::Equal)
    });
    ngram_results.truncate(max);
    Ok(ngram_results)
}

/// L2 话题检索 — 共享事务变体
/// 传入 _txn 供未来使用，当前委托给原始函数
pub(crate) fn search_l2_with_txn(
    brain: &mut Brain,
    sparse: &HashMap<String, f32>,
    dense: &[half::f16],
    max: usize,
    _txn: &redb::ReadTransaction,
) -> Result<Vec<RecallResult>> {
    search_l2(brain, sparse, dense, max)
}

/// L3 领域检索 — v0.23.1: Domain Router 两步检索
/// Step 1: route_domains() 找到最相关的 domain
/// Step 2: search_in_domain() 在指定 domain 内搜索
pub(crate) fn search_l3(
    brain: &mut Brain,
    sparse: &HashMap<String, f32>,
    dense: &[f16],
    max: usize,
) -> Result<Vec<RecallResult>> {
    brain.ensure_l3()?;
    let l3 = brain.l3.as_mut().unwrap();
    let store = brain.redb_store.as_ref()
        .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;

    // ── Step 1: Domain Router — 从 redb 加载 domain_meta 做 ngram 匹配──────
    let rtxn = match store.begin_read() {
        Ok(t) => t,
        Err(_) => return Ok(Vec::new()),
    };
    let meta_table = match rtxn.open_table(crate::storage::L3_DOMAIN_META) {
        Ok(t) => t,
        Err(_) => {
            drop(rtxn);
            // Fallback to BM25
            let bm25_hits = l3.search_by_bm25(sparse, max)?;
            let mut results: Vec<RecallResult> = Vec::new();
            for (node_id, bm25_score) in &bm25_hits {
                results.push(RecallResult {
                    layer: Layer::L3,
                    id: node_id.clone(),
                    text: String::new(),
                    score: *bm25_score,
                    topic_label: None,
                    created_at: 0,
                    version: 1,
                    emotion: None,
                    domain_id: None,
                });
            }
            return Ok(results);
        }
    };
    let mut domain_scores: Vec<(String, f32)> = Vec::new();
    for result in meta_table.iter()
        .map_err(|e| MemHopError::Storage(format!("iter L3_DOMAIN_META: {}", e)))?
    {
        let (_key, bytes) = result
            .map_err(|e| MemHopError::Storage(format!("iter entry: {}", e)))?;
        if let Ok(meta) = serde_json::from_slice::<serde_json::Value>(bytes.value()) {
            let domain_id = meta["id"].as_str().unwrap_or("").to_string();
            let domain_name = meta["name"].as_str().unwrap_or("").to_lowercase();
            let overlap: f32 = sparse.keys()
                .filter(|k| domain_name.contains(k.as_str()))
                .count() as f32;
            if overlap > 0.0 {
                domain_scores.push((domain_id, overlap));
            }
        }
    }
    drop(meta_table);
    domain_scores.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
    domain_scores.truncate(3);

    let routed_domains: Vec<String> = domain_scores.into_iter().map(|(id, _)| id).collect();

    // ── Step 2: Domain-scoped Search ────────────────────────────
    if !routed_domains.is_empty() {
        let hits = l3.search_in_domain(&rtxn, store, sparse, dense, &routed_domains, max)?;
        let mut results: Vec<RecallResult> = Vec::new();
        for (node_id, score, domain_id) in hits {
            results.push(RecallResult {
                layer: Layer::L3,
                id: node_id,
                text: String::new(),
                score,
                topic_label: None,
                created_at: 0,
                version: 1,
                emotion: None,
                domain_id: Some(domain_id),
            });
        }
        return Ok(results);
    }
    drop(rtxn);

    // ── Fallback: 全量 BM25 搜索 ─────────────────────────────
    let bm25_hits = l3.search_by_bm25(sparse, max)?;
    let mut results: Vec<RecallResult> = Vec::new();
    for (node_id, bm25_score) in &bm25_hits {
        results.push(RecallResult {
            layer: Layer::L3,
            id: node_id.clone(),
            text: String::new(),
            score: *bm25_score,
            topic_label: None,
            created_at: 0,
            version: 1,
            emotion: None,
            domain_id: None,
        });
    }
    Ok(results)
}

/// L3 领域检索 — 共享事务变体
/// 传入 _txn 供未来使用，当前委托给原始函数
pub(crate) fn search_l3_with_txn(
    brain: &mut Brain,
    sparse: &HashMap<String, f32>,
    dense: &[f16],
    max: usize,
    _txn: &redb::ReadTransaction,
) -> Result<Vec<RecallResult>> {
    search_l3(brain, sparse, dense, max)
}

/// v0.22.0: L4 原文检索 — 仅 ngram overlap（HNSW 已移除）。
/// v0.23.1: 已从检索管线移除，保留代码供参考。
#[allow(dead_code)]
pub(crate) fn search_l4(
    _brain: &mut Brain,
    _sparse: &HashMap<String, f32>,
    _dense: &[f16],
    _max: usize,
) -> Result<Vec<RecallResult>> {
    // L4 已从检索管线移除，返回空
    Ok(Vec::new())
}

/// 获取指定 Topic 的 node_ids 集合（用于级联路由过滤）
#[allow(dead_code)]
fn get_topic_node_ids(brain: &mut Brain, topic_id: &str) -> Result<std::collections::HashSet<String>> {
    let store = brain.redb_store.as_ref()
        .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;
    match store.l2_get_topic(topic_id)? {
        Some(topic) => Ok(topic.node_ids.into_iter().collect()),
        None => Ok(std::collections::HashSet::new()),
    }
}

/// v0.18.0: 跨层结果验证
#[allow(dead_code)]
pub(crate) fn cross_layer_validation(results: &mut [RecallResult], _brain: &Brain) {
    if results.is_empty() {
        return;
    }

    let mut layer_counts: HashMap<String, usize> = HashMap::new();
    let mut layer_ranks: HashMap<String, Vec<usize>> = HashMap::new();

    for (rank, result) in results.iter().enumerate() {
        let id = result.id.clone();
        *layer_counts.entry(id.clone()).or_insert(0) += 1;
        layer_ranks.entry(id.clone()).or_default().push(rank);
    }

    for result in results.iter_mut() {
        let id = &result.id;
        let count = layer_counts.get(id).copied().unwrap_or(1);
        let ranks = layer_ranks.get(id).cloned().unwrap_or_default();

        let layer_weight = (count as f32).ln().max(0.0);
        let rank_variance = if ranks.len() > 1 {
            let mean_rank: f32 = ranks.iter().map(|r| *r as f32).sum::<f32>() / ranks.len() as f32;
            let variance: f32 = ranks
                .iter()
                .map(|r| {
                    let diff = *r as f32 - mean_rank;
                    diff * diff
                })
                .sum::<f32>()
                / ranks.len() as f32;
            variance.sqrt() / (ranks.len() as f32).max(1.0)
        } else {
            0.0
        };
        let rank_consistency = 1.0 / (1.0 + rank_variance);
        let consistency_score = 1.0 + layer_weight * rank_consistency;
        result.score *= consistency_score;
    }
}
