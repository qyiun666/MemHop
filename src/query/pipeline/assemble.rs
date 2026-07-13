// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Result assembly: convert raw retrieval data into the public `SearchResult` type.

use crate::layers::context::ContextSlot;
use crate::query::types::*;
use crate::shared::common::format_hash;

fn convert_contexts(contexts: &[(ContextSlot, f32)]) -> Vec<ContextResult> {
    contexts
        .iter()
        .map(|(c, score)| ContextResult {
            id: format_hash(c.id),
            parent_id: c.parent_id.map(format_hash),
            depth: c.depth,
            scene_id: format_hash(c.scene_id),
            user_keywords: c.user_keywords.clone(),
            user_timestamp: c.user_timestamp,
            agent_keywords: c.agent_keywords.clone(),
            agent_timestamp: c.agent_timestamp,
            fused_keywords: c.fused_keywords.clone(),
            fused_summary: c.fused_summary.clone(),
            children_ids: c.children_ids.iter().map(|h| format_hash(*h)).collect(),
            l4_refs: c
                .user_l4_refs
                .iter()
                .chain(c.agent_l4_refs.iter())
                .map(|h| format_hash(*h))
                .collect(),
            l3_refs: c
                .user_l3_refs
                .iter()
                .chain(c.agent_l3_refs.iter())
                .map(|h| format_hash(*h))
                .collect(),
            retrieval_score: *score,
        })
        .collect()
}

/// Assemble the final `SearchResult` from all pipeline outputs.
///
/// # Arguments
/// * `profile` - L0 agent profile (optional)
/// * `all_contexts` - Combined primary + associated contexts (for L3 ID collection)
/// * `primary_contexts` - Primary matched contexts (goes into `contexts` field)
/// * `associated_contexts` - L1-associated contexts (goes into `associated_contexts` field)
/// * `l1_previews` - L1 ContextNode previews for matched contexts
pub fn assemble_search_result(
    profile: Option<ProfileResult>,
    all_contexts: &[(ContextSlot, f32)],
    primary_contexts: &[(ContextSlot, f32)],
    associated_contexts: &[(ContextSlot, f32)],
    l1_previews: Vec<L1Preview>,
) -> SearchResult {
    // Collect L3 IDs referenced by all returned contexts.
    let mut l3_ids: Vec<String> = all_contexts
        .iter()
        .flat_map(|(ctx, _)| {
            ctx.user_l3_refs
                .iter()
                .chain(ctx.agent_l3_refs.iter())
                .map(|h| format_hash(*h))
        })
        .collect();
    l3_ids.sort();
    l3_ids.dedup();

    SearchResult {
        profile,
        contexts: convert_contexts(primary_contexts),
        associated_contexts: convert_contexts(associated_contexts),
        l3_ids,
        l1_previews,
    }
}
