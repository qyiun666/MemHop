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
use crate::engram::Hyperedge;
use crate::storage::{L1_HYPEREDGES, L1_NODE_TO_HYPEREDGES};
use crate::types::{PrefetchHint, PrefetchReason, RecallRequest, RecallResponse, RecallResult};
use redb::{ReadTransaction, ReadableTable};
use std::collections::HashSet;

/// 级联检索入口
pub fn cascade_recall(brain: &mut Brain, req: &RecallRequest) -> Result<RecallResponse> {
    let encoded = brain.encoder.encode(&req.query);
    let sparse = &encoded.sparse;
    let dense = &encoded.dense;

    let mut all_results: Vec<RecallResult> = Vec::new();
    let max_results = req.max_results;

    // 在入口统一打开读事务（各 stage 复用此 txn）
    // 注意：search_* 函数内部仍需独立事务（如 update_activation_scores 需要写事务）
    // 未来 query_engine 函数可接受外部 txn 参数进一步优化（Phase 1 三通道重构时清理）
    let shared_txn = brain.redb_store.as_ref().and_then(|s| s.begin_read().ok());

    // ── Stage 1: 激活 L2 搜索 ────────────────────────────────
    if let Some(ref session_id) = req.session_id {
        let activated_node_ids = get_activated_node_ids(brain, session_id);
        if !activated_node_ids.is_empty() {
            let stage1_results = if let Some(ref txn) = shared_txn {
                query_engine::search_l1_scoped_with_txn(
                    brain,
                    sparse,
                    dense,
                    &activated_node_ids,
                    max_results,
                    txn,
                )?
            } else {
                query_engine::search_l1_scoped(
                    brain,
                    sparse,
                    dense,
                    &activated_node_ids,
                    max_results,
                )?
            };
            all_results.extend(stage1_results);

            // Early termination: 如果 Stage 1 结果足够且质量好
            if all_results.len() >= max_results && top_score(&all_results) > 0.6 {
                return build_response(brain, all_results, max_results, req);
            }
        }
    }

    // ── Stage 2: 扩展 L2 搜索 ────────────────────────────────
    let stage2_results = if let Some(ref txn) = shared_txn {
        query_engine::search_l2_with_txn(brain, sparse, dense, max_results, txn)?
    } else {
        query_engine::search_l2(brain, sparse, dense, max_results)?
    };
    // 收集 Stage 2 topic IDs（用于双向链接更新）
    let stage2_topic_ids: HashSet<String> = stage2_results.iter().map(|r| r.id.clone()).collect();
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
        let scoped_results = if let Some(ref txn) = shared_txn {
            query_engine::search_l1_scoped_with_txn(
                brain,
                sparse,
                dense,
                &stage2_node_ids,
                max_results,
                txn,
            )?
        } else {
            query_engine::search_l1_scoped(
                brain,
                sparse,
                dense,
                &stage2_node_ids,
                max_results,
            )?
        };
        // 去重添加
        let existing_ids: HashSet<String> = all_results.iter().map(|r| r.id.clone()).collect();
        for r in scoped_results {
            if !existing_ids.contains(&r.id) {
                all_results.push(r);
            }
        }

        // Early termination
        if all_results.len() >= max_results && top_score(&all_results) > 0.5 {
            let response = build_response(brain, all_results, max_results, req)?;
            // ── Crystal 使用反馈 ──
            for crystal in &response.recommended_crystals {
                let _ = brain.update_crystal_usage(&crystal.id, true);
            }
            return Ok(response);
        }
    }

    // ── Stage 3: L3 Domain Router 搜索 ──────────────
    // 优先级 1: active_l3_domains（显式指定）
    // 优先级 2: Stage 2 的 linked_domain_ids（L2→L3 正向链接）
    // 优先级 3: Domain Router ngram 匹配（search_l3 fallback）

    let stage3_results = if let Some(ref active_domains) = req.active_l3_domains {
        if active_domains.is_empty() {
            // 显式空列表 → 跳过 L3
            Vec::new()
        } else {
            // 在指定 L3 域中搜索
            let domain_set: HashSet<String> = active_domains.iter().cloned().collect();
            let txn = shared_txn.as_ref().ok_or_else(|| {
                crate::error::MemHopError::Storage("redb txn unavailable for L3 search".into())
            })?;
            search_l3_in_domains(brain, sparse, dense, &domain_set, max_results, txn)?
        }
    } else {
        // 未指定 active_l3 → 使用 Stage 2 的 linked_domain_ids
        let stage2_domain_ids: HashSet<String> = stage2_results
            .iter()
            .filter_map(|r| {
                // 从 L2 topic 获取 linked_domain_ids
                get_topic_linked_domains_safe(brain, &r.id).ok()
            })
            .flatten()
            .collect();

        if !stage2_domain_ids.is_empty() {
            let txn = shared_txn.as_ref().ok_or_else(|| {
                crate::error::MemHopError::Storage("redb txn unavailable for L3 search".into())
            })?;
            search_l3_in_domains(brain, sparse, dense, &stage2_domain_ids, max_results, txn)?
        } else {
            // Fallback: 全局 L3 搜索
            if let Some(ref txn) = shared_txn {
                query_engine::search_l3_with_txn(brain, sparse, dense, max_results, txn)?
            } else {
                query_engine::search_l3(brain, sparse, dense, max_results)?
            }
        }
    };
    let existing_ids: HashSet<String> = all_results.iter().map(|r| r.id.clone()).collect();
    for r in stage3_results {
        if !existing_ids.contains(&r.id) {
            all_results.push(r);
        }
    }

    // 收集 Stage 3 domain IDs（用于双向链接更新）
    let stage3_domain_ids: HashSet<String> = all_results.iter()
        .filter(|r| r.layer == crate::types::Layer::L3)
        .filter_map(|r| r.domain_id.clone())
        .collect();

    // Early termination
    if all_results.len() >= max_results && top_score(&all_results) > 0.4 {
        let response = build_response(brain, all_results, max_results, req)?;
        // ── Crystal 使用反馈 ──
        for crystal in &response.recommended_crystals {
            let _ = brain.update_crystal_usage(&crystal.id, true);
        }
        return Ok(response);
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

    // v1.0: 标记命中的 L1 节点为 labile（再巩固准备）
    if let Some(ref mut rm) = brain.reconsolidation {
        for r in &all_results {
            if r.layer == crate::types::Layer::L1 {
                rm.mark_labile(&r.id, 6);
            }
        }
    }

    // ── L2↔L3 双向链接更新 ──────────────────────────────
    if !stage2_topic_ids.is_empty() && !stage3_domain_ids.is_empty() {
        update_l2_l3_bidirectional_links(brain, &stage2_topic_ids, &stage3_domain_ids);
    }

    let response = build_response(brain, all_results, max_results, req)?;

    // ── Crystal 使用反馈 ──
    for crystal in &response.recommended_crystals {
        let _ = brain.update_crystal_usage(&crystal.id, true);
    }

    Ok(response)
}

/// 获取 session 激活的 topic 的 node_ids
fn get_activated_node_ids(brain: &mut Brain, session_id: &str) -> HashSet<String> {
    let mut node_ids = HashSet::new();

    if brain.ensure_l2().is_err() {
        return node_ids;
    }
    let active_topic_ids = brain.session_mgr.get_active_topic_ids(session_id);

    let store = match brain.redb_store.as_ref() {
        Some(s) => s,
        None => return HashSet::new(),
    };
    for tid in active_topic_ids {
        if let Ok(Some(topic)) = store.l2_get_topic(&tid) {
            node_ids.extend(topic.node_ids);
        }
    }

    node_ids
}

/// 安全获取 topic 的 node_ids
fn get_topic_node_ids_safe(brain: &mut Brain, topic_id: &str) -> Result<Vec<String>> {
    let store = brain.redb_store.as_ref()
        .ok_or_else(|| crate::error::MemHopError::Storage("redb not available".into()))?;

    match store.l2_get_topic(topic_id)? {
        Some(topic) => Ok(topic.node_ids),
        None => Ok(Vec::new()),
    }
}

/// 安全获取 topic 的 linked_domain_ids
fn get_topic_linked_domains_safe(brain: &mut Brain, topic_id: &str) -> Result<Vec<String>> {
    let store = brain.redb_store.as_ref()
        .ok_or_else(|| crate::error::MemHopError::Storage("redb not available".into()))?;

    match store.l2_get_topic(topic_id)? {
        Some(topic) => Ok(topic.linked_domain_ids),
        None => Ok(Vec::new()),
    }
}

/// 在指定的 L3 domain 中搜索（使用外部传入的读事务）
fn search_l3_in_domains(
    brain: &mut Brain,
    sparse: &std::collections::HashMap<String, f32>,
    dense: &[half::f16],
    domain_ids: &HashSet<String>,
    max: usize,
    txn: &ReadTransaction,
) -> Result<Vec<RecallResult>> {
    brain.ensure_l3()?;
    let l3 = brain.l3.as_mut().unwrap();
    let store = brain.redb_store.as_ref()
        .ok_or_else(|| crate::error::MemHopError::Storage("redb not available".into()))?;
    // 使用外部传入的 txn，不再内部 begin_read

    let domain_id_vec: Vec<String> = domain_ids.iter().cloned().collect();
    let hits = l3.search_in_domain(txn, store, sparse, dense, &domain_id_vec, max)?;

    let mut results: Vec<RecallResult> = Vec::new();
    for (node_id, score, domain_id) in hits {
        results.push(RecallResult {
            layer: crate::types::Layer::L3,
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

fn update_activation_scores(brain: &mut Brain, results: &[RecallResult]) {
    // 如果 ActivationManager 未初始化，跳过
    if brain.activation.is_none() {
        return;
    }

    brain.ensure_l1().ok();
    if brain.l1.is_none() {
        return;
    }

    let l1 = brain.l1.as_mut().unwrap();
    let activation = brain.activation.as_ref().unwrap();
    let store = match brain.redb_store.as_ref() {
        Some(s) => s,
        None => return,
    };

    let wtxn = match store.begin_write() {
        Ok(txn) => txn,
        Err(_) => return,
    };

    for result in results {
        // 仅处理 L1 节点
        if result.layer != crate::types::Layer::L1 {
            continue;
        }

        // 从 write txn 中读取节点
        let node_result: Option<crate::engram::KnowledgeNode> = (|| {
            let table = wtxn.open_table(crate::storage::L1_NODES).ok()?;
            let guard = table.get(result.id.as_str()).ok()??;
            bincode::deserialize(guard.value()).ok()
        })();

        if let Some(mut node) = node_result {
            // 应用 recall_bonus
            let new_score = activation.apply_recall_bonus(node.memory.activation_score);
            node.memory.activation_score = new_score;
            node.memory.memory_state = activation.should_transition(new_score, node.memory.importance);
            node.updated_at = chrono::Utc::now().timestamp_millis();

            // 写回
            if let Ok(new_bytes) = bincode::serialize(&node) {
                if let Ok(mut table) = wtxn.open_table(crate::storage::L1_NODES) {
                    table.insert(result.id.as_str(), new_bytes.as_slice()).ok();
                }
                l1.vector_index.update(&result.id, &node.vector);
            }
        }
    }

    wtxn.commit().ok();
}

/// 构建预测性记忆提示 — 从 top-N 结果的 L1 超边扩散
pub(crate) fn build_prefetch_hints(
    brain: &Brain,
    top_results: &[RecallResult],
    existing_ids: &HashSet<String>,
    max_prefetch: usize,
) -> Vec<PrefetchHint> {
    let store = match brain.redb_store.as_ref() {
        Some(s) => s,
        None => return Vec::new(),
    };

    let rtxn = match store.begin_read() {
        Ok(t) => t,
        Err(_) => return Vec::new(),
    };

    let he_table = match rtxn.open_table(L1_HYPEREDGES) {
        Ok(t) => t,
        Err(_) => return Vec::new(),
    };

    let mut hints: Vec<PrefetchHint> = Vec::new();
    let mut seen_for_prefetch: HashSet<String> = existing_ids.clone();

    // 取 top-N 结果做 BFS depth=1 扩散
    for result in top_results.iter().take(5) {
        // 只对 L1 节点做超边扩散
        if result.layer != crate::types::Layer::L1 {
            continue;
        }

        // 查该节点所属的超边 ID 列表
        let node_to_he = match rtxn.open_table(L1_NODE_TO_HYPEREDGES) {
            Ok(t) => t,
            Err(_) => continue,
        };

        let he_ids: Vec<String> = match node_to_he.get(result.id.as_str()) {
            Ok(Some(bytes)) => bincode::deserialize(bytes.value()).unwrap_or_default(),
            _ => continue,
        };

        // 对每个超边，获取 neighbor nodes
        for he_id in &he_ids {
            let he_bytes = match he_table.get(he_id.as_str()) {
                Ok(Some(b)) => b,
                _ => continue,
            };

            let he: Hyperedge = match bincode::deserialize(he_bytes.value()) {
                Ok(h) => h,
                _ => continue,
            };

            for neighbor_id in &he.node_ids {
                if seen_for_prefetch.contains(neighbor_id) {
                    continue;
                }
                if hints.len() >= max_prefetch {
                    break;
                }

                seen_for_prefetch.insert(neighbor_id.clone());
                hints.push(PrefetchHint {
                    node_id: neighbor_id.clone(),
                    text: String::new(),  // 懒加载，协议层按需填充
                    prediction_score: result.score * he.weight * 0.5,
                    reason: PrefetchReason::HyperedgeSpread,
                });
            }
            if hints.len() >= max_prefetch {
                break;
            }
        }
        if hints.len() >= max_prefetch {
            break;
        }
    }

    hints
}

/// 构建最终响应（带 RRF 融合和去重）
fn build_response(
    brain: &Brain,
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

    // 构建 prefetch 提示
    let prefetch = build_prefetch_hints(brain, &results, &seen, 5);

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

/// 更新 L2↔L3 双向链接 — 命中 L3 后自动关联 topic 和 domain
fn update_l2_l3_bidirectional_links(
    brain: &mut Brain,
    topic_ids: &HashSet<String>,
    domain_ids: &HashSet<String>,
) {
    if topic_ids.is_empty() || domain_ids.is_empty() {
        return;
    }

    let store = match brain.redb_store.as_ref() {
        Some(s) => s,
        None => return,
    };

    // a. 更新 Topic.linked_domain_ids（L2 → L3 正向）
    for topic_id in topic_ids {
        let mut topic = match store.l2_get_topic(topic_id) {
            Ok(Some(t)) => t,
            _ => continue,
        };
        let mut modified = false;
        for domain_id in domain_ids {
            if !topic.linked_domain_ids.contains(domain_id) {
                topic.linked_domain_ids.push(domain_id.clone());
                modified = true;
            }
            *topic.domain_weights.entry(domain_id.clone()).or_insert(0.0) += 0.1;
        }
        if modified {
            topic.updated_at = chrono::Utc::now().timestamp_millis();
            let _ = store.l2_store_topic(&topic);
        }
    }

    // b. 更新 DomainMeta.linked_topic_ids（L3 → L2 反向）+ topic_weights
    for domain_id in domain_ids {
        let mut meta = match store.l3_get_domain_meta_v2(domain_id) {
            Ok(Some(m)) => m,
            _ => continue,
        };
        let mut modified = false;
        for topic_id in topic_ids {
            if !meta.linked_topic_ids.contains(topic_id) {
                meta.linked_topic_ids.push(topic_id.clone());
                modified = true;
            }
            *meta.topic_weights.entry(topic_id.clone()).or_insert(0.0) += 0.1;
        }
        if modified {
            meta.updated_at = chrono::Utc::now().timestamp_millis();
            let _ = store.l3_store_domain_meta_v2(domain_id, &meta);
        }
    }

    // c. 更新 L3DomainGraph 内存索引
    if let Some(ref mut l3) = brain.l3 {
        for domain_id in domain_ids {
            for topic_id in topic_ids {
                l3.add_domain_topic_link(domain_id, topic_id);
            }
        }
    }
}
