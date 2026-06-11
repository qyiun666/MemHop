//! Redb-backed 分层存储操作。
//!
//! 提供 L0-L5 各层的读写操作，使用 bincode 序列化。
//! 调用方负责事务生命周期。

use crate::error::{MemHopError, Result};
use redb::ReadableTable;
use crate::storage::store::RedbStore;
use crate::storage::{
    L0_HISTORY, L0_PROFILE,
    L1_HYPEREDGES, L1_NODES, L1_NODE_TO_HYPEREDGES,
    L2_TOPICS, L2_TOPIC_EDGES,
    L3_DOMAIN_META, L3_DOMAIN_NODES, L3_NODE_TO_HYPEREDGES, L3_STRUCTURAL_INDEX,
    L4_DOCS, L4_SESSION_INDEX, L4_TURN_INDEX,
    L5_CHAIN_INDEX, L5_CRYSTALS,
};
use crate::types::{
    L0Profile, L0Snapshot, ProceduralCrystal, DomainMeta,
};
use crate::engram::{Hyperedge, KnowledgeNode, RawDocument, Topic, TopicEdge};

const PROFILE_KEY: &str = "profile:main";

// ── L0: 角色画像 ─────────────────────────────────────────

impl RedbStore {
    /// 读取 L0 角色画像。
    pub fn l0_get_profile(&self) -> Result<Option<L0Profile>> {
        let txn = self.begin_read()?;
        self.read_bincode(&txn, L0_PROFILE, PROFILE_KEY)
    }

    /// 写入 L0 角色画像。
    pub fn l0_set_profile(&self, profile: &L0Profile) -> Result<()> {
        let mut wtxn = self.begin_write()?;
        self.write_bincode(&mut wtxn, L0_PROFILE, PROFILE_KEY, profile)?;
        wtxn.commit().map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;
        Ok(())
    }

    /// 保存 L0 快照到历史表。
    pub fn l0_snapshot(&self, snapshot: &L0Snapshot) -> Result<()> {
        let key = format!("hist:{}", snapshot.version);
        let wtxn = self.begin_write()?;
        let mut table = wtxn.open_table(L0_HISTORY)
            .map_err(|e| MemHopError::Storage(format!("open table: {}", e)))?;
        let bytes = bincode::serialize(snapshot)
            .map_err(|e| MemHopError::Internal(format!("bincode serialize: {}", e)))?;
        table.insert(key.as_str(), bytes.as_slice())
            .map_err(|e| MemHopError::Storage(format!("insert: {}", e)))?;
        drop(table);
        wtxn.commit().map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;
        Ok(())
    }
}

// ── L5: 程序性晶体 ───────────────────────────────────────

impl RedbStore {
    /// 存储一个程序性晶体。
    pub fn l5_store_crystal(&self, crystal: &ProceduralCrystal) -> Result<()> {
        let key = format!("crystal:{}", crystal.id);
        let mut wtxn = self.begin_write()?;
        self.write_bincode(&mut wtxn, L5_CRYSTALS, &key, crystal)?;
        wtxn.commit().map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;
        Ok(())
    }

    /// 按 ID 获取程序性晶体。
    pub fn l5_get_crystal(&self, id: &str) -> Result<Option<ProceduralCrystal>> {
        let key = format!("crystal:{}", id);
        let txn = self.begin_read()?;
        self.read_bincode(&txn, L5_CRYSTALS, &key)
    }

    /// 列出所有程序性晶体。
    pub fn l5_list_crystals(&self) -> Result<Vec<ProceduralCrystal>> {
        let txn = self.begin_read()?;
        self.iter_bincode(&txn, L5_CRYSTALS)
            .map(|pairs| pairs.into_iter().map(|(_, c)| c).collect())
    }

    /// 按关键词匹配程序性晶体。
    pub fn l5_get_crystals_by_keyword(&self, keyword: &str) -> Result<Vec<ProceduralCrystal>> {
        let crystals = self.l5_list_crystals()?;
        Ok(crystals
            .into_iter()
            .filter(|c| {
                c.trigger_keywords
                    .iter()
                    .any(|kw| kw.contains(keyword) || keyword.contains(kw))
            })
            .collect())
    }

    /// 按链 ID 获取晶体索引（链索引）。
    pub fn l5_get_chain(&self, chain_id: &str) -> Result<Option<Vec<String>>> {
        let key = format!("chain:{}", chain_id);
        let txn = self.begin_read()?;
        self.read_bincode(&txn, L5_CHAIN_INDEX, &key)
    }

    /// 存储链索引。
    pub fn l5_store_chain(&self, chain_id: &str, crystal_ids: &[String]) -> Result<()> {
        let key = format!("chain:{}", chain_id);
        let mut wtxn = self.begin_write()?;
        self.write_bincode(&mut wtxn, L5_CHAIN_INDEX, &key, &crystal_ids.to_vec())?;
        wtxn.commit().map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;
        Ok(())
    }
}

// ── L4: 原文库 ───────────────────────────────────────────

impl RedbStore {
    /// 存储一篇原始文档。
    pub fn l4_store_doc(&self, doc: &RawDocument) -> Result<()> {
        let key = format!("doc:{}", doc.id);
        let mut wtxn = self.begin_write()?;
        self.write_bincode(&mut wtxn, L4_DOCS, &key, doc)?;
        wtxn.commit().map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;
        Ok(())
    }

    /// 按 ID 获取原始文档。
    pub fn l4_get_doc(&self, id: &str) -> Result<Option<RawDocument>> {
        let key = format!("doc:{}", id);
        let txn = self.begin_read()?;
        self.read_bincode(&txn, L4_DOCS, &key)
    }

    /// 按 turn_id 获取文档列表。
    pub fn l4_get_by_turn(&self, turn_id: &str) -> Result<Vec<RawDocument>> {
        let key = format!("turn:{}", turn_id);
        let txn = self.begin_read()?;
        let doc_ids: Option<Vec<String>> = self.read_bincode(&txn, L4_TURN_INDEX, &key)?;
        match doc_ids {
            Some(ids) => self.l4_get_by_ids(&ids),
            None => Ok(Vec::new()),
        }
    }

    /// 按 session_id 获取文档列表。
    pub fn l4_get_by_session(&self, session_id: &str) -> Result<Vec<RawDocument>> {
        let key = format!("session:{}", session_id);
        let txn = self.begin_read()?;
        let doc_ids: Option<Vec<String>> = self.read_bincode(&txn, L4_SESSION_INDEX, &key)?;
        match doc_ids {
            Some(ids) => self.l4_get_by_ids(&ids),
            None => Ok(Vec::new()),
        }
    }

    /// 按多个 ID 批量获取文档。
    pub fn l4_get_by_ids(&self, ids: &[String]) -> Result<Vec<RawDocument>> {
        let txn = self.begin_read()?;
        let mut docs = Vec::new();
        for id in ids {
            let key = format!("doc:{}", id);
            if let Some(doc) = self.read_bincode::<RawDocument>(&txn, L4_DOCS, &key)? {
                docs.push(doc);
            }
        }
        Ok(docs)
    }

    /// 存储 Turn 索引。
    pub fn l4_store_turn_index(&self, turn_id: &str, doc_ids: &[String]) -> Result<()> {
        let key = format!("turn:{}", turn_id);
        let mut wtxn = self.begin_write()?;
        self.write_bincode(&mut wtxn, L4_TURN_INDEX, &key, doc_ids)?;
        wtxn.commit().map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;
        Ok(())
    }

    /// 存储 Session 索引。
    pub fn l4_store_session_index(&self, session_id: &str, doc_ids: &[String]) -> Result<()> {
        let key = format!("session:{}", session_id);
        let mut wtxn = self.begin_write()?;
        self.write_bincode(&mut wtxn, L4_SESSION_INDEX, &key, doc_ids)?;
        wtxn.commit().map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;
        Ok(())
    }

    /// 获取 L4 文档数量。
    pub fn l4_doc_count(&self) -> Result<u64> {
        let txn = self.begin_read()?;
        self.count(&txn, L4_DOCS)
    }
}

// ── L1: 超图 ─────────────────────────────────────────────

impl RedbStore {
    /// 存储 L1 节点。
    pub fn l1_store_node(&self, node: &KnowledgeNode) -> Result<()> {
        let mut wtxn = self.begin_write()?;
        self.write_bincode(&mut wtxn, L1_NODES, &node.id, node)?;
        wtxn.commit().map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;
        Ok(())
    }

    /// 获取 L1 节点。
    pub fn l1_get_node(&self, id: &str) -> Result<Option<KnowledgeNode>> {
        let txn = self.begin_read()?;
        self.read_bincode(&txn, L1_NODES, id)
    }

    /// 存储 L1 超边。
    pub fn l1_store_hyperedge(&self, he: &Hyperedge) -> Result<()> {
        let mut wtxn = self.begin_write()?;
        self.write_bincode(&mut wtxn, L1_HYPEREDGES, &he.id, he)?;
        wtxn.commit().map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;
        Ok(())
    }

    /// 获取 L1 超边。
    pub fn l1_get_hyperedge(&self, id: &str) -> Result<Option<Hyperedge>> {
        let txn = self.begin_read()?;
        self.read_bincode(&txn, L1_HYPEREDGES, id)
    }

    /// 存储节点→超边反向索引。
    pub fn l1_store_node_hyperedge_index(&self, node_id: &str, he_ids: &Vec<String>) -> Result<()> {
        let mut wtxn = self.begin_write()?;
        self.write_bincode(&mut wtxn, L1_NODE_TO_HYPEREDGES, node_id, he_ids)?;
        wtxn.commit()
            .map_err(|e| MemHopError::Storage(format!("commit L1_NODE_TO_HYPEREDGES: {}", e)))?;
        Ok(())
    }

    /// 获取节点→超边反向索引。
    pub fn l1_get_node_hyperedge_index(&self, node_id: &str) -> Result<Option<Vec<String>>> {
        let txn = self.begin_read()?;
        self.read_bincode(&txn, L1_NODE_TO_HYPEREDGES, node_id)
    }

    /// 获取 L1 节点数量。
    pub fn l1_node_count(&self) -> Result<u64> {
        let txn = self.begin_read()?;
        self.count(&txn, L1_NODES)
    }
}

// ── L2: 话题图 ───────────────────────────────────────────

impl RedbStore {
    /// 存储话题。
    pub fn l2_store_topic(&self, topic: &Topic) -> Result<()> {
        let key = format!("topic:{}:meta", topic.id);
        let mut wtxn = self.begin_write()?;
        self.write_bincode(&mut wtxn, L2_TOPICS, &key, topic)?;
        wtxn.commit().map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;
        Ok(())
    }

    /// 获取话题。
    pub fn l2_get_topic(&self, topic_id: &str) -> Result<Option<Topic>> {
        let key = format!("topic:{}:meta", topic_id);
        let txn = self.begin_read()?;
        self.read_bincode(&txn, L2_TOPICS, &key)
    }

    /// 列出所有话题。
    pub fn l2_list_topics(&self) -> Result<Vec<Topic>> {
        let txn = self.begin_read()?;
        let pairs = self.iter_bincode::<Topic>(&txn, L2_TOPICS)?;
        Ok(pairs
            .into_iter()
            .filter(|(key, _)| key.starts_with("topic:") && key.ends_with(":meta"))
            .map(|(_, t)| t)
            .collect())
    }

    /// 存储话题边。
    pub fn l2_store_topic_edge(&self, edge: &TopicEdge) -> Result<()> {
        let key = format!("edge:{}_{}", edge.source_id, edge.target_id);
        let mut wtxn = self.begin_write()?;
        self.write_bincode(&mut wtxn, L2_TOPIC_EDGES, &key, edge)?;
        wtxn.commit().map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;
        Ok(())
    }

    /// 获取话题数量。
    pub fn l2_topic_count(&self) -> Result<u64> {
        let txn = self.begin_read()?;
        self.count(&txn, L2_TOPICS)
    }
}

// ── L3: 领域超图 ─────────────────────────────────────────

impl RedbStore {
    /// 存储领域元信息（旧版 serde_json::Value）。
    pub fn l3_store_domain_meta(&self, domain_id: &str, meta: &serde_json::Value) -> Result<()> {
        let key = format!("meta:{}", domain_id);
        let wtxn = self.begin_write()?;
        let bytes = serde_json::to_vec(meta)
            .map_err(|e| MemHopError::Internal(format!("json serialize: {}", e)))?;
        let mut table = wtxn.open_table(L3_DOMAIN_META)
            .map_err(|e| MemHopError::Storage(format!("open table: {}", e)))?;
        table.insert(key.as_str(), bytes.as_slice())
            .map_err(|e| MemHopError::Storage(format!("insert: {}", e)))?;
        drop(table);
        wtxn.commit().map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;
        Ok(())
    }

    /// 存储 DomainMeta（强类型版本，替代 serde_json::Value）
    pub fn l3_store_domain_meta_v2(&self, domain_id: &str, meta: &DomainMeta) -> Result<()> {
        let key = format!("meta:{}", domain_id);
        let mut wtxn = self.begin_write()?;
        self.write_bincode(&mut wtxn, L3_DOMAIN_META, &key, meta)?;
        wtxn.commit().map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;
        Ok(())
    }

    /// 获取 DomainMeta（强类型版本，替代 serde_json::Value）
    pub fn l3_get_domain_meta_v2(&self, domain_id: &str) -> Result<Option<DomainMeta>> {
        let meta_key = format!("meta:{}", domain_id);
        let txn = self.begin_read()?;
        self.read_bincode(&txn, L3_DOMAIN_META, &meta_key)
    }

    /// 列出已挂载的领域路径。
    pub fn l3_list_paths(&self) -> Result<Vec<String>> {
        let txn = self.begin_read()?;
        let table = match txn.open_table(L3_DOMAIN_META) {
            Ok(t) => t,
            Err(e) => {
                if e.to_string().contains("does not exist") {
                    return Ok(Vec::new());
                }
                return Err(MemHopError::Storage(format!("open L3_DOMAIN_META: {}", e)));
            }
        };
        let mut paths = Vec::new();
        for result in table.iter().map_err(|e| MemHopError::Storage(format!("iter: {}", e)))? {
            let (key, _) = result
                .map_err(|e| MemHopError::Storage(format!("iter item: {}", e)))?;
            let k = key.value();
            if let Some(rest) = k.strip_prefix("meta:") {
                paths.push(rest.to_string());
            }
        }
        Ok(paths)
    }

    /// 存储 L3 领域节点。
    pub fn l3_store_node(&self, domain_id: &str, node_id: &str, node: &KnowledgeNode) -> Result<()> {
        let key = format!("node:{}:{}", domain_id, node_id);
        let mut wtxn = self.begin_write()?;
        self.write_bincode(&mut wtxn, L3_DOMAIN_NODES, &key, node)?;
        wtxn.commit().map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;
        Ok(())
    }

    /// 获取 L3 领域节点。
    pub fn l3_get_node(&self, domain_id: &str, node_id: &str) -> Result<Option<KnowledgeNode>> {
        let key = format!("node:{}:{}", domain_id, node_id);
        let txn = self.begin_read()?;
        self.read_bincode(&txn, L3_DOMAIN_NODES, &key)
    }

    /// 获取 L3 节点数量。
    pub fn l3_node_count(&self) -> Result<u64> {
        let txn = self.begin_read()?;
        self.count(&txn, L3_DOMAIN_NODES)
    }

    // ── L3 骨架化: 新增存储方法 ───────────────────────────────

    /// 存储 L3 节点→超边反向索引。
    pub fn l3_store_node_hyperedge_index(&self, node_id: &str, he_ids: &Vec<String>) -> Result<()> {
        let mut wtxn = self.begin_write()?;
        self.write_bincode(&mut wtxn, L3_NODE_TO_HYPEREDGES, node_id, he_ids)?;
        wtxn.commit()
            .map_err(|e| MemHopError::Storage(format!("commit L3_NODE_TO_HYPEREDGES: {}", e)))?;
        Ok(())
    }

    /// 获取 L3 节点→超边反向索引。
    pub fn l3_get_node_hyperedge_index(&self, node_id: &str) -> Result<Option<Vec<String>>> {
        let txn = self.begin_read()?;
        self.read_bincode(&txn, L3_NODE_TO_HYPEREDGES, node_id)
    }

    /// 存储 L3 结构节点索引（domain_id → structural node_ids）。
    pub fn l3_store_structural_index(&self, domain_id: &str, node_ids: &Vec<String>) -> Result<()> {
        let key = format!("domain:{}", domain_id);
        let mut wtxn = self.begin_write()?;
        self.write_bincode(&mut wtxn, L3_STRUCTURAL_INDEX, &key, node_ids)?;
        wtxn.commit()
            .map_err(|e| MemHopError::Storage(format!("commit L3_STRUCTURAL_INDEX: {}", e)))?;
        Ok(())
    }

    /// 获取 L3 结构节点索引。
    pub fn l3_get_structural_index(&self, domain_id: &str) -> Result<Option<Vec<String>>> {
        let key = format!("domain:{}", domain_id);
        let txn = self.begin_read()?;
        self.read_bincode(&txn, L3_STRUCTURAL_INDEX, &key)
    }

    /// 删除 L3 结构节点索引。
    pub fn l3_delete_structural_index(&self, domain_id: &str) -> Result<()> {
        let key = format!("domain:{}", domain_id);
        let wtxn = self.begin_write()?;
        let mut table = wtxn.open_table(L3_STRUCTURAL_INDEX)
            .map_err(|e| MemHopError::Storage(format!("open L3_STRUCTURAL_INDEX: {}", e)))?;
        table.remove(key.as_str())
            .map_err(|e| MemHopError::Storage(format!("remove: {}", e)))?;
        drop(table);
        wtxn.commit()
            .map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;
        Ok(())
    }
}
