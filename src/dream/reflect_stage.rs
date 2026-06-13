//! Stage 4: Topic Reflect - Generate summaries for topics
//!
//! This stage aggregates keywords from engrams within each topic and generates
//! concise summaries to improve topic discoverability.

use crate::file::page::decode_page_ref;
use crate::index::btree::BTreeIndex;
use crate::index::sparse::SparseIndex;
use crate::organize::reflect::reflect_topic;
use crate::slot::topic::TopicSlot;
use crate::util::PAGE_SIZE;
use crate::MemHopError;
use memmap2::MmapMut;

/// Reflect on all topics and generate summaries (full implementation)
///
/// # Arguments
/// * `mmap` - Mutable memory-mapped file
/// * `btree` - B-tree index for topic lookup
///
/// # Returns
/// Vector of new topic IDs created during reflection
pub fn reflect_all_topics(
    mmap: &mut MmapMut,
    btree: &BTreeIndex,
) -> Result<Vec<String>, MemHopError> {
    let sparse_index = SparseIndex::new();
    let mut updated_topic_ids: Vec<String> = Vec::new();

    // Iterate through all topics in B-tree
    for (&id_hash, &page_ref) in btree.iter() {
        let (page_id, _slot_index) = decode_page_ref(page_ref);
        let offset = (page_id as usize) * PAGE_SIZE + 32;

        if offset >= mmap.len() {
            continue;
        }

        // Deserialize topic
        if let Ok(topic) = TopicSlot::deserialize(&mmap[offset..]) {
            // Skip topics that already have a summary
            if topic.summary.is_some() && !topic.summary.as_ref().unwrap().is_empty() {
                continue;
            }

            // Call organize::reflect_topic to generate summary
            let topic_id_str = format!("{:016x}", id_hash);
            match reflect_topic(&topic_id_str, mmap, btree, &sparse_index) {
                Ok(Some(summary)) => {
                    // Update topic's summary field
                    let mut updated_topic = topic;
                    updated_topic.summary = Some(summary.clone());

                    let topic_data = updated_topic
                        .serialize()
                        .map_err(|e| MemHopError::Serialization(e.to_string()))?;

                    if offset + topic_data.len() <= mmap.len() {
                        mmap[offset..offset + topic_data.len()].copy_from_slice(&topic_data);
                        updated_topic_ids.push(topic_id_str);
                    }
                }
                Ok(None) => {
                    // Topic already has summary or no engrams found
                    continue;
                }
                Err(_) => {
                    // Skip topics that fail reflection
                    continue;
                }
            }
        }
    }

    Ok(updated_topic_ids)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;

    #[test]
    fn test_reflect_all_topics_empty() {
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
        let result = reflect_all_topics(&mut mmap, &BTreeIndex::new());
        assert!(result.is_ok());
        assert_eq!(result.unwrap().len(), 0);
    }
}
