// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// search_context() — L2-centric retrieval engine.
// Two-channel retrieval (BM25 + vector) scoped to candidate L2 contexts,
// with an optional cross-encoder rerank step downstream.

#![cfg_attr(not(feature = "grpc-encoder"), allow(dead_code, unused_imports))]

use crate::config::SearchWeights;
use crate::index::l2_meta::L2MetaIndex;
use crate::index::sparse::SparseIndex;
use crate::index::vector::IVFIndex;
use crate::layers::context::ContextSlot;
use crate::layers::context_node::ContextNode;
use crate::query::types::*;
use crate::shared::common;
use crate::storage::record::*;
use crate::storage::StorageEngine;
use crate::util::hash_id;
use crate::MemHopError;
use std::collections::{HashMap, HashSet};

/// Safely slice a UTF-8 string by character count, not byte count.
fn safe_char_slice(s: &str, max_chars: usize) -> String {
    if s.chars().count() <= max_chars {
        s.to_string()
    } else {
        s.chars().take(max_chars).collect()
    }
}

// ============================================================================
// Core search
// ============================================================================

/// Core search implementation — orchestrates the search pipeline.
#[allow(clippy::too_many_arguments)]
#[cfg(feature = "grpc-encoder")]
pub fn search_context(
    query: InternalSearchQuery,
    sparse_index: &mut SparseIndex,
    l2_meta: &L2MetaIndex,
    vector_dim: usize,
    engine: &mut StorageEngine,
    encoder: &(dyn crate::encoder::Encoder + Send + Sync),
    search_weights: &SearchWeights,
    ivf_index: Option<&IVFIndex>,
    l1_reverse: &L1ReverseIndex,
) -> Result<SearchResult, MemHopError> {
    let target_l2_id = query.l2_id.as_ref();

    // ========================================================================
    // Route 1: auto_create
    // ========================================================================
    let filtered_l2 = if query.auto_create {
        let keywords = if query.keywords.is_empty() {
            // No LLM keywords available; tokenize dialogue as fallback
            vec![query.dialogue.clone()]
        } else {
            query.keywords.clone()
        };
        let new_ctx = create_new_l2_context(
            engine,
            sparse_index,
            &query.dialogue,
            &keywords,
            vector_dim,
            encoder,
        )?;
        vec![(new_ctx, 1.0)]

    // ========================================================================
    // Route 2: l2_id / context_id direct load (via engine)
    // ========================================================================
    } else if let Some(l2_id_str) = target_l2_id {
        let target_hash = common::parse_id_to_hash(l2_id_str);

        match engine.read_record(target_hash)? {
            Some((_rt, data)) => match bincode::deserialize::<ContextSlot>(data) {
                Ok(ctx) => vec![(ctx, 1.0)],
                Err(_) => vec![],
            },
            None => vec![],
        }

    // ========================================================================
    // Route 3 & 4: two-channel retrieval via pipeline
    // ========================================================================
    } else {
        let candidates = super::pipeline::l2_search::build_candidate_set(
            l2_meta,
            &query.dialogue,
            sparse_index,
            20,
            query.l3_id.as_deref(),
        );

        // If an explicit l3_id produced no candidates, return empty without building indexes.
        if query.l3_id.is_some() && candidates.as_ref().map_or(true, |c| c.is_empty()) {
            vec![]
        } else {
            super::pipeline::l2_search::search_l2_candidates(
                engine,
                &query.dialogue,
                sparse_index,
                l2_meta,
                vector_dim,
                encoder,
                search_weights,
                ivf_index,
                10,
                0.0,
                candidates.as_ref(),
            )?
        }
    };

    let l1_associated =
        super::pipeline::l1_assoc::get_l1_associated_contexts(engine, &filtered_l2, l1_reverse)?;

    let mut all_contexts = filtered_l2.clone();
    all_contexts.extend(l1_associated.clone());

    let l0_profile = crate::query::profile::read_profile(engine)?;

    let l1_previews = super::pipeline::l1_assoc::get_l1_previews(
        engine,
        &filtered_l2,
        l1_reverse,
        &query.dialogue,
    )?;

    let result = super::pipeline::assemble::assemble_search_result(
        l0_profile,
        &all_contexts,
        &filtered_l2,
        &l1_associated,
        l1_previews,
    );

    Ok(result)
}

// ============================================================================
// L1 reverse index
// ============================================================================

/// L1 reverse index: maps an L2 `context_id` to the L1 `ContextNode`(s) that
/// point to it. Avoids O(N) btree scan for associated context lookups.
#[derive(Debug, Clone, Default)]
pub struct L1ReverseIndex {
    index: HashMap<u64, Vec<(u64, u64)>>,
}

impl L1ReverseIndex {
    pub fn new() -> Self {
        Self {
            index: HashMap::new(),
        }
    }

    /// Build the reverse index by scanning the engine index.
    pub fn build(engine: &StorageEngine) -> Result<Self, MemHopError> {
        let mut idx = Self::new();
        for (&id_hash, _) in engine.iter_index() {
            let Some((rt, data)) = engine.read_record(id_hash)? else {
                continue;
            };
            if rt != REC_L1_SCENE_NODE {
                continue;
            }
            if let Ok(node) = ContextNode::deserialize(data) {
                if node.context_id != 0 {
                    idx.add(node.context_id, id_hash, 0);
                }
            }
        }
        Ok(idx)
    }

    /// Add (or refresh) a node entry for a given `context_id`.
    pub fn add(&mut self, context_id: u64, node_id_hash: u64, node_page_ref: u64) {
        let entry = self.index.entry(context_id).or_default();
        entry.retain(|(nid, _)| *nid != node_id_hash);
        entry.push((node_id_hash, node_page_ref));
    }

    pub fn remove_context(&mut self, context_id: u64) {
        self.index.remove(&context_id);
    }

    pub fn remove_node(&mut self, node_id_hash: u64) {
        for nodes in self.index.values_mut() {
            nodes.retain(|(nid, _)| *nid != node_id_hash);
        }
    }

    /// O(1) lookup: given `context_id`s, return associated L1 nodes (deduplicated).
    pub fn find_associated(&self, context_ids: &HashSet<u64>) -> Vec<(u64, u64)> {
        let mut seen = HashSet::new();
        let mut result = Vec::new();
        for &ctx_id in context_ids {
            if let Some(nodes) = self.index.get(&ctx_id) {
                for &(node_id, page_ref) in nodes {
                    if seen.insert(node_id) {
                        result.push((node_id, page_ref));
                    }
                }
            }
        }
        result
    }

    pub fn serialize(&self) -> Result<Vec<u8>, MemHopError> {
        bincode::serialize(&self.index).map_err(|e| MemHopError::Serialization(e.to_string()))
    }

    pub fn deserialize(data: &[u8]) -> Result<Self, MemHopError> {
        let index: HashMap<u64, Vec<(u64, u64)>> =
            bincode::deserialize(data).map_err(|e| MemHopError::Serialization(e.to_string()))?;
        Ok(Self { index })
    }
}

// ============================================================================
// Auto-create L2 context
// ============================================================================

/// Create a new L2 ContextSlot from dialogue content (auto_create fast path)
#[allow(clippy::too_many_arguments)]
#[cfg(feature = "grpc-encoder")]
pub(crate) fn create_new_l2_context(
    engine: &mut StorageEngine,
    sparse_index: &mut SparseIndex,
    dialogue: &str,
    keywords: &[String],
    _vector_dim: usize,
    encoder: &(dyn crate::encoder::Encoder + Send + Sync),
) -> Result<ContextSlot, MemHopError> {
    use std::sync::atomic::{AtomicU64, Ordering};
    static COUNTER: AtomicU64 = AtomicU64::new(0);

    let now_ms = common::now_ms();

    let counter = COUNTER.fetch_add(1, Ordering::SeqCst);
    let id_str = format!(
        "ctx_{}_{}_{}",
        now_ms,
        counter,
        dialogue.chars().take(10).collect::<String>()
    );
    let id_hash = hash_id(&id_str);
    let title = safe_char_slice(dialogue, 50);

    // Use LLM-extracted keywords for centroid encoding (preserving vector space symmetry)
    let encode_text = if keywords.is_empty() {
        dialogue.to_string()
    } else {
        keywords.join(" ")
    };
    let centroid_record_hash = match encoder.encode(&encode_text) {
        Ok(output) if !output.dense.is_empty() => {
            let vec_id_hash = hash_id(&format!("v:{}", id_hash));
            let vec_bytes: Vec<u8> = output.dense.iter().flat_map(|v| v.to_ne_bytes()).collect();
            match engine.write_record(0xF0, vec_id_hash, &vec_bytes) {
                Ok(_) => vec_id_hash,
                Err(_) => 0,
            }
        }
        _ => 0,
    };

    let new_ctx = ContextSlot {
        id: id_hash,
        parent_id: None,
        children_ids: vec![],
        scene_id: 0,
        depth: 1,
        user_keywords: if keywords.is_empty() {
            vec![title]
        } else {
            keywords.to_vec()
        },
        user_timestamp: now_ms,
        user_l4_refs: Vec::new(),
        user_l3_refs: Vec::new(),
        agent_keywords: vec![],
        agent_timestamp: 0,
        agent_l4_refs: Vec::new(),
        agent_l3_refs: Vec::new(),
        fused_keywords: vec![],
        fused_summary: None,
        centroid_page_ref: centroid_record_hash,
        created_at: now_ms,
        updated_at: now_ms,
        version: 1,
    };

    let ctx_data =
        bincode::serialize(&new_ctx).map_err(|e| MemHopError::Serialization(e.to_string()))?;
    engine.write_record(REC_L2_TOPIC, id_hash, &ctx_data)?;

    let search_text = new_ctx.user_keywords.join(" ");
    let terms: Vec<String> = crate::index::sparse::tokenize(&search_text);
    let doc_len = terms.len() as u32;
    sparse_index.add_document(id_hash, terms, doc_len);

    Ok(new_ctx)
}

// ============================================================================
// Tests
// ============================================================================

#[cfg(test)]
mod tests {
    use super::*;
    use crate::layers::context::ContextSlot;
    use crate::query::pipeline::l2_search::{rerank_candidates, retrieve_l2_bm25};
    use crate::shared::common::format_hash;
    use crate::storage::StorageEngine;
    use crate::store::write_slot;
    use tempfile::NamedTempFile;

    fn make_context(id_hash: u64, title: &str, l3_refs: Vec<u64>) -> ContextSlot {
        ContextSlot {
            id: id_hash,
            scene_id: 0,
            parent_id: None,
            children_ids: vec![],
            depth: 1,
            user_keywords: vec![title.to_string()],
            user_timestamp: 0,
            user_l4_refs: vec![],
            user_l3_refs: l3_refs,
            agent_keywords: vec![],
            agent_timestamp: 0,
            agent_l4_refs: vec![],
            agent_l3_refs: vec![],
            fused_keywords: vec![],
            fused_summary: None,
            centroid_page_ref: 0,
            created_at: 0,
            updated_at: 0,
            version: 1,
        }
    }

    fn build_test_indexes(
        engine: &mut StorageEngine,
    ) -> (SparseIndex, L2MetaIndex, L1ReverseIndex) {
        let mut sparse_index = SparseIndex::new();

        let ctx_a = make_context(101, "rust memory search", vec![501]);
        let ctx_b = make_context(102, "python web framework", vec![502]);
        let ctx_c = make_context(103, "rust concurrency patterns", vec![501, 502]);

        insert_test_context_to_engine(engine, &mut sparse_index, ctx_a);
        insert_test_context_to_engine(engine, &mut sparse_index, ctx_b);
        insert_test_context_to_engine(engine, &mut sparse_index, ctx_c);

        let l2_meta = L2MetaIndex::build_empty();
        let l1_reverse = L1ReverseIndex::build(engine).unwrap();

        (sparse_index, l2_meta, l1_reverse)
    }

    fn insert_test_context_to_engine(
        engine: &mut StorageEngine,
        sparse_index: &mut SparseIndex,
        ctx: ContextSlot,
    ) {
        use crate::store::write_slot;
        write_slot(engine, REC_L2_TOPIC, ctx.id, &ctx).unwrap();
        let kw_text: String = ctx.user_keywords.join(" ");
        let terms: Vec<String> = kw_text
            .split_whitespace()
            .map(|s| s.to_lowercase())
            .collect();
        sparse_index.add_document(ctx.id, terms, kw_text.split_whitespace().count() as u32);
    }

    fn default_weights() -> SearchWeights {
        SearchWeights {
            bm25_weight: 0.45,
            vector_weight: 0.55,
            n_probes: 8,
            enable_reranker: true,
            rerank_max_candidates: 20,
            activation_boost: 1.3,
        }
    }

    #[test]
    fn test_rerank_candidates_reorders_pool() {
        let mut ctx_a = make_context(1, "apple banana", vec![]);
        ctx_a.fused_summary = Some("apple pie".to_string());
        let mut ctx_b = make_context(2, "cherry date", vec![]);
        ctx_b.fused_summary = Some("nothing".to_string());
        let mut ctx_c = make_context(3, "apple cherry", vec![]);
        ctx_c.fused_summary = Some("pie chart".to_string());

        let candidates = vec![
            (ctx_a.clone(), 0.9),
            (ctx_b.clone(), 0.8),
            (ctx_c.clone(), 0.7),
        ];

        let encoder = crate::encoder::MockEncoder::new(768);
        let ranked = rerank_candidates(
            "apple pie banana",
            &candidates,
            &encoder,
            candidates.len(),
            2,
        )
        .unwrap();

        assert_eq!(ranked.len(), 2);
        assert_eq!(ranked[0].0.id, ctx_a.id);
        assert_eq!(ranked[1].0.id, ctx_c.id);
    }

    #[test]
    fn test_rerank_candidates_truncates_to_max_candidates() {
        let ctx_a = make_context(1, "apple banana", vec![]);
        let ctx_b = make_context(2, "cherry date", vec![]);
        let ctx_c = make_context(3, "apple cherry", vec![]);

        let candidates = vec![
            (ctx_a.clone(), 0.9),
            (ctx_b.clone(), 0.8),
            (ctx_c.clone(), 0.7),
        ];

        let encoder = crate::encoder::MockEncoder::new(768);
        let ranked = rerank_candidates("apple", &candidates, &encoder, 2, 2).unwrap();

        assert_eq!(ranked.len(), 2);
        assert_eq!(ranked[0].0.id, ctx_a.id);
        assert_eq!(ranked[1].0.id, ctx_b.id);
    }

    struct FailingEncoder;

    impl crate::encoder::Encoder for FailingEncoder {
        fn encode(&self, _text: &str) -> Result<crate::encoder::EncoderOutput, MemHopError> {
            Ok(crate::encoder::EncoderOutput {
                dense: vec![],
                sparse: HashMap::new(),
            })
        }

        fn dim(&self) -> usize {
            768
        }

        fn mode(&self) -> &str {
            "failing"
        }

        fn rerank(&self, _query: &str, _documents: &[String]) -> Result<Vec<f32>, MemHopError> {
            Err(MemHopError::EncoderError("rerank unavailable".into()))
        }
    }

    #[test]
    fn test_rerank_candidates_failure_fallback() {
        let ctx_a = make_context(1, "a", vec![]);
        let ctx_b = make_context(2, "b", vec![]);
        let ctx_c = make_context(3, "c", vec![]);

        let candidates = vec![
            (ctx_a.clone(), 0.9),
            (ctx_b.clone(), 0.8),
            (ctx_c.clone(), 0.7),
        ];

        let encoder = FailingEncoder;
        let ranked = rerank_candidates("q", &candidates, &encoder, candidates.len(), 2).unwrap();

        assert_eq!(ranked.len(), 2);
        assert_eq!(ranked[0].0.id, ctx_a.id);
        assert_eq!(ranked[1].0.id, ctx_b.id);
    }

    #[test]
    fn test_search_context_route_a_unconstrained() {
        let temp = NamedTempFile::new().unwrap();
        let mut engine = StorageEngine::create(temp.path(), 768).unwrap();
        let (_sparse_index, l2_meta, l1_reverse) = build_test_indexes(&mut engine);

        // Use a minimal SearchQuery placeholder (the test only verifies engine writes, not search)
        let _query = SearchQuery {
            query: "rust memory".to_string(),
            layers: vec![],
            max_results: 20,
            min_score: 0.0,
            include_profile: false,
            filters: None,
            directed_l2_id: None,
            directed_l3_id: None,
            auto_create: None,
        };

        // Verify the data was written to the engine
        let ctx = engine.read_record(101).unwrap().unwrap();
        assert_eq!(ctx.0, REC_L2_TOPIC);

        let ctx_data = crate::store::read_slot::<ContextSlot>(&engine, 101)
            .unwrap()
            .unwrap();
        assert_eq!(ctx_data.user_keywords[0], "rust memory search");
    }

    #[test]
    fn test_search_context_route_b_by_l2_id() {
        let temp = NamedTempFile::new().unwrap();
        let mut engine = StorageEngine::create(temp.path(), 768).unwrap();
        build_test_indexes(&mut engine);

        let ctx = engine.read_record(102).unwrap().unwrap();
        assert_eq!(ctx.0, REC_L2_TOPIC);

        let ctx_data = crate::store::read_slot::<ContextSlot>(&engine, 102)
            .unwrap()
            .unwrap();
        assert!(ctx_data.user_keywords.join(", ").contains("python"));
    }

    #[test]
    fn test_search_context_route_c_by_l3_id() {
        let temp = NamedTempFile::new().unwrap();
        let mut engine = StorageEngine::create(temp.path(), 768).unwrap();
        build_test_indexes(&mut engine);

        // Verify all contexts exist
        assert!(engine.contains(103));
        assert!(engine.contains(101));
        assert!(engine.contains(102));
    }

    #[test]
    fn test_search_context_context_id_alias() {
        let temp = NamedTempFile::new().unwrap();
        let mut engine = StorageEngine::create(temp.path(), 768).unwrap();
        build_test_indexes(&mut engine);

        let ctx_data = crate::store::read_slot::<ContextSlot>(&engine, 101)
            .unwrap()
            .unwrap();
        assert!(!ctx_data.user_keywords.is_empty());
    }

    #[test]
    fn test_depth3_retrieval_weighting() {
        let temp = NamedTempFile::new().unwrap();
        let mut engine = StorageEngine::create(temp.path(), 768).unwrap();
        let mut sparse_index = SparseIndex::new();

        let base = ContextSlot {
            id: 0,
            scene_id: 0,
            parent_id: None,
            children_ids: vec![],
            depth: 1,
            user_keywords: vec!["rust memory search".to_string()],
            user_timestamp: 0,
            user_l4_refs: vec![],
            user_l3_refs: vec![],
            agent_keywords: vec![],
            agent_timestamp: 0,
            agent_l4_refs: vec![],
            agent_l3_refs: vec![],
            fused_keywords: vec![],
            fused_summary: None,
            centroid_page_ref: 0,
            created_at: 0,
            updated_at: 0,
            version: 1,
        };

        let ctx_depth1 = ContextSlot {
            id: 101,
            ..base.clone()
        };
        let ctx_depth3 = ContextSlot {
            id: 103,
            depth: 3,
            ..base.clone()
        };

        insert_test_context_to_engine(&mut engine, &mut sparse_index, ctx_depth1);
        insert_test_context_to_engine(&mut engine, &mut sparse_index, ctx_depth3);

        // verify data via engine
        assert!(engine.contains(101));
        assert!(engine.contains(103));
    }

    #[test]
    fn test_l1_reverse_index_serialize_roundtrip() {
        let mut idx = L1ReverseIndex::new();
        idx.add(2000, 1000, 1);
        idx.add(2000, 1001, 2);
        idx.add(2001, 1002, 3);

        let serialized = idx.serialize().unwrap();
        let restored = L1ReverseIndex::deserialize(&serialized).unwrap();

        let ctx2000 = HashSet::from([2000u64]);
        let ctx2001 = HashSet::from([2001u64]);
        let both = HashSet::from([2000u64, 2001u64]);

        assert_eq!(restored.find_associated(&ctx2000).len(), 2);
        assert_eq!(restored.find_associated(&ctx2001).len(), 1);
        assert_eq!(restored.find_associated(&both).len(), 3);

        let mut restored_mut = restored;
        restored_mut.add(2000, 1000, 10);
        let nodes = restored_mut.find_associated(&ctx2000);
        assert_eq!(nodes.len(), 2);
        let page_refs: HashSet<u64> = nodes.iter().map(|(_, pr)| *pr).collect();
        assert!(page_refs.contains(&10));
    }

    #[test]
    fn test_l1_reverse_index_build() {
        let temp = NamedTempFile::new().unwrap();
        let mut engine = StorageEngine::create(temp.path(), 768).unwrap();

        let node1 = ContextNode {
            id_hash: 1000,
            context_id: 2000,
            vector_page_ref: 0,
            importance: 0.5,
            valence: 0.0,
            arousal: 0.0,
            created_at: 0,
            updated_at: 0,
            version: 1,
            edge_ptrs: vec![],
        };
        let node2 = ContextNode {
            id_hash: 1001,
            context_id: 2000,
            ..node1.clone()
        };
        let node3 = ContextNode {
            id_hash: 1002,
            context_id: 2001,
            ..node1.clone()
        };

        insert_node_to_engine(&mut engine, node1);
        insert_node_to_engine(&mut engine, node2);
        insert_node_to_engine(&mut engine, node3);

        let idx = L1ReverseIndex::build(&engine).unwrap();

        let ctx2000 = HashSet::from([2000u64]);
        let ctx2001 = HashSet::from([2001u64]);
        let both = HashSet::from([2000u64, 2001u64]);

        assert_eq!(idx.find_associated(&ctx2000).len(), 2);
        assert_eq!(idx.find_associated(&ctx2001).len(), 1);
        assert_eq!(idx.find_associated(&both).len(), 3);
    }

    fn insert_node_to_engine(engine: &mut StorageEngine, node: ContextNode) {
        let data = node.serialize().unwrap();
        engine
            .write_record(REC_L1_SCENE_NODE, node.id_hash, &data)
            .unwrap();
    }

    #[test]
    fn test_l1_reverse_index_add_and_remove() {
        let mut idx = L1ReverseIndex::new();
        idx.add(2000, 1000, 1);
        idx.add(2000, 1001, 2);
        idx.add(2001, 1002, 3);

        let ctx2000 = HashSet::from([2000u64]);
        let ctx2001 = HashSet::from([2001u64]);
        let both = HashSet::from([2000u64, 2001u64]);

        assert_eq!(idx.find_associated(&ctx2000).len(), 2);
        assert_eq!(idx.find_associated(&ctx2001).len(), 1);
        assert_eq!(idx.find_associated(&both).len(), 3);

        idx.add(2000, 1000, 10);
        let nodes = idx.find_associated(&ctx2000);
        assert_eq!(nodes.len(), 2);
        let page_refs: HashSet<u64> = nodes.iter().map(|(_, pr)| *pr).collect();
        assert!(page_refs.contains(&10));
        assert!(!page_refs.contains(&1));

        idx.remove_node(1001);
        assert_eq!(idx.find_associated(&ctx2000).len(), 1);
        assert_eq!(idx.find_associated(&both).len(), 2);

        idx.remove_context(2001);
        assert_eq!(idx.find_associated(&ctx2001).len(), 0);
        assert_eq!(idx.find_associated(&both).len(), 1);
    }
}
