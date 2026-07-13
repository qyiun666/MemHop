// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

pub(crate) mod compress_stage;
pub(crate) mod crystallize_stage;
pub(crate) mod emotion;
pub(crate) mod habit_distill_stage;
pub(crate) mod l0_form_stage;
pub(crate) mod l1_decay;
pub(crate) mod l3_distill_stage;
pub mod llm;
#[cfg(feature = "llm")]
pub mod llm_preprocess;
#[cfg(feature = "llm")]
pub mod openai_compatible;
pub mod prune;

use crate::config::DecayConfig;
use crate::dream::llm::{
    ConsolidationInput, ConsolidationOutput, HabitAnalysis, L2Group, L2NodeData, L3Extraction,
    LlmProvider, SceneData, Section,
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
    encoder: &(dyn Encoder + Send + Sync),
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

/// Run the LLM consolidation. Returns error on any LLM failure or parse failure.
fn run_consolidation(
    llm: &dyn LlmProvider,
    input: &ConsolidationInput,
) -> Result<ConsolidationOutput, MemHopError> {
    let output = llm.consolidate(input)?;

    // Any section that failed to parse is treated as an error
    if output.l2_groups.needs_retry() {
        return Err(MemHopError::LlmError(
            "L2 groups section failed to parse from LLM response".into(),
        ));
    }
    if output.l3_extractions.needs_retry() {
        return Err(MemHopError::LlmError(
            "L3 extractions section failed to parse from LLM response".into(),
        ));
    }
    if output.habits.needs_retry() {
        return Err(MemHopError::LlmError(
            "Habits section failed to parse from LLM response".into(),
        ));
    }
    if output.crystals.needs_retry() {
        return Err(MemHopError::LlmError(
            "Crystals section failed to parse from LLM response".into(),
        ));
    }

    Ok(output)
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
    encoder: &(dyn Encoder + Send + Sync),
) -> Result<DreamReport, MemHopError> {
    let start_time = std::time::Instant::now();
    let _stages: Vec<StageReport> = Vec::new();

    let target_l2_ids = match l2_ids {
        Some(ids) if !ids.is_empty() => ids.into_iter().collect::<HashSet<u64>>(),
        _ => collect_all_l2_ids(engine),
    };

    let mut stages = Vec::new();

    // Track L1 metrics for the new DreamReport
    let mut l1_decayed: u32 = 0;
    let mut l1_pruned_edges: u32 = 0;
    let mut l1_removed_nodes: u32 = 0;
    let mut l1_removed_edges: u32 = 0;
    let mut l1_updated_nodes: Vec<String> = Vec::new();
    let mut l2_total_affected: u32 = 0;

    // ========================================================================
    // Phase 1: collect data + one LLM call
    // ========================================================================
    let input = build_consolidation_input(engine, l2_meta, &target_l2_ids)?;
    let llm_output = run_consolidation(llm, &input)?;

    // ========================================================================
    // Apply L2 groups
    // ========================================================================
    if let Section::Valid(ref groups) = llm_output.l2_groups {
        match apply_l2_groups(groups, engine, sparse_index, l2_meta, encoder) {
            Ok((groups_detected, nodes_merged, parents, sunk, removed)) => {
                l2_total_affected += groups_detected + nodes_merged + parents + sunk + removed;
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

    // ========================================================================
    // Apply L3 extractions
    // ========================================================================
    let mut new_l3_node_ids: Vec<String> = Vec::new();
    match llm_output.l3_extractions {
        Section::Valid(ref extractions) => {
            match apply_l3_extractions(engine, extractions, sparse_index) {
                Ok(ids) => {
                    new_l3_node_ids = ids;
                    stages.push(StageReport {
                        name: "l3_distill".into(),
                        status: StageStatus::Success,
                        description: format!("Distilled {} L3 nodes", new_l3_node_ids.len()),
                        processed_count: new_l3_node_ids.len(),
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
    if let Section::Valid(ref analysis) = llm_output.habits {
        match apply_habits(analysis, engine) {
            Ok((new_l, new_s, new_e)) => {
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
        }
    }

    // ========================================================================
    // Apply crystals
    // ========================================================================
    let mut new_crystal_ids: Vec<String> = Vec::new();
    match llm_output.crystals {
        Section::Valid(ref crystals) => match apply_crystals(crystals, engine) {
            Ok(ids) => {
                new_crystal_ids = ids;
                stages.push(StageReport {
                    name: "l5_crystallize".into(),
                    status: StageStatus::Success,
                    description: format!("Crystallized {} patterns", new_crystal_ids.len()),
                    processed_count: new_crystal_ids.len(),
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

    // ========================================================================
    // Non-LLM stages
    // ========================================================================

    {
        let stage_start = std::time::Instant::now();
        match rebuild_l1_from_l2(engine, sparse_index, &target_l2_ids, decay_config, l2_meta) {
            Ok(updated) => {
                let count = updated.len();
                l1_updated_nodes = updated;
                stages.push(StageReport {
                    name: "l1_rebuild".into(),
                    status: StageStatus::Success,
                    description: format!("Rebuilt {} L1 associations", count),
                    processed_count: count,
                    duration_ms: stage_start.elapsed().as_millis() as u64,
                    error: None,
                });
            }
            Err(e) => {
                tracing::error!("L1 rebuild failed: {}", e);
                stages.push(StageReport {
                    name: "l1_rebuild".into(),
                    status: StageStatus::Failed,
                    description: "L1 rebuild failed".into(),
                    processed_count: 0,
                    duration_ms: stage_start.elapsed().as_millis() as u64,
                    error: Some(e.to_string()),
                });
            }
        }
    }

    {
        let stage_start = std::time::Instant::now();
        match l1_decay::decay_l1_network(engine, decay_config, l2_meta) {
            Ok(decay_report) => {
                l1_decayed = decay_report.decayed_nodes as u32;
                l1_pruned_edges = decay_report.pruned_edges as u32;
                l1_removed_nodes = decay_report.removed_nodes as u32;
                l1_removed_edges = decay_report.removed_edges as u32;
                let count = l1_decayed + l1_pruned_edges + l1_removed_nodes + l1_removed_edges;
                stages.push(StageReport {
                    name: "l1_decay".into(),
                    status: StageStatus::Success,
                    description: format!(
                        "L1 decay: {} nodes, {} edges",
                        l1_decayed, l1_pruned_edges
                    ),
                    processed_count: count as usize,
                    duration_ms: stage_start.elapsed().as_millis() as u64,
                    error: None,
                });
            }
            Err(e) => {
                stages.push(StageReport {
                    name: "l1_decay".into(),
                    status: StageStatus::Failed,
                    description: "L1 decay failed".into(),
                    processed_count: 0,
                    duration_ms: stage_start.elapsed().as_millis() as u64,
                    error: Some(e.to_string()),
                });
            }
        }
    }

    {
        let stage_start = std::time::Instant::now();
        match l0_form_stage::generate_profile(engine, sparse_index) {
            Ok(_) => {
                stages.push(StageReport {
                    name: "l0_profile".into(),
                    status: StageStatus::Success,
                    description: "L0 profile regenerated".into(),
                    processed_count: 1,
                    duration_ms: stage_start.elapsed().as_millis() as u64,
                    error: None,
                });
            }
            Err(e) => {
                stages.push(StageReport {
                    name: "l0_profile".into(),
                    status: StageStatus::Failed,
                    description: "L0 profile generation failed".into(),
                    processed_count: 0,
                    duration_ms: stage_start.elapsed().as_millis() as u64,
                    error: Some(e.to_string()),
                });
            }
        }
    }

    let mut pruned_crystal_ids: Vec<String> = Vec::new();
    {
        match crystallize_stage::prune_low_quality_crystals(engine) {
            Ok(pruned) => {
                let count = pruned.len();
                pruned_crystal_ids = pruned;
                stages.push(StageReport {
                    name: "crystal_prune".into(),
                    status: StageStatus::Success,
                    description: format!("Pruned {} low-quality crystals", count),
                    processed_count: count,
                    duration_ms: 0,
                    error: None,
                });
            }
            Err(e) => {
                stages.push(StageReport {
                    name: "crystal_prune".into(),
                    status: StageStatus::Failed,
                    description: "Crystal pruning failed".into(),
                    processed_count: 0,
                    duration_ms: 0,
                    error: Some(e.to_string()),
                });
            }
        }
    }

    let _duration_ms = start_time.elapsed().as_millis() as u64;
    let consolidated_count = l2_total_affected
        + new_l3_node_ids.len() as u32
        + new_crystal_ids.len() as u32
        + pruned_crystal_ids.len() as u32
        + l1_decayed
        + l1_pruned_edges
        + l1_removed_nodes
        + l1_removed_edges
        + l1_updated_nodes.len() as u32;

    // DreamStage is now an alias for StageReport, so we can use stages directly.
    let report = DreamReport {
        consolidated_count,
        new_skills: if new_crystal_ids.is_empty() {
            None
        } else {
            Some(new_crystal_ids.clone())
        },
        compressed_layers: Some(vec![2, 3, 5]),
        new_l3_nodes: new_l3_node_ids.len() as u32,
        new_crystals: new_crystal_ids.len() as u32,
        pruned_crystals: pruned_crystal_ids.len() as u32,
        l1_decayed_nodes: l1_decayed,
        l1_pruned_edges,
        l1_removed_nodes,
        l1_removed_edges,
        stages,
    };

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
