use crate::engram::{Topic, TopicEdge};
use crate::error::{MemHopError, Result};
use crate::index::HnswIndex;
use crate::lmdb::{L2Env, truncate_key};
use crate::types::TopicEdgeKind;
use half::f16;
use heed::RwTxn;
use std::collections::HashMap;

const VECTOR_INDEX_KEY: &str = "topic_vectors_v1";

/// L2 话题标准图 — 话题级情景记忆（含向量索引，env 从外部传入）。
pub struct L2TopicGraph {
    /// Topic centroid 向量索引，用于 cosine 粗筛。
    pub topic_vectors: HnswIndex,
}

impl L2TopicGraph {
    pub fn new() -> Self {
        L2TopicGraph {
            topic_vectors: HnswIndex::default(),
        }
    }

    /// v0.16.0: 使用指定维度创建。
    pub fn with_dim(dim: usize) -> Self {
        L2TopicGraph {
            topic_vectors: HnswIndex::new(dim),
        }
    }

    /// v0.18.0: 使用指定维度和配置创建。
    pub fn with_dim_and_config(dim: usize, config: crate::index::HnswConfig) -> Result<Self> {
        Ok(L2TopicGraph {
            topic_vectors: HnswIndex::new_with_config(dim, config)?,
        })
    }

    /// 从 LMDB 重建 topic 向量索引。
    pub fn rebuild_topic_vectors(&mut self, env: &L2Env) -> Result<()> {
        let txn = env
            .env
            .read_txn()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        // 尝试从持久化加载
        if let Ok(Some(bytes)) = env.topic_vector_index.get(&txn, VECTOR_INDEX_KEY)
            && let Some(idx) = HnswIndex::from_bytes(bytes)
        {
            self.topic_vectors = idx;
            return Ok(());
        }
        // 回退：从 topic centroid 重建（保留现有维度）
        let dim = self.topic_vectors.dims();
        self.topic_vectors = if dim > 0 {
            HnswIndex::new(dim)
        } else {
            HnswIndex::default()
        };
        if let Ok(iter) = env.topics.iter(&txn) {
            for (key, bytes) in iter.flatten() {
                if !key.starts_with("topic:") || !key.ends_with(":meta") {
                    continue;
                }
                if let Ok(t) = bincode::deserialize::<Topic>(bytes)
                    && !t.centroid.is_empty()
                {
                    self.topic_vectors.add(&t.id, &t.centroid);
                }
            }
        }
        Ok(())
    }

    /// 持久化 topic 向量索引到 LMDB。
    pub fn persist_topic_vectors(&self, wtxn: &mut RwTxn<'_>, env: &L2Env) -> Result<()> {
        let bytes = self.topic_vectors.to_bytes();
        env.topic_vector_index
            .put(wtxn, VECTOR_INDEX_KEY, &bytes)
            .map_err(|e| MemHopError::Storage(e.to_string()))
    }

    /// 计算并更新 topic 的 centroid（所有成员 node 向量均值）。
    pub fn update_topic_centroid(
        &mut self,
        wtxn: &mut RwTxn<'_>,
        env: &L2Env,
        topic_id: &str,
        l1_nodes: &[(String, Vec<f16>)],
    ) -> Result<()> {
        if l1_nodes.is_empty() {
            return Ok(());
        }
        let dims = l1_nodes[0].1.len();
        if dims == 0 {
            return Ok(());
        }

        // 计算均值向量
        let mut sum = vec![0.0f64; dims];
        for (_, v) in l1_nodes {
            if v.len() != dims {
                continue;
            }
            for (i, val) in v.iter().enumerate() {
                sum[i] += val.to_f64();
            }
        }
        let n = l1_nodes.len() as f64;
        let centroid: Vec<f16> = sum.iter().map(|s| f16::from_f64(*s / n)).collect();

        // 更新 topic.centroid
        let key = format!("topic:{}:meta", topic_id);
        if let Some(bytes) = env
            .topics
            .get(wtxn, &key)
            .map_err(|e| MemHopError::Storage(e.to_string()))?
            && let Ok(mut t) = bincode::deserialize::<Topic>(bytes)
        {
            t.centroid = centroid.clone();
            let new_bytes =
                bincode::serialize(&t).map_err(|e| MemHopError::Storage(e.to_string()))?;
            env.topics
                .put(wtxn, &key, &new_bytes)
                .map_err(|e| MemHopError::Storage(e.to_string()))?;
        }

        // 更新向量索引
        self.topic_vectors.update(topic_id, &centroid);
        Ok(())
    }

    /// Cosine 搜索 topic centroids，返回 (topic_id, score) 列表。
    pub fn search_by_vector(&self, query: &[f16], top_k: usize) -> Vec<(String, f32)> {
        self.topic_vectors.cosine_search(query, top_k)
    }

    pub fn find_or_create_topic(
        &mut self,
        wtxn: &mut RwTxn<'_>,
        env: &L2Env,
        label: &str,
    ) -> Result<(String, bool)> {
        // 先查 label→id 映射（避免 key 前缀不匹配）
        // v0.17.2: 截断键以适应 LMDB 511 字节限制
        let raw_lookup_key = format!("label:{}", label);
        let lookup_key = truncate_key(&raw_lookup_key);
        if let Some(bytes) = env
            .topics
            .get(wtxn, &lookup_key)
            .map_err(|e| MemHopError::Storage(e.to_string()))?
            && let Ok(topic_id) = bincode::deserialize::<String>(bytes)
        {
            return Ok((topic_id, false)); // 已存在
        }
        // 创建新 topic
        let now = chrono::Utc::now().timestamp_millis();
        let id = format!("topic_{}", now);
        let meta_key = format!("topic:{}:meta", &id);
        let topic = Topic {
            id: id.clone(),
            label: label.to_string(),
            summary: None,
            keywords: Vec::new(),
            centroid: Vec::new(),
            node_ids: Vec::new(),
            linked_domain_ids: Vec::new(),
            doc_ids: Vec::new(),
            dialogue_range: None,
            created_at: now,
            updated_at: now,
            version: 1,
            history: Vec::new(),
            extended_meta: HashMap::new(),
            domain_weights: HashMap::new(),
            node_weights: HashMap::new(),
        };
        let bytes = bincode::serialize(&topic).map_err(|e| MemHopError::Storage(e.to_string()))?;
        env.topics
            .put(wtxn, &meta_key, &bytes)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        // 写 label→id 映射
        let id_bytes = bincode::serialize(&id).map_err(|e| MemHopError::Storage(e.to_string()))?;
        env.topics
            .put(wtxn, &lookup_key, &id_bytes)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        Ok((id, true)) // 新创建
    }

    pub fn get_topic_by_id(
        &self,
        txn: &heed::RoTxn<'_>,
        env: &L2Env,
        id: &str,
    ) -> Result<Option<Topic>> {
        let key = format!("topic:{}:meta", id);
        match env
            .topics
            .get(txn, &key)
            .map_err(|e| MemHopError::Storage(e.to_string()))?
        {
            Some(bytes) => Ok(Some(
                bincode::deserialize(bytes).map_err(|e| MemHopError::Storage(e.to_string()))?,
            )),
            None => Ok(None),
        }
    }

    pub fn add_node_to_topic(
        &mut self,
        wtxn: &mut RwTxn<'_>,
        env: &L2Env,
        topic_id: &str,
        node_id: &str,
        _sparse: &HashMap<String, f32>,
    ) -> Result<()> {
        let key = format!("topic:{}:meta", topic_id);
        if let Some(bytes) = env
            .topics
            .get(wtxn, &key)
            .map_err(|e| MemHopError::Storage(e.to_string()))?
            && let Ok(mut topic) = bincode::deserialize::<Topic>(bytes)
        {
            // 实际追加 node_id（修复#6: 之前被忽略）
            if !topic.node_ids.contains(&node_id.to_string()) {
                topic.node_ids.push(node_id.to_string());
            }
            topic.updated_at = chrono::Utc::now().timestamp_millis();
            let bytes =
                bincode::serialize(&topic).map_err(|e| MemHopError::Storage(e.to_string()))?;
            env.topics
                .put(wtxn, &key, &bytes)
                .map_err(|e| MemHopError::Storage(e.to_string()))?;
        }
        Ok(())
    }

    /// Find all topics linked to a given domain_id.
    pub fn get_topics_by_domain(
        &self,
        txn: &heed::RoTxn<'_>,
        env: &L2Env,
        domain_id: &str,
    ) -> Result<Vec<Topic>> {
        let mut result = Vec::new();
        if let Ok(iter) = env.topics.iter(txn) {
            for (key, bytes) in iter.flatten() {
                if !key.starts_with("topic:") || !key.ends_with(":meta") {
                    continue;
                }
                if let Ok(t) = bincode::deserialize::<Topic>(bytes)
                    && t.linked_domain_ids.contains(&domain_id.to_string())
                {
                    result.push(t);
                }
            }
        }
        Ok(result)
    }

    pub fn add_topic_edge(
        &mut self,
        wtxn: &mut RwTxn<'_>,
        env: &L2Env,
        source_id: &str,
        target_id: &str,
        kind: TopicEdgeKind,
        weight: f32,
    ) -> Result<()> {
        let key = format!("topic_edge:{}:{}", source_id, target_id);
        let edge = TopicEdge {
            source_id: source_id.to_string(),
            target_id: target_id.to_string(),
            kind,
            weight,
            created_at: chrono::Utc::now().timestamp_millis(),
        };
        let bytes = bincode::serialize(&edge).map_err(|e| MemHopError::Storage(e.to_string()))?;
        env.topic_edges
            .put(wtxn, &key, &bytes)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        Ok(())
    }
}
