use crate::engram::RawDocument;
use crate::error::{MemHopError, Result};
use crate::index::HnswIndex;
use crate::lmdb::L4Env;
use half::f16;

/// L4 原文库 — 原始对话存储（含向量索引，env 从外部传入）。
pub struct L4RawArchive {
    pub vector_index: HnswIndex,
}

impl L4RawArchive {
    pub fn new() -> Self {
        L4RawArchive {
            vector_index: HnswIndex::default(),
        }
    }

    /// v0.16.0: 使用指定维度创建。
    pub fn with_dim(dim: usize) -> Self {
        L4RawArchive {
            vector_index: HnswIndex::new(dim),
        }
    }

    /// v0.18.0: 使用指定维度和配置创建。
    pub fn with_dim_and_config(dim: usize, config: crate::index::HnswConfig) -> Result<Self> {
        Ok(L4RawArchive {
            vector_index: HnswIndex::new_with_config(dim, config)?,
        })
    }

    /// 从 LMDB 重建向量索引（保留现有维度）。
    pub fn rebuild_vector_index(&mut self, env: &L4Env) -> Result<()> {
        let _timer = std::time::Instant::now();
        let dim = self.vector_index.dims();
        let txn = env
            .env
            .read_txn()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        self.vector_index = if dim > 0 {
            HnswIndex::new(dim)
        } else {
            HnswIndex::default()
        };
        let mut count = 0u64;
        if let Ok(iter) = env.docs.iter(&txn) {
            for item in iter {
                if let Ok((_key, bytes)) = item
                    && let Ok(doc) = bincode::deserialize::<RawDocument>(bytes)
                    && !doc.vector.is_empty()
                {
                    self.vector_index.add(&doc.id, &doc.vector);
                    count += 1;
                }
            }
        }
        eprintln!("[memhop] L4 rebuild_vector_index: {} docs in {}ms", count, _timer.elapsed().as_millis());
        Ok(())
    }

    /// Cosine 搜索 L4 文档，返回 (doc_id, score) 列表。
    pub fn search_by_vector(&self, query: &[f16], top_k: usize) -> Vec<(String, f32)> {
        self.vector_index.cosine_search(query, top_k)
    }

    pub fn store(
        &mut self,
        wtxn: &mut heed::RwTxn<'_>,
        env: &L4Env,
        text: &str,
        source: &str,
        turn_id: Option<&str>,
        session_id: Option<&str>,
    ) -> Result<String> {
        let now = chrono::Utc::now().timestamp_millis();
        let id = format!("l4d_{}", now);
        self.store_with_id(
            wtxn,
            env,
            &id,
            text,
            source,
            turn_id,
            session_id,
            Vec::new(),
        )
    }

    /// Store with a caller-provided unique ID.
    #[allow(clippy::too_many_arguments)]
    pub fn store_with_id(
        &mut self,
        wtxn: &mut heed::RwTxn<'_>,
        env: &L4Env,
        id: &str,
        text: &str,
        source: &str,
        turn_id: Option<&str>,
        session_id: Option<&str>,
        vector: Vec<f16>,
    ) -> Result<String> {
        let now = chrono::Utc::now().timestamp_millis();
        let id = id.to_string();
        let doc = RawDocument {
            id: id.clone(),
            text: text.to_string(),
            turn_id: turn_id.map(|s| s.to_string()),
            session_id: session_id.map(|s| s.to_string()),
            source: source.to_string(),
            created_at: now,
            version: 1,
            history: Vec::new(),
            vector: vector.clone(),
        };
        let bytes = bincode::serialize(&doc).map_err(|e| MemHopError::Storage(e.to_string()))?;
        env.docs
            .put(wtxn, &id, &bytes)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        // 更新向量索引
        if !vector.is_empty() {
            self.vector_index.add(&id, &vector);
        }

        if let Some(tid) = turn_id {
            env.turn_index
                .put(wtxn, tid, id.as_bytes())
                .map_err(|e| MemHopError::Storage(e.to_string()))?;
        }
        if let Some(sid) = session_id {
            let skey = format!("session:{}", sid);
            let existing = env
                .session_index
                .get(wtxn, &skey)
                .map_err(|e| MemHopError::Storage(e.to_string()))?;
            let mut ids: Vec<String> = match existing {
                Some(b) => bincode::deserialize(b).unwrap_or_default(),
                None => Vec::new(),
            };
            ids.push(id.clone());
            let bytes =
                bincode::serialize(&ids).map_err(|e| MemHopError::Storage(e.to_string()))?;
            env.session_index
                .put(wtxn, &skey, &bytes)
                .map_err(|e| MemHopError::Storage(e.to_string()))?;
        }
        Ok(id)
    }
}
