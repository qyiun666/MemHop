// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! API-5 + API-6: L2 Context CRUD and merge operations.

use crate::query::types::{
    MergeResult, TopicDetail, TopicListQuery, TopicListResult, UpdateL2Fields,
};
use crate::{MemHop, Result};

impl MemHop {
    /// List L2 contexts with pagination and filtering.
    pub fn list_l2(&self, query: TopicListQuery) -> Result<TopicListResult> {
        crate::query::l2_ops::list_l2(&self.mmap, &self.header, &self.btree, query)
    }

    /// Get a single L2 context by ID.
    pub fn get_l2(&self, id: &str) -> Result<Option<TopicDetail>> {
        Ok(
            crate::query::l2_ops::get_l2(&self.mmap, &self.btree, id)?.map(|ctx| {
                crate::query::types::TopicDetail {
                    id: crate::shared::common::format_hash(ctx.id_hash),
                    title: ctx.title,
                    summary: ctx.summary,
                    depth: ctx.depth,
                    archive_refs: ctx
                        .archive_refs
                        .iter()
                        .map(|h| crate::shared::common::format_hash(*h))
                        .collect(),
                    l3_refs: ctx
                        .l3_refs
                        .iter()
                        .map(|h| crate::shared::common::format_hash(*h))
                        .collect(),
                    turn_count: ctx.turn_count,
                    parent_id: ctx.parent_id.map(crate::shared::common::format_hash),
                    is_active: ctx.is_active,
                    importance: ctx.importance,
                    activation_score: ctx.activation_score,
                    activation_state: format!("{:?}", ctx.activation_state),
                    created_at: ctx.created_at,
                    updated_at: ctx.updated_at,
                    llm_params: Some(ctx.llm_params),
                }
            }),
        )
    }

    /// Partially update an L2 context.
    pub fn update_l2(&mut self, id: &str, fields: UpdateL2Fields) -> Result<TopicDetail> {
        crate::query::l2_ops::update_l2(
            &mut self.mmap,
            &mut self.header,
            &self.btree,
            &mut self.sparse_index,
            id,
            fields,
        )
    }

    /// Delete an L2 context and all associated data.
    pub fn delete_l2(&mut self, id: &str) -> Result<()> {
        crate::query::l2_ops::delete_l2(
            &mut self.mmap,
            &mut self.header,
            &mut self.btree,
            &mut self.l1_reverse_index,
            &mut self.sparse_index,
            id,
        )
    }

    /// Delete a range of L4 archives (turns) from an L2 context.
    pub fn delete_turn(&mut self, id: &str, range: std::ops::Range<usize>) -> Result<()> {
        crate::query::l2_ops::delete_turn(
            &mut self.mmap,
            &mut self.header,
            &mut self.btree,
            &mut self.sparse_index,
            id,
            range,
        )
    }

    /// Merge multiple L2 contexts into a primary context.
    pub fn merge_l2(&mut self, primary_id: &str, merge_ids: Vec<String>) -> Result<MergeResult> {
        crate::query::l2_ops::merge_l2(
            &mut self.mmap,
            &mut self.header,
            &mut self.btree,
            &mut self.sparse_index,
            primary_id,
            merge_ids,
        )
    }
}
