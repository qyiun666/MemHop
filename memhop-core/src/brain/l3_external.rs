//! L3 外部超图 API — Brain 对外暴露的 L3 操作接口。

use std::collections::HashMap;
use crate::brain::Brain;
use crate::engram::{Hyperedge, KnowledgeNode};
use crate::error::{MemHopError, Result};
use crate::storage::L3_DOMAIN_HYPEREDGES;
use crate::types::{
    HyperedgeKind, Layer, NeighborResult, NodeSource, RecallResult, SourceKind, SourceRef,
};

impl Brain {
    /// 在指定 domain 中添加 L3 知识节点。
    /// 自动编码文本并写入 redb + 更新索引。
    pub fn l3_add_node(
        &mut self,
        domain_id: &str,
        text: &str,
        is_structural: bool,
        source_ref: Option<SourceRef>,
    ) -> Result<String> {
        if domain_id.is_empty() {
            return Err(MemHopError::InvalidArgument("domain_id must not be empty".into()));
        }
        if text.is_empty() {
            return Err(MemHopError::InvalidArgument("text must not be empty".into()));
        }

        self.ensure_l3()?;

        // 先编码（避免与后面 l3 mutable borrow 冲突）
        let encoded = self.encoder.encode(text);
        let node_id = crate::batch_store::unique_id("l3ext");

        let store = self.redb_store.as_ref()
            .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;
        let domain_node_key = format!("node:{}:{}", domain_id, node_id);

        let mut node = KnowledgeNode::new(
            node_id.clone(),
            text.to_string(),
            encoded.sparse.clone(),
            encoded.dense.clone(),
            Layer::L3,
            NodeSource::KnowledgeMount,
        );
        node.is_structural = is_structural;
        node.source_ref = source_ref;

        // 写 redb（在 l3 可变借用前完成）
        {
            let wtxn = store.begin_write()
                .map_err(|e| MemHopError::Storage(format!("begin_write: {}", e)))?;
            let mut nodes_table = wtxn.open_table(crate::storage::L3_DOMAIN_NODES)
                .map_err(|e| MemHopError::Storage(format!("open L3_DOMAIN_NODES: {}", e)))?;
            let bytes = bincode::serialize(&node)
                .map_err(|e| MemHopError::Internal(format!("serialize: {}", e)))?;
            nodes_table.insert(domain_node_key.as_str(), bytes.as_slice())
                .map_err(|e| MemHopError::Storage(format!("insert: {}", e)))?;
            drop(nodes_table);
            wtxn.commit()
                .map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;
        }

        // 再拿 l3 可变引用更新索引
        let l3 = self.l3.as_mut()
            .ok_or_else(|| MemHopError::Internal("L3 layer not initialized".into()))?;
        l3.bm25.add(&node_id, &encoded.sparse, text.len())?;
        if !node.vector.is_empty() && node.vector.len() > 1 {
            l3.vector_index.add(&node_id, &node.vector);
        }

        // 更新结构节点索引（store 是共享引用，不冲突）
        if is_structural {
            store.l3_store_structural_index(domain_id, &vec![node_id.clone()])?;
        }

        Ok(node_id)
    }

    /// 在指定 domain 中添加 L3 超边（连接多个节点）。
    pub fn l3_add_hyperedge(
        &mut self,
        domain_id: &str,
        node_ids: &[String],
        kind: HyperedgeKind,
        weight: f32,
    ) -> Result<String> {
        if domain_id.is_empty() || node_ids.is_empty() {
            return Err(MemHopError::InvalidArgument("domain_id and node_ids required".into()));
        }

        self.ensure_l3()?;
        let store = self.redb_store.as_ref()
            .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;
        let l3 = self.l3.as_mut()
            .ok_or_else(|| MemHopError::Internal("L3 layer not initialized".into()))?;

        let he_id = crate::batch_store::unique_id("l3ext");
        let now = chrono::Utc::now().timestamp_millis();
        let hyperedge = Hyperedge {
            id: he_id.clone(),
            node_ids: node_ids.to_vec(),
            kind,
            weight,
            created_at: now,
            updated_at: now,
            version: 1,
            history: Vec::new(),
            meta: HashMap::new(),
            chain_prev: None,
            chain_next: None,
            chain_label: None,
        };

        let hyp_key = format!("hyp:{}:{}", domain_id, he_id);
        {
            let wtxn = store.begin_write()
                .map_err(|e| MemHopError::Storage(format!("begin_write: {}", e)))?;
            let mut table = wtxn.open_table(L3_DOMAIN_HYPEREDGES)
                .map_err(|e| MemHopError::Storage(format!("open L3_DOMAIN_HYPEREDGES: {}", e)))?;
            let bytes = bincode::serialize(&hyperedge)
                .map_err(|e| MemHopError::Internal(format!("serialize: {}", e)))?;
            table.insert(hyp_key.as_str(), bytes.as_slice())
                .map_err(|e| MemHopError::Storage(format!("insert: {}", e)))?;
            drop(table);

            // 同时更新 node_to_hyperedges 反向索引
            for node_id in node_ids {
                let mut he_ids = store.l3_get_node_hyperedge_index(node_id)?
                    .unwrap_or_default();
                if !he_ids.contains(&he_id) {
                    he_ids.push(he_id.clone());
                    store.l3_store_node_hyperedge_index(node_id, &he_ids)?;
                }
            }

            wtxn.commit()
                .map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;
        }

        // 更新内存反向索引
        for node_id in node_ids {
            l3.node_to_hyperedges
                .entry(node_id.clone())
                .or_default()
                .push(he_id.clone());
        }

        Ok(he_id)
    }

    /// 查询 L3 节点的超图邻居。
    pub fn l3_neighbors(
        &mut self,
        node_id: &str,
        max_depth: usize,
    ) -> Result<Vec<NeighborResult>> {
        self.ensure_l3()?;
        let store = self.redb_store.as_ref()
            .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;
        let l3 = self.l3.as_mut()
            .ok_or_else(|| MemHopError::Internal("L3 layer not initialized".into()))?;
        let seed_scores: HashMap<String, f32> = HashMap::new(); // 外部调用无种子得分
        l3.expand_neighborhood(
            &[node_id.to_string()],
            store,
            max_depth.min(3), // 最大 3 跳
            10,
            &seed_scores,
        )
    }

    /// 在 L3 domain 中搜索。
    pub fn l3_search(
        &mut self,
        query: &str,
        domain_id: Option<&str>,
        max: usize,
    ) -> Result<Vec<RecallResult>> {
        self.ensure_l3()?;
        let store = self.redb_store.as_ref()
            .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;
        let l3 = self.l3.as_mut()
            .ok_or_else(|| MemHopError::Internal("L3 layer not initialized".into()))?;

        let encoded = self.encoder.encode(query);
        let txn = store.begin_read()
            .map_err(|e| MemHopError::Storage(format!("begin_read: {}", e)))?;

        let domain_ids = match domain_id {
            Some(id) => vec![id.to_string()],
            None => {
                // 搜索所有 domain
                store.l3_list_paths()?
            }
        };

        let hits = l3.structural_search_in_domain(
            &txn, store, &encoded.sparse, &encoded.dense, &domain_ids, max,
        )?;

        let mut results: Vec<RecallResult> = Vec::new();
        for (node_id, score, did, is_structural, source_ref) in hits {
            results.push(RecallResult {
                layer: Layer::L3,
                id: node_id,
                text: String::new(),
                score,
                topic_label: None,
                created_at: 0,
                version: 1,
                emotion: None,
                domain_id: Some(did),
                source_ref,
                is_structural,
                neighbors: Vec::new(),
            });
        }
        Ok(results)
    }

    /// 读取来源原文片段。
    /// - File 类型: 直接读取文件
    /// - 其他类型: 返回占位提示
    pub fn read_source_excerpt(
        &self,
        source_ref: &SourceRef,
        max_chars: usize,
    ) -> Result<String> {
        match source_ref.kind {
            SourceKind::File => {
                let path = std::path::Path::new(&source_ref.location);
                let content = std::fs::read_to_string(path)
                    .map_err(|e| MemHopError::InvalidArgument(format!(
                        "cannot read source file '{}': {}", source_ref.location, e
                    )))?;

                let excerpt = if let Some((start, end)) = source_ref.line_range {
                    let lines: Vec<&str> = content.lines().collect();
                    let start = (start.max(1) - 1).min(lines.len());
                    let end = end.min(lines.len());
                    lines[start..end].join("\n")
                } else {
                    content.chars().take(max_chars).collect()
                };

                let excerpt: String = excerpt.chars().take(max_chars).collect();
                if excerpt.len() < content.len() {
                    Ok(format!("{}...\n[truncated at {} chars]", excerpt, max_chars))
                } else {
                    Ok(excerpt)
                }
            }
            SourceKind::Database | SourceKind::Api => {
                Err(MemHopError::InvalidArgument(format!(
                    "source type {:?} requires agent-side fetching: {}",
                    source_ref.kind, source_ref.location
                )))
            }
            SourceKind::Manual | SourceKind::Custom(_) => {
                Ok(format!("[source: {:?}] {}", source_ref.kind, source_ref.location))
            }
        }
    }
}
