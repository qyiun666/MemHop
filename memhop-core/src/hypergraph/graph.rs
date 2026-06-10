use crate::engram::{Hyperedge, KnowledgeNode};
use crate::error::{MemHopError, Result};
use crate::index::{HnswIndex, MemHopHnswConfig, SparseIndexV2};
use crate::storage::store::RedbStore;
use crate::storage::{L1_HYPEREDGES, L1_NODES, L1_NODE_TO_HYPEREDGES};
use crate::types::{HyperedgeKind, Layer, NodeSource};
use redb::ReadableTable;
use std::collections::HashMap;

/// L1 全局超图 — 管理超图节点和超边（无状态，store 从外部传入）。
pub struct L1Hypergraph {
    pub bm25: SparseIndexV2,
    pub vector_index: HnswIndex,
    /// v1.0: E5 dense HNSW 索引（第三检索通道）
    pub vector_index_e5: HnswIndex,
    node_count: u64,
    /// v0.22.0: 保存初始 HNSW 配置，rebuild 时复用（避免丢失 for_scale 自适应参数）。
    config: MemHopHnswConfig,
}

impl L1Hypergraph {
    pub fn new() -> Self {
        L1Hypergraph {
            bm25: SparseIndexV2::new(None),
            vector_index: HnswIndex::default(),
            vector_index_e5: HnswIndex::default(),
            node_count: 0,
            config: MemHopHnswConfig::default(),
        }
    }

    /// v0.16.0: 使用指定维度创建。
    pub fn with_dim(dim: usize) -> Self {
        L1Hypergraph {
            bm25: SparseIndexV2::new(None),
            vector_index: HnswIndex::new(dim),
            vector_index_e5: HnswIndex::new(dim),
            node_count: 0,
            config: MemHopHnswConfig::default(),
        }
    }

    /// v0.18.0: 使用指定维度和配置创建。
    pub fn with_dim_and_config(dim: usize, config: crate::index::MemHopHnswConfig) -> Self {
        L1Hypergraph {
            bm25: SparseIndexV2::new(None),
            vector_index: HnswIndex::new_with_config(dim, config.clone()),
            vector_index_e5: HnswIndex::new_with_config(dim, config.clone()),
            node_count: 0,
            config,
        }
    }

    /// 从 redb 重建 BM25 索引（batch 模式，单写事务批量添加）。
    pub fn rebuild_bm25(&mut self, store: &RedbStore) -> Result<()> {
        let _timer = std::time::Instant::now();
        self.node_count = 0;
        self.bm25 = SparseIndexV2::new(Some(store.db_arc()));

        let rtxn = store.begin_read()
            .map_err(|e| MemHopError::Storage(format!("begin_read: {}", e)))?;
        let table = match rtxn.open_table(L1_NODES) {
            Ok(t) => t,
            Err(e) if e.to_string().contains("does not exist") => {
                eprintln!("[memhop] L1 rebuild_bm25: L1_NODES table not found, skipping");
                return Ok(());
            }
            Err(e) => return Err(MemHopError::Storage(format!("open L1_NODES: {}", e))),
        };

        let mut batch_items: Vec<(String, HashMap<String, f32>, usize)> = Vec::new();
        for result in table.iter()
            .map_err(|e| MemHopError::Storage(format!("iter L1_NODES: {}", e)))?
        {
            let (_key, value) = result
                .map_err(|e| MemHopError::Storage(format!("iter entry: {}", e)))?;
            if let Ok(node) = bincode::deserialize::<KnowledgeNode>(value.value()) {
                batch_items.push((node.id.clone(), node.sparse.clone(), node.text.len()));
                self.node_count += 1;
            }
        }
        drop(table);
        drop(rtxn);

        // Batch add all to BM25 in a single write transaction
        let batch_refs: Vec<(&str, &HashMap<String, f32>, usize)> = batch_items.iter()
            .map(|(id, sparse, len)| (id.as_str(), sparse, *len))
            .collect();
        self.bm25.add_batch(store, &batch_refs)?;

        eprintln!("[memhop] L1 rebuild_bm25: {} nodes in {}ms", self.node_count, _timer.elapsed().as_millis());
        Ok(())
    }

    /// v0.25.0: 从 redb 重建 HnswIndex（保留现有维度）。
    pub fn rebuild_vector_index(&mut self, store: &RedbStore) -> Result<()> {
        let _timer = std::time::Instant::now();
        let dim = self.vector_index.dims();
        let rtxn = store.begin_read()
            .map_err(|e| MemHopError::Storage(format!("begin_read: {}", e)))?;
        let table = match rtxn.open_table(L1_NODES) {
            Ok(t) => t,
            Err(e) if e.to_string().contains("does not exist") => {
                eprintln!("[memhop] L1 rebuild_vector_index: L1_NODES table not found, skipping");
                return Ok(());
            }
            Err(e) => return Err(MemHopError::Storage(format!("open L1_NODES: {}", e))),
        };

        // v0.22.0: 复用初始配置（保留 for_scale 自适应参数），避免回退到 default。
        self.vector_index = if dim > 0 {
            HnswIndex::new_with_config(dim, self.config.clone())
        } else {
            HnswIndex::default()
        };

        // v1.0: 重建 E5 向量索引
        let dim_e5 = self.vector_index_e5.dims();
        self.vector_index_e5 = if dim_e5 > 0 {
            HnswIndex::new_with_config(dim_e5, self.config.clone())
        } else {
            HnswIndex::default()
        };

        let mut count = 0u64;
        let mut count_e5 = 0u64;
        for result in table.iter()
            .map_err(|e| MemHopError::Storage(format!("iter L1_NODES: {}", e)))?
        {
            let (_key, value) = result
                .map_err(|e| MemHopError::Storage(format!("iter entry: {}", e)))?;
            if let Ok(node) = bincode::deserialize::<KnowledgeNode>(value.value())
                && !node.vector.is_empty() {
                    self.vector_index.add(&node.id, &node.vector);
                    count += 1;
                }
            // 重建 E5 向量索引
            if let Ok(node) = bincode::deserialize::<KnowledgeNode>(value.value())
                && !node.vector_e5.is_empty()
                && node.vector_e5.len() > 1 {
                    self.vector_index_e5.add(&node.id, &node.vector_e5);
                    count_e5 += 1;
                }
            }
        eprintln!("[memhop] L1 rebuild_vector_index: {} nodes in {}ms", count, _timer.elapsed().as_millis());
        if count_e5 > 0 {
            eprintln!("[memhop] L1 rebuild_vector_index_e5: {} nodes", count_e5);
        }
        Ok(())
    }

    #[allow(clippy::too_many_arguments)]
    pub fn add_node(
        &mut self,
        store: &RedbStore,
        wtxn: &mut redb::WriteTransaction,
        text: &str,
        sparse: &HashMap<String, f32>,
        vector: Vec<half::f16>,
        _keywords: Vec<String>,
        source: NodeSource,
    ) -> Result<String> {
        let id = format!("kn_{}", chrono::Utc::now().timestamp_millis());
        self.add_node_with_id(store, wtxn, &id, text, sparse, vector, _keywords, source, 0.5)
    }

    /// Add a node with a caller-provided unique ID.
    #[allow(clippy::too_many_arguments)]
    pub fn add_node_with_id(
        &mut self,
        _store: &RedbStore,
        wtxn: &mut redb::WriteTransaction,
        id: &str,
        text: &str,
        sparse: &HashMap<String, f32>,
        vector: Vec<half::f16>,
        _keywords: Vec<String>,
        source: NodeSource,
        importance: f32,
    ) -> Result<String> {
        let id = id.to_string();
        let mut node = KnowledgeNode::new(
            id.clone(),
            text.to_string(),
            sparse.clone(),
            vector.clone(),
            Layer::L1,
            source,
        );
        node.memory.importance = importance;
        let bytes = bincode::serialize(&node)?;
        {
            let mut table = wtxn.open_table(L1_NODES)
                .map_err(|e| MemHopError::Storage(format!("open L1_NODES: {}", e)))?;
            table.insert(id.as_str(), bytes.as_slice())
                .map_err(|e| MemHopError::Storage(format!("insert node: {}", e)))?;
        }
        self.bm25.add(&id, sparse, text.len())?;
        if !vector.is_empty() {
            self.vector_index.add(&id, &vector);
        }
        self.node_count += 1;
        Ok(id)
    }

    pub fn get_node(
        &self,
        txn: &redb::ReadTransaction,
        id: &str,
    ) -> Result<Option<KnowledgeNode>> {
        let table = txn.open_table(L1_NODES)
            .map_err(|e| MemHopError::Storage(format!("open L1_NODES: {}", e)))?;
        match table.get(id)
            .map_err(|e| MemHopError::Storage(format!("get node: {}", e)))?
        {
            Some(bytes) => Ok(Some(
                bincode::deserialize(bytes.value())?,
            )),
            None => Ok(None),
        }
    }

    #[allow(clippy::too_many_arguments)]
    pub fn add_hyperedge(
        &mut self,
        _store: &RedbStore,
        wtxn: &mut redb::WriteTransaction,
        node_ids: Vec<String>,
        kind: HyperedgeKind,
        weight: f32,
        chain_prev: Option<String>,
        chain_label: Option<String>,
    ) -> Result<String> {
        let now = chrono::Utc::now().timestamp_millis();
        let id = format!("he_{}", now);
        let he = Hyperedge {
            id: id.clone(),
            node_ids,
            kind,
            weight,
            created_at: now,
            updated_at: now,
            version: 1,
            history: Vec::new(),
            meta: HashMap::new(),
            chain_prev,
            chain_next: None,
            chain_label,
        };
        let bytes = bincode::serialize(&he)?;
        {
            let mut table = wtxn.open_table(L1_HYPEREDGES)
                .map_err(|e| MemHopError::Storage(format!("open L1_HYPEREDGES: {}", e)))?;
            table.insert(id.as_str(), bytes.as_slice())
                .map_err(|e| MemHopError::Storage(format!("insert hyperedge: {}", e)))?;
        }

        {
            let mut he_table = wtxn.open_table(L1_NODE_TO_HYPEREDGES)
                .map_err(|e| MemHopError::Storage(format!("open L1_NODE_TO_HYPEREDGES: {}", e)))?;
            for nid in &he.node_ids {
                let existing: Vec<String> = match he_table.get(nid.as_str())
                    .map_err(|e| MemHopError::Storage(format!("get node_to_he: {}", e)))?
                {
                    Some(b) => bincode::deserialize(b.value()).unwrap_or_default(),
                    None => Vec::new(),
                };
                let mut ids = existing;
                ids.push(id.clone());
                let bytes = bincode::serialize(&ids)?;
                he_table.insert(nid.as_str(), bytes.as_slice())
                    .map_err(|e| MemHopError::Storage(format!("insert node_to_he: {}", e)))?;
            }
        }

        // Update chain_prev's chain_next (scoped to avoid E0502)
        if let Some(ref prev_id) = he.chain_prev {
            let existing_prev: Option<Hyperedge> = {
                let table = wtxn.open_table(L1_HYPEREDGES)
                    .map_err(|e| MemHopError::Storage(format!("open L1_HYPEREDGES: {}", e)))?;
                match table.get(prev_id.as_str())
                    .map_err(|e| MemHopError::Storage(format!("get hyperedge: {}", e)))?
                {
                    Some(bytes) => bincode::deserialize(bytes.value()).ok(),
                    None => None,
                }
            };
            if let Some(mut prev) = existing_prev {
                prev.chain_next = Some(id.clone());
                prev.updated_at = now;
                if let Ok(bytes) = bincode::serialize(&prev) {
                    let mut table = wtxn.open_table(L1_HYPEREDGES)
                        .map_err(|e| MemHopError::Storage(format!("open L1_HYPEREDGES: {}", e)))?;
                    let _ = table.insert(prev_id.as_str(), bytes.as_slice());
                }
            }
        }
        Ok(id)
    }

    pub fn get_hyperedge(
        &self,
        txn: &redb::ReadTransaction,
        id: &str,
    ) -> Result<Option<Hyperedge>> {
        let table = txn.open_table(L1_HYPEREDGES)
            .map_err(|e| MemHopError::Storage(format!("open L1_HYPEREDGES: {}", e)))?;
        match table.get(id)
            .map_err(|e| MemHopError::Storage(format!("get hyperedge: {}", e)))?
        {
            Some(bytes) => Ok(Some(
                bincode::deserialize(bytes.value())?,
            )),
            None => Ok(None),
        }
    }

    pub fn search(
        &self,
        sparse: &HashMap<String, f32>,
        top_k: usize,
    ) -> Result<Vec<(String, f32)>> {
        let idf = self.bm25.idf_map();
        self.bm25.bm25_search(sparse, &idf, top_k)
    }

    /// v1.0: E5 向量索引是否可用
    pub fn has_e5_index(&self) -> bool {
        !self.vector_index_e5.is_empty()
    }

    pub fn node_count(&self) -> u64 {
        self.node_count
    }

    /// v0.25.0: 使用 redb 读取超图和反向索引。
    pub fn bfs_spread(
        &self,
        txn: &redb::ReadTransaction,
        seed_ids: &[String],
        depth: usize,
    ) -> Result<Vec<(String, f32)>> {
        let mut visited = std::collections::HashSet::new();
        let mut results = Vec::new();
        let mut current = seed_ids.to_vec();
        for _ in 0..depth {
            if current.is_empty() {
                break;
            }
            let mut next = Vec::new();
            for sid in &current {
                if !visited.insert(sid.clone()) {
                    continue;
                }
                let hyperedge_ids: Vec<String> = {
                    let table = match txn.open_table(L1_NODE_TO_HYPEREDGES) {
                        Ok(t) => t,
                        Err(_) => continue,
                    };
                    match table.get(sid.as_str())
                        .map_err(|e| MemHopError::Storage(format!("get node_to_he: {}", e)))?
                    {
                        Some(bytes) => bincode::deserialize(bytes.value()).unwrap_or_default(),
                        None => Vec::new(),
                    }
                };
                for eid in &hyperedge_ids {
                    let he_table = match txn.open_table(L1_HYPEREDGES) {
                        Ok(t) => t,
                        Err(_) => continue,
                    };
                    if let Ok(Some(hb)) = he_table.get(eid.as_str())
                        && let Ok(he) = bincode::deserialize::<Hyperedge>(hb.value())
                    {
                        for nid in &he.node_ids {
                            if nid != sid && visited.insert(nid.clone()) {
                                results.push((nid.clone(), he.weight));
                                next.push(nid.clone());
                            }
                        }
                    }
                }
            }
            current = next;
        }
        Ok(results)
    }
}
