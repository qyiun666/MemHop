// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

pub(crate) mod compress_stage;
pub(crate) mod crystallize_stage;
pub(crate) mod emotion;
pub(crate) mod habit_distill_stage;
pub(crate) mod l0_form_stage;
pub(crate) mod l1_decay;
pub(crate) mod l3_distill_stage;
pub(crate) mod l6_decay;
pub mod llm;
#[cfg(feature = "llm")]
pub mod llm_preprocess;
#[cfg(feature = "llm")]
pub mod openai_compatible;
pub mod prune;

use crate::config::DecayConfig;
use crate::dream::llm::{
    ConsolidationInput, ConsolidationOutput, DreamSection, HabitAnalysis, L2Group, L2NodeData,
    L3Extraction, LlmProvider, SceneData, Section,
};
use crate::dream::prune::DreamReport;
use crate::encoder::Encoder;
use crate::index::l2_meta::L2MetaIndex;
use crate::index::sparse::SparseIndex;
use crate::layers::context::TopicSlot;
use crate::query::diagnostics::{StageReport, StageStatus};
use crate::shared::common::format_hash;
use crate::storage::record::REC_L2_TOPIC;
use crate::storage::StorageEngine;
use crate::MemHopError;
use std::collections::HashSet;

const MAX_RECENT_DIALOGUES: usize = 30;

/// Run a single dream stage, recording its result and rolling back on fatal errors.
#[allow(clippy::too_many_arguments)]
fn run_stage<F, R>(
    name: &str,
    failure_description: &str,
    f: F,
    report: &mut DreamReport,
    sparse_index: &mut SparseIndex,
    stages: &mut Vec<StageReport>,
    fatal: bool,
    rollback: R,
    start_time: std::time::Instant,
) -> Result<(), MemHopError>
where
    F: FnOnce(&mut DreamReport, &mut SparseIndex) -> Result<(String, usize), MemHopError>,
    R: FnOnce(&mut DreamReport, &mut SparseIndex),
{
    let stage_start = std::time::Instant::now();
    match f(report, sparse_index) {
        Ok((description, processed_count)) => {
            stages.push(StageReport {
                name: name.to_string(),
                status: StageStatus::Success,
                description,
                processed_count,
                duration_ms: stage_start.elapsed().as_millis() as u64,
                error: None,
            });
            Ok(())
        }
        Err(e) => {
            stages.push(StageReport {
                name: name.to_string(),
                status: StageStatus::Failed,
                description: failure_description.to_string(),
                processed_count: 0,
                duration_ms: stage_start.elapsed().as_millis() as u64,
                error: Some(e.to_string()),
            });
            if fatal {
                rollback(report, sparse_index);
                report.stages = std::mem::take(stages);
                report.duration_ms = start_time.elapsed().as_millis() as u64;
                if report.rollback_incomplete {
                    tracing::error!("Dream rollback incomplete");
                }
                return Err(e);
            }
            Ok(())
        }
    }
}

// ============================================================================
// Data collection — gather all info needed for the consolidated LLM call
// ============================================================================

fn build_consolidation_input(
    engine: &StorageEngine,
    l2_meta: &L2MetaIndex,
    target_l2_ids: &HashSet<u64>,
) -> Result<ConsolidationInput, MemHopError> {
    // Collect scenes from active L2 contexts
    let mut scene_map: std::collections::HashMap<u64, Vec<L2NodeData>> =
        std::collections::HashMap::new();

    for &id_hash in target_l2_ids {
        let (_, data) = match engine.read_record(id_hash)? {
            Some(v) => v,
            None => continue,
        };
        let ctx: TopicSlot = match bincode::deserialize(data) {
            Ok(c) => c,
            Err(_) => continue,
        };

        let meta = l2_meta.get(id_hash);
        let scene_id = meta.map(|m| m.scene_id).unwrap_or(0);
        let children_ids = meta.map(|m| m.children_ids.clone()).unwrap_or_default();

        scene_map.entry(scene_id).or_default().push(L2NodeData {
            id_hash: ctx.id,
            created_at: ctx.created_at,
            depth: ctx.depth,
            user_keywords: ctx.user_keywords.clone(),
            agent_keywords: ctx.agent_keywords.clone(),
            fused_keywords: ctx.fused_keywords.clone(),
            fused_summary: ctx.fused_summary.clone(),
            children_ids,
        });
    }

    // Sort each scene's nodes by created_at
    let mut scenes: Vec<SceneData> = scene_map
        .into_iter()
        .map(|(scene_id, mut nodes)| {
            nodes.sort_by_key(|n| n.created_at);
            SceneData { scene_id, nodes }
        })
        .collect();
    scenes.sort_by_key(|s| s.scene_id);

    // Collect recent dialogues from L4 archives
    let recent_dialogues =
        habit_distill_stage::extract_recent_dialogues_inner(engine, MAX_RECENT_DIALOGUES);

    // Collect existing L5 action chains
    let existing_chains = crystallize_stage::extract_existing_chains(engine);

    Ok(ConsolidationInput {
        scenes,
        recent_dialogues,
        existing_chains,
    })
}

// ============================================================================
// Apply LLM output to storage
// ============================================================================

fn apply_l2_groups(
    groups: &[L2Group],
    engine: &mut StorageEngine,
    sparse_index: &mut SparseIndex,
    l2_meta: &mut L2MetaIndex,
    encoder: Option<&(dyn Encoder + Send + Sync)>,
) -> Result<(u32, u32, u32, u32, u32), MemHopError> {
    compress_stage::apply_precomputed_groups(groups, engine, sparse_index, l2_meta, encoder)
}

fn apply_l3_extractions(
    engine: &mut StorageEngine,
    extractions: &[L3Extraction],
    sparse_index: &mut SparseIndex,
) -> Result<Vec<String>, MemHopError> {
    l3_distill_stage::apply_distill_extractions(extractions, engine, sparse_index)
}

fn apply_habits(
    analysis: &HabitAnalysis,
    engine: &mut StorageEngine,
) -> Result<(usize, usize, usize), MemHopError> {
    habit_distill_stage::merge_habits_into_profile(engine, analysis)
}

fn apply_crystals(
    crystals: &[crate::dream::llm::CrystalDef],
    engine: &mut StorageEngine,
) -> Result<Vec<String>, MemHopError> {
    crystallize_stage::apply_precomputed_crystals(crystals, engine)
}

// ============================================================================
// Core function — two-phase consolidated dream
// ============================================================================

/// Run the LLM consolidation in two phases (Dream-A then Dream-B), collect
/// failed sections, retry each group independently.
fn run_consolidation(
    llm: &dyn LlmProvider,
    input: &ConsolidationInput,
) -> (ConsolidationOutput, Vec<DreamSection>) {
    let mut failed = Vec::new();

    let mut output = ConsolidationOutput {
        l2_groups: Section::Empty,
        l3_extractions: Section::Empty,
        habits: Section::Empty,
        crystals: Section::Empty,
    };

    // ========================================================================
    // Phase A: L2 grouping + L3 distillation
    // ========================================================================
    let mut output_a = match llm.consolidate(input) {
        Ok(o) => o,
        Err(e) => {
            tracing::error!("consolidate LLM call failed: {}", e);
            let err_msg = format!("consolidate: {}", e);
            output.l2_groups = Section::ParseFailed(err_msg.clone());
            output.l3_extractions = Section::ParseFailed(err_msg);
            failed.extend_from_slice(&[DreamSection::L2Groups, DreamSection::L3Distill]);
            // Continue to Phase B — A and B are independent
            ConsolidationOutput {
                l2_groups: Section::Empty,
                l3_extractions: Section::Empty,
                habits: Section::Empty,
                crystals: Section::Empty,
            }
        }
    };

    // Retry failed A sections
    {
        let mut retry_a = Vec::new();
        if output_a.l2_groups.needs_retry() {
            retry_a.push(DreamSection::L2Groups);
        }
        if output_a.l3_extractions.needs_retry() {
            retry_a.push(DreamSection::L3Distill);
        }

        if !retry_a.is_empty() {
            tracing::info!("consolidate_a: phase 2 retry for {:?}", retry_a);
            match llm.retry_sections(input, &retry_a) {
                Ok(retry) => {
                    if let Section::Valid(g) = retry.l2_groups {
                        output_a.l2_groups = Section::Valid(g);
                    }
                    if let Section::Valid(e) = retry.l3_extractions {
                        output_a.l3_extractions = Section::Valid(e);
                    }
                }
                Err(e) => {
                    tracing::warn!("consolidate_a retry failed: {}", e);
                }
            }
        }
    }

    // Collect A failures that remain
    if output_a.l2_groups.needs_retry() {
        failed.push(DreamSection::L2Groups);
    }
    if output_a.l3_extractions.needs_retry() {
        failed.push(DreamSection::L3Distill);
    }

    output.l2_groups = output_a.l2_groups;
    output.l3_extractions = output_a.l3_extractions;

    // ========================================================================
    // Phase B: habit analysis + crystal generation
    // ========================================================================
    let mut output_b = match llm.consolidate(input) {
        Ok(o) => o,
        Err(e) => {
            tracing::error!("consolidate LLM call failed: {}", e);
            let err_msg = format!("consolidate: {}", e);
            output.habits = Section::ParseFailed(err_msg.clone());
            output.crystals = Section::ParseFailed(err_msg);
            failed.extend_from_slice(&[DreamSection::Habits, DreamSection::Crystals]);
            return (output, failed);
        }
    };

    // Retry failed B sections
    {
        let mut retry_b = Vec::new();
        if output_b.habits.needs_retry() {
            retry_b.push(DreamSection::Habits);
        }
        if output_b.crystals.needs_retry() {
            retry_b.push(DreamSection::Crystals);
        }

        if !retry_b.is_empty() {
            tracing::info!("consolidate_b: phase 2 retry for {:?}", retry_b);
            match llm.retry_sections(input, &retry_b) {
                Ok(retry) => {
                    if let Section::Valid(h) = retry.habits {
                        output_b.habits = Section::Valid(h);
                    }
                    if let Section::Valid(c) = retry.crystals {
                        output_b.crystals = Section::Valid(c);
                    }
                }
                Err(e) => {
                    tracing::warn!("consolidate_b retry failed: {}", e);
                }
            }
        }
    }

    // Collect B failures that remain
    if output_b.habits.needs_retry() {
        failed.push(DreamSection::Habits);
    }
    if output_b.crystals.needs_retry() {
        failed.push(DreamSection::Crystals);
    }

    output.habits = output_b.habits;
    output.crystals = output_b.crystals;

    (output, failed)
}

// ============================================================================
// Main dream pipeline
// ============================================================================

#[allow(clippy::too_many_arguments)]
pub fn dream_pipeline(
    engine: &mut StorageEngine,
    sparse_index: &mut SparseIndex,
    llm: &dyn LlmProvider,
    l2_ids: Option<Vec<u64>>,
    decay_config: &DecayConfig,
    l2_meta: &mut L2MetaIndex,
    encoder: Option<&(dyn Encoder + Send + Sync)>,
) -> Result<DreamReport, MemHopError> {
    let start_time = std::time::Instant::now();

    let mut report = DreamReport {
        demoted_to_secondary: Vec::new(),
        demoted_to_tertiary: Vec::new(),
        removed_contexts: Vec::new(),
        new_compressed: Vec::new(),
        groups_detected: 0,
        nodes_merged: 0,
        parent_nodes_created: 0,
        nodes_sunk: 0,
        nodes_removed: 0,
        l1_updated: Vec::new(),
        l1_decayed_nodes: 0,
        l1_pruned_edges: 0,
        l1_removed_nodes: 0,
        l1_removed_edges: 0,
        l0_updated: None,
        habits_updated: None,
        new_l3_nodes: Vec::new(),
        new_crystals: Vec::new(),
        pruned_crystals: Vec::new(),
        l6_decayed: 0,
        l6_pruned: 0,
        l6_decayed_details: None,
        l6_pruned_details: None,
        stages: Vec::new(),
        duration_ms: 0,
        rollback_incomplete: false,
    };

    let target_l2_ids = match l2_ids {
        Some(ids) if !ids.is_empty() => ids.into_iter().collect::<HashSet<u64>>(),
        _ => collect_all_l2_ids(engine),
    };

    let sparse_snapshot = sparse_index.clone();
    let mut stages = Vec::new();

    // ========================================================================
    // Phase 1: collect data + one LLM call
    // ========================================================================
    let input = build_consolidation_input(engine, l2_meta, &target_l2_ids)?;
    let (llm_output, failed_after_retry) = run_consolidation(llm, &input);

    // ========================================================================
    // Apply L2 groups
    // ========================================================================
    match llm_output.l2_groups {
        Section::Valid(ref groups) => {
            match apply_l2_groups(groups, engine, sparse_index, l2_meta, encoder) {
                Ok((groups_detected, nodes_merged, parents, sunk, removed)) => {
                    report.groups_detected = groups_detected;
                    report.nodes_merged = nodes_merged;
                    report.parent_nodes_created = parents;
                    report.nodes_sunk = sunk;
                    report.nodes_removed = removed;
                    stages.push(StageReport {
                        name: "l2_compress".into(),
                        status: StageStatus::Success,
                        description: format!(
                            "{} groups, {} merged, {} parents, {} sunk, {} removed",
                            groups_detected, nodes_merged, parents, sunk, removed
                        ),
                        processed_count: (groups_detected + nodes_merged + parents + sunk + removed)
                            as usize,
                        duration_ms: 0,
                        error: None,
                    });
                }
                Err(e) => {
                    stages.push(StageReport {
                        name: "l2_compress".into(),
                        status: StageStatus::Failed,
                        description: "L2 merge failed".into(),
                        processed_count: 0,
                        duration_ms: 0,
                        error: Some(e.to_string()),
                    });
                }
            }
        }
        Section::ParseFailed(ref msg) => {
            // Fallback: use non-LLM merge
            tracing::warn!(
                "L2 groups from LLM failed ({}), using keyword-only fallback",
                msg
            );
            let fallback_texts: Vec<String> = input
                .scenes
                .iter()
                .flat_map(|s| s.nodes.iter())
                .filter_map(|n| n.fused_summary.as_ref().cloned())
                .collect();
            let (title, _summary) = llm.fallback_summarize(&fallback_texts);
            stages.push(StageReport {
                name: "l2_compress".into(),
                status: StageStatus::Failed,
                description: format!("L2 merge fallback: \"{}\"", title),
                processed_count: 0,
                duration_ms: 0,
                error: Some(msg.clone()),
            });
        }
        _ => {}
    }

    // ========================================================================
    // Apply L3 extractions
    // ========================================================================
    match llm_output.l3_extractions {
        Section::Valid(ref extractions) => {
            match apply_l3_extractions(engine, extractions, sparse_index) {
                Ok(ids) => {
                    report.new_l3_nodes = ids;
                    stages.push(StageReport {
                        name: "l3_distill".into(),
                        status: StageStatus::Success,
                        description: format!("Distilled {} L3 nodes", report.new_l3_nodes.len()),
                        processed_count: report.new_l3_nodes.len(),
                        duration_ms: 0,
                        error: None,
                    });
                }
                Err(e) => {
                    stages.push(StageReport {
                        name: "l3_distill".into(),
                        status: StageStatus::Failed,
                        description: "L3 distillation write failed".into(),
                        processed_count: 0,
                        duration_ms: 0,
                        error: Some(e.to_string()),
                    });
                }
            }
        }
        Section::ParseFailed(ref msg) => {
            stages.push(StageReport {
                name: "l3_distill".into(),
                status: StageStatus::Failed,
                description: "L3 distillation skipped (LLM failed)".into(),
                processed_count: 0,
                duration_ms: 0,
                error: Some(msg.clone()),
            });
        }
        _ => {}
    }

    // ========================================================================
    // Apply habits
    // ========================================================================
    match llm_output.habits {
        Section::Valid(ref analysis) => match apply_habits(analysis, engine) {
            Ok((new_l, new_s, new_e)) => {
                report.habits_updated = Some(habit_distill_stage::HabitUpdate {
                    new_lexicon: new_l,
                    new_style_traits: new_s,
                    new_emotion_patterns: new_e,
                    total_dialogues_analyzed: input.recent_dialogues.len(),
                });
                stages.push(StageReport {
                    name: "habit_distill".into(),
                    status: StageStatus::Success,
                    description: format!(
                        "Habits: {} lexicon, {} style, {} emotion",
                        new_l, new_s, new_e
                    ),
                    processed_count: new_l + new_s + new_e,
                    duration_ms: 0,
                    error: None,
                });
            }
            Err(e) => {
                stages.push(StageReport {
                    name: "habit_distill".into(),
                    status: StageStatus::Failed,
                    description: "Habit merge failed".into(),
                    processed_count: 0,
                    duration_ms: 0,
                    error: Some(e.to_string()),
                });
            }
        },
        Section::ParseFailed(_) => {
            // Non-LLM fallback
            let fallback = llm.fallback_habits(&input.recent_dialogues);
            let _ = apply_habits(&fallback, engine).ok();
            stages.push(StageReport {
                name: "habit_distill".into(),
                status: StageStatus::Failed,
                description: "Habit analysis: used keyword fallback".into(),
                processed_count: fallback.lexicon.len(),
                duration_ms: 0,
                error: None,
            });
        }
        _ => {}
    }

    // ========================================================================
    // Apply crystals
    // ========================================================================
    match llm_output.crystals {
        Section::Valid(ref crystals) => match apply_crystals(crystals, engine) {
            Ok(ids) => {
                report.new_crystals = ids;
                stages.push(StageReport {
                    name: "l5_crystallize".into(),
                    status: StageStatus::Success,
                    description: format!("Crystallized {} patterns", report.new_crystals.len()),
                    processed_count: report.new_crystals.len(),
                    duration_ms: 0,
                    error: None,
                });
            }
            Err(e) => {
                stages.push(StageReport {
                    name: "l5_crystallize".into(),
                    status: StageStatus::Failed,
                    description: "Crystal write failed".into(),
                    processed_count: 0,
                    duration_ms: 0,
                    error: Some(e.to_string()),
                });
            }
        },
        Section::ParseFailed(ref msg) => {
            stages.push(StageReport {
                name: "l5_crystallize".into(),
                status: StageStatus::Failed,
                description: "Crystallization skipped (LLM failed)".into(),
                processed_count: 0,
                duration_ms: 0,
                error: Some(msg.clone()),
            });
        }
        _ => {}
    }

    // Log final retry status
    if !failed_after_retry.is_empty() {
        tracing::warn!(
            "consolidate: sections still failed after retry: {:?}",
            failed_after_retry
        );
    }

    // ========================================================================
    // Non-LLM stages
    // ========================================================================

    run_stage(
        "l1_rebuild",
        "L1 rebuild failed",
        |report, sparse_index| {
            let l1_updated =
                rebuild_l1_from_l2(engine, sparse_index, &target_l2_ids, decay_config, l2_meta)?;
            let count = l1_updated.len();
            report.l1_updated = l1_updated;
            Ok((format!("Rebuilt {} L1 associations", count), count))
        },
        &mut report,
        sparse_index,
        &mut stages,
        true,
        |report, sparse_index| {
            *sparse_index = sparse_snapshot.clone();
            report.rollback_incomplete = true;
        },
        start_time,
    )?;

    run_stage(
        "l1_decay",
        "L1 decay failed",
        |report, _| {
            let decay_report = l1_decay::decay_l1_network(engine, decay_config, l2_meta)?;
            report.l1_decayed_nodes = decay_report.decayed_nodes;
            report.l1_pruned_edges = decay_report.pruned_edges;
            report.l1_removed_nodes = decay_report.removed_nodes;
            report.l1_removed_edges = decay_report.removed_edges;
            let count = report.l1_decayed_nodes
                + report.l1_pruned_edges
                + report.l1_removed_nodes
                + report.l1_removed_edges;
            Ok((
                format!(
                    "L1 decay: {} nodes, {} edges",
                    report.l1_decayed_nodes, report.l1_pruned_edges
                ),
                count,
            ))
        },
        &mut report,
        sparse_index,
        &mut stages,
        true,
        |report, sparse_index| {
            *sparse_index = sparse_snapshot.clone();
            report.rollback_incomplete = true;
        },
        start_time,
    )?;

    run_stage(
        "l0_profile",
        "L0 profile generation failed",
        |report, sparse_index| {
            l0_form_stage::generate_profile(engine, sparse_index)?;
            if !target_l2_ids.is_empty() {
                let pid = format_hash(crate::util::hash_id("profile"));
                report.l0_updated = Some((pid, vec!["personality".into(), "preferences".into()]));
            }
            Ok((
                "L0 profile regenerated".into(),
                if report.l0_updated.is_some() { 1 } else { 0 },
            ))
        },
        &mut report,
        sparse_index,
        &mut stages,
        true,
        |report, sparse_index| {
            *sparse_index = sparse_snapshot.clone();
            report.rollback_incomplete = true;
        },
        start_time,
    )?;

    run_stage(
        "l6_decay",
        "L6 pathway decay failed",
        |report, _| {
            let l6_report = l6_decay::decay_l6_pathways(engine, decay_config)?;
            report.l6_decayed = l6_report.decayed;
            report.l6_pruned = l6_report.pruned;
            report.l6_decayed_details = if l6_report.decayed_details.is_empty() {
                None
            } else {
                Some(l6_report.decayed_details)
            };
            report.l6_pruned_details = if l6_report.pruned_details.is_empty() {
                None
            } else {
                Some(l6_report.pruned_details)
            };
            let count = l6_report.decayed + l6_report.pruned;
            Ok((
                format!(
                    "L6 decay: {} decayed, {} pruned",
                    l6_report.decayed, l6_report.pruned
                ),
                count,
            ))
        },
        &mut report,
        sparse_index,
        &mut stages,
        false,
        |_, _| {},
        start_time,
    )?;

    run_stage(
        "crystal_prune",
        "Crystal pruning failed",
        |report, _| {
            let pruned = crystallize_stage::prune_low_quality_crystals(engine)?;
            let count = pruned.len();
            report.pruned_crystals = pruned;
            Ok((format!("Pruned {} low-quality crystals", count), count))
        },
        &mut report,
        sparse_index,
        &mut stages,
        false,
        |_, _| {},
        start_time,
    )?;

    report.stages = stages;
    report.duration_ms = start_time.elapsed().as_millis() as u64;

    if report.rollback_incomplete {
        tracing::error!("Dream rollback incomplete");
    }
    Ok(report)
}

// ============================================================================
// Reused helpers
// ============================================================================

fn collect_all_l2_ids(engine: &StorageEngine) -> HashSet<u64> {
    let mut ids = HashSet::new();
    for (&id_hash, _) in engine.iter_index() {
        if let Ok(Some((record_type, _data))) = engine.read_record(id_hash) {
            if record_type == REC_L2_TOPIC {
                ids.insert(id_hash);
            }
        }
    }
    ids
}

fn rebuild_l1_from_l2(
    engine: &mut StorageEngine,
    sparse_index: &mut SparseIndex,
    _session_topic_ids: &HashSet<u64>,
    decay_config: &DecayConfig,
    l2_meta: &L2MetaIndex,
) -> Result<Vec<String>, MemHopError> {
    use crate::layers::context_node::SceneNode;
    use crate::storage::record::REC_L1_SCENE_NODE;

    let mut stale_nodes: Vec<u64> = Vec::new();
    let entries: Vec<(u64, u64)> = engine.iter_index().map(|(k, v)| (*k, *v)).collect();
    for (id_hash, _offset) in &entries {
        let Some((record_type, data)) = engine.read_record(*id_hash)? else {
            continue;
        };
        if record_type != REC_L1_SCENE_NODE {
            continue;
        }
        let Ok(node) = bincode::deserialize::<SceneNode>(data) else {
            continue;
        };
        let first_topic_id = node.topic_ids.first().copied().unwrap_or(0);
        if first_topic_id == 0 || !engine.contains(first_topic_id) {
            stale_nodes.push(*id_hash);
        } else {
            let is_depth_out_of_range = l2_meta
                .get(first_topic_id)
                .map(|meta| meta.depth > 2)
                .unwrap_or(false);
            if is_depth_out_of_range {
                let keep = l2_meta
                    .get(first_topic_id)
                    .and_then(|meta| {
                        if meta.depth != 3 {
                            return Some(false);
                        }
                        let parent_data = engine.read_record(first_topic_id).ok()??;
                        let ctx: TopicSlot = bincode::deserialize(parent_data.1).ok()?;
                        let parent_id = ctx.parent_id?;
                        let parent_depth = l2_meta.get(parent_id).map(|pm| pm.depth)?;
                        Some(parent_depth <= 2)
                    })
                    .unwrap_or(false);
                if !keep {
                    stale_nodes.push(*id_hash);
                }
            }
        }
    }

    let mut updated = Vec::new();
    for id_hash in &stale_nodes {
        let Some((_rt, data)) = engine.read_record(*id_hash)? else {
            continue;
        };
        if let Ok(node) = bincode::deserialize::<SceneNode>(data) {
            for edge_id in &node.edge_ids {
                crate::dream::l1_decay::remove_node_from_edge(
                    engine,
                    *edge_id,
                    *id_hash,
                    decay_config,
                )?;
            }
        }
        engine.delete_record(*id_hash)?;
        sparse_index.remove_document(*id_hash);
        updated.push(format_hash(*id_hash));
    }
    Ok(updated)
}
