//! 记忆再巩固管理器 — 每次 recall 命中标记 labile，Dream 时重新编码

use crate::error::{MemHopError, Result};
use crate::storage::store::RedbStore;
use crate::types::ReconsolidationEntry;
use std::collections::HashMap;

/// 记忆再巩固管理器
#[derive(Debug, Default)]
pub struct ReconsolidationManager {
    /// node_id → 再巩固条目
    pub entries: HashMap<String, ReconsolidationEntry>,
}

/// 再巩固统计
#[derive(Debug, Clone, Default)]
pub struct ReconsolidationStats {
    pub labile_count: usize,
    pub reconsolidated_count: u32,
}

impl ReconsolidationManager {
    pub fn new() -> Self {
        Self {
            entries: HashMap::new(),
        }
    }

    /// 标记节点进入 labile 状态
    pub fn mark_labile(&mut self, node_id: &str, window_hours: i64) {
        let now = chrono::Utc::now().timestamp_millis();
        let labile_until = now + window_hours * 3_600_000;

        self.entries
            .entry(node_id.to_string())
            .and_modify(|e| {
                e.labile_until = labile_until;
                e.recall_count += 1;
            })
            .or_insert(ReconsolidationEntry {
                node_id: node_id.to_string(),
                original_text_hash: 0,
                labile_until,
                recall_count: 1,
                reconsolidation_count: 0,
            });
    }

    /// Dream 时对 labile 节点做再巩固
    pub fn reconsolidate(&mut self, store: &RedbStore) -> Result<u32> {
        let now = chrono::Utc::now().timestamp_millis();
        // store passed directly as parameter

        let mut count = 0u32;
        let mut to_remove: Vec<String> = Vec::new();

        for (node_id, entry) in &self.entries {
            if entry.labile_until <= now {
                // 过期
                to_remove.push(node_id.clone());
                continue;
            }

            // 读取节点
            let rtxn = store.begin_read()?;
            let table = rtxn.open_table(crate::storage::L1_NODES)
                .map_err(|e| MemHopError::Storage(format!("open L1_NODES: {}", e)))?;
            let node_opt: Option<crate::engram::KnowledgeNode> = match table.get(node_id.as_str()) {
                Ok(Some(bytes)) => bincode::deserialize(bytes.value()).ok(),
                _ => None,
            };
            drop(table);
            drop(rtxn);

            if let Some(mut node) = node_opt {
                node.memory.reconsolidation_count += 1;
                node.updated_at = chrono::Utc::now().timestamp_millis();

                // 写回
                let wtxn = store.begin_write()?;
                let mut wtable = wtxn.open_table(crate::storage::L1_NODES)
                    .map_err(|e| MemHopError::Storage(format!("open L1_NODES: {}", e)))?;
                let bytes = bincode::serialize(&node)?;
                wtable.insert(node_id.as_str(), bytes.as_slice())
                    .map_err(|e| MemHopError::Storage(format!("insert node: {}", e)))?;
                drop(wtable);
                wtxn.commit()
                    .map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;

                count += 1;
            }

            to_remove.push(node_id.clone());
        }

        for id in to_remove {
            self.entries.remove(&id);
        }

        Ok(count)
    }

    /// 获取 labile 状态统计
    pub fn stats(&self) -> ReconsolidationStats {
        let now = chrono::Utc::now().timestamp_millis();
        ReconsolidationStats {
            labile_count: self.entries.values()
                .filter(|e| e.labile_until > now)
                .count(),
            reconsolidated_count: self.entries.values()
                .map(|e| e.reconsolidation_count)
                .sum(),
        }
    }
}
