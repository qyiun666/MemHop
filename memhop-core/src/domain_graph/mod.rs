use crate::engram::{Hyperedge, KnowledgeNode};
use crate::error::Result;
use crate::index::{HnswIndex, SparseIndex};
use crate::lmdb::{L3Env, truncate_key};
use crate::types::{HyperedgeKind, Layer, NodeSource};
use half::f16;
use heed::RwTxn;
use std::collections::HashMap;

/// L3 领域超图 — 知识图谱（含向量索引和BM25索引，env 从外部传入）。
pub struct L3DomainGraph {
    pub vector_index: HnswIndex,
    pub bm25: SparseIndex,
    /// v0.22.0: 保存初始 HNSW 配置，rebuild 时复用（避免丢失 for_scale 自适应参数）。
    config: crate::index::MemHopHnswConfig,
}

impl L3DomainGraph {
    pub fn new() -> Self {
        L3DomainGraph {
            vector_index: HnswIndex::default(),
            bm25: SparseIndex::new(),
            config: crate::index::MemHopHnswConfig::default(),
        }
    }

    /// v0.16.0: 使用指定维度创建。
    pub fn with_dim(dim: usize) -> Self {
        L3DomainGraph {
            vector_index: HnswIndex::new(dim),
            bm25: SparseIndex::new(),
            config: crate::index::MemHopHnswConfig::default(),
        }
    }

    /// v0.18.0: 使用指定维度和配置创建。
    pub fn with_dim_and_config(dim: usize, config: crate::index::MemHopHnswConfig) -> Self {
        L3DomainGraph {
            vector_index: HnswIndex::new_with_config(dim, config.clone()),
            bm25: SparseIndex::new(),
            config,
        }
    }

    /// 从 LMDB 重建向量索引（保留现有维度）。
    pub fn rebuild_vector_index(&mut self, env: &L3Env) -> Result<()> {
        let _timer = std::time::Instant::now();
        let dim = self.vector_index.dims();
        let txn = env
            .env
            .read_txn()
            ?;
        // v0.22.0: 复用初始配置（保留 for_scale 自适应参数），避免回退到 default。
        self.vector_index = if dim > 0 {
            HnswIndex::new_with_config(dim, self.config.clone())
        } else {
            HnswIndex::default()
        };
        let mut count = 0u64;
        if let Ok(iter) = env.domain_nodes.iter(&txn) {
            for item in iter {
                if let Ok((_key, bytes)) = item
                    && let Ok(node) = bincode::deserialize::<KnowledgeNode>(bytes)
                    && !node.vector.is_empty()
                    && node.vector.len() > 1
                {
                    self.vector_index.add(&node.id, &node.vector);
                    count += 1;
                }
            }
        }
        eprintln!("[memhop] L3 rebuild_vector_index: {} nodes in {}ms", count, _timer.elapsed().as_millis());
        Ok(())
    }

    /// v0.18.0: 从 LMDB 重建 BM25 索引。
    pub fn rebuild_bm25(&mut self, env: &L3Env) -> Result<()> {
        let _timer = std::time::Instant::now();
        self.bm25 = SparseIndex::new();
        let txn = env
            .env
            .read_txn()
            ?;
        let mut count = 0u64;
        if let Ok(iter) = env.domain_nodes.iter(&txn) {
            for item in iter {
                if let Ok((_key, bytes)) = item
                    && let Ok(node) = bincode::deserialize::<KnowledgeNode>(bytes)
                {
                    self.bm25.add(&node.id, &node.sparse, node.text.len());
                    count += 1;
                }
            }
        }
        eprintln!("[memhop] L3 rebuild_bm25: {} nodes in {}ms", count, _timer.elapsed().as_millis());
        Ok(())
    }

    /// v0.18.0: BM25 搜索 L3 节点，返回 (node_id, score) 列表。
    pub fn search_by_bm25(
        &self,
        sparse: &HashMap<String, f32>,
        top_k: usize,
    ) -> Vec<(String, f32)> {
        let idf = self.bm25.idf_map();
        self.bm25.bm25_search(sparse, &idf, top_k)
    }

    /// Cosine 搜索 L3 节点，返回 (node_id, score) 列表。
    pub fn search_by_vector(&self, query: &[f16], top_k: usize) -> Vec<(String, f32)> {
        self.vector_index.cosine_search(query, top_k)
    }

    pub fn mount_domain(
        &mut self,
        wtxn: &mut RwTxn<'_>,
        env: &L3Env,
        name: &str,
    ) -> Result<String> {
        let now = chrono::Utc::now().timestamp_millis();
        // v0.17.2: 截断 domain_id 以适应 LMDB 键限制
        let raw_id = format!("domain_{}", name.chars().take(32).collect::<String>());
        let id = truncate_key(&raw_id);
        let meta = serde_json::json!({"id": id, "name": name, "created_at": now, "node_count": 0});
        let bytes = serde_json::to_vec(&meta)?;
        env.domain_meta
            .put(wtxn, &format!("meta:{}", id), &bytes)
            ?;
        Ok(id)
    }

    pub fn add_node(
        &mut self,
        wtxn: &mut RwTxn<'_>,
        env: &L3Env,
        domain_id: &str,
        text: &str,
        sparse: &HashMap<String, f32>,
        _location: &str,
    ) -> Result<String> {
        let id = format!("l3n_{}", chrono::Utc::now().timestamp_millis());
        self.add_node_with_id(
            wtxn,
            env,
            &id,
            domain_id,
            text,
            sparse,
            _location,
            Vec::new(),
        )
    }

    /// Add a node with a caller-provided unique ID.
    #[allow(clippy::too_many_arguments)]
    pub fn add_node_with_id(
        &mut self,
        wtxn: &mut RwTxn<'_>,
        env: &L3Env,
        id: &str,
        domain_id: &str,
        text: &str,
        sparse: &HashMap<String, f32>,
        _location: &str,
        vector: Vec<f16>,
    ) -> Result<String> {
        let id = id.to_string();
        let node = KnowledgeNode::new(
            id.clone(),
            text.to_string(),
            sparse.clone(),
            vector.clone(),
            Layer::L3,
            NodeSource::KnowledgeMount,
        );
        let key = format!("node:{}:{}", domain_id, id);
        let bytes = bincode::serialize(&node)?;
        env.domain_nodes
            .put(wtxn, &key, &bytes)
            ?;
        // v0.22.0: 增量更新 BM25 + 向量索引
        self.bm25.add(&id, sparse, text.len());
        if !vector.is_empty() && vector.len() > 1 {
            self.vector_index.add(&id, &vector);
        }
        Ok(id)
    }

    pub fn add_hyperedge(
        &mut self,
        wtxn: &mut RwTxn<'_>,
        env: &L3Env,
        domain_id: &str,
        node_ids: Vec<String>,
    ) -> Result<String> {
        let id = format!("l3he_{}", chrono::Utc::now().timestamp_millis());
        let he = Hyperedge {
            id: id.clone(),
            node_ids,
            kind: HyperedgeKind::Association,
            weight: 1.0,
            created_at: chrono::Utc::now().timestamp_millis(),
            updated_at: chrono::Utc::now().timestamp_millis(),
            version: 1,
            history: Vec::new(),
            meta: HashMap::new(),
            chain_prev: None,
            chain_next: None,
            chain_label: None,
        };
        let key = format!("hyp:{}:{}", domain_id, id);
        let bytes = bincode::serialize(&he)?;
        env.domain_hyperedges
            .put(wtxn, &key, &bytes)
            ?;
        Ok(id)
    }

    pub fn unmount_domain(
        &mut self,
        wtxn: &mut RwTxn<'_>,
        env: &L3Env,
        domain_id: &str,
    ) -> Result<()> {
        env.domain_meta
            .delete(wtxn, &format!("meta:{}", domain_id))
            ?;
        Ok(())
    }

    /// v0.23.1: Domain Router — 根据查询找到最相关的 domain
    /// 返回 top-N (domain_id, score)
    pub fn route_domains(
        &self,
        txn: &heed::RoTxn<'_>,
        env: &L3Env,
        sparse: &HashMap<String, f32>,
        max_domains: usize,
    ) -> Result<Vec<(String, f32)>> {
        let mut domain_scores: Vec<(String, f32)> = Vec::new();

        // 遍历 domain_meta，计算每个 domain 与查询的 ngram overlap
        if let Ok(iter) = env.domain_meta.iter(txn) {
            for (key, bytes) in iter.flatten() {
                if !key.starts_with("meta:") {
                    continue;
                }
                if let Ok(meta) = serde_json::from_slice::<serde_json::Value>(bytes) {
                    let domain_id = meta["id"].as_str().unwrap_or("").to_string();
                    let domain_name = meta["name"].as_str().unwrap_or("").to_lowercase();

                    // 计算 ngram overlap
                    let overlap: f32 = sparse.keys()
                        .filter(|k| domain_name.contains(k.as_str()))
                        .count() as f32;

                    if overlap > 0.0 {
                        domain_scores.push((domain_id, overlap));
                    }
                }
            }
        }

        // 按 score 降序排序
        domain_scores.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
        domain_scores.truncate(max_domains);

        Ok(domain_scores)
    }

    /// v0.23.1: 在指定 domain 内搜索节点
    /// 仅搜索属于目标 domain 的节点
    pub fn search_in_domain(
        &self,
        txn: &heed::RoTxn<'_>,
        env: &L3Env,
        sparse: &HashMap<String, f32>,
        dense: &[f16],
        domain_ids: &[String],
        max: usize,
    ) -> Vec<(String, f32, String)> {
        let mut bm25_results: Vec<(String, f32, String)> = Vec::new();
        let mut cos_results: Vec<(String, f32, String)> = Vec::new();
        let domain_set: std::collections::HashSet<&String> = domain_ids.iter().collect();

        // 遍历 domain_nodes，仅处理目标 domain 的节点
        if let Ok(iter) = env.domain_nodes.iter(txn) {
            for (key, bytes) in iter.flatten() {
                if !key.starts_with("node:") {
                    continue;
                }
                // key format: "node:{domain_id}:{node_id}"
                let parts: Vec<&str> = key.splitn(3, ':').collect();
                if parts.len() < 3 {
                    continue;
                }
                let domain_id = parts[1].to_string();
                let node_id = parts[2].to_string();

                // 仅处理目标 domain
                if !domain_set.contains(&domain_id) {
                    continue;
                }

                if let Ok(node) = bincode::deserialize::<crate::engram::KnowledgeNode>(bytes) {
                    // 计算 BM25 分数
                    let bm25_score: f32 = sparse.keys()
                        .filter(|k| node.text.to_lowercase().contains(k.as_str()))
                        .count() as f32;

                    if bm25_score > 0.0 {
                        bm25_results.push((node_id.clone(), bm25_score.min(1.0), domain_id.clone()));
                    }

                    // 计算 Dense 余弦相似度
                    let has_cosine = !self.vector_index.is_empty()
                        && !dense.is_empty()
                        && dense.iter().any(|v| v.to_f32().abs() > 1e-8);

                    if has_cosine {
                        let cos_sim = self.vector_index.cosine_similarity(&node.vector, dense);
                        if cos_sim > 0.1 {
                            cos_results.push((node_id, cos_sim, domain_id));
                        }
                    }
                }
            }
        }

        // 如果 Dense 结果为空，直接返回 BM25 结果
        if cos_results.is_empty() {
            bm25_results.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
            bm25_results.truncate(max);
            return bm25_results;
        }

        // RRF 融合
        let rrf_k = 60.0f64;
        let mut rrf_scores: HashMap<String, f64> = HashMap::new();
        let mut id_to_info: HashMap<String, (String, f32, String)> = HashMap::new();

        // BM25 通道排名
        bm25_results.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
        for (rank, (node_id, score, domain_id)) in bm25_results.into_iter().enumerate() {
            *rrf_scores.entry(node_id.clone()).or_insert(0.0) += 1.0 / (rrf_k + rank as f64);
            id_to_info.entry(node_id.clone()).or_insert((node_id, score, domain_id));
        }

        // Dense 通道排名
        cos_results.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
        for (rank, (node_id, score, domain_id)) in cos_results.into_iter().enumerate() {
            *rrf_scores.entry(node_id.clone()).or_insert(0.0) += 1.0 / (rrf_k + rank as f64);
            id_to_info.entry(node_id.clone()).or_insert((node_id, score, domain_id));
        }

        // 按 RRF 分数排序
        let mut ranked: Vec<(String, f64)> = rrf_scores.into_iter().collect();
        ranked.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
        ranked.truncate(max);

        // 构建最终结果
        let mut results: Vec<(String, f32, String)> = Vec::new();
        for (node_id, _rrf_score) in ranked {
            if let Some((_, original_score, domain_id)) = id_to_info.remove(&node_id) {
                results.push((node_id, original_score, domain_id));
            }
        }

        results
    }
}
