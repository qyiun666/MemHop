//! Perceive — store new perception into the Brain.
//!
//! Extracted from brain.rs in v0.12.2.

use std::collections::HashMap;

use half::f16;

use crate::brain::{generate_id, now_millis, Brain};
use crate::engram::{
    AssociationKind, DialogueTurn, Engram, EngramKind, PlanLevel, PlanNode,
    PlanState, Protection, StyleCompact, ToneMeta, TurnSource,
};
use crate::error::Result;
use crate::scene_gating::SceneGate;
use crate::types::{PerceptionInput, PerceptionOutput};

/// Store a new perception into the Brain.
///
/// Creates engrams (one per segment), updates PlanGate, PlanIndex, ActiveContexts,
/// builds temporal graph edges, and persists PlanNode + DialogueTurn.
pub(crate) fn perceive(brain: &mut Brain, input: PerceptionInput) -> Result<PerceptionOutput> {
    let now = now_millis();
    let id = generate_id();
    // v0.9.1: Auto-generate turn_id if empty
    let turn_id = if input.turn_id.is_empty() {
        format!("turn_{}_{}", now, input.turn_index)
    } else {
        input.turn_id.clone()
    };

    brain.emotional_ctx
        .update(input.emotional_state.valence, input.emotional_state.arousal);

    // ── v0.8.0: Plan-gating — PlanGate boundary detection & decision ──

    // 1. Convert embedding to f32 for PlanGate
    let query_f32: Vec<f32> = input.vector.iter().map(|x| x.to_f32()).collect();

    // 2. Get plan centroid from PlanIndex (may be None for new brain)
    let plan_centroid: Option<Vec<f32>> = {
        let idx = brain.plan_index.borrow();
        idx.active_plan_id
            .as_ref()
            .and_then(|pid| {
                idx.centroids
                    .get(pid)
                    .map(|c| c.iter().map(|x: &half::f16| x.to_f32()).collect())
            })
    };

    // 3. Time gap since last perceive (minutes)
    let time_gap_minutes = if brain.last_perceive_at > 0 {
        ((now - brain.last_perceive_at).max(0) as f64) / 60_000.0
    } else {
        0.0
    };
    brain.last_perceive_at = now;

    // 4. Extract user tone from text (rule-based, no LLM)
    let current_tone = crate::tone_extractor::extract_tone(&input.content);

    // 5. Compute boundary score
    let boundary = brain.plan_gate.boundary_score(
        &query_f32,
        &current_tone,
        &input.attention_anchors,
        crate::plan_gate::PlanContext {
            centroid: plan_centroid.as_deref(),
            avg_tone: None,
            anchors: &[],
        },
        time_gap_minutes,
    );

    // ── v0.12.0: 保存旧 plan_id 用于 compress ──
    let old_plan_id = brain.plan_index.borrow().active_plan_id.clone().unwrap_or_default();

    // 6. Match to plan
    let matched_plan = brain.plan_gate.match_to_plan(
        input.plan_id.as_deref(),
        &brain.plan_index.borrow(),
        &query_f32,
        boundary,
    );

    // 7. Determine plan_id: explicit match → use it; otherwise anonymous
    let plan_id = matched_plan.unwrap_or_else(|| format!("plan_{}", now));

    // 8. Decide plan hint (accumulates boundary scores over rounds)
    let plan_hint = brain.plan_gate.decide(boundary, now);

    // 9. Plan name from explicit input or default
    let plan_name = input
        .plan_id
        .as_deref()
        .unwrap_or("Unnamed Plan")
        .to_string();

    // ── v0.12.0: Full 模式下边界检测 → 自动压缩 ──
    if brain.phase == crate::context::Phase::Full && plan_hint == crate::engram::PlanHint::NewTopicLikely
        && !old_plan_id.is_empty() && old_plan_id != plan_id {
        let _ = brain.compress_plan(&old_plan_id);
    }

    // ── v0.8.0: Populate PlanIndex ──
    {
        let mut pi = brain.plan_index.borrow_mut();
        pi.add_engram(&plan_id, &id);
        pi.update_centroid(&plan_id, &query_f32);
        if pi.active_plan_id.is_none() {
            pi.active_plan_id = Some(plan_id.clone());
        }
    }

    // ── v0.12.0: Phase 判断 ──
    let phase = if brain.growth.total_perceptions < brain.config.warmup_rounds as u64 {
        crate::context::Phase::Warmup
    } else if brain.growth.total_perceptions < (brain.config.warmup_rounds as u64) * 2 {
        crate::context::Phase::Early
    } else {
        crate::context::Phase::Full
    };
    brain.phase = phase;

    // ── v0.12.0: 活跃上下文匹配（Warmup 不做） ──
    let mut matched_ctx_id: Option<String> = None;
    let mut matched_tree_id: Option<String> = input.tree_id.clone(); // v0.12.1: from input first, context overrides
    if brain.phase != crate::context::Phase::Warmup {
        if let Some(ctx) = brain.active_contexts.match_context(&query_f32, now) {
            matched_ctx_id = Some(ctx.id.clone());
            // v0.12.1: context's tree_id overrides input's tree_id
            if ctx.tree_id.is_some() {
                matched_tree_id = ctx.tree_id.clone();
            }
        } else {
            // 没有匹配到上下文 → 使用 PlanGate 的结果创建新上下文
            // v0.12.1: pass input.tree_id to new context if available
            brain.active_contexts.create(input.tree_id.clone(), plan_id.clone(), input.vector.clone(), now);
        }
        // 淘汰过期的上下文
        brain.active_contexts.evict_stale();
    }

    // v0.12.1: 从匹配上下文的 tree_id 查找 Tree，构建 tree_ref
    let engram_tree_ref: Option<crate::tree::TreeRef> = if let Some(ref tid) = matched_tree_id {
        brain.get_tree(tid).ok().flatten().map(|tree| crate::tree::TreeRef {
            tree_id: tree.id,
            tree_name: tree.name,
            tree_domain: tree.domain,
        })
    } else {
        None
    };

    // Save data for DialogueTurn before input is consumed
    let saved_content = input.content.clone();
    let saved_vector = input.vector.clone();
    let saved_agent_response = input.agent_response.clone();
    let saved_dialogue_timestamp = input.dialogue_timestamp;

    // ── v0.9.1: Long text segmentation ──
    const MAX_SEGMENT_CHARS: usize = 5000;
    let segments: Vec<String> = if saved_content.len() > MAX_SEGMENT_CHARS {
        split_text_at_boundaries(&saved_content, MAX_SEGMENT_CHARS)
    } else {
        vec![saved_content.clone()]
    };
    let segment_count = segments.len() as u32;
    let text_was_split = segments.len() > 1;

    // ── Create engrams (one per segment) ──
    let mut engram_ids: Vec<String> = Vec::new();
    for (seg_idx, segment_text) in segments.iter().enumerate() {
        let seg_id = if seg_idx == 0 {
            id.clone()
        } else {
            generate_id()
        };

        // Re-encode per segment if text was split, else use original vector
        let seg_vector = if text_was_split {
            brain.encode_text(segment_text)
        } else {
            input.vector.clone()
        };

        let engram = Engram {
            id: seg_id.clone(),
            text: segment_text.clone(),
            summary: None,
            vector: seg_vector,
            keywords: Vec::new(),
            content_type: None,
            valence: input.emotional_state.valence,
            arousal: input.emotional_state.arousal,
            vitality: 1.0,
            protection: Protection::Normal,
            created_at: now,
            last_activated: now,
            activation_count: 1,
            kind: EngramKind::Episode,
            meta: HashMap::new(),
            is_archived: false,
            is_dormant: false,
            turn_id: Some(turn_id.clone()),
            tree_path: None,
            source_path: None,
            source_textunit: None,
            turn_ids: Vec::new(),
            context_id: matched_ctx_id.clone(),
            tree_ref: engram_tree_ref.clone(),
        };

        brain.cortex.push(engram.clone(), &input.session_id);
        // v0.11.0: Use store_engram for unified index writes
        crate::store::store_engram(brain, engram)?;
        engram_ids.push(seg_id);
    }

    // 建立时间边（与 Hippocampus 中最近 3 条），只连接最后一个 segment
    let last_seg_id = engram_ids.last().cloned().unwrap_or_default();
    let recent_entries = brain
        .hippocampus
        .batch_entries(&brain.storage, brain.hippocampus.len().saturating_sub(4), 3)?;
    for (recent_id, _) in &recent_entries {
        if recent_id.as_str() != last_seg_id.as_str() {
            brain.graph.add_edge(
                &brain.storage, &last_seg_id, recent_id, 0.5, AssociationKind::Temporal, now,
            )?;
            brain.graph.add_edge(
                &brain.storage, recent_id, &last_seg_id, 0.5, AssociationKind::Temporal, now,
            )?;
        }
    }

    brain.growth.total_perceptions += 1;

    // 记录到 Anchor 索引（所有 segment 都关联）
    if !input.attention_anchors.is_empty() {
        for seg_id in &engram_ids {
            let _ = SceneGate::add_to_anchors(&brain.storage, seg_id, &input.attention_anchors);
        }
    }

    // Parse turn source
    let turn_source = input.source.as_deref()
        .map(parse_turn_source)
        .unwrap_or(TurnSource::User);

    // ── v0.8.0: Persist PlanNode & DialogueTurn ──
    {
        // Persist PlanNode to LMDB (upsert pattern)
        let plan = PlanNode {
            id: plan_id.clone(),
            parent_id: None,
            name: plan_name.clone(),
            level: PlanLevel::Plan,
            centroid_vector: query_f32.iter().map(|&x| f16::from_f32(x)).collect(),
            dialogue_count: 1,
            compressed_summary: None,
            state: PlanState::Active,
            created_at: now,
            completed_at: None,
            meta: HashMap::new(),
        };
        let mut txn = brain.storage
            .begin_write()
            .map_err(|e| crate::error::MemHopError::Storage(e.to_string()))?;
        brain.storage
            .put_plan(&mut txn, &plan)
            .map_err(|e| crate::error::MemHopError::Storage(e.to_string()))?;

        // v0.11.0: Always create DialogueTurn for session-level aggregation,
        // regardless of whether agent_response is present.
        let turn = DialogueTurn {
            id: turn_id.clone(),
            plan_id: plan_id.clone(),
            user_input: saved_content.clone(),
            agent_response: saved_agent_response.clone().unwrap_or_default(),
            user_tone: current_tone,
            agent_tone: ToneMeta {
                valence: 0.0,
                arousal: 0.0,
                tone_tags: vec![],
                filler_ratio: 0.0,
                sentence_style: StyleCompact {
                    avg_sentence_len: 0.0,
                    question_ratio: 0.0,
                    exclamation_count: 0,
                },
            },
            timestamp: saved_dialogue_timestamp.unwrap_or(now),
            vector: saved_vector.clone(),
            session_id: input.session_id.clone(),
            turn_index: input.turn_index,
            segment_count,
            source: turn_source,
            topic_label: input.topic_label.clone(),
        };
        brain.storage
            .put_dialogue(&mut txn, &turn)
            .map_err(|e| crate::error::MemHopError::Storage(e.to_string()))?;

        txn.commit()
            .map_err(|e| crate::error::MemHopError::Storage(e.to_string()))?;
    }

    let output = PerceptionOutput {
        engram_id: id,
        current_plan_id: plan_id,
        plan_hint,
        plan_name,
        context_id: matched_ctx_id,
        phase: format!("{}", brain.phase),
    };

    if let Err(e) = crate::organize::organize(brain, &input, &output) {
        eprintln!("[organize] {}", e);
    }

    Ok(output)
}

/// Split text at sentence boundaries near `max_chars` chunks.
fn split_text_at_boundaries(text: &str, max_chars: usize) -> Vec<String> {
    let mut segments: Vec<String> = Vec::new();
    let mut start = 0;
    let chars: Vec<char> = text.chars().collect();
    let len = chars.len();

    while start < len {
        if start + max_chars >= len {
            segments.push(chars[start..].iter().collect());
            break;
        }
        // Find the last sentence boundary at or before start + max_chars
        let end = chars[start..start + max_chars]
            .iter()
            .rposition(|&c| c == '.' || c == '!' || c == '?' || c == '\n')
            .map(|pos| start + pos + 1)
            .unwrap_or(start + max_chars);

        // Ensure minimum segment size (500 chars), merge if too short
        if !segments.is_empty() && end - start < 500 {
            // Merge with previous segment
            let last = segments.last_mut().unwrap();
            last.extend(chars[start..end].iter());
        } else {
            segments.push(chars[start..end].iter().collect());
        }
        start = end;
    }

    segments
}

/// Parse a string into a TurnSource, defaulting to User on unrecognized values.
fn parse_turn_source(s: &str) -> TurnSource {
    match s.to_lowercase().as_str() {
        "agent" => TurnSource::Agent,
        "system" => TurnSource::System,
        "external" => TurnSource::External,
        _ => TurnSource::User,
    }
}
