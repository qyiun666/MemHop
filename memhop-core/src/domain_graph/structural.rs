//! L3 主节点优先搜索 — 两阶段检索。
//! Phase 1: 仅搜 is_structural=true 的主节点（结果 × 1.2 boost）
//! Phase 2: 主节点结果不足时，补充搜索详情节点

use std::collections::{HashMap, HashSet};
use half::f16;
use redb::ReadTransaction;
use crate::domain_graph::L3DomainGraph;
use crate::error::Result;
use crate::storage::store::RedbStore;
use crate::storage::{L3_DOMAIN_NODES, L3_STRUCTURAL_INDEX};
use crate::types::SourceRef;

/// 搜索结果项: (node_id, score, domain_id, is_structural, source_ref)
type SearchResultItem = (String, f32, String, bool, Option<SourceRef>);

impl L3DomainGraph {
    /// 两阶段领域检索：先搜结构节点，不足再补详情节点。
    ///
    /// 返回: Vec<(node_id, score, domain_id, is_structural, source_ref)>
    pub fn structural_search_in_domain(
        &self,
        txn: &ReadTransaction,
        store: &RedbStore,
        sparse: &HashMap<String, f32>,
        dense: &[f16],
        domain_ids: &[String],
        max: usize,
    ) -> Result<Vec<SearchResultItem>> {
        if max == 0 || domain_ids.is_empty() {
            return Ok(Vec::new());
        }

        // ── Phase 1: 搜索结构节点 ─────────────────────────────
        let _structural_min = (max / 2).max(1); // 结构节点至少贡献一半结果
        let (structural_hits, remaining) = self.phase1_structural_search(
            txn, store, sparse, dense, domain_ids, max
        )?;

        let mut results: Vec<SearchResultItem> = structural_hits;

        // ── Phase 2: 补充搜索详情节点 ─────────────────────────
        if remaining > 0 {
            let detail_hits = self.phase2_detail_search(
                txn, store, sparse, dense, domain_ids, remaining
            )?;
            results.extend(detail_hits);
        }

        // RRF 融合
        if results.len() > 1 {
            self.rrf_merge_results(&mut results, max);
        }

        Ok(results)
    }

    /// Phase 1: 仅搜索结构节点，score × 1.2 boost
    fn phase1_structural_search(
        &self,
        txn: &ReadTransaction,
        _store: &RedbStore,
        sparse: &HashMap<String, f32>,
        dense: &[f16],
        domain_ids: &[String],
        max: usize,
    ) -> Result<(Vec<SearchResultItem>, usize)> {
        let structural_index_table = match txn.open_table(L3_STRUCTURAL_INDEX) {
            Ok(t) => t,
            Err(_) => return Ok((Vec::new(), max)),
        };
        let nodes_table = match txn.open_table(L3_DOMAIN_NODES) {
            Ok(t) => t,
            Err(_) => return Ok((Vec::new(), max)),
        };

        // 收集所有结构节点 ID
        let mut structural_ids: HashSet<String> = HashSet::new();
        let mut structural_domain_map: HashMap<String, String> = HashMap::new(); // node_id → domain_id
        for domain_id in domain_ids {
            let key = format!("domain:{}", domain_id);
            if let Ok(Some(bytes)) = structural_index_table.get(key.as_str())
                && let Ok(ids) = bincode::deserialize::<Vec<String>>(bytes.value())
            {
                for node_id in &ids {
                    structural_domain_map.insert(node_id.clone(), domain_id.clone());
                    structural_ids.insert(node_id.clone());
                }
            }
        }

        if structural_ids.is_empty() {
            return Ok((Vec::new(), max));
        }

        // 全局 BM25 搜索，过滤出结构节点
        let idf = self.bm25.idf_map();
        let global_bm25 = self.bm25.bm25_search(sparse, &idf, max * 10)?;
        
        // 构建 BM25 得分映射
        let bm25_scores: HashMap<String, f32> = global_bm25.into_iter()
            .filter(|(id, _)| structural_ids.contains(id))
            .collect();
        
        // 对结构节点做 BM25 + Cosine 融合
        let mut hits: Vec<SearchResultItem> = Vec::new();

        for node_id in bm25_scores.keys() {
            let domain_id = structural_domain_map.get(node_id).cloned().unwrap_or_default();
            let key = format!("node:{}:{}", domain_id, node_id);
            if let Ok(Some(bytes)) = nodes_table.get(key.as_str())
                && let Ok(node) = bincode::deserialize::<crate::engram::KnowledgeNode>(bytes.value())
            {
                let bm25_score = bm25_scores.get(node_id).copied().unwrap_or(0.0);
                let cos_score = if !node.vector.is_empty() && !dense.is_empty() {
                    self.vector_index.cosine_similarity(&node.vector, dense)
                } else {
                    0.0
                };
                let combined_score = (bm25_score * 1.2 + cos_score * 0.8).min(1.0); // × 1.2 boost

                if combined_score > 0.0 {
                    hits.push((node_id.clone(), combined_score, domain_id, true, node.source_ref.clone()));
                }
            }
        }

        // 按得分降序排列，取 top
        hits.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
        let keep = hits.len().min(max);
        let remaining = max.saturating_sub(keep);
        hits.truncate(keep);

        Ok((hits, remaining))
    }

    /// Phase 2: 补充搜索详情节点（is_structural=false）
    fn phase2_detail_search(
        &self,
        txn: &ReadTransaction,
        _store: &RedbStore,
        sparse: &HashMap<String, f32>,
        _dense: &[f16],
        domain_ids: &[String],
        max: usize,
    ) -> Result<Vec<SearchResultItem>> {
        // 使用原始的 search_in_domain 方法，过滤掉已找到的结构节点
        // 我们使用现有的 BM25 搜索
        let idf = self.bm25.idf_map();
        let global_bm25 = self.bm25.bm25_search(sparse, &idf, max * 5)?;
        let bm25_map: HashMap<String, f32> = global_bm25.into_iter().collect();

        let nodes_table = match txn.open_table(L3_DOMAIN_NODES) {
            Ok(t) => t,
            Err(_) => return Ok(Vec::new()),
        };

        let mut detail_hits: Vec<SearchResultItem> = Vec::new();

        for domain_id in domain_ids {
            let start = format!("node:{}:", domain_id);
            let end = format!("node:{}:\u{FF}", domain_id);
            let range_result = nodes_table.range(start.as_str()..=end.as_str());
            let range = match range_result {
                Ok(r) => r,
                Err(_) => continue,
            };

            for result in range {
                let (key, value) = match result {
                    Ok(kv) => kv,
                    Err(_) => continue,
                };
                let key_str = key.value();
                let parts: Vec<&str> = key_str.splitn(3, ':').collect();
                if parts.len() < 3 { continue; }
                let node_id = parts[2].to_string();
                let node_domain_id = parts[1].to_string();

                if let Ok(node) = bincode::deserialize::<crate::engram::KnowledgeNode>(value.value()) {
                    if node.is_structural {
                        continue; // 跳过结构节点（Phase 1 已处理）
                    }
                    let bm25_score = bm25_map.get(&node_id).copied().unwrap_or(0.0);
                    if bm25_score > 0.0 {
                        detail_hits.push((node_id, bm25_score.min(1.0), node_domain_id, false, node.source_ref));
                    }
                    if detail_hits.len() >= max {
                        break;
                    }
                }
            }
            if detail_hits.len() >= max {
                break;
            }
        }

        Ok(detail_hits)
    }

    /// 简单 RRF 融合
    fn rrf_merge_results(
        &self,
        results: &mut Vec<SearchResultItem>,
        max: usize,
    ) {
        let k = 60.0f64;
        let mut rrf_scores: HashMap<String, f64> = HashMap::new();
        let mut id_to_info: HashMap<String, (String, f32, String, bool, Option<SourceRef>)> = HashMap::new();

        results.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
        for (rank, (node_id, score, domain_id, is_structural, source_ref)) in results.drain(..).enumerate() {
            *rrf_scores.entry(node_id.clone()).or_insert(0.0) += 1.0 / (k + rank as f64);
            id_to_info.entry(node_id.clone()).or_insert((node_id, score, domain_id, is_structural, source_ref));
        }

        let mut ranked: Vec<(String, f64)> = rrf_scores.into_iter().collect();
        ranked.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));

        for (node_id, _) in ranked.into_iter().take(max) {
            if let Some((_, score, domain_id, is_structural, source_ref)) = id_to_info.remove(&node_id) {
                results.push((node_id, score, domain_id, is_structural, source_ref));
            }
        }
    }
}
