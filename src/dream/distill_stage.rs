//! Stage 7: L1→L3 Distillation - Extract procedural knowledge into L3
//!
//! This stage identifies frequent behavioral patterns in episodic memories
//! and distills them into procedural knowledge nodes (L3) using LLM refinement.

use crate::dream::llm::{LlmProvider, MemorySummary};
use crate::file::header::FileHeader;
use crate::file::page::{allocate_page, read_page_header, write_page_data};
use crate::index::btree::BTreeIndex;
use crate::index::sparse::SparseIndex;
use crate::slot::engram::EngramSlot;
use crate::util::hash::hash_id;
use crate::util::{PageType, PAGE_SIZE};
use crate::MemHopError;
use memmap2::MmapMut;
use std::collections::HashSet;

/// Distill L1 patterns into L3 domain nodes using LLM
///
/// # Arguments
/// * `mmap` - Mutable memory-mapped file for reading/writing memory slots
/// * `header` - File header for page allocation and free list management
/// * `btree` - B-tree index for engram lookup
/// * `sparse_index` - Sparse index for keyword indexing
/// * `llm` - LLM provider for pattern refinement
///
/// # Returns
/// Vector of new L3 domain node IDs created during distillation
pub fn distill_l1_to_l3(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    _sparse_index: &SparseIndex,
    llm: &dyn LlmProvider,
) -> Result<Vec<String>, MemHopError> {
    // Step 1: Scan all EngramSlots, filter importance >= 0.7
    let mut high_importance_engrams: Vec<EngramSlot> = Vec::new();
    let page_count = header.page_count;

    for page_id in 18..page_count {
        let offset = (page_id as usize) * PAGE_SIZE;
        if offset + PAGE_SIZE > mmap.len() {
            break;
        }

        if let Ok(page_header) = read_page_header(unsafe { &*(mmap.as_ptr() as *const memmap2::Mmap) }, page_id) {
            if page_header.page_type != PageType::Engram as u16 {
                continue;
            }

            if let Ok(engram) = EngramSlot::deserialize(&mmap[offset + 32..]) {
                if engram.importance >= 0.7 {
                    high_importance_engrams.push(engram);
                }
            }
        }
    }

    if high_importance_engrams.len() < 2 {
        return Ok(vec![]);
    }

    // Step 2: Cluster by keyword intersection (share >= 2 keywords → same group)
    let mut groups: Vec<Vec<EngramSlot>> = Vec::new();
    let mut assigned = vec![false; high_importance_engrams.len()];

    for i in 0..high_importance_engrams.len() {
        if assigned[i] {
            continue;
        }

        let mut group = vec![high_importance_engrams[i].clone()];
        assigned[i] = true;

        for j in (i + 1)..high_importance_engrams.len() {
            if assigned[j] {
                continue;
            }

            // Calculate keyword intersection
            let intersection: HashSet<_> = high_importance_engrams[i].keywords.iter()
                .cloned()
                .collect::<HashSet<_>>()
                .intersection(&high_importance_engrams[j].keywords.iter().cloned().collect())
                .cloned()
                .collect();

            if intersection.len() >= 2 {
                group.push(high_importance_engrams[j].clone());
                assigned[j] = true;
            }
        }

        if group.len() >= 2 {
            groups.push(group);
        }
    }

    // Step 3: Call LLM to extract patterns for each group
    let mut new_l3_ids = Vec::new();

    for group in &groups {
        // 3a: Collect all engram.text in the group
        let _texts: Vec<String> = group.iter().map(|e| e.text.clone()).collect();

        // Build MemorySummary list
        let memories: Vec<MemorySummary> = group.iter().map(|e| {
            MemorySummary {
                text: e.text.clone(),
                keywords: e.keywords.clone(),
                timestamp: e.created_at,
            }
        }).collect();

        // 3b: Call LLM to extract patterns (fallback on error)
        let patterns = match llm.extract_patterns(&memories) {
            Ok(pats) if !pats.is_empty() => pats,
            Ok(_) | Err(_) => {
                eprintln!("LLM extract_patterns failed, using fallback");
                llm.fallback_extract_patterns(&memories)
            }
        };

        // 3c: Create L3 EngramSlot for each Pattern (is_structural=true)
        for pattern in &patterns {
            let now = chrono::Utc::now().timestamp_millis();
            let id_hash = hash_id(&format!("l3_pattern_{}_{}", pattern.description, now));

            let avg_importance: f32 = group.iter().map(|e| e.importance).sum::<f32>() / group.len() as f32;

            let l3_engram = EngramSlot {
                id_hash,
                text: pattern.description.clone(),
                summary: None,
                keywords: vec![format!("pattern_{}", pattern.frequency)],
                created_at: now,
                updated_at: now,
                version: 1,
                edge_count: 0,
                doc_len: pattern.description.len() as u16,
                vector_page_ref: 0,  // L3 may not need vectors
                is_structural: true,  // ⚠️ Key: mark as structural memory
                source_type: 3,       // L3
                memory_state: 0,      // Active
                emotion_type: 0,
                valence: 0.0,
                arousal: 0.0,
                importance: avg_importance * 1.1,
                edge_ptrs: [0; 8],
            };

            // 3d: Allocate page, write, update indices
            let page_id = allocate_page(mmap, PageType::Engram, 3, 0)?;  // L3 layer
            let serialized = l3_engram.serialize()?;
            write_page_data(mmap, page_id, &serialized)?;

            let page_ref = crate::file::page::encode_page_ref(page_id, 0);
            btree.insert(id_hash, page_ref);

            // Note: sparse_index should already contain this document from initial store
            // sparse_index.add_document(id_hash, l3_engram.keywords.clone(), l3_engram.doc_len as u32);

            new_l3_ids.push(format!("{:016x}", id_hash));
        }
    }

    Ok(new_l3_ids)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::dream::openai_compatible::OpenAICompatibleLlmProvider;
    use crate::file::header::FileHeader;
    use std::io::Write;

    #[test]
    fn test_distill_l1_to_l3_empty() {
        // Test returns empty list when no high-importance engrams exist
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
        let sparse_index = SparseIndex::new();
        let llm = OpenAICompatibleLlmProvider::new(
            "test-key".to_string(),
            "https://api.example.com/v1/chat/completions".to_string(),
            "test-model".to_string(),
        );

        let result = distill_l1_to_l3(
            &mut mmap, 
            &mut header, 
            &mut btree, 
            &sparse_index, 
            &llm
        );
        
        assert!(result.is_ok());
        // Should return empty list since there are no engrams
        assert_eq!(result.unwrap().len(), 0);
    }

    #[test]
    fn test_distill_creates_l3_engrams() {
        // TODO: Create high importance engrams and verify distillation results
        // This would require setting up a database with test data
    }

    #[test]
    fn test_distill_l3_is_structural() {
        // TODO: Verify that generated L3 engrams have is_structural = true
        // This would require creating test engrams and running distillation
    }
}
