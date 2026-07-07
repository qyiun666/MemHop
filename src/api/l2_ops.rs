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
        Ok(
            crate::query::l2_ops::get_l2(&self.mmap, &self.btree, id)?.map(|ctx| {
                crate::query::types::TopicDetail {
                    id: crate::shared::common::format_hash(ctx.id_hash),
                    title: ctx.title,
                    summary: ctx.summary,
                    depth: ctx.depth,
                    scene_id: ctx.scene_id,
                    children_ids: ctx.children_ids.clone(),
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

    /// List the full scene tree for a given scene ID.
    ///
    /// Returns all nodes belonging to the scene sorted by creation time,
    /// along with depth distribution and edge topology.
    pub fn list_scene_tree(&self, scene_id: &str) -> Result<SceneTreeResult> {
        let scene_hash = parse_id_to_hash(scene_id);
        crate::query::l2_ops::list_scene_tree(
            &self.mmap[..],
            &self.btree,
            &self.l2_meta,
            scene_hash,
        )
    }

    /// Manually merge multiple depth-1 nodes under a scene into a new parent node.
    ///
    /// Uses the configured LLM provider for merge summarization.
    /// If an encoder is available, a centroid vector is computed for the merged content.
    ///
    /// # Arguments
    /// * `request` - Merge request containing `node_ids` (depth-1 nodes) and `scene_id`.
    ///
    /// # Errors
    /// Returns `ConfigError` if the `llm` feature is not enabled.
    #[cfg(feature = "llm")]
    pub fn merge_nodes(&mut self, request: MergeNodesRequest) -> Result<MergeNodesResult> {
        use crate::dream::openai_compatible::OpenAICompatibleLlmProvider;

        let scene_hash = parse_id_to_hash(&request.scene_id);
        let node_hashes: Vec<u64> = request
            .node_ids
            .iter()
            .map(|id| parse_id_to_hash(id))
            .collect();

        let llm = OpenAICompatibleLlmProvider::new(self.config.llm.clone());

        #[cfg(feature = "grpc-encoder")]
        let encoder = self.encoder.as_deref();
        #[cfg(not(feature = "grpc-encoder"))]
        let encoder: Option<&(dyn crate::encoder::Encoder + Send + Sync)> = None;

        crate::query::l2_ops::merge_nodes(
            &mut self.mmap,
            &mut self.header,
            &mut self.btree,
            &mut self.sparse_index,
            &mut self.l2_meta,
            &llm,
            &node_hashes,
            scene_hash,
            &mut self.file,
            encoder,
        )
    }

    /// Manually merge multiple depth-1 nodes under a scene into a new parent node.
    ///
    /// This fallback is used when the `llm` feature is disabled.
    #[cfg(not(feature = "llm"))]
    pub fn merge_nodes(&mut self, _request: MergeNodesRequest) -> Result<MergeNodesResult> {
        Err(MemHopError::ConfigError(
            "LLM feature not enabled, cannot merge nodes".to_string(),
        ))
    }
}
