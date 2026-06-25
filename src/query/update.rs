//! Update implementation for MemHop
//!
//! Implements the update_memory() interface with multi-level联动 updates.

use crate::file::free_list::allocate_from_free_list;
use crate::file::header::FileHeader;
use crate::file::page::PageHeader;
use crate::index::btree::BTreeIndex;
use crate::index::sparse::SparseIndex;
use crate::query::common::{format_hash, now_ms, parse_id_to_hash};
use crate::query::types::*;
use crate::slot::archive::ArchiveSlot;
use crate::util::{hash_id, PageType, PAGE_SIZE};
use crate::organize::extract_keywords;
use crate::MemHopError;
use memmap2::MmapMut;

/// Write a proper PageHeader for a newly allocated data page
fn write_slot_page_header(
    mmap: &mut MmapMut,
    page_id: u32,
    page_type: PageType,
    layer_id: u16,
    data_len: usize,
) {
    let header = PageHeader {
        page_id,
        page_type: page_type.to_u16(),
        slot_count: 1,
        free_bytes: (PAGE_SIZE - 32).saturating_sub(data_len) as u16,
        layer_id,
        next_page: 0xFFFFFFFF,
        prev_page: 0xFFFFFFFF,
        reserved: [0u8; 12],
    };
    let header_bytes = header.to_bytes();
    let offset = (page_id as usize) * PAGE_SIZE;
    mmap[offset..offset + 32].copy_from_slice(&header_bytes);
}

/// Core update implementation
///
/// After search_memory activates an L2 context, this interface:
/// 1. Writes dialogue_text to L4 ArchiveSlot on disk
/// 2. Writes action_chain to L5 ActionChainSlot on disk
/// 3. Appends L4 archive_id to L2 archive_refs index
/// 4. Appends summary to L2 context summary
/// 5. Updates sparse index
pub fn update_memory(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    request: UpdateRequest,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    _vector_dim: usize,
) -> Result<UpdateResult, MemHopError> {
    let now_ms = now_ms();

    // Step 1: Find the activated L2 topic (required, not optional)
    // NOTE: topic_id comes from format_hash(id_hash) — a hex string like "1a2b3c..."
    // Use parse_id_to_hash to correctly reverse format_hash; hash_id would hash the hex string itself
    let topic_hash = parse_id_to_hash(&request.topic_id);
    let page_ref = btree
        .search(topic_hash)
        .ok_or(MemHopError::PageNotFound(0))?;

    // Step 2: Write dialogue_text to L4 ArchiveSlot on disk
    let l4_id_hash = hash_id(&format!("L4-{}-{}", topic_hash, now_ms));
    allocate_and_write_l4_archive(
        mmap,
        header,
        l4_id_hash,
        &request.dialogue_text,
        topic_hash,
        now_ms,
        btree,
    )?;
    let archive_id = format_hash(l4_id_hash);

    // Step 3: Write action_chain to L5 ActionChainSlot on disk
    for action in &request.action_chain {
        let crystal_id_hash = hash_id(&format!(
            "{}-{:?}-{}",
            topic_hash, action.action_type, now_ms
        ));
        allocate_and_write_l5_crystal(
            mmap,
            header,
            crystal_id_hash,
            &action.title,
            &action.description,
            now_ms,
            btree,
        )?;
    }

    // Step 4: Deserialize the ContextSlot and update
    let data = &mmap[..];
    let page_id = crate::query::slot_io::decode_page_id(page_ref);
    let slot_data = crate::query::slot_io::get_slot_data(data, page_ref)
        .ok_or(MemHopError::PageNotFound(page_id))?;

    let mut ctx = crate::slot::context::ContextSlot::deserialize_slot(slot_data)?;

    // Step 5: Append L4 archive_id to L2 archive_refs index
    if !ctx.archive_refs.contains(&l4_id_hash) {
        ctx.archive_refs.push(l4_id_hash);
        ctx.archive_refs.sort();
    }

    // Step 6: Update turn count
    ctx.turn_count += 1;

    // Step 7: Append summary to L2 context summary
    if let Some(ref summary) = request.summary {
        match ctx.summary {
            Some(ref existing) => {
                ctx.summary = Some(format!("{}\n{}", existing, summary));
            }
            None => {
                ctx.summary = Some(summary.clone());
            }
        }
        ctx.updated_at = now_ms;
    }

    // Step 7.5: Instant L3 distillation (optional)
    if request.instant_distill {
        let keywords = extract_keywords(&request.dialogue_text, 10);
        let mut graphs_to_link: Vec<u64> = Vec::new();
        let data: &[u8] = &mmap[..];
        for kw in &keywords {
            // Use entity_search_nodes to get actual L3 node hashes
            let hits = sparse_index.entity_search_nodes(kw);
            for (node_hash, _l2_ids) in &hits {
                // Find graph_id from node_hash
                if let Some(slot_data) = btree.search(*node_hash).and_then(|pr| crate::query::slot_io::get_slot_data(data, pr)) {
                    if let Ok(node) = crate::slot::hypergraph::HypergraphNode::deserialize(slot_data) {
                        if !ctx.l3_refs.contains(&node.graph_id) {
                            graphs_to_link.push(node.graph_id);
                        }
                    }
                }
            }
        }
        // Deduplicate and append to l3_refs
        graphs_to_link.sort();
        graphs_to_link.dedup();
        ctx.l3_refs.extend(graphs_to_link);
    }

    // Step 8: Serialize and write back
    let serialized = ctx
        .serialize()
        .map_err(|e| MemHopError::Serialization(format!("ContextSlot serialize: {}", e)))?;
    let write_offset = (page_id as usize) * PAGE_SIZE + 32;
    if write_offset + serialized.len() > mmap.len() {
        return Err(MemHopError::Serialization(format!(
            "ContextSlot too large for page: {} > {}",
            serialized.len(),
            PAGE_SIZE - 32
        )));
    }
    mmap[write_offset..write_offset + serialized.len()].copy_from_slice(&serialized);

    // Step 9: Update sparse index
    if let Some(ref summary) = request.summary {
        let terms: Vec<String> = summary
            .split_whitespace()
            .map(|s| s.to_lowercase())
            .filter(|s| !s.is_empty())
            .collect();
        sparse_index.add_document(topic_hash, terms, summary.len() as u32);
    }

    Ok(UpdateResult {
        topic_id: format_hash(topic_hash),
        archive_id,
        status: UpdateStatus::Updated,
    })
}

/// Allocate page and write L4 Archive
fn allocate_and_write_l4_archive(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    id_hash: u64,
    content: &str,
    topic_id: u64,
    now_ms: i64,
    btree: &mut BTreeIndex,
) -> Result<u64, MemHopError> {
    // Allocate new page
    let page_id = allocate_from_free_list(mmap, header)?;
    let offset = (page_id as usize) * PAGE_SIZE + 32;

    // Create ArchiveSlot
    use crate::slot::archive::ContentType;
    let archive = ArchiveSlot {
        id_hash,
        content_type: ContentType::Text,
        role: 0, // user
        context_id: topic_id,
        created_at: now_ms,
        content: content.to_string(),
        metadata: None,
    };

    // Serialize and write
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

    // Insert into B-tree
    let page_ref = (page_id as u64) << 16;
    btree.insert(id_hash, page_ref);

    Ok(page_ref)
}

/// Allocate page and write L5 ActionChainSlot
fn allocate_and_write_l5_crystal(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    id_hash: u64,
    action_title: &str,
    action_description: &str,
    now_ms: i64,
    btree: &mut BTreeIndex,
) -> Result<u64, MemHopError> {
    // Allocate new page
    let page_id = allocate_from_free_list(mmap, header)?;
    let offset = (page_id as usize) * PAGE_SIZE + 32;

    // Create ActionChainSlot
    use crate::slot::action_chain::ActionChainSlot;
    let chain = ActionChainSlot {
        id_hash,
        title: action_title.to_string(),
        trigger: action_description.to_string(),
        status: crate::slot::action_chain::ChainStatus::Active,
        confidence: 0.8,
        success_rate: 1.0,
        trigger_count: 0,
        last_triggered: 0,
        created_at: now_ms,
        updated_at: now_ms,
        version: 1,
    };

    // Serialize and write
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

    // Insert into B-tree
    let page_ref = (page_id as u64) << 16;
    btree.insert(id_hash, page_ref);

    Ok(page_ref)
}
