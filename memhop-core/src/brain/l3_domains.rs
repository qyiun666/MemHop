use crate::brain::Brain;
use crate::engram::KnowledgeNode;
use crate::error::{MemHopError, Result};
use crate::types::{CrystallizeL3Report, CrystallizeL3Request, DomainMeta, L3PathInfo, Layer, NodeSource};
use half::f16;
use std::collections::HashMap;

impl Brain {
    /// 列出 L3 领域路径
    pub fn list_l3_paths(&mut self) -> Result<Vec<L3PathInfo>> {
        let store = self.redb_store
            .as_ref()
            .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;
        let domain_ids = store.l3_list_paths()?;
        let mut paths = Vec::new();
        for id in domain_ids {
            let meta_key = format!("meta:{}", id);
            let rtxn = store
                .begin_read()
                .map_err(|e| MemHopError::Storage(format!("begin_read: {}", e)))?;
            let table = rtxn
                .open_table(crate::storage::L3_DOMAIN_META)
                .map_err(|e| MemHopError::Storage(format!("open L3_DOMAIN_META: {}", e)))?;
            if let Some(bytes) = table
                .get(meta_key.as_str())
                .map_err(|e| MemHopError::Storage(format!("get meta: {}", e)))?
                && let Ok(meta) = serde_json::from_slice::<serde_json::Value>(bytes.value())
            {
                paths.push(L3PathInfo {
                    domain_id: meta["id"].as_str().unwrap_or("").to_string(),
                    name: meta["name"].as_str().unwrap_or("").to_string(),
                    node_count: meta["node_count"].as_u64().unwrap_or(0),
                    mounted_at: meta["created_at"].as_i64().unwrap_or(0),
                });
            }
        }
        Ok(paths)
    }

    /// 将 L2 话题"结晶"为 L3 高层领域知识。
    /// meowAgent 负责 LLM 总结生成 summary + keywords；
    /// MemHop 负责创建/更新 L3 domain 节点、更新 L2 的 linked_domain_ids。
    pub fn crystallize_l3(&mut self, req: &CrystallizeL3Request) -> Result<CrystallizeL3Report> {
        req.validate()?;

        // 1. 验证 topic 存在（从 redb 读取）
        self.ensure_l2()?;
        self.ensure_l3()?;
        let store = self.redb_store
            .as_ref()
            .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;
        let topic = store.l2_get_topic(&req.topic_id)?
            .ok_or_else(|| MemHopError::NotFound(format!("topic {}", req.topic_id)))?;

        let domain_name = req
            .domain_name
            .clone()
            .unwrap_or_else(|| topic.label.clone());
        let domain_id = format!("crystallized_{}", req.topic_id);

        // 2. 创建/获取 L3 domain（使用 redb）
        let l3 = self.l3.as_mut()
            .ok_or_else(|| MemHopError::Internal("L3 layer not initialized".into()))?;

        // 检查 domain_meta 是否已存在
        let rtxn = store
            .begin_read()
            .map_err(|e| MemHopError::Storage(format!("begin_read: {}", e)))?;
        let meta_table = rtxn
            .open_table(crate::storage::L3_DOMAIN_META)
            .map_err(|e| MemHopError::Storage(format!("open L3_DOMAIN_META: {}", e)))?;
        let meta_key = format!("meta:{}", domain_id);
        let exists = meta_table
            .get(meta_key.as_str())
            .map_err(|e| MemHopError::Storage(format!("get meta: {}", e)))?
            .is_some();
        drop(meta_table);
        drop(rtxn);

        if !exists {
            // 使用强类型 DomainMeta（替代 serde_json::Value）
            let now = chrono::Utc::now().timestamp_millis();
            let meta = DomainMeta {
                id: domain_id.clone(),
                name: domain_name.clone(),
                created_at: now,
                node_count: 0,
                updated_at: now,
                linked_topic_ids: Vec::new(),
                topic_weights: HashMap::new(),
            };
            store.l3_store_domain_meta_v2(&domain_id, &meta)?;
        }

        // 3. 将 summary + keywords 编码后写入 L3 node
        let encoded = self.encoder.encode(&req.summary);
        let l3_node_id = crate::batch_store::unique_id("l3n");
        {
            let wtxn = store
                .begin_write()
                .map_err(|e| MemHopError::Storage(format!("begin_write: {}", e)))?;

            let mut add_l3_node = |node_id: &str, domain_id: &str, text: &str, sparse: &HashMap<String, f32>, vector: Vec<f16>| -> Result<()> {
                let node = KnowledgeNode::new(
                    node_id.to_string(),
                    text.to_string(),
                    sparse.clone(),
                    vector.clone(),
                    Layer::L3,
                    NodeSource::KnowledgeMount,
                );
                let key = format!("node:{}:{}", domain_id, node_id);
                let bytes = bincode::serialize(&node)
                    .map_err(|e| MemHopError::Internal(format!("serialize: {}", e)))?;
                let mut table = wtxn
                    .open_table(crate::storage::L3_DOMAIN_NODES)
                    .map_err(|e| MemHopError::Storage(format!("open L3_DOMAIN_NODES: {}", e)))?;
                table
                    .insert(key.as_str(), bytes.as_slice())
                    .map_err(|e| MemHopError::Storage(format!("insert node: {}", e)))?;
                drop(table);
                l3.bm25.add(node_id, sparse, text.len())?;
                if !vector.is_empty() && vector.len() > 1 {
                    l3.vector_index.add(node_id, &vector);
                }
                Ok(())
            };

            add_l3_node(&l3_node_id, &domain_id, &req.summary, &encoded.sparse, encoded.dense)?;

            for kw in &req.keywords {
                let kw_encoded = self.encoder.encode(kw);
                let kw_id = crate::batch_store::unique_id("l3n");
                add_l3_node(&kw_id, &domain_id, kw, &kw_encoded.sparse, kw_encoded.dense)?;
            }

            wtxn
                .commit()
                .map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;
        }

        // 4. 更新 L2 topic 的 linked_domain_ids + domain_weights（通过 redb）
        let mut topic = store.l2_get_topic(&req.topic_id)?
            .ok_or_else(|| MemHopError::NotFound(format!("topic {}", req.topic_id)))?;
        if !topic.linked_domain_ids.contains(&domain_id) {
            topic.linked_domain_ids.push(domain_id.clone());
        }
        let weight = topic.domain_weights.get(&domain_id).copied().unwrap_or(0.0);
        topic.domain_weights.insert(domain_id.clone(), weight + 1.0);
        topic.updated_at = chrono::Utc::now().timestamp_millis();
        store.l2_store_topic(&topic)?;

        // 5. 更新 L3 domain_to_topics 内存索引
        if let Some(ref mut l3_graph) = self.l3 {
            l3_graph.add_domain_topic_link(&domain_id, &req.topic_id);
        }

        Ok(CrystallizeL3Report {
            domain_id,
            domain_name,
            l3_nodes_created: 1 + req.keywords.len() as u32,
            topic_linked: true,
        })
    }
}
