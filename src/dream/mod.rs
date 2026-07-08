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
use crate::file::header::FileHeader;
use crate::index::btree::BTreeIndex;
use crate::index::l2_meta::L2MetaIndex;
use crate::index::sparse::SparseIndex;
use crate::layers::context::ContextSlot;
use crate::query::diagnostics::{StageReport, StageStatus};
use crate::shared::common::{format_hash, now_ms};
use crate::shared::slot_io::get_slot_data;
use crate::util::{PageType, PAGE_SIZE};
use crate::MemHopError;
use memmap2::MmapMut;
use std::collections::HashSet;
use std::fs::File;

const MAX_RECENT_DIALOGUES: usize = 30;

/// Run a single dream stage, recording its result and rolling back on fatal errors.
#[allow(clippy::too_many_arguments)]
fn run_stage<F, R>(
    name: &str,
    failure_description: &str,
    f: F,
    report: &mut DreamReport,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    stages: &mut Vec<StageReport>,
    fatal: bool,
    rollback: R,
    start_time: std::time::Instant,
) -> Result<(), MemHopError>
where
    F: FnOnce(
        &mut DreamReport,
        &mut BTreeIndex,
        &mut SparseIndex,
    ) -> Result<(String, usize), MemHopError>,
    R: FnOnce(&mut DreamReport, &mut BTreeIndex, &mut SparseIndex),
{
    let stage_start = std::time::Instant::now();
    match f(report, btree, sparse_index) {
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
                rollback(report, btree, sparse_index);
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
    mmap: &MmapMut,
    btree: &BTreeIndex,
    header: &FileHeader,
    l2_meta: &L2MetaIndex,
    target_l2_ids: &HashSet<u64>,
) -> Result<ConsolidationInput, MemHopError> {
    let data: &[u8] = &mmap[..];

    // Collect scenes from active L2 contexts
    let mut scene_map: std::collections::HashMap<u64, Vec<L2NodeData>> =
        std::collections::HashMap::new();

    for &id_hash in target_l2_ids {
        let page_ref = match btree.search(id_hash) {
            Some(pr) => pr,
            None => continue,
        };
        let slot_data = match get_slot_data(data, page_ref) {
            Some(d) => d,
            None => continue,
        };
        let ctx = match ContextSlot::deserialize(slot_data) {
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
    let recent_dialogues = habit_distill_stage::extract_recent_dialogues_inner(
        mmap,
        header,
        btree,
        MAX_RECENT_DIALOGUES,
    );

    // Collect existing L5 action chains
    let existing_chains = crystallize_stage::extract_existing_chains(mmap, header);

    Ok(ConsolidationInput {
        scenes,
        recent_dialogues,
        existing_chains,
    })
}

// ============================================================================
// Apply LLM output to mmap
// ============================================================================

fn apply_l2_groups(
    groups: &[L2Group],
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    l2_meta: &mut L2MetaIndex,
    encoder: Option<&(dyn Encoder + Send + Sync)>,
    file: &mut File,
) -> Result<(u32, u32, u32, u32, u32), MemHopError> {
    compress_stage::apply_precomputed_groups(
        groups,
        mmap,
        header,
        btree,
        sparse_index,
        l2_meta,
        encoder,
        file,
    )
}

fn apply_l3_extractions(
    extractions: &[L3Extraction],
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    file: &mut File,
) -> Result<Vec<String>, MemHopError> {
    l3_distill_stage::apply_distill_extractions(
        extractions,
        mmap,
        header,
        btree,
        sparse_index,
        file,
    )
}

fn apply_habits(
    analysis: &HabitAnalysis,
    mmap: &mut MmapMut,
    btree: &BTreeIndex,
) -> Result<(usize, usize, usize), MemHopError> {
    habit_distill_stage::merge_habits_into_profile(mmap, btree, analysis)
}

fn apply_crystals(
    crystals: &[crate::dream::llm::CrystalDef],
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    file: &mut File,
) -> Result<Vec<String>, MemHopError> {
    crystallize_stage::apply_precomputed_crystals(crystals, mmap, header, btree, file)
}

// ============================================================================
// Core function — two-phase consolidated dream
// ============================================================================

/// Run the LLM consolidation in two phases, collect failed sections, retry.
fn run_consolidation(
    llm: &dyn LlmProvider,
    input: &ConsolidationInput,
) -> (ConsolidationOutput, Vec<DreamSection>) {
    // Phase 1
    let mut output = match llm.consolidate(input) {
        Ok(o) => o,
        Err(e) => {
            tracing::error!("consolidate LLM call failed: {}", e);
            let err_msg = format!("consolidate phase 1: {}", e);
            return (
                ConsolidationOutput {
                    l2_groups: Section::ParseFailed(err_msg.clone()),
                    l3_extractions: Section::ParseFailed(err_msg.clone()),
                    habits: Section::ParseFailed(err_msg.clone()),
                    crystals: Section::ParseFailed(err_msg),
                },
                vec![
                    DreamSection::L2Groups,
                    DreamSection::L3Distill,
                    DreamSection::Habits,
                    DreamSection::Crystals,
                ],
            );
        }
    };

    // Collect failed sections
    let mut failed = Vec::new();
    if !output.l2_groups.is_ok() {
        failed.push(DreamSection::L2Groups);
    }
    if !output.l3_extractions.is_ok() {
        failed.push(DreamSection::L3Distill);
    }
    if !output.habits.is_ok() {
        failed.push(DreamSection::Habits);
    }
    if !output.crystals.is_ok() {
        failed.push(DreamSection::Crystals);
    }

    if failed.is_empty() {
        return (output, failed);
    }

    // Phase 2: retry failed sections
    tracing::info!("consolidate: phase 2 retry for {:?} sections", failed);

    let retry = match llm.retry_sections(input, &failed) {
        Ok(r) => r,
        Err(e) => {
            tracing::warn!("consolidate retry failed: {}", e);
            return (output, failed);
        }
    };

    // Merge retry results into output (only replace failed sections)
    if let Section::Valid(g) = retry.l2_groups {
        output.l2_groups = Section::Valid(g);
        failed.retain(|s| *s != DreamSection::L2Groups);
    }
    if let Section::Valid(e) = retry.l3_extractions {
        output.l3_extractions = Section::Valid(e);
        failed.retain(|s| *s != DreamSection::L3Distill);
    }
    if let Section::Valid(h) = retry.habits {
        output.habits = Section::Valid(h);
        failed.retain(|s| *s != DreamSection::Habits);
    }
    if let Section::Valid(c) = retry.crystals {
        output.crystals = Section::Valid(c);
        failed.retain(|s| *s != DreamSection::Crystals);
    }

    (output, failed)
}

// ============================================================================
// Main dream pipeline
// ============================================================================

#[allow(clippy::too_many_arguments)]
pub fn dream_pipeline(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    llm: &dyn LlmProvider,
    l2_ids: Option<Vec<u64>>,
    file: &mut File,
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

    flush_l2_meta_to_mmap(mmap, btree, l2_meta)?;

    let target_l2_ids = match l2_ids {
        Some(ids) if !ids.is_empty() => ids.into_iter().collect::<HashSet<u64>>(),
        _ => collect_all_l2_ids(&mmap[..], btree, header.page_count),
    };

    let btree_snapshot = btree.clone();
    let sparse_snapshot = sparse_index.clone();
    let mut stages = Vec::new();

    // ========================================================================
    // Phase 1: collect data + one LLM call
    // ========================================================================
    let input = build_consolidation_input(mmap, btree, header, l2_meta, &target_l2_ids)?;
    let (llm_output, failed_after_retry) = run_consolidation(llm, &input);

    // ========================================================================
    // Apply L2 groups
    // ========================================================================
    match llm_output.l2_groups {
        Section::Valid(ref groups) => {
            match apply_l2_groups(
                groups,
                mmap,
                header,
                btree,
                sparse_index,
                l2_meta,
                encoder,
                file,
            ) {
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
            match apply_l3_extractions(extractions, mmap, header, btree, sparse_index, file) {
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
        Section::Valid(ref analysis) => match apply_habits(analysis, mmap, btree) {
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
            let _ = apply_habits(&fallback, mmap, btree).ok();
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
        Section::Valid(ref crystals) => match apply_crystals(crystals, mmap, header, btree, file) {
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
    // Non-LLM stages (unchanged from original)
    // ========================================================================

    run_stage(
        "l1_rebuild",
        "L1 rebuild failed",
        |report, btree, sparse_index| {
            let l1_updated = rebuild_l1_from_l2(
                mmap,
                header,
                btree,
                sparse_index,
                &target_l2_ids,
                decay_config,
                l2_meta,
            )?;
            let count = l1_updated.len();
            report.l1_updated = l1_updated;
            Ok((format!("Rebuilt {} L1 associations", count), count))
        },
        &mut report,
        btree,
        sparse_index,
        &mut stages,
        true,
        |report, btree, sparse_index| {
            *btree = btree_snapshot.clone();
            *sparse_index = sparse_snapshot.clone();
            report.rollback_incomplete = true;
        },
        start_time,
    )?;

    run_stage(
        "l1_decay",
        "L1 decay failed",
        |report, btree, _| {
            let decay_report =
                l1_decay::decay_l1_network(mmap, header, btree, decay_config, l2_meta)?;
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
        btree,
        sparse_index,
        &mut stages,
        true,
        |report, btree, sparse_index| {
            *btree = btree_snapshot.clone();
            *sparse_index = sparse_snapshot.clone();
            report.rollback_incomplete = true;
        },
        start_time,
    )?;

    run_stage(
        "l0_profile",
        "L0 profile generation failed",
        |report, btree, sparse_index| {
            l0_form_stage::generate_profile(mmap, header, btree, sparse_index, file)?;
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
        btree,
        sparse_index,
        &mut stages,
        true,
        |report, btree, sparse_index| {
            *btree = btree_snapshot.clone();
            *sparse_index = sparse_snapshot.clone();
            report.rollback_incomplete = true;
        },
        start_time,
    )?;

    run_stage(
        "l6_decay",
        "L6 pathway decay failed",
        |report, btree, _| {
            let l6_report = l6_decay::decay_l6_pathways(mmap, header, btree, decay_config, file)?;
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
        btree,
        sparse_index,
        &mut stages,
        false,
        |_, _, _| {},
        start_time,
    )?;

    run_stage(
        "crystal_prune",
        "Crystal pruning failed",
        |report, btree, _| {
            let pruned = crystallize_stage::prune_low_quality_crystals(
                mmap,
                header,
                btree,
                header.page_count,
            )?;
            let count = pruned.len();
            report.pruned_crystals = pruned;
            Ok((format!("Pruned {} low-quality crystals", count), count))
        },
        &mut report,
        btree,
        sparse_index,
        &mut stages,
        false,
        |_, _, _| {},
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

fn flush_l2_meta_to_mmap(
    mmap: &mut MmapMut,
    _btree: &BTreeIndex,
    l2_meta: &L2MetaIndex,
) -> Result<(), MemHopError> {
    for (_id_hash, meta) in l2_meta.iter() {
        let page_ref = meta.page_ref;
        let page_id = crate::shared::slot_io::decode_page_id(page_ref);
        let offset = crate::shared::slot_io::page_offset(page_id);
        if offset + PAGE_SIZE > mmap.len() {
            continue;
        }
        let Some(slot_data) = get_slot_data(&mmap[..], page_ref) else {
            continue;
        };
        let Ok(_ctx) = ContextSlot::deserialize(slot_data) else {
            continue;
        };
        // TopicSlot no longer has activation_score / activation_state fields.
        // Metadata sync is handled via L2MetaIndex in memory.
    }
    Ok(())
}

fn collect_all_l2_ids(mmap: &[u8], btree: &BTreeIndex, page_count: u32) -> HashSet<u64> {
    let mut ids = HashSet::new();
    for (&id_hash, &page_ref) in btree.iter_unsorted() {
        let page_id = crate::shared::slot_io::decode_page_id(page_ref);
        if page_id == 0 || page_id >= page_count {
            continue;
        }
        let pt_offset = (page_id as usize) * PAGE_SIZE + 4;
        if pt_offset + 2 > mmap.len() {
            continue;
        }
        let pt = u16::from_le_bytes([mmap[pt_offset], mmap[pt_offset + 1]]);
        if pt == PageType::Context as u16 {
            ids.insert(id_hash);
        }
    }
    ids
}

fn rebuild_l1_from_l2(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    _session_topic_ids: &HashSet<u64>,
    decay_config: &DecayConfig,
    l2_meta: &L2MetaIndex,
) -> Result<Vec<String>, MemHopError> {
    use crate::layers::context_node::ContextNode;
    let page_count = header.page_count;
    let mut stale_nodes: Vec<(u64, u32)> = Vec::new();
    let entries: Vec<(u64, u64)> = btree.iter_unsorted().map(|(k, v)| (*k, *v)).collect();
    for (id_hash, page_ref) in &entries {
        let page_id = (page_ref >> 16) as u32;
        if page_id >= page_count {
            continue;
        }
        let page_offset = (page_id as usize) * PAGE_SIZE;
        if page_offset + PAGE_SIZE > mmap.len() {
            continue;
        }
        if let Ok(page_hdr) = crate::file::page::read_page_header(&mmap[..], page_id) {
            if page_hdr.page_type != PageType::ContextNode as u16 {
                continue;
            }
        } else {
            continue;
        }
        if let Some(slot_data) = get_slot_data(&mmap[..], *page_ref) {
            if let Ok(node) = ContextNode::deserialize(slot_data) {
                if btree.search(node.context_id).is_none() {
                    stale_nodes.push((*id_hash, page_id));
                } else {
                    let is_depth_out_of_range = l2_meta
                        .get(node.context_id)
                        .map(|meta| meta.depth > 2)
                        .unwrap_or(false);
                    if is_depth_out_of_range {
                        let keep = l2_meta
                            .get(node.context_id)
                            .and_then(|meta| {
                                if meta.depth != 3 {
                                    return Some(false);
                                }
                                let parent_ref = btree.search(node.context_id)?;
                                let parent_data = get_slot_data(&mmap[..], parent_ref)?;
                                let ctx = ContextSlot::deserialize(parent_data).ok()?;
                                let parent_id = ctx.parent_id?;
                                let parent_depth = l2_meta.get(parent_id).map(|pm| pm.depth)?;
                                Some(parent_depth <= 2)
                            })
                            .unwrap_or(false);
                        if !keep {
                            stale_nodes.push((*id_hash, page_id));
                        }
                    }
                }
            }
        }
    }
    let mut updated = Vec::new();
    for (id_hash, page_id) in stale_nodes {
        let page_ref = crate::file::page::encode_page_ref(page_id, 0);
        if let Some(slot_data) = get_slot_data(&mmap[..], page_ref) {
            if let Ok(node) = ContextNode::deserialize(slot_data) {
                for edge_id in &node.edge_ptrs {
                    crate::dream::l1_decay::remove_node_from_edge(
                        mmap,
                        btree,
                        header,
                        *edge_id,
                        id_hash,
                        decay_config,
                    )?;
                }
            }
        }
        btree.remove(id_hash);
        let offset = crate::shared::slot_io::page_offset(page_id);
        mmap[offset..offset + PAGE_SIZE].fill(0);
        crate::file::free_list::free_page(mmap, header, page_id)?;
        sparse_index.remove_document(id_hash);
        updated.push(format_hash(id_hash));
    }
    Ok(updated)
}
