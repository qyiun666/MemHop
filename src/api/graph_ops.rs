// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L3 graph API operations.

use crate::MemHop;
use crate::Result;
use std::collections::HashSet;

impl MemHop {
    /// Parse a string into a `GraphEdgeKind`.
    fn parse_graph_edge_kind(s: &str) -> Option<crate::layers::hypergraph::GraphEdgeKind> {
        use crate::layers::hypergraph::GraphEdgeKind;
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
    ) -> Result<crate::query::types::Subgraph> {
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
    ) -> Result<(
        crate::query::types::Subgraph,
        Vec<crate::query::types::TraversalHop>,
    )> {
        use crate::layers::hypergraph::HypergraphNode;
        use crate::query::types::Subgraph;

        let graph_hash = crate::shared::common::parse_id_to_hash(graph_id);
        let start_hash = crate::shared::common::parse_id_to_hash(start_node);

        let kinds = edge_kinds.and_then(|vec| {
            let parsed: Vec<_> = vec
                .iter()
                .filter_map(|s| Self::parse_graph_edge_kind(s))
                .collect();
            // Treat empty array as None (no filtering)
            if parsed.is_empty() {
                None
            } else {
                Some(parsed)
            }
        });

        let data: &[u8] = &self.mmap[..];
        let hops = crate::l3::store::bfs_traversal_cached(
            data,
            &self.btree,
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
            if edge_ids.insert(hop.edge.id_hash) {
                edges.push(hop.edge.clone());
            }
        }

        let mut nodes: Vec<HypergraphNode> = Vec::new();
        for &node_hash in &node_hashes {
            if let Some(page_ref) = self.btree.search(node_hash) {
                if let Some(slot_data) = crate::shared::slot_io::get_slot_data(data, page_ref) {
                    if let Ok(node) = HypergraphNode::deserialize(slot_data) {
                        if node.graph_id == graph_hash {
                            nodes.push(node);
                        }
                    }
                }
            }
        }

        Ok((Subgraph { nodes, edges }, hops))
    }

    /// Detect isolated (or low-degree) nodes in an L3 hypergraph.
    ///
    /// A node is "isolated" if it has degree 0 (no hyperedge references it).
    /// Set `threshold > 0` to also include weakly-connected nodes.
    pub fn l3_detect_isolated(
        &mut self,
        graph_id: &str,
        threshold: u32,
    ) -> Result<crate::l3::degree::IsolatedResult> {
        let graph_hash = crate::shared::common::parse_id_to_hash(graph_id);
        crate::l3::degree::detect_isolated(
            &self.mmap,
            &self.btree,
            graph_hash,
            &mut self.degree_tracker,
            threshold,
        )
    }

    /// Run Leiden community detection on an L3 hypergraph.
    ///
    /// Hypergraph edges are reduced to binary edges via clique expansion
    /// before running the Leiden algorithm.
    pub fn l3_detect_communities(
        &mut self,
        graph_id: &str,
        config: Option<crate::l3::CommunityConfig>,
    ) -> Result<crate::l3::CommunityResult> {
        let graph_hash = crate::shared::common::parse_id_to_hash(graph_id);
        let cfg = config.unwrap_or_default();
        crate::l3::community::run_community_detection(&self.mmap, &self.btree, graph_hash, &cfg)
    }

    /// Execute an L3 DSL query against a hypergraph.
    ///
    /// Supports MATCH, HYPEREDGE, PATH, and SUBGRAPH query types.
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
            &self.mmap,
            &self.btree,
            graph_hash,
            &mut self.adjacency_cache,
            page,
            20,
        )
    }

    /// Delete an L3 hypergraph and clean up its references from L2 contexts.
    pub fn delete_graph(&mut self, graph_id: u64) -> Result<()> {
        let l3_id_str = crate::shared::common::format_hash(graph_id);

        // Collect L2 ContextSlots that reference this graph before deleting it.
        let l2_refs = crate::l3::store::collect_l2_refs(&self.mmap, &self.btree, graph_id)?;

        crate::l3::store::delete_graph(
            &mut self.mmap,
            &mut self.header,
            &mut self.btree,
            &l3_id_str,
        )?;

        // Remove the graph reference from each L2 context.
        for (page_id, _id_hash) in l2_refs {
            crate::l3::store::remove_l3_ref_from_context(&mut self.mmap, page_id, graph_id)?;
        }

        // Invalidate adjacency cache for this graph
        self.adjacency_cache.invalidate(graph_id);

        Ok(())
    }
}
