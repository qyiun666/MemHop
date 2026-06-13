// Batch Store API implementation
use crate::encoder::ipc::Encoder;
use crate::file::free_list::allocate_from_free_list;
use crate::file::header::FileHeader;
use crate::index::btree::BTreeIndex;
use crate::index::sparse::SparseIndex;
use crate::slot::engram::EngramSlot;
use crate::slot::hyperedge::{HyperedgeKind, HyperedgeSlot};
use crate::slot::topic::TopicSlot;
use crate::util::{hash_id, PAGE_SIZE};
use crate::util::{SourceMeta, SourceRef};
use crate::MemHopError;
use half::f16;
use memmap2::MmapMut;
use std::collections::HashMap;

/// Batch store request containing multiple items to be stored
#[derive(Debug, Clone)]
pub struct StoreBatch {
    /// List of items to store
    pub items: Vec<StoreItem>,
    /// Optional session ID for tracking
    pub session_id: Option<String>,
    /// Optional turn ID within the session
    pub turn_id: Option<String>,
}

/// Individual item in a batch store operation
#[derive(Debug, Clone)]
pub struct StoreItem {
    /// The text content to store
    pub text: String,
    /// Optional topic label for semantic organization
    pub topic_label: Option<String>,
    /// Optional domain identifier (for L3 procedural memory)
    pub domain_id: Option<String>,
    /// Importance score (0.0 - 1.0), affects memory retention
    pub importance: Option<f32>,
    /// Valence: emotional pleasantness (-1.0 ~ 1.0)
    pub valence: Option<f64>,
    /// Arousal: emotional activation level (0 ~ 1.0)
    pub arousal: Option<f64>,
    /// Metadata about the source of this memory
    pub source: SourceMeta,
    /// Whether this is structural knowledge (vs episodic)
    pub is_structural: bool,
    /// Optional reference to external source location
    pub source_ref: Option<SourceRef>,
}

/// Encoded item ready for storage after encoding
#[derive(Debug, Clone)]
pub struct EncodedItem {
    /// Original text content
    pub text: String,
    /// Dense vector embedding (f16 for memory efficiency)
    pub dense: Vec<f16>,
    /// Sparse representation (term -> weight mapping)
    pub sparse: HashMap<String, f32>,
    /// Topic label if assigned
    pub topic_label: Option<String>,
    /// Domain identifier for L3 memory
    pub domain_id: Option<String>,
    /// Normalized importance score
    pub importance: f32,
    /// Emotional valence
    pub valence: f64,
    /// Emotional arousal
    pub arousal: f64,
    /// Whether this represents structural knowledge
    pub is_structural: bool,
}

/// Report generated after batch store operation
#[derive(Debug, Clone)]
pub struct BatchReport {
    /// Number of documents stored at L4 (archive layer)
    pub l4_docs: u32,
    /// Number of new L1 nodes created (episodic memories)
    pub l1_nodes_created: u32,
    /// Number of L1 nodes updated (deduplication hits)
    pub l1_nodes_updated: u32,
    /// Number of L2 topics updated or created
    pub l2_topics_updated: u32,
    /// Number of L3 nodes (procedural memories)
    pub l3_nodes: u32,
    /// Number of hyperedges created (associations)
    pub edges_created: u32,
    /// Number of items skipped due to deduplication
    pub dedup_skipped: u32,
}

/// Split long text into chunks (max 512 characters)
pub fn split_long_text(text: &str, max_len: usize) -> Vec<String> {
    if text.len() <= max_len {
        return vec![text.to_string()];
    }

    let mut chunks = Vec::new();
    let mut current_chunk = String::new();

    // Split by sentences or paragraphs
    for sentence in text.split(['。', '.', '\n']) {
        if current_chunk.len() + sentence.len() > max_len && !current_chunk.is_empty() {
            chunks.push(current_chunk.clone());
            current_chunk.clear();
        }
        if !sentence.is_empty() {
            current_chunk.push_str(sentence);
        }
    }

    if !current_chunk.is_empty() {
        // If current chunk is still too long, split it further
        if current_chunk.len() > max_len {
            let chars: Vec<char> = current_chunk.chars().collect();
            let mut start = 0;
            while start < chars.len() {
                let end = std::cmp::min(start + max_len, chars.len());
                chunks.push(chars[start..end].iter().collect());
                start = end;
            }
        } else {
            chunks.push(current_chunk);
        }
    }

    chunks
}

/// Encode all items in batch using the encoder
pub fn encode_items(
    items: &[StoreItem],
    encoder: &dyn Encoder,
) -> Result<Vec<EncodedItem>, MemHopError> {
    let mut encoded = Vec::new();

    for item in items {
        let chunks = split_long_text(&item.text, 512);

        for chunk in chunks {
            let output = encoder.encode(&chunk);
            encoded.push(EncodedItem {
                text: chunk,
                dense: output.dense,
                sparse: output.sparse,
                topic_label: item.topic_label.clone(),
                domain_id: item.domain_id.clone(),
                importance: item.importance.unwrap_or(0.5),
                valence: item.valence.unwrap_or(0.0),
                arousal: item.arousal.unwrap_or(0.0),
                is_structural: item.is_structural,
            });
        }
    }

    Ok(encoded)
}

/// Archive documents to L4 (simplified implementation)
pub fn archive_documents(
    _mmap: &mut MmapMut,
    items: &[EncodedItem],
    _batch: &StoreBatch,
) -> Result<Vec<u64>, MemHopError> {
    // Simplified: just return doc IDs without actual archival
    // TODO: Implement full L4 archival with turn_index and session_index
    let doc_ids = items.iter().map(|item| hash_id(&item.text)).collect();

    Ok(doc_ids)
}

/// Calculate n-gram Jaccard similarity between two texts
#[allow(dead_code)]
fn calculate_ngram_jaccard(text1: &str, text2: &str) -> f64 {
    let ngrams1: std::collections::HashSet<String> = text1
        .chars()
        .collect::<Vec<_>>()
        .windows(3)
        .map(|w| w.iter().collect())
        .collect();

    let ngrams2: std::collections::HashSet<String> = text2
        .chars()
        .collect::<Vec<_>>()
        .windows(3)
        .map(|w| w.iter().collect())
        .collect();

    let intersection = ngrams1.intersection(&ngrams2).count();
    let union = ngrams1.union(&ngrams2).count();

    if union == 0 {
        0.0
    } else {
        intersection as f64 / union as f64
    }
}

/// Check for duplicate engram using cosine similarity and Jaccard overlap
fn check_duplicate(
    mmap: &MmapMut,
    item: &EncodedItem,
    btree: &BTreeIndex,
    vector_dim: usize,
) -> Result<Option<u64>, MemHopError> {
    use crate::index::vector::cosine_similarity;
    use crate::slot::engram::EngramSlot;

    // Thresholds for deduplication
    const COSINE_THRESHOLD: f32 = 0.95;
    const JACCARD_THRESHOLD: f32 = 0.8;

    // Iterate through all entries in btree
    for (&existing_hash, &page_ref) in btree.iter() {
        let page_id = (page_ref >> 16) as u32;
        let engram_offset = (page_id as usize) * PAGE_SIZE + 32;

        if engram_offset >= mmap.len() {
            continue;
        }

        // Deserialize existing engram
        if let Ok(existing_engram) = EngramSlot::deserialize(&mmap[engram_offset..]) {
            // Check 1: Vector cosine similarity
            if existing_engram.vector_page_ref != 0 {
                let vec_page_id = (existing_engram.vector_page_ref >> 16) as u32;
                let vec_offset = (vec_page_id as usize) * PAGE_SIZE + 32;

                if vec_offset + vector_dim * 2 <= mmap.len() {
                    // Read existing vector (f16)
                    let mut existing_vec = Vec::with_capacity(vector_dim);
                    for i in 0..vector_dim {
                        let bytes = [mmap[vec_offset + i * 2], mmap[vec_offset + i * 2 + 1]];
                        existing_vec.push(f16::from_le_bytes(bytes));
                    }

                    // Calculate cosine similarity
                    let cosine_sim = cosine_similarity(&item.dense, &existing_vec);
                    if cosine_sim > COSINE_THRESHOLD {
                        return Ok(Some(existing_hash));
                    }
                }
            }

            // Check 2: Keyword Jaccard similarity
            let existing_keywords: std::collections::HashSet<&str> = existing_engram
                .keywords
                .iter()
                .map(|s| s.as_str())
                .collect();
            let new_keywords: std::collections::HashSet<&str> =
                item.sparse.keys().map(|s| s.as_str()).collect();

            if !existing_keywords.is_empty() && !new_keywords.is_empty() {
                let intersection = existing_keywords.intersection(&new_keywords).count();
                let union = existing_keywords.union(&new_keywords).count();

                if union > 0 {
                    let jaccard = intersection as f32 / union as f32;
                    if jaccard > JACCARD_THRESHOLD {
                        return Ok(Some(existing_hash));
                    }
                }
            }
        }
    }

    Ok(None)
}

/// Infer emotion type from valence and arousal
fn infer_emotion_type(valence: f64, arousal: f64) -> u8 {
    use crate::dream::emotion::infer_emotion;
    infer_emotion(valence, arousal) as u8
}

/// Write L1 engrams with deduplication
pub fn dedup_and_write_l1(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    items: &[EncodedItem],
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    vector_dim: usize,
) -> Result<(Vec<u64>, u32, u32, u32), MemHopError> {
    let mut node_ids = Vec::new();
    let mut created = 0u32;
    let mut updated = 0u32;
    let skipped = 0u32;

    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as i64;

    for item in items {
        let id_hash = hash_id(&item.text);

        // Check for duplicates
        if let Some(existing_id) = check_duplicate(mmap, item, btree, vector_dim)? {
            // Update existing engram (simplified)
            updated += 1;
            node_ids.push(existing_id);
            continue;
        }

        // Create new Engram slot
        let engram = EngramSlot {
            id_hash,
            text: item.text.clone(),
            summary: None,
            keywords: vec![],
            created_at: now,
            updated_at: now,
            version: 1,
            edge_count: 0,
            doc_len: item.text.len() as u16,
            vector_page_ref: 0,
            is_structural: item.is_structural,
            source_type: 0,
            memory_state: 0,
            emotion_type: infer_emotion_type(item.valence, item.arousal),
            valence: item.valence as f32,
            arousal: item.arousal as f32,
            importance: item.importance,
            edge_ptrs: [0; 8],
        };

        let engram_data = engram
            .serialize()
            .map_err(|e| MemHopError::Serialization(e.to_string()))?;

        // Allocate page from free list
        let page_id = allocate_from_free_list(mmap, header)?;

        // Write engram to page (skip 32-byte header)
        let engram_offset = (page_id as usize) * 4096 + 32;
        if engram_offset + engram_data.len() <= mmap.len() {
            mmap[engram_offset..engram_offset + engram_data.len()].copy_from_slice(&engram_data);
        }

        // Update B-tree index
        let page_ref = (page_id as u64) << 16;
        btree.insert(id_hash, page_ref);

        // Update BM25 sparse index
        let terms: Vec<String> = item
            .text
            .split_whitespace()
            .map(|s| s.to_lowercase())
            .collect();
        sparse_index.add_document(id_hash, terms, item.text.len() as u32);

        created += 1;
        node_ids.push(id_hash);
    }

    Ok((node_ids, created, updated, skipped))
}

/// Update L2 topics based on topic labels
pub fn update_topics(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    items: &[EncodedItem],
    l1_node_ids: &[u64],
) -> Result<u32, MemHopError> {
    let mut topics_updated = 0u32;

    // Group items by topic_label
    let mut topic_groups: HashMap<String, Vec<usize>> = HashMap::new();

    for (idx, item) in items.iter().enumerate() {
        if let Some(ref label) = item.topic_label {
            topic_groups
                .entry(label.clone())
                .or_default()
                .push(idx);
        }
    }

    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as i64;

    for (label, indices) in topic_groups {
        let topic_id = hash_id(&label);

        // Create or find topic (simplified: always create new)
        let topic = TopicSlot {
            id_hash: topic_id,
            title: label.clone(),
            summary: None,
            node_ids: indices.iter().map(|&idx| l1_node_ids[idx]).collect(),
            l3_refs: vec![], l4_refs: vec![], parent_id: None,
            created_at: now,
            updated_at: now,
            version: 1,
            importance: 0.5,
            activation_score: 0.5,
            is_active: false,
            activation_state: crate::slot::topic::ActivationState::Dormant,
            centroid_vector: None,
            domain_weights: vec![],
            dialogue_range: (now, now),
            reserved: [0; 16],
        };

        let topic_data = topic
            .serialize()
            .map_err(|e| MemHopError::Serialization(e.to_string()))?;

        // Allocate page and write topic
        let page_id = allocate_from_free_list(mmap, header)?;
        let topic_offset = (page_id as usize) * 4096 + 32;
        if topic_offset + topic_data.len() <= mmap.len() {
            mmap[topic_offset..topic_offset + topic_data.len()].copy_from_slice(&topic_data);
        }

        topics_updated += 1;
    }

    Ok(topics_updated)
}

/// Write L3 domain nodes (simplified)
pub fn write_l3_domains(
    _mmap: &mut MmapMut,
    _items: &[EncodedItem],
    _l1_node_ids: &[u64],
) -> Result<u32, MemHopError> {
    // Simplified: skip L3 writing for now
    // TODO: Implement full L3 domain node creation
    Ok(0)
}

/// Create batch hyperedges (Association and Evolution)
pub fn create_batch_hyperedges(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    l1_node_ids: &[u64],
) -> Result<u32, MemHopError> {
    let mut edge_count = 0u32;

    if l1_node_ids.len() > 1 {
        // Create Association hyperedge (connects all nodes in batch)
        let assoc_edge = HyperedgeSlot {
            id_hash: hash_id("batch_association"),
            kind: HyperedgeKind::Association,
            node_ptrs: l1_node_ids.to_vec(),
            meta: vec![],
            weight: 1.0,
            created_at: std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap_or_default()
                .as_millis() as i64,
            updated_at: 0,
            version: 1,
            overflow_page: 0,
        };

        let edge_data = assoc_edge
            .serialize()
            .map_err(|e| MemHopError::Serialization(e.to_string()))?;

        let page_id = allocate_from_free_list(mmap, header)?;
        let edge_offset = (page_id as usize) * 4096 + 32;
        if edge_offset + edge_data.len() <= mmap.len() {
            mmap[edge_offset..edge_offset + edge_data.len()].copy_from_slice(&edge_data);
        }

        edge_count += 1;

        // Create Evolution hyperedges (chain relationships)
        for i in 1..l1_node_ids.len() {
            let evol_edge = HyperedgeSlot {
                id_hash: hash_id(&format!("evolution_{}_{}", i - 1, i)),
                kind: HyperedgeKind::Evolution,
                node_ptrs: vec![l1_node_ids[i - 1], l1_node_ids[i]],
                meta: vec![],
                weight: 1.0,
                created_at: std::time::SystemTime::now()
                    .duration_since(std::time::UNIX_EPOCH)
                    .unwrap_or_default()
                    .as_millis() as i64,
                updated_at: 0,
                version: 1,
                overflow_page: 0,
            };

            let edge_data = evol_edge
                .serialize()
                .map_err(|e| MemHopError::Serialization(e.to_string()))?;

            let page_id = allocate_from_free_list(mmap, header)?;
            let edge_offset = (page_id as usize) * 4096 + 32;
            if edge_offset + edge_data.len() <= mmap.len() {
                mmap[edge_offset..edge_offset + edge_data.len()].copy_from_slice(&edge_data);
            }

            edge_count += 1;
        }
    }

    Ok(edge_count)
}

/// Main batch store function - five-phase pipeline
pub fn batch_store(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    batch: StoreBatch,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    vector_dim: usize,
    encoder: &dyn Encoder,
) -> Result<BatchReport, MemHopError> {
    let mut report = BatchReport {
        l4_docs: 0,
        l1_nodes_created: 0,
        l1_nodes_updated: 0,
        l2_topics_updated: 0,
        l3_nodes: 0,
        edges_created: 0,
        dedup_skipped: 0,
    };

    // Phase 1: Encode
    let encoded_items = encode_items(&batch.items, encoder)?;

    // Phase 2: L4 Archive
    let doc_ids = archive_documents(mmap, &encoded_items, &batch)?;
    report.l4_docs = doc_ids.len() as u32;

    // Phase 3: L1 Write with deduplication
    let (l1_node_ids, created, updated, skipped) = dedup_and_write_l1(
        mmap,
        header,
        &encoded_items,
        btree,
        sparse_index,
        vector_dim,
    )?;
    report.l1_nodes_created = created;
    report.l1_nodes_updated = updated;
    report.dedup_skipped = skipped;

    // Phase 4: L2 Topic Update
    let topics_updated = update_topics(mmap, header, &encoded_items, &l1_node_ids)?;
    report.l2_topics_updated = topics_updated;

    // Phase 5: L3 Domain Write
    let l3_count = write_l3_domains(mmap, &encoded_items, &l1_node_ids)?;
    report.l3_nodes = l3_count;

    // Create hyperedges
    let edge_count = create_batch_hyperedges(mmap, header, &l1_node_ids)?;
    report.edges_created = edge_count;

    Ok(report)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_split_long_text_short() {
        let text = "Short text";
        let chunks = split_long_text(text, 512);
        assert_eq!(chunks.len(), 1);
        assert_eq!(chunks[0], text);
    }

    #[test]
    fn test_split_long_text_long() {
        let text = "A".repeat(600);
        let chunks = split_long_text(&text, 512);
        assert!(chunks.len() >= 2);
    }

    #[test]
    fn test_calculate_ngram_jaccard_identical() {
        let jaccard = calculate_ngram_jaccard("hello", "hello");
        assert!((jaccard - 1.0).abs() < 1e-6);
    }

    #[test]
    fn test_calculate_ngram_jaccard_different() {
        let jaccard = calculate_ngram_jaccard("abc", "xyz");
        assert!(jaccard < 0.1);
    }
}
