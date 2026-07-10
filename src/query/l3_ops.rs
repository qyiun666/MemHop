// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L3 Hypergraph CRUD internal implementation (v2 engine).

use crate::l3::AdjacencyCache;
use crate::layers::hypergraph::{HypergraphEdge, HypergraphNode, HypergraphSlot};
use crate::query::types::{GraphEdge, GraphNode, L3Detail, UpdateL3Fields};
use crate::shared::common::{now_ms, parse_id_to_hash};
use crate::storage::record::*;
use crate::storage::StorageEngine;
use crate::MemHopError;

/// Get an L3 hypergraph by ID, including all nodes and edges.
pub fn get_l3(engine: &StorageEngine, id: &str) -> Result<Option<L3Detail>, MemHopError> {
    let graph_hash = parse_id_to_hash(id);

    let slot = match engine.read_record(graph_hash)? {
        Some((rt, data)) if rt == REC_L3_GRAPH_SLOT => bincode::deserialize::<HypergraphSlot>(data)
            .map_err(|e| MemHopError::Deserialization(e.to_string()))?,
        _ => return Ok(None),
    };

    let mut nodes: Vec<GraphNode> = Vec::new();
    let mut edges: Vec<GraphEdge> = Vec::new();

    for (&id_hash, _) in engine.iter_index() {
        let Some((rt, data)) = engine.read_record(id_hash)? else {
            continue;
        };
        match rt {
            REC_L3_GRAPH_NODE => {
                if let Ok(node) = bincode::deserialize::<HypergraphNode>(data) {
                    if node.graph_id == graph_hash {
                        nodes.push(node.into());
                    }
                }
            }
            REC_L3_GRAPH_EDGE => {
                if let Ok(edge) = bincode::deserialize::<HypergraphEdge>(data) {
                    if edge.graph_id == graph_hash {
                        edges.push(edge.into());
                    }
                }
            }
            _ => {}
        }
    }

    Ok(Some(L3Detail {
        slot: slot.into(),
        nodes,
        edges,
    }))
}

/// Partially update an L3 hypergraph container.
pub fn update_l3(
    engine: &mut StorageEngine,
    id: &str,
    fields: UpdateL3Fields,
) -> Result<(), MemHopError> {
    let graph_hash = parse_id_to_hash(id);
    let (_, data) = engine
        .read_record(graph_hash)?
        .ok_or(MemHopError::PageNotFound(0))?;
    let mut slot = bincode::deserialize::<HypergraphSlot>(data)
        .map_err(|e| MemHopError::Deserialization(e.to_string()))?;

    if let Some(name) = fields.name {
        slot.name = name;
    }

    slot.updated_at = now_ms();
    slot.version += 1;

    let data = bincode::serialize(&slot).map_err(|e| MemHopError::Serialization(e.to_string()))?;
    engine.write_record(REC_L3_GRAPH_SLOT, graph_hash, &data)?;

    Ok(())
}

/// Delete an L3 hypergraph and clean up L2 references.
pub fn delete_l3(
    engine: &mut StorageEngine,
    adjacency_cache: &mut AdjacencyCache,
    l3_id: &str,
) -> Result<(), MemHopError> {
    let graph_hash = parse_id_to_hash(l3_id);

    // Collect all L2 topics that reference this graph
    let mut l2_refs: Vec<u64> = Vec::new();
    for (&id_hash, _) in engine.iter_index() {
        let Some((rt, data)) = engine.read_record(id_hash)? else {
            continue;
        };
        if rt != REC_L2_TOPIC {
            continue;
        }
        if let Ok(ctx) = bincode::deserialize::<crate::layers::context::ContextSlot>(data) {
            if ctx.user_l3_refs.contains(&graph_hash) || ctx.agent_l3_refs.contains(&graph_hash) {
                l2_refs.push(id_hash);
            }
        }
    }

    // Delete all nodes and edges belonging to this graph
    let mut to_delete: Vec<u64> = Vec::new();
    for (&id_hash, _) in engine.iter_index() {
        let Some((rt, data)) = engine.read_record(id_hash)? else {
            continue;
        };
        match rt {
            REC_L3_GRAPH_NODE => {
                if let Ok(node) = bincode::deserialize::<HypergraphNode>(data) {
                    if node.graph_id == graph_hash {
                        to_delete.push(id_hash);
                    }
                }
            }
            REC_L3_GRAPH_EDGE => {
                if let Ok(edge) = bincode::deserialize::<HypergraphEdge>(data) {
                    if edge.graph_id == graph_hash {
                        to_delete.push(id_hash);
                    }
                }
            }
            _ => {}
        }
    }
    for id_hash in to_delete {
        engine.delete_record(id_hash)?;
    }

    // Delete the graph slot itself
    engine.delete_record(graph_hash)?;

    // Remove reference from each L2 topic
    for id_hash in &l2_refs {
        let (_, data) = engine
            .read_record(*id_hash)?
            .ok_or(MemHopError::PageNotFound(0))?;
        let mut ctx: crate::layers::context::ContextSlot =
            bincode::deserialize(data).map_err(|e| MemHopError::Deserialization(e.to_string()))?;
        ctx.user_l3_refs.retain(|&h| h != graph_hash);
        ctx.agent_l3_refs.retain(|&h| h != graph_hash);
        ctx.updated_at = now_ms();
        let data =
            bincode::serialize(&ctx).map_err(|e| MemHopError::Serialization(e.to_string()))?;
        engine.write_record(REC_L2_TOPIC, *id_hash, &data)?;
    }

    adjacency_cache.invalidate(graph_hash);

    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::layers::hypergraph::{GraphEdgeKind, HypergraphSource};
    use crate::store::write_slot;
    use tempfile::NamedTempFile;

    fn make_graph_slot(id_hash: u64, name: &str) -> HypergraphSlot {
        HypergraphSlot {
            id_hash,
            name: name.to_string(),
            source: HypergraphSource::Manual,
            node_count: 0,
            edge_count: 0,
            created_at: 0,
            updated_at: 0,
            version: 1,
        }
    }

    fn make_node(id_hash: u64, graph_id: u64, title: &str) -> HypergraphNode {
        HypergraphNode {
            id_hash,
            graph_id,
            title: title.to_string(),
            node_type: "test".to_string(),
            content: String::new(),
            keywords: vec![],
            source_ref: None,
            importance: 0.5,
            summary: None,
            valid_from: 0,
            valid_until: 0,
            created_at: 0,
            updated_at: 0,
            version: 1,
        }
    }

    fn make_edge(id_hash: u64, graph_id: u64, node_ids: Vec<u64>) -> HypergraphEdge {
        HypergraphEdge {
            id_hash,
            graph_id,
            kind: GraphEdgeKind::Related,
            node_ids,
            weight: 1.0,
            label: None,
            description: None,
            confidence: 0.8,
            valid_from: 0,
            valid_until: 0,
            created_at: 0,
        }
    }

    #[test]
    fn test_l3_get_update_delete() {
        let temp = NamedTempFile::new().unwrap();
        let mut engine = StorageEngine::create(temp.path(), 768).unwrap();
        let mut adjacency_cache = AdjacencyCache::new();

        let graph_id = 1u64;
        let slot = make_graph_slot(graph_id, "test graph");
        write_slot(&mut engine, REC_L3_GRAPH_SLOT, graph_id, &slot).unwrap();

        let node = make_node(101, graph_id, "node101");
        write_slot(&mut engine, REC_L3_GRAPH_NODE, 101, &node).unwrap();

        let edge = make_edge(201, graph_id, vec![101]);
        write_slot(&mut engine, REC_L3_GRAPH_EDGE, 201, &edge).unwrap();

        let detail = get_l3(&engine, "0000000000000001")
            .unwrap()
            .expect("graph should exist");
        assert_eq!(detail.slot.name, "test graph");
        assert_eq!(detail.nodes.len(), 1);
        assert_eq!(detail.edges.len(), 1);

        update_l3(
            &mut engine,
            "0000000000000001",
            UpdateL3Fields {
                name: Some("renamed graph".into()),
            },
        )
        .unwrap();

        let updated = get_l3(&engine, "0000000000000001").unwrap().unwrap();
        assert_eq!(updated.slot.name, "renamed graph");

        delete_l3(&mut engine, &mut adjacency_cache, "0000000000000001").unwrap();
        assert!(get_l3(&engine, "0000000000000001").unwrap().is_none());
    }
}
