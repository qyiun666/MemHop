//! Stage 2: Temporal Bind - Create Temporal hyperedges between time-adjacent engrams
//!
//! This stage identifies engrams created within a specified time window and creates
//! Temporal hyperedges to link them chronologically.

use crate::file::free_list::allocate_from_free_list;
use crate::file::header::FileHeader;
use crate::slot::engram::EngramSlot;
use crate::slot::hyperedge::{HyperedgeKind, HyperedgeSlot};
use crate::util::{hash_id, PAGE_SIZE};
use crate::MemHopError;
use memmap2::MmapMut;

/// Create temporal hyperedges between engrams within time window
///
/// # Arguments
/// * `mmap` - Mutable memory-mapped file
/// * `header` - File header for free list management
/// * `page_count` - Total number of pages in the database
/// * `time_window` - Time range (start_timestamp, end_timestamp) in milliseconds
///
/// # Returns
/// Vector of temporal hyperedge IDs (in hex format) that were created
pub fn create_temporal_edges(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    page_count: u32,
    time_window: (i64, i64),
) -> Result<Vec<String>, MemHopError> {
    let mut edge_ids = Vec::new();
    let mut engrams_in_window: Vec<(u64, i64)> = Vec::new();

    // Collect engrams within time window
    // Scan all data pages (skip page 0 and 1 which are headers)
    for page_id in 2..page_count {
        let engram_offset = (page_id as usize) * PAGE_SIZE + 32;

        if engram_offset >= mmap.len() {
            break;
        }

        if let Ok(engram) = EngramSlot::deserialize(&mmap[engram_offset..]) {
            if engram.created_at >= time_window.0 && engram.created_at <= time_window.1 {
                engrams_in_window.push((engram.id_hash, engram.created_at));
            }
        }
    }

    // Sort by timestamp
    engrams_in_window.sort_by_key(|&(_, ts)| ts);

    // Create temporal hyperedges between adjacent engrams
    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as i64;

    for i in 0..engrams_in_window.len().saturating_sub(1) {
        let (id1, _) = engrams_in_window[i];
        let (id2, _) = engrams_in_window[i + 1];

        let edge = HyperedgeSlot {
            id_hash: hash_id(&format!("temporal_{:?}_{:?}", id1, id2)),
            kind: HyperedgeKind::Temporal,
            node_ptrs: vec![id1, id2],
            meta: vec![],
            weight: 1.0,
            created_at: now,
            updated_at: 0,
            version: 1,
            overflow_page: 0,
        };

        let edge_data = edge
            .serialize()
            .map_err(|e| MemHopError::Serialization(e.to_string()))?;

        let page_id = allocate_from_free_list(mmap, header)?;
        let edge_offset = (page_id as usize) * PAGE_SIZE + 32;

        if edge_offset + edge_data.len() <= mmap.len() {
            mmap[edge_offset..edge_offset + edge_data.len()].copy_from_slice(&edge_data);

            edge_ids.push(format!("{:016x}", edge.id_hash));
        }
    }

    Ok(edge_ids)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;

    #[test]
    fn test_create_temporal_edges_empty() {
        // Test with empty database - just verify it doesn't panic
        let temp_file = tempfile::NamedTempFile::new().unwrap();
        let path = temp_file.path();

        let mut file = std::fs::File::create(path).unwrap();
        file.write_all(&vec![0u8; PAGE_SIZE * 50]).unwrap();
        drop(file);

        let file = std::fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open(path)
            .unwrap();

        let mut mmap = unsafe { MmapMut::map_mut(&file).unwrap() };
        let mut header = FileHeader::new(768);

        // Should not panic even with uninitialized pages
        // Note: This may return an error if no free pages are available, which is expected
        let _result = create_temporal_edges(&mut mmap, &mut header, 50, (0, i64::MAX));
        // Just verify it doesn't panic
    }
}
