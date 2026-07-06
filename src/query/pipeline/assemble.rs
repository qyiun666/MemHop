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
            id: format_hash(c.id_hash),
            parent_id: c.parent_id.map(format_hash),
            depth: c.depth,
            title: c.title.clone(),
            summary: c.summary.clone(),
            activation_score: c.activation_score,
            turn_count: c.turn_count,
            l3_refs: c.l3_refs.iter().map(|h| format_hash(*h)).collect(),
            archive_refs: c.archive_refs.iter().map(|h| format_hash(*h)).collect(),
            llm_params: Some(LlmParams {
                temperature: c.llm_params.temperature,
                top_p: c.llm_params.top_p,
                presence_penalty: c.llm_params.presence_penalty,
                frequency_penalty: c.llm_params.frequency_penalty,
            }),
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
pub fn assemble_search_result(
    profile: Option<ProfileResult>,
    all_contexts: &[(ContextSlot, f32)],
    primary_contexts: &[(ContextSlot, f32)],
    associated_contexts: &[(ContextSlot, f32)],
) -> SearchResult {
    // Collect L3 IDs referenced by all returned contexts.
    let mut l3_ids: Vec<String> = all_contexts
        .iter()
        .flat_map(|(ctx, _)| ctx.l3_refs.iter().map(|h| format_hash(*h)))
        .collect();
    l3_ids.sort();
    l3_ids.dedup();

    SearchResult {
        profile,
        contexts: convert_contexts(primary_contexts),
        associated_contexts: convert_contexts(associated_contexts),
        l3_ids,
        l3_previews: Vec::new(),
        archive_refs: Vec::new(),
    }
}
