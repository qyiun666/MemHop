use crate::brain::Brain;
use crate::engram::Topic;
use crate::error::{MemHopError, Result};

impl Brain {
    /// 列出所有 L2 Topic
    pub fn list_topics(&mut self) -> Result<Vec<Topic>> {
        match self.redb_store.as_ref() {
            Some(store) => store.l2_list_topics(),
            None => Ok(Vec::new()),
        }
    }

    /// v0.20.0: 按 topic_id 查询单个话题。
    /// 返回 Ok(None) 表示 topic 不存在（非错误），与 get_crystal 语义一致。
    pub fn get_topic(&mut self, topic_id: &str) -> Result<Option<Topic>> {
        match self.redb_store.as_ref() {
            Some(store) => store.l2_get_topic(topic_id),
            None => Ok(None),
        }
    }

    /// v0.17.0: LLM plan compression 后更新 topic 的摘要/关键词/扩展元数据。
    pub fn update_topic(
        &mut self,
        topic_id: &str,
        summary: Option<String>,
        keywords: Option<Vec<String>>,
        extended_meta: Option<std::collections::HashMap<String, String>>,
    ) -> Result<()> {
        let store = self.redb_store
            .as_ref()
            .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;
        let mut topic = store.l2_get_topic(topic_id)?
            .ok_or_else(|| MemHopError::NotFound(format!("topic {} not found", topic_id)))?;
        if let Some(ref s) = summary {
            topic.summary = Some(s.clone());
        }
        if let Some(ref kw) = keywords {
            topic.keywords = kw.clone();
        }
        if let Some(ref meta) = extended_meta {
            topic.extended_meta = meta.clone();
        }
        topic.updated_at = chrono::Utc::now().timestamp_millis();
        store.l2_store_topic(&topic)
    }
}
