// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! CRUD/list API operations.

use crate::query::types::{
    Archive, ArchiveListResult, ArchivePageQuery, CrystalListQuery, CrystalListResult,
    EngramListQuery, EngramListResult, EngramResult, KnowledgeDetail, KnowledgeListQuery,
    KnowledgeListResult, KnowledgeNodeDetail, KnowledgeNodesResult,
};
use crate::storage::record::{REC_L3_GRAPH_NODE, REC_L3_GRAPH_SLOT};
use crate::MemHop;
use crate::Result;
impl MemHop {
    /// Get single engram by ID
    pub fn get_engram(&self, id: &str) -> Result<Option<EngramResult>> {
        use crate::query::list::get_engram as impl_fn;
        impl_fn(&self.engine, id)
    }

    /// List engrams with pagination and filtering
    pub fn list_engrams(&self, query: EngramListQuery) -> Result<EngramListResult> {
        use crate::query::list::list_engrams as impl_fn;
        impl_fn(&self.engine, query)
    }

    /// Get single archive by ID
    pub fn get_archive(&self, id: &str) -> Result<Option<Archive>> {
        use crate::query::list::get_archive as impl_fn;
        impl_fn(&self.engine, id)
    }

    /// List archives by topic ID
    pub fn list_archives_by_topic(
        &self,
        topic_id: &str,
        query: ArchivePageQuery,
    ) -> Result<ArchiveListResult> {
        use crate::query::list::list_archives_by_topic as impl_fn;
        impl_fn(&self.engine, topic_id, query)
    }

    /// List archives by node IDs
    pub fn list_archives_by_nodes(
        &self,
        node_ids: &[String],
        query: ArchivePageQuery,
    ) -> Result<ArchiveListResult> {
        use crate::query::list::list_archives_by_nodes as impl_fn;
        impl_fn(&self.engine, node_ids, query)
    }

    /// List all archives
    pub fn list_all_archives(&self, query: ArchivePageQuery) -> Result<ArchiveListResult> {
        use crate::query::list::list_all_archives as impl_fn;
        impl_fn(&self.engine, query)
    }

    /// List crystals with pagination and filtering
    pub fn list_crystals(&self, query: CrystalListQuery) -> Result<CrystalListResult> {
        use crate::query::list::list_crystals as impl_fn;
        impl_fn(&self.engine, query)
    }

    /// Activate a crystal (L5 action chain) by ID.
    ///
    /// Validates confidence >= 0.5 and at least one linked ActionStep, then
    /// flips the chain status to Active.
    pub fn activate_crystal(&mut self, id: &str) -> Result<()> {
        use crate::dream::crystallize_stage::activate_crystal as impl_fn;
        let chain_id = crate::shared::common::parse_id_to_hash(id);
        impl_fn(&mut self.engine, chain_id)
    }

    /// Get single knowledge (L3 hypergraph) by ID
    ///
    /// Uses l3::store engine to read the hypergraph and aggregate node content
    /// into the KnowledgeDetail structure.
    pub fn get_knowledge(&self, id: &str) -> Result<Option<KnowledgeDetail>> {
        let id_hash = crate::shared::common::parse_id_to_hash(id);

        // Read HypergraphSlot from engine
        let slot = match self.engine.read_record(id_hash) {
            Ok(Some((rt, data))) if rt == REC_L3_GRAPH_SLOT => {
                match crate::layers::hypergraph::HypergraphSlot::deserialize(data) {
                    Ok(s) => s,
                    Err(_) => return Ok(None),
                }
            }
            _ => return Ok(None),
        };

        // Aggregate node content using l3::store
        let source_ref = match &slot.source {
            crate::layers::hypergraph::HypergraphSource::Path(p) => Some(p.clone()),
            crate::layers::hypergraph::HypergraphSource::Url(u) => Some(u.clone()),
            _ => None,
        };

        let mut text = String::new();
        let mut summary: Option<String> = None;
        let mut keywords: Vec<String> = Vec::new();
        let edge_ptrs: Vec<String> = Vec::new();
        let mut avg_importance = 0.5f32;

        let node_query = crate::query::types::NodeListQuery {
            page: 1,
            page_size: 1000,
            node_type: None,
            keyword: None,
            min_importance: None,
        };
        if let Ok(nodes) = crate::l3::store::list_nodes_by_graph(&self.engine, id_hash, &node_query)
        {
            let count = nodes.total as f32;
            if count > 0.0 {
                let imp_sum: f32 = nodes.items.iter().map(|n| n.importance).sum();
                avg_importance = imp_sum / count;
            }
            for node in &nodes.items {
                if !node.content.is_empty() {
                    text.push_str(&node.content);
                    text.push('\n');
                }
                if summary.is_none() && !node.title.is_empty() {
                    summary = Some(node.title.clone());
                }
                keywords.extend(node.keywords.iter().cloned());
            }
        }

        keywords.sort();
        keywords.dedup();

        Ok(Some(KnowledgeDetail {
            id: crate::shared::common::format_hash(slot.id_hash),
            title: slot.name,
            domain: format!("{:?}", slot.source.kind()),
            knowledge_type: "Generic".to_string(),
            text: text.trim_end().to_string(),
            summary,
            keywords,
            edge_ptrs,
            archive_refs: vec![],
            source_ref,
            importance: avg_importance,
            confidence: 1.0,
            created_at: slot.created_at,
            updated_at: slot.updated_at,
        }))
    }

    /// Get L3 knowledge nodes by batch IDs (max 50)
    ///
    /// Returns node details with domain resolved from the parent HypergraphSlot.
    /// Missing IDs are silently skipped. `include_text` controls whether the
    /// full `text` field is returned.
    pub fn get_knowledge_nodes_by_ids(
        &self,
        ids: &[String],
        include_text: bool,
    ) -> Result<KnowledgeNodesResult> {
        const MAX_IDS: usize = 50;
        let requested = ids.len();
        let ids = if ids.len() > MAX_IDS {
            &ids[..MAX_IDS]
        } else {
            ids
        };

        let mut nodes: Vec<KnowledgeNodeDetail> = Vec::with_capacity(ids.len());

        for id_str in ids {
            let id_hash = crate::shared::common::parse_id_to_hash(id_str);
            if let Some(detail) = self.resolve_knowledge_node_detail(id_hash, include_text) {
                nodes.push(detail);
            }
        }

        Ok(KnowledgeNodesResult {
            total: nodes.len(),
            nodes,
            requested,
        })
    }

    /// Resolve a single HypergraphNode into a KnowledgeNodeDetail.
    pub(crate) fn resolve_knowledge_node_detail(
        &self,
        node_hash: u64,
        include_text: bool,
    ) -> Option<KnowledgeNodeDetail> {
        match self.engine.read_record(node_hash) {
            Ok(Some((rt, data))) if rt == REC_L3_GRAPH_NODE => {
                if let Ok(node) = crate::layers::hypergraph::HypergraphNode::deserialize(data) {
                    let domain = self.resolve_node_domain(node.graph_id);
                    return Some(KnowledgeNodeDetail {
                        id: crate::shared::common::format_hash(node.id_hash),
                        title: node.title,
                        text: if include_text {
                            Some(node.content)
                        } else {
                            None
                        },
                        keywords: node.keywords,
                        domain,
                        knowledge_type: node.node_type,
                        created_at: node.created_at,
                        importance: node.importance,
                    });
                }
            }
            _ => {}
        }
        None
    }

    /// Resolve domain name from a HypergraphSlot by graph_id
    pub(crate) fn resolve_node_domain(&self, graph_id: u64) -> String {
        match self.engine.read_record(graph_id) {
            Ok(Some((rt, data))) if rt == REC_L3_GRAPH_SLOT => {
                if let Ok(slot) = crate::layers::hypergraph::HypergraphSlot::deserialize(data) {
                    return slot.name;
                }
            }
            _ => {}
        }
        "unknown".to_string()
    }

    /// List knowledge (L3 hypergraphs) with pagination and filtering
    pub fn list_knowledge(&self, query: KnowledgeListQuery) -> Result<KnowledgeListResult> {
        use crate::query::list::list_knowledge as impl_fn;
        impl_fn(&self.engine, query)
    }
}
