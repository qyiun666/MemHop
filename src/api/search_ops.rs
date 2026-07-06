// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Search API operations.

use crate::query::types::KnowledgeNodesResult;
#[cfg(feature = "grpc-encoder")]
use crate::query::types::{SearchQuery, SearchResult};
use crate::MemHop;
use crate::MemHopError;
use crate::Result;

impl MemHop {
    /// Search memory using topic-centric retrieval model.
    ///
    /// # Arguments
    /// * `query` - Search query with dialogue, filters, and optional encoder-backed vector retrieval
    ///
    /// # Returns
    /// `SearchResult` containing profile, contexts, associated contexts, L3 IDs, etc.
    #[cfg(feature = "grpc-encoder")]
    pub fn search_context(&mut self, query: SearchQuery) -> Result<SearchResult> {
        use crate::query::search::search_context;

        let result = search_context(
            &mut self.mmap,
            &mut self.header,
            query,
            &mut self.btree,
            &mut self.sparse_index,
            &self.l2_meta,
            self.config.vector_dim,
            self.encoder.as_deref(),
            self.config
                .search_weights
                .as_ref()
                .unwrap_or(&crate::config::SearchWeights::default()),
            self.ivf_index.as_ref(),
            &self.l1_reverse_index,
            &mut self.file,
        );

        // After search (which may have auto-created a new context), rebuild IVF
        self.rebuild_ivf_index();

        result
    }

    /// Search L3 knowledge nodes within a graph by exact keyword.
    ///
    /// Uses the in-memory L3 index for the graph and returns node details
    /// (without full text). If the graph has no loaded L3 index, returns an error.
    pub fn search_knowledge_nodes_by_keyword(
        &self,
        graph_id: &str,
        keyword: &str,
        limit: usize,
    ) -> Result<KnowledgeNodesResult> {
        let graph_hash = crate::shared::common::parse_id_to_hash(graph_id);
        let index = self.l3_index_map.get(&graph_hash).ok_or_else(|| {
            MemHopError::Serialization(format!("L3 index not found for graph {}", graph_id))
        })?;
        let node_hashes = index.search_by_keyword(keyword, limit);
        let data: &[u8] = &self.mmap[..];
        let nodes: Vec<crate::query::types::KnowledgeNodeDetail> = node_hashes
            .into_iter()
            .filter_map(|h| self.resolve_knowledge_node_detail(data, h, false))
            .collect();
        Ok(KnowledgeNodesResult {
            total: nodes.len(),
            nodes,
            requested: limit,
        })
    }

    /// Get L3 knowledge nodes within a graph by node type.
    ///
    /// Uses the in-memory L3 index for the graph and returns node details
    /// (without full text). If the graph has no loaded L3 index, returns an error.
    pub fn get_knowledge_nodes_by_type(
        &self,
        graph_id: &str,
        node_type: &str,
        limit: usize,
    ) -> Result<KnowledgeNodesResult> {
        let graph_hash = crate::shared::common::parse_id_to_hash(graph_id);
        let index = self.l3_index_map.get(&graph_hash).ok_or_else(|| {
            MemHopError::Serialization(format!("L3 index not found for graph {}", graph_id))
        })?;
        let node_hashes = index.get_nodes_by_type(node_type, limit);
        let data: &[u8] = &self.mmap[..];
        let nodes: Vec<crate::query::types::KnowledgeNodeDetail> = node_hashes
            .into_iter()
            .filter_map(|h| self.resolve_knowledge_node_detail(data, h, false))
            .collect();
        Ok(KnowledgeNodesResult {
            total: nodes.len(),
            nodes,
            requested: limit,
        })
    }
}
