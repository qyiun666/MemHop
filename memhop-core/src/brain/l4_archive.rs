use crate::brain::Brain;
use crate::engram::RawDocument;
use crate::error::Result;

impl Brain {
    /// v0.23.1: 按 session_id 获取 L4 原文文档
    pub fn get_l4_by_session(&mut self, session_id: &str) -> Result<Vec<RawDocument>> {
        if let Some(ref store) = self.redb_store {
            return store.l4_get_by_session(session_id);
        }
        Ok(Vec::new())
    }

    /// v0.23.1: 按 topic_id 获取关联的 L4 原文文档
    /// 先从 L2 获取 topic 的 node_ids，再从 L4 获取对应的文档
    pub fn get_l4_by_topic(&mut self, topic_id: &str) -> Result<Vec<RawDocument>> {
        if let Some(ref store) = self.redb_store {
            let topic = store.l2_get_topic(topic_id)?;
            match topic {
                Some(t) => {
                    if t.doc_ids.is_empty() {
                        return Ok(Vec::new());
                    }
                    return store.l4_get_by_ids(&t.doc_ids);
                }
                None => return Ok(Vec::new()),
            }
        }
        Ok(Vec::new())
    }

    /// v0.22.0: 获取 L4 文档计数（redb 直接统计）。
    pub fn l4_doc_count(&self) -> usize {
        self.redb_store
            .as_ref()
            .and_then(|store| store.l4_doc_count().ok())
            .unwrap_or(0) as usize
    }

    /// 获取 L4 原文
    pub fn get_l4_raw(&mut self, doc_id: &str) -> Result<Option<RawDocument>> {
        match self.redb_store.as_ref() {
            Some(store) => store.l4_get_doc(doc_id),
            None => Ok(None),
        }
    }
}
