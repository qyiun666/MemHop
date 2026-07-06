// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! In-memory L2 context metadata index.
//!
//! `L2MetaIndex` keeps a lightweight, mutable copy of the metadata fields for
//! every L2 `ContextSlot`. It is rebuilt from disk on `open()` and updated
//! whenever a context is written.


use crate::index::btree::BTreeIndex;
use crate::index::sparse::SparseIndex;
use crate::layers::context::{ActivationState, ContextSlot};
use crate::shared::slot_io::get_slot_data;
use crate::util::PageType;
use std::collections::HashMap;

/// Activation status used by the L2 metadata index.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ActivationStatus {
    Dormant,
    Active,
    Crystallized,
}

impl From<ActivationState> for ActivationStatus {
    fn from(state: ActivationState) -> Self {
        match state {
            ActivationState::Dormant => ActivationStatus::Dormant,
            ActivationState::Active => ActivationStatus::Active,
            ActivationState::Crystallized => ActivationStatus::Crystallized,
        }
    }
}

/// Lightweight metadata copy of an L2 context.
#[derive(Debug, Clone)]
pub struct L2Meta {
    pub id_hash: u64,
    pub page_ref: u64,
    pub title: String,
    pub summary: Option<String>,
    pub depth: u8,
    pub status: ActivationStatus,
    pub activation_score: f32,
    /// Page reference to the centroid vector stored in mmap.
    pub vector_offset: u64,
    pub turn_count: u32,
    pub archive_count: usize,
    pub l3_refs: Vec<u64>,
    pub timestamp: u64,
}

impl L2Meta {
    fn from_context(page_ref: u64, ctx: &ContextSlot) -> Self {
        Self {
            id_hash: ctx.id_hash,
            page_ref,
            title: ctx.title.clone(),
            summary: ctx.summary.clone(),
            depth: ctx.depth,
            status: ctx.activation_state.into(),
            activation_score: ctx.activation_score,
            vector_offset: ctx.centroid_page_ref,
            turn_count: ctx.turn_count,
            archive_count: ctx.archive_refs.len(),
            l3_refs: ctx.l3_refs.clone(),
            timestamp: ctx.updated_at.max(ctx.created_at).max(0) as u64,
        }
    }
}

/// In-memory index of L2 context metadata.
#[derive(Debug, Clone, Default)]
pub struct L2MetaIndex {
    entries: HashMap<u64, L2Meta>,
}

impl L2MetaIndex {
    /// Create an empty index.
    pub fn new() -> Self {
        Self {
            entries: HashMap::new(),
        }
    }

    /// Build the index by scanning all L2 `ContextSlot` entries in the B-tree.
    pub fn build(mmap: &[u8], btree: &BTreeIndex) -> Self {
        let data = mmap;
        let mut entries = HashMap::new();

        for (&id_hash, &page_ref) in btree.iter_unsorted() {
            let page_id = (page_ref >> 16) as u32;
            if page_id == 0 {
                continue;
            }

            // Only index pages whose header type is Context.
            let pt_offset = (page_id as usize) * crate::util::PAGE_SIZE + 4;
            if pt_offset + 2 > data.len() {
                continue;
            }
            let pt = u16::from_le_bytes([data[pt_offset], data[pt_offset + 1]]);
            if pt != PageType::Context as u16 {
                continue;
            }

            if let Some(slot_data) = get_slot_data(data, page_ref) {
                if let Ok(ctx) = ContextSlot::deserialize_slot(slot_data) {
                    entries.insert(id_hash, L2Meta::from_context(page_ref, &ctx));
                }
            }
        }

        Self { entries }
    }

    /// Update or insert metadata from a full `ContextSlot`.
    pub fn update_from_context(&mut self, ctx: &ContextSlot) {
        let page_ref = self
            .entries
            .get(&ctx.id_hash)
            .map(|m| m.page_ref)
            .unwrap_or_else(|| (ctx.id_hash) << 16); // placeholder; caller should set via build
        self.entries
            .insert(ctx.id_hash, L2Meta::from_context(page_ref, ctx));
    }

    /// Update or insert metadata directly.
    pub fn update(&mut self, meta: L2Meta) {
        self.entries.insert(meta.id_hash, meta);
    }

    /// Get metadata for a context by id_hash.
    pub fn get(&self, id_hash: u64) -> Option<&L2Meta> {
        self.entries.get(&id_hash)
    }

    /// Mutable access to an entry.
    pub fn get_mut(&mut self, id_hash: u64) -> Option<&mut L2Meta> {
        self.entries.get_mut(&id_hash)
    }

    /// Remove an entry and return it.
    pub fn remove(&mut self, id_hash: u64) -> Option<L2Meta> {
        self.entries.remove(&id_hash)
    }

    /// Iterate over all indexed entries.
    pub fn iter(&self) -> impl Iterator<Item = (&u64, &L2Meta)> {
        self.entries.iter()
    }

    /// BM25 pre-screen over indexed L2 title + summary text.
    /// Returns up to `limit` candidate L2 `id_hash` values that exist in this index.
    pub fn bm25_prescreen(
        &self,
        query: &str,
        sparse_index: &SparseIndex,
        limit: usize,
    ) -> Vec<u64> {
        let terms: Vec<String> = crate::index::sparse::tokenize(query);
        if terms.is_empty() {
            return Vec::new();
        }
        sparse_index
            .search(&terms, limit * 2)
            .into_iter()
            .filter_map(|(id_hash, _score)| self.entries.contains_key(&id_hash).then_some(id_hash))
            .take(limit)
            .collect()
    }

    /// Return all L2 `id_hash`s whose `l3_refs` contain `l3_id`.
    pub fn get_l2_ids_by_l3(&self, l3_id: u64) -> Vec<u64> {
        self.entries
            .values()
            .filter(|m| m.l3_refs.contains(&l3_id))
            .map(|m| m.id_hash)
            .collect()
    }

    /// Number of indexed contexts.
    pub fn len(&self) -> usize {
        self.entries.len()
    }

    pub fn is_empty(&self) -> bool {
        self.entries.is_empty()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::index::sparse::SparseIndex;
    use crate::test_helpers::*;

    fn make_context(id_hash: u64, title: &str, l3_refs: Vec<u64>) -> ContextSlot {
        ContextSlot {
            id_hash,
            parent_id: None,
            depth: 1,
            title: title.to_string(),
            summary: None,
            archive_refs: Vec::new(),
            l3_refs,
            turn_count: 0,
            created_at: 1000,
            updated_at: 2000,
            version: 1,
            importance: 0.5,
            activation_score: 0.7,
            is_active: true,
            activation_state: ActivationState::Active,
            centroid_page_ref: (id_hash + 1000) << 16,
            dialogue_range: (0, 0),
            llm_params: crate::layers::context::LlmParams::default(),
        }
    }

    #[test]
    fn test_build_and_get() {
        let (_temp, mut mmap, mut header, mut file) = create_test_mmap_with_tempfile(20);
        let mut btree = BTreeIndex::new();
        let mut sparse = SparseIndex::new();

        let ctx = make_context(101, "rust memory search", vec![501]);
        insert_test_context(
            &mut mmap,
            &mut header,
            &mut btree,
            &mut sparse,
            ctx,
            &mut file,
        );

        let l2_meta = L2MetaIndex::build(&mmap, &btree);
        assert_eq!(l2_meta.len(), 1);

        let meta = l2_meta.get(101).unwrap();
        assert_eq!(meta.title, "rust memory search");
        assert_eq!(meta.l3_refs, vec![501]);
        assert_eq!(meta.status, ActivationStatus::Active);
        assert_eq!(meta.activation_score, 0.7);
        assert_eq!(meta.vector_offset, (101 + 1000) << 16);
        assert_eq!(meta.timestamp, 2000);
    }

    #[test]
    fn test_get_l2_ids_by_l3() {
        let (_temp, mut mmap, mut header, mut file) = create_test_mmap_with_tempfile(20);
        let mut btree = BTreeIndex::new();
        let mut sparse = SparseIndex::new();

        let ctx_a = make_context(101, "topic a", vec![501, 502]);
        let ctx_b = make_context(102, "topic b", vec![502]);
        let ctx_c = make_context(103, "topic c", vec![503]);
        insert_test_context(
            &mut mmap,
            &mut header,
            &mut btree,
            &mut sparse,
            ctx_a,
            &mut file,
        );
        insert_test_context(
            &mut mmap,
            &mut header,
            &mut btree,
            &mut sparse,
            ctx_b,
            &mut file,
        );
        insert_test_context(
            &mut mmap,
            &mut header,
            &mut btree,
            &mut sparse,
            ctx_c,
            &mut file,
        );

        let l2_meta = L2MetaIndex::build(&mmap, &btree);
        let ids = l2_meta.get_l2_ids_by_l3(502);
        assert_eq!(ids.len(), 2);
        assert!(ids.contains(&101));
        assert!(ids.contains(&102));

        assert_eq!(l2_meta.get_l2_ids_by_l3(503), vec![103]);
        assert!(l2_meta.get_l2_ids_by_l3(999).is_empty());
    }

    #[test]
    fn test_bm25_prescreen_filters_l2_only() {
        let (_temp, mut mmap, mut header, mut file) = create_test_mmap_with_tempfile(20);
        let mut btree = BTreeIndex::new();
        let mut sparse = SparseIndex::new();

        let ctx = make_context(101, "rust memory search", vec![]);
        insert_test_context(
            &mut mmap,
            &mut header,
            &mut btree,
            &mut sparse,
            ctx,
            &mut file,
        );

        // Insert a non-context entry that happens to match the sparse query.
        let node_page_id = crate::file::page::allocate_page(
            &mut mmap,
            &mut header,
            PageType::HypergraphNode,
            3,
            0,
            &mut file,
        )
        .unwrap();
        crate::file::page::write_page_data(&mut mmap, node_page_id, &[0u8; 64]).unwrap();
        btree.insert(201, crate::file::page::encode_page_ref(node_page_id, 0));
        sparse.add_document(201, vec!["rust".to_string(), "memory".to_string()], 2);

        let l2_meta = L2MetaIndex::build(&mmap, &btree);
        let ids = l2_meta.bm25_prescreen("rust memory", &sparse, 10);
        assert_eq!(ids, vec![101]);
    }

    #[test]
    fn test_update_remove_and_iter() {
        let mut idx = L2MetaIndex::new();
        let ctx = make_context(42, "test", vec![]);
        idx.update_from_context(&ctx);
        assert_eq!(idx.get(42).unwrap().title, "test");

        let meta = L2Meta {
            id_hash: 42,
            page_ref: 1 << 16,
            title: "updated".to_string(),
            summary: None,
            depth: 1,
            status: ActivationStatus::Active,
            activation_score: 0.9,
            vector_offset: 0,
            turn_count: 0,
            archive_count: 0,
            l3_refs: vec![],
            timestamp: 0,
        };
        idx.update(meta);
        assert_eq!(idx.get(42).unwrap().title, "updated");
        assert_eq!(idx.get(42).unwrap().activation_score, 0.9);

        let removed = idx.remove(42);
        assert!(removed.is_some());
        assert!(idx.get(42).is_none());

        assert_eq!(idx.iter().count(), 0);
    }
}
