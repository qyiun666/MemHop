use std::collections::HashMap;
use heed::RwTxn;
use crate::engram::{KnowledgeNode, Hyperedge};
use crate::types::{HyperedgeKind, Layer, NodeSource};
use crate::lmdb::{L3Env, truncate_key};
use crate::error::{Result, MemHopError};
use crate::index::{HnswIndex, SparseIndex};
use half::f16;

/// L3 领域超图 — 知识图谱（含向量索引和BM25索引，env 从外部传入）。
pub struct L3DomainGraph {
    pub vector_index: HnswIndex,
    pub bm25: SparseIndex,
}

impl L3DomainGraph {
    pub fn new() -> Self {
        L3DomainGraph { vector_index: HnswIndex::default(), bm25: SparseIndex::new() }
    }

    /// v0.16.0: 使用指定维度创建。
    pub fn with_dim(dim: usize) -> Self {
        L3DomainGraph { vector_index: HnswIndex::new(dim), bm25: SparseIndex::new() }
    }

    /// v0.18.0: 使用指定维度和配置创建。
    pub fn with_dim_and_config(dim: usize, config: crate::index::HnswConfig) -> Self {
        L3DomainGraph { vector_index: HnswIndex::new_with_config(dim, config), bm25: SparseIndex::new() }
    }

    /// 从 LMDB 重建向量索引（保留现有维度）。
    pub fn rebuild_vector_index(&mut self, env: &L3Env) -> Result<()> {
        let dim = self.vector_index.dims();
        let txn = env.env.read_txn().map_err(|e| MemHopError::Storage(e.to_string()))?;
        self.vector_index = if dim > 0 { HnswIndex::new(dim) } else { HnswIndex::default() };
        if let Ok(iter) = env.domain_nodes.iter(&txn) {
            for item in iter {
                if let Ok((_key, bytes)) = item
                    && let Ok(node) = bincode::deserialize::<KnowledgeNode>(bytes)
                        && !node.vector.is_empty() && node.vector.len() > 1 {
                            self.vector_index.add(&node.id, &node.vector);
                        }
            }
        }
        Ok(())
    }

    /// v0.18.0: 从 LMDB 重建 BM25 索引。
    pub fn rebuild_bm25(&mut self, env: &L3Env) -> Result<()> {
        self.bm25 = SparseIndex::new();
        let txn = env.env.read_txn().map_err(|e| MemHopError::Storage(e.to_string()))?;
        if let Ok(iter) = env.domain_nodes.iter(&txn) {
            for item in iter {
                if let Ok((_key, bytes)) = item
                    && let Ok(node) = bincode::deserialize::<KnowledgeNode>(bytes) {
                        self.bm25.add(&node.id, &node.sparse, node.text.len());
                    }
            }
        }
        Ok(())
    }

    /// v0.18.0: BM25 搜索 L3 节点，返回 (node_id, score) 列表。
    pub fn search_by_bm25(&self, sparse: &HashMap<String, f32>, top_k: usize) -> Vec<(String, f32)> {
        let idf = self.bm25.idf_map();
        self.bm25.bm25_search(sparse, &idf, top_k)
    }

    /// Cosine 搜索 L3 节点，返回 (node_id, score) 列表。
    pub fn search_by_vector(&self, query: &[f16], top_k: usize) -> Vec<(String, f32)> {
        self.vector_index.cosine_search(query, top_k)
    }

    pub fn mount_domain(&mut self, wtxn: &mut RwTxn<'_>, env: &L3Env, name: &str) -> Result<String> {
        let now = chrono::Utc::now().timestamp_millis();
        // v0.17.2: 截断 domain_id 以适应 LMDB 键限制
        let raw_id = format!("domain_{}", name.chars().take(32).collect::<String>());
        let id = truncate_key(&raw_id);
        let meta = serde_json::json!({"id": id, "name": name, "created_at": now, "node_count": 0});
        let bytes = serde_json::to_vec(&meta).map_err(|e| MemHopError::Storage(e.to_string()))?;
        env.domain_meta.put(wtxn, &format!("meta:{}", id), &bytes)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        Ok(id)
    }

    pub fn add_node(&mut self, wtxn: &mut RwTxn<'_>, env: &L3Env,
        domain_id: &str, text: &str, sparse: &HashMap<String, f32>, _location: &str) -> Result<String> {
        let id = format!("l3n_{}", chrono::Utc::now().timestamp_millis());
        self.add_node_with_id(wtxn, env, &id, domain_id, text, sparse, _location, Vec::new())
    }

    /// Add a node with a caller-provided unique ID.
    #[allow(clippy::too_many_arguments)]
    pub fn add_node_with_id(&mut self, wtxn: &mut RwTxn<'_>, env: &L3Env,
        id: &str, domain_id: &str, text: &str, sparse: &HashMap<String, f32>, _location: &str,
        vector: Vec<f16>) -> Result<String> {
        let id = id.to_string();
        let node = KnowledgeNode::new(id.clone(), text.to_string(), sparse.clone(), vector.clone(), Layer::L3, NodeSource::KnowledgeMount);
        let key = format!("node:{}:{}", domain_id, id);
        let bytes = bincode::serialize(&node).map_err(|e| MemHopError::Storage(e.to_string()))?;
        env.domain_nodes.put(wtxn, &key, &bytes).map_err(|e| MemHopError::Storage(e.to_string()))?;
        // 更新向量索引
        if !vector.is_empty() && vector.len() > 1 {
            self.vector_index.add(&id, &vector);
        }
        Ok(id)
    }

    pub fn add_hyperedge(&mut self, wtxn: &mut RwTxn<'_>, env: &L3Env,
        domain_id: &str, node_ids: Vec<String>) -> Result<String> {
        let id = format!("l3he_{}", chrono::Utc::now().timestamp_millis());
        let he = Hyperedge {
            id: id.clone(), node_ids, kind: HyperedgeKind::Association,
            weight: 1.0, created_at: chrono::Utc::now().timestamp_millis(),
            updated_at: chrono::Utc::now().timestamp_millis(), version: 1,
            history: Vec::new(), meta: HashMap::new(),
            chain_prev: None, chain_next: None, chain_label: None,
        };
        let key = format!("hyp:{}:{}", domain_id, id);
        let bytes = bincode::serialize(&he).map_err(|e| MemHopError::Storage(e.to_string()))?;
        env.domain_hyperedges.put(wtxn, &key, &bytes).map_err(|e| MemHopError::Storage(e.to_string()))?;
        Ok(id)
    }

    pub fn unmount_domain(&mut self, wtxn: &mut RwTxn<'_>, env: &L3Env, domain_id: &str) -> Result<()> {
        env.domain_meta.delete(wtxn, &format!("meta:{}", domain_id))
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        Ok(())
    }
}
