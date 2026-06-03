//! query_engine — 按层检索引擎。
//! L1 → L2 → L4 级联检索 + per-layer RRF 融合。

use std::collections::HashMap;
use crate::error::Result;
use crate::types::{RecallRequest, RecallResponse, RecallResult, Layer};
use crate::brain::Brain;
use crate::encoder::Encoder;

pub(crate) fn execute(brain: &Brain, req: &RecallRequest) -> Result<RecallResponse> {
    let encoded = brain.encoder.encode(&req.query);
    let sparse = &encoded.sparse;

    let layers = if req.target_layers.is_empty() {
        vec![Layer::L1, Layer::L2, Layer::L4]
    } else {
        req.target_layers.clone()
    };

    // 收集各层结果并分别排名
    let mut layers_map: HashMap<Layer, Vec<RecallResult>> = HashMap::new();
    for layer in &layers {
        let layer_results = match layer {
            Layer::L1 => search_l1(brain, sparse, req.max_results)?,
            Layer::L2 => search_l2(brain, sparse, req.max_results)?,
            Layer::L3 => search_l3(brain, sparse, req.max_results)?,
            Layer::L4 => search_l4(brain, sparse, req.max_results)?,
        };
        layers_map.entry(*layer).or_default().extend(layer_results);
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

    let results: Vec<RecallResult> = ranked.into_iter()
        .filter_map(|(id, _)| id_to_result.remove(&id))
        .collect();

    let total_count = results.len();
    Ok(RecallResponse { results, total_count })
}

/// L1 超图检索 — BM25 搜索 + 超边扩散
fn search_l1(brain: &Brain, sparse: &HashMap<String, f32>, max: usize) -> Result<Vec<RecallResult>> {
    let hits = brain.l1.search(sparse, max)?;
    let txn = brain.l1_env.env.read_txn()
        .map_err(|e| crate::error::MemHopError::Storage(e.to_string()))?;
    let mut results = Vec::new();
    for (node_id, bm25_score) in hits {
        if let Ok(Some(node)) = brain.l1.get_node(&txn, &brain.l1_env, &node_id) {
            results.push(RecallResult {
                layer: Layer::L1,
                id: node_id,
                text: node.summary.unwrap_or(node.text).chars().take(200).collect(),
                score: bm25_score,
                topic_label: None,
                created_at: node.created_at,
                version: node.version,
            });
        }
    }
    Ok(results)
}

/// L2 话题检索 — ngram 重叠匹配 topic label + keywords
fn search_l2(brain: &Brain, sparse: &HashMap<String, f32>, max: usize) -> Result<Vec<RecallResult>> {
    let txn = brain.l2_env.env.read_txn()
        .map_err(|e| crate::error::MemHopError::Storage(e.to_string()))?;
    let mut results = Vec::new();

    if let Ok(iter) = brain.l2_env.topics.iter(&txn) {
        for item in iter {
            if let Ok((key, bytes)) = item {
                // 只处理 topic: 前缀的 key（跳过 label: 和 topic_edge: 前缀）
                if !key.starts_with("topic:") { continue; }
                if let Ok(topic) = bincode::deserialize::<crate::engram::Topic>(bytes) {
                    let mut overlap = 0.0f32;
                    let label_lower = topic.label.to_lowercase();
                    for (ngram, _) in sparse {
                        if label_lower.contains(ngram) { overlap += 1.0; }
                        for kw in &topic.keywords {
                            if kw.to_lowercase().contains(ngram) { overlap += 0.5; }
                        }
                    }
                    let label = topic.label.clone();
                    let score = if overlap > 0.0 { (overlap * 0.1).min(1.0) } else { 0.05 };

                    results.push(RecallResult {
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

    results.sort_by(|a, b| b.score.partial_cmp(&a.score).unwrap_or(std::cmp::Ordering::Equal));
    results.truncate(max);
    Ok(results)
}

/// L3 领域检索 — ngram 重叠匹配（依赖 sparse 数据）
fn search_l3(brain: &Brain, sparse: &HashMap<String, f32>, max: usize) -> Result<Vec<RecallResult>> {
    let txn = brain.l3_env.env.read_txn()
        .map_err(|e| crate::error::MemHopError::Storage(e.to_string()))?;
    let mut results = Vec::new();

    if let Ok(iter) = brain.l3_env.domain_nodes.iter(&txn) {
        for item in iter {
            if let Ok((_key, bytes)) = item {
                if let Ok(node) = bincode::deserialize::<crate::engram::KnowledgeNode>(bytes) {
                    let overlap: f32 = node.sparse.keys()
                        .filter(|k| sparse.contains_key(*k))
                        .count() as f32;
                    if overlap > 0.0 {
                        results.push(RecallResult {
                            layer: Layer::L3,
                            id: node.id,
                            text: node.summary.unwrap_or(node.text).chars().take(200).collect(),
                            score: (overlap * 0.2).min(1.0),
                            topic_label: None,
                            created_at: node.created_at,
                            version: node.version,
                        });
                    }
                }
            }
        }
    }

    results.sort_by(|a, b| b.score.partial_cmp(&a.score).unwrap_or(std::cmp::Ordering::Equal));
    results.truncate(max);
    Ok(results)
}

/// L4 原文检索 — ngram 重叠匹配对话原文
fn search_l4(brain: &Brain, sparse: &HashMap<String, f32>, max: usize) -> Result<Vec<RecallResult>> {
    let txn = brain.l4_env.env.read_txn()
        .map_err(|e| crate::error::MemHopError::Storage(e.to_string()))?;
    let mut results = Vec::new();

    if let Ok(iter) = brain.l4_env.docs.iter(&txn) {
        for item in iter {
            if let Ok((_key, bytes)) = item {
                if let Ok(doc) = bincode::deserialize::<crate::engram::RawDocument>(bytes) {
                    let text_lower = doc.text.to_lowercase();
                    let overlap: f32 = sparse.keys()
                        .filter(|k| text_lower.contains(*k))
                        .count() as f32;
                    if overlap > 0.0 {
                        results.push(RecallResult {
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
    }

    results.sort_by(|a, b| b.score.partial_cmp(&a.score).unwrap_or(std::cmp::Ordering::Equal));
    results.truncate(max);
    Ok(results)
}
