// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Scene aggregation: group scored topics by scene_id, compute aggregate scores,
//! and select the best-matching scene for search result presentation.

use crate::layers::context::{SceneSlot, TopicSlot};
use crate::storage::record::REC_L2_SCENE;
use crate::storage::StorageEngine;
use std::collections::HashMap;

/// Scene score aggregation result (internal use).
pub(crate) struct SceneScore {
    pub scene_id: u64,
    pub scene_title: String,
    pub score: f32,
    pub topic_indices: Vec<usize>,
}

/// Aggregate scored topics by scene_id.
///
/// For each unique scene_id, sums the scores of all topics in that scene.
/// scene_id=0 (unassigned topics) is treated as a standalone scene with empty title.
/// Scenes are returned sorted by total score descending.
pub(crate) fn aggregate_scene_scores(
    topics: &[(TopicSlot, f32)],
    engine: &StorageEngine,
) -> Vec<SceneScore> {
    let mut scene_agg: HashMap<u64, (f32, Vec<usize>)> = HashMap::new();

    for (i, (ctx, score)) in topics.iter().enumerate() {
        let sid = ctx.scene_id;
        let entry = scene_agg.entry(sid).or_insert((0.0, Vec::new()));
        entry.0 += score;
        entry.1.push(i);
    }

    let mut result: Vec<SceneScore> = scene_agg
        .into_iter()
        .map(|(scene_id, (score, topic_indices))| {
            let scene_title = if scene_id == 0 {
                String::new()
            } else {
                match engine.read_record(scene_id) {
                    Ok(Some((_rt, data))) => {
                        bincode::deserialize::<SceneSlot>(data)
                            .ok()
                            .map(|s| s.scene_name)
                            .unwrap_or_default()
                    }
                    _ => String::new(),
                }
            };
            SceneScore {
                scene_id,
                scene_title,
                score,
                topic_indices,
            }
        })
        .collect();

    // Sort by score descending (highest-scoring scene first)
    result.sort_by(|a, b| {
        b.score
            .partial_cmp(&a.score)
            .unwrap_or(std::cmp::Ordering::Equal)
    });

    result
}

/// Select the best-matching scene from aggregated scores.
///
/// Returns the highest-scoring scene. The returned scene's topics can then
/// be filtered to depth==1 and sorted by user_timestamp by the caller.
pub(crate) fn select_best_scene(scores: &[SceneScore]) -> Option<&SceneScore> {
    // Scores are already sorted by score descending.
    // Pick the first (highest-scoring) scene.
    scores.first()
}
