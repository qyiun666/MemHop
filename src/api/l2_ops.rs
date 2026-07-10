// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! API-5 + API-6: L2 Context CRUD and merge operations.

use crate::query::types::{
    MergeResult, SceneTreeResult, TopicDetail, TopicListQuery, TopicListResult, UpdateL2Fields,
};
use crate::shared::common::parse_id_to_hash;
use crate::{MemHop, Result};

impl MemHop {
    /// List L2 contexts with pagination and filtering.
    pub fn list_l2(&self, query: TopicListQuery) -> Result<TopicListResult> {
        crate::query::l2_ops::list_l2(&self.engine, query)
    }

    /// Get a single L2 context by ID.
    pub fn get_l2(&self, id: &str) -> Result<Option<TopicDetail>> {
        Ok(crate::query::l2_ops::get_l2(&self.engine, id)?
            .map(|ctx| crate::query::l2_ops::to_topic_detail(&ctx)))
    }

    /// Partially update an L2 context.
    pub fn update_l2(&mut self, id: &str, fields: UpdateL2Fields) -> Result<TopicDetail> {
        crate::query::l2_ops::update_l2(&mut self.engine, &mut self.sparse_index, id, fields)
    }

    /// Delete an L2 context and all associated data.
    pub fn delete_l2(&mut self, id: &str) -> Result<()> {
        crate::query::l2_ops::delete_l2(
            &mut self.engine,
            &mut self.l1_reverse_index,
            &mut self.sparse_index,
            id,
        )
    }

    /// Delete a range of L4 archives (turns) from an L2 context.
    pub fn delete_turn(&mut self, id: &str, range: std::ops::Range<usize>) -> Result<()> {
        crate::query::l2_ops::delete_turn(&mut self.engine, &mut self.sparse_index, id, range)
    }

    /// Merge multiple L2 contexts into a primary context.
    pub fn merge_l2(&mut self, primary_id: &str, merge_ids: Vec<String>) -> Result<MergeResult> {
        crate::query::l2_ops::merge_l2(
            &mut self.engine,
            &mut self.sparse_index,
            primary_id,
            merge_ids,
        )
    }

    /// List the full scene tree for a given scene ID.
    pub fn list_scene_tree(&self, scene_id: &str) -> Result<SceneTreeResult> {
        let scene_hash = parse_id_to_hash(scene_id);
        crate::query::l2_ops::list_scene_tree(&self.engine, &self.l2_meta, scene_hash)
    }
}
