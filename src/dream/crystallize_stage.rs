//! Stage 4: L5 Crystallization - Generate procedural knowledge crystals
//!
//! This stage identifies repeated behavioral patterns from existing L5
//! ActionChainSlots and crystallizes them into higher-quality action chains
//! using LLM generation.

use crate::dream::llm::{LlmProvider, Pattern};
use crate::file::header::FileHeader;
use crate::file::free_list::free_page;
use crate::file::page::{allocate_page, read_page_header, write_page_data};
use crate::index::btree::BTreeIndex;
use crate::slot::action_chain::{ActionChainSlot, ChainStatus};
use crate::util::hash::hash_id;
use crate::util::{PageType, PAGE_SIZE};
use crate::MemHopError;
use memmap2::MmapMut;

/// Crystallize repeated patterns into L5 action chains using LLM
///
/// Scans existing L5 ActionChainSlots, identifies high-frequency patterns,
/// and creates new consolidated action chains via LLM generation.
///
/// # Arguments
/// * `mmap` - Mutable memory-mapped file for reading/writing memory slots
/// * `header` - File header for page allocation and free list management
/// * `btree` - B-tree index for crystal lookup
/// * `llm` - LLM provider for crystal generation
///
/// # Returns
/// Vector of new L5 action chain IDs created during crystallization
pub fn crystallize_patterns(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    llm: &dyn LlmProvider,
) -> Result<Vec<String>, MemHopError> {
    // Step 1: Scan all existing L5 ActionChainSlots
    let mut existing_chains: Vec<ActionChainSlot> = Vec::new();
    let page_count = header.page_count;

    for page_id in 18..page_count {
        let offset = (page_id as usize) * PAGE_SIZE;
        if offset + PAGE_SIZE > mmap.len() {
            break;
        }

        if let Ok(page_header) = read_page_header(unsafe { &*(mmap.as_ptr() as *const memmap2::Mmap) }, page_id) {
            if page_header.page_type != PageType::ActionChain as u16 {
                continue;
            }

            if let Ok(chain) = ActionChainSlot::deserialize(&mmap[offset + 32..]) {
                existing_chains.push(chain);
            }
        }
    }

    if existing_chains.is_empty() {
        return Ok(vec![]);
    }

    // Step 2: Sort by created_at, take most recent N (N = min(20, total))
    existing_chains.sort_by_key(|c| c.created_at);
    let n = std::cmp::min(20, existing_chains.len());
    let recent_chains = &existing_chains[existing_chains.len() - n..];

    // Step 3: Collect their patterns (trigger + title as description)
    let patterns: Vec<Pattern> = recent_chains.iter().map(|c| {
        Pattern {
            description: format!("{}: {}", c.title, c.trigger),
            frequency: c.trigger_count.max(1),
            confidence: c.confidence,
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

        // Step 5: Create ActionChainSlot
        let now = chrono::Utc::now().timestamp_millis();
        let id_hash = hash_id(&format!("crystal_{}_{}", crystal_def.condition, now));

        let chain = ActionChainSlot {
            id_hash,
            title: format!("crystal_{}", crystal_def.condition),
            trigger: crystal_def.condition,
            status: ChainStatus::Draft,
            confidence: crystal_def.confidence,
            success_rate: 0.0,
            trigger_count: 0,
            last_triggered: 0,
            created_at: now,
            updated_at: now,
            version: 1,
        };

        // Allocate page, write, update btree
        let page_id = allocate_page(mmap, header, PageType::ActionChain, 5, 0)?;  // L5 layer
        let serialized = chain.serialize()?;
        write_page_data(mmap, page_id, &serialized)?;

        let page_ref = crate::file::page::encode_page_ref(page_id, 0);
        btree.insert(id_hash, page_ref);

        new_crystal_ids.push(format!("{:016x}", id_hash));
    }

    Ok(new_crystal_ids)
}

/// Prune low-quality action chains during dream pipeline
///
/// Scans action chain pages and removes chains with low confidence and low trigger counts.
/// This helps maintain crystal quality and prevents accumulation of ineffective rules.
///
/// # Arguments
/// * `mmap` - Mutable memory-mapped file containing action chain slots
/// * `header` - File header for free list management
/// * `page_count` - Total number of pages in the database
///
/// # Returns
/// Vector of pruned action chain IDs (hex strings)
///
/// # Errors
/// Returns `MemHopError` if memory access fails
pub fn prune_low_quality_crystals(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    page_count: u32,
) -> Result<Vec<String>, MemHopError> {
    let mut pruned = Vec::new();

    // Scan all data pages for action chains (skip header pages 0-1 and reserved pages 2-17)
    let start_page = 18;
    let end_page = page_count.min(500);

    for page_id in start_page..end_page {
        let page_offset = (page_id as usize) * PAGE_SIZE;

        if page_offset + PAGE_SIZE > mmap.len() {
            break;
        }

        // Check page type before deserializing — only process ActionChain pages
        if let Ok(page_hdr) = read_page_header(
            unsafe { &*(mmap.as_ptr() as *const memmap2::Mmap) }, page_id
        ) {
            if page_hdr.page_type != PageType::ActionChain as u16 {
                continue;
            }
        } else {
            continue;
        }

        let chain_offset = page_offset + 32;
        if let Ok(chain) = ActionChainSlot::deserialize(&mmap[chain_offset..]) {
            // Low confidence + low trigger count → prune
            if chain.confidence < 0.3 && chain.trigger_count < 5 {
                // 1. Clear page data
                mmap[page_offset..page_offset + PAGE_SIZE].fill(0);

                // 2. Remove from B-tree index
                btree.remove(chain.id_hash);

                // 3. Return to free list
                free_page(mmap, header, page_id)?;

                pruned.push(format!("{:016x}", chain.id_hash));
            }
        }
    }

    Ok(pruned)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::dream::openai_compatible::OpenAICompatibleLlmProvider;
    use crate::file::header::FileHeader;
    use std::io::Write;

    #[test]
    fn test_crystallize_patterns_empty() {
        // Test returns empty list when no L5 action chains exist
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
        let llm = OpenAICompatibleLlmProvider::new(
            "test-key".to_string(),
            "https://api.example.com/v1/chat/completions".to_string(),
            "test-model".to_string(),
        );

        let result = crystallize_patterns(&mut mmap, &mut header, &mut btree, &llm);
        assert!(result.is_ok());
        assert_eq!(result.unwrap().len(), 0);
    }

    #[test]
    fn test_crystallize_creates_crystals() {
        // TODO: Create L5 action chains and verify crystallization results
    }

    #[test]
    fn test_crystallize_fallback_no_llm() {
        // TODO: Simulate LLM unavailability and verify regex fallback still produces results
    }
}
