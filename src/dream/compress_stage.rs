//! Stage 6: L1→L2 Compression - Cluster recent engrams into L2 topics
//!
//! This stage uses clustering algorithms and LLM summarization to compress
//! episodic memories (L1) into semantic topics (L2).

use crate::dream::llm::LlmProvider;
use crate::file::header::FileHeader;
use crate::file::page::{allocate_page, read_page_header, write_page_data};
use crate::index::btree::BTreeIndex;
use crate::index::sparse::SparseIndex;
use crate::slot::engram::EngramSlot;
use crate::slot::topic::TopicSlot;
use crate::util::hash::hash_id;
use crate::util::{PageType, PAGE_SIZE};
use crate::MemHopError;
use half::f16;
use memmap2::MmapMut;
use std::collections::{HashMap, HashSet, VecDeque};

/// Compress L1 engrams into L2 topics using LLM summarization
///
/// # Arguments
/// * `mmap` - Mutable memory-mapped file for reading/writing memory slots
/// * `header` - File header for page allocation and free list management
/// * `btree` - B-tree index for topic lookup
/// * `sparse_index` - Sparse index for keyword lookup
/// * `llm` - LLM provider for generating topic summaries
/// * `time_window` - Time range for selecting recent engrams
///
/// # Returns
/// Vector of new L2 topic IDs created during compression
pub fn compress_l1_to_l2(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    _sparse_index: &SparseIndex,
    llm: &dyn LlmProvider,
    time_window: (i64, i64),
) -> Result<Vec<String>, MemHopError> {
    // Step 1: Scan all EngramSlots, filter edge_count >= 3 within time window
    let mut high_connectivity_engrams: Vec<(u32, EngramSlot)> = Vec::new();
    let page_count = header.page_count;

    for page_id in 18..page_count {  // Skip reserved pages
        let offset = (page_id as usize) * PAGE_SIZE;
        if offset + PAGE_SIZE > mmap.len() {
            break;
        }

        // Read page header to check type
        if let Ok(page_header) = read_page_header(unsafe { &*(mmap.as_ptr() as *const memmap2::Mmap) }, page_id) {
            if page_header.page_type != PageType::Engram as u16 {
                continue;
            }

            // Deserialize engram
            if let Ok(engram) = EngramSlot::deserialize(&mmap[offset + 32..]) {
                // Check time window and connectivity
                if engram.created_at >= time_window.0 
                    && engram.created_at <= time_window.1 
                    && engram.edge_count >= 3 
                {
                    high_connectivity_engrams.push((page_id, engram));
                }
            }
        }
    }

    if high_connectivity_engrams.is_empty() {
        return Ok(vec![]);
    }

    // Step 2: Greedy clustering
    let mut assigned = HashSet::new();
    let mut clusters: Vec<Vec<(u32, EngramSlot)>> = Vec::new();

    while assigned.len() < high_connectivity_engrams.len() {
        // Find first unassigned node as seed
        let seed_idx = high_connectivity_engrams.iter()
            .position(|(page_id, _)| !assigned.contains(page_id))
            .unwrap();
        
        let (seed_page_id, seed_engram) = &high_connectivity_engrams[seed_idx];
        assigned.insert(*seed_page_id);

        let mut cluster = vec![(*seed_page_id, seed_engram.clone())];

        // BFS depth=1 through edge_ptrs to collect neighbors
        let mut queue = VecDeque::new();
        for neighbor_page_ref in &seed_engram.edge_ptrs {
            if *neighbor_page_ref == 0 {
                continue;
            }
            let (neighbor_page_id, _) = crate::file::page::decode_page_ref(*neighbor_page_ref);
            queue.push_back(neighbor_page_id);
        }

        while let Some(neighbor_page_id) = queue.pop_front() {
            if assigned.contains(&neighbor_page_id) {
                continue;
            }

            // Find engram in that page
            if let Some(neighbor_engram) = high_connectivity_engrams.iter()
                .find(|(pid, _)| *pid == neighbor_page_id)
                .map(|(_, e)| e.clone())
            {
                // Calculate cosine similarity
                let similarity = calculate_cosine_similarity(
                    mmap, 
                    seed_engram.vector_page_ref, 
                    neighbor_engram.vector_page_ref
                ).unwrap_or(0.0);

                if similarity > 0.7 {
                    assigned.insert(neighbor_page_id);
                    cluster.push((neighbor_page_id, neighbor_engram));
                }
            }
        }

        // Only keep clusters with size >= 3, max 10 new topics
        if cluster.len() >= 3 && clusters.len() < 10 {
            clusters.push(cluster);
        }
    }

    // Step 3: Create TopicSlot for each cluster
    let mut new_topic_ids = Vec::new();

    for cluster in &clusters {
        // 3a: Compute centroid_vector (average vector)
        let centroid = compute_centroid_vector(mmap, cluster.iter().map(|(_, e)| e.vector_page_ref))?;

        // 3b: Aggregate all engram keywords, take top-5 as title
        let mut keyword_freq: HashMap<String, usize> = HashMap::new();
        let mut all_texts = Vec::new();
        let mut node_ids = Vec::new();
        let mut total_importance = 0.0;

        for (_, engram) in cluster {
            all_texts.push(engram.text.clone());
            node_ids.push(engram.id_hash);
            total_importance += engram.importance;

            for kw in &engram.keywords {
                *keyword_freq.entry(kw.clone()).or_insert(0) += 1;
            }
        }

        let mut sorted_keywords: Vec<_> = keyword_freq.into_iter().collect();
        sorted_keywords.sort_by_key(|b| std::cmp::Reverse(b.1));
        let title = sorted_keywords.into_iter()
            .take(5)
            .map(|(k, _)| k)
            .collect::<Vec<_>>()
            .join(", ");

        // 3c: Call LLM to generate summary (fallback on error)
        let summary = match llm.summarize(&all_texts) {
            Ok(s) => Some(s),
            Err(e) => {
                eprintln!("LLM summarize failed, using fallback: {:?}", e);
                Some(llm.fallback_summarize(&all_texts))
            }
        };

        // 3d: Create TopicSlot
        let now = chrono::Utc::now().timestamp_millis();
        let id_hash = hash_id(&format!("compressed_topic_{}_{}", title, now));
        
        let topic = TopicSlot {
            id_hash,
            title,
            summary,
            node_ids,
            l3_refs: vec![], l4_refs: vec![], parent_id: None,
            created_at: now,
            updated_at: now,
            version: 1,
            importance: (total_importance / cluster.len() as f32) * 1.1,
            activation_score: 0.5,
            is_active: true,
            activation_state: crate::slot::topic::ActivationState::Active,
            centroid_vector: Some(centroid),
            domain_weights: vec![],
            dialogue_range: (
                cluster.iter().map(|(_, e)| e.created_at).min().unwrap_or(0),
                cluster.iter().map(|(_, e)| e.created_at).max().unwrap_or(0),
            ),
            reserved: [0; 16],
        };

        // 3e: Allocate page, serialize, write to mmap
        let page_id = allocate_page(mmap, PageType::Topic, 2, 0)?;  // L2 layer
        let serialized = topic.serialize()?;
        write_page_data(mmap, page_id, &serialized)?;

        // 3f: Update btree + sparse_index
        let page_ref = crate::file::page::encode_page_ref(page_id, 0);
        btree.insert(id_hash, page_ref);

        new_topic_ids.push(format!("{:016x}", id_hash));
    }

    Ok(new_topic_ids)
}

/// Calculate cosine similarity between two vector pages
fn calculate_cosine_similarity(
    mmap: &MmapMut,
    vec_ref_a: u64,
    vec_ref_b: u64,
) -> Result<f32, MemHopError> {
    let (page_a, slot_a) = crate::file::page::decode_page_ref(vec_ref_a);
    let (page_b, slot_b) = crate::file::page::decode_page_ref(vec_ref_b);

    // Read vector data from pages (assuming vectors stored in page data area)
    let offset_a = (page_a as usize) * PAGE_SIZE + 32 + (slot_a as usize) * 768 * 2;  // f16 = 2 bytes
    let offset_b = (page_b as usize) * PAGE_SIZE + 32 + (slot_b as usize) * 768 * 2;

    if offset_a + 768 * 2 > mmap.len() || offset_b + 768 * 2 > mmap.len() {
        return Ok(0.0);
    }

    let vec_a: Vec<f32> = (0..768)
        .map(|i| f16::from_le_bytes([
            mmap[offset_a + i * 2],
            mmap[offset_a + i * 2 + 1],
        ]).to_f32())
        .collect();

    let vec_b: Vec<f32> = (0..768)
        .map(|i| f16::from_le_bytes([
            mmap[offset_b + i * 2],
            mmap[offset_b + i * 2 + 1],
        ]).to_f32())
        .collect();

    // Calculate cosine similarity
    let dot_product: f32 = vec_a.iter().zip(vec_b.iter()).map(|(a, b)| a * b).sum();
    let norm_a: f32 = vec_a.iter().map(|x| x * x).sum::<f32>().sqrt();
    let norm_b: f32 = vec_b.iter().map(|x| x * x).sum::<f32>().sqrt();

    if norm_a < f32::EPSILON || norm_b < f32::EPSILON {
        Ok(0.0)
    } else {
        Ok(dot_product / (norm_a * norm_b))
    }
}

/// Compute centroid vector from multiple vector references
fn compute_centroid_vector(
    mmap: &MmapMut,
    vector_refs: impl Iterator<Item = u64>,
) -> Result<Vec<f16>, MemHopError> {
    let mut vectors: Vec<Vec<f32>> = Vec::new();

    for vec_ref in vector_refs {
        let (page_id, slot_index) = crate::file::page::decode_page_ref(vec_ref);
        let offset = (page_id as usize) * PAGE_SIZE + 32 + (slot_index as usize) * 768 * 2;

        if offset + 768 * 2 > mmap.len() {
            continue;
        }

        let vec: Vec<f32> = (0..768)
            .map(|i| {
                f16::from_le_bytes([
                    mmap[offset + i * 2],
                    mmap[offset + i * 2 + 1],
                ]).to_f32()
            })
            .collect();

        vectors.push(vec);
    }

    if vectors.is_empty() {
        return Ok(vec![f16::from_f32(0.0); 768]);
    }

    // Calculate average
    let dim = vectors[0].len();
    let mut centroid = vec![0.0f32; dim];

    for vec in &vectors {
        for i in 0..dim {
            centroid[i] += vec[i];
        }
    }

    for i in 0..dim {
        centroid[i] /= vectors.len() as f32;
    }

    // Convert to f16
    Ok(centroid.into_iter().map(f16::from_f32).collect())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::dream::deepseek_llm::DeepSeekLlmProvider;
    use crate::file::header::FileHeader;
    use std::io::Write;

    #[test]
    fn test_compress_l1_to_l2_empty() {
        // Test returns empty list when no engrams exist
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
        let llm = DeepSeekLlmProvider::new("test-key".to_string());
        
        let result = compress_l1_to_l2(
            &mut mmap, 
            &mut header, 
            &mut btree, 
            &sparse_index, 
            &llm, 
            (0, i64::MAX)
        );
        
        assert!(result.is_ok());
        // Should return empty list since there are no engrams
        assert_eq!(result.unwrap().len(), 0);
    }

    #[test]
    fn test_compress_centroid_vector() {
        // Test centroid vector computation with mock data
        // This is a basic sanity check
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

        let mmap = unsafe { MmapMut::map_mut(&file).unwrap() };
        
        // With no valid vectors, should return zero vector
        let result = compute_centroid_vector(&mmap, std::iter::empty());
        assert!(result.is_ok());
        let centroid = result.unwrap();
        assert_eq!(centroid.len(), 768);
    }
}
