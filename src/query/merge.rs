//! Merge L2 topics implementation for MemHop
//!
//! Implements the merge_l2_topics() interface to merge multiple L2 topics into one.

use crate::file::header::FileHeader;
use crate::index::btree::BTreeIndex;
use crate::index::sparse::SparseIndex;
use crate::query::types::*;
use crate::slot::topic::TopicSlot;
use crate::util::hash_id;
use crate::MemHopError;
use memmap2::MmapMut;
use std::collections::HashSet;
use std::time::{SystemTime, UNIX_EPOCH};

const PAGE_SIZE: usize = 4096;

/// Parse ID string to u64 hash (supports hex-encoded hashes)
fn parse_id_to_hash(id: &str) -> u64 {
    if id.len() == 16 {
        u64::from_str_radix(id, 16).unwrap_or_else(|_| hash_id(id))
    } else {
        hash_id(id)
    }
}

/// Merge multiple L2 topics into a primary topic
pub fn merge_l2_topics(
    mmap: &mut MmapMut,
    _header: &mut FileHeader,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    primary_id: &str,
    secondary_ids: Vec<String>,
) -> Result<L2TopicDetail, MemHopError> {
    let now_ms = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as i64;

    let primary_hash = parse_id_to_hash(primary_id);
    let secondary_hashes: Vec<u64> = secondary_ids.iter().map(|id| parse_id_to_hash(id)).collect();

    // Step 1: Verify all topics exist
    if btree.search(primary_hash).is_none() {
        return Err(MemHopError::PageNotFound(0));
    }

    for &sec_hash in &secondary_hashes {
        if btree.search(sec_hash).is_none() {
            return Err(MemHopError::PageNotFound(0));
        }
    }

    // Step 2: Load primary topic
    let primary_page_ref = btree.search(primary_hash).unwrap();
    let primary_page_id = (primary_page_ref >> 16) as u32;
    let primary_offset = (primary_page_id as usize) * PAGE_SIZE + 32;
    let mut primary_topic = TopicSlot::deserialize(&mmap[primary_offset..])
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;

    // Step 3-5: Merge nodes and refs from secondary topics
    let mut merged_node_ids: HashSet<u64> = primary_topic.node_ids.iter().cloned().collect();
    let mut merged_l3_refs: HashSet<u64> = primary_topic.l3_refs.iter().cloned().collect();
    let mut merged_l4_refs: HashSet<u64> = primary_topic.l4_refs.iter().cloned().collect();

    for &sec_hash in &secondary_hashes {
        let sec_page_ref = btree.search(sec_hash).unwrap();
        let sec_page_id = (sec_page_ref >> 16) as u32;
        let sec_offset = (sec_page_id as usize) * PAGE_SIZE + 32;
        let sec_topic = TopicSlot::deserialize(&mmap[sec_offset..])
            .map_err(|e| MemHopError::Serialization(e.to_string()))?;

        // Merge node_ids
        merged_node_ids.extend(sec_topic.node_ids.iter());

        // Merge l3_refs
        merged_l3_refs.extend(sec_topic.l3_refs.iter());

        // Merge l4_refs
        merged_l4_refs.extend(sec_topic.l4_refs.iter());
    }

    // Convert back to vectors
    primary_topic.node_ids = merged_node_ids.into_iter().collect();
    primary_topic.l3_refs = merged_l3_refs.into_iter().collect();
    primary_topic.l4_refs = merged_l4_refs.into_iter().collect();

    // Step 6: Update dialogue_range to cover all merged topics
    let mut min_ts = primary_topic.dialogue_range.0;
    let mut max_ts = primary_topic.dialogue_range.1;

    for &sec_hash in &secondary_hashes {
        let sec_page_ref = btree.search(sec_hash).unwrap();
        let sec_page_id = (sec_page_ref >> 16) as u32;
        let sec_offset = (sec_page_id as usize) * PAGE_SIZE + 32;
        let sec_topic = TopicSlot::deserialize(&mmap[sec_offset..])
            .map_err(|e| MemHopError::Serialization(e.to_string()))?;

        min_ts = min_ts.min(sec_topic.dialogue_range.0);
        max_ts = max_ts.max(sec_topic.dialogue_range.1);
    }

    primary_topic.dialogue_range = (min_ts, max_ts);

    // Step 7: Generate new summary (TODO: Use LLM or keyword extraction)
    // For now, keep the primary topic's summary or create a simple merged summary
    if primary_topic.summary.is_none() {
        primary_topic.summary = Some(format!("Merged from {} topics", secondary_ids.len() + 1));
    }

    // Update timestamp and version
    primary_topic.updated_at = now_ms;
    primary_topic.version += 1;

    // Serialize and write back primary topic
    let primary_data = primary_topic.serialize()
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;

    if primary_offset + primary_data.len() <= mmap.len() {
        mmap[primary_offset..primary_offset + primary_data.len()].copy_from_slice(&primary_data);
    } else {
        return Err(MemHopError::PageNotFound(primary_page_id));
    }

    // Update sparse index for primary topic
    let terms: Vec<String> = primary_topic.title.split_whitespace().map(|s| s.to_string()).collect();
    sparse_index.remove_document(primary_topic.id_hash);
    sparse_index.add_document(primary_topic.id_hash, terms, primary_topic.title.len() as u32);

    // Step 8: Delete secondary topics from B-tree and sparse index
    for &sec_hash in &secondary_hashes {
        let sec_page_ref = btree.search(sec_hash).unwrap();
        let _sec_page_id = (sec_page_ref >> 16) as u32;

        // Remove from sparse index
        sparse_index.remove_document(sec_hash);

        // TODO: Add page to free list for reuse
        // For now, just remove from B-tree
        btree.remove(sec_hash);
    }

    // Return merged topic detail
    Ok(L2TopicDetail {
        id: format!("{:016x}", primary_topic.id_hash),
        title: primary_topic.title,
        summary: primary_topic.summary,
        node_ids: primary_topic.node_ids.iter().map(|id| format!("{:016x}", id)).collect(),
        l3_refs: primary_topic.l3_refs.iter().map(|id| format!("{:016x}", id)).collect(),
        l4_refs: primary_topic.l4_refs.iter().map(|id| format!("{:016x}", id)).collect(),
        parent_id: primary_topic.parent_id.map(|id| format!("{:016x}", id)),
        is_active: primary_topic.is_active,
        importance: primary_topic.importance,
        activation_score: primary_topic.activation_score,
        created_at: primary_topic.created_at,
        updated_at: primary_topic.updated_at,
    })
}
