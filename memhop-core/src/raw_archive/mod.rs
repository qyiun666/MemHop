use crate::engram::RawDocument;
use crate::error::Result;
use crate::lmdb::L4Env;
use half::f16;

/// v0.22.0: L4 原文库 — 仅存储，无向量索引（HNSW 已移除，检索走 ngram overlap）。
pub struct L4RawArchive;

impl L4RawArchive {
    pub fn new() -> Self {
        L4RawArchive
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
        let bytes = bincode::serialize(&doc)?;
        env.docs
            .put(wtxn, &id, &bytes)
            ?;
        if let Some(tid) = turn_id {
            env.turn_index
                .put(wtxn, tid, id.as_bytes())
                ?;
        }
        if let Some(sid) = session_id {
            let skey = format!("session:{}", sid);
            let existing = env
                .session_index
                .get(wtxn, &skey)
                ?;
            let mut ids: Vec<String> = match existing {
                Some(b) => bincode::deserialize(b).unwrap_or_default(),
                None => Vec::new(),
            };
            ids.push(id.clone());
            let bytes =
                bincode::serialize(&ids)?;
            env.session_index
                .put(wtxn, &skey, &bytes)
                ?;
        }
        Ok(id)
    }

    /// v0.23.1: 按 session_id 获取所有文档
    pub fn get_by_session(
        &self,
        txn: &heed::RoTxn<'_>,
        env: &L4Env,
        session_id: &str,
    ) -> Result<Vec<RawDocument>> {
        let skey = format!("session:{}", session_id);
        let ids: Vec<String> = match env
            .session_index
            .get(txn, &skey)
            ?
        {
            Some(b) => bincode::deserialize(b).unwrap_or_default(),
            None => return Ok(Vec::new()),
        };

        let mut docs = Vec::with_capacity(ids.len());
        for id in &ids {
            if let Ok(Some(bytes)) = env.docs.get(txn, id)
                && let Ok(doc) = bincode::deserialize::<RawDocument>(bytes)
            {
                docs.push(doc);
            }
        }
        Ok(docs)
    }

    /// v0.23.1: 按 topic 的 doc_ids 批量获取原文
    pub fn get_by_ids(
        &self,
        txn: &heed::RoTxn<'_>,
        env: &L4Env,
        doc_ids: &[String],
    ) -> Result<Vec<RawDocument>> {
        let mut docs = Vec::with_capacity(doc_ids.len());
        for id in doc_ids {
            if let Ok(Some(bytes)) = env.docs.get(txn, id)
                && let Ok(doc) = bincode::deserialize::<RawDocument>(bytes)
            {
                docs.push(doc);
            }
        }
        Ok(docs)
    }
}
