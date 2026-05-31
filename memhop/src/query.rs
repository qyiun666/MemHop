//! Query helpers — aggregated queries, accessors, and dialogue history.
//!
//! v0.12.2: Extracted from brain.rs.

use std::collections::HashMap;

use crate::brain::{ngram_overlap, Brain};
use crate::engram::{EmotionalContext, Engram, PlanLevel, ToneAggregate, TopicDistribution};
use crate::error::{MemHopError, Result};
use crate::personality::GrowthState;
use crate::types::{ForgetFilter, PerceptionInput, PerceptionOutput};

// ── Domain / Plan helpers ──────────────────────────────────

/// Get all domain-level plan names (deduplicated).
pub(crate) fn get_all_domains(brain: &Brain) -> Result<Vec<String>> {
    let txn = brain.storage.begin_read()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    let plans = brain.storage.get_all_plans(&txn)
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    let mut domains: Vec<String> = plans.into_iter()
        .filter(|p| p.level == PlanLevel::Domain)
        .map(|p| p.name)
        .collect();
    domains.sort();
    domains.dedup();
    Ok(domains)
}

/// Get archived dialogue turns for a plan, sorted by timestamp, with pagination.
pub(crate) fn archived_dialogue(
    brain: &Brain,
    plan_id: &str,
    offset: usize,
    limit: usize,
) -> Result<Vec<crate::engram::DialogueTurn>> {
    let txn = brain.storage.begin_read()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    let turns = brain.storage.get_dialogues_by_plan(&txn, plan_id)
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    let turns: Vec<_> = turns.into_iter().skip(offset).take(limit).collect();
    Ok(turns)
}

/// Randomly sample up to max_turns dialogue turns from a plan.
pub(crate) fn extract_dialogue_sample(
    brain: &Brain,
    plan_id: &str,
    max_turns: usize,
) -> Result<Vec<crate::engram::DialogueTurn>> {
    let txn = brain.storage.begin_read()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    let turns = brain.storage.get_dialogues_by_plan(&txn, plan_id)
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    if turns.len() <= max_turns {
        return Ok(turns);
    }
    // Simple random sampling: shuffle and take first max_turns
    use rand::seq::SliceRandom;
    let mut rng = rand::thread_rng();
    let mut sample = turns;
    sample.shuffle(&mut rng);
    sample.truncate(max_turns);
    Ok(sample)
}

/// Aggregate tone statistics over a time range.
pub(crate) fn get_tone_aggregates(
    brain: &Brain,
    start_time: i64,
    end_time: i64,
) -> Result<ToneAggregate> {
    let txn = brain.storage.begin_read()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    let all_turns = brain.storage.all_dialogues(&txn)
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    drop(txn);

    let turns: Vec<&crate::engram::DialogueTurn> = all_turns.iter()
        .filter(|t| t.timestamp >= start_time && t.timestamp <= end_time)
        .collect();

    if turns.is_empty() {
        return Ok(ToneAggregate {
            time_range_start: start_time,
            time_range_end: end_time,
            avg_valence: 0.0,
            avg_arousal: 0.0,
            valence_trend: 0.0,
            top_tone_tags: Vec::new(),
            filler_ratio_trend: 0.0,
        });
    }

    let n = turns.len() as f32;
    let sum_valence: f32 = turns.iter().map(|t| t.user_tone.valence).sum();
    let sum_arousal: f32 = turns.iter().map(|t| t.user_tone.arousal).sum();
    let avg_valence = sum_valence / n;
    let avg_arousal = sum_arousal / n;

    // Valence trend: early half vs late half
    let mid = turns.len() / 2;
    let early_val: f32 = turns[..mid].iter().map(|t| t.user_tone.valence).sum::<f32>() / mid as f32;
    let late_val: f32 = turns[mid..].iter().map(|t| t.user_tone.valence).sum::<f32>() / (turns.len() - mid) as f32;
    let valence_trend = late_val - early_val;

    // Tone tag frequency
    let mut tag_counts: HashMap<&str, u32> = HashMap::new();
    for t in &turns {
        for tag in &t.user_tone.tone_tags {
            *tag_counts.entry(tag.as_str()).or_default() += 1;
        }
    }
    let mut top_tone_tags: Vec<(String, u32)> = tag_counts.into_iter()
        .map(|(k, v)| (k.to_string(), v))
        .collect();
    top_tone_tags.sort_by_key(|b| std::cmp::Reverse(b.1));
    top_tone_tags.truncate(10);

    // Filler ratio trend
    let early_fill: f32 = turns[..mid].iter().map(|t| t.user_tone.filler_ratio).sum::<f32>() / mid as f32;
    let late_fill: f32 = turns[mid..].iter().map(|t| t.user_tone.filler_ratio).sum::<f32>() / (turns.len() - mid) as f32;
    let filler_ratio_trend = late_fill - early_fill;

    Ok(ToneAggregate {
        time_range_start: start_time,
        time_range_end: end_time,
        avg_valence,
        avg_arousal,
        valence_trend,
        top_tone_tags,
        filler_ratio_trend,
    })
}

/// Get topic distribution across all domain-level plans.
pub(crate) fn get_topic_distribution(
    brain: &Brain,
) -> Result<TopicDistribution> {
    let txn = brain.storage.begin_read()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    let plans = brain.storage.get_all_plans(&txn)
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    drop(txn);

    let mut domains: HashMap<String, crate::engram::DomainStats> = HashMap::new();
    for plan in &plans {
        if plan.level != PlanLevel::Domain {
            continue;
        }
        let entry = domains.entry(plan.name.clone()).or_insert_with(|| {
            crate::engram::DomainStats {
                plan_count: 0,
                dialogue_count: 0,
                avg_valence: 0.0,
                top_keywords: Vec::new(),
            }
        });
        entry.plan_count += 1;
        entry.dialogue_count += plan.dialogue_count;
    }

    Ok(TopicDistribution { domains })
}

/// Search chat history by n-gram overlap, with optional plan filter and pagination.
pub(crate) fn search_chat_history(
    brain: &Brain,
    query: &str,
    plan_id: Option<&str>,
    offset: usize,
    limit: usize,
) -> Result<Vec<crate::engram::DialogueTurn>> {
    let txn = brain.storage.begin_read()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    let all_turns = brain.storage.all_dialogues(&txn)
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    drop(txn);

    // Optional plan filter
    let turns: Vec<crate::engram::DialogueTurn> = match plan_id {
        Some(pid) => all_turns.into_iter().filter(|t| t.plan_id == pid).collect(),
        None => all_turns,
    };

    let query_lower = query.to_lowercase();
    let mut scored: Vec<(f32, crate::engram::DialogueTurn)> = turns.into_iter()
        .map(|t| {
            let user_score = ngram_overlap(&query_lower, &t.user_input.to_lowercase());
            let agent_score = ngram_overlap(&query_lower, &t.agent_response.to_lowercase());
            let score = user_score.max(agent_score);
            (score, t)
        })
        .collect();

    scored.sort_by(|a, b| b.0.partial_cmp(&a.0).unwrap_or(std::cmp::Ordering::Equal));
    scored.truncate(limit + offset);

    Ok(scored.into_iter().skip(offset).filter(|(s, _)| *s > 0.0).map(|(_, t)| t).collect())
}

// ── v0.9.0: Save / close ─────────────────────────────────

/// Persist HNSW index before the Brain is discarded.
pub(crate) fn close(brain: &Brain) -> Result<()> {
    brain.hnsw
        .save_to_storage(&brain.storage)
        .map_err(|e| MemHopError::Storage(e.to_string()))
}

// ── 访问器 ──────────────────────────────────────────────

pub(crate) fn cortex_len(brain: &Brain) -> usize {
    brain.cortex.len()
}
pub(crate) fn hippocampus_len(brain: &Brain) -> usize {
    brain.hippocampus.len()
}
pub(crate) fn memory_count(brain: &Brain) -> usize {
    brain.hopfield.len()
}
pub(crate) fn hopfield_is_empty(brain: &Brain) -> bool {
    brain.hopfield.is_empty()
}
pub(crate) fn hnsw_is_empty(brain: &Brain) -> bool {
    brain.hnsw.is_empty()
}
pub(crate) fn growth_state(brain: &Brain) -> &GrowthState {
    &brain.growth
}
pub(crate) fn emotional_context(brain: &Brain) -> &EmotionalContext {
    &brain.emotional_ctx
}

// ── Update ──────────────────────────────────────────────

/// Update a turn with new content (forget + perceive).
pub(crate) fn update(
    brain: &mut Brain,
    turn_id: &str,
    input: PerceptionInput,
) -> Result<PerceptionOutput> {
    crate::store::forget_batch(brain, &ForgetFilter::ByTurnId(turn_id.to_string()))?;
    // Also delete the dialogue turn for backward compatibility.
    if let Ok(mut wtxn) = brain.storage.begin_write() {
        let _ = brain.storage.delete_dialogue(&mut wtxn, turn_id);
        let _ = wtxn.commit();
    }
    brain.perceive(input)
}

// ── Schema ──────────────────────────────────────────────

/// List all schema engrams with their metadata.
pub(crate) fn list_schemas(
    brain: &Brain,
) -> Result<Vec<(Engram, crate::engram::SchemaExtra)>> {
    let rtxn = brain.storage.begin_read()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    let ids = brain.storage.all_schema_ids(&rtxn)
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    let mut results = Vec::new();
    for id in &ids {
        let engram = brain.storage.get_hippocampus(&rtxn, id)
            .map_err(|e| MemHopError::Storage(e.to_string()))?
            .ok_or_else(|| MemHopError::Storage(format!("schema engram not found: {}", id)))?;
        let extra = brain.storage.get_schema(&rtxn, id)
            .map_err(|e| MemHopError::Storage(e.to_string()))?
            .unwrap_or_default();
        results.push((engram, extra));
    }
    Ok(results)
}

// ── v0.9.1: Turn hit builder ──────────────────────────────

/// Build per-turn hit list and per-session aggregation from associated engrams.
pub(crate) fn build_turn_hits(
    brain: &Brain,
    associations: &[Engram],
    score_map: &HashMap<String, f32>,
) -> Result<(Vec<crate::types::TurnHit>, Vec<crate::types::SessionScore>)> {
    // Group engrams by turn_id
    let mut turn_groups: HashMap<String, Vec<(f32, &Engram)>> = HashMap::new();
    for engram in associations {
        if let Some(ref turn_id) = engram.turn_id {
            let score = score_map.get(&engram.id).copied().unwrap_or(0.0);
            turn_groups.entry(turn_id.clone()).or_default().push((score, engram));
        }
    }

    if turn_groups.is_empty() {
        return Ok((Vec::new(), Vec::new()));
    }

    let rtxn = brain.storage.begin_read()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    let mut hit_turns = Vec::new();
    let mut session_agg: HashMap<String, (f32, Vec<String>)> = HashMap::new();

    for (turn_id, entries) in &turn_groups {
        let (best_score, best_engram) = if let Some(best) = entries.iter()
            .max_by(|a, b| a.0.partial_cmp(&b.0).unwrap_or(std::cmp::Ordering::Equal))
        {
            best
        } else {
            continue;
        };
        let snippet = best_engram.text.chars().take(200).collect::<String>();

        if let Ok(Some(turn)) = brain.storage.get_dialogue(&rtxn, turn_id) {
            hit_turns.push(crate::types::TurnHit {
                engram_id: best_engram.id.clone(),
                turn_id: turn_id.clone(),
                session_id: turn.session_id.clone(),
                score: *best_score,
                snippet,
            });
            let entry = session_agg.entry(turn.session_id).or_default();
            entry.0 += *best_score;
            entry.1.push(turn_id.clone());
        }
    }

    hit_turns.sort_by(|a, b| b.score.partial_cmp(&a.score).unwrap_or(std::cmp::Ordering::Equal));
    let mut aggregated_sessions: Vec<crate::types::SessionScore> = session_agg
        .into_iter()
        .map(|(sid, (total, ids))| crate::types::SessionScore {
            session_id: sid,
            total_score: total,
            top_turn_ids: ids.into_iter().take(5).collect(),
        })
        .collect();
    aggregated_sessions.sort_by(|a, b| b.total_score.partial_cmp(&a.total_score).unwrap_or(std::cmp::Ordering::Equal));

    Ok((hit_turns, aggregated_sessions))
}
