// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Search API operations.

use crate::query::types::KnowledgeNodesResult;
#[cfg(feature = "grpc-encoder")]
use crate::query::types::{L3EntityHint, SearchQuery, SearchResult};
use crate::MemHop;
use crate::MemHopError;
use crate::Result;

impl MemHop {
    /// Search memory using topic-centric retrieval model.
    ///
    /// When `enable_llm_preprocess` is true, the query dialogue is first sent
    /// through LLM preprocessing to extract precise keywords and judge L3
    /// import need. Results are then ranked by recency > activation > base score.
    ///
    /// # Arguments
    /// * `query` - Search query with dialogue, filters, and optional LLM preprocessing
    ///
    /// # Returns
    /// `SearchResult` containing profile, contexts, associated contexts, L3 IDs, etc.
    #[cfg(feature = "grpc-encoder")]
    pub fn search_context(&mut self, query: SearchQuery) -> Result<SearchResult> {
        use crate::query::search::search_context;

        // Log a warning if encoder is not configured (vector search degraded to BM25-only)
        if self.encoder.is_none() {
            tracing::warn!(
                "Encoder not configured — search falling back to BM25-only (vector search unavailable)."
            );
        }

        // Determine effective keywords and L3 import hints
        let (effective_keywords, l3_import_hints): (
            Option<Vec<String>>,
            Option<Vec<L3EntityHint>>,
        ) = if query.llm_keywords.is_some() {
            // Caller already provided preprocessed keywords
            (query.llm_keywords.clone(), None)
        } else if query.enable_llm_preprocess {
            // Run LLM preprocessing inline
            self.preprocess_search_query(&query.dialogue)
        } else {
            (None, None)
        };

        // Build the search text: use LLM keywords if available, else raw dialogue.
        // Keywords optimize BM25 retrieval; original dialogue preserved for vector encoding.
        let search_text = effective_keywords
            .as_ref()
            .map(|kws| kws.join(" "))
            .unwrap_or_else(|| query.dialogue.clone());

        // Create a modified query that uses the optimized search text.
        // The original dialogue is kept separately for vector encoding in the search pipeline.
        let original_dialogue = query.dialogue.clone();
        let mut search_query = query.clone();
        search_query.dialogue = if effective_keywords.is_some() {
            // Dual-channel: BM25 uses keywords, vector uses original dialogue.
            // The search pipeline encode()s dialogue for vector; we replace it
            // for BM25 but the pipeline internally re-tokenizes anyway.
            search_text
        } else {
            original_dialogue
        };

        let mut result = search_context(
            search_query,
            &mut self.sparse_index,
            &self.l2_meta,
            self.config.vector_dim,
            &mut self.engine,
            self.encoder.as_deref(),
            self.config
                .search_weights
                .as_ref()
                .unwrap_or(&crate::config::SearchWeights::default()),
            self.ivf_index.as_ref(),
            &self.l1_reverse_index,
        )?;

        // Apply recency + activation boosts to contexts
        let active_ids = self.session_manager.get_active_topic_ids();
        let default_sw = crate::config::SearchWeights::default();
        let sw = self.config.search_weights.as_ref().unwrap_or(&default_sw);
        apply_temporal_boosts(&mut result.contexts, &active_ids, sw);
        apply_temporal_boosts(&mut result.associated_contexts, &active_ids, sw);

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

        // Attach LLM metadata to result
        result.llm_keywords_used = effective_keywords;
        result.l3_import_hints = l3_import_hints;

        // After search (which may have auto-created a new context), rebuild IVF
        self.rebuild_ivf_index();

        Ok(result)
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

    /// Run LLM search preprocessing inline.
    ///
    /// Creates an OpenAI-compatible LLM provider from the current config and
    /// calls the preprocess pipeline. Falls back to tokenizer on failure.
    #[cfg(feature = "llm")]
    fn preprocess_search_query(
        &self,
        dialogue: &str,
    ) -> (Option<Vec<String>>, Option<Vec<L3EntityHint>>) {
        use crate::dream::llm_preprocess;
        use crate::dream::openai_compatible::OpenAICompatibleLlmProvider;

        let provider = OpenAICompatibleLlmProvider::new(self.config.llm.clone());
        let result = llm_preprocess::preprocess_search_query(Some(&provider), dialogue);

        let l3_hints = if result.needs_l3_import && !result.l3_entities.is_empty() {
            Some(result.l3_entities)
        } else {
            None
        };

        (Some(result.keywords), l3_hints)
    }

    /// Fallback when LLM feature is disabled.
    #[cfg(not(feature = "llm"))]
    fn preprocess_search_query(
        &self,
        _dialogue: &str,
    ) -> (Option<Vec<String>>, Option<Vec<L3EntityHint>>) {
        (None, None)
    }
}

/// Apply recency and activation boosts to context scores.
///
/// - **Recency boost**: `score += recency_weight * exp(-age_days / 7)`
///   Recent contexts (within days) get higher boosts.
/// - **Activation boost**: `score *= activation_boost` for topics in working memory.
///
/// Scores are clamped to [0.0, 1.0] after boost application.
#[cfg(feature = "grpc-encoder")]
#[allow(clippy::ptr_arg)]
fn apply_temporal_boosts(
    contexts: &mut Vec<crate::query::types::ContextResult>,
    active_topic_ids: &[u64],
    weights: &crate::config::SearchWeights,
) {
    let now_ms = crate::util::get_current_timestamp();
    let ms_per_day: f64 = 86_400_000.0;

    for ctx in contexts.iter_mut() {
        let age_ms = (now_ms - ctx.created_at).max(0) as f64;
        let age_days = age_ms / ms_per_day;

        // Recency boost: exponential decay, capped contribution
        let recency = (weights.recency_weight as f64 * (-age_days / 7.0).exp()) as f32;

        // Activation boost: check if this topic is in active session
        let ctx_hash = crate::shared::common::parse_id_to_hash(&ctx.id);
        let activation = if active_topic_ids.contains(&ctx_hash) {
            weights.activation_boost
        } else {
            1.0
        };

        // Apply combined boost
        ctx.retrieval_score = (ctx.retrieval_score + recency) * activation;
        ctx.retrieval_score = ctx.retrieval_score.clamp(0.0, 1.0);
    }
}
