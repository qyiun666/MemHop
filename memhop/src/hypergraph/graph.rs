use std::collections::HashMap;
use heed::RwTxn;
use crate::engram::{Hyperedge, KnowledgeNode};
use crate::types::{HyperedgeKind, NodeSource, Layer};
use crate::lmdb::L1Env;
use crate::error::{Result, MemHopError};
use crate::index::SparseIndex;

/// L1 全局超图 — 管理超图节点和超边（无状态，env 从外部传入）。
pub struct L1Hypergraph {
    pub bm25: SparseIndex,
    node_count: u64,
}

impl L1Hypergraph {
    pub fn new() -> Self {
        L1Hypergraph { bm25: SparseIndex::new(), node_count: 0 }
    }

    pub fn rebuild_bm25(&mut self, env: &L1Env) -> Result<()> {
        let txn = env.env.read_txn().map_err(|e| MemHopError::Storage(e.to_string()))?;
        self.node_count = 0;
        if let Ok(iter) = env.nodes.iter(&txn) {
            for item in iter {
                if let Ok((_key, bytes)) = item {
                    if let Ok(node) = bincode::deserialize::<KnowledgeNode>(bytes) {
                        self.bm25.add(&node.id, &node.sparse, node.text.len());
                        self.node_count += 1;
                    }
                }
            }
        }
        Ok(())
    }

    pub fn add_node(&mut self, wtxn: &mut RwTxn<'_>, env: &L1Env,
        text: &str, sparse: &HashMap<String, f32>, vector: Vec<half::f16>,
        _keywords: Vec<String>, _source: NodeSource) -> Result<String> {
        let id = format!("kn_{}", chrono::Utc::now().timestamp_millis());
        let node = KnowledgeNode::new(id.clone(), text.to_string(), sparse.clone(), vector, Layer::L1);
        let bytes = bincode::serialize(&node).map_err(|e| MemHopError::Storage(e.to_string()))?;
        env.nodes.put(wtxn, &id, &bytes).map_err(|e| MemHopError::Storage(e.to_string()))?;
        self.bm25.add(&id, sparse, text.len());
        self.node_count += 1;
        Ok(id)
    }

    pub fn get_node(&self, txn: &heed::RoTxn<'_>, env: &L1Env, id: &str) -> Result<Option<KnowledgeNode>> {
        match env.nodes.get(txn, id).map_err(|e| MemHopError::Storage(e.to_string()))? {
            Some(bytes) => Ok(Some(bincode::deserialize(bytes).map_err(|e| MemHopError::Storage(e.to_string()))?)),
            None => Ok(None),
        }
    }

    pub fn add_hyperedge(&mut self, wtxn: &mut RwTxn<'_>, env: &L1Env,
        node_ids: Vec<String>, kind: HyperedgeKind, weight: f32,
        chain_prev: Option<String>, chain_label: Option<String>) -> Result<String> {
        let now = chrono::Utc::now().timestamp_millis();
        let id = format!("he_{}", now);
        let he = Hyperedge {
            id: id.clone(), node_ids, kind, weight,
            created_at: now, updated_at: now, version: 1,
            history: Vec::new(), meta: HashMap::new(),
            chain_prev, chain_next: None, chain_label,
        };
        let bytes = bincode::serialize(&he).map_err(|e| MemHopError::Storage(e.to_string()))?;
        env.hyperedges.put(wtxn, &id, &bytes).map_err(|e| MemHopError::Storage(e.to_string()))?;

        for nid in &he.node_ids {
            let existing = env.node_to_hyperedges.get(wtxn, nid).map_err(|e| MemHopError::Storage(e.to_string()))?;
            let mut ids: Vec<String> = match existing {
                Some(b) => bincode::deserialize(b).unwrap_or_default(),
                None => Vec::new(),
            };
            ids.push(id.clone());
            let bytes = bincode::serialize(&ids).map_err(|e| MemHopError::Storage(e.to_string()))?;
            env.node_to_hyperedges.put(wtxn, nid, &bytes).map_err(|e| MemHopError::Storage(e.to_string()))?;
        }

        // Update chain_prev's chain_next
        if let Some(ref prev_id) = he.chain_prev {
            if let Ok(Some(bytes)) = env.hyperedges.get(wtxn, prev_id) {
                if let Ok(mut prev) = bincode::deserialize::<Hyperedge>(bytes) {
                    prev.chain_next = Some(id.clone());
                    prev.updated_at = now;
                    let bytes = bincode::serialize(&prev).map_err(|e| MemHopError::Storage(e.to_string()))?;
                    env.hyperedges.put(wtxn, prev_id, &bytes).map_err(|e| MemHopError::Storage(e.to_string()))?;
                }
            }
        }
        Ok(id)
    }

    pub fn get_hyperedge(&self, txn: &heed::RoTxn<'_>, env: &L1Env, id: &str) -> Result<Option<Hyperedge>> {
        match env.hyperedges.get(txn, id).map_err(|e| MemHopError::Storage(e.to_string()))? {
            Some(bytes) => Ok(Some(bincode::deserialize(bytes).map_err(|e| MemHopError::Storage(e.to_string()))?)),
            None => Ok(None),
        }
    }

    pub fn search(&self, sparse: &HashMap<String, f32>, top_k: usize) -> Result<Vec<(String, f32)>> {
        let idf = self.bm25.idf_map();
        Ok(self.bm25.bm25_search(sparse, &idf, top_k))
    }

    pub fn bfs_spread(&self, txn: &heed::RoTxn<'_>, env: &L1Env,
        seed_ids: &[String], depth: usize) -> Result<Vec<(String, f32)>> {
        let mut visited = std::collections::HashSet::new();
        let mut results = Vec::new();
        let mut current = seed_ids.to_vec();
        for _ in 0..depth {
            if current.is_empty() { break; }
            let mut next = Vec::new();
            for sid in &current {
                if !visited.insert(sid.clone()) { continue; }
                if let Ok(Some(bytes)) = env.node_to_hyperedges.get(txn, sid) {
                    if let Ok(eids) = bincode::deserialize::<Vec<String>>(bytes) {
                        for eid in eids {
                            if let Ok(Some(hb)) = env.hyperedges.get(txn, &eid) {
                                if let Ok(he) = bincode::deserialize::<Hyperedge>(hb) {
                                    for nid in &he.node_ids {
                                        if nid != sid && visited.insert(nid.clone()) {
                                            results.push((nid.clone(), he.weight));
                                            next.push(nid.clone());
                                        }
                                    }
                                }
                            }
                        }
                    }
                }
            }
            current = next;
        }
        Ok(results)
    }
}
