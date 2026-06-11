//! mount_source — 非文件型知识源的统一挂载入口。
//! 支持通过 MountSourceInput 挂载 API JSON / 数据库导出 / 手动录入等知识源。

use crate::brain::Brain;
use crate::error::Result;
use crate::types::{MountSourceInput, ShelfMeta};
use crate::shelf::summarizer;

impl Brain {
    /// 挂载非文件型知识源。
    ///
    /// 流程：
    /// 1. 创建/获取 L3 domain
    /// 2. 对每个 item 做 summarize → 骨架节点写入
    /// 3. chunk → 详情节点写入（带 source_ref）
    /// 4. build hyperedges
    /// 5. 返回 ShelfMeta
    pub fn mount_source(&mut self, input: MountSourceInput) -> Result<ShelfMeta> {
        if input.domain_name.is_empty() {
            return Err(crate::error::MemHopError::InvalidArgument(
                "domain_name must not be empty".into(),
            ));
        }
        if input.items.is_empty() {
            return Err(crate::error::MemHopError::InvalidArgument(
                "items must not be empty".into(),
            ));
        }

        // 1. 创建 L3 domain
        self.ensure_l3()?;
        let now = chrono::Utc::now().timestamp_millis();
        let domain_id = format!("source_mount_{}", now);
        let store = self.redb_store.as_ref()
            .ok_or_else(|| crate::error::MemHopError::Storage("redb not available".into()))?;

        // 写入 DomainMeta
        {
            let meta = crate::types::DomainMeta {
                id: domain_id.clone(),
                name: input.domain_name.clone(),
                created_at: now,
                node_count: 0,
                updated_at: now,
                linked_topic_ids: Vec::new(),
                topic_weights: std::collections::HashMap::new(),
            };
            store.l3_store_domain_meta_v2(&domain_id, &meta)?;
        }

        // 2-3. 对每个 item: summarize → 骨架节点 → chunk → 详情节点
        use crate::types::StoreItem;
        let mut all_items: Vec<StoreItem> = Vec::new();

        for item in &input.items {
            // 骨架节点
            let summary = summarizer::summarize(&item.text, &input.domain);
            for sc in &summary.structural_nodes {
                let mut source_ref = sc.source_ref.clone();
                source_ref.kind = input.kind.clone();
                source_ref.location = item.source_ref.location.clone();
                all_items.push(StoreItem {
                    text: String::new(),
                    source: "mount_source".to_string(),
                    domain_id: Some(domain_id.clone()),
                    turn_id: None,
                    session_id: None,
                    topic_label: Some(input.domain_name.clone()),
                    llm_keywords: item.keywords.clone(),
                    llm_compressed_summary: None,
                    valence: None,
                    arousal: None,
                    chain_parent_id: None,
                    chain_label: None,
                    importance: None,
                    is_structural: Some(true),
                    source_ref: Some(source_ref),
                    skeletal_text: Some(sc.text.clone()),
                });
            }

            // 详情节点（使用原始文本作为单一 chunk）
            all_items.push(StoreItem {
                text: item.text.clone(),
                source: "mount_source".to_string(),
                domain_id: Some(domain_id.clone()),
                turn_id: None,
                session_id: None,
                topic_label: Some(input.domain_name.clone()),
                llm_keywords: item.keywords.clone(),
                llm_compressed_summary: None,
                valence: None,
                arousal: None,
                chain_parent_id: None,
                chain_label: None,
                importance: None,
                is_structural: Some(false),
                source_ref: Some(item.source_ref.clone()),
                skeletal_text: None,
            });
        }

        // 使用 batch_store 写入
        let total_items = all_items.len();
        let mut chunk_count = 0usize;
        let mut all_engram_ids = std::collections::HashMap::new();
        let mut all_l3_engram_ids = std::collections::HashMap::new();

        for item_batch in all_items.chunks(100) {
            let batch = crate::types::StoreBatch { items: item_batch.to_vec() };
            let report = self.batch_store(batch)?;
            for (idx, node_id) in report.engram_ids {
                let global_idx = format!("{}", chunk_count + idx.parse::<usize>().unwrap_or(0));
                all_engram_ids.insert(global_idx, node_id);
            }
            for (idx, node_id) in report.l3_engram_ids {
                let global_idx = format!("{}", chunk_count + idx.parse::<usize>().unwrap_or(0));
                all_l3_engram_ids.insert(global_idx, node_id);
            }
            chunk_count += item_batch.len();
        }

        // 4. Build hyperedges（所有条目作为一个超边）
        let store = self.redb_store.as_ref()
            .ok_or_else(|| crate::error::MemHopError::Storage("redb not available".into()))?;
        let _l3 = self.l3.as_mut()
            .ok_or_else(|| crate::error::MemHopError::Internal("L3 layer not initialized".into()))?;
        let all_node_ids: Vec<String> = (0..total_items)
            .filter_map(|i| all_l3_engram_ids.get(&i.to_string()).cloned())
            .collect();

        if all_node_ids.len() >= 2 {
            let he_id = crate::batch_store::unique_id("srchyp");
            let hyperedge = crate::engram::Hyperedge {
                id: format!("{}:{}", domain_id, he_id),
                node_ids: all_node_ids,
                kind: crate::types::HyperedgeKind::Association,
                weight: 0.5,
                created_at: now,
                updated_at: now,
                version: 1,
                history: Vec::new(),
                meta: std::collections::HashMap::new(),
                chain_prev: None,
                chain_next: None,
                chain_label: None,
            };
            let hyp_key = format!("hyp:{}:{}", domain_id, he_id);
            {
                let wtxn = store.begin_write()
                    .map_err(|e| crate::error::MemHopError::Storage(format!("begin_write: {}", e)))?;
                let mut hyp_table = wtxn.open_table(crate::storage::L3_DOMAIN_HYPEREDGES)
                    .map_err(|e| crate::error::MemHopError::Storage(format!("open L3_DOMAIN_HYPEREDGES: {}", e)))?;
                let bytes = bincode::serialize(&hyperedge)
                    .map_err(|e| crate::error::MemHopError::Internal(format!("serialize: {}", e)))?;
                hyp_table.insert(hyp_key.as_str(), bytes.as_slice())
                    .map_err(|e| crate::error::MemHopError::Storage(format!("insert: {}", e)))?;
                drop(hyp_table);
                wtxn.commit()
                    .map_err(|e| crate::error::MemHopError::Storage(format!("commit: {}", e)))?;
            }
        }

        // 5. 返回 ShelfMeta
        Ok(ShelfMeta {
            id: domain_id,
            path: format!("{:?}", input.kind),
            doc_type: input.domain,
            chunk_count,
            mounted_at: now,
            engram_ids: all_engram_ids,
            l3_engram_ids: all_l3_engram_ids,
        })
    }
}
