//! L0 Profile Generation - Extract agent persona from topic keywords
//!
//! This stage analyzes the distribution of topic keywords to generate
//! an L0 Profile representing the agent's personality and expertise areas.

use crate::file::header::FileHeader;
use crate::index::btree::BTreeIndex;
use crate::index::sparse::SparseIndex;
use crate::slot::engram::EngramSlot;
use crate::util::{get_current_timestamp, hash_id, PAGE_SIZE};
use crate::MemHopError;
use memmap2::MmapMut;

/// Generate L0 profile from topic keyword distribution
///
/// # Arguments
/// * `mmap` - Mutable memory-mapped file
/// * `header` - File header for accessing L0 reserved page
/// * `btree` - B-tree index for topic lookup
/// * `sparse_index` - Sparse index for keyword frequency analysis
///
/// # Returns
/// Ok(()) on success
pub fn generate_profile(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &BTreeIndex,
    sparse_index: &SparseIndex,
) -> Result<(), MemHopError> {
    // 1. Get top keywords from sparse index
    let top_keywords_with_freq = sparse_index.top_terms(20);
    let top_keywords: Vec<String> = top_keywords_with_freq.iter().map(|(term, _)| term.clone()).collect();

    // 2. Count total engrams
    let total_engrams = btree.len();

    // 3. Generate profile text in JSON format
    let now_ms = get_current_timestamp() as u128;

    let profile_text = serde_json::json!({
        "top_keywords": top_keywords,
        "total_engrams": total_engrams,
        "generated_at": now_ms,
    })
    .to_string();

    // 4. Create special EngramSlot as L0 Profile
    let doc_len = profile_text.len() as u16;
    let profile_engram = EngramSlot {
        id_hash: hash_id("l0_profile"),
        text: profile_text,
        summary: None,
        keywords: top_keywords.iter().take(10).cloned().collect(),
        created_at: now_ms as i64,
        updated_at: now_ms as i64,
        version: 1,
        edge_count: 0,
        doc_len,
        vector_page_ref: 0,
        is_structural: true,
        source_type: 0,
        memory_state: 0, // Active
        emotion_type: 0,
        valence: 0.0,
        arousal: 0.0,
        importance: 1.0, // Highest priority
        edge_ptrs: [0; 8],
    };

    // 5. Write to header.layer_roots[0] (L0 reserved page)
    let root_page = header.layer_roots[0];
    if root_page != 0 && root_page < header.page_count {
        let data = profile_engram
            .serialize()
            .map_err(|e| MemHopError::Serialization(e.to_string()))?;
        let offset = (root_page as usize) * PAGE_SIZE + 32;
        if offset + data.len() <= mmap.len() {
            mmap[offset..offset + data.len()].copy_from_slice(&data);
        }
    }

    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;

    #[test]
    fn test_generate_profile_empty() {
        // Test returns Ok for now
        let temp_file = tempfile::NamedTempFile::new().unwrap();
        let path = temp_file.path();

        let mut file = std::fs::File::create(path).unwrap();
        file.write_all(&vec![0u8; 4096]).unwrap();
        drop(file);

        let file = std::fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open(path)
            .unwrap();

        let mut mmap = unsafe { MmapMut::map_mut(&file).unwrap() };
        let mut header = crate::file::header::FileHeader::new(768);
        let btree = BTreeIndex::new();
        let sparse_index = SparseIndex::new();
        let result = generate_profile(&mut mmap, &mut header, &btree, &sparse_index);
        assert!(result.is_ok());
    }
}
