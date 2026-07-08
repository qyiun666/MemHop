// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! API-5 + API-6: L2 Context CRUD and merge operations.

use crate::query::types::{
    MergeNodesRequest, MergeNodesResult, MergeResult, SceneTreeResult, TopicDetail, TopicListQuery,
    TopicListResult, UpdateL2Fields,
};
use crate::shared::common::parse_id_to_hash;
use crate::{MemHop, Result};

impl MemHop {
    /// List L2 contexts with pagination and filtering.
    pub fn list_l2(&self, query: TopicListQuery) -> Result<TopicListResult> {
        crate::query::l2_ops::list_l2(&self.mmap, &self.header, &self.btree, query)
    }

    /// Get a single L2 context by ID.
    pub fn get_l2(&self, id: &str) -> Result<Option<TopicDetail>> {
        Ok(crate::query::l2_ops::get_l2(&self.mmap, &self.btree, id)?
            .map(|ctx| crate::query::l2_ops::to_topic_detail(&ctx)))
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

    /// List the full scene tree for a given scene ID.
    pub fn list_scene_tree(&self, scene_id: &str) -> Result<SceneTreeResult> {
        let scene_hash = parse_id_to_hash(scene_id);
        crate::query::l2_ops::list_scene_tree(
            &self.mmap[..],
            &self.btree,
            &self.l2_meta,
            scene_hash,
        )
    }

    /// Merge secondary scenes into a main scene.
    ///
    /// All nodes from `secondary_scene_ids` have their `scene_id` changed to
    /// `main_scene_id`.  No other metadata is modified — pure scene reassignment.
    /// Dream pipeline handles compression later.
    pub fn merge_nodes(&mut self, request: MergeNodesRequest) -> Result<MergeNodesResult> {
        let main_hash = parse_id_to_hash(&request.main_scene_id);
        let secondary_hashes: Vec<u64> = request
            .secondary_scene_ids
            .iter()
            .map(|id| parse_id_to_hash(id))
            .collect();

        crate::query::l2_ops::merge_nodes(
            &mut self.mmap,
            &mut self.header,
            &mut self.btree,
            &mut self.sparse_index,
            &mut self.l2_meta,
            main_hash,
            &secondary_hashes,
            &mut self.file,
        )
    }

    /// Create a scene.  Idempotent — returns scene_id of existing scene if name matches.
    pub fn create_scene(&mut self, name: &str) -> Result<u64> {
        crate::query::l2_ops::create_scene(
            &mut self.mmap,
            &mut self.header,
            &mut self.btree,
            &mut self.file,
            name,
        )
    }

    /// Read a scene by its hex-formatted id.
    pub fn get_scene(&self, id: &str) -> Result<Option<(u64, String)>> {
        let scene_id = parse_id_to_hash(id);
        crate::query::l2_ops::get_scene(&self.mmap, &self.btree, scene_id)
            .map(|opt| opt.map(|s| (s.scene_id, s.scene_name)))
    }

    /// List all scenes as (scene_id, scene_name) pairs.
    pub fn list_scenes(&self) -> Result<Vec<(u64, String)>> {
        crate::query::l2_ops::list_scenes(&self.mmap, &self.header)
    }
}
