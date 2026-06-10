use crate::engram::RawDocument;
use crate::error::{MemHopError, Result};
use crate::storage::store::RedbStore;
use crate::storage::{L4_DOCS, L4_SESSION_INDEX, L4_TURN_INDEX};
use half::f16;
use redb::ReadableTable;

/// v0.22.0: L4 原文库 — 仅存储，无向量索引（HNSW 已移除，检索走 ngram overlap）。
pub struct L4RawArchive;

impl L4RawArchive {
    pub fn new() -> Self {
        L4RawArchive
    }

    pub fn store(
        &mut self,
        store: &RedbStore,
        text: &str,
        source: &str,
        turn_id: Option<&str>,
        session_id: Option<&str>,
    ) -> Result<String> {
        let now = chrono::Utc::now().timestamp_millis();
        let id = format!("l4d_{}", now);
        self.store_with_id(
            store,
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
        store: &RedbStore,
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
        let bytes = bincode::serialize(&doc)?;

        let wtxn = store.begin_write()?;
        {
            let mut docs_table = wtxn.open_table(L4_DOCS)
                .map_err(|e| MemHopError::Storage(format!("open L4_DOCS: {}", e)))?;
            docs_table.insert(id.as_str(), bytes.as_slice())
                .map_err(|e| MemHopError::Storage(format!("insert doc: {}", e)))?;
            drop(docs_table);

            if let Some(tid) = turn_id {
                let mut turn_table = wtxn.open_table(L4_TURN_INDEX)
                    .map_err(|e| MemHopError::Storage(format!("open L4_TURN_INDEX: {}", e)))?;
                turn_table.insert(tid, id.as_bytes())
                    .map_err(|e| MemHopError::Storage(format!("insert turn: {}", e)))?;
                drop(turn_table);
            }

            if let Some(sid) = session_id {
                let skey = format!("session:{}", sid);
                let mut session_table = wtxn.open_table(L4_SESSION_INDEX)
                    .map_err(|e| MemHopError::Storage(format!("open L4_SESSION_INDEX: {}", e)))?;
                let existing: Vec<String> = match session_table.get(skey.as_str())
                    .map_err(|e| MemHopError::Storage(format!("get session: {}", e)))?
                {
                    Some(b) => bincode::deserialize(b.value()).unwrap_or_default(),
                    None => Vec::new(),
                };
                let mut ids = existing;
                ids.push(id.clone());
                let bytes = bincode::serialize(&ids)?;
                session_table.insert(skey.as_str(), bytes.as_slice())
                    .map_err(|e| MemHopError::Storage(format!("insert session: {}", e)))?;
            }
        }
        wtxn.commit().map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;
        Ok(id)
    }

    /// v0.23.1: 按 session_id 获取所有文档
    pub fn get_by_session(
        &self,
        store: &RedbStore,
        session_id: &str,
    ) -> Result<Vec<RawDocument>> {
        let skey = format!("session:{}", session_id);
        let txn = store.begin_read()?;
        let session_table = txn.open_table(L4_SESSION_INDEX)
            .map_err(|e| MemHopError::Storage(format!("open L4_SESSION_INDEX: {}", e)))?;
        let ids: Vec<String> = match session_table.get(skey.as_str())
            .map_err(|e| MemHopError::Storage(format!("get session: {}", e)))?
        {
            Some(b) => bincode::deserialize(b.value()).unwrap_or_default(),
            None => return Ok(Vec::new()),
        };
        drop(session_table);

        let docs_table = txn.open_table(L4_DOCS)
            .map_err(|e| MemHopError::Storage(format!("open L4_DOCS: {}", e)))?;
        let mut docs = Vec::with_capacity(ids.len());
        for id in &ids {
            if let Ok(Some(bytes)) = docs_table.get(id.as_str())
                && let Ok(doc) = bincode::deserialize::<RawDocument>(bytes.value())
            {
                docs.push(doc);
            }
        }
        Ok(docs)
    }

    /// v0.23.1: 按 topic 的 doc_ids 批量获取原文
    pub fn get_by_ids(
        &self,
        store: &RedbStore,
        doc_ids: &[String],
    ) -> Result<Vec<RawDocument>> {
        let txn = store.begin_read()?;
        let docs_table = txn.open_table(L4_DOCS)
            .map_err(|e| MemHopError::Storage(format!("open L4_DOCS: {}", e)))?;
        let mut docs = Vec::with_capacity(doc_ids.len());
        for id in doc_ids {
            if let Ok(Some(bytes)) = docs_table.get(id.as_str())
                && let Ok(doc) = bincode::deserialize::<RawDocument>(bytes.value())
            {
                docs.push(doc);
            }
        }
        Ok(docs)
    }
}
