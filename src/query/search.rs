// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// search_context() — L2-centric retrieval engine.
// Two-channel retrieval (BM25 + vector) scoped to candidate L2 contexts,
// with an optional cross-encoder rerank step downstream.

#![cfg_attr(not(feature = "grpc-encoder"), allow(dead_code, unused_imports))]

use crate::config::SearchWeights;
use crate::file::header::FileHeader;
use crate::index::btree::BTreeIndex;
use crate::index::l2_meta::L2MetaIndex;
use crate::index::sparse::SparseIndex;
use crate::index::vector::IVFIndex;
use crate::layers::context::ContextSlot;
use crate::layers::context_node::ContextNode;
use crate::query::types::*;
use crate::shared::common;
use crate::shared::slot_io::{decode_page_id, get_slot_data};
use crate::util::{hash_id, DEFAULT_GROW_PAGES, PAGE_SIZE, SENTINEL_PAGE_ID};
use crate::MemHopError;
use memmap2::MmapMut;
use std::collections::{HashMap, HashSet};
use std::fs::File;

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
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    query: SearchQuery,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    l2_meta: &L2MetaIndex,
    vector_dim: usize,
    encoder: Option<&(dyn crate::encoder::Encoder + Send + Sync)>,
    search_weights: &SearchWeights,
    ivf_index: Option<&IVFIndex>,
    l1_reverse: &L1ReverseIndex,
    file: &mut File,
) -> Result<SearchResult, MemHopError> {
    let target_l2_id = query.l2_id.as_ref().or(query.context_id.as_ref());

    // ========================================================================
    // Route 1: auto_create
    // ========================================================================
    let filtered_l2 = if query.auto_create == 1 {
        let new_ctx = create_new_l2_context(
            mmap,
            header,
            btree,
            sparse_index,
            &query.dialogue,
            vector_dim,
            file,
            encoder,
        )?;
        vec![(new_ctx, 1.0)]

    // ========================================================================
    // Route 2: l2_id / context_id direct load
    // ========================================================================
    } else if let Some(l2_id_str) = target_l2_id {
        let target_hash = common::parse_id_to_hash(l2_id_str);
        let data: &[u8] = &mmap[..];

        if let Some(slot_data) = btree
            .search(target_hash)
            .and_then(|pr| get_slot_data(data, pr))
        {
            match ContextSlot::deserialize_slot(slot_data) {
                Ok(ctx) => vec![(ctx, 1.0)],
                Err(_) => vec![],
            }
        } else {
            vec![]
        }

    // ========================================================================
    // Route 3 & 4: two-channel retrieval via pipeline
    // ========================================================================
    } else {
        let data: &[u8] = &mmap[..];
        let candidates = super::pipeline::l2_search::build_candidate_set(
            l2_meta,
            &query.dialogue,
            sparse_index,
            query.context_limit * 2,
            query.l3_id.as_deref(),
        );

        // If an explicit l3_id produced no candidates, return empty without building indexes.
        if query.l3_id.is_some() && candidates.as_ref().map_or(true, |c| c.is_empty()) {
            vec![]
        } else {
            super::pipeline::l2_search::search_l2_candidates(
                data,
                &query.dialogue,
                sparse_index,
                btree,
                l2_meta,
                vector_dim,
                encoder,
                search_weights,
                ivf_index,
                query.context_limit,
                query.min_score,
                candidates.as_ref(),
            )?
        }
    };

    let data: &[u8] = &mmap[..];
    let l1_associated = super::pipeline::l1_assoc::get_l1_associated_contexts(
        data,
        &filtered_l2,
        btree,
        l1_reverse,
    )?;

    let mut all_contexts = filtered_l2.clone();
    all_contexts.extend(l1_associated.clone());

    let l0_profile = crate::query::profile::read_profile(mmap, btree)?;

    let l1_previews = super::pipeline::l1_assoc::get_l1_previews(
        data,
        &filtered_l2,
        btree,
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

    /// Build the reverse index by scanning the btree once.
    pub fn build(data: &[u8], btree: &BTreeIndex) -> Result<Self, MemHopError> {
        let mut idx = Self::new();
        for (&id_hash, &page_ref) in btree.iter_unsorted() {
            let page_id = decode_page_id(page_ref);
            let page_header = match crate::file::page::read_page_header(data, page_id) {
                Ok(h) => h,
                Err(_) => continue,
            };
            if page_header.page_type != crate::util::PageType::ContextNode as u16 {
                continue;
            }
            if let Some(slot_data) = get_slot_data(data, page_ref) {
                if let Ok(node) = ContextNode::deserialize(slot_data) {
                    if node.context_id != 0 {
                        idx.add(node.context_id, id_hash, page_ref);
                    }
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
fn create_new_l2_context(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    dialogue: &str,
    vector_dim: usize,
    file: &mut File,
    encoder: Option<&(dyn crate::encoder::Encoder + Send + Sync)>,
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

    let centroid_page_ref = if let Some(enc) = encoder {
        match enc.encode(dialogue) {
            Ok(output) if !output.dense.is_empty() => {
                match crate::file::free_list::allocate_or_extend(
                    mmap,
                    header,
                    file,
                    DEFAULT_GROW_PAGES,
                ) {
                    Ok(vec_page_id) => {
                        let vec_slot_index = 0u16;
                        match crate::index::vector::write_vector(
                            mmap,
                            vec_page_id,
                            vec_slot_index,
                            id_hash,
                            &output.dense,
                            vector_dim,
                        ) {
                            Ok(()) => ((vec_page_id as u64) << 16) | (vec_slot_index as u64),
                            Err(_) => 0,
                        }
                    }
                    Err(_) => 0,
                }
            }
            _ => 0,
        }
    } else {
        0
    };

    let new_ctx = ContextSlot {
        id: id_hash,
        parent_id: None,
        children_ids: vec![],
        scene_id: 0,
        depth: 1,
        user_keywords: vec![title],
        user_timestamp: now_ms,
        user_l4_refs: Vec::new(),
        user_l3_refs: Vec::new(),
        agent_keywords: vec![],
        agent_timestamp: 0,
        agent_l4_refs: Vec::new(),
        agent_l3_refs: Vec::new(),
        fused_keywords: vec![],
        fused_summary: None,
        centroid_page_ref,
        created_at: now_ms,
        updated_at: now_ms,
        version: 1,
    };

    let ctx_data = new_ctx
        .serialize()
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;

    let page_id =
        crate::file::free_list::allocate_or_extend(mmap, header, file, DEFAULT_GROW_PAGES)?;
    let page_offset = crate::shared::slot_io::page_offset(page_id);

    let mut page_header = crate::file::page::PageHeader::new(
        page_id,
        crate::util::PageType::Context,
        2,
        SENTINEL_PAGE_ID,
    );
    page_header.slot_count = 1;
    page_header.free_bytes = (PAGE_SIZE - 32 - ctx_data.len()) as u16;
    crate::file::page::write_page_header(mmap, page_id, &page_header)?;

    let data_offset = page_offset + 32;
    if data_offset + ctx_data.len() > mmap.len() {
        return Err(MemHopError::Serialization(format!(
            "ContextSlot data too large for page: {} > {}",
            ctx_data.len(),
            mmap.len() - data_offset
        )));
    }
    mmap[data_offset..data_offset + ctx_data.len()].copy_from_slice(&ctx_data);

    let page_ref = (page_id as u64) << 16;
    btree.insert(id_hash, page_ref);

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
    use crate::test_helpers::*;

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
        mmap: &mut MmapMut,
        header: &mut FileHeader,
        file: &mut File,
    ) -> (BTreeIndex, SparseIndex, L2MetaIndex, L1ReverseIndex) {
        let mut btree = BTreeIndex::new();
        let mut sparse_index = SparseIndex::new();

        let ctx_a = make_context(101, "rust memory search", vec![501]);
        let ctx_b = make_context(102, "python web framework", vec![502]);
        let ctx_c = make_context(103, "rust concurrency patterns", vec![501, 502]);

        insert_test_context(mmap, header, &mut btree, &mut sparse_index, ctx_a, file);
        insert_test_context(mmap, header, &mut btree, &mut sparse_index, ctx_b, file);
        insert_test_context(mmap, header, &mut btree, &mut sparse_index, ctx_c, file);

        let l2_meta = L2MetaIndex::build(&mmap[..], &btree);
        let l1_reverse = L1ReverseIndex::build(&mmap[..], &btree).unwrap();

        (btree, sparse_index, l2_meta, l1_reverse)
    }

    fn default_weights() -> SearchWeights {
        SearchWeights {
            bm25_weight: 0.45,
            vector_weight: 0.55,
            n_probes: 8,
            enable_reranker: true,
            rerank_max_candidates: 20,
            recency_weight: 0.5,
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
        let (_temp, mut mmap, mut header, mut file) = create_test_mmap_with_tempfile(20);
        let (mut btree, mut sparse_index, l2_meta, l1_reverse) =
            build_test_indexes(&mut mmap, &mut header, &mut file);

        let query = SearchQuery {
            dialogue: "rust memory".to_string(),
            l2_id: None,
            context_id: None,
            l3_id: None,
            context_limit: 10,
            auto_create: 0,
            min_score: 0.0,
            source: RequestSource::default(),
            llm_keywords: None,
            enable_llm_preprocess: false,
        };
        let result = search_context(
            &mut mmap,
            &mut header,
            query,
            &mut btree,
            &mut sparse_index,
            &l2_meta,
            768,
            None,
            &default_weights(),
            None,
            &l1_reverse,
            &mut file,
        )
        .unwrap();

        let ids: Vec<u64> = result
            .contexts
            .iter()
            .map(|c| common::parse_id_to_hash(&c.id))
            .collect();
        assert!(ids.contains(&101), "should return rust topic 101");
        assert!(ids.contains(&103), "should return rust topic 103");
        assert!(!ids.contains(&102), "should not return python topic 102");
        let l3_hashes: Vec<u64> = result
            .l3_ids
            .iter()
            .map(|id| common::parse_id_to_hash(id))
            .collect();
        assert!(l3_hashes.contains(&501));
        assert!(l3_hashes.contains(&502));
    }

    #[test]
    fn test_search_context_route_b_by_l2_id() {
        let (_temp, mut mmap, mut header, mut file) = create_test_mmap_with_tempfile(20);
        let (mut btree, mut sparse_index, l2_meta, l1_reverse) =
            build_test_indexes(&mut mmap, &mut header, &mut file);

        let query = SearchQuery {
            dialogue: "completely unrelated query".to_string(),
            l2_id: Some(format_hash(102)),
            context_id: None,
            l3_id: None,
            context_limit: 10,
            auto_create: 0,
            min_score: 0.0,
            source: RequestSource::default(),
            llm_keywords: None,
            enable_llm_preprocess: false,
        };

        let result = search_context(
            &mut mmap,
            &mut header,
            query,
            &mut btree,
            &mut sparse_index,
            &l2_meta,
            768,
            None,
            &default_weights(),
            None,
            &l1_reverse,
            &mut file,
        )
        .unwrap();

        assert_eq!(result.contexts.len(), 1);
        assert_eq!(common::parse_id_to_hash(&result.contexts[0].id), 102);
        assert!(result.contexts[0]
            .user_keywords
            .join(", ")
            .contains("python"));
    }

    #[test]
    fn test_search_context_route_c_by_l3_id() {
        // ... unchanged from original
        let (_temp, mut mmap, mut header, mut file) = create_test_mmap_with_tempfile(20);
        let (mut btree, mut sparse_index, l2_meta, l1_reverse) =
            build_test_indexes(&mut mmap, &mut header, &mut file);

        let query = SearchQuery {
            dialogue: "rust".to_string(),
            l2_id: None,
            context_id: None,
            l3_id: Some(format_hash(502)),
            context_limit: 10,
            auto_create: 0,
            min_score: 0.0,
            source: RequestSource::default(),
            llm_keywords: None,
            enable_llm_preprocess: false,
        };

        let result = search_context(
            &mut mmap,
            &mut header,
            query,
            &mut btree,
            &mut sparse_index,
            &l2_meta,
            768,
            None,
            &default_weights(),
            None,
            &l1_reverse,
            &mut file,
        )
        .unwrap();

        let ids: Vec<u64> = result
            .contexts
            .iter()
            .map(|c| common::parse_id_to_hash(&c.id))
            .collect();
        assert!(ids.contains(&103), "context 103 is linked to 502");
        assert!(!ids.contains(&101), "context 101 is not linked to 502");
    }

    #[test]
    fn test_search_context_context_id_alias() {
        let (_temp, mut mmap, mut header, mut file) = create_test_mmap_with_tempfile(20);
        let (mut btree, mut sparse_index, l2_meta, l1_reverse) =
            build_test_indexes(&mut mmap, &mut header, &mut file);

        let query = SearchQuery {
            dialogue: "ignored".to_string(),
            l2_id: None,
            context_id: Some(format_hash(101)),
            l3_id: None,
            context_limit: 10,
            auto_create: 0,
            min_score: 0.0,
            source: RequestSource::default(),
            llm_keywords: None,
            enable_llm_preprocess: false,
        };

        let result = search_context(
            &mut mmap,
            &mut header,
            query,
            &mut btree,
            &mut sparse_index,
            &l2_meta,
            768,
            None,
            &default_weights(),
            None,
            &l1_reverse,
            &mut file,
        )
        .unwrap();

        assert_eq!(result.contexts.len(), 1);
        assert_eq!(common::parse_id_to_hash(&result.contexts[0].id), 101);
    }

    #[test]
    fn test_depth3_retrieval_weighting() {
        let (_temp, mut mmap, mut header, mut file) = create_test_mmap_with_tempfile(10);
        let mut btree = BTreeIndex::new();
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

        insert_test_context(
            &mut mmap,
            &mut header,
            &mut btree,
            &mut sparse_index,
            ctx_depth1,
            &mut file,
        );
        insert_test_context(
            &mut mmap,
            &mut header,
            &mut btree,
            &mut sparse_index,
            ctx_depth3,
            &mut file,
        );

        let data: &[u8] = &mmap[..];
        let results =
            retrieve_l2_bm25(data, "rust memory search", &sparse_index, &btree, 10, None).unwrap();

        assert_eq!(
            results.len(),
            2,
            "depth-3 contexts should be included in retrieval"
        );

        let score_depth1 = results
            .iter()
            .find(|(ctx, _)| ctx.id == 101)
            .map(|(_, s)| *s)
            .unwrap();
        let score_depth3 = results
            .iter()
            .find(|(ctx, _)| ctx.id == 103)
            .map(|(_, s)| *s)
            .unwrap();

        assert!(
            score_depth1 > 0.0 && score_depth3 > 0.0,
            "both contexts should have positive scores"
        );
        assert!(
            (score_depth1 - score_depth3 * 2.0).abs() < 1e-6,
            "depth-3 score should be 0.5x the raw score ({} vs {})",
            score_depth1,
            score_depth3
        );
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
        let (_temp, mut mmap, mut header, mut file) = create_test_mmap_with_tempfile(20);
        let mut btree = BTreeIndex::new();

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

        insert_test_context_node(&mut mmap, &mut header, &mut btree, node1, &mut file);
        insert_test_context_node(&mut mmap, &mut header, &mut btree, node2, &mut file);
        insert_test_context_node(&mut mmap, &mut header, &mut btree, node3, &mut file);

        let data: &[u8] = &mmap[..];
        let idx = L1ReverseIndex::build(data, &btree).unwrap();

        let ctx2000 = HashSet::from([2000u64]);
        let ctx2001 = HashSet::from([2001u64]);
        let both = HashSet::from([2000u64, 2001u64]);

        assert_eq!(idx.find_associated(&ctx2000).len(), 2);
        assert_eq!(idx.find_associated(&ctx2001).len(), 1);
        assert_eq!(idx.find_associated(&both).len(), 3);
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
