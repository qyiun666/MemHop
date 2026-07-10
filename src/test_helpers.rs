// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Shared test helpers for MemHop unit tests.

use crate::layers::context::ContextSlot;
use crate::layers::hypergraph::{
    GraphEdge, GraphEdgeKind, GraphNode, HypergraphEdge, HypergraphNode,
};
use crate::storage::StorageEngine;
use crate::store::l3_store::{add_edge as l3_add_edge, add_node as l3_add_node};

/// Build a `HypergraphNode` with DSL-test defaults.
pub fn make_node(id: u64, graph_id: u64, title: &str) -> GraphNode {
    HypergraphNode {
        id_hash: id,
        graph_id,
        title: title.to_string(),
        node_type: "concept".to_string(),
        content: "test".to_string(),
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

/// Build a `HypergraphEdge` with DSL-test defaults.
pub fn make_edge(id: u64, graph_id: u64, nodes: Vec<u64>) -> GraphEdge {
    HypergraphEdge {
        id_hash: id,
        graph_id,
        kind: GraphEdgeKind::Related,
        node_ids: nodes,
        weight: 1.0,
        label: None,
        confidence: 1.0,
        description: None,
        valid_from: 0,
        valid_until: 0,
        created_at: 0,
    }
}

/// Build a small graph for DSL executor tests (v2 engine API).
pub fn build_dsl_test_graph(engine: &mut StorageEngine) -> u64 {
    let gid = 1u64;
    let nodes = [
        make_node(101, gid, "Rust"),
        make_node(102, gid, "Cargo"),
        make_node(103, gid, "Borrow Checker"),
        make_node(104, gid, "Lifetime"),
        make_node(105, gid, "Trait"),
    ];
    for n in &nodes {
        l3_add_node(engine, n.clone(), None, None).unwrap();
    }
    let edges = [
        make_edge(201, gid, vec![101, 102]),
        make_edge(202, gid, vec![101, 103]),
        make_edge(203, gid, vec![103, 104]),
        make_edge(204, gid, vec![101, 105, 102]),
    ];
    for e in &edges {
        l3_add_edge(engine, e.clone(), None).unwrap();
    }
    gid
}

/// Build a `HypergraphNode` with store-test defaults.
pub fn create_test_node(id_hash: u64, graph_id: u64, title: &str) -> GraphNode {
    HypergraphNode {
        id_hash,
        graph_id,
        title: title.to_string(),
        node_type: "concept".to_string(),
        content: format!("content of {}", title),
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

/// Build a `HypergraphEdge` with store-test defaults.
pub fn create_test_edge(
    id_hash: u64,
    graph_id: u64,
    kind: GraphEdgeKind,
    node_ids: Vec<u64>,
) -> GraphEdge {
    HypergraphEdge {
        id_hash,
        graph_id,
        kind,
        node_ids,
        weight: 1.0,
        label: None,
        confidence: 1.0,
        description: None,
        valid_from: 0,
        valid_until: 0,
        created_at: 0,
    }
}

/// Build a small graph for L3 store traversal tests (v2 engine API).
pub fn build_test_graph(engine: &mut StorageEngine) -> (Vec<u64>, Vec<u64>) {
    let graph_id = 1u64;
    let node_ids = vec![101u64, 102, 103, 104, 105];
    let edge_ids = vec![201u64, 202, 203, 204];

    for &nid in &node_ids {
        l3_add_node(
            engine,
            create_test_node(nid, graph_id, &format!("node{}", nid)),
            None,
            None,
        )
        .unwrap();
    }

    l3_add_edge(
        engine,
        create_test_edge(201, graph_id, GraphEdgeKind::Related, vec![101, 102]),
        None,
    )
    .unwrap();
    l3_add_edge(
        engine,
        create_test_edge(202, graph_id, GraphEdgeKind::Related, vec![102, 103]),
        None,
    )
    .unwrap();
    l3_add_edge(
        engine,
        create_test_edge(203, graph_id, GraphEdgeKind::Dependency, vec![103, 104]),
        None,
    )
    .unwrap();
    l3_add_edge(
        engine,
        create_test_edge(204, graph_id, GraphEdgeKind::Causal, vec![101, 103, 105]),
        None,
    )
    .unwrap();

    (node_ids, edge_ids)
}

/// Build a `ContextSlot` with sparse-index-test defaults.
pub fn create_test_context(_id_hash: u64, title: &str, l3_refs: Vec<u64>) -> ContextSlot {
    ContextSlot {
        id: _id_hash,
        scene_id: 0,
        parent_id: None,
        children_ids: vec![],
        depth: 1,
        user_keywords: vec![title.to_string()],
        user_timestamp: 0,
        user_l4_refs: Vec::new(),
        user_l3_refs: l3_refs,
        agent_keywords: vec![],
        agent_timestamp: 0,
        agent_l4_refs: Vec::new(),
        agent_l3_refs: Vec::new(),
        fused_keywords: vec![],
        fused_summary: None,
        centroid_page_ref: 0,
        created_at: 0,
        updated_at: 0,
        version: 4,
    }
}
