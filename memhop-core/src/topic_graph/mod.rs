use crate::engram::{Topic, TopicEdge};
use crate::error::{MemHopError, Result};
use crate::index::{HnswIndex, SparseIndexV2};
use crate::storage::store::RedbStore;
use crate::storage::{
    L2_TOPICS, L2_TOPIC_EDGES, L2_TOPIC_NGRAM_DOC_LEN, L2_TOPIC_NGRAM_FORWARD,
    L2_TOPIC_VECTOR_INDEX,
};
use crate::types::TopicEdgeKind;
use half::f16;
use redb::ReadableTable;
use std::collections::HashMap;

const VECTOR_INDEX_KEY: &str = "topic_vectors_v1";

/// L2 话题标准图 — 话题级情景记忆（含向量索引，env 从外部传入）。
pub struct L2TopicGraph {
    /// Topic centroid 向量索引，用于 cosine 粗筛。
    pub topic_vectors: HnswIndex,
    /// v0.22.0: 保存初始 HNSW 配置，rebuild 时复用（避免丢失 for_scale 自适应参数）。
    config: crate::index::MemHopHnswConfig,
    /// v1.0: topic ngram 倒排索引（替代 search_l2 的线性扫描）
    pub ngram_index: SparseIndexV2,
}

impl L2TopicGraph {
    pub fn new() -> Self {
        L2TopicGraph {
            topic_vectors: HnswIndex::default(),
            config: crate::index::MemHopHnswConfig::default(),
            ngram_index: SparseIndexV2::with_tables(None, L2_TOPIC_NGRAM_FORWARD, L2_TOPIC_NGRAM_DOC_LEN),
        }
    }

    /// v0.16.0: 使用指定维度创建。
    pub fn with_dim(dim: usize) -> Self {
        L2TopicGraph {
            topic_vectors: HnswIndex::new(dim),
            config: crate::index::MemHopHnswConfig::default(),
            ngram_index: SparseIndexV2::with_tables(None, L2_TOPIC_NGRAM_FORWARD, L2_TOPIC_NGRAM_DOC_LEN),
        }
    }

    /// v0.18.0: 使用指定维度和配置创建。
    pub fn with_dim_and_config(dim: usize, config: crate::index::MemHopHnswConfig) -> Self {
        L2TopicGraph {
            topic_vectors: HnswIndex::new_with_config(dim, config.clone()),
            config,
            ngram_index: SparseIndexV2::with_tables(None, L2_TOPIC_NGRAM_FORWARD, L2_TOPIC_NGRAM_DOC_LEN),
        }
    }

    /// 从 redb 重建 topic 向量索引。
    pub fn rebuild_topic_vectors(&mut self, store: &RedbStore) -> Result<()> {
        let _timer = std::time::Instant::now();
        let rtxn = store.begin_read()
            .map_err(|e| MemHopError::Storage(format!("begin_read: {}", e)))?;

        // 从持久化加载
        if let Ok(Some(bytes)) = rtxn.open_table(L2_TOPIC_VECTOR_INDEX)
            .and_then(|table| table.get(VECTOR_INDEX_KEY).map_err(redb::TableError::Storage))
            && let Some(idx) = HnswIndex::from_bytes(bytes.value())
        {
                self.topic_vectors = idx;
                eprintln!("[memhop] L2 rebuild_topic_vectors: loaded from persistent index in {}ms", _timer.elapsed().as_millis());
                return Ok(());
            }

        // 回退：从 topic centroid 重建（保留现有维度）
        let dim = self.topic_vectors.dims();
        self.topic_vectors = if dim > 0 {
            HnswIndex::new_with_config(dim, self.config.clone())
        } else {
            HnswIndex::default()
        };
        let table = match rtxn.open_table(L2_TOPICS) {
            Ok(t) => t,
            Err(_) => return Ok(()),
        };
        let mut count = 0u64;
        for (key, bytes) in table.iter()
            .map_err(|e| MemHopError::Storage(format!("iter L2_TOPICS: {}", e)))?
            .flatten()
        {
                let k = key.value();
                if !k.starts_with("topic:") || !k.ends_with(":meta") {
                    continue;
                }
                if let Ok(t) = bincode::deserialize::<Topic>(bytes.value())
                    && !t.centroid.is_empty()
                {
                    self.topic_vectors.add(&t.id, &t.centroid);
                    count += 1;
                }
            }
        eprintln!("[memhop] L2 rebuild_topic_vectors: {} topics in {}ms", count, _timer.elapsed().as_millis());
        Ok(())
    }

    /// 持久化 topic 向量索引到 redb。
    pub fn persist_topic_vectors(&self, store: &RedbStore) -> Result<()> {
        let bytes = self.topic_vectors.to_bytes();
        let wtxn = store.begin_write()?;
        {
            let mut table = wtxn.open_table(L2_TOPIC_VECTOR_INDEX)
                .map_err(|e| MemHopError::Storage(format!("open L2_TOPIC_VECTOR_INDEX: {}", e)))?;
            table.insert(VECTOR_INDEX_KEY, bytes.as_slice())
                .map_err(|e| MemHopError::Storage(format!("persist topic vectors: {}", e)))?;
        }
        wtxn.commit().map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;
        Ok(())
    }

    /// 计算并更新 topic 的 centroid（所有成员 node 向量均值）。
    pub fn update_topic_centroid(
        &mut self,
        store: &RedbStore,
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
        let wtxn = store.begin_write()?;
        {
            let mut table = wtxn.open_table(L2_TOPICS)
                .map_err(|e| MemHopError::Storage(format!("open L2_TOPICS: {}", e)))?;
            let existing: Option<Topic> = match table.get(key.as_str())
                .map_err(|e| MemHopError::Storage(format!("get: {}", e)))?
            {
                Some(bytes) => bincode::deserialize::<Topic>(bytes.value()).ok(),
                None => None,
            };
            if let Some(mut t) = existing {
                t.centroid = centroid.clone();
                let new_bytes = bincode::serialize(&t)?;
                table.insert(key.as_str(), new_bytes.as_slice())
                    .map_err(|e| MemHopError::Storage(format!("insert: {}", e)))?;
            }
        }
        wtxn.commit().map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;

        // 更新向量索引
        self.topic_vectors.update(topic_id, &centroid);
        Ok(())
    }

    /// Cosine 搜索 topic centroids，返回 (topic_id, score) 列表。
    pub fn search_by_vector(&self, query: &[f16], top_k: usize) -> Vec<(String, f32)> {
        self.topic_vectors.cosine_search(query, top_k)
    }

    /// v0.25.0: 已迁移到 redb，使用 store.l2_get_topic()。
    pub fn find_or_create_topic(
        &mut self,
        store: &RedbStore,
        label: &str,
    ) -> Result<(String, bool)> {
        // 先查 label→id 映射（避免 key 前缀不匹配）
        let lookup_key = format!("label:{}", label);
        let rtxn = store.begin_read()?;
        let table = rtxn.open_table(L2_TOPICS)
            .map_err(|e| MemHopError::Storage(format!("open L2_TOPICS: {}", e)))?;
        let existing_id: Option<String> = match table.get(lookup_key.as_str())
            .map_err(|e| MemHopError::Storage(format!("get: {}", e)))?
        {
            Some(bytes) => bincode::deserialize(bytes.value()).ok(),
            None => None,
        };
        drop(table);
        drop(rtxn);

        if let Some(topic_id) = existing_id {
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
        let wtxn = store.begin_write()?;
        {
            let mut table = wtxn.open_table(L2_TOPICS)
                .map_err(|e| MemHopError::Storage(format!("open L2_TOPICS: {}", e)))?;
            let bytes = bincode::serialize(&topic)?;
            table.insert(meta_key.as_str(), bytes.as_slice())
                .map_err(|e| MemHopError::Storage(format!("insert topic: {}", e)))?;
            // 写 label→id 映射
            let id_bytes = bincode::serialize(&id)?;
            table.insert(lookup_key.as_str(), id_bytes.as_slice())
                .map_err(|e| MemHopError::Storage(format!("insert topic lookup: {}", e)))?;
        }
        wtxn.commit().map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;

        // 增量更新 ngram 倒排索引
        let text = format!(
            "{} {} {}",
            topic.label,
            topic.keywords.join(" "),
            topic.summary.as_deref().unwrap_or("")
        );
        let sparse = build_topic_ngram_sparse(&text);
        self.ngram_index.add(&id, &sparse, text.len())?;

        Ok((id, true)) // 新创建
    }

    /// v0.25.0: 已迁移到 redb，使用 store.l2_get_topic()。
    pub fn get_topic_by_id(
        &self,
        store: &RedbStore,
        id: &str,
    ) -> Result<Option<Topic>> {
        let key = format!("topic:{}:meta", id);
        let txn = store.begin_read()?;
        let table = txn.open_table(L2_TOPICS)
            .map_err(|e| MemHopError::Storage(format!("open L2_TOPICS: {}", e)))?;
        match table.get(key.as_str())
            .map_err(|e| MemHopError::Storage(format!("get: {}", e)))?
        {
            Some(bytes) => Ok(Some(
                bincode::deserialize(bytes.value())?,
            )),
            None => Ok(None),
        }
    }

    /// v0.25.0: 已迁移到 redb，使用 store.l2_store_topic()。
    pub fn add_node_to_topic(
        &mut self,
        store: &RedbStore,
        topic_id: &str,
        node_id: &str,
        _sparse: &HashMap<String, f32>,
    ) -> Result<()> {
        let key = format!("topic:{}:meta", topic_id);
        let wtxn = store.begin_write()?;
        {
            let mut table = wtxn.open_table(L2_TOPICS)
                .map_err(|e| MemHopError::Storage(format!("open L2_TOPICS: {}", e)))?;
            let existing: Option<Topic> = match table.get(key.as_str())
                .map_err(|e| MemHopError::Storage(format!("get: {}", e)))?
            {
                Some(bytes) => bincode::deserialize::<Topic>(bytes.value()).ok(),
                None => None,
            };
            if let Some(mut topic) = existing {
                if !topic.node_ids.contains(&node_id.to_string()) {
                    topic.node_ids.push(node_id.to_string());
                }
                topic.updated_at = chrono::Utc::now().timestamp_millis();
                let bytes = bincode::serialize(&topic)?;
                table.insert(key.as_str(), bytes.as_slice())
                    .map_err(|e| MemHopError::Storage(format!("insert: {}", e)))?;
            }
        }
        wtxn.commit().map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;
        Ok(())
    }

    /// v0.25.0: 已迁移到 redb，使用 store.l2_get_topic() / store.l2_list_topics()。
    pub fn get_topics_by_domain(
        &self,
        store: &RedbStore,
        domain_id: &str,
    ) -> Result<Vec<Topic>> {
        let mut result = Vec::new();
        let txn = store.begin_read()?;
        let table = match txn.open_table(L2_TOPICS) {
            Ok(t) => t,
            Err(_) => return Ok(Vec::new()),
        };
        if let Ok(iter) = table.iter() {
            for item in iter.flatten() {
                let (key, bytes) = item;
                let k = key.value();
                if !k.starts_with("topic:") || !k.ends_with(":meta") {
                    continue;
                }
                if let Ok(t) = bincode::deserialize::<Topic>(bytes.value())
                    && t.linked_domain_ids.contains(&domain_id.to_string())
                {
                    result.push(t);
                }
            }
        }
        Ok(result)
    }

    /// v0.25.0: 已迁移到 redb。
    pub fn add_topic_edge(
        &mut self,
        store: &RedbStore,
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
        let wtxn = store.begin_write()?;
        {
            let mut table = wtxn.open_table(L2_TOPIC_EDGES)
                .map_err(|e| MemHopError::Storage(format!("open L2_TOPIC_EDGES: {}", e)))?;
            let bytes = bincode::serialize(&edge)?;
            table.insert(key.as_str(), bytes.as_slice())
                .map_err(|e| MemHopError::Storage(format!("insert edge: {}", e)))?;
        }
        wtxn.commit().map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;
        Ok(())
    }

    /// 从 redb 重建 topic ngram 倒排索引（启动时调用）
    pub fn rebuild_ngram_index(&mut self, store: &RedbStore) -> Result<()> {
        let _timer = std::time::Instant::now();

        self.ngram_index = SparseIndexV2::with_tables(
            Some(store.db_arc()),
            L2_TOPIC_NGRAM_FORWARD,
            L2_TOPIC_NGRAM_DOC_LEN,
        );

        let topics = store.l2_list_topics()?;
        let mut batch_items: Vec<(String, HashMap<String, f32>, usize)> = Vec::new();

        for topic in &topics {
            let text = format!(
                "{} {} {}",
                topic.label,
                topic.keywords.join(" "),
                topic.summary.as_deref().unwrap_or("")
            );
            if text.trim().is_empty() {
                continue;
            }
            // 构建 ngram sparse 表示（用简单的 ngram 频率）
            let doc_len = text.len();
            let sparse = build_topic_ngram_sparse(&text);
            batch_items.push((topic.id.clone(), sparse, doc_len));
        }

        let batch_refs: Vec<(&str, &HashMap<String, f32>, usize)> = batch_items
            .iter()
            .map(|(id, sparse, len)| (id.as_str(), sparse, *len))
            .collect();
        self.ngram_index.add_batch(store, &batch_refs)?;

        eprintln!(
            "[memhop] L2 rebuild_ngram_index: {} topics in {}ms",
            batch_items.len(),
            _timer.elapsed().as_millis()
        );
        Ok(())
    }

    /// 使用 ngram 倒排索引搜索话题（替代线性扫描）
    pub fn search_by_ngram_index(
        &self,
        query_sparse: &HashMap<String, f32>,
        top_k: usize,
    ) -> Result<Vec<(String, f32)>> {
        let idf = self.ngram_index.idf_map();
        self.ngram_index.bm25_search(query_sparse, &idf, top_k)
    }
}

/// 从话题文本构建 ngram sparse 权重（2-gram 和 3-gram 字符级）
fn build_topic_ngram_sparse(text: &str) -> HashMap<String, f32> {
    let mut sparse = HashMap::new();
    let chars: Vec<char> = text.to_lowercase().chars().collect();
    // 2-grams
    for w in chars.windows(2) {
        let ngram: String = w.iter().collect();
        *sparse.entry(ngram).or_insert(0.0) += 1.0;
    }
    // 3-grams
    for w in chars.windows(3) {
        let ngram: String = w.iter().collect();
        *sparse.entry(ngram).or_insert(0.0) += 1.0;
    }
    sparse
}
