//! Unified Graph — adjacency list for typed associations between engrams.

//! 
//! Maintains all edges (associations) in LMDB-backed storage with an
//! in-memory adjacency summary for fast lookups during activation spread.

use std::collections::{HashMap, HashSet};

use crate::engram::{Association, AssociationKind};
use crate::error::Result;
use crate::storage::LmdbStorage;

/// Unified Graph — manages typed, weighted edges between engrams.
pub struct UnifiedGraph {
    /// In-memory adjacency map: source_id → Vec<Association>.
    adjacency: HashMap<String, Vec<Association>>,
}

impl UnifiedGraph {
    pub fn new() -> Self {
        UnifiedGraph {
            adjacency: HashMap::new(),
        }
    }

    /// Rebuild the in-memory adjacency from LMDB.
    pub fn rebuild(storage: &LmdbStorage) -> Result<Self> {
        let txn = storage.begin_read()?;
        let all_entries = storage.all_hippocampus_entries(&txn)?;
        let all_ids: Vec<String> = all_entries.into_iter().map(|(id, _)| id).collect();
        let mut adjacency = HashMap::new();

        for id in &all_ids {
            if let Some(edges) = storage.get_edges(&txn, id)? {
                adjacency.insert(id.clone(), edges);
            }
        }
        txn.commit().map_err(|e| crate::error::MemHopError::Storage(e.to_string()))?;

        Ok(UnifiedGraph { adjacency })
    }

    /// Sync in-memory adjacency back to LMDB for a specific node.
    fn sync_node(&self, storage: &LmdbStorage, id: &str) -> Result<()> {
        let mut txn = storage.begin_write()?;
        if let Some(edges) = self.adjacency.get(id) {
            if edges.is_empty() {
                let _ = storage.delete_edges(&mut txn, id);
            } else {
                storage.put_edges(&mut txn, id, edges)?;
            }
        } else {
            let _ = storage.delete_edges(&mut txn, id);
        }
        txn.commit().map_err(|e| crate::error::MemHopError::Storage(e.to_string()))?;
        Ok(())
    }

    /// Add a directed edge. If an edge to the same target already exists,
    /// its weight and kind are updated (last-write-wins).
    pub fn add_edge(
        &mut self,
        storage: &LmdbStorage,
        from: &str,
        target_id: &str,
        weight: f32,
        kind: AssociationKind,
        now: i64,
    ) -> Result<()> {
        if from == target_id {
            return Ok(()); // no self-loops
        }

        let w = weight.clamp(0.0, 1.0);

        let edges = self.adjacency.entry(from.to_string()).or_default();

        if let Some(existing) = edges.iter_mut().find(|e| e.target_id == target_id) {
            existing.weight = w;
            existing.kind = kind;
            existing.last_activated = now;
        } else {
            edges.push(Association {
                target_id: target_id.to_string(),
                weight: w,
                kind,
                last_activated: now,
            });
        }

        self.sync_node(storage, from)
    }

    /// Add a bidirectional edge (two directed edges).
    #[allow(dead_code)]
    pub fn add_bidirectional_edge(
        &mut self,
        storage: &LmdbStorage,
        a: &str,
        b: &str,
        weight: f32,
        kind: AssociationKind,
        now: i64,
    ) -> Result<()> {
        self.add_edge(storage, a, b, weight, kind.clone(), now)?;
        self.add_edge(storage, b, a, weight, kind, now)
    }

    /// Remove a node and all edges referencing it.
    #[allow(dead_code)]
    pub fn remove_node(&mut self, storage: &LmdbStorage, id: &str) -> Result<()> {
        self.adjacency.remove(id);
        // Remove incoming edges from all other nodes.
        for edges in self.adjacency.values_mut() {
            edges.retain(|e| e.target_id != id);
        }
        // Sync: remove from LMDB too.
        let mut txn = storage.begin_write()?;
        let _ = storage.delete_edges(&mut txn, id);
        for edges in self.adjacency.values() {
            for edge in edges {
                let _ = storage.put_edges(&mut txn, edge.target_id.as_str(), &[]);
            }
        }
        txn.commit().map_err(|e| crate::error::MemHopError::Storage(e.to_string()))?;
        Ok(())
    }

    /// Get outgoing edges for a node.
    pub fn edges_of(&self, id: &str) -> &[Association] {
        self.adjacency
            .get(id)
            .map(|v| v.as_slice())
            .unwrap_or(&[])
    }

    /// Get mutable edges for a node.
    #[allow(dead_code)]
    pub fn edges_of_mut(&mut self, id: &str) -> Option<&mut Vec<Association>> {
        self.adjacency.get_mut(id)
    }

    /// Find contradiction pairs among the given set of IDs.
    pub fn contradiction_pairs_in(&self, ids: &HashSet<String>) -> Vec<(String, String)> {
        let mut pairs = Vec::new();
        for id_a in ids {
            if let Some(edges) = self.adjacency.get(id_a) {
                for edge in edges {
                    if edge.kind == AssociationKind::Contradicts
                        && ids.contains(&edge.target_id)
                        && id_a.as_str() < edge.target_id.as_str()
                    {
                        pairs.push((id_a.clone(), edge.target_id.clone()));
                    }
                }
            }
        }
        pairs
    }

    /// Set the weight of a specific edge.
    #[allow(dead_code)]
    pub fn set_edge_weight(
        &mut self,
        storage: &LmdbStorage,
        from: &str,
        target_id: &str,
        new_weight: f32,
    ) -> Result<()> {
        if let Some(edges) = self.adjacency.get_mut(from)
            && let Some(edge) = edges.iter_mut().find(|e| e.target_id == target_id)
        {
            edge.weight = new_weight.clamp(0.0, 1.0);
            self.sync_node(storage, from)?;
        }
        Ok(())
    }

    /// Decay all edge weights by a factor.
    pub fn decay_edges(&mut self, storage: &LmdbStorage, lambda: f32) -> Result<usize> {
        let factor = (1.0 - lambda).max(0.0);
        let mut count = 0;
        let modified: Vec<String> = self.adjacency.keys().cloned().collect();

        for id in &modified {
            if let Some(edges) = self.adjacency.get_mut(id) {
                for edge in edges.iter_mut() {
                    edge.weight = (edge.weight * factor).clamp(0.0, 1.0);
                    count += 1;
                }
                // Sync each modified node
                let mut txn = storage.begin_write()?;
                storage.put_edges(&mut txn, id, edges)?;
                txn.commit().map_err(|e| crate::error::MemHopError::Storage(e.to_string()))?;
            }
        }
        Ok(count)
    }

    /// Prune edges below the threshold. Returns count of removed edges.
    pub fn prune_edges(&mut self, storage: &LmdbStorage, threshold: f32) -> Result<usize> {
        let mut removed = 0usize;
        let modified: Vec<String> = self.adjacency.keys().cloned().collect();

        for id in &modified {
            if let Some(edges) = self.adjacency.get_mut(id) {
                let before = edges.len();
                edges.retain(|e| e.weight >= threshold);
                removed += before - edges.len();
                // Sync
                let mut txn = storage.begin_write()?;
                storage.put_edges(&mut txn, id, edges)?;
                txn.commit().map_err(|e| crate::error::MemHopError::Storage(e.to_string()))?;
            }
        }
        Ok(removed)
    }

    /// Prune to max average degree.
    pub fn prune_to_max_degree(
        &mut self,
        storage: &LmdbStorage,
        max_avg: usize,
    ) -> Result<usize> {
        let n = self.adjacency.len().max(1);
        let current_avg = self.edge_count() / n;
        if current_avg <= max_avg {
            return Ok(0);
        }
        // Sort edges by weight ascending and remove the weakest ones.
        let mut all_edges: Vec<(String, &Association)> = Vec::new();
        for (src, edges) in &self.adjacency {
            for edge in edges {
                all_edges.push((src.clone(), edge));
            }
        }
        all_edges.sort_by(|a, b| a.1.weight.partial_cmp(&b.1.weight).unwrap_or(std::cmp::Ordering::Equal));

        let target_total = max_avg * n;
        let to_remove = all_edges.len().saturating_sub(target_total);
        let weak: Vec<(String, String)> = all_edges
            .into_iter()
            .take(to_remove)
            .map(|(src, e)| (src, e.target_id.clone()))
            .collect();

        for (src, tgt) in &weak {
            if let Some(edges) = self.adjacency.get_mut(src) {
                edges.retain(|e| e.target_id != *tgt);
            }
        }

        // Sync all modified nodes
        for id in self.adjacency.keys() {
            let mut txn = storage.begin_write()?;
            if let Some(edges) = self.adjacency.get(id) {
                storage.put_edges(&mut txn, id, edges)?;
            }
            txn.commit().map_err(|e| crate::error::MemHopError::Storage(e.to_string()))?;
        }

        Ok(to_remove)
    }

    #[allow(dead_code)]
    pub fn node_count(&self) -> usize {
        self.adjacency.len()
    }

    pub fn edge_count(&self) -> usize {
        self.adjacency.values().map(|v| v.len()).sum()
    }

    pub fn avg_degree(&self) -> f32 {
        let n = self.adjacency.len().max(1);
        self.edge_count() as f32 / n as f32
    }
}

impl Default for UnifiedGraph {
    fn default() -> Self {
        Self::new()
    }
}
