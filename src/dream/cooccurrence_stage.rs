//! Stage 5: Co-occurrence - Create co-occurrence hyperedges
//!
//! This stage analyzes keyword co-occurrence patterns across engrams and creates
//! CoOccurrence hyperedges to capture semantic relationships.

use crate::file::header::FileHeader;
use crate::index::sparse::SparseIndex;
use crate::organize::cooccurrence::create_cooccurrence_hyperedges;
use crate::MemHopError;
use memmap2::MmapMut;
use std::collections::HashSet;

/// Create co-occurrence hyperedges (full implementation)
///
/// # Arguments
/// * `mmap` - Mutable memory-mapped file
/// * `header` - File header for page allocation and free list management
/// * `sparse_index` - Sparse index for keyword lookup
/// * `session_topics` - Set of active topic IDs from current session
///
/// # Returns
/// Vector of co-occurrence hyperedge IDs (in hex format) that were created
pub fn create_cooccurrence_edges(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    _sparse_index: &SparseIndex,
    session_topics: &HashSet<u64>,
) -> Result<Vec<String>, MemHopError> {
    if session_topics.is_empty() {
        return Ok(vec![]);
    }

    // Call organize::create_cooccurrence_hyperedges with real header
    let edge_ids = create_cooccurrence_hyperedges(
        mmap,
        header,  // Use the passed header instead of creating a temporary one
        &[],
        session_topics,
    )?;

    if edge_ids.is_empty() {
        return Ok(vec![]);
    }

    // Return the actual edge IDs that were created
    Ok(edge_ids)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;

    #[test]
    fn test_create_cooccurrence_edges_empty() {
        // Test returns empty list for now
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
        let session_topics = HashSet::new();
        let result = create_cooccurrence_edges(&mut mmap, &mut header, &SparseIndex::new(), &session_topics);
        assert!(result.is_ok());
        assert_eq!(result.unwrap().len(), 0);
    }
}
