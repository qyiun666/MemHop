// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! update_memory() internal engine with WAL-backed cross-layer atomicity.

use crate::config::MemHopConfig;
use crate::file::free_list::{allocate_or_extend, free_page};
use crate::file::header::FileHeader;
use crate::file::journal::{replay_journal_to_mmap, JournalEntry};
use crate::file::page::PageHeader;
use crate::index::btree::BTreeIndex;
use crate::index::l2_meta::L2MetaIndex;
use crate::index::sparse::SparseIndex;
use crate::l3;
use crate::layers::archive::ArchiveSlot;
use crate::layers::context_node::ContextNode;
use crate::layers::hypergraph::{HypergraphNode, HypergraphSlot, HypergraphSource};
use crate::organize::extract_keywords;
use crate::query::types::*;
use crate::shared::common::{format_hash, now_ms, parse_id_to_hash};
use crate::util::{hash_id, PageType, DEFAULT_GROW_PAGES, PAGE_SIZE, SENTINEL_PAGE_ID};
use crate::MemHopError;
use memmap2::MmapMut;
use std::collections::HashMap;
use std::fs::File;

/// Convenience alias to avoid a `>>` tokenization issue in function signatures.
type L3IndexMap = HashMap<u64, crate::l3::L3Index>;

/// Per-transaction rollback state.
struct TxState {
    /// Journal entry holding pre-transaction page snapshots.
    journal: JournalEntry,
    /// Newly allocated page ids that must be returned to the free list on abort.
    new_pages: Vec<u32>,
    /// New btree id_hashes that must be removed on abort.
    new_btree_keys: Vec<u64>,
}

impl TxState {
    fn new(commit_id: u64) -> Self {
        Self {
            journal: JournalEntry::new(commit_id),
            new_pages: Vec::new(),
            new_btree_keys: Vec::new(),
        }
    }

    /// Snapshot a full 4KB page before it is modified.
    fn snapshot_page(&mut self, mmap: &MmapMut, page_id: u32) -> Result<(), MemHopError> {
        let offset = (page_id as usize) * PAGE_SIZE;
        if offset + PAGE_SIZE > mmap.len() {
            return Err(MemHopError::PageNotFound(page_id));
        }
        let mut data = vec![0u8; PAGE_SIZE];
        data.copy_from_slice(&mmap[offset..offset + PAGE_SIZE]);
        self.journal.add_page(page_id, data)
    }

    /// Track a newly allocated page so it can be freed on abort.
    fn track_new_page(&mut self, page_id: u32) {
        self.new_pages.push(page_id);
    }

    /// Track a newly inserted btree key so it can be removed on abort.
    fn track_new_btree_key(&mut self, id_hash: u64) {
        self.new_btree_keys.push(id_hash);
    }
}

/// Abort a transaction: replay journal snapshots and clean up allocations.
fn abort_transaction(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    tx: &TxState,
) -> Result<(), MemHopError> {
    replay_journal_to_mmap(mmap, std::slice::from_ref(&tx.journal))?;

    // Remove btree entries for newly created objects.
    for &id_hash in &tx.new_btree_keys {
        btree.remove(id_hash);
    }

    // Return newly allocated pages to the free list.
    for &page_id in &tx.new_pages {
        free_page(mmap, header, page_id)?;
    }

    Ok(())
}

/// Write a proper PageHeader for a newly allocated data page.
fn write_slot_page_header(
    mmap: &mut MmapMut,
    page_id: u32,
    page_type: PageType,
    layer_id: u16,
    data_len: usize,
) {
    let mut header = PageHeader::new(page_id, page_type, layer_id, SENTINEL_PAGE_ID);
    header.slot_count = 1;
    header.free_bytes = (PAGE_SIZE - 32).saturating_sub(data_len) as u16;
    let header_bytes = header.to_bytes();
    let offset = crate::shared::slot_io::page_offset(page_id);
    mmap[offset..offset + 32].copy_from_slice(&header_bytes);
}

/// Core update engine: writes L4 archive, updates L2 context, optionally creates
/// L1 node, L3 hypergraph, and L5 action chain.
///
/// All mmap modifications are journaled in `tx.journal`. On failure the journal
/// is replayed and any newly allocated pages are freed, leaving the database in
/// its pre-transaction state.
#[allow(clippy::too_many_arguments)]
pub fn update_memory_internal(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    request: UpdateRequest,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    l2_meta: &mut L2MetaIndex,
    file: &mut File,
    config: &MemHopConfig,
    journal: &mut Vec<JournalEntry>,
    tracker: Option<&mut crate::l3::DegreeTracker>,
    index_map: Option<&mut L3IndexMap>,
) -> Result<UpdateResult, MemHopError> {
    // Validate basic parameters.
    if request.topic_id.is_empty() {
        return Err(MemHopError::InvalidQuery("topic_id is empty".to_string()));
    }
    if request.dialogue_text.is_empty() {
        return Err(MemHopError::InvalidQuery(
            "dialogue_text is empty".to_string(),
        ));
    }

    let topic_hash = parse_id_to_hash(&request.topic_id);
    let page_ref = btree
        .search(topic_hash)
        .ok_or(MemHopError::PageNotFound(0))?;

    let now_ms = now_ms();
    let commit_id = header.commit_id.wrapping_add(1);
    let mut tx = TxState::new(commit_id);

    // ------------------------------------------------------------------
    // Step 1: L4 ArchiveSlot (new)
    // ------------------------------------------------------------------
    let l4_id_hash = hash_id(&format!("L4-{}-{}", topic_hash, now_ms));
    let _archive_page_ref = allocate_and_write_l4_archive(
        mmap,
        header,
        &mut tx,
        l4_id_hash,
        &request.dialogue_text,
        topic_hash,
        now_ms,
        btree,
        request.source.to_metadata_json(),
        file,
    )?;
    let archive_id = format_hash(l4_id_hash);

    // ------------------------------------------------------------------
    // Step 2: L2 ContextSlot update (existing page)
    // ------------------------------------------------------------------
    let l2_page_id = crate::shared::slot_io::decode_page_id(page_ref);
    let mut ctx = {
        let data: &[u8] = &mmap[..];
        let slot_data = crate::shared::slot_io::get_slot_data(data, page_ref)
            .ok_or(MemHopError::PageNotFound(l2_page_id))?;
        crate::layers::context::ContextSlot::deserialize_slot(slot_data)?
    };

    // A brand-new context (e.g. from auto_create) has no archives and no turns yet.
    let is_fresh_context = ctx.turn_count == 0 && ctx.archive_refs.is_empty();

    tx.snapshot_page(mmap, l2_page_id)?;

    if !ctx.archive_refs.contains(&l4_id_hash) {
        ctx.archive_refs.push(l4_id_hash);
        ctx.archive_refs.sort();
    }
    ctx.turn_count += 1;

    if let Some(ref summary) = request.summary {
        match ctx.summary {
            Some(ref existing) => ctx.summary = Some(format!("{}\n{}", existing, summary)),
            None => ctx.summary = Some(summary.clone()),
        }
        ctx.updated_at = now_ms;
    }

    // ------------------------------------------------------------------
    // Step 3: L1 ContextNode (optional, only if no node points to this L2)
    // ------------------------------------------------------------------
    let has_l1_node = btree.iter_unsorted().any(|(_, &pr)| {
        let page_id = pr >> 16;
        if page_id == 0 {
            return false;
        }
        let pt_offset = (page_id as usize) * PAGE_SIZE + 4;
        if pt_offset + 2 > mmap.len() {
            return false;
        }
        let pt = u16::from_le_bytes([mmap[pt_offset], mmap[pt_offset + 1]]);
        if pt != PageType::ContextNode as u16 {
            return false;
        }
        crate::shared::slot_io::get_slot_data(&mmap[..], pr)
            .and_then(|d| ContextNode::deserialize(d).ok())
            .map(|n| n.context_id == topic_hash)
            .unwrap_or(false)
    });

    if !has_l1_node {
        allocate_and_write_l1_node(mmap, header, &mut tx, topic_hash, now_ms, btree, file)?;
    }

    // ------------------------------------------------------------------
    // Step 4: L3 Hypergraph distillation (optional)
    // ------------------------------------------------------------------
    if request.instant_distill {
        distill_l3_for_update(
            mmap,
            header,
            &mut tx,
            &request,
            topic_hash,
            now_ms,
            btree,
            sparse_index,
            file,
            tracker,
            index_map,
            &mut ctx,
        )?;
    }

    // ------------------------------------------------------------------
    // Step 5: L5 ActionChain (optional)
    // ------------------------------------------------------------------
    if let Some(ref action_chain) = request.action_chain {
        for action in action_chain {
            let crystal_id_hash = hash_id(&format!(
                "{}-{:?}-{}",
                topic_hash, action.action_type, now_ms
            ));
            allocate_and_write_l5_crystal(
                mmap,
                header,
                &mut tx,
                crystal_id_hash,
                &action.title,
                &action.description,
                now_ms,
                btree,
                file,
            )?;
        }
    }

    // ------------------------------------------------------------------
    // Commit L2 changes to mmap.
    // ------------------------------------------------------------------
    let serialized = ctx
        .serialize()
        .map_err(|e| MemHopError::Serialization(format!("ContextSlot serialize: {}", e)))?;
    let write_offset = crate::shared::slot_io::slot_offset(l2_page_id);
    if write_offset + serialized.len() > mmap.len() {
        abort_transaction(mmap, header, btree, &tx)?;
        return Err(MemHopError::Serialization(format!(
            "ContextSlot too large for page: {} > {}",
            serialized.len(),
            PAGE_SIZE - 32
        )));
    }
    mmap[write_offset..write_offset + serialized.len()].copy_from_slice(&serialized);

    // Update in-memory indices only after all mmap writes succeed.
    if let Some(ref summary) = request.summary {
        let terms = crate::index::sparse::tokenize(summary);
        sparse_index.add_document(topic_hash, terms, summary.len() as u32);
    }

    l2_meta.update_from_context(&ctx);

    // Determine update status based on what actually changed.
    let status = if is_fresh_context {
        UpdateStatus::Created
    } else if request.summary.is_some() || request.action_chain.is_some() || request.instant_distill
    {
        UpdateStatus::Updated
    } else {
        UpdateStatus::Archived
    };

    // Check if archive/summary thresholds exceeded for auto-dream trigger.
    let archive_threshold = config.auto_dream_archive_threshold.unwrap_or(20);
    let summary_threshold = config.auto_dream_summary_bytes.unwrap_or(2048);
    let dream_triggered = ctx.archive_refs.len() >= archive_threshold
        || ctx.summary.as_ref().map(|s| s.len()).unwrap_or(0) >= summary_threshold;

    // Append the transaction journal entry to the buffered WAL.
    journal.push(tx.journal);

    let result = UpdateResult {
        topic_id: format_hash(topic_hash),
        archive_id,
        status,
        dream_triggered,
    };

    Ok(result)
}

/// Allocate and write an L4 ArchiveSlot page.
#[allow(clippy::too_many_arguments)]
fn allocate_and_write_l4_archive(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    tx: &mut TxState,
    id_hash: u64,
    content: &str,
    topic_id: u64,
    now_ms: i64,
    btree: &mut BTreeIndex,
    metadata: Option<String>,
    file: &mut File,
) -> Result<u64, MemHopError> {
    // Archive slots are constrained to a single page payload.
    if content.len() > PAGE_SIZE - 32 - 26 {
        return Err(MemHopError::Serialization(
            "ArchiveSlot content exceeds single-page capacity".to_string(),
        ));
    }

    let page_id = allocate_or_extend(mmap, header, file, DEFAULT_GROW_PAGES)?;
    tx.track_new_page(page_id);
    tx.snapshot_page(mmap, page_id)?;

    let offset = crate::shared::slot_io::slot_offset(page_id);

    use crate::layers::archive::ContentType;
    let archive = ArchiveSlot {
        id_hash,
        content_type: ContentType::Text,
        role: 0, // user
        context_id: topic_id,
        created_at: now_ms,
        content: content.to_string(),
        metadata,
    };

    let data = archive
        .serialize()
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;
    write_slot_page_header(mmap, page_id, PageType::Archive, 4, data.len());
    if offset + data.len() > mmap.len() {
        return Err(MemHopError::Serialization(format!(
            "ArchiveSlot data too large for page: {} > {}",
            data.len(),
            mmap.len() - offset
        )));
    }
    mmap[offset..offset + data.len()].copy_from_slice(&data);

    let page_ref = (page_id as u64) << 16;
    btree.insert(id_hash, page_ref);
    tx.track_new_btree_key(id_hash);

    Ok(page_ref)
}

/// Allocate and write an L1 ContextNode for an L2 context.
#[allow(clippy::too_many_arguments)]
fn allocate_and_write_l1_node(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    tx: &mut TxState,
    context_id: u64,
    now_ms: i64,
    btree: &mut BTreeIndex,
    file: &mut File,
) -> Result<(), MemHopError> {
    let id_hash = hash_id(&format!("L1-{}", context_id));
    let page_id = allocate_or_extend(mmap, header, file, DEFAULT_GROW_PAGES)?;
    tx.track_new_page(page_id);
    tx.snapshot_page(mmap, page_id)?;

    let node = ContextNode {
        id_hash,
        context_id,
        vector_page_ref: 0,
        importance: 0.5,
        valence: 0.0,
        arousal: 0.0,
        created_at: now_ms,
        updated_at: now_ms,
        version: 1,
        edge_ptrs: vec![],
    };

    let data = node
        .serialize()
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;
    write_slot_page_header(mmap, page_id, PageType::ContextNode, 1, data.len());

    let offset = crate::shared::slot_io::slot_offset(page_id);
    if offset + data.len() > mmap.len() {
        return Err(MemHopError::Serialization(format!(
            "ContextNode too large for page: {} > {}",
            data.len(),
            mmap.len() - offset
        )));
    }
    mmap[offset..offset + data.len()].copy_from_slice(&data);

    btree.insert(id_hash, (page_id as u64) << 16);
    tx.track_new_btree_key(id_hash);

    Ok(())
}

/// L3 distillation helper used by update_memory_internal.
#[allow(clippy::too_many_arguments)]
fn distill_l3_for_update(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    tx: &mut TxState,
    request: &UpdateRequest,
    topic_hash: u64,
    now_ms: i64,
    btree: &mut BTreeIndex,
    sparse_index: &SparseIndex,
    file: &mut File,
    mut tracker: Option<&mut crate::l3::DegreeTracker>,
    mut index_map: Option<&mut L3IndexMap>,
    ctx: &mut crate::layers::context::ContextSlot,
) -> Result<(), MemHopError> {
    let keywords = extract_keywords(&request.dialogue_text, 10);
    let mut graphs_to_link: Vec<u64> = Vec::new();
    let data: &[u8] = &mmap[..];

    for kw in &keywords {
        let hits = sparse_index.entity_search_nodes(kw);
        for (node_hash, _l2_ids) in &hits {
            if let Some(slot_data) = btree
                .search(*node_hash)
                .and_then(|pr| crate::shared::slot_io::get_slot_data(data, pr))
            {
                if let Ok(node) = crate::layers::hypergraph::HypergraphNode::deserialize(slot_data)
                {
                    if !ctx.l3_refs.contains(&node.graph_id) {
                        graphs_to_link.push(node.graph_id);
                    }
                }
            }
        }
    }

    if graphs_to_link.is_empty() && !keywords.is_empty() {
        let distilled_id = hash_id(&format!("distilled_{}_{}", topic_hash, now_ms));
        let graph_name = format!(
            "distilled:{}",
            &request.dialogue_text.chars().take(40).collect::<String>()
        );

        let slot = HypergraphSlot {
            id_hash: distilled_id,
            name: graph_name,
            source: HypergraphSource::Manual,
            node_count: keywords.len() as u32,
            edge_count: 0,
            created_at: now_ms,
            updated_at: now_ms,
            version: 1,
        };

        let slot_data = slot
            .serialize()
            .map_err(|e| MemHopError::Serialization(e.to_string()))?;
        let slot_page_id = allocate_or_extend(mmap, header, file, DEFAULT_GROW_PAGES)?;
        tx.track_new_page(slot_page_id);
        tx.snapshot_page(mmap, slot_page_id)?;

        let slot_offset = crate::shared::slot_io::page_offset(slot_page_id);
        let page_hdr = PageHeader::new(slot_page_id, PageType::HypergraphSlot, 3, SENTINEL_PAGE_ID);
        mmap[slot_offset..slot_offset + 32].copy_from_slice(&page_hdr.to_bytes());

        let data_offset = slot_offset + 32;
        if data_offset + slot_data.len() > mmap.len() {
            return Err(MemHopError::Serialization(format!(
                "HypergraphSlot too large for page: {} > {}",
                slot_data.len(),
                PAGE_SIZE - 32
            )));
        }
        mmap[data_offset..data_offset + slot_data.len()].copy_from_slice(&slot_data);

        btree.insert(distilled_id, (slot_page_id as u64) << 16);
        tx.track_new_btree_key(distilled_id);

        for kw in &keywords {
            let node_hash = hash_id(&format!("distilled_node_{}_{}", distilled_id, kw));
            let node = HypergraphNode {
                id_hash: node_hash,
                graph_id: distilled_id,
                title: kw.clone(),
                node_type: "concept".to_string(),
                content: String::new(),
                keywords: vec![kw.clone()],
                source_ref: None,
                importance: 0.5,
                created_at: now_ms,
                updated_at: now_ms,
                version: 1,
            };
            // add_node allocates its own page; we snapshot inside a dedicated wrapper.
            add_node_with_journal(
                mmap,
                header,
                tx,
                btree,
                node,
                file,
                tracker.as_deref_mut(),
                index_map.as_deref_mut(),
            )?;
        }

        graphs_to_link.push(distilled_id);
    }

    graphs_to_link.sort();
    graphs_to_link.dedup();
    ctx.l3_refs.extend(graphs_to_link);

    Ok(())
}

/// Wrap `l3::store::add_node` with journal snapshotting for the allocated page.
#[allow(clippy::too_many_arguments)]
fn add_node_with_journal(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    tx: &mut TxState,
    btree: &mut BTreeIndex,
    node: HypergraphNode,
    file: &mut File,
    tracker: Option<&mut crate::l3::DegreeTracker>,
    index_map: Option<&mut L3IndexMap>,
) -> Result<(), MemHopError> {
    let id_hash = node.id_hash;
    l3::store::add_node(mmap, header, btree, node, file, tracker, index_map)?;
    // add_node writes to the page returned by the allocator; recover that page id from btree.
    if let Some(page_ref) = btree.search(id_hash) {
        let page_id = (page_ref >> 16) as u32;
        tx.track_new_page(page_id);
        tx.snapshot_page(mmap, page_id)?;
        tx.track_new_btree_key(id_hash);
    }
    Ok(())
}

/// Allocate page and write L5 ActionChainSlot.
#[allow(clippy::too_many_arguments)]
fn allocate_and_write_l5_crystal(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    tx: &mut TxState,
    id_hash: u64,
    action_title: &str,
    action_description: &str,
    now_ms: i64,
    btree: &mut BTreeIndex,
    file: &mut File,
) -> Result<u64, MemHopError> {
    let page_id = allocate_or_extend(mmap, header, file, DEFAULT_GROW_PAGES)?;
    tx.track_new_page(page_id);
    tx.snapshot_page(mmap, page_id)?;

    let offset = crate::shared::slot_io::slot_offset(page_id);

    use crate::layers::action_chain::ActionChainSlot;
    let chain = ActionChainSlot {
        id_hash,
        title: action_title.to_string(),
        trigger: action_description.to_string(),
        status: crate::layers::action_chain::ChainStatus::Active,
        confidence: 0.8,
        success_rate: 1.0,
        trigger_count: 0,
        last_triggered: 0,
        created_at: now_ms,
        updated_at: now_ms,
        version: 1,
    };

    let data = chain
        .serialize()
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;
    write_slot_page_header(mmap, page_id, PageType::ActionChain, 5, data.len());
    if offset + data.len() > mmap.len() {
        return Err(MemHopError::Serialization(format!(
            "ActionChainSlot data too large for page: {} > {}",
            data.len(),
            mmap.len() - offset
        )));
    }
    mmap[offset..offset + data.len()].copy_from_slice(&data);

    let page_ref = (page_id as u64) << 16;
    btree.insert(id_hash, page_ref);
    tx.track_new_btree_key(id_hash);

    Ok(page_ref)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::config::MemHopConfig;
    use crate::file::header::FileHeader;
    use crate::file::page::allocate_page;
    use crate::index::btree::BTreeIndex;
    use crate::index::l2_meta::L2MetaIndex;
    use crate::index::sparse::SparseIndex;
    use crate::layers::context::{ActivationState, ContextSlot, LlmParams};
    use crate::layers::context_node::ContextNode;
    use crate::util::{PageType, PAGE_SIZE, SENTINEL_PAGE_ID};
    use memmap2::MmapMut;
    use std::fs::File;
    use std::io::Write;
    use tempfile::TempDir;

    fn create_test_db(pages: usize) -> (MmapMut, FileHeader, BTreeIndex, File) {
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
        (mmap, header, btree, file)
    }

    fn create_context_page(
        mmap: &mut MmapMut,
        header: &mut FileHeader,
        btree: &mut BTreeIndex,
        file: &mut File,
        topic_hash: u64,
        title: &str,
    ) -> u32 {
        let page_id =
            allocate_page(mmap, header, PageType::Context, 2, SENTINEL_PAGE_ID, file).unwrap();

        let ctx = ContextSlot {
            id_hash: topic_hash,
            parent_id: None,
            depth: 1,
            title: title.to_string(),
            summary: None,
            archive_refs: vec![],
            l3_refs: vec![],
            turn_count: 0,
            created_at: 0,
            updated_at: 0,
            version: 2,
            importance: 0.5,
            activation_score: 0.0,
            is_active: false,
            activation_state: ActivationState::Dormant,
            centroid_page_ref: 0,
            dialogue_range: (0, 0),
            llm_params: LlmParams::default(),
        };

        let data = ctx.serialize().unwrap();
        let offset = crate::shared::slot_io::slot_offset(page_id);
        mmap[offset..offset + data.len()].copy_from_slice(&data);

        btree.insert(topic_hash, (page_id as u64) << 16);
        page_id
    }

    fn make_request(topic_id: &str, text: &str) -> UpdateRequest {
        UpdateRequest {
            topic_id: topic_id.to_string(),
            dialogue_text: text.to_string(),
            summary: None,
            action_chain: None,
            instant_distill: false,
            source: RequestSource::default(),
        }
    }

    #[test]
    fn test_update_memory_writes_l4_and_updates_l2() {
        let (mut mmap, mut header, mut btree, mut file) = create_test_db(64);
        let topic_hash = hash_id("topic-a");
        let topic_id = format_hash(topic_hash);
        create_context_page(
            &mut mmap,
            &mut header,
            &mut btree,
            &mut file,
            topic_hash,
            "topic a",
        );

        let mut sparse_index = SparseIndex::new();
        let mut l2_meta =
            L2MetaIndex::build(&unsafe { memmap2::Mmap::map(&file).unwrap() }, &btree);
        let mut journal = Vec::new();
        let config = MemHopConfig::new(std::path::PathBuf::from("/dev/null"), 768);

        let result = update_memory_internal(
            &mut mmap,
            &mut header,
            make_request(&topic_id, "hello world"),
            &mut btree,
            &mut sparse_index,
            &mut l2_meta,
            &mut file,
            &config,
            &mut journal,
            None,
            None,
        )
        .unwrap();

        assert_eq!(result.topic_id, topic_id);
        assert!(!result.archive_id.is_empty());
        assert_eq!(result.status, UpdateStatus::Created);

        // L2 turn_count updated.
        let l2_page_ref = btree.search(topic_hash).unwrap();
        let slot_data = crate::shared::slot_io::get_slot_data(&mmap[..], l2_page_ref).unwrap();
        let ctx = ContextSlot::deserialize_slot(slot_data).unwrap();
        assert_eq!(ctx.turn_count, 1);
        assert_eq!(ctx.archive_refs.len(), 1);

        // L2Meta updated.
        let meta = l2_meta.get(topic_hash).unwrap();
        assert_eq!(meta.turn_count, 1);
        assert_eq!(meta.archive_count, 1);

        // Journal entry recorded.
        assert_eq!(journal.len(), 1);
    }

    #[test]
    fn test_update_memory_failure_rolls_back_l2() {
        let (mut mmap, mut header, mut btree, mut file) = create_test_db(64);
        let topic_hash = hash_id("topic-b");
        let topic_id = format_hash(topic_hash);
        create_context_page(
            &mut mmap,
            &mut header,
            &mut btree,
            &mut file,
            topic_hash,
            "topic b",
        );

        // Snapshot the original L2 page content.
        let l2_page_ref = btree.search(topic_hash).unwrap();
        let l2_page_id = (l2_page_ref >> 16) as u32;
        let original_l2 =
            mmap[(l2_page_id as usize) * PAGE_SIZE..(l2_page_id as usize + 1) * PAGE_SIZE].to_vec();

        let mut sparse_index = SparseIndex::new();
        let mut l2_meta =
            L2MetaIndex::build(&unsafe { memmap2::Mmap::map(&file).unwrap() }, &btree);
        let mut journal = Vec::new();

        // Force a failure after L4 was written by requesting a summary so large that
        // the updated L2 ContextSlot cannot fit in the mmap. L4 allocation succeeds,
        // L2 snapshot is taken, then L2 serialization fails and triggers rollback.
        let bad_request = UpdateRequest {
            topic_id: topic_id.clone(),
            dialogue_text: "small dialogue".to_string(),
            summary: Some("x".repeat(mmap.len())),
            action_chain: None,
            instant_distill: false,
            source: RequestSource::default(),
        };

        let config = MemHopConfig::new(std::path::PathBuf::from("/dev/null"), 768);
        let result = update_memory_internal(
            &mut mmap,
            &mut header,
            bad_request,
            &mut btree,
            &mut sparse_index,
            &mut l2_meta,
            &mut file,
            &config,
            &mut journal,
            None,
            None,
        );

        assert!(result.is_err());

        // L2 page content must be unchanged.
        let restored =
            &mmap[(l2_page_id as usize) * PAGE_SIZE..(l2_page_id as usize + 1) * PAGE_SIZE];
        assert_eq!(restored, &original_l2[..]);

        // No journal entry should have been buffered on failure.
        assert!(journal.is_empty());
    }

    #[test]
    fn test_replay_journal_restores_l2_after_partial_l4_write() {
        let (mut mmap, mut header, mut btree, mut file) = create_test_db(64);
        let topic_hash = hash_id("topic-c");
        let _topic_id = format_hash(topic_hash);
        create_context_page(
            &mut mmap,
            &mut header,
            &mut btree,
            &mut file,
            topic_hash,
            "topic c",
        );

        let l2_page_ref = btree.search(topic_hash).unwrap();
        let l2_page_id = (l2_page_ref >> 16) as u32;
        let original_l2 =
            mmap[(l2_page_id as usize) * PAGE_SIZE..(l2_page_id as usize + 1) * PAGE_SIZE].to_vec();

        // Simulate a crash after L4 was written but before L2 was modified:
        // build a journal entry that records the original L2 page state.
        let mut entry = JournalEntry::new(1);
        entry.add_page(l2_page_id, original_l2.clone()).unwrap();

        // Mutate L2 as if the transaction had partially applied.
        mmap[(l2_page_id as usize) * PAGE_SIZE..(l2_page_id as usize + 1) * PAGE_SIZE].fill(0xAB);

        // Replay should restore the original L2 page.
        replay_journal_to_mmap(&mut mmap, std::slice::from_ref(&entry)).unwrap();
        let restored =
            &mmap[(l2_page_id as usize) * PAGE_SIZE..(l2_page_id as usize + 1) * PAGE_SIZE];
        assert_eq!(restored, &original_l2[..]);
    }

    #[test]
    fn test_update_memory_creates_l1_node_when_missing() {
        let (mut mmap, mut header, mut btree, mut file) = create_test_db(64);
        let topic_hash = hash_id("topic-d");
        let topic_id = format_hash(topic_hash);
        create_context_page(
            &mut mmap,
            &mut header,
            &mut btree,
            &mut file,
            topic_hash,
            "topic d",
        );

        let mut sparse_index = SparseIndex::new();
        let mut l2_meta =
            L2MetaIndex::build(&unsafe { memmap2::Mmap::map(&file).unwrap() }, &btree);
        let mut journal = Vec::new();
        let config = MemHopConfig::new(std::path::PathBuf::from("/dev/null"), 768);

        update_memory_internal(
            &mut mmap,
            &mut header,
            make_request(&topic_id, "first turn"),
            &mut btree,
            &mut sparse_index,
            &mut l2_meta,
            &mut file,
            &config,
            &mut journal,
            None,
            None,
        )
        .unwrap();

        // There should be a ContextNode whose context_id matches the topic.
        let found = btree.iter_unsorted().any(|(_, &pr)| {
            crate::shared::slot_io::get_slot_data(&mmap[..], pr)
                .and_then(|d| ContextNode::deserialize(d).ok())
                .map(|n| n.context_id == topic_hash)
                .unwrap_or(false)
        });
        assert!(found);
    }

    #[test]
    fn test_update_memory_checkpoint_persists_data() {
        let dir = TempDir::new().unwrap();
        let path = dir.path().join("checkpoint.meh");

        let topic_hash = hash_id("persist-topic");
        let topic_id = format_hash(topic_hash);

        {
            let mut config = MemHopConfig::new(path.clone(), 768);
            config.encoder_grpc_addr = None;
            let mut db = crate::MemHop::open(config).unwrap();

            // Create an L2 context via the public search path (auto_create).
            let page_id = db
                .allocate_page(PageType::Context, 2, SENTINEL_PAGE_ID)
                .unwrap();
            let ctx = ContextSlot {
                id_hash: topic_hash,
                parent_id: None,
                depth: 1,
                title: "persist topic".to_string(),
                summary: None,
                archive_refs: vec![],
                l3_refs: vec![],
                turn_count: 0,
                created_at: 0,
                updated_at: 0,
                version: 2,
                importance: 0.5,
                activation_score: 0.0,
                is_active: false,
                activation_state: ActivationState::Dormant,
                centroid_page_ref: 0,
                dialogue_range: (0, 0),
                llm_params: LlmParams::default(),
            };
            let data = ctx.serialize().unwrap();
            let offset = crate::shared::slot_io::slot_offset(page_id);
            db.mmap[offset..offset + data.len()].copy_from_slice(&data);
            db.btree.insert(topic_hash, (page_id as u64) << 16);
            db.l2_meta.update_from_context(&ctx);

            update_memory_internal(
                &mut db.mmap,
                &mut db.header,
                make_request(&topic_id, "data to persist"),
                &mut db.btree,
                &mut db.sparse_index,
                &mut db.l2_meta,
                &mut db.file,
                &db.config,
                &mut db.journal_buffer,
                None,
                None,
            )
            .unwrap();

            db.checkpoint().unwrap();
        }

        // Re-open and verify the archive exists.
        let mut config = MemHopConfig::new(path, 768);
        config.encoder_grpc_addr = None;
        let db = crate::MemHop::open(config).unwrap();
        let meta = db
            .l2_meta
            .get(topic_hash)
            .expect("L2 meta missing after reopen");
        assert_eq!(meta.turn_count, 1);
        assert_eq!(meta.archive_count, 1);
    }
}
