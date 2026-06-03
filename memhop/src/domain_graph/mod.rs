use std::collections::HashMap;
use heed::RwTxn;
use crate::engram::{KnowledgeNode, Hyperedge};
use crate::types::{HyperedgeKind, Layer};
use crate::lmdb::L3Env;
use crate::error::{Result, MemHopError};
use half::f16;

/// L3 领域超图 — 知识图谱（无状态，env 从外部传入）。
pub struct L3DomainGraph;

impl L3DomainGraph {
    pub fn new() -> Self { L3DomainGraph }

    pub fn mount_domain(&mut self, wtxn: &mut RwTxn<'_>, env: &L3Env, name: &str) -> Result<String> {
        let now = chrono::Utc::now().timestamp_millis();
        let id = format!("domain_{}", name.chars().take(32).collect::<String>());
        let meta = serde_json::json!({"id": id, "name": name, "created_at": now, "node_count": 0});
        let bytes = serde_json::to_vec(&meta).map_err(|e| MemHopError::Storage(e.to_string()))?;
        env.domain_meta.put(wtxn, &format!("meta:{}", id), &bytes)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        Ok(id)
    }

    pub fn add_node(&mut self, wtxn: &mut RwTxn<'_>, env: &L3Env,
        domain_id: &str, text: &str, sparse: &HashMap<String, f32>, _location: &str) -> Result<String> {
        let id = format!("l3n_{}", chrono::Utc::now().timestamp_millis());
        let vector = vec![f16::ZERO; 1];
        let node = KnowledgeNode::new(id.clone(), text.to_string(), sparse.clone(), vector, Layer::L3);
        let key = format!("node:{}:{}", domain_id, id);
        let bytes = bincode::serialize(&node).map_err(|e| MemHopError::Storage(e.to_string()))?;
        env.domain_nodes.put(wtxn, &key, &bytes).map_err(|e| MemHopError::Storage(e.to_string()))?;
        Ok(id)
    }

    pub fn add_hyperedge(&mut self, wtxn: &mut RwTxn<'_>, env: &L3Env,
        domain_id: &str, node_ids: Vec<String>) -> Result<String> {
        let id = format!("l3he_{}", chrono::Utc::now().timestamp_millis());
        let he = Hyperedge {
            id: id.clone(), node_ids, kind: HyperedgeKind::Association,
            weight: 1.0, created_at: chrono::Utc::now().timestamp_millis(),
            updated_at: chrono::Utc::now().timestamp_millis(), version: 1,
            history: Vec::new(), meta: HashMap::new(),
            chain_prev: None, chain_next: None, chain_label: None,
        };
        let key = format!("hyp:{}:{}", domain_id, id);
        let bytes = bincode::serialize(&he).map_err(|e| MemHopError::Storage(e.to_string()))?;
        env.domain_hyperedges.put(wtxn, &key, &bytes).map_err(|e| MemHopError::Storage(e.to_string()))?;
        Ok(id)
    }

    pub fn unmount_domain(&mut self, wtxn: &mut RwTxn<'_>, env: &L3Env, domain_id: &str) -> Result<()> {
        env.domain_meta.delete(wtxn, &format!("meta:{}", domain_id))
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        Ok(())
    }
}
