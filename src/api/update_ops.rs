// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Update API operations.

use crate::query::types::{
    CrystalSummary, KnowledgeSummary, ProfileResult, TopicDetail, TopicSummary,
    UpdateProfileRequest, UpdateRequest, UpdateResult,
};
use crate::MemHop;
use crate::Result;

impl MemHop {
    /// Update memory with multi-level updates
    ///
    /// # Arguments
    /// * `request` - Update request with dialogue, titles, and action chain
    ///
    /// # Returns
    /// UpdateResult with IDs of created/updated items
    pub fn update_memory(&mut self, request: UpdateRequest) -> Result<UpdateResult> {
        use crate::query::update::update_memory as update_impl;

        let result = update_impl(
            &mut self.mmap,
            &mut self.header,
            request,
            &mut self.btree,
            &mut self.sparse_index,
            &mut self.file,
            &self.config,
            Some(&mut self.degree_tracker),
            Some(&mut self.l3_index_map),
        );

        // Rebuild IVF index after mutation (single update may have added vectors)
        self.rebuild_ivf_index();

        result
    }

    /// Update profile (merge strategy - only update Some fields)
    pub fn update_profile(&mut self, request: UpdateProfileRequest) -> Result<ProfileResult> {
        use crate::query::update_title::update_profile as impl_fn;
        impl_fn(
            &mut self.mmap,
            &mut self.header,
            &mut self.btree,
            request,
            &mut self.file,
        )
    }

    /// Update topic title (with sparse index synchronization)
    pub fn update_topic_title(&mut self, id: &str, new_title: String) -> Result<TopicSummary> {
        use crate::query::update_title::update_topic_title as impl_fn;
        impl_fn(
            &mut self.mmap,
            &mut self.header,
            &self.btree,
            &mut self.sparse_index,
            id,
            new_title,
        )
    }

    /// Update topic title with optional l3_refs (L3 knowledge node references)
    pub fn update_topic_title_with_refs(
        &mut self,
        id: &str,
        new_title: String,
        l3_refs: Option<Vec<String>>,
    ) -> Result<TopicSummary> {
        use crate::query::update_title::update_topic_title_with_refs as impl_fn;
        impl_fn(
            &mut self.mmap,
            &mut self.header,
            &self.btree,
            &mut self.sparse_index,
            id,
            new_title,
            l3_refs,
        )
    }

    /// Update crystal title
    pub fn update_crystal_title(&mut self, id: &str, new_title: String) -> Result<CrystalSummary> {
        use crate::query::update_title::update_crystal_title as impl_fn;
        impl_fn(&mut self.mmap, &self.btree, id, new_title)
    }

    /// Update L3 knowledge title (Interface 15)
    pub fn update_knowledge_title(
        &mut self,
        id: &str,
        new_title: String,
    ) -> Result<KnowledgeSummary> {
        use crate::query::update_title::update_knowledge_title as impl_fn;
        impl_fn(&mut self.mmap, &self.btree, id, new_title)
    }

    /// Merge multiple topics into a primary topic
    pub fn merge_topics(
        &mut self,
        primary_id: &str,
        secondary_ids: Vec<String>,
    ) -> Result<TopicDetail> {
        use crate::query::merge::merge_topics as impl_fn;
        impl_fn(
            &mut self.mmap,
            &mut self.header,
            &mut self.btree,
            &mut self.sparse_index,
            primary_id,
            secondary_ids,
        )
    }
}
