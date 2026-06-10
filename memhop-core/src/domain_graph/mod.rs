use crate::engram::KnowledgeNode;
use crate::error::{MemHopError, Result};
use crate::index::{HnswIndex, MemHopHnswConfig, SparseIndexV2};
use crate::storage::store::RedbStore;
use crate::storage::{L3_DOMAIN_NODES, L3_SPARSE_DOC_LEN, L3_SPARSE_FORWARD};
use crate::types::CrossDomainLink;
use half::f16;
use redb::ReadableTable;
use std::collections::HashMap;

/// L3 领域超图 — 知识图谱（含向量索引和 BM25 索引，env 从外部传入）。
pub struct L3DomainGraph {
    pub vector_index: HnswIndex,
    pub bm25: SparseIndexV2,
    /// domain_id → linked topic_ids 内存反向索引
    pub domain_to_topics: HashMap<String, Vec<String>>,
    config: MemHopHnswConfig,
}

impl L3DomainGraph {
    pub fn new() -> Self {
        L3DomainGraph {
            vector_index: HnswIndex::default(),
            bm25: SparseIndexV2::with_tables(None, L3_SPARSE_FORWARD, L3_SPARSE_DOC_LEN),
            domain_to_topics: HashMap::new(),
            config: MemHopHnswConfig::default(),
        }
    }

    pub fn with_dim(dim: usize) -> Self {
        L3DomainGraph {
            vector_index: HnswIndex::new(dim),
            bm25: SparseIndexV2::with_tables(None, L3_SPARSE_FORWARD, L3_SPARSE_DOC_LEN),
            domain_to_topics: HashMap::new(),
            config: MemHopHnswConfig::default(),
        }
    }

    pub fn with_dim_and_config(dim: usize, config: MemHopHnswConfig) -> Self {
        L3DomainGraph {
            vector_index: HnswIndex::new_with_config(dim, config.clone()),
            bm25: SparseIndexV2::with_tables(None, L3_SPARSE_FORWARD, L3_SPARSE_DOC_LEN),
            domain_to_topics: HashMap::new(),
            config,
        }
    }

    /// 添加 domain→topic 关联
    pub fn add_domain_topic_link(&mut self, domain_id: &str, topic_id: &str) {
        self.domain_to_topics
            .entry(domain_id.to_string())
            .or_default()
            .push(topic_id.to_string());
    }

    /// 移除 domain→topic 关联
    pub fn remove_domain_topic_link(&mut self, domain_id: &str, topic_id: &str) {
        if let Some(topics) = self.domain_to_topics.get_mut(domain_id) {
            topics.retain(|t| t != topic_id);
        }
    }

    /// 获取 domain 关联的所有 topic IDs
    pub fn get_domain_topic_links(&self, domain_id: &str) -> Vec<String> {
        self.domain_to_topics.get(domain_id).cloned().unwrap_or_default()
    }

    /// 从 redb 重建向量索引。
    pub fn rebuild_vector_index(&mut self, store: &RedbStore) -> Result<()> {
        let _timer = std::time::Instant::now();
        let dim = self.vector_index.dims();
        let rtxn = match store.begin_read() {
            Ok(t) => t,
            Err(_) => return Ok(()),
        };
        self.vector_index = if dim > 0 {
            HnswIndex::new_with_config(dim, self.config.clone())
        } else {
            HnswIndex::default()
        };
        let mut count = 0u64;
        let table = match rtxn.open_table(L3_DOMAIN_NODES) {
            Ok(t) => t,
            Err(_) => return Ok(()),
        };
        for result in table.iter()
            .map_err(|e| MemHopError::Storage(format!("iter L3_DOMAIN_NODES: {}", e)))?
        {
            if let Ok((_key, bytes)) = result
                && let Ok(node) = bincode::deserialize::<KnowledgeNode>(bytes.value())
                && !node.vector.is_empty()
                && node.vector.len() > 1
            {
                self.vector_index.add(&node.id, &node.vector);
                count += 1;
            }
        }
        eprintln!("[memhop] L3 rebuild_vector_index: {} nodes in {}ms", count, _timer.elapsed().as_millis());
        Ok(())
    }

    /// 从 redb 重建 BM25 索引（batch 模式，单写事务批量添加）。
    pub fn rebuild_bm25(&mut self, store: &RedbStore) -> Result<()> {
        let _timer = std::time::Instant::now();

        // 创建新的 SparseIndexV2，使用 L3 专用表，并关联数据库
        self.bm25 = SparseIndexV2::with_tables(
            Some(store.db_arc()),
            L3_SPARSE_FORWARD,
            L3_SPARSE_DOC_LEN,
        );

        let rtxn = match store.begin_read() {
            Ok(t) => t,
            Err(_) => return Ok(()),
        };
        let table = match rtxn.open_table(L3_DOMAIN_NODES) {
            Ok(t) => t,
            Err(_) => return Ok(()),
        };

        let mut batch_items: Vec<(String, HashMap<String, f32>, usize)> = Vec::new();
        for result in table.iter()
            .map_err(|e| MemHopError::Storage(format!("iter L3_DOMAIN_NODES: {}", e)))?
        {
            if let Ok((_key, bytes)) = result
                && let Ok(node) = bincode::deserialize::<KnowledgeNode>(bytes.value())
            {
                batch_items.push((node.id.clone(), node.sparse.clone(), node.text.len()));
            }
        }
        drop(table);
        drop(rtxn);

        // Batch add all to BM25 in a single write transaction
        let batch_refs: Vec<(&str, &HashMap<String, f32>, usize)> = batch_items.iter()
            .map(|(id, sparse, len)| (id.as_str(), sparse, *len))
            .collect();
        self.bm25.add_batch(store, &batch_refs)?;

        eprintln!("[memhop] L3 rebuild_bm25: {} nodes in {}ms", batch_items.len(), _timer.elapsed().as_millis());
        Ok(())
    }

    /// v0.25.0: 使用 redb BM25 搜索（SparseIndexV2 版）。
    pub fn search_by_bm25(
        &self,
        sparse: &HashMap<String, f32>,
        top_k: usize,
    ) -> Result<Vec<(String, f32)>> {
        let idf = self.bm25.idf_map();
        self.bm25.bm25_search(sparse, &idf, top_k)
    }

    /// v0.25.0: 使用 redb 向量搜索。
    pub fn search_by_vector(&self, query: &[f16], top_k: usize) -> Vec<(String, f32)> {
        self.vector_index.cosine_search(query, top_k)
    }

    /// v0.25.0: 领域内检索 — 使用前缀 range 查询替代全表扫描。
    ///
    /// redb key 格式: "node:{domain_id}:{node_id}"
    /// 对每个 domain_id 做 range(start..=end) 查询，只遍历该 domain 的节点。
    /// 结果使用 RRF 融合跨 domain 结果。
    pub fn search_in_domain(
        &self,
        txn: &redb::ReadTransaction,
        _store: &RedbStore,
        sparse: &HashMap<String, f32>,
        dense: &[f16],
        domain_ids: &[String],
        max: usize,
    ) -> Result<Vec<(String, f32, String)>> {
        let mut bm25_results: Vec<(String, f32, String)> = Vec::new();
        let mut cos_results: Vec<(String, f32, String)> = Vec::new();

        let node_table = txn.open_table(L3_DOMAIN_NODES)
            .map_err(|e| MemHopError::Storage(format!("open L3_DOMAIN_NODES: {}", e)))?;

        let has_cosine = !self.vector_index.is_empty()
            && !dense.is_empty()
            && dense.iter().any(|v| v.to_f32().abs() > 1e-8);

        // 全局 BM25 搜索（使用 SparseIndexV2 的 BM25 IDF 加权）
        let idf = self.bm25.idf_map();
        let global_bm25 = self.bm25.bm25_search(sparse, &idf, max * 5)?;
        let bm25_map: HashMap<String, f32> = global_bm25.into_iter().collect();

        // 对每个 domain_id 做范围查询（前缀 range 优化）
        for domain_id in domain_ids {
            let start = format!("node:{}:", domain_id);
            // End key: node:{domain_id}:\xFF (FF is highest byte, so all keys with this prefix are included)
            let end = format!("node:{}:\u{FF}", domain_id);

            let range_result = node_table.range(start.as_str()..=end.as_str());
            let range = match range_result {
                Ok(r) => r,
                Err(e) => {
                    eprintln!("[memhop] L3 search_in_domain range error: {}", e);
                    continue;
                }
            };

            for result in range {
                let (key, value) = match result {
                    Ok(kv) => kv,
                    Err(e) => {
                        eprintln!("[memhop] L3 search_in_domain iter error: {}", e);
                        continue;
                    }
                };

                let key_str = key.value();
                let parts: Vec<&str> = key_str.splitn(3, ':').collect();
                if parts.len() < 3 {
                    continue;
                }
                let node_id = parts[2].to_string();
                let node_domain_id = parts[1].to_string();

                if let Ok(node) = bincode::deserialize::<KnowledgeNode>(value.value()) {
                    // BM25 score: 使用全局 BM25 IDF 加权评分
                    let bm25_score = bm25_map.get(&node_id).copied().unwrap_or(0.0);
                    if bm25_score > 0.0 {
                        bm25_results.push((node_id.clone(), bm25_score.min(1.0), node_domain_id.clone()));
                    }

                    // Cosine similarity (uses HNSW index's cosine_similarity method)
                    if has_cosine {
                        let cos_sim = self.vector_index.cosine_similarity(&node.vector, dense);
                        if cos_sim > 0.1 {
                            cos_results.push((node_id, cos_sim, node_domain_id));
                        }
                    }
                }
            }
        }

        // RRF fusion
        if cos_results.is_empty() {
            bm25_results.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
            bm25_results.truncate(max);
            return Ok(bm25_results);
        }

        let rrf_k = 60.0f64;
        let mut rrf_scores: HashMap<String, f64> = HashMap::new();
        let mut id_to_info: HashMap<String, (String, f32, String)> = HashMap::new();

        bm25_results.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
        for (rank, (node_id, score, domain_id)) in bm25_results.into_iter().enumerate() {
            *rrf_scores.entry(node_id.clone()).or_insert(0.0) += 1.0 / (rrf_k + rank as f64);
            id_to_info.entry(node_id.clone()).or_insert((node_id, score, domain_id));
        }

        cos_results.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
        for (rank, (node_id, score, domain_id)) in cos_results.into_iter().enumerate() {
            *rrf_scores.entry(node_id.clone()).or_insert(0.0) += 1.0 / (rrf_k + rank as f64);
            id_to_info.entry(node_id.clone()).or_insert((node_id, score, domain_id));
        }

        let mut ranked: Vec<(String, f64)> = rrf_scores.into_iter().collect();
        ranked.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
        ranked.truncate(max);

        let mut results: Vec<(String, f32, String)> = Vec::new();
        for (node_id, _rrf_score) in ranked {
            if let Some((_, original_score, domain_id)) = id_to_info.remove(&node_id) {
                results.push((node_id, original_score, domain_id));
            }
        }
        Ok(results)
    }
    /// 发现跨域链接 — 检测同时链接到 domain_a 和 domain_b 的 L2 topics
    pub fn discover_cross_domain_links(
        &self,
        store: &RedbStore,
    ) -> Result<Vec<CrossDomainLink>> {
        let topics = store.l2_list_topics()?;

        // 统计 domain 对 → 共现 topic 列表
        let mut domain_pairs: std::collections::HashMap<(String, String), Vec<String>> = std::collections::HashMap::new();

        for topic in &topics {
            let linked = &topic.linked_domain_ids;
            if linked.len() < 2 {
                continue;
            }
            for i in 0..linked.len() {
                for j in i+1..linked.len() {
                    let key = if linked[i] < linked[j] {
                        (linked[i].clone(), linked[j].clone())
                    } else {
                        (linked[j].clone(), linked[i].clone())
                    };
                    domain_pairs.entry(key).or_default().push(topic.id.clone());
                }
            }
        }

        // 筛选共现 topic ≥ 2 的 domain 对
        let mut links = Vec::new();
        for ((domain_a, domain_b), bridge_topics) in domain_pairs {
            if bridge_topics.len() >= 2 {
                links.push(CrossDomainLink {
                    domain_a,
                    domain_b,
                    bridge_topic_ids: bridge_topics.clone(),
                    strength: bridge_topics.len() as f32 * 0.1,
                    created_at: chrono::Utc::now().timestamp_millis(),
                });
            }
        }

        Ok(links)
    }
}

impl Default for L3DomainGraph {
    fn default() -> Self {
        Self::new()
    }
}
