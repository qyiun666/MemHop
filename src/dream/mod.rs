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
pub mod openai_compatible;
pub mod prune;

use crate::config::DecayConfig;
use crate::dream::llm::LlmProvider;
use crate::dream::prune::DreamReport;
use crate::file::header::FileHeader;
use crate::index::btree::BTreeIndex;
use crate::index::l2_meta::{ActivationStatus, L2MetaIndex};
use crate::index::sparse::SparseIndex;
use crate::layers::context::{ActivationState, ContextSlot};
use crate::query::diagnostics::{StageReport, StageStatus};
use crate::shared::common::{format_hash, now_ms};
use crate::util::{PageType, PAGE_SIZE};
use crate::MemHopError;
use memmap2::MmapMut;
use std::collections::HashSet;
use std::fs::File;

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

/// Main dream pipeline - memory consolidation through depth demotion
///
/// This function orchestrates the complete dream consolidation process:
/// 1. L2 Compression: depth demotion (主→次→次次→remove) on active contexts
/// 2. L1 Update: rebuild L1 ContextNode associations based on updated L2
/// 3. L0 Update: regenerate L0 profile from L1 knowledge distribution
/// 4. L3 Distillation: extract structured knowledge into L3 hypergraph via LLM
/// 5. L5 Crystallization: scan all ActionChainSlots and extract crystals
/// 6. L6 Decay: time-decay procedural pathway weights
///
/// # Transaction Safety
/// The pipeline takes in-memory snapshots of btree and sparse_index before
/// execution. If any stage fails, the snapshots are restored so that the
/// in-memory indices remain consistent with the mmap state (which may have
/// been partially modified). Callers should checkpoint after a successful
/// dream to persist the changes.
///
/// # Arguments
/// * `mmap` - Mutable memory-mapped file for reading/writing memory slots
/// * `header` - File header for page allocation and free list management
/// * `btree` - B-tree index for topic lookup
/// * `sparse_index` - Sparse index for keyword lookup
/// * `llm` - LLM provider for summarization and crystal generation
/// * `l2_ids` - Optional list of L2 context id_hashes to process; `None` processes all L2s
/// * `file` - Backing file handle for mmap lifecycle and extension
/// * `decay_config` - Time-decay parameters for L1/L6 stages
/// * `l2_meta` - In-memory L2 metadata index; pending activation_score deltas are flushed to mmap
///
/// # Returns
/// DreamReport containing statistics about all operations performed
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
    l2_meta: &L2MetaIndex,
) -> Result<DreamReport, MemHopError> {
    let start_time = std::time::Instant::now();

    let mut report = DreamReport {
        demoted_to_secondary: Vec::new(),
        demoted_to_tertiary: Vec::new(),
        removed_contexts: Vec::new(),
        new_compressed: Vec::new(),
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
        stages: Vec::new(),
        duration_ms: 0,
        rollback_incomplete: false,
    };

    // Flush any in-memory L2 metadata deltas back to mmap before the pipeline
    // modifies L2 slots directly.
    flush_l2_meta_to_mmap(mmap, btree, l2_meta)?;

    // Resolve target L2 ids: None or empty means process every existing L2 context.
    let target_l2_ids = match l2_ids {
        Some(ids) if !ids.is_empty() => ids.into_iter().collect::<HashSet<u64>>(),
        _ => collect_all_l2_ids(&mmap[..], btree, header.page_count),
    };

    let btree_snapshot = btree.clone();
    let sparse_snapshot = sparse_index.clone();

    // TODO: evaluate COW snapshot to reduce btree/sparse_index clone cost.

    // Helper to record stage results (failure doesn't block subsequent stages)
    let mut stages = Vec::new();

    // L3 distillation runs BEFORE L2 compression because compression demotes
    // active depth-1 contexts to depth-2, and L3 needs their original summaries.
    run_stage(
        "l3_distill",
        "L3 knowledge distillation failed",
        |report, btree, sparse_index| {
            let nodes = l3_distill_stage::distill_l3_knowledge(
                mmap,
                header,
                btree,
                sparse_index,
                llm,
                &target_l2_ids,
                file,
            )?;
            let count = nodes.len();
            report.new_l3_nodes = nodes;
            Ok((format!("Distilled {} L3 knowledge nodes", count), count))
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
        "l2_compress",
        "L2 compression failed",
        |report, btree, sparse_index| {
            let (demoted_sec, compressed, removed, demoted_ter) =
                compress_stage::compress_active_contexts(
                    mmap,
                    header,
                    btree,
                    sparse_index,
                    llm,
                    &target_l2_ids,
                    file,
                )?;
            let count = demoted_sec.len() + compressed.len() + removed.len() + demoted_ter.len();
            report.demoted_to_secondary = demoted_sec;
            report.new_compressed = compressed;
            report.removed_contexts = removed;
            report.demoted_to_tertiary = demoted_ter;
            Ok((
                format!(
                    "Compressed L2 contexts: {} demoted, {} new compressed, {} removed",
                    report.demoted_to_secondary.len(),
                    report.new_compressed.len(),
                    report.removed_contexts.len()
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

    // L1 nodes point to L2 contexts; after L2 depth changes, L1 associations need refresh
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
        |report, btree, _sparse_index| {
            let decay_report = l1_decay::decay_l1_network(mmap, header, btree, decay_config)?;
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
                    "L1 decay: {} nodes decayed, {} edges pruned, {} nodes removed, {} edges removed",
                    report.l1_decayed_nodes,
                    report.l1_pruned_edges,
                    report.l1_removed_nodes,
                    report.l1_removed_edges
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
                let profile_id_hash = crate::util::hash_id("profile");
                let profile_id = format_hash(profile_id_hash);
                report.l0_updated = Some((
                    profile_id,
                    vec!["personality".to_string(), "preferences".to_string()],
                ));
            }
            Ok((
                "L0 profile regenerated".to_string(),
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
        "habit_distill",
        "Habit distillation failed",
        |report, btree, _sparse_index| {
            let habit_update = habit_distill_stage::distill_user_habits(mmap, header, btree, llm)?;
            let count = habit_update.new_lexicon
                + habit_update.new_style_traits
                + habit_update.new_emotion_patterns;
            if count > 0 {
                report.habits_updated = Some(habit_distill_stage::HabitUpdate {
                    new_lexicon: habit_update.new_lexicon,
                    new_style_traits: habit_update.new_style_traits,
                    new_emotion_patterns: habit_update.new_emotion_patterns,
                    total_dialogues_analyzed: habit_update.total_dialogues_analyzed,
                });
            }
            Ok((
                format!(
                    "Habit distillation: {} lexicon, {} style traits, {} emotion patterns",
                    habit_update.new_lexicon,
                    habit_update.new_style_traits,
                    habit_update.new_emotion_patterns
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
        "l5_crystallize",
        "L5 crystallization failed",
        |report, btree, _sparse_index| {
            let crystals = crystallize_stage::crystallize_patterns(mmap, header, btree, llm, file)?;
            let count = crystals.len();
            report.new_crystals = crystals;
            Ok((format!("Crystallized {} new patterns", count), count))
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
        "l6_decay",
        "L6 pathway decay failed",
        |report, btree, _sparse_index| {
            let l6_report = l6_decay::decay_l6_pathways(mmap, header, btree, decay_config, file)?;
            report.l6_decayed = l6_report.decayed;
            report.l6_pruned = l6_report.pruned;
            let count = l6_report.decayed + l6_report.pruned;
            Ok((
                format!(
                    "L6 pathway decay: {} decayed, {} pruned",
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
        |report, btree, _sparse_index| {
            let page_count = header.page_count;
            let pruned =
                crystallize_stage::prune_low_quality_crystals(mmap, header, btree, page_count)?;
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

/// Rebuild L1 ContextNode associations based on updated L2 contexts
fn rebuild_l1_from_l2(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    _session_topic_ids: &HashSet<u64>,
    decay_config: &DecayConfig,
) -> Result<Vec<String>, MemHopError> {
    use crate::layers::context_node::ContextNode;
    use crate::util::PageType;

    let page_count = header.page_count;

    let mut stale_nodes: Vec<(u64, u32)> = Vec::new(); // (id_hash, page_id)
    let entries: Vec<(u64, u64)> = btree.iter_unsorted().map(|(k, v)| (*k, *v)).collect();

    for (id_hash, page_ref) in &entries {
        let page_id = (page_ref >> 16) as u32;
        if page_id >= page_count {
            continue;
        }

        let page_offset = (page_id as usize) * crate::util::PAGE_SIZE;
        if page_offset + crate::util::PAGE_SIZE > mmap.len() {
            continue;
        }

        if let Ok(page_hdr) = crate::file::page::read_page_header(&mmap[..], page_id) {
            if page_hdr.page_type != PageType::ContextNode as u16 {
                continue;
            }
        } else {
            continue;
        }

        if let Some(slot_data) = crate::shared::slot_io::get_slot_data(&mmap[..], *page_ref) {
            if let Ok(node) = ContextNode::deserialize(slot_data) {
                if btree.search(node.context_id).is_none() {
                    stale_nodes.push((*id_hash, page_id));
                }
            }
        }
    }

    let mut updated_ids: Vec<String> = Vec::new();
    for (id_hash, page_id) in stale_nodes {
        // Clean up references from this stale node to edges before freeing it.
        let page_ref = crate::file::page::encode_page_ref(page_id, 0);
        if let Some(slot_data) = crate::shared::slot_io::get_slot_data(&mmap[..], page_ref) {
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
        mmap[offset..offset + crate::util::PAGE_SIZE].fill(0);
        crate::file::free_list::free_page(mmap, header, page_id)?;
        sparse_index.remove_document(id_hash);
        updated_ids.push(format_hash(id_hash));
    }

    Ok(updated_ids)
}

/// Flush pending in-memory L2 metadata deltas back to the mmap-backed ContextSlots.
///
/// Since `L2MetaIndex` does not track per-field deltas, this iterates all indexed
/// entries and synchronizes `activation_score` (and derived `activation_state`)
/// whenever it differs from the on-disk value.
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
        let Some(slot_data) = crate::shared::slot_io::get_slot_data(&mmap[..], page_ref) else {
            continue;
        };
        let Ok(mut ctx) = ContextSlot::deserialize_slot(slot_data) else {
            continue;
        };

        let mut changed = false;
        if (ctx.activation_score - meta.activation_score).abs() > f32::EPSILON {
            ctx.activation_score = meta.activation_score;
            changed = true;
        }
        let expected_state = match meta.status {
            ActivationStatus::Dormant => ActivationState::Dormant,
            ActivationStatus::Active => ActivationState::Active,
            ActivationStatus::Crystallized => ActivationState::Crystallized,
        };
        if ctx.activation_state != expected_state {
            ctx.activation_state = expected_state;
            changed = true;
        }

        if changed {
            ctx.updated_at = now_ms();
            ctx.version += 1;
            let data = ctx
                .serialize()
                .map_err(|e| MemHopError::Serialization(e.to_string()))?;
            crate::file::page::write_page_data(mmap, page_id, &data)?;
        }
    }
    Ok(())
}

/// Collect all L2 context id_hashes currently present in the B-tree.
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

#[cfg(test)]
mod tests {
    use super::*;
    use crate::file::header::FileHeader;
    use crate::file::page::{allocate_page, encode_page_ref, write_page_data};
    use crate::index::btree::BTreeIndex;
    use crate::index::sparse::SparseIndex;
    use crate::layers::context_node::ContextNode;
    use crate::layers::hyperedge::{HyperedgeKind, HyperedgeSlot};
    use crate::test_helpers::create_test_mmap;
    use crate::util::{PageType, PAGE_SIZE, SENTINEL_PAGE_ID};
    use memmap2::MmapMut;
    use std::fs::File;
    use std::io::Write;

    fn default_decay_config() -> DecayConfig {
        DecayConfig {
            lambda_node: 0.01,
            lambda_edge: 0.02,
            node_remove_threshold: 0.05,
            node_prune_edges_threshold: 0.15,
            edge_remove_threshold: 0.05,
            min_edge_nodes: 2,
            lambda_pathway: 0.01,
            pathway_remove_threshold: 0.05,
        }
    }

    fn create_mmap(pages: usize) -> (MmapMut, FileHeader, BTreeIndex) {
        let temp_file = tempfile::NamedTempFile::new().unwrap();
        let path = temp_file.path();
        let mut file = File::create(path).unwrap();
        file.write_all(&vec![0u8; PAGE_SIZE * pages]).unwrap();
        drop(file);

        let file = std::fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open(path)
            .unwrap();

        let mut mmap = unsafe { MmapMut::map_mut(&file).unwrap() };
        let mut header = FileHeader::new(768);
        header.page_count = pages as u32;
        crate::file::free_list::init_free_list(&mut header).unwrap();

        for page_id in (2..pages as u32).rev() {
            crate::file::free_list::free_page(&mut mmap, &mut header, page_id).unwrap();
        }

        let btree = BTreeIndex::new();
        (mmap, header, btree)
    }

    #[allow(clippy::too_many_arguments)]
    fn allocate_context_node_page(
        mmap: &mut MmapMut,
        header: &mut FileHeader,
        btree: &mut BTreeIndex,
        id_hash: u64,
        importance: f32,
        context_id: u64,
        edge_ptrs: Vec<u64>,
        file: &mut File,
    ) -> u32 {
        let page_id = allocate_page(
            mmap,
            header,
            PageType::ContextNode,
            1,
            SENTINEL_PAGE_ID,
            file,
        )
        .unwrap();
        let node = ContextNode {
            id_hash,
            context_id,
            vector_page_ref: 0,
            importance,
            valence: 0.0,
            arousal: 0.0,
            created_at: 0,
            updated_at: 0,
            version: 1,
            edge_ptrs,
        };
        write_page_data(mmap, page_id, &node.serialize().unwrap()).unwrap();
        btree.insert(id_hash, encode_page_ref(page_id, 0));
        page_id
    }

    fn allocate_hyperedge_page(
        mmap: &mut MmapMut,
        header: &mut FileHeader,
        btree: &mut BTreeIndex,
        id_hash: u64,
        weight: f32,
        node_ptrs: Vec<u64>,
        file: &mut File,
    ) -> u32 {
        let page_id =
            allocate_page(mmap, header, PageType::Hyperedge, 2, SENTINEL_PAGE_ID, file).unwrap();
        let edge = HyperedgeSlot {
            id_hash,
            kind: HyperedgeKind::Semantic,
            node_ptrs,
            weight,
            created_at: 0,
            updated_at: 0,
            version: 1,
            overflow_page: 0,
        };
        write_page_data(mmap, page_id, &edge.serialize().unwrap()).unwrap();
        btree.insert(id_hash, encode_page_ref(page_id, 0));
        page_id
    }

    fn read_hyperedge(mmap: &MmapMut, page_id: u32) -> HyperedgeSlot {
        let offset = crate::shared::slot_io::slot_offset(page_id);
        HyperedgeSlot::deserialize(&mmap[offset..offset + PAGE_SIZE - 32]).unwrap()
    }

    fn read_context_node(mmap: &MmapMut, page_id: u32) -> ContextNode {
        let offset = crate::shared::slot_io::slot_offset(page_id);
        ContextNode::deserialize(&mmap[offset..offset + PAGE_SIZE - 32]).unwrap()
    }

    #[test]
    fn test_rebuild_l1_cleans_edge_references() {
        let (mut mmap, mut header, mut btree) = create_mmap(20);
        let mut sparse_index = SparseIndex::new();

        let temp_file2 = tempfile::NamedTempFile::new().unwrap();
        let path2 = temp_file2.path();
        let mut file2 = File::create(path2).unwrap();
        use std::io::Write;
        file2.write_all(&vec![0u8; PAGE_SIZE * 20]).unwrap();
        drop(file2);
        let mut file2 = std::fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open(path2)
            .unwrap();

        // Stale L1 node points to an L2 context that no longer exists.
        let _stale_page = allocate_context_node_page(
            &mut mmap,
            &mut header,
            &mut btree,
            1,
            1.0,
            1000,
            vec![10],
            &mut file2,
        );
        // Edge connects the stale node and two surviving nodes.
        let edge_page = allocate_hyperedge_page(
            &mut mmap,
            &mut header,
            &mut btree,
            10,
            1.0,
            vec![1, 2, 3],
            &mut file2,
        );
        let node2_page = allocate_context_node_page(
            &mut mmap,
            &mut header,
            &mut btree,
            2,
            1.0,
            2000,
            vec![10],
            &mut file2,
        );
        let node3_page = allocate_context_node_page(
            &mut mmap,
            &mut header,
            &mut btree,
            3,
            1.0,
            2001,
            vec![10],
            &mut file2,
        );

        // Mark the surviving L2 contexts as present so only node 1 is stale.
        btree.insert(2000, 0);
        btree.insert(2001, 0);

        sparse_index.add_document(1, vec!["test".to_string()], 1);
        assert!(sparse_index.bm25_score(&["test".to_string()], 1) > 0.0);

        let dc = default_decay_config();
        let updated = rebuild_l1_from_l2(
            &mut mmap,
            &mut header,
            &mut btree,
            &mut sparse_index,
            &HashSet::new(),
            &dc,
        )
        .unwrap();
        assert_eq!(updated.len(), 1);

        // Stale node removed.
        assert!(btree.search(1).is_none());

        // Edge survives but no longer references the stale node.
        assert!(btree.search(10).is_some());
        let edge = read_hyperedge(&mmap, edge_page);
        assert!(!edge.node_ptrs.contains(&1));
        assert_eq!(edge.node_ptrs, vec![2, 3]);

        // Surviving nodes still reference the edge.
        let node2 = read_context_node(&mmap, node2_page);
        assert!(node2.edge_ptrs.contains(&10));
        let node3 = read_context_node(&mmap, node3_page);
        assert!(node3.edge_ptrs.contains(&10));

        // Sparse index entry for the stale node was removed.
        assert_eq!(sparse_index.bm25_score(&["test".to_string()], 1), 0.0);
    }

    #[test]
    fn test_rebuild_l1_removes_underpopulated_edge() {
        let (mut mmap, mut header, mut btree) = create_mmap(20);
        let mut sparse_index = SparseIndex::new();

        let temp_file2 = tempfile::NamedTempFile::new().unwrap();
        let path2 = temp_file2.path();
        let mut file2 = File::create(path2).unwrap();
        use std::io::Write;
        file2.write_all(&vec![0u8; PAGE_SIZE * 20]).unwrap();
        drop(file2);
        let mut file2 = std::fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open(path2)
            .unwrap();

        let _stale_page = allocate_context_node_page(
            &mut mmap,
            &mut header,
            &mut btree,
            1,
            1.0,
            1000,
            vec![10],
            &mut file2,
        );
        let _edge_page = allocate_hyperedge_page(
            &mut mmap,
            &mut header,
            &mut btree,
            10,
            1.0,
            vec![1, 2],
            &mut file2,
        );
        let node2_page = allocate_context_node_page(
            &mut mmap,
            &mut header,
            &mut btree,
            2,
            1.0,
            2000,
            vec![10],
            &mut file2,
        );

        // Mark the surviving L2 context as present so only node 1 is stale.
        btree.insert(2000, 0);

        let dc = default_decay_config();
        let updated = rebuild_l1_from_l2(
            &mut mmap,
            &mut header,
            &mut btree,
            &mut sparse_index,
            &HashSet::new(),
            &dc,
        )
        .unwrap();
        assert_eq!(updated.len(), 1);

        // Stale node and underpopulated edge removed.
        assert!(btree.search(1).is_none());
        assert!(btree.search(10).is_none());

        // Surviving node no longer references the removed edge.
        let node2 = read_context_node(&mmap, node2_page);
        assert!(node2.edge_ptrs.is_empty());
    }

    #[test]
    fn test_flush_l2_meta_to_mmap_syncs_activation_score() {
        let (mut mmap, mut header, mut btree, mut file) = create_test_mmap(32);
        let ctx = ContextSlot {
            id_hash: 101,
            parent_id: None,
            depth: 1,
            title: "test context".to_string(),
            summary: None,
            archive_refs: vec![],
            l3_refs: vec![],
            turn_count: 0,
            created_at: 0,
            updated_at: 0,
            version: 2,
            importance: 0.5,
            activation_score: 0.5,
            is_active: false,
            activation_state: ActivationState::Dormant,
            centroid_page_ref: 0,
            dialogue_range: (0, 0),
            llm_params: crate::layers::context::LlmParams::default(),
        };
        crate::test_helpers::insert_test_context(
            &mut mmap,
            &mut header,
            &mut btree,
            &mut SparseIndex::new(),
            ctx,
            &mut file,
        );

        let mut l2_meta = L2MetaIndex::build(&mmap, &btree);
        l2_meta.get_mut(101).unwrap().activation_score = 0.95;

        flush_l2_meta_to_mmap(&mut mmap, &btree, &l2_meta).unwrap();

        // Verify mmap was updated.
        let page_ref = btree.search(101).unwrap();
        let slot_data = crate::shared::slot_io::get_slot_data(&mmap[..], page_ref).unwrap();
        let updated = ContextSlot::deserialize_slot(slot_data).unwrap();
        assert!((updated.activation_score - 0.95).abs() < f32::EPSILON);
        assert_eq!(updated.activation_state, ActivationState::Dormant);
        assert_eq!(updated.version, 3);

        // Verify rebuilt index reflects the flushed value.
        let rebuilt = L2MetaIndex::build(&mmap, &btree);
        assert!((rebuilt.get(101).unwrap().activation_score - 0.95).abs() < f32::EPSILON);

        let _ = file;
    }

    #[cfg(feature = "llm")]
    mod pipeline_tests {
        use super::*;
        use crate::config::LlmConfig;
        use crate::dream::openai_compatible::OpenAICompatibleLlmProvider;
        use crate::layers::context::{ActivationState, ContextSlot, LlmParams};
        use crate::layers::pathway::PathwayWeightSlot;
        use crate::test_helpers::{create_test_mmap, insert_test_context};

        fn make_llm() -> OpenAICompatibleLlmProvider {
            OpenAICompatibleLlmProvider::new(LlmConfig {
                api_url: "https://api.example.com/v1/chat/completions".to_string(),
                api_key: "test-key".to_string(),
                model: "test-model".to_string(),
                ..Default::default()
            })
        }

        fn make_active_context(id_hash: u64, title: &str) -> ContextSlot {
            ContextSlot {
                id_hash,
                parent_id: None,
                depth: 1,
                title: title.to_string(),
                summary: Some(format!("summary of {}", title)),
                archive_refs: vec![],
                l3_refs: vec![],
                turn_count: 3,
                created_at: 0,
                updated_at: 0,
                version: 2,
                importance: 0.8,
                activation_score: 0.9,
                is_active: true,
                activation_state: ActivationState::Active,
                centroid_page_ref: 0,
                dialogue_range: (0, 0),
                llm_params: LlmParams::default(),
            }
        }

        #[test]
        fn test_dream_with_l2_ids() {
            let (mut mmap, mut header, mut btree, mut file) = create_test_mmap(64);
            let mut sparse = SparseIndex::new();
            let l2_meta = L2MetaIndex::new();

            insert_test_context(
                &mut mmap,
                &mut header,
                &mut btree,
                &mut sparse,
                make_active_context(101, "topic one"),
                &mut file,
            );
            insert_test_context(
                &mut mmap,
                &mut header,
                &mut btree,
                &mut sparse,
                make_active_context(102, "topic two"),
                &mut file,
            );

            let llm = make_llm();
            let report = dream_pipeline(
                &mut mmap,
                &mut header,
                &mut btree,
                &mut sparse,
                &llm,
                Some(vec![101]),
                &mut file,
                &default_decay_config(),
                &l2_meta,
            )
            .unwrap();

            let demoted_ids: Vec<u64> = report
                .demoted_to_secondary
                .iter()
                .map(|d| u64::from_str_radix(&d.context_id, 16).unwrap())
                .collect();
            assert!(demoted_ids.contains(&101));
            assert!(!demoted_ids.contains(&102));

            let ctx_101 = read_context_slot(&mmap, &btree, 101);
            let ctx_102 = read_context_slot(&mmap, &btree, 102);
            assert_eq!(ctx_101.depth, 2);
            assert_eq!(ctx_102.depth, 1);

            let _ = file;
        }

        #[test]
        fn test_dream_l6_decay() {
            let (mut mmap, mut header, mut btree, mut file) = create_test_mmap(64);
            let mut sparse = SparseIndex::new();
            let l2_meta = L2MetaIndex::new();

            let old = (now_ms() - 100_000) as u64; // ~1.7 minutes ago
            let pathway = PathwayWeightSlot {
                id_hash: 6001,
                source_node: "condition:deploy".into(),
                target_node: "action:restart".into(),
                weight: 1.0,
                trigger_count: 1,
                success_rate: 0.9,
                last_accessed: old,
                metadata: "{}".into(),
                created_at: old as i64,
                updated_at: old as i64,
                version: 1,
            };
            crate::query::l6_ops::add_l6(&mut mmap, &mut header, &btree, &mut file, pathway)
                .unwrap();

            let llm = make_llm();
            let report = dream_pipeline(
                &mut mmap,
                &mut header,
                &mut btree,
                &mut sparse,
                &llm,
                None,
                &mut file,
                &default_decay_config(),
                &l2_meta,
            )
            .unwrap();

            assert!(report.l6_decayed >= 1, "expected at least one L6 decay");
            assert_eq!(report.l6_pruned, 0);

            let list = crate::query::l6_ops::list_l6(&mmap, &header, &btree, None).unwrap();
            assert_eq!(list.len(), 1);
            assert!(list[0].weight < 1.0);
            assert!(list[0].weight > 0.05);

            let _ = file;
        }

        fn read_context_slot(mmap: &MmapMut, btree: &BTreeIndex, id_hash: u64) -> ContextSlot {
            let page_ref = btree.search(id_hash).unwrap();
            let slot_data = crate::shared::slot_io::get_slot_data(&mmap[..], page_ref).unwrap();
            ContextSlot::deserialize_slot(slot_data).unwrap()
        }
    }
}
