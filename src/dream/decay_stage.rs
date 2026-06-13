//! Stage 1: Decay - Demote low-activation memories to Dormant state
//!
//! This stage scans all L1 engrams and calculates their activation scores.
//! Engrams with activation below the threshold are demoted to Dormant state.

use crate::activation::{ActivationConfig, ActivationManager};
use crate::slot::engram::EngramSlot;
use crate::util::PAGE_SIZE;
use crate::MemHopError;
use memmap2::MmapMut;

/// Apply decay to all L1 engrams and demote low-activation ones to Dormant
///
/// # Arguments
/// * `mmap` - Mutable memory-mapped file
/// * `page_count` - Total number of pages in the database
/// * `prune_threshold` - Activation score threshold below which engrams are demoted
/// * `activation_config` - Activation configuration for calculating scores
///
/// # Returns
/// Vector of engram IDs (in hex format) that were demoted to Dormant state
pub fn apply_decay(
    mmap: &mut MmapMut,
    page_count: u32,
    prune_threshold: f32,
    activation_config: &ActivationConfig,
) -> Result<Vec<String>, MemHopError> {
    let manager = ActivationManager::new(activation_config.clone());
    let mut dormant_ids = Vec::new();
    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as i64;

    // Scan all data pages (skip page 0 and 1 which are headers)
    for page_id in 2..page_count {
        let engram_offset = (page_id as usize) * PAGE_SIZE + 32;

        if engram_offset >= mmap.len() {
            break;
        }

        // Try to deserialize engram
        if let Ok(mut engram) = EngramSlot::deserialize(&mmap[engram_offset..]) {
            // Calculate hours since last access
            let hours_since_access = ((now - engram.updated_at) as f32) / 3600000.0;

            // Use proper activation decay formula from activation module
            let activation_score = manager.calculate_score(engram.importance, hours_since_access);

            // Demote to Dormant if below threshold and currently Active
            if activation_score < prune_threshold && engram.memory_state == 0 {
                engram.memory_state = 2; // Dormant state (MemoryState::Dormant = 2)
                let updated_data = engram
                    .serialize()
                    .map_err(|e| MemHopError::Serialization(e.to_string()))?;

                if engram_offset + updated_data.len() <= mmap.len() {
                    mmap[engram_offset..engram_offset + updated_data.len()]
                        .copy_from_slice(&updated_data);
                }

                dormant_ids.push(format!("{:016x}", engram.id_hash));
            }
        }
    }

    Ok(dormant_ids)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;

    #[test]
    fn test_apply_decay_empty() {
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

        // Should not panic even with uninitialized pages
        let config = crate::activation::ActivationConfig::default();
        let result = apply_decay(&mut mmap, 100, 0.5, &config);
        assert!(result.is_ok());
    }
}
