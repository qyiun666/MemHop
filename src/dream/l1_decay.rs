// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L1 associative decay stage — time-decay importance/weight, prune weak associations.

use crate::config::DecayConfig;
use crate::dream::emotion::apply_emotional_boost;
use crate::index::l2_meta::L2MetaIndex;
use crate::layers::context_node::SceneNode;
use crate::layers::hyperedge::SceneEdge;
use crate::shared::common::now_ms;
use crate::storage::record::{REC_L1_HYPEREDGE, REC_L1_SCENE_NODE};
use crate::storage::StorageEngine;
use crate::MemHopError;
use std::collections::{HashMap, HashSet};

// Decay defaults: lambda_node=0.01, lambda_edge=0.02, node_remove=0.05, node_prune_edges=0.15, edge_remove=0.05, min_edge_nodes=2
/// Report produced by the L1 decay stage
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct L1DecayReport {
    /// Number of nodes whose importance was updated (including edge pruning)
    pub decayed_nodes: usize,
    /// Number of edge pointers removed from SceneNodes
    pub pruned_edges: usize,
    /// Number of SceneNodes removed due to low importance
    pub removed_nodes: usize,
    /// Number of SceneEdges removed due to low weight or underpopulation
    pub removed_edges: usize,
}

/// Run time-based decay over the L1 scene hypergraph skeleton.
pub fn decay_l1_network(
    engine: &mut StorageEngine,
    decay_config: &DecayConfig,
    l2_meta: &L2MetaIndex,
) -> Result<L1DecayReport, MemHopError> {
    let now = now_ms();
    let mut report = L1DecayReport {
        decayed_nodes: 0,
        pruned_edges: 0,
        removed_nodes: 0,
        removed_edges: 0,
    };

    let entries: Vec<(u64, u64)> = engine.iter_index().map(|(k, v)| (*k, *v)).collect();

    // -------------------------------------------------------------------------
    let mut removed_node_ids: HashSet<u64> = HashSet::new();
    // Maps edge id → set of node ids that cleared their reference to it
    let mut cleared_edges: HashMap<u64, HashSet<u64>> = HashMap::new();

    for (id_hash, _offset) in entries {
        let Some((record_type, data)) = engine.read_record(id_hash)? else {
            continue;
        };

        if record_type != REC_L1_SCENE_NODE {
            continue;
        }

        let mut node: SceneNode = match bincode::deserialize(data) {
            Ok(n) => n,
            Err(e) => {
                return Err(MemHopError::Serialization(format!(
                    "SceneNode deserialize failed: {}",
                    e
                )));
            }
        };

        // Skip nodes whose first L2 topic has depth > 2 (L1 only maintains depth <= 2)
        let first_topic_id = node.topic_ids.first().copied().unwrap_or(0);
        if let Some(meta) = l2_meta.get(first_topic_id) {
            if meta.depth > 2 {
                continue;
            }
        }

        let dt_hours = dt_hours_from(now, node.updated_at);
        let lambda = apply_emotional_boost(
            decay_config.lambda_node,
            node.valence as f64,
            node.arousal as f64,
        );
        let new_importance = node.importance * (-lambda * dt_hours).exp();

        if new_importance < decay_config.node_remove_threshold {
            engine.delete_record(id_hash)?;
            removed_node_ids.insert(id_hash);
            report.removed_nodes += 1;
            continue;
        }

        node.importance = new_importance;

        if new_importance < decay_config.node_prune_edges_threshold {
            report.pruned_edges += node.edge_ids.len();
            for edge_hash in &node.edge_ids {
                cleared_edges.entry(*edge_hash).or_default().insert(id_hash);
            }
            node.edge_ids.clear();
        }

        node.updated_at = now;
        let node_data = bincode::serialize(&node).map_err(|e| {
            MemHopError::Serialization(format!("SceneNode serialize failed: {}", e))
        })?;
        engine.write_record(REC_L1_SCENE_NODE, id_hash, &node_data)?;
        report.decayed_nodes += 1;
    }

    // -------------------------------------------------------------------------
    // Process edges whose references were cleared from nodes above
    let mut edges_removed_by_clear: HashSet<u64> = HashSet::new();
    for (edge_id, node_ids) in &cleared_edges {
        for node_id in node_ids {
            if remove_node_from_edge(engine, *edge_id, *node_id, decay_config)? {
                edges_removed_by_clear.insert(*edge_id);
                break;
            }
        }
    }
    report.removed_edges += edges_removed_by_clear.len();

    let edge_entries: Vec<(u64, u64)> = engine.iter_index().map(|(k, v)| (*k, *v)).collect();

    for (id_hash, _offset) in edge_entries {
        if edges_removed_by_clear.contains(&id_hash) {
            continue;
        }

        let Some((record_type, data)) = engine.read_record(id_hash)? else {
            continue;
        };

        if record_type != REC_L1_HYPEREDGE {
            continue;
        }

        let mut edge: SceneEdge = match bincode::deserialize(data) {
            Ok(e) => e,
            Err(err) => {
                return Err(MemHopError::Serialization(format!(
                    "SceneEdge deserialize failed: {}",
                    err
                )));
            }
        };

        let dt_hours = dt_hours_from(now, edge.created_at);
        let new_weight = edge.weight * (-decay_config.lambda_edge * dt_hours).exp();

        // Clean references to nodes removed above
        edge.node_ids.retain(|ptr| !removed_node_ids.contains(ptr));

        if edge.node_ids.len() < decay_config.min_edge_nodes
            || new_weight < decay_config.edge_remove_threshold
        {
            // Before freeing the edge, clean references from surviving nodes
            for &node_ptr in &edge.node_ids {
                remove_edge_from_node(engine, node_ptr, id_hash)?;
            }
            engine.delete_record(id_hash)?;
            report.removed_edges += 1;
            continue;
        }

        edge.weight = new_weight;
        let edge_data = bincode::serialize(&edge).map_err(|e| {
            MemHopError::Serialization(format!("SceneEdge serialize failed: {}", e))
        })?;
        engine.write_record(REC_L1_HYPEREDGE, id_hash, &edge_data)?;
    }

    Ok(report)
}

#[inline]
fn dt_hours_from(now_ms: i64, updated_at_ms: i64) -> f32 {
    let dt_ms = now_ms.saturating_sub(updated_at_ms).max(0) as f32;
    dt_ms / 3_600_000.0
}

/// Remove `edge_id` from the `edge_ids` of the SceneNode identified by `node_id`.
/// If the node does not exist or does not reference the edge, this is a no-op.
pub(crate) fn remove_edge_from_node(
    engine: &mut StorageEngine,
    node_id: u64,
    edge_id: u64,
) -> Result<(), MemHopError> {
    let Some((record_type, data)) = engine.read_record(node_id)? else {
        return Ok(());
    };
    if record_type != REC_L1_SCENE_NODE {
        return Ok(());
    }
    let Ok(mut node) = bincode::deserialize::<SceneNode>(data) else {
        return Ok(());
    };
    if node.edge_ids.contains(&edge_id) {
        node.edge_ids.retain(|&e| e != edge_id);
        let node_data = bincode::serialize(&node).map_err(|e| {
            MemHopError::Serialization(format!("SceneNode serialize failed: {}", e))
        })?;
        engine.write_record(REC_L1_SCENE_NODE, node_id, &node_data)?;
    }
    Ok(())
}

/// Remove `node_id` from the `node_ids` of the SceneEdge identified by `edge_id`.
/// Returns `true` if the edge was removed entirely because it became underpopulated.
pub(crate) fn remove_node_from_edge(
    engine: &mut StorageEngine,
    edge_id: u64,
    node_id: u64,
    decay_config: &DecayConfig,
) -> Result<bool, MemHopError> {
    let Some((record_type, data)) = engine.read_record(edge_id)? else {
        return Ok(false);
    };
    if record_type != REC_L1_HYPEREDGE {
        return Ok(false);
    }
    let Ok(mut edge) = bincode::deserialize::<SceneEdge>(data) else {
        return Ok(false);
    };
    if !edge.node_ids.contains(&node_id) {
        return Ok(false);
    }
    edge.node_ids.retain(|&n| n != node_id);
    if edge.node_ids.len() < decay_config.min_edge_nodes {
        // Edge underpopulated: remove it and clean surviving nodes
        for &surviving_node in &edge.node_ids {
            remove_edge_from_node(engine, surviving_node, edge_id)?;
        }
        engine.delete_record(edge_id)?;
        return Ok(true);
    }
    let edge_data = bincode::serialize(&edge)
        .map_err(|e| MemHopError::Serialization(format!("SceneEdge serialize failed: {}", e)))?;
    engine.write_record(REC_L1_HYPEREDGE, edge_id, &edge_data)?;
    Ok(false)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::layers::hyperedge::HyperedgeKind;
    use crate::storage::StorageEngine;
    use tempfile::NamedTempFile;

    fn default_decay_config() -> DecayConfig {
        DecayConfig {
            lambda_node: 0.01,
            lambda_edge: 0.02,
            node_remove_threshold: 0.05,
            node_prune_edges_threshold: 0.15,
            edge_remove_threshold: 0.05,
            min_edge_nodes: 2,
            lambda_pathway: 0.01,
            pathway_remove_threshold: 0.05,
        }
    }

    fn create_engine() -> StorageEngine {
        let temp = NamedTempFile::new().unwrap();
        StorageEngine::create(temp.path(), 768).unwrap()
    }

    #[allow(clippy::too_many_arguments)]
    fn allocate_scene_node_page(
        engine: &mut StorageEngine,
        id_hash: u64,
        importance: f32,
        updated_at: i64,
        edge_ids: Vec<u64>,
    ) {
        let node = SceneNode {
            id_hash,
            scene_id: 1000,
            topic_ids: vec![1000],
            depth: 1,
            vector_page_ref: 0,
            importance,
            valence: 0.0,
            arousal: 0.0,
            created_at: updated_at,
            updated_at,
            edge_ids,
        };
        let data = bincode::serialize(&node).unwrap();
        engine
            .write_record(REC_L1_SCENE_NODE, id_hash, &data)
            .unwrap();
    }

    #[allow(clippy::too_many_arguments)]
    fn allocate_scene_edge_page(
        engine: &mut StorageEngine,
        id_hash: u64,
        weight: f32,
        created_at: i64,
        node_ids: Vec<u64>,
    ) {
        let edge = SceneEdge {
            id_hash,
            kind: HyperedgeKind::Semantic,
            node_ids,
            weight,
            created_at,
        };
        let data = bincode::serialize(&edge).unwrap();
        engine
            .write_record(REC_L1_HYPEREDGE, id_hash, &data)
            .unwrap();
    }

    fn read_scene_node(engine: &StorageEngine, id_hash: u64) -> SceneNode {
        let (_, data) = engine.read_record(id_hash).unwrap().unwrap();
        bincode::deserialize(data).unwrap()
    }

    fn read_scene_edge(engine: &StorageEngine, id_hash: u64) -> SceneEdge {
        let (_, data) = engine.read_record(id_hash).unwrap().unwrap();
        bincode::deserialize(data).unwrap()
    }

    #[test]
    fn test_node_decay_and_update() {
        let mut engine = create_engine();
        let old_time = now_ms() - 10 * 3_600_000;
        allocate_scene_node_page(&mut engine, 1, 0.5, old_time, vec![10, 11]);
        let dc = default_decay_config();
        let l2_meta = L2MetaIndex::new();
        let report = decay_l1_network(&mut engine, &dc, &l2_meta).unwrap();
        assert_eq!(report.decayed_nodes, 1);
        assert_eq!(report.removed_nodes, 0);
        assert_eq!(report.pruned_edges, 0);
        assert!(engine.contains(1));
        let node = read_scene_node(&engine, 1);
        let expected = 0.5 * (-dc.lambda_node * 10.0).exp();
        assert!((node.importance - expected).abs() < 1e-5);
        assert!(node.updated_at > old_time);
        assert_eq!(node.edge_ids, vec![10, 11]);
    }

    #[test]
    fn test_node_prune_edges() {
        let mut engine = create_engine();
        let old_time = now_ms() - 20 * 3_600_000;
        let dc = default_decay_config();
        let l2_meta = L2MetaIndex::new();
        let target = (dc.node_remove_threshold + dc.node_prune_edges_threshold) / 2.0;
        let start_importance = target / (-dc.lambda_node * 20.0).exp();
        allocate_scene_node_page(&mut engine, 2, start_importance, old_time, vec![10, 11, 12]);
        let report = decay_l1_network(&mut engine, &dc, &l2_meta).unwrap();
        assert_eq!(report.decayed_nodes, 1);
        assert_eq!(report.pruned_edges, 3);
        assert_eq!(report.removed_nodes, 0);
        let node = read_scene_node(&engine, 2);
        assert!(node.edge_ids.is_empty());
        assert!(node.importance < dc.node_prune_edges_threshold);
        assert!(node.importance >= dc.node_remove_threshold);
    }

    #[test]
    fn test_node_removal() {
        let mut engine = create_engine();
        let old_time = now_ms() - 400 * 3_600_000;
        allocate_scene_node_page(&mut engine, 3, 0.5, old_time, vec![10]);
        let dc = default_decay_config();
        let l2_meta = L2MetaIndex::new();
        let report = decay_l1_network(&mut engine, &dc, &l2_meta).unwrap();
        assert_eq!(report.removed_nodes, 1);
        assert_eq!(report.decayed_nodes, 0);
        assert!(!engine.contains(3));
    }

    #[test]
    fn test_edge_decay_and_update() {
        let mut engine = create_engine();
        let old_time = now_ms() - 10 * 3_600_000;
        allocate_scene_edge_page(&mut engine, 10, 0.5, old_time, vec![1, 2]);
        let dc = default_decay_config();
        let l2_meta = L2MetaIndex::new();
        let report = decay_l1_network(&mut engine, &dc, &l2_meta).unwrap();
        assert_eq!(report.removed_edges, 0);
        assert!(engine.contains(10));
        let edge = read_scene_edge(&engine, 10);
        let expected = 0.5 * (-dc.lambda_edge * 10.0).exp();
        assert!((edge.weight - expected).abs() < 1e-5);
    }

    #[test]
    fn test_edge_removal_by_weight() {
        let mut engine = create_engine();
        let old_time = now_ms() - 200 * 3_600_000;
        allocate_scene_edge_page(&mut engine, 11, 0.5, old_time, vec![1, 2]);
        let dc = default_decay_config();
        let l2_meta = L2MetaIndex::new();
        let report = decay_l1_network(&mut engine, &dc, &l2_meta).unwrap();
        assert_eq!(report.removed_edges, 1);
        assert!(!engine.contains(11));
    }

    #[test]
    fn test_edge_removal_by_underpopulation() {
        let mut engine = create_engine();
        let old_time = now_ms();
        allocate_scene_edge_page(&mut engine, 12, 1.0, old_time, vec![1]);
        let dc = default_decay_config();
        let l2_meta = L2MetaIndex::new();
        let report = decay_l1_network(&mut engine, &dc, &l2_meta).unwrap();
        assert_eq!(report.removed_edges, 1);
        assert!(!engine.contains(12));
    }

    #[test]
    fn test_edge_cleans_stale_node_references() {
        let mut engine = create_engine();
        let old_time = now_ms() - 400 * 3_600_000;
        allocate_scene_node_page(&mut engine, 4, 0.5, old_time, vec![20]);
        allocate_scene_edge_page(&mut engine, 20, 1.0, now_ms(), vec![4, 5]);
        allocate_scene_node_page(&mut engine, 5, 1.0, now_ms(), vec![20]);
        let dc = default_decay_config();
        let l2_meta = L2MetaIndex::new();
        let report = decay_l1_network(&mut engine, &dc, &l2_meta).unwrap();
        assert_eq!(report.removed_nodes, 1);
        assert_eq!(report.removed_edges, 1);
        assert!(!engine.contains(20));
        assert!(!engine.contains(4));
        assert!(engine.contains(5));
    }

    #[test]
    fn test_edge_survives_after_cleaning_stale_refs() {
        let mut engine = create_engine();
        let old_time = now_ms() - 400 * 3_600_000;
        allocate_scene_node_page(&mut engine, 6, 0.5, old_time, vec![21]);
        allocate_scene_edge_page(&mut engine, 21, 1.0, now_ms(), vec![6, 7, 8]);
        allocate_scene_node_page(&mut engine, 7, 1.0, now_ms(), vec![21]);
        allocate_scene_node_page(&mut engine, 8, 1.0, now_ms(), vec![21]);
        let dc = default_decay_config();
        let l2_meta = L2MetaIndex::new();
        let report = decay_l1_network(&mut engine, &dc, &l2_meta).unwrap();
        assert_eq!(report.removed_nodes, 1);
        assert_eq!(report.removed_edges, 0);
        assert!(engine.contains(21));
        let edge = read_scene_edge(&engine, 21);
        assert_eq!(edge.node_ids, vec![7, 8]);
    }

    #[test]
    fn test_empty_btree_does_nothing() {
        let mut engine = create_engine();
        let dc = default_decay_config();
        let l2_meta = L2MetaIndex::new();
        let report = decay_l1_network(&mut engine, &dc, &l2_meta).unwrap();
        assert_eq!(report.decayed_nodes, 0);
        assert_eq!(report.pruned_edges, 0);
        assert_eq!(report.removed_nodes, 0);
        assert_eq!(report.removed_edges, 0);
    }

    #[test]
    fn test_pruned_node_clears_edge_reference() {
        let mut engine = create_engine();
        let old_time = now_ms() - 20 * 3_600_000;
        let dc = default_decay_config();
        let l2_meta = L2MetaIndex::new();
        let target = (dc.node_remove_threshold + dc.node_prune_edges_threshold) / 2.0;
        let start_importance = target / (-dc.lambda_node * 20.0).exp();
        allocate_scene_node_page(&mut engine, 100, start_importance, old_time, vec![50]);
        allocate_scene_node_page(&mut engine, 101, 1.0, now_ms(), vec![50]);
        allocate_scene_edge_page(&mut engine, 50, 1.0, now_ms(), vec![100, 101]);
        let dc2 = default_decay_config();
        let l2_meta = L2MetaIndex::new();
        let report = decay_l1_network(&mut engine, &dc2, &l2_meta).unwrap();
        assert_eq!(report.pruned_edges, 1);
        assert_eq!(report.removed_edges, 1);
        assert!(!engine.contains(50));
        let a = read_scene_node(&engine, 100);
        assert!(a.edge_ids.is_empty());
        let b = read_scene_node(&engine, 101);
        assert!(!b.edge_ids.contains(&50));
    }

    #[test]
    fn test_removed_edge_clears_node_references() {
        let mut engine = create_engine();
        let old_time = now_ms() - 200 * 3_600_000;
        allocate_scene_node_page(&mut engine, 200, 1.0, now_ms(), vec![60]);
        allocate_scene_node_page(&mut engine, 201, 1.0, now_ms(), vec![60]);
        allocate_scene_edge_page(&mut engine, 60, 0.5, old_time, vec![200, 201]);
        let dc = default_decay_config();
        let l2_meta = L2MetaIndex::new();
        let report = decay_l1_network(&mut engine, &dc, &l2_meta).unwrap();
        assert_eq!(report.removed_edges, 1);
        assert!(!engine.contains(60));
        let a = read_scene_node(&engine, 200);
        assert!(!a.edge_ids.contains(&60));
        let b = read_scene_node(&engine, 201);
        assert!(!b.edge_ids.contains(&60));
    }

    #[test]
    fn test_pruned_node_edge_survives_with_other_nodes() {
        let mut engine = create_engine();
        let old_time = now_ms() - 20 * 3_600_000;
        let dc = default_decay_config();
        let l2_meta = L2MetaIndex::new();
        let target = (dc.node_remove_threshold + dc.node_prune_edges_threshold) / 2.0;
        let start_importance = target / (-dc.lambda_node * 20.0).exp();
        allocate_scene_node_page(&mut engine, 300, start_importance, old_time, vec![70]);
        allocate_scene_node_page(&mut engine, 301, 1.0, now_ms(), vec![70]);
        allocate_scene_node_page(&mut engine, 302, 1.0, now_ms(), vec![70]);
        allocate_scene_edge_page(&mut engine, 70, 1.0, now_ms(), vec![300, 301, 302]);
        let dc2 = default_decay_config();
        let l2_meta = L2MetaIndex::new();
        let report = decay_l1_network(&mut engine, &dc2, &l2_meta).unwrap();
        assert_eq!(report.pruned_edges, 1);
        assert_eq!(report.removed_edges, 0);
        assert!(engine.contains(70));
        let a = read_scene_node(&engine, 300);
        assert!(a.edge_ids.is_empty());
        let b = read_scene_node(&engine, 301);
        assert!(b.edge_ids.contains(&70));
        let c = read_scene_node(&engine, 302);
        assert!(c.edge_ids.contains(&70));
        let edge = read_scene_edge(&engine, 70);
        assert_eq!(edge.node_ids, vec![301, 302]);
    }
}
