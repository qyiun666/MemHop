// Dream module
pub mod compress_stage;
pub mod cooccurrence_stage;
pub mod crystallize_stage;
pub mod decay_stage;
pub mod deepseek_llm;
pub mod distill_stage;
pub mod emotion;
pub mod l0_form_stage;
pub mod llm;
pub mod merge_stage;
pub mod prune;
pub mod reflect_stage;
pub mod temporal_stage;

use crate::dream::llm::LlmProvider;
use crate::dream::prune::{DreamConfig, DreamReport};
use crate::file::header::FileHeader;
use crate::index::btree::BTreeIndex;
use crate::index::sparse::SparseIndex;
use crate::MemHopError;
use memmap2::MmapMut;
use std::collections::HashSet;

/// Main dream pipeline - eight stages of memory consolidation
///
/// This function orchestrates the complete dream consolidation process,
/// executing all eight stages in sequence to transform episodic memories
/// into structured knowledge.
///
/// # Arguments
/// * `mmap` - Mutable memory-mapped file for reading/writing memory slots
/// * `config` - Dream configuration with thresholds and flags
/// * `header` - File header for page allocation and free list management
/// * `btree` - B-tree index for topic lookup
/// * `sparse_index` - Sparse index for keyword lookup
/// * `llm` - LLM provider for summarization and knowledge generation
/// * `session_topic_ids` - Set of active topic IDs from current session
///
/// # Returns
/// DreamReport containing statistics about all operations performed
pub fn dream_pipeline(
    mmap: &mut MmapMut,
    config: DreamConfig,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,  // Changed to mutable
    sparse_index: &SparseIndex,
    llm: &dyn LlmProvider,
    session_topic_ids: HashSet<u64>,
) -> Result<DreamReport, MemHopError> {
    let mut report = DreamReport {
        new_topics: Vec::new(),
        new_domain_nodes: Vec::new(),
        new_crystals: Vec::new(),
        merged_topics: Vec::new(),
        pruned: Vec::new(),
        demoted_to_dormant: Vec::new(),
        new_temporal_edges: Vec::new(),
        new_cooccurrence_edges: Vec::new(),
    };

    // Stage 1: Decay - Demote low-activation memories to Dormant
    use crate::activation::ActivationConfig;
    let activation_config = ActivationConfig::default();
    let page_count = header.page_count;

    let dormant_ids = decay_stage::apply_decay(mmap, page_count, config.prune_threshold, &activation_config)?;
    report.demoted_to_dormant = dormant_ids;

    // Stage 2: Temporal Bind - Create Temporal hyperedges between time-adjacent engrams
    let temporal_edges =
        temporal_stage::create_temporal_edges(mmap, header, page_count, config.time_window)?;
    report.new_temporal_edges = temporal_edges;

    // Stage 3: Topic Merge - Merge similar topics based on Jaccard similarity
    let merged = merge_stage::merge_similar_topics(mmap, header, btree, 0.5)?;
    report.merged_topics = merged;

    // Stage 4: Topic Reflect - Aggregate keywords and generate summaries
    let reflected_topics = reflect_stage::reflect_all_topics(mmap, btree)?;
    report.new_topics.extend(reflected_topics);

    // Stage 5: Co-occurrence - Create co-occurrence hyperedges
    let cooccur_edges = cooccurrence_stage::create_cooccurrence_edges(mmap, header, sparse_index, &session_topic_ids)?;
    report.new_cooccurrence_edges = cooccur_edges;

    // Stage 6: L1→L2 Compression - Cluster recent engrams into L2 topics (needs LLM)
    if config.compress_l2 {
        let new_topics = compress_stage::compress_l1_to_l2(
            mmap, 
            header, 
            btree, 
            sparse_index, 
            llm, 
            config.time_window
        )?;
        report.new_topics.extend(new_topics);
    }

    // Stage 7: L1→L3 Distillation - Extract procedural knowledge into L3 (needs LLM)
    if config.distill_l3 {
        let new_domains = distill_stage::distill_l1_to_l3(
            mmap, 
            header, 
            btree, 
            sparse_index, 
            llm
        )?;
        report.new_domain_nodes = new_domains;
    }

    // Stage 8: L5 Crystallization - Generate procedural knowledge crystals (needs LLM)
    if config.crystallize_l5 {
        let crystals = crystallize_stage::crystallize_patterns(
            mmap, 
            header, 
            btree, 
            llm
        )?;
        report.new_crystals = crystals;
    }

    // Prune low-quality crystals after crystallization
    let pruned = crystallize_stage::prune_low_quality_crystals(mmap, header, page_count)?;
    report.pruned.extend(pruned);

    // Final: L0 Profile Generation - Extract agent persona from topic keywords
    l0_form_stage::generate_profile(mmap, header, btree, sparse_index)?;

    Ok(report)
}
