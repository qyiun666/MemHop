// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! CRUD/list API operations.

use crate::query::types::{
    Archive, ArchiveListResult, ArchivePageQuery, CrystalListQuery, CrystalListResult,
    EngramListQuery, EngramListResult, EngramResult, KnowledgeDetail, KnowledgeListQuery,
    KnowledgeListResult, KnowledgeNodeDetail, KnowledgeNodesResult, ProfileResult, TopicDetail,
    TopicListQuery, TopicListResult,
};
use crate::MemHop;
use crate::MemHopError;
use crate::Result;
impl MemHop {
    /// Get profile
    pub fn get_profile(&self) -> Result<Option<ProfileResult>> {
        use crate::query::list::get_profile as impl_fn;
        impl_fn(&self.mmap, &self.btree)
    }

    /// Get single engram by ID
    pub fn get_engram(&self, id: &str) -> Result<Option<EngramResult>> {
        use crate::query::list::get_engram as impl_fn;
        impl_fn(&self.mmap, &self.btree, id)
    }

    /// List engrams with pagination and filtering
    pub fn list_engrams(&self, query: EngramListQuery) -> Result<EngramListResult> {
        use crate::query::list::list_engrams as impl_fn;
        impl_fn(&self.mmap, &self.header, &self.btree, query)
    }

    /// Get single topic by ID
    pub fn get_topic(&self, id: &str) -> Result<Option<TopicDetail>> {
        use crate::query::list::get_topic as impl_fn;
        impl_fn(&self.mmap, &self.btree, id)
    }

    /// List topics with pagination and filtering
    pub fn list_topics(&self, query: TopicListQuery) -> Result<TopicListResult> {
        use crate::query::list::list_topics as impl_fn;
        impl_fn(&self.mmap, &self.header, &self.btree, query)
    }

    /// List archives by topic ID
    pub fn list_archives_by_topic(
        &self,
        topic_id: &str,
        query: ArchivePageQuery,
    ) -> Result<ArchiveListResult> {
        use crate::query::list::list_archives_by_topic as impl_fn;
        impl_fn(&self.mmap, &self.header, &self.btree, topic_id, query)
    }

    /// List archives by node IDs
    pub fn list_archives_by_nodes(
        &self,
        node_ids: &[String],
        query: ArchivePageQuery,
    ) -> Result<ArchiveListResult> {
        use crate::query::list::list_archives_by_nodes as impl_fn;
        impl_fn(&self.mmap, &self.header, &self.btree, node_ids, query)
    }

    /// List all archives
    pub fn list_all_archives(&self, query: ArchivePageQuery) -> Result<ArchiveListResult> {
        use crate::query::list::list_all_archives as impl_fn;
        impl_fn(&self.mmap, &self.header, &self.btree, query)
    }

    /// Get single archive by ID
    pub fn get_archive(&self, id: &str) -> Result<Option<Archive>> {
        use crate::query::list::get_archive as impl_fn;
        impl_fn(&self.mmap, &self.btree, id)
    }

    /// List crystals with pagination and filtering
    pub fn list_crystals(&self, query: CrystalListQuery) -> Result<CrystalListResult> {
        use crate::query::list::list_crystals as impl_fn;
        impl_fn(&self.mmap, &self.header, &self.btree, query)
    }

    /// Activate a crystal (L5 action chain) by ID.
    ///
    /// Validates confidence >= 0.5 and at least one linked ActionStep, then
    /// flips the chain status to Active.
    pub fn activate_crystal(&mut self, id: &str) -> Result<()> {
        use crate::dream::crystallize_stage::activate_crystal as impl_fn;
        let chain_id = crate::shared::common::parse_id_to_hash(id);
        impl_fn(&mut self.mmap, &self.header, &self.btree, chain_id)
    }

    /// Get single knowledge (L3 hypergraph) by ID
    ///
    /// Uses l3::store engine to read the hypergraph and aggregate node content
    /// into the KnowledgeDetail structure.
    pub fn get_knowledge(&self, id: &str) -> Result<Option<KnowledgeDetail>> {
        let data: &[u8] = &self.mmap[..];
        let id_hash = crate::shared::common::parse_id_to_hash(id);

        // Read HypergraphSlot from BTree
        let slot = match self.btree.search(id_hash) {
            Some(page_ref) => {
                if let Some(slot_data) = crate::shared::slot_io::get_slot_data(data, page_ref) {
                    match crate::layers::hypergraph::HypergraphSlot::deserialize_slot(slot_data) {
                        Ok(s) => s,
                        Err(_) => return Ok(None),
                    }
                } else {
                    return Err(MemHopError::PageNotFound(
                        crate::shared::slot_io::decode_page_id(page_ref),
                    ));
                }
            }
            None => return Ok(None),
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
        if let Ok(nodes) =
            crate::l3::store::list_nodes_by_graph(&self.mmap, &self.btree, id_hash, &node_query)
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

        let data: &[u8] = &self.mmap[..];
        let mut nodes: Vec<KnowledgeNodeDetail> = Vec::with_capacity(ids.len());

        for id_str in ids {
            let id_hash = crate::shared::common::parse_id_to_hash(id_str);
            if let Some(detail) = self.resolve_knowledge_node_detail(data, id_hash, include_text) {
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
        data: &[u8],
        node_hash: u64,
        include_text: bool,
    ) -> Option<KnowledgeNodeDetail> {
        if let Some(page_ref) = self.btree.search(node_hash) {
            if let Some(slot_data) = crate::shared::slot_io::get_slot_data(data, page_ref) {
                if let Ok(node) = crate::layers::hypergraph::HypergraphNode::deserialize(slot_data)
                {
                    let domain = self.resolve_node_domain(data, node.graph_id);
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
        }
        None
    }

    /// Resolve domain name from a HypergraphSlot by graph_id
    pub(crate) fn resolve_node_domain(&self, data: &[u8], graph_id: u64) -> String {
        if let Some(page_ref) = self.btree.search(graph_id) {
            if let Some(slot_data) = crate::shared::slot_io::get_slot_data(data, page_ref) {
                if let Ok(slot) = crate::layers::hypergraph::HypergraphSlot::deserialize(slot_data)
                {
                    return slot.name;
                }
            }
        }
        "unknown".to_string()
    }

    /// List knowledge (L3 hypergraphs) with pagination and filtering
    pub fn list_knowledge(&self, query: KnowledgeListQuery) -> Result<KnowledgeListResult> {
        use crate::query::list::list_knowledge as impl_fn;
        impl_fn(&self.mmap, &self.header, &self.btree, query)
    }

    /// Delete an L2 topic and its associated L1 nodes and L4 archives.
    pub fn delete_topic(&mut self, topic_id: u64) -> Result<()> {
        let page_ref = match self.btree.search(topic_id) {
            Some(pr) => pr,
            None => return Ok(()),
        };

        let ctx = {
            let data: &[u8] = &self.mmap[..];
            let slot_data = crate::shared::slot_io::get_slot_data(data, page_ref).ok_or(
                MemHopError::PageNotFound(crate::shared::slot_io::decode_page_id(page_ref)),
            )?;
            crate::layers::context::ContextSlot::deserialize_slot(slot_data)?
        };

        // Collect associated L1 ContextNode records using L1ReverseIndex (O(1) lookup).
        let l1_nodes: Vec<(u64, u64)> = {
            let data: &[u8] = &self.mmap[..];
            self.l1_reverse_index
                .find_associated(&std::iter::once(topic_id).collect())
                .into_iter()
                .filter(|(_, page_ref)| {
                    // Verify the page is still a ContextNode (defensive check)
                    let page_id = crate::shared::slot_io::decode_page_id(*page_ref);
                    if page_id >= self.header.page_count {
                        return false;
                    }
                    if let Ok(page_hdr) = crate::file::page::read_page_header(data, page_id) {
                        page_hdr.page_type == crate::util::PageType::ContextNode as u16
                    } else {
                        false
                    }
                })
                .collect()
        };

        // Free L1 nodes and update the reverse index.
        for (node_hash, page_ref) in l1_nodes {
            self.btree.delete(node_hash);
            let page_id = crate::shared::slot_io::decode_page_id(page_ref);
            crate::file::free_list::free_page(&mut self.mmap, &mut self.header, page_id)?;
            self.l1_reverse_index.remove_node(node_hash);
        }

        // Free associated L4 archives.
        for &arc_hash in &ctx.archive_refs {
            if let Some(page_ref) = self.btree.delete(arc_hash) {
                let page_id = crate::shared::slot_io::decode_page_id(page_ref);
                crate::file::free_list::free_page(&mut self.mmap, &mut self.header, page_id)?;
            }
        }

        // Free centroid vector page if present.
        if ctx.centroid_page_ref != 0 {
            let page_id = crate::shared::slot_io::decode_page_id(ctx.centroid_page_ref);
            crate::file::free_list::free_page(&mut self.mmap, &mut self.header, page_id)?;
        }

        // Remove the ContextSlot itself.
        self.btree.delete(topic_id);
        let page_id = crate::shared::slot_io::decode_page_id(page_ref);
        crate::file::free_list::free_page(&mut self.mmap, &mut self.header, page_id)?;

        self.sparse_index.remove_document(topic_id);
        self.l1_reverse_index.remove_context(topic_id);

        Ok(())
    }

    /// Delete an L5 action chain and all associated action steps.
    pub fn delete_action_chain(&mut self, chain_id: u64) -> Result<()> {
        let chain_page_ref = match self.btree.search(chain_id) {
            Some(pr) => pr,
            None => return Ok(()),
        };

        let chain_page_id = crate::shared::slot_io::decode_page_id(chain_page_ref);
        crate::file::free_list::free_page(&mut self.mmap, &mut self.header, chain_page_id)?;
        self.btree.delete(chain_id);

        // Collect associated ActionStep records.
        let mut steps: Vec<(u64, u64)> = Vec::new();
        {
            let data: &[u8] = &self.mmap[..];
            for (&id_hash, &page_ref) in self.btree.iter() {
                if crate::l3::store::page_type_of(data, page_ref)
                    != Some(crate::util::PageType::ActionStep as u16)
                {
                    continue;
                }
                if let Some(slot_data) = crate::shared::slot_io::get_slot_data(data, page_ref) {
                    if let Ok(step) =
                        crate::layers::action_chain::ActionStep::deserialize(slot_data)
                    {
                        if step.chain_id == chain_id {
                            steps.push((id_hash, page_ref));
                        }
                    }
                }
            }
        }

        // Free each action step.
        for (step_hash, page_ref) in steps {
            self.btree.delete(step_hash);
            let page_id = crate::shared::slot_io::decode_page_id(page_ref);
            crate::file::free_list::free_page(&mut self.mmap, &mut self.header, page_id)?;
        }

        Ok(())
    }
}
