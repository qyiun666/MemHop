// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! DSL query executor — translates AST into store.rs function calls.

use crate::index::btree::BTreeIndex;
use crate::l3::cache::AdjacencyCache;
use crate::l3::dsl::ast::*;
use crate::l3::dsl::result::QueryResult;
use crate::l3::store;
use crate::layers::hypergraph::{GraphEdgeKind, HypergraphEdge, HypergraphNode};
use crate::query::types::{EdgeListQuery, NodeListQuery};
use crate::MemHopError;
use memmap2::MmapMut;

/// Execute a parsed DSL query against the L3 store.
pub fn execute(
    query: &Query,
    mmap: &MmapMut,
    btree: &BTreeIndex,
    graph_id: u64,
    cache: &mut AdjacencyCache,
    page: usize,
    page_size: usize,
) -> Result<QueryResult, MemHopError> {
    match query {
        Query::Match(m) => execute_match(m, mmap, btree, graph_id, page, page_size),
        Query::Hyperedge(h) => execute_hyperedge(h, mmap, btree, graph_id, page, page_size),
        Query::Path(p) => execute_path(p, mmap, btree, graph_id, cache),
        Query::Subgraph(s) => execute_subgraph(s, mmap, btree, graph_id, cache),
    }
}

// ── MATCH executor ─────────────────────────────────────────────────────────

fn execute_match(
    m: &NodeMatch,
    mmap: &MmapMut,
    btree: &BTreeIndex,
    graph_id: u64,
    page: usize,
    page_size: usize,
) -> Result<QueryResult, MemHopError> {
    let list_query = NodeListQuery {
        page,
        page_size: page_size.max(m.limit.unwrap_or(page_size)),
        node_type: m.node_type.clone(),
        keyword: None,
        min_importance: None,
    };

    let result = store::list_nodes_by_graph(mmap, btree, graph_id, &list_query)?;
    let mut nodes = result.items;

    if let Some(ref where_clause) = m.where_clause {
        nodes.retain(|n| eval_where_node(where_clause, n));
    }

    if let Some(limit) = m.limit {
        nodes.truncate(limit);
    }

    let total = nodes.len();
    Ok(QueryResult::Nodes {
        items: nodes,
        total,
    })
}

// ── HYPEREDGE executor ─────────────────────────────────────────────────────

fn execute_hyperedge(
    h: &HyperedgeMatch,
    mmap: &MmapMut,
    btree: &BTreeIndex,
    graph_id: u64,
    page: usize,
    page_size: usize,
) -> Result<QueryResult, MemHopError> {
    let list_query = EdgeListQuery {
        page,
        page_size: page_size.max(h.limit.unwrap_or(page_size)),
        kind: None,
        node_id: None,
    };

    let result = store::list_edges_by_graph(mmap, btree, graph_id, &list_query)?;
    let mut edges = result.items;

    if let Some(ref where_clause) = h.where_clause {
        edges.retain(|e| eval_where_edge(where_clause, e));
    }

    if let Some(limit) = h.limit {
        edges.truncate(limit);
    }

    let total = edges.len();
    Ok(QueryResult::Edges {
        items: edges,
        total,
    })
}

// ── PATH executor ──────────────────────────────────────────────────────────

fn execute_path(
    p: &PathQuery,
    mmap: &MmapMut,
    btree: &BTreeIndex,
    graph_id: u64,
    cache: &mut AdjacencyCache,
) -> Result<QueryResult, MemHopError> {
    let start_hash = crate::shared::common::parse_id_to_hash(&p.start_node);

    let edge_kinds = p.edge_kinds.as_ref().and_then(|kinds| {
        let parsed: Vec<GraphEdgeKind> = kinds.iter().filter_map(|s| parse_edge_kind(s)).collect();
        if parsed.is_empty() {
            None
        } else {
            Some(parsed)
        }
    });

    let data: &[u8] = &mmap[..];
    let hops = store::bfs_traversal_cached(
        data,
        btree,
        graph_id,
        start_hash,
        p.max_depth,
        edge_kinds.as_deref(),
        cache,
    )?;

    let total = hops.len();
    Ok(QueryResult::Hops { items: hops, total })
}

// ── SUBGRAPH executor ──────────────────────────────────────────────────────

fn execute_subgraph(
    s: &SubgraphQuery,
    mmap: &MmapMut,
    btree: &BTreeIndex,
    graph_id: u64,
    cache: &mut AdjacencyCache,
) -> Result<QueryResult, MemHopError> {
    let start_hash = crate::shared::common::parse_id_to_hash(&s.start_node);

    let data: &[u8] = &mmap[..];
    let hops =
        store::bfs_traversal_cached(data, btree, graph_id, start_hash, s.max_depth, None, cache)?;

    let mut node_hashes = std::collections::HashSet::new();
    let mut edge_ids = std::collections::HashSet::new();
    let mut edges = Vec::new();

    node_hashes.insert(start_hash);
    for hop in &hops {
        node_hashes.insert(hop.from_node);
        node_hashes.insert(hop.to_node);
        if edge_ids.insert(hop.edge.id_hash) {
            edges.push(hop.edge.clone());
        }
    }

    let mut nodes: Vec<HypergraphNode> = Vec::new();
    for &node_hash in &node_hashes {
        if let Some(page_ref) = btree.search(node_hash) {
            if let Some(slot_data) = crate::shared::slot_io::get_slot_data(data, page_ref) {
                if let Ok(node) = HypergraphNode::deserialize(slot_data) {
                    if node.graph_id == graph_id {
                        nodes.push(node);
                    }
                }
            }
        }
    }

    Ok(QueryResult::Subgraph(crate::query::types::Subgraph {
        nodes,
        edges,
    }))
}

// ── WHERE evaluation ───────────────────────────────────────────────────────

fn eval_where_node(cond: &WhereCondition, node: &HypergraphNode) -> bool {
    match cond {
        WhereCondition::PropertyCompare {
            property,
            operator,
            value,
        } => {
            let node_val = match property.as_str() {
                "importance" => node.importance,
                "version" => node.version as f32,
                _ => return false,
            };
            apply_op(*operator, node_val, *value)
        }
        WhereCondition::TypeEquals(type_str) => node.node_type == *type_str,
        WhereCondition::KeywordContains(keyword) => {
            node.keywords.iter().any(|k| k.contains(keyword))
        }
        WhereCondition::And(a, b) => eval_where_node(a, node) && eval_where_node(b, node),
        WhereCondition::Or(a, b) => eval_where_node(a, node) || eval_where_node(b, node),
    }
}

fn eval_where_edge(cond: &WhereCondition, edge: &HypergraphEdge) -> bool {
    match cond {
        WhereCondition::PropertyCompare {
            property,
            operator,
            value,
        } => {
            let edge_val = match property.as_str() {
                "weight" => edge.weight,
                _ => return false,
            };
            apply_op(*operator, edge_val, *value)
        }
        WhereCondition::TypeEquals(type_str) => {
            format!("{:?}", edge.kind).to_lowercase() == *type_str.to_lowercase()
        }
        _ => false,
    }
}

fn apply_op(op: CompareOp, left: f32, right: f32) -> bool {
    match op {
        CompareOp::Gt => left > right,
        CompareOp::Ge => left >= right,
        CompareOp::Lt => left < right,
        CompareOp::Le => left <= right,
        CompareOp::Eq => (left - right).abs() < f32::EPSILON,
        CompareOp::Ne => (left - right).abs() >= f32::EPSILON,
    }
}

// ── Helpers ────────────────────────────────────────────────────────────────

fn parse_edge_kind(s: &str) -> Option<GraphEdgeKind> {
    match s.to_lowercase().as_str() {
        "related" => Some(GraphEdgeKind::Related),
        "causal" => Some(GraphEdgeKind::Causal),
        "partof" | "part_of" => Some(GraphEdgeKind::PartOf),
        "sequence" => Some(GraphEdgeKind::Sequence),
        "dependency" => Some(GraphEdgeKind::Dependency),
        "custom" => Some(GraphEdgeKind::Custom),
        _ => None,
    }
}

// ── Tests ──────────────────────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;
    use crate::test_helpers::*;

    #[test]
    fn test_execute_match_all() {
        let (mut mmap, mut header, mut btree, mut file) = create_test_mmap(128);
        let gid = build_dsl_test_graph(&mut mmap, &mut header, &mut btree, &mut file);
        let mut cache = AdjacencyCache::new();

        let q = Query::Match(NodeMatch {
            variable: None,
            node_type: None,
            where_clause: None,
            limit: None,
        });
        let result = execute(&q, &mmap, &btree, gid, &mut cache, 1, 20).unwrap();
        match result {
            QueryResult::Nodes { items, total } => {
                assert_eq!(total, 5);
                assert_eq!(items.len(), 5);
            }
            _ => panic!("expected Nodes"),
        }
    }

    #[test]
    fn test_execute_match_with_where() {
        let (mut mmap, mut header, mut btree, mut file) = create_test_mmap(128);
        let gid = build_dsl_test_graph(&mut mmap, &mut header, &mut btree, &mut file);
        let mut cache = AdjacencyCache::new();

        let q = Query::Match(NodeMatch {
            variable: None,
            node_type: None,
            where_clause: Some(WhereCondition::PropertyCompare {
                property: "importance".into(),
                operator: CompareOp::Gt,
                value: 0.4,
            }),
            limit: None,
        });
        let result = execute(&q, &mmap, &btree, gid, &mut cache, 1, 20).unwrap();
        match result {
            QueryResult::Nodes { items, .. } => {
                assert!(items.iter().all(|n| n.importance > 0.4));
            }
            _ => panic!("expected Nodes"),
        }
    }

    #[test]
    fn test_execute_path() {
        let (mut mmap, mut header, mut btree, mut file) = create_test_mmap(128);
        let gid = build_dsl_test_graph(&mut mmap, &mut header, &mut btree, &mut file);
        let mut cache = AdjacencyCache::new();

        // node 101 (Rust) connects to 102, 103, 105
        let start_id = format!("{:016x}", 101u64);
        let q = Query::Path(PathQuery {
            start_node: start_id,
            max_depth: 2,
            edge_kinds: None,
        });
        let result = execute(&q, &mmap, &btree, gid, &mut cache, 1, 20).unwrap();
        match result {
            QueryResult::Hops { items, .. } => {
                assert!(!items.is_empty(), "should find hops from node 101");
            }
            _ => panic!("expected Hops"),
        }
    }

    #[test]
    fn test_execute_subgraph() {
        let (mut mmap, mut header, mut btree, mut file) = create_test_mmap(128);
        let gid = build_dsl_test_graph(&mut mmap, &mut header, &mut btree, &mut file);
        let mut cache = AdjacencyCache::new();

        let start_id = format!("{:016x}", 101u64);
        let q = Query::Subgraph(SubgraphQuery {
            start_node: start_id,
            max_depth: 1,
        });
        let result = execute(&q, &mmap, &btree, gid, &mut cache, 1, 20).unwrap();
        match result {
            QueryResult::Subgraph(sub) => {
                assert!(!sub.nodes.is_empty(), "subgraph should contain nodes");
            }
            _ => panic!("expected Subgraph"),
        }
    }

    #[test]
    fn test_execute_hyperedge() {
        let (mut mmap, mut header, mut btree, mut file) = create_test_mmap(128);
        let gid = build_dsl_test_graph(&mut mmap, &mut header, &mut btree, &mut file);
        let mut cache = AdjacencyCache::new();

        let q = Query::Hyperedge(HyperedgeMatch {
            edge_var: None,
            node_vars: vec![],
            where_clause: None,
            limit: None,
        });
        let result = execute(&q, &mmap, &btree, gid, &mut cache, 1, 20).unwrap();
        match result {
            QueryResult::Edges { items, .. } => {
                assert_eq!(items.len(), 4);
            }
            _ => panic!("expected Edges"),
        }
    }
}
