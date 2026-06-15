// Dream module
pub mod compress_stage;
pub mod crystallize_stage;
pub mod emotion;
pub mod openai_compatible;
pub mod l0_form_stage;
pub mod l3_distill_stage;
pub mod llm;
pub mod prune;

use crate::dream::llm::LlmProvider;
use crate::dream::prune::DreamReport;
use crate::file::header::FileHeader;
use crate::index::btree::BTreeIndex;
use crate::index::sparse::SparseIndex;
use crate::MemHopError;
use memmap2::MmapMut;
use std::collections::HashSet;

/// Main dream pipeline - memory consolidation through depth demotion
///
/// This function orchestrates the complete dream consolidation process:
/// 1. L2 Compression: depth demotion (主→次→次次→remove) on active contexts
/// 2. L1 Update: rebuild L1 ContextNode associations based on updated L2
/// 3. L0 Update: regenerate L0 profile from L1 knowledge distribution
/// 4. L5 Crystallization: scan all ActionChainSlots and extract crystals
///
/// # Arguments
/// * `mmap` - Mutable memory-mapped file for reading/writing memory slots
/// * `header` - File header for page allocation and free list management
/// * `btree` - B-tree index for topic lookup
/// * `sparse_index` - Sparse index for keyword lookup
/// * `llm` - LLM provider for summarization and crystal generation
/// * `session_topic_ids` - Set of active topic IDs from current session
///
/// # Returns
/// DreamReport containing statistics about all operations performed
pub fn dream_pipeline(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    llm: &dyn LlmProvider,
    session_topic_ids: HashSet<u64>,
) -> Result<DreamReport, MemHopError> {
    let start_time = std::time::Instant::now();

    let mut report = DreamReport {
        demoted_to_secondary: Vec::new(),
        demoted_to_tertiary: Vec::new(),
        removed_contexts: Vec::new(),
        new_compressed: Vec::new(),
        l1_updated: Vec::new(),
        l0_updated: None,
        new_crystals: Vec::new(),
        pruned_crystals: Vec::new(),
        duration_ms: 0,
    };

    // Stage 1: L2 Compression - depth demotion on active contexts
    let (demoted_sec, compressed, removed, demoted_ter) =
        compress_stage::compress_active_contexts(
            mmap, header, btree, sparse_index, llm, &session_topic_ids
        )?;
    report.demoted_to_secondary = demoted_sec;
    report.new_compressed = compressed;
    report.removed_contexts = removed;
    report.demoted_to_tertiary = demoted_ter;

    // Stage 2: L1 Update - rebuild L1 ContextNode based on updated L2
    // L1 nodes point to L2 contexts; after L2 depth changes, L1 associations need refresh
    let l1_updated = rebuild_l1_from_l2(mmap, header, btree, &session_topic_ids)?;
    report.l1_updated = l1_updated;

    // Stage 3: L0 Update - regenerate profile from knowledge distribution
    l0_form_stage::generate_profile(mmap, header, btree, sparse_index)?;
    // Mark L0 as updated if we have any topics
    if !session_topic_ids.is_empty() {
        report.l0_updated = Some(("profile".to_string(), vec!["personality".to_string(), "preferences".to_string()]));
    }

    // Stage 4: L5 Crystallization - scan all ActionChainSlots, extract crystals
    let crystals = crystallize_stage::crystallize_patterns(mmap, header, btree, llm)?;
    report.new_crystals = crystals;

    // Prune low-quality crystals
    let page_count = header.page_count;
    let pruned = crystallize_stage::prune_low_quality_crystals(mmap, header, btree, page_count)?;
    report.pruned_crystals = pruned;

    report.duration_ms = start_time.elapsed().as_millis() as u64;

    Ok(report)
}

/// Rebuild L1 ContextNode associations based on updated L2 contexts
///
/// After L2 depth demotion, L1 graph nodes need to be refreshed:
/// - Remove L1 nodes pointing to removed contexts (pages freed)
/// - Validate L1 node references still point to valid L2 contexts
fn rebuild_l1_from_l2(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    _session_topic_ids: &HashSet<u64>,
) -> Result<Vec<String>, MemHopError> {
    use crate::slot::context_node::ContextNode;
    use crate::util::PageType;

    let page_count = header.page_count;

    // Phase 1: Collect stale L1 node IDs (read-only scan)
    let mut stale_nodes: Vec<(u64, u32)> = Vec::new(); // (id_hash, page_id)
    let entries: Vec<(u64, u64)> = btree.iter().map(|(k, v)| (*k, *v)).collect();

    for (id_hash, page_ref) in &entries {
        let page_id = (page_ref >> 16) as u32;
        if page_id >= page_count {
            continue;
        }

        let page_offset = (page_id as usize) * crate::util::PAGE_SIZE;
        if page_offset + crate::util::PAGE_SIZE > mmap.len() {
            continue;
        }

        // Check page type — only process ContextNode pages
        let ro_mmap = unsafe { &*(mmap.as_ptr() as *const memmap2::Mmap) };
        if let Ok(page_hdr) = crate::file::page::read_page_header(ro_mmap, page_id) {
            if page_hdr.page_type != PageType::ContextNode as u16 {
                continue;
            }
        } else {
            continue;
        }

        // Deserialize ContextNode and check if its target L2 still exists
        if let Some(slot_data) = crate::query::slot_io::get_slot_data(&mmap[..], *page_ref) {
            if let Ok(node) = ContextNode::deserialize(slot_data) {
                if btree.search(node.context_id).is_none() {
                    stale_nodes.push((*id_hash, page_id));
                }
            }
        }
    }

    // Phase 2: Remove stale L1 nodes (write phase)
    let mut updated_ids: Vec<String> = Vec::new();
    for (id_hash, page_id) in stale_nodes {
        btree.remove(id_hash);
        let offset = (page_id as usize) * crate::util::PAGE_SIZE;
        mmap[offset..offset + crate::util::PAGE_SIZE].fill(0);
        crate::file::free_list::free_page(mmap, header, page_id)?;
        updated_ids.push(format!("{:016x}", id_hash));
    }

    Ok(updated_ids)
}
