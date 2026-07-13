// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! API-5 + API-6: L2 Context CRUD and merge operations.

use crate::query::types::{
    ContextResult, MergeResult, SceneTreeResult, TopicDetail, TopicListQuery, TopicListResult,
    UpdateL2Fields,
};
use crate::shared::common::{format_hash, parse_id_to_hash};
use crate::{MemHop, MemHopError, Result};

impl MemHop {
    /// List L2 contexts with pagination and filtering.
    pub fn list_contexts(&self, scene_id: &str) -> Result<Vec<ContextResult>> {
        use crate::storage::record::REC_L2_TOPIC;
        let scene_hash = parse_id_to_hash(scene_id);
        let mut results = Vec::new();

        for (&id_hash, _) in self.engine.iter_index() {
            let Ok(Some((rt, data))) = self.engine.read_record(id_hash) else {
                continue;
            };
            if rt != REC_L2_TOPIC {
                continue;
            }
            let Ok(ctx) = bincode::deserialize::<crate::layers::context::ContextSlot>(data) else {
                continue;
            };
            if ctx.scene_id != scene_hash {
                continue;
            }
            results.push(slot_to_context_result(&ctx, id_hash, 0.0));
        }

        results.sort_by_key(|b| std::cmp::Reverse(b.user_timestamp));
        Ok(results)
    }

    /// Get a single L2 context by ID.
    pub fn get_context(&self, id: &str) -> Result<Option<ContextResult>> {
        use crate::storage::record::REC_L2_TOPIC;
        let id_hash = parse_id_to_hash(id);
        match self.engine.read_record(id_hash) {
            Ok(Some((rt, data))) if rt == REC_L2_TOPIC => {
                match bincode::deserialize::<crate::layers::context::ContextSlot>(data) {
                    Ok(ctx) => Ok(Some(slot_to_context_result(&ctx, id_hash, 0.0))),
                    Err(_) => Ok(None),
                }
            }
            _ => Ok(None),
        }
    }

    /// List the full scene tree for a given scene ID.
    pub fn list_context_tree(&self, scene_id: &str) -> Result<Vec<ContextResult>> {
        let scene_hash = parse_id_to_hash(scene_id);
        let tree = crate::query::l2_ops::list_scene_tree(&self.engine, &self.l2_meta, scene_hash)?;
        let results: Vec<ContextResult> = tree
            .nodes
            .into_iter()
            .map(|ctx| ContextResult {
                id: ctx.id.clone(),
                parent_id: ctx.parent_id.clone(),
                depth: ctx.depth,
                scene_id: ctx.scene_id.clone(),
                user_keywords: ctx.user_keywords.clone(),
                user_timestamp: ctx.user_timestamp,
                agent_keywords: ctx.agent_keywords.clone(),
                agent_timestamp: ctx.agent_timestamp,
                fused_keywords: ctx.fused_keywords.clone(),
                fused_summary: ctx.fused_summary.clone(),
                children_ids: ctx.children_ids.clone(),
                l4_refs: {
                    let mut refs = ctx.user_l4_refs.clone();
                    refs.extend(ctx.agent_l4_refs.clone());
                    refs.sort();
                    refs.dedup();
                    refs
                },
                l3_refs: {
                    let mut refs = ctx.user_l3_refs.clone();
                    refs.extend(ctx.agent_l3_refs.clone());
                    refs.sort();
                    refs.dedup();
                    refs
                },
                retrieval_score: 0.0,
            })
            .collect();
        Ok(results)
    }

    /// Partially update an L2 context and return ContextResult.
    pub fn update_context(&mut self, id: &str, fields: UpdateL2Fields) -> Result<ContextResult> {
        let detail =
            crate::query::l2_ops::update_l2(&mut self.engine, &mut self.sparse_index, id, fields)?;
        Ok(to_context_result(&detail, 0.0))
    }

    /// Delete an L2 context.
    pub fn delete_context(&mut self, id: &str) -> Result<()> {
        crate::query::l2_ops::delete_l2(
            &mut self.engine,
            &mut self.l1_reverse_index,
            &mut self.sparse_index,
            id,
        )
    }

    /// Merge multiple L2 contexts into one and return ContextResult.
    pub fn merge_contexts(&mut self, ids: Vec<String>) -> Result<ContextResult> {
        if ids.is_empty() {
            return Err(crate::MemHopError::InvalidQuery(
                "merge_contexts requires at least one ID".into(),
            ));
        }
        let primary_id = &ids[0];
        let merge_ids: Vec<String> = ids[1..].to_vec();
        let merge_result = crate::query::l2_ops::merge_l2(
            &mut self.engine,
            &mut self.sparse_index,
            primary_id,
            merge_ids,
        )?;
        // Fetch the merged primary context by ID and return as ContextResult
        self.get_context(&merge_result.primary_id)?.ok_or_else(|| {
            MemHopError::Serialization(format!(
                "Merged context {} not found after merge",
                merge_result.primary_id
            ))
        })
    }

    /// Delete multiple turns by their IDs.
    pub fn delete_turns(&mut self, ids: Vec<String>) -> Result<()> {
        for id in &ids {
            crate::query::l2_ops::delete_l2(
                &mut self.engine,
                &mut self.l1_reverse_index,
                &mut self.sparse_index,
                id,
            )?;
        }
        Ok(())
    }

    /// Legacy: list L2 contexts with pagination.
    pub fn list_l2(&self, query: TopicListQuery) -> Result<TopicListResult> {
        crate::query::l2_ops::list_l2(&self.engine, query)
    }

    /// Legacy: get a single L2 context by ID as TopicDetail.
    pub fn get_l2(&self, id: &str) -> Result<Option<TopicDetail>> {
        Ok(crate::query::l2_ops::get_l2(&self.engine, id)?
            .map(|ctx| crate::query::l2_ops::to_topic_detail(&ctx)))
    }

    /// Legacy: partially update an L2 context.
    pub fn update_l2(&mut self, id: &str, fields: UpdateL2Fields) -> Result<TopicDetail> {
        crate::query::l2_ops::update_l2(&mut self.engine, &mut self.sparse_index, id, fields)
    }

    /// Legacy: delete an L2 context.
    pub fn delete_l2(&mut self, id: &str) -> Result<()> {
        crate::query::l2_ops::delete_l2(
            &mut self.engine,
            &mut self.l1_reverse_index,
            &mut self.sparse_index,
            id,
        )
    }

    /// Legacy: delete a range of L4 archives from an L2 context.
    pub fn delete_turn(&mut self, id: &str, range: std::ops::Range<usize>) -> Result<()> {
        crate::query::l2_ops::delete_turn(&mut self.engine, &mut self.sparse_index, id, range)
    }

    /// Legacy: merge multiple L2 contexts.
    pub fn merge_l2(&mut self, primary_id: &str, merge_ids: Vec<String>) -> Result<MergeResult> {
        crate::query::l2_ops::merge_l2(
            &mut self.engine,
            &mut self.sparse_index,
            primary_id,
            merge_ids,
        )
    }

    /// Legacy: list the full scene tree.
    pub fn list_scene_tree(&self, scene_id: &str) -> Result<SceneTreeResult> {
        let scene_hash = parse_id_to_hash(scene_id);
        crate::query::l2_ops::list_scene_tree(&self.engine, &self.l2_meta, scene_hash)
    }
}

/// Convert a TopicDetail to a ContextResult
fn to_context_result(detail: &TopicDetail, score: f32) -> ContextResult {
    ContextResult {
        id: detail.id.clone(),
        parent_id: detail.parent_id.clone(),
        depth: detail.depth,
        scene_id: detail.scene_id.clone(),
        user_keywords: detail.user_keywords.clone(),
        user_timestamp: detail.user_timestamp,
        agent_keywords: detail.agent_keywords.clone(),
        agent_timestamp: detail.agent_timestamp,
        fused_keywords: detail.fused_keywords.clone(),
        fused_summary: detail.fused_summary.clone(),
        children_ids: detail.children_ids.clone(),
        l4_refs: {
            let mut refs = detail.user_l4_refs.clone();
            refs.extend(detail.agent_l4_refs.clone());
            refs.sort();
            refs.dedup();
            refs
        },
        l3_refs: {
            let mut refs = detail.user_l3_refs.clone();
            refs.extend(detail.agent_l3_refs.clone());
            refs.sort();
            refs.dedup();
            refs
        },
        retrieval_score: score,
    }
}

/// Convert a TopicDetail to a ContextResult using the TopicDetail directly (with id)
#[allow(dead_code)]
fn to_context_result_from_detail(detail: &TopicDetail, score: f32) -> ContextResult {
    to_context_result(detail, score)
}

/// Convert a ContextSlot to a ContextResult (pub(crate) for auto_create use)
use crate::layers::context::ContextSlot;
pub(crate) fn slot_to_context_result(ctx: &ContextSlot, id_hash: u64, score: f32) -> ContextResult {
    let id = format_hash(id_hash);
    ContextResult {
        id,
        parent_id: ctx.parent_id.map(format_hash),
        depth: ctx.depth,
        scene_id: format_hash(ctx.scene_id),
        user_keywords: ctx.user_keywords.clone(),
        user_timestamp: ctx.user_timestamp,
        agent_keywords: ctx.agent_keywords.clone(),
        agent_timestamp: ctx.agent_timestamp,
        fused_keywords: ctx.fused_keywords.clone(),
        fused_summary: ctx.fused_summary.clone(),
        children_ids: ctx.children_ids.iter().map(|&h| format_hash(h)).collect(),
        l4_refs: {
            let mut refs: Vec<String> = ctx
                .user_l4_refs
                .iter()
                .chain(ctx.agent_l4_refs.iter())
                .map(|h| format_hash(*h))
                .collect();
            refs.sort();
            refs.dedup();
            refs
        },
        l3_refs: {
            let mut refs: Vec<String> = ctx
                .user_l3_refs
                .iter()
                .chain(ctx.agent_l3_refs.iter())
                .map(|h| format_hash(*h))
                .collect();
            refs.sort();
            refs.dedup();
            refs
        },
        retrieval_score: score,
    }
}
