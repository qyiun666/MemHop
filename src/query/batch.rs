// Batch Store API implementation
use crate::encoder::ipc::Encoder;
use crate::file::free_list::allocate_from_free_list;
use crate::file::header::FileHeader;
use crate::index::btree::BTreeIndex;
use crate::index::sparse::SparseIndex;
use crate::slot::context::ActivationState;
use crate::slot::context::ContextSlot;
use crate::slot::context_node::ContextNode;
use crate::slot::hyperedge::{HyperedgeKind, HyperedgeSlot};
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

/// Archive documents to L4 — compute doc IDs for batch reporting
pub fn archive_documents(
    _mmap: &mut MmapMut,
    items: &[EncodedItem],
    _batch: &StoreBatch,
) -> Result<Vec<u64>, MemHopError> {
    // Return hash-based doc IDs for batch report tracking.
    // Full L4 archival with page allocation is handled by update_memory().
    let doc_ids: Vec<u64> = items.iter().map(|item| hash_id(&item.text)).collect();
    Ok(doc_ids)
}

/// Check for duplicate L1 node using cosine similarity
fn check_duplicate(
    mmap: &MmapMut,
    item: &EncodedItem,
    btree: &BTreeIndex,
    vector_dim: usize,
) -> Result<Option<u64>, MemHopError> {
    use crate::index::vector::cosine_similarity;

    // Thresholds for deduplication
    const COSINE_THRESHOLD: f32 = 0.95;

    // Iterate through all L1 ContextNode entries in btree
    for (&existing_hash, &page_ref) in btree.iter() {
        let page_id = (page_ref >> 16) as u32;
        let node_offset = (page_id as usize) * PAGE_SIZE + 32;

        if node_offset >= mmap.len() {
            continue;
        }

        // Deserialize existing L1 ContextNode
        if let Ok(existing_node) = ContextNode::deserialize(&mmap[node_offset..]) {
            if existing_node.vector_page_ref != 0 {
                let vec_page_id = (existing_node.vector_page_ref >> 16) as u32;
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
        }
    }

    Ok(None)
}

/// Write L1 ContextNodes with deduplication
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
            // Update existing L1 node (simplified)
            updated += 1;
            node_ids.push(existing_id);
            continue;
        }

        // Calculate vector_page_ref before creating node
        let vector_page_ref = if !item.dense.is_empty() {
            // Allocate a new page for vector storage
            let vec_page_id = allocate_from_free_list(mmap, header)?;
            let vec_slot_index = 0u16; // First slot in new page

            // Write vector to the allocated page
            crate::index::vector::write_vector(
                mmap,
                vec_page_id,
                vec_slot_index,
                id_hash,
                &item.dense,
                vector_dim,
            )?;

            // Encode page_ref: high 32 bits = page_id, low 16 bits = slot_index
            ((vec_page_id as u64) << 16) | (vec_slot_index as u64)
        } else {
            0
        };

        // Create L1 ContextNode (points to L2 context via context_id)
        // context_id is set to 0 initially; will be linked when L2 context is created
        let node = ContextNode {
            id_hash,
            context_id: 0,
            vector_page_ref,
            importance: item.importance,
            created_at: now,
            updated_at: now,
            version: 1,
            edge_ptrs: vec![],
        };

        let node_data = node
            .serialize()
            .map_err(|e| MemHopError::Serialization(e.to_string()))?;

        // Allocate page from free list for L1 node
        let page_id = allocate_from_free_list(mmap, header)?;

        // Write node to page (skip 32-byte header)
        let node_offset = (page_id as usize) * PAGE_SIZE + 32;
        if node_offset + node_data.len() <= mmap.len() {
            mmap[node_offset..node_offset + node_data.len()].copy_from_slice(&node_data);
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

/// Update L2 contexts based on topic labels
pub fn update_topics(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    items: &[EncodedItem],
    l1_node_ids: &[u64],
    btree: &BTreeIndex,
    vector_dim: usize,
) -> Result<u32, MemHopError> {
    let mut topics_updated = 0u32;

    // Group items by topic_label
    let mut topic_groups: HashMap<String, Vec<usize>> = HashMap::new();

    for (idx, item) in items.iter().enumerate() {
        if let Some(ref label) = item.topic_label {
            topic_groups.entry(label.clone()).or_default().push(idx);
        }
    }

    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as i64;

    for (label, indices) in topic_groups {
        let context_id = hash_id(&label);
        let _node_ids: Vec<u64> = indices.iter().map(|&idx| l1_node_ids[idx]).collect();

        // Calculate centroid vector from associated L1 nodes
        let centroid_vector = calculate_centroid_from_nodes(mmap, &_node_ids, btree, vector_dim)?;

        // Write centroid vector to page if available
        let centroid_page_ref = if let Some(ref vec) = centroid_vector {
            let vec_page_id = allocate_from_free_list(mmap, header)?;
            let vec_slot_index = 0u16;
            crate::index::vector::write_vector(
                mmap,
                vec_page_id,
                vec_slot_index,
                context_id,
                vec,
                vector_dim,
            )?;
            ((vec_page_id as u64) << 16) | (vec_slot_index as u64)
        } else {
            0
        };

        // Create or find L2 ContextSlot
        let context = ContextSlot {
            id_hash: context_id,
            parent_id: None,
            depth: 1,
            title: label.clone(),
            summary: None,
            archive_refs: vec![],
            l3_refs: vec![],
            turn_count: 0,
            created_at: now,
            updated_at: now,
            version: 1,
            importance: 0.5,
            activation_score: 0.5,
            is_active: false,
            activation_state: ActivationState::Dormant,
            centroid_page_ref,
            dialogue_range: (now, now),
        };

        let context_data = context
            .serialize()
            .map_err(|e| MemHopError::Serialization(e.to_string()))?;

        // Allocate page and write context
        let page_id = allocate_from_free_list(mmap, header)?;
        let context_offset = (page_id as usize) * PAGE_SIZE + 32;
        if context_offset + context_data.len() <= mmap.len() {
            mmap[context_offset..context_offset + context_data.len()]
                .copy_from_slice(&context_data);
        }

        topics_updated += 1;
    }

    Ok(topics_updated)
}

/// Calculate centroid vector from a list of L1 ContextNode IDs
fn calculate_centroid_from_nodes(
    mmap: &MmapMut,
    node_ids: &[u64],
    btree: &BTreeIndex,
    vector_dim: usize,
) -> Result<Option<Vec<half::f16>>, MemHopError> {
    use half::f16;

    if node_ids.is_empty() {
        return Ok(None);
    }

    let data = &mmap[..];
    let mut sum = vec![0.0f32; vector_dim];
    let mut count = 0usize;

    for &id_hash in node_ids {
        if let Some(page_ref) = btree.search(id_hash) {
            let page_id = (page_ref >> 16) as u32;
            let offset = (page_id as usize) * PAGE_SIZE + 32;

            if let Ok(node) = ContextNode::deserialize(&data[offset..]) {
                if node.vector_page_ref != 0 {
                    let vec_page_id = (node.vector_page_ref >> 16) as u32;
                    let vec_slot_index = (node.vector_page_ref & 0xFFFF) as u16;

                    if let Ok(vector) = crate::index::vector::read_vector(
                        data,
                        vec_page_id,
                        vec_slot_index,
                        vector_dim,
                    ) {
                        for (i, val) in vector.iter().enumerate() {
                            sum[i] += val.to_f32();
                        }
                        count += 1;
                    }
                }
            }
        }
    }

    if count == 0 {
        return Ok(None);
    }

    // Calculate average
    let count_f32 = count as f32;
    let centroid: Vec<f16> = sum.iter().map(|&s| f16::from_f32(s / count_f32)).collect();

    Ok(Some(centroid))
}

/// Create batch hyperedges (Association and Evolution) with btree registration
pub fn create_batch_hyperedges(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    l1_node_ids: &[u64],
    btree: &mut BTreeIndex,
) -> Result<u32, MemHopError> {
    let mut edge_count = 0u32;

    if l1_node_ids.len() > 1 {
        // Create CoOccurrence hyperedge (connects all nodes in batch)
        let assoc_edge = HyperedgeSlot {
            id_hash: hash_id("batch_association"),
            kind: HyperedgeKind::CoOccurrence,
            node_ptrs: l1_node_ids.to_vec(),
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
        let edge_offset = (page_id as usize) * PAGE_SIZE + 32;
        if edge_offset + edge_data.len() <= mmap.len() {
            mmap[edge_offset..edge_offset + edge_data.len()].copy_from_slice(&edge_data);
        }

        // Write page header for hyperedge
        let page_hdr =
            crate::file::page::PageHeader::new(0, crate::util::PageType::Hyperedge, 1, 0xFFFFFFFF);
        let hdr_bytes = page_hdr.to_bytes();
        let page_offset = (page_id as usize) * PAGE_SIZE;
        mmap[page_offset..page_offset + 32].copy_from_slice(&hdr_bytes);

        btree.insert(assoc_edge.id_hash, (page_id as u64) << 16);
        edge_count += 1;

        // Create Temporal hyperedges (chain relationships)
        for i in 1..l1_node_ids.len() {
            let edge_id_hash = hash_id(&format!("evolution_{}_{}", i - 1, i));
            let evol_edge = HyperedgeSlot {
                id_hash: edge_id_hash,
                kind: HyperedgeKind::Temporal,
                node_ptrs: vec![l1_node_ids[i - 1], l1_node_ids[i]],
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
            let edge_offset = (page_id as usize) * PAGE_SIZE + 32;
            if edge_offset + edge_data.len() <= mmap.len() {
                mmap[edge_offset..edge_offset + edge_data.len()].copy_from_slice(&edge_data);
            }

            // Write page header for temporal hyperedge
            let page_hdr = crate::file::page::PageHeader::new(
                0,
                crate::util::PageType::Hyperedge,
                1,
                0xFFFFFFFF,
            );
            let hdr_bytes = page_hdr.to_bytes();
            let page_offset = (page_id as usize) * PAGE_SIZE;
            mmap[page_offset..page_offset + 32].copy_from_slice(&hdr_bytes);

            btree.insert(edge_id_hash, (page_id as u64) << 16);
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
    let topics_updated = update_topics(
        mmap,
        header,
        &encoded_items,
        &l1_node_ids,
        btree,
        vector_dim,
    )?;
    report.l2_topics_updated = topics_updated;

    // Phase 5: L3 Domain Write (delegated to l3::store)
    // write_l3_domains removed — use l3::store::add_node directly

    // Create hyperedges
    let edge_count = create_batch_hyperedges(mmap, header, &l1_node_ids, btree)?;
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
}
