//! Reflect — create a Reflection type Engram.
//!
//! v0.12.2: Extracted from `Brain::reflect()` into its own module.

use std::collections::HashMap;

use half::f16;

use crate::brain::{generate_id, now_millis, Brain};
use crate::engram::{AssociationKind, Engram, EngramKind, Protection};
use crate::error::Result;
use crate::types::ReflectionInput;

/// 创建 Reflection 类型 Engram。
pub(crate) fn reflect(brain: &mut Brain, input: ReflectionInput) -> Result<String> {
    let now = now_millis();
    let id = generate_id();
    let kind_name = input.kind.to_string();

    let mut meta = HashMap::new();
    meta.insert(
        "reflection_kind".to_string(),
        serde_json::Value::String(kind_name.clone()),
    );
    meta.insert(
        "anchored_to".to_string(),
        serde_json::Value::Array(
            input
                .anchored_to
                .iter()
                .map(|s| serde_json::Value::String(s.clone()))
                .collect(),
        ),
    );

    let engram = Engram {
        id: id.clone(),
        text: input.content,
        summary: None,
        vector: vec![f16::from_f32(0.0); crate::engram::VECTOR_DIM],
        keywords: vec![kind_name],
        content_type: Some("reflection".to_string()),
        valence: input.emotional_state.valence,
        arousal: input.emotional_state.arousal,
        vitality: 0.9,
        protection: Protection::Normal,
        created_at: now,
        last_activated: now,
        activation_count: 1,
        kind: EngramKind::Reflection,
        meta,
        is_archived: false,
        is_dormant: false,
        turn_id: None,
        tree_path: None,
        source_path: None,
        source_textunit: None,
        turn_ids: Vec::new(),
        context_id: None,
        tree_ref: None,
    };

    brain.hippocampus.store(&brain.storage, &engram)?;

    for anchor_id in &input.anchored_to {
        brain
            .graph
            .add_edge(&brain.storage, &id, anchor_id, 0.7, AssociationKind::Manual, now)?;
        brain
            .graph
            .add_edge(&brain.storage, anchor_id, &id, 0.7, AssociationKind::Manual, now)?;
    }

    brain.growth.total_reflections += 1;
    brain.growth.total_engrams_created += 1;

    Ok(id)
}
