//! Stage 8: L5 Crystallization - Generate procedural knowledge crystals
//!
//! This stage identifies repeated behavioral patterns and crystallizes them
//! into executable code or structured knowledge (L5 Crystal) using LLM generation.

use crate::dream::llm::{LlmProvider, Pattern};
use crate::file::header::FileHeader;
use crate::file::free_list::free_page;
use crate::file::page::{allocate_page, read_page_header, write_page_data};
use crate::index::btree::BTreeIndex;
use crate::slot::crystal::{CrystalSlot, CrystalStatus};
use crate::slot::engram::EngramSlot;
use crate::util::hash::hash_id;
use crate::util::{PageType, PAGE_SIZE};
use crate::MemHopError;
use memmap2::MmapMut;

/// Crystallize repeated patterns into L5 crystals using LLM
///
/// # Arguments
/// * `mmap` - Mutable memory-mapped file for reading/writing memory slots
/// * `header` - File header for page allocation and free list management
/// * `btree` - B-tree index for crystal lookup
/// * `llm` - LLM provider for crystal generation
///
/// # Returns
/// Vector of new L5 crystal IDs created during crystallization
pub fn crystallize_patterns(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    llm: &dyn LlmProvider,
) -> Result<Vec<String>, MemHopError> {
    // Step 1: Scan all L3 EngramSlots (is_structural = true)
    let mut l3_structural_engrams: Vec<EngramSlot> = Vec::new();
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
                if engram.is_structural && engram.source_type == 3 {
                    l3_structural_engrams.push(engram);
                }
            }
        }
    }

    if l3_structural_engrams.is_empty() {
        return Ok(vec![]);
    }

    // Step 2: Sort by created_at, take most recent N (N = min(20, total))
    l3_structural_engrams.sort_by_key(|e| e.created_at);
    let n = std::cmp::min(20, l3_structural_engrams.len());
    let recent_engrams = &l3_structural_engrams[l3_structural_engrams.len() - n..];

    // Step 3: Collect their pattern texts
    let patterns: Vec<Pattern> = recent_engrams.iter().map(|e| {
        Pattern {
            description: e.text.clone(),
            frequency: 1,  // Simplified, could infer from edge_count
            confidence: e.importance,
        }
    }).collect();

    // Step 4: Call LLM to generate crystals (fallback on error)
    let mut new_crystal_ids = Vec::new();

    for pattern in &patterns {
        let crystal_def = match llm.generate_crystal(pattern) {
            Ok(crystal) => crystal,
            Err(e) => {
                eprintln!("LLM generate_crystal failed, using fallback: {:?}", e);
                llm.fallback_generate_crystal(pattern)
            }
        };

        // Step 5: Create CrystalSlot
        let now = chrono::Utc::now().timestamp_millis();
        let id_hash = hash_id(&format!("crystal_{}_{}", crystal_def.condition, now));

        let crystal = CrystalSlot {
            id_hash,
            title: format!("crystal_{}", crystal_def.condition),
            condition: crystal_def.condition,
            action: crystal_def.action,
            raw_steps: "".to_string(),
            status: CrystalStatus::NotCrystallized,
            confidence: crystal_def.confidence,
            trigger_count: 0,
            last_triggered: 0,
            created_at: now,
            version: 1,
        };

        // Allocate page, write, update btree
        let page_id = allocate_page(mmap, PageType::Crystal, 5, 0)?;  // L5 layer
        let serialized = crystal.serialize()?;
        write_page_data(mmap, page_id, &serialized)?;

        let page_ref = crate::file::page::encode_page_ref(page_id, 0);
        btree.insert(id_hash, page_ref);

        new_crystal_ids.push(format!("{:016x}", id_hash));
    }

    Ok(new_crystal_ids)
}

/// Prune low-quality crystals during dream pipeline
///
/// Scans crystal pages and removes crystals with low confidence and low trigger counts.
/// This helps maintain crystal quality and prevents accumulation of ineffective rules.
///
/// # Arguments
/// * `mmap` - Mutable memory-mapped file containing crystal slots
/// * `header` - File header for free list management
/// * `page_count` - Total number of pages in the database
///
/// # Returns
/// Vector of pruned crystal IDs (hex strings)
///
/// # Errors
/// Returns `MemHopError` if memory access fails
pub fn prune_low_quality_crystals(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    page_count: u32,
) -> Result<Vec<String>, MemHopError> {
    let mut pruned = Vec::new();

    // Scan all data pages for crystals (skip header pages 0-1 and reserved pages 2-17)
    // Crystal pages typically start from page 18 onwards
    let start_page = 18;
    let end_page = page_count.min(500); // Cap at reasonable limit

    for page_id in start_page..end_page {
        let crystal_offset = (page_id as usize) * PAGE_SIZE + 32;

        if crystal_offset >= mmap.len() {
            break;
        }

        if let Ok(crystal) = CrystalSlot::deserialize(&mmap[crystal_offset..]) {
            // Low confidence + low trigger count → prune
            if crystal.confidence < 0.3 && crystal.trigger_count < 5 {
                // 1. 清零页面数据
                let page_offset = (page_id as usize) * PAGE_SIZE;
                mmap[page_offset..page_offset + PAGE_SIZE].fill(0);

                // 2. 归还到 free list
                free_page(mmap, header, page_id)?;

                pruned.push(format!("{:016x}", crystal.id_hash));
            }
        }
    }

    Ok(pruned)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::dream::deepseek_llm::DeepSeekLlmProvider;
    use crate::file::header::FileHeader;
    use std::io::Write;

    #[test]
    fn test_crystallize_patterns_empty() {
        // Test returns empty list when no L3 structural engrams exist
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
        let llm = DeepSeekLlmProvider::new("test-key".to_string());
        
        let result = crystallize_patterns(&mut mmap, &mut header, &mut btree, &llm);
        assert!(result.is_ok());
        assert_eq!(result.unwrap().len(), 0);
    }

    #[test]
    fn test_crystallize_creates_crystals() {
        // TODO: Create L3 structural engrams and verify crystallization results
    }

    #[test]
    fn test_crystallize_fallback_no_llm() {
        // TODO: Simulate LLM unavailability and verify regex fallback still produces results
    }
}
