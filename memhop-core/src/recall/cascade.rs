//! v0.23.1: CascadingRecall — 仿人脑激活优先级联检索
//!
//! 级联管道：
//! Stage 1: 激活 L2 搜索 — 当前 session 激活的 topic 范围
//! Stage 2: 扩展 L2 搜索 — 全量 L2 topics
//! Stage 3: L3 Domain Router — 领域级检索
//! Stage 4: L1 全局兜底 — 全量 L1 搜索

use crate::brain::Brain;
use crate::error::Result;
use crate::query_engine;
use crate::types::{RecallRequest, RecallResponse, RecallResult};
use std::collections::HashSet;

/// 级联检索入口
pub fn cascade_recall(brain: &mut Brain, req: &RecallRequest) -> Result<RecallResponse> {
    let encoded = brain.encoder.encode(&req.query);
    let sparse = &encoded.sparse;
    let dense = &encoded.dense;

    let mut all_results: Vec<RecallResult> = Vec::new();
    let max_results = req.max_results;

    // ── Stage 1: 激活 L2 搜索 ────────────────────────────────
    if let Some(ref session_id) = req.session_id {
        let activated_node_ids = get_activated_node_ids(brain, session_id);
        if !activated_node_ids.is_empty() {
            let stage1_results = query_engine::search_l1_scoped(
                brain,
                sparse,
                dense,
                &activated_node_ids,
                max_results,
            )?;
            all_results.extend(stage1_results);

            // Early termination: 如果 Stage 1 结果足够且质量好
            if all_results.len() >= max_results && top_score(&all_results) > 0.6 {
                return build_response(all_results, max_results, req);
            }
        }
    }

    // ── Stage 2: 扩展 L2 搜索 ────────────────────────────────
    let stage2_results = query_engine::search_l2(brain, sparse, dense, max_results)?;
    let stage2_node_ids: HashSet<String> = stage2_results
        .iter()
        .filter_map(|r| {
            // 从 L2 topic 获取 node_ids
            get_topic_node_ids_safe(brain, &r.id).ok()
        })
        .flatten()
        .collect();

    // 在 Stage 2 的 node_ids 范围内搜索 L1
    if !stage2_node_ids.is_empty() {
        let scoped_results = query_engine::search_l1_scoped(
            brain,
            sparse,
            dense,
            &stage2_node_ids,
            max_results,
        )?;
        // 去重添加
        let existing_ids: HashSet<String> = all_results.iter().map(|r| r.id.clone()).collect();
        for r in scoped_results {
            if !existing_ids.contains(&r.id) {
                all_results.push(r);
            }
        }

        // Early termination
        if all_results.len() >= max_results && top_score(&all_results) > 0.5 {
            return build_response(all_results, max_results, req);
        }
    }

    // ── Stage 3: L3 Domain Router 搜索（通过 L2→L3 link）──────────────
    // 从 Stage 2 的 L2 结果中获取 linked_domain_ids
    let stage2_domain_ids: HashSet<String> = stage2_results
        .iter()
        .filter_map(|r| {
            // 从 L2 topic 获取 linked_domain_ids
            get_topic_linked_domains_safe(brain, &r.id).ok()
        })
        .flatten()
        .collect();

    let stage3_results = if !stage2_domain_ids.is_empty() {
        // 在关联的 L3 domain 中搜索
        search_l3_in_domains(brain, sparse, dense, &stage2_domain_ids, max_results)?
    } else {
        // Fallback: 全局 L3 搜索
        query_engine::search_l3(brain, sparse, dense, max_results)?
    };
    let existing_ids: HashSet<String> = all_results.iter().map(|r| r.id.clone()).collect();
    for r in stage3_results {
        if !existing_ids.contains(&r.id) {
            all_results.push(r);
        }
    }

    // Early termination
    if all_results.len() >= max_results && top_score(&all_results) > 0.4 {
        return build_response(all_results, max_results, req);
    }

    // ── Stage 4: L1 全局兜底 ─────────────────────────────────
    let stage4_results = query_engine::search_l1(brain, sparse, dense, max_results)?;
    let existing_ids: HashSet<String> = all_results.iter().map(|r| r.id.clone()).collect();
    for r in stage4_results {
        if !existing_ids.contains(&r.id) {
            all_results.push(r);
        }
    }

    // ── Post-process: 更新激活分数 ───────────────────────────────
    update_activation_scores(brain, &all_results);

    build_response(all_results, max_results, req)
}

/// 获取 session 激活的 topic 的 node_ids
fn get_activated_node_ids(brain: &mut Brain, session_id: &str) -> HashSet<String> {
    let mut node_ids = HashSet::new();

    if let Ok(()) = brain.ensure_l2() {
        let l2 = brain.l2.as_ref().unwrap();
        let l2_env = brain.l2_env.as_ref().unwrap();

        if let Ok(txn) = l2_env.env.read_txn() {
            let active_topic_ids = brain.session_mgr.get_active_topic_ids(session_id);
            for tid in active_topic_ids {
                if let Ok(Some(topic)) = l2.get_topic_by_id(&txn, l2_env, &tid) {
                    node_ids.extend(topic.node_ids);
                }
            }
        }
    }

    node_ids
}

/// 安全获取 topic 的 node_ids
fn get_topic_node_ids_safe(brain: &mut Brain, topic_id: &str) -> Result<Vec<String>> {
    brain.ensure_l2()?;
    let l2 = brain.l2.as_ref().unwrap();
    let l2_env = brain.l2_env.as_ref().unwrap();
    let txn = l2_env
        .env
        .read_txn()
        ?;

    match l2.get_topic_by_id(&txn, l2_env, topic_id)? {
        Some(topic) => Ok(topic.node_ids),
        None => Ok(Vec::new()),
    }
}

/// 安全获取 topic 的 linked_domain_ids
fn get_topic_linked_domains_safe(brain: &mut Brain, topic_id: &str) -> Result<Vec<String>> {
    brain.ensure_l2()?;
    let l2 = brain.l2.as_ref().unwrap();
    let l2_env = brain.l2_env.as_ref().unwrap();
    let txn = l2_env
        .env
        .read_txn()
        ?;

    match l2.get_topic_by_id(&txn, l2_env, topic_id)? {
        Some(topic) => Ok(topic.linked_domain_ids),
        None => Ok(Vec::new()),
    }
}

/// 在指定的 L3 domain 中搜索
fn search_l3_in_domains(
    brain: &mut Brain,
    sparse: &std::collections::HashMap<String, f32>,
    dense: &[half::f16],
    domain_ids: &HashSet<String>,
    max: usize,
) -> Result<Vec<RecallResult>> {
    brain.ensure_l3()?;
    let l3 = brain.l3.as_mut().unwrap();
    let l3_env = brain.l3_env.as_ref().unwrap();
    let txn = l3_env
        .env
        .read_txn()
        ?;

    let domain_id_vec: Vec<String> = domain_ids.iter().cloned().collect();
    let hits = l3.search_in_domain(&txn, l3_env, sparse, dense, &domain_id_vec, max);

    let mut results: Vec<RecallResult> = Vec::new();
    for (node_id, score, _domain_id) in hits {
        // 从 LMDB 获取节点详情
        let key_prefix = format!("node:{}:", _domain_id);
        if let Ok(iter) = l3_env.domain_nodes.iter(&txn) {
            for item in iter {
                if let Ok((key, bytes)) = item
                    && key.starts_with(&key_prefix)
                    && key.ends_with(&format!(":{}", node_id))
                    && let Ok(node) = bincode::deserialize::<crate::engram::KnowledgeNode>(bytes)
                {
                    results.push(RecallResult {
                        layer: crate::types::Layer::L3,
                        id: node_id.clone(),
                        text: node
                            .summary
                            .unwrap_or(node.text)
                            .chars()
                            .take(200)
                            .collect(),
                        score,
                        topic_label: None,
                        created_at: node.created_at,
                        version: node.version,
                        emotion: None,
                    });
                    break;
                }
            }
        }
    }
    Ok(results)
}

/// 获取结果中的最高分
fn top_score(results: &[RecallResult]) -> f32 {
    results
        .iter()
        .map(|r| r.score)
        .max_by(|a, b| a.partial_cmp(b).unwrap_or(std::cmp::Ordering::Equal))
        .unwrap_or(0.0)
}

/// 更新激活分数（recall 后应用 recall_bonus）
fn update_activation_scores(brain: &mut Brain, results: &[RecallResult]) {
    // 如果 ActivationManager 未初始化，跳过
    if brain.activation.is_none() {
        return;
    }

    brain.ensure_l1().ok();
    if brain.l1_env.is_none() || brain.l1.is_none() {
        return;
    }

    let l1_env = brain.l1_env.as_ref().unwrap();
    let l1 = brain.l1.as_mut().unwrap();
    let activation = brain.activation.as_ref().unwrap();

    let mut wtxn = match l1_env.env.write_txn() {
        Ok(txn) => txn,
        Err(_) => return,
    };

    for result in results {
        // 仅处理 L1 节点
        if result.layer != crate::types::Layer::L1 {
            continue;
        }

        if let Ok(Some(bytes)) = l1_env.nodes.get(&wtxn, &result.id)
            && let Ok(mut node) = bincode::deserialize::<crate::engram::KnowledgeNode>(bytes) {
            // 应用 recall_bonus
            let new_score = activation.apply_recall_bonus(node.activation_score);
            node.activation_score = new_score;
            node.memory_state = activation.should_transition(new_score, node.importance);
            node.updated_at = chrono::Utc::now().timestamp_millis();

            // 写回
            if let Ok(new_bytes) = bincode::serialize(&node) {
                l1_env.nodes.put(&mut wtxn, &result.id, &new_bytes).ok();
                l1.vector_index.update(&result.id, &node.vector);
            }
        }
    }

    wtxn.commit().ok();
}

/// 构建最终响应（带 RRF 融合和去重）
fn build_response(
    mut results: Vec<RecallResult>,
    max_results: usize,
    req: &RecallRequest,
) -> Result<RecallResponse> {
    // 按 score 降序排序
    results.sort_by(|a, b| {
        b.score
            .partial_cmp(&a.score)
            .unwrap_or(std::cmp::Ordering::Equal)
    });

    // 去重（保留第一次出现的）
    let mut seen = HashSet::new();
    results.retain(|r| seen.insert(r.id.clone()));

    // 应用 exclude_ids 过滤
    if !req.exclude_ids.is_empty() {
        results.retain(|r| !req.exclude_ids.contains(&r.id));
    }

    // 应用 exclude_topic_ids 过滤
    if !req.exclude_topic_ids.is_empty() {
        results.retain(|r| {
            if let Some(ref label) = r.topic_label {
                !req.exclude_topic_ids.iter().any(|t| label.contains(t))
            } else {
                true
            }
        });
    }

    // 截断到 max_results
    results.truncate(max_results);

    let total_count = results.len();

    // 计算 confidence
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

    Ok(RecallResponse {
        results,
        total_count,
        l0_profile: None,
        confidence,
        activated_topics: Vec::new(),
        recommended_crystals: Vec::new(),
    })
}
