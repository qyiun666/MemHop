use std::collections::HashMap;
use heed::RwTxn;
use crate::engram::{Topic, TopicEdge};
use crate::types::TopicEdgeKind;
use crate::lmdb::L2Env;
use crate::error::{Result, MemHopError};

/// L2 话题标准图 — 话题级情景记忆（无状态，env 从外部传入）。
pub struct L2TopicGraph;

impl L2TopicGraph {
    pub fn new() -> Self { L2TopicGraph }

    pub fn find_or_create_topic(&mut self, wtxn: &mut RwTxn<'_>, env: &L2Env, label: &str) -> Result<(String, bool)> {
        // 先查 label→id 映射（避免 key 前缀不匹配）
        let lookup_key = format!("label:{}", label);
        if let Some(bytes) = env.topics.get(wtxn, &lookup_key).map_err(|e| MemHopError::Storage(e.to_string()))? {
            if let Ok(topic_id) = bincode::deserialize::<String>(bytes) {
                return Ok((topic_id, false)); // 已存在
            }
        }
        // 创建新 topic
        let now = chrono::Utc::now().timestamp_millis();
        let id = format!("topic_{}", now);
        let meta_key = format!("topic:{}:meta", &id);
        let topic = Topic {
            id: id.clone(), label: label.to_string(),
            summary: None, keywords: Vec::new(), centroid: Vec::new(),
            node_ids: Vec::new(), linked_domain_ids: Vec::new(),
            dialogue_range: None, created_at: now, updated_at: now,
            version: 1, history: Vec::new(),
        };
        let bytes = bincode::serialize(&topic).map_err(|e| MemHopError::Storage(e.to_string()))?;
        env.topics.put(wtxn, &meta_key, &bytes).map_err(|e| MemHopError::Storage(e.to_string()))?;
        // 写 label→id 映射
        let id_bytes = bincode::serialize(&id).map_err(|e| MemHopError::Storage(e.to_string()))?;
        env.topics.put(wtxn, &lookup_key, &id_bytes).map_err(|e| MemHopError::Storage(e.to_string()))?;
        Ok((id, true)) // 新创建
    }

    pub fn get_topic_by_id(&self, txn: &heed::RoTxn<'_>, env: &L2Env, id: &str) -> Result<Option<Topic>> {
        let key = format!("topic:{}:meta", id);
        match env.topics.get(txn, &key).map_err(|e| MemHopError::Storage(e.to_string()))? {
            Some(bytes) => Ok(Some(bincode::deserialize(bytes).map_err(|e| MemHopError::Storage(e.to_string()))?)),
            None => Ok(None),
        }
    }

    pub fn add_node_to_topic(&mut self, wtxn: &mut RwTxn<'_>, env: &L2Env,
        topic_id: &str, node_id: &str, _sparse: &HashMap<String, f32>) -> Result<()> {
        let key = format!("topic:{}:meta", topic_id);
        if let Some(bytes) = env.topics.get(wtxn, &key).map_err(|e| MemHopError::Storage(e.to_string()))? {
            if let Ok(mut topic) = bincode::deserialize::<Topic>(bytes) {
                // 实际追加 node_id（修复#6: 之前被忽略）
                if !topic.node_ids.contains(&node_id.to_string()) {
                    topic.node_ids.push(node_id.to_string());
                }
                topic.updated_at = chrono::Utc::now().timestamp_millis();
                let bytes = bincode::serialize(&topic).map_err(|e| MemHopError::Storage(e.to_string()))?;
                env.topics.put(wtxn, &key, &bytes).map_err(|e| MemHopError::Storage(e.to_string()))?;
            }
        }
        Ok(())
    }

    pub fn add_topic_edge(&mut self, wtxn: &mut RwTxn<'_>, env: &L2Env,
        source_id: &str, target_id: &str, kind: TopicEdgeKind, weight: f32) -> Result<()> {
        let key = format!("topic_edge:{}:{}", source_id, target_id);
        let edge = TopicEdge {
            source_id: source_id.to_string(), target_id: target_id.to_string(),
            kind, weight, created_at: chrono::Utc::now().timestamp_millis(),
        };
        let bytes = bincode::serialize(&edge).map_err(|e| MemHopError::Storage(e.to_string()))?;
        env.topic_edges.put(wtxn, &key, &bytes).map_err(|e| MemHopError::Storage(e.to_string()))?;
        Ok(())
    }
}
