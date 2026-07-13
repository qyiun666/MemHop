// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Search API operations.

use crate::query::types::{
    KnowledgeNodeQuery, KnowledgeNodesResult, SearchPreprocessResult, SearchQuery, SearchResult,
};
use crate::MemHop;
use crate::MemHopError;
use crate::Result;

impl MemHop {
    /// Search memory using topic-centric retrieval model.
    #[cfg(feature = "grpc-encoder")]
    pub fn search(&mut self, query: SearchQuery) -> Result<SearchResult> {
        use crate::query::search::search_context;

        // ====================================================================
        // Directed L2: skip vector search, return the specified context directly
        // ====================================================================
        if let Some(ref l2_id) = query.directed_l2_id {
            let topic = self.get_context(l2_id)?;
            let profile = if query.include_profile {
                self.get_profile()?
            } else {
                None
            };
            let contexts = match topic {
                Some(ctx) => vec![ctx],
                None => vec![],
            };
            return Ok(SearchResult {
                profile,
                contexts,
                associated_contexts: vec![],
                l3_ids: vec![],
                l1_previews: vec![],
            });
        }

        // Convert public SearchQuery to internal search query for the pipeline
        let dialogue = query.query.clone();

        let preprocess_result = if self.config.llm_preprocess.preprocess_max_tokens > 0 {
            self.preprocess_search_query(
                &dialogue,
                self.config.llm_preprocess.preprocess_temperature,
                self.config.llm_preprocess.preprocess_max_tokens,
            )?
        } else {
            SearchPreprocessResult {
                keywords: vec![],
                needs_l3_import: false,
                l3_entities: vec![],
            }
        };
        let effective_keywords = if preprocess_result.keywords.is_empty() {
            None
        } else {
            Some(preprocess_result.keywords)
        };
        let l3_import_hints =
            if preprocess_result.needs_l3_import && !preprocess_result.l3_entities.is_empty() {
                Some(preprocess_result.l3_entities)
            } else {
                None
            };

        let search_text = effective_keywords
            .as_ref()
            .map(|kws| kws.join(" "))
            .unwrap_or_else(|| dialogue.clone());

        let original_dialogue = dialogue.clone();
        let raw_keywords: Vec<String> = effective_keywords.unwrap_or_default();
        let internal_search_query = crate::query::types::InternalSearchQuery {
            dialogue: if !raw_keywords.is_empty() {
                search_text
            } else {
                original_dialogue
            },
            keywords: raw_keywords.clone(),
            l2_id: query.directed_l2_id.clone(),
            l3_id: query.directed_l3_id.clone(),
            auto_create: query.auto_create.unwrap_or(0) != 0,
        };

        let mut result = search_context(
            internal_search_query,
            &mut self.sparse_index,
            &self.l2_meta,
            self.config.vector_dim,
            &mut self.engine,
            &*self.encoder,
            self.config
                .search_weights
                .as_ref()
                .unwrap_or(&crate::config::SearchWeights::default()),
            self.ivf_index.as_ref(),
            &self.l1_reverse_index,
        )?;

        // ====================================================================
        // Auto-create: when auto_create is 1 and no contexts found, create new L2
        // ====================================================================
        if query.auto_create.unwrap_or(0) != 0 && result.contexts.is_empty() {
            tracing::info!(
                "[search] auto_create=1, no matches found, creating new L2 context for: {}",
                query.query
            );
            let create_keywords: Vec<String> = if raw_keywords.is_empty() {
                vec![query.query.clone()]
            } else {
                raw_keywords.clone()
            };
            match crate::query::search::create_new_l2_context(
                &mut self.engine,
                &mut self.sparse_index,
                &query.query,
                &create_keywords,
                self.config.vector_dim,
                &*self.encoder,
            ) {
                Ok(new_ctx) => {
                    let ctx_result =
                        crate::api::l2_ops::slot_to_context_result(&new_ctx, new_ctx.id, 1.0);
                    result.contexts.push(ctx_result);
                    // Rebuild L2 metadata so the new context is findable in subsequent searches
                    self.l2_meta =
                        crate::index::l2_meta::L2MetaIndex::build_from_engine(&self.engine);
                    tracing::info!(
                        "[search] auto_create: new L2 context created: {}",
                        crate::shared::common::format_hash(new_ctx.id)
                    );
                }
                Err(e) => {
                    tracing::warn!("[search] auto_create failed to create new L2: {}", e);
                }
            }
        }

        // Fire-and-forget L3 knowledge import: trigger based on search hints
        if let Some(ref hints) = l3_import_hints {
            if let Err(e) = self.import_l3_from_hints(hints) {
                tracing::warn!("L3 import from search hints failed: {}", e);
            }
        }

        // Apply activation boosts only (recency boost removed — ContextResult no longer has created_at)
        let active_ids = self.session_manager.get_active_topic_ids();
        let default_sw = crate::config::SearchWeights::default();
        let sw = self.config.search_weights.as_ref().unwrap_or(&default_sw);
        apply_activation_boost(&mut result.contexts, &active_ids, sw);
        apply_activation_boost(&mut result.associated_contexts, &active_ids, sw);

        // Re-sort after boost application
        result.contexts.sort_by(|a, b| {
            b.retrieval_score
                .partial_cmp(&a.retrieval_score)
                .unwrap_or(std::cmp::Ordering::Equal)
        });
        result.associated_contexts.sort_by(|a, b| {
            b.retrieval_score
                .partial_cmp(&a.retrieval_score)
                .unwrap_or(std::cmp::Ordering::Equal)
        });

        self.rebuild_ivf_index();
        Ok(result)
    }

    /// Unified L3 knowledge node retrieval.
    pub fn query_knowledge_nodes(&self, query: KnowledgeNodeQuery) -> Result<KnowledgeNodesResult> {
        match query {
            KnowledgeNodeQuery::ByIds { ids, include_text } => {
                const MAX_IDS: usize = 50;
                let requested = ids.len();
                let ids = if ids.len() > MAX_IDS {
                    &ids[..MAX_IDS]
                } else {
                    &ids
                };
                let mut nodes: Vec<crate::query::types::KnowledgeNodeDetail> =
                    Vec::with_capacity(ids.len());
                for id_str in ids {
                    let id_hash = crate::shared::common::parse_id_to_hash(id_str);
                    if let Some(detail) = self.resolve_knowledge_node_detail(id_hash, include_text)
                    {
                        nodes.push(detail);
                    }
                }
                Ok(KnowledgeNodesResult {
                    total: nodes.len(),
                    nodes,
                    requested,
                })
            }
            KnowledgeNodeQuery::ByKeyword {
                graph_id,
                keyword,
                limit,
            } => self.search_knowledge_nodes_by_keyword(&graph_id, &keyword, limit),
            KnowledgeNodeQuery::ByType {
                graph_id,
                node_type,
                limit,
            } => self.get_knowledge_nodes_by_type(&graph_id, &node_type, limit),
        }
    }

    fn search_knowledge_nodes_by_keyword(
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
        let nodes: Vec<crate::query::types::KnowledgeNodeDetail> = node_hashes
            .into_iter()
            .filter_map(|h| self.resolve_knowledge_node_detail(h, false))
            .collect();
        Ok(KnowledgeNodesResult {
            total: nodes.len(),
            nodes,
            requested: limit,
        })
    }

    fn get_knowledge_nodes_by_type(
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
        let nodes: Vec<crate::query::types::KnowledgeNodeDetail> = node_hashes
            .into_iter()
            .filter_map(|h| self.resolve_knowledge_node_detail(h, false))
            .collect();
        Ok(KnowledgeNodesResult {
            total: nodes.len(),
            nodes,
            requested: limit,
        })
    }

    #[cfg(feature = "llm")]
    fn preprocess_search_query(
        &self,
        dialogue: &str,
        temperature: f32,
        max_tokens: u32,
    ) -> Result<SearchPreprocessResult> {
        use crate::dream::llm_preprocess;
        use crate::dream::openai_compatible::OpenAICompatibleLlmProvider;

        let provider = OpenAICompatibleLlmProvider::new(self.config.llm.clone());
        llm_preprocess::preprocess_search_query(&provider, dialogue, temperature, max_tokens)
    }

    #[cfg(not(feature = "llm"))]
    fn preprocess_search_query(
        &self,
        _dialogue: &str,
        _temperature: f32,
        _max_tokens: u32,
    ) -> Result<SearchPreprocessResult> {
        Ok(SearchPreprocessResult {
            keywords: vec![_dialogue.to_string()],
            needs_l3_import: false,
            l3_entities: vec![],
        })
    }
}

impl MemHop {
    /// Import L3 knowledge nodes from entity hints (fire-and-forget).
    ///
    /// Uses a deterministic default graph (`hash_id("default_l3_graph")`)
    /// that is automatically created on first use.
    fn import_l3_from_hints(&mut self, hints: &[crate::query::types::L3EntityHint]) -> Result<()> {
        let graph_id = crate::util::hash_id("default_l3_graph");
        let _ = crate::query::pipeline::l3_import::import_entities_from_hints(
            &mut self.engine,
            &mut self.l3_index_map,
            &mut self.degree_tracker,
            graph_id,
            hints,
        )?;
        Ok(())
    }
}

/// Apply activation boost only (recency boost removed as ContextResult no longer has created_at)
#[cfg(feature = "grpc-encoder")]
fn apply_activation_boost(
    contexts: &mut [crate::query::types::ContextResult],
    active_topic_ids: &[u64],
    weights: &crate::config::SearchWeights,
) {
    for ctx in contexts.iter_mut() {
        let ctx_hash = crate::shared::common::parse_id_to_hash(&ctx.id);
        let activation = if active_topic_ids.contains(&ctx_hash) {
            weights.activation_boost
        } else {
            1.0
        };
        ctx.retrieval_score *= activation;
        ctx.retrieval_score = ctx.retrieval_score.clamp(0.0, 1.0);
    }
}
