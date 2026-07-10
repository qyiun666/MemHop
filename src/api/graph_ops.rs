// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L3 graph API operations (API-7).

use crate::query::types::{
    GraphEdgeKind, GraphNode, L3Detail, Subgraph, TraversalHop, UpdateL3Fields,
};
use crate::storage::record::REC_L3_GRAPH_NODE;
use crate::{MemHop, Result};
use std::collections::HashSet;

impl MemHop {
    /// Parse a string into a `GraphEdgeKind`.
    fn parse_graph_edge_kind(s: &str) -> Option<GraphEdgeKind> {
        match s {
            "Related" | "related" => Some(GraphEdgeKind::Related),
            "Causal" | "causal" => Some(GraphEdgeKind::Causal),
            "PartOf" | "part_of" => Some(GraphEdgeKind::PartOf),
            "Sequence" | "sequence" => Some(GraphEdgeKind::Sequence),
            "Dependency" | "dependency" => Some(GraphEdgeKind::Dependency),
            "Custom" | "custom" => Some(GraphEdgeKind::Custom),
            _ => None,
        }
    }

    /// Query a subgraph reachable from `start_node` within `max_depth` hops.
    pub fn graph_query(
        &mut self,
        graph_id: &str,
        start_node: &str,
        max_depth: usize,
        edge_kinds: Option<Vec<String>>,
    ) -> Result<Subgraph> {
        let (subgraph, _hops) =
            self.graph_query_internal(graph_id, start_node, max_depth, edge_kinds)?;
        Ok(subgraph)
    }

    /// Internal graph query that returns both the subgraph and the traversal hops.
    pub(crate) fn graph_query_internal(
        &mut self,
        graph_id: &str,
        start_node: &str,
        max_depth: usize,
        edge_kinds: Option<Vec<String>>,
    ) -> Result<(Subgraph, Vec<TraversalHop>)> {
        let graph_hash = crate::shared::common::parse_id_to_hash(graph_id);
        let start_hash = crate::shared::common::parse_id_to_hash(start_node);

        let kinds = edge_kinds.and_then(|vec| {
            let parsed: Vec<_> = vec
                .iter()
                .filter_map(|s| Self::parse_graph_edge_kind(s))
                .collect();
            if parsed.is_empty() {
                None
            } else {
                Some(parsed)
            }
        });

        let hops = crate::l3::store::bfs_traversal_cached(
            &self.engine,
            graph_hash,
            start_hash,
            max_depth,
            kinds.as_deref(),
            &mut self.adjacency_cache,
        )?;

        let mut node_hashes = HashSet::new();
        let mut edge_ids = HashSet::new();
        let mut edges = Vec::new();

        node_hashes.insert(start_hash);
        for hop in &hops {
            node_hashes.insert(hop.from_node);
            node_hashes.insert(hop.to_node);
            if edge_ids.insert(hop.edge.id.clone()) {
                edges.push(hop.edge.clone());
            }
        }

        let mut nodes: Vec<GraphNode> = Vec::new();
        for &node_hash in &node_hashes {
            if let Ok(Some((rt, node_data))) = self.engine.read_record(node_hash) {
                if rt == REC_L3_GRAPH_NODE {
                    if let Ok(node) =
                        crate::layers::hypergraph::HypergraphNode::deserialize(node_data)
                    {
                        if node.graph_id == graph_hash {
                            nodes.push(node.into());
                        }
                    }
                }
            }
        }

        Ok((Subgraph { nodes, edges }, hops))
    }

    /// Detect isolated (or low-degree) nodes in an L3 hypergraph.
    pub fn l3_detect_isolated(
        &mut self,
        graph_id: &str,
        threshold: u32,
    ) -> Result<crate::l3::degree::IsolatedResult> {
        let graph_hash = crate::shared::common::parse_id_to_hash(graph_id);
        crate::l3::degree::detect_isolated(
            &self.engine,
            graph_hash,
            &mut self.degree_tracker,
            threshold,
        )
    }

    /// Run Leiden community detection on an L3 hypergraph.
    pub fn l3_detect_communities(
        &mut self,
        graph_id: &str,
        config: Option<crate::l3::CommunityConfig>,
    ) -> Result<crate::l3::CommunityResult> {
        let graph_hash = crate::shared::common::parse_id_to_hash(graph_id);
        let cfg = config.unwrap_or_default();
        crate::l3::community::run_community_detection(&self.engine, graph_hash, &cfg)
    }

    /// Execute an L3 DSL query against a hypergraph.
    pub fn l3_query(
        &mut self,
        graph_id: &str,
        query: &str,
        page: usize,
    ) -> Result<crate::l3::dsl::QueryResult> {
        let graph_hash = crate::shared::common::parse_id_to_hash(graph_id);
        let ast = crate::l3::dsl::parser::parse(query)?;
        crate::l3::dsl::executor::execute(
            &ast,
            &self.engine,
            graph_hash,
            &mut self.adjacency_cache,
            page,
            20,
        )
    }

    /// Get an L3 hypergraph detail by ID.
    pub fn get_l3(&self, id: &str) -> Result<Option<L3Detail>> {
        crate::query::l3_ops::get_l3(&self.engine, id)
    }

    /// Partially update an L3 hypergraph container.
    pub fn update_l3(&mut self, id: &str, fields: UpdateL3Fields) -> Result<()> {
        crate::query::l3_ops::update_l3(&mut self.engine, id, fields)
    }

    /// Delete an L3 hypergraph by ID.
    pub fn delete_l3(&mut self, id: &str) -> Result<()> {
        crate::query::l3_ops::delete_l3(&mut self.engine, &mut self.adjacency_cache, id)
    }
}
