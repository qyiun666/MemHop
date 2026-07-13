// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! CRUD/list API operations.

use crate::layers::context_node::SceneNode;
use crate::layers::hyperedge::SceneEdge;
use crate::query::types::{
    CrystalListQuery, CrystalListResult, KnowledgeDetail, KnowledgeListQuery, KnowledgeListResult,
    KnowledgeNodeDetail, UpdateL3Fields,
};
use crate::query::types::{L1Edge, L1Graph, L1Node};
use crate::shared::common::format_hash;
use crate::storage::record::{
    REC_L1_HYPEREDGE, REC_L1_SCENE_NODE, REC_L3_GRAPH_NODE, REC_L3_GRAPH_SLOT,
};
use crate::{MemHop, Result};

impl MemHop {
    /// List crystals with pagination and filtering
    pub fn list_crystals(&self, query: CrystalListQuery) -> Result<CrystalListResult> {
        use crate::query::list::list_crystals as impl_fn;
        impl_fn(&self.engine, query)
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

    /// 获取 L1 层完整图结构（节点 + 边），供 Agent 侧构建可视化图
    pub fn get_l1_graph(&self, scene_id: Option<&str>) -> Result<L1Graph> {
        let mut nodes = Vec::new();
        let mut edges = Vec::new();

        let scene_filter = scene_id.and_then(|s| {
            if s.len() == 16 {
                u64::from_str_radix(s, 16).ok()
            } else {
                None
            }
        });

        for (&id_hash, _offset) in self.engine.iter_index() {
            let Ok(Some((record_type, data))) = self.engine.read_record(id_hash) else {
                continue;
            };

            match record_type {
                REC_L1_SCENE_NODE => {
                    if let Ok(scene_node) = bincode::deserialize::<SceneNode>(data) {
                        if let Some(filter) = scene_filter {
                            if scene_node.scene_id != filter {
                                continue;
                            }
                        }
                        // Resolve L2 context slot for L1Node supplementary fields
                        let (l2_summary, l2_kw, valence, arousal) = if let Some(&first_topic) =
                            scene_node.topic_ids.first()
                        {
                            if let Ok(Some((_rt, data))) = self.engine.read_record(first_topic) {
                                if let Ok(ctx) = bincode::deserialize::<
                                    crate::layers::context::ContextSlot,
                                >(data)
                                {
                                    (
                                        ctx.fused_summary,
                                        {
                                            let mut kw = ctx.user_keywords.clone();
                                            kw.extend(ctx.agent_keywords);
                                            kw.sort();
                                            kw.dedup();
                                            kw
                                        },
                                        scene_node.valence,
                                        scene_node.arousal,
                                    )
                                } else {
                                    (None, vec![], 0.0, 0.0)
                                }
                            } else {
                                (None, vec![], 0.0, 0.0)
                            }
                        } else {
                            (None, vec![], 0.0, 0.0)
                        };
                        let dominant_emotion = if valence > 0.3 {
                            Some("positive".to_string())
                        } else if valence < -0.3 {
                            Some("negative".to_string())
                        } else if arousal > 0.6 {
                            Some("exciting".to_string())
                        } else {
                            Some("neutral".to_string())
                        };
                        nodes.push(L1Node {
                            id: format_hash(scene_node.id_hash),
                            scene_id: format_hash(scene_node.scene_id),
                            topic_ids: scene_node
                                .topic_ids
                                .iter()
                                .map(|&h| format_hash(h))
                                .collect(),
                            depth: scene_node.depth,
                            importance: scene_node.importance,
                            valence: scene_node.valence,
                            arousal: scene_node.arousal,
                            summary: l2_summary,
                            dominant_emotion,
                            keywords: l2_kw,
                            recall_score: scene_node.importance,
                            created_at: scene_node.created_at,
                            updated_at: scene_node.updated_at,
                            edge_ids: scene_node
                                .edge_ids
                                .iter()
                                .map(|&h| format_hash(h))
                                .collect(),
                        });
                    }
                }
                REC_L1_HYPEREDGE => {
                    if let Ok(scene_edge) = bincode::deserialize::<SceneEdge>(data) {
                        edges.push(L1Edge {
                            id: format_hash(scene_edge.id_hash),
                            kind: format!("{:?}", scene_edge.kind),
                            node_ids: scene_edge
                                .node_ids
                                .iter()
                                .map(|&h| format_hash(h))
                                .collect(),
                            weight: scene_edge.weight,
                            created_at: scene_edge.created_at,
                        });
                    }
                }
                _ => {}
            }
        }

        // 如果传入了 scene_id，过滤边使其只包含可见节点的连接
        if scene_filter.is_some() {
            let node_set: std::collections::HashSet<u64> = nodes
                .iter()
                .filter_map(|n| u64::from_str_radix(&n.id, 16).ok())
                .collect();
            edges.retain(|e| {
                e.node_ids.iter().any(|nid| {
                    u64::from_str_radix(nid, 16)
                        .ok()
                        .is_some_and(|h| node_set.contains(&h))
                })
            });
        }

        Ok(L1Graph { nodes, edges })
    }

    /// Update a knowledge graph (L3) by ID.
    pub fn update_knowledge(&mut self, id: &str, fields: UpdateL3Fields) -> Result<()> {
        self.update_l3(id, fields)?;
        Ok(())
    }

    /// Delete a knowledge graph (L3) by ID.
    pub fn delete_knowledge(&mut self, id: &str) -> Result<()> {
        self.delete_l3(id)
    }

    /// Query knowledge nodes by keyword across all graphs.
    pub fn query_knowledge_nodes_by_keyword(&self, keyword: &str) -> Result<Vec<KnowledgeDetail>> {
        let mut results = Vec::new();
        for (&graph_hash, index) in &self.l3_index_map {
            let node_hashes = index.search_by_keyword(keyword, 20);
            for node_hash in node_hashes {
                if let Some(detail) = self.resolve_knowledge_node_detail(node_hash, false) {
                    // Convert to KnowledgeDetail
                    let domain = self.resolve_node_domain(graph_hash);
                    results.push(KnowledgeDetail {
                        id: detail.id,
                        title: detail.title,
                        domain,
                        knowledge_type: detail.knowledge_type,
                        text: detail.text.unwrap_or_default(),
                        summary: None,
                        keywords: detail.keywords,
                        edge_ptrs: vec![],
                        archive_refs: vec![],
                        source_ref: None,
                        importance: detail.importance,
                        confidence: 1.0,
                        created_at: detail.created_at,
                        updated_at: detail.created_at,
                    });
                }
            }
        }
        results.sort_by(|a, b| {
            b.importance
                .partial_cmp(&a.importance)
                .unwrap_or(std::cmp::Ordering::Equal)
        });
        Ok(results)
    }

    /// Query knowledge graph by keyword, with minimum score threshold.
    pub fn query_knowledge_graph(
        &self,
        keyword: &str,
        min_score: f64,
    ) -> Result<Vec<KnowledgeDetail>> {
        let mut results = self.query_knowledge_nodes_by_keyword(keyword)?;
        results.retain(|k| k.importance as f64 >= min_score);
        Ok(results)
    }
}
