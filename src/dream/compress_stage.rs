//! Stage: L2 Depth-based Compression
//!
//! Compresses activated L2 contexts through depth demotion:
//! - Depth 1 (主节点/Scene) → Depth 2 (次节点/Sub-scene), with compressed summary
//! - Depth 2 (次节点/Sub-scene) → Depth 3 (次次节点/Turn group)
//! - Depth 3 (次次节点/Turn group) → Removed (free page)
//!
//! Original depth-1 nodes are compressed into new contexts before demotion.
//! All changes are written to disk and memory indexes are updated.

use crate::dream::llm::LlmProvider;
use crate::dream::prune::{CompressResult, DemotionResult};

/// Result type for compress_active_contexts function
pub type CompressStageResult = Result<(
    Vec<DemotionResult>,
    Vec<CompressResult>,
    Vec<String>,  // removed context IDs (depth 3 → gone)
    Vec<String>,  // demoted to tertiary IDs (depth 2 → 3)
), MemHopError>;
use crate::file::free_list::free_page;
use crate::file::header::FileHeader;
use crate::file::page::{allocate_page, read_page_header, write_page_data};
use crate::index::btree::BTreeIndex;
use crate::index::sparse::SparseIndex;
use crate::slot::context::{ActivationState, ContextSlot};
use crate::util::hash::hash_id;
use crate::util::{get_current_timestamp, PageType, PAGE_SIZE};
use crate::MemHopError;
use memmap2::MmapMut;
use std::collections::HashSet;

/// Compress activated L2 contexts through depth-based demotion
///
/// # Depth Demotion Rules
/// 1. Depth 3 (turn group) → removed, page freed
/// 2. Depth 2 (sub-scene) → demoted to depth 3
/// 3. Depth 1 (scene) → compressed summary generated, demoted to depth 2,
///    new compressed context created at depth 1
///
/// # Arguments
/// * `mmap` - Mutable memory-mapped file for reading/writing context slots
/// * `header` - File header for page allocation and free list management
/// * `btree` - B-tree index for context lookup
/// * `sparse_index` - Sparse index for keyword lookup updates
/// * `llm` - LLM provider for generating compressed summaries
/// * `active_topic_ids` - Set of currently active topic IDs from session
///
/// # Returns
/// Tuple of (demotion_results, compression_results, removed_context_ids, demoted_to_tertiary_ids)
pub fn compress_active_contexts(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    llm: &dyn LlmProvider,
    active_topic_ids: &HashSet<u64>,
) -> CompressStageResult {
    let now_ms = get_current_timestamp();
    let _page_count = header.page_count;

    // Step 1: Collect all activated ContextSlots
    let mut active_contexts: Vec<(u32, ContextSlot)> = Vec::new();

    for &topic_id in active_topic_ids {
        if let Some(page_ref) = btree.search(topic_id) {
            let page_id = (page_ref >> 16) as u32;
            let offset = (page_id as usize) * PAGE_SIZE;
            if offset + PAGE_SIZE > mmap.len() {
                continue;
            }
            if let Ok(page_header) = read_page_header(
                unsafe { &*(mmap.as_ptr() as *const memmap2::Mmap) }, page_id
            ) {
                if page_header.page_type != PageType::Context as u16 {
                    continue;
                }
                if let Ok(ctx) = ContextSlot::deserialize(&mmap[offset + 32..]) {
                    active_contexts.push((page_id, ctx));
                }
            }
        }
    }

    if active_contexts.is_empty() {
        return Ok((vec![], vec![], vec![], vec![]));
    }

    let mut demoted_to_secondary: Vec<DemotionResult> = Vec::new();
    let mut new_compressed: Vec<CompressResult> = Vec::new();
    let mut removed_contexts: Vec<String> = Vec::new();
    let mut demoted_to_tertiary: Vec<String> = Vec::new();

    // Step 2: Process by depth (deepest first to avoid conflicts)

    // 2a: Remove depth-3 contexts (turn groups)
    let depth3: Vec<_> = active_contexts.iter()
        .filter(|(_, ctx)| ctx.depth == 3)
        .collect();

    for &(page_id, ref ctx) in &depth3 {
        let ctx_id = format!("{:016x}", ctx.id_hash);

        // Free the page
        let page_offset = (*page_id as usize) * PAGE_SIZE;
        mmap[page_offset..page_offset + PAGE_SIZE].fill(0);
        free_page(mmap, header, *page_id)?;

        // Remove from btree and sparse index
        btree.remove(ctx.id_hash);
        sparse_index.remove_document(ctx.id_hash);

        removed_contexts.push(ctx_id);
    }

    // 2b: Demote depth-2 contexts to depth 3
    for (page_id, ctx) in active_contexts.iter_mut()
        .filter(|(_, ctx)| ctx.depth == 2)
    {
        let ctx_id = format!("{:016x}", ctx.id_hash);

        // Demote to depth 3
        ctx.depth = 3;
        ctx.updated_at = now_ms;

        // Serialize and write back
        let serialized = ctx.serialize()
            .map_err(|e| MemHopError::Serialization(e.to_string()))?;
        write_page_data(mmap, *page_id, &serialized)?;

        demoted_to_tertiary.push(ctx_id);
    }

    // 2c: Compress and demote depth-1 contexts
    let depth1: Vec<(u32, ContextSlot)> = active_contexts.iter()
        .filter(|(_, ctx)| ctx.depth == 1)
        .map(|(pid, ctx)| (*pid, ctx.clone()))
        .collect();

    for (page_id, ctx) in &depth1 {
        let ctx_id = format!("{:016x}", ctx.id_hash);

        // Generate compressed summary from context's summary + title + archive count
        let texts_to_compress: Vec<String> = vec![
            format!("Title: {}", ctx.title),
            format!("Summary: {}", ctx.summary.as_deref().unwrap_or("(none)")),
            format!("Turns: {}, Archives: {}", ctx.turn_count, ctx.archive_refs.len()),
        ];

        let compressed_summary = match llm.summarize(&texts_to_compress) {
            Ok(s) => s,
            Err(_) => llm.fallback_summarize(&texts_to_compress),
        };

        // Create new compressed context at depth 1
        let new_id_hash = hash_id(&format!("compressed_{}_{}", ctx_id, now_ms));
        let new_ctx = ContextSlot {
            id_hash: new_id_hash,
            parent_id: None,
            depth: 1,
            title: format!("[Compressed] {}", ctx.title),
            summary: Some(compressed_summary.clone()),
            archive_refs: ctx.archive_refs.clone(),
            l3_refs: ctx.l3_refs.clone(),
            turn_count: ctx.turn_count,
            created_at: now_ms,
            updated_at: now_ms,
            version: 1,
            importance: ctx.importance * 0.9,
            activation_score: 0.3,
            is_active: false,  // Compressed contexts start inactive
            activation_state: ActivationState::Crystallized,
            centroid_page_ref: ctx.centroid_page_ref,
            dialogue_range: ctx.dialogue_range,
        };

        // Allocate page for new compressed context
        let new_page_id = allocate_page(mmap, header, PageType::Context, 2, 0)?;
        let new_serialized = new_ctx.serialize()
            .map_err(|e| MemHopError::Serialization(e.to_string()))?;
        write_page_data(mmap, new_page_id, &new_serialized)?;

        let new_page_ref = crate::file::page::encode_page_ref(new_page_id, 0);
        btree.insert(new_id_hash, new_page_ref);

        // Update sparse index for new context
        let title_terms: Vec<String> = new_ctx.title.split_whitespace()
            .map(|s| s.to_lowercase())
            .collect();
        let doc_len = title_terms.len() as u32;
        sparse_index.add_document(new_id_hash, title_terms, doc_len);

        new_compressed.push(CompressResult {
            new_context_id: format!("{:016x}", new_id_hash),
            source_context_id: ctx_id.clone(),
            new_summary: compressed_summary.clone(),
        });

        // Demote original depth-1 context to depth 2
        let mut demoted_ctx = ctx.clone();
        demoted_ctx.depth = 2;
        demoted_ctx.summary = Some(compressed_summary.clone());
        demoted_ctx.updated_at = now_ms;

        let demoted_serialized = demoted_ctx.serialize()
            .map_err(|e| MemHopError::Serialization(e.to_string()))?;
        write_page_data(mmap, *page_id, &demoted_serialized)?;

        demoted_to_secondary.push(DemotionResult {
            context_id: ctx_id,
            original_title: ctx.title.clone(),
            compressed_summary,
            new_depth: 2,
        });
    }

    Ok((demoted_to_secondary, new_compressed, removed_contexts, demoted_to_tertiary))
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;

    #[test]
    fn test_compress_empty_active_topics() {
        // With no active topics, should return empty results
        let temp_file = tempfile::NamedTempFile::new().unwrap();
        let path = temp_file.path();

        let mut file = std::fs::File::create(path).unwrap();
        file.write_all(&vec![0u8; 4096 * 50]).unwrap();
        drop(file);

        let file = std::fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open(path)
            .unwrap();

        let mut mmap = unsafe { MmapMut::map_mut(&file).unwrap() };
        let mut header = FileHeader::new(768);
        let mut btree = BTreeIndex::new();
        let mut sparse_index = SparseIndex::new();

        struct MockLlm;
        impl crate::dream::llm::LlmProvider for MockLlm {
            fn summarize(&self, texts: &[String]) -> Result<String, crate::MemHopError> {
                Ok(texts.join(", "))
            }
            fn extract_patterns(&self, _: &[crate::dream::llm::MemorySummary]) -> Result<Vec<crate::dream::llm::Pattern>, crate::MemHopError> {
                Ok(vec![])
            }
            fn generate_crystal(&self, _: &crate::dream::llm::Pattern) -> Result<crate::dream::llm::CrystalDef, crate::MemHopError> {
                Ok(crate::dream::llm::CrystalDef {
                    condition: "mock".to_string(),
                    action: "mock".to_string(),
                    confidence: 0.5,
                })
            }
            fn fallback_summarize(&self, texts: &[String]) -> String {
                texts.join(", ")
            }
            fn fallback_extract_patterns(&self, _: &[crate::dream::llm::MemorySummary]) -> Vec<crate::dream::llm::Pattern> {
                vec![]
            }
            fn fallback_generate_crystal(&self, _: &crate::dream::llm::Pattern) -> crate::dream::llm::CrystalDef {
                crate::dream::llm::CrystalDef {
                    condition: "mock".to_string(),
                    action: "mock".to_string(),
                    confidence: 0.3,
                }
            }
        }

        let llm = MockLlm;
        let empty_topics = HashSet::new();

        let result = compress_active_contexts(
            &mut mmap,
            &mut header,
            &mut btree,
            &mut sparse_index,
            &llm,
            &empty_topics,
        );

        assert!(result.is_ok());
        let (demoted, compressed, removed, tertiary) = result.unwrap();
        assert!(demoted.is_empty());
        assert!(compressed.is_empty());
        assert!(removed.is_empty());
        assert!(tertiary.is_empty());
    }
}
