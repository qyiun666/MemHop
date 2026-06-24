// Dream module
pub mod compress_stage;
pub mod crystallize_stage;
pub mod emotion;
pub mod habit_distill_stage;
pub mod l0_form_stage;
pub mod l1_decay;
pub mod l3_distill_stage;
pub mod llm;
pub mod openai_compatible;
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
/// 4. L3 Distillation: extract structured knowledge into L3 hypergraph via LLM
/// 5. L5 Crystallization: scan all ActionChainSlots and extract crystals
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
        l1_decayed_nodes: 0,
        l1_pruned_edges: 0,
        l1_removed_nodes: 0,
        l1_removed_edges: 0,
        l0_updated: None,
        habits_updated: None,
        new_l3_nodes: Vec::new(),
        new_crystals: Vec::new(),
        pruned_crystals: Vec::new(),
        duration_ms: 0,
    };

    // Stage 1: L3 Knowledge Distillation - extract structured knowledge via LLM
    // This must run BEFORE L2 compression because compression demotes active
    // depth-1 contexts to depth-2 and creates new (initially inactive) compressed
    // contexts. Running L3 distillation first lets it process the original active
    // depth-1 topics while their full summaries are still intact.
    let l3_nodes = l3_distill_stage::distill_l3_knowledge(
        mmap,
        header,
        btree,
        sparse_index,
        llm,
        &session_topic_ids,
    )?;
    report.new_l3_nodes = l3_nodes;

    // Stage 2: L2 Compression - depth demotion on active contexts
    let (demoted_sec, compressed, removed, demoted_ter) = compress_stage::compress_active_contexts(
        mmap,
        header,
        btree,
        sparse_index,
        llm,
        &session_topic_ids,
    )?;
    report.demoted_to_secondary = demoted_sec;
    report.new_compressed = compressed;
    report.removed_contexts = removed;
    report.demoted_to_tertiary = demoted_ter;

    // Stage 3: L1 Update - rebuild L1 ContextNode based on updated L2
    // L1 nodes point to L2 contexts; after L2 depth changes, L1 associations need refresh
    let l1_updated = rebuild_l1_from_l2(mmap, header, btree, sparse_index, &session_topic_ids)?;
    report.l1_updated = l1_updated;

    // Stage 3b: L1 Decay - time-decay node importance and prune weak edges
    let l1_decay_report = l1_decay::decay_l1_network(mmap, header, btree)?;
    report.l1_decayed_nodes = l1_decay_report.decayed_nodes;
    report.l1_pruned_edges = l1_decay_report.pruned_edges;
    report.l1_removed_nodes = l1_decay_report.removed_nodes;
    report.l1_removed_edges = l1_decay_report.removed_edges;

    // Stage 4: L0 Update - regenerate profile from knowledge distribution
    l0_form_stage::generate_profile(mmap, header, btree, sparse_index)?;
    // Mark L0 as updated if we have any topics
    if !session_topic_ids.is_empty() {
        report.l0_updated = Some((
            "profile".to_string(),
            vec!["personality".to_string(), "preferences".to_string()],
        ));
    }

    // Stage 4.5: User Language Habit Distillation
    // Analyzes recent dialogues to learn user language patterns and merge into L0 profile
    let habit_update = habit_distill_stage::distill_user_habits(mmap, header, btree, llm)?;
    if habit_update.new_lexicon > 0
        || habit_update.new_style_traits > 0
        || habit_update.new_emotion_patterns > 0
    {
        report.habits_updated = Some(habit_distill_stage::HabitUpdate {
            new_lexicon: habit_update.new_lexicon,
            new_style_traits: habit_update.new_style_traits,
            new_emotion_patterns: habit_update.new_emotion_patterns,
            total_dialogues_analyzed: habit_update.total_dialogues_analyzed,
        });
    }

    // Stage 5: L5 Crystallization - scan all ActionChainSlots, extract crystals
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
    sparse_index: &mut SparseIndex,
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
        if let Ok(page_hdr) = crate::file::page::read_page_header(&mmap[..], page_id) {
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
        // Clean up references from this stale node to edges before freeing it.
        let page_ref = crate::file::page::encode_page_ref(page_id, 0);
        if let Some(slot_data) = crate::query::slot_io::get_slot_data(&mmap[..], page_ref) {
            if let Ok(node) = ContextNode::deserialize(slot_data) {
                for edge_id in &node.edge_ptrs {
                    crate::dream::l1_decay::remove_node_from_edge(
                        mmap, btree, header, *edge_id, id_hash,
                    )?;
                }
            }
        }

        btree.remove(id_hash);
        let offset = (page_id as usize) * crate::util::PAGE_SIZE;
        mmap[offset..offset + crate::util::PAGE_SIZE].fill(0);
        crate::file::free_list::free_page(mmap, header, page_id)?;
        sparse_index.remove_document(id_hash);
        updated_ids.push(format!("{:016x}", id_hash));
    }

    Ok(updated_ids)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::file::header::FileHeader;
    use crate::file::page::{allocate_page, encode_page_ref, write_page_data};
    use crate::index::btree::BTreeIndex;
    use crate::index::sparse::SparseIndex;
    use crate::slot::context_node::ContextNode;
    use crate::slot::hyperedge::{HyperedgeKind, HyperedgeSlot};
    use crate::util::{PageType, PAGE_SIZE};
    use memmap2::MmapMut;
    use std::fs::File;
    use std::io::Write;

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

    fn allocate_context_node_page(
        mmap: &mut MmapMut,
        header: &mut FileHeader,
        btree: &mut BTreeIndex,
        id_hash: u64,
        importance: f32,
        context_id: u64,
        edge_ptrs: Vec<u64>,
    ) -> u32 {
        let page_id = allocate_page(mmap, header, PageType::ContextNode, 1, 0xFFFFFFFF).unwrap();
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
    ) -> u32 {
        let page_id = allocate_page(mmap, header, PageType::Hyperedge, 2, 0xFFFFFFFF).unwrap();
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
        let offset = (page_id as usize) * PAGE_SIZE + 32;
        HyperedgeSlot::deserialize(&mmap[offset..offset + PAGE_SIZE - 32]).unwrap()
    }

    fn read_context_node(mmap: &MmapMut, page_id: u32) -> ContextNode {
        let offset = (page_id as usize) * PAGE_SIZE + 32;
        ContextNode::deserialize(&mmap[offset..offset + PAGE_SIZE - 32]).unwrap()
    }

    #[test]
    fn test_rebuild_l1_cleans_edge_references() {
        let (mut mmap, mut header, mut btree) = create_mmap(20);
        let mut sparse_index = SparseIndex::new();

        // Stale L1 node points to an L2 context that no longer exists.
        let _stale_page =
            allocate_context_node_page(&mut mmap, &mut header, &mut btree, 1, 1.0, 1000, vec![10]);
        // Edge connects the stale node and two surviving nodes.
        let edge_page =
            allocate_hyperedge_page(&mut mmap, &mut header, &mut btree, 10, 1.0, vec![1, 2, 3]);
        let node2_page =
            allocate_context_node_page(&mut mmap, &mut header, &mut btree, 2, 1.0, 2000, vec![10]);
        let node3_page =
            allocate_context_node_page(&mut mmap, &mut header, &mut btree, 3, 1.0, 2001, vec![10]);

        // Mark the surviving L2 contexts as present so only node 1 is stale.
        btree.insert(2000, 0);
        btree.insert(2001, 0);

        sparse_index.add_document(1, vec!["test".to_string()], 1);
        assert!(sparse_index.bm25_score(&["test".to_string()], 1) > 0.0);

        let updated = rebuild_l1_from_l2(
            &mut mmap,
            &mut header,
            &mut btree,
            &mut sparse_index,
            &HashSet::new(),
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

        let _stale_page =
            allocate_context_node_page(&mut mmap, &mut header, &mut btree, 1, 1.0, 1000, vec![10]);
        let _edge_page =
            allocate_hyperedge_page(&mut mmap, &mut header, &mut btree, 10, 1.0, vec![1, 2]);
        let node2_page =
            allocate_context_node_page(&mut mmap, &mut header, &mut btree, 2, 1.0, 2000, vec![10]);

        // Mark the surviving L2 context as present so only node 1 is stale.
        btree.insert(2000, 0);

        let updated = rebuild_l1_from_l2(
            &mut mmap,
            &mut header,
            &mut btree,
            &mut sparse_index,
            &HashSet::new(),
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
}
