// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

#[cfg(feature = "grpc-encoder")]
use crate::encoder::Encoder;
use crate::index::sparse::{self, SparseIndex};
use crate::layers::archive::ArchiveSlot;
use crate::layers::context::ContextSlot;
use crate::layers::context_node::ContextNode;
use crate::layers::hyperedge::{HyperedgeKind, HyperedgeSlot};
use crate::storage::record::*;
use crate::storage::StorageEngine;
use crate::store::write_slot;
use crate::util::hash_id;
use crate::util::{SourceMeta, SourceRef};
use crate::MemHopError;
use half::f16;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;

/// Batch store request containing multiple items to be stored
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct StoreBatch {
    /// List of items to store
    pub items: Vec<StoreItem>,
    /// Optional session ID for tracking
    pub session_id: Option<String>,
    /// Optional turn ID within the session
    pub turn_id: Option<String>,
    /// API 请求来源（记录是谁发起的批量存储）
    #[serde(
        default,
        skip_serializing_if = "crate::query::types::RequestSource::is_empty"
    )]
    pub source: crate::query::types::RequestSource,
}

/// Individual item in a batch store operation
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct StoreItem {
    /// The text content to store
    pub text: String,
    /// Optional topic label for semantic organization
    pub topic_label: Option<String>,
    /// Optional domain identifier (for L3 procedural memory)
    pub domain_id: Option<String>,
    /// LLM-preprocessed keywords for L2 storage (5-10 items). When provided,
    /// these override tokenizer-based keyword extraction during batch store.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub keywords: Option<Vec<String>>,
    /// Importance score (0.0 - 1.0), affects memory retention
    pub importance: Option<f32>,
    /// Valence: emotional pleasantness (-1.0 ~ 1.0)
    pub valence: Option<f64>,
    /// Arousal: emotional activation level (0 ~ 1.0)
    pub arousal: Option<f64>,
    /// Metadata about the source of this memory
    #[serde(default)]
    pub source: SourceMeta,
    /// Whether this is structural knowledge (vs episodic)
    #[serde(default)]
    pub is_structural: bool,
    /// Optional reference to external source location
    pub source_ref: Option<SourceRef>,
}

/// Encoded item ready for storage after encoding
#[derive(Debug, Clone, Serialize, Deserialize)]
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
    /// LLM-preprocessed keywords (propagated from StoreItem)
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub keywords: Option<Vec<String>>,
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
#[derive(Debug, Clone, Serialize, Deserialize)]
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
#[cfg(feature = "grpc-encoder")]
pub fn encode_items(
    items: &[StoreItem],
    encoder: &dyn Encoder,
) -> Result<Vec<EncodedItem>, MemHopError> {
    let mut encoded = Vec::new();

    for item in items {
        let chunks = split_long_text(&item.text, 512);

        for chunk in chunks {
            let output = encoder.encode(&chunk)?;
            encoded.push(EncodedItem {
                text: chunk,
                dense: output.dense,
                sparse: output.sparse,
                topic_label: item.topic_label.clone(),
                domain_id: item.domain_id.clone(),
                keywords: item.keywords.clone(),
                importance: item.importance.unwrap_or(0.5),
                valence: item.valence.unwrap_or(0.0),
                arousal: item.arousal.unwrap_or(0.0),
                is_structural: item.is_structural,
            });
        }
    }

    Ok(encoded)
}

/// Archive documents to L4 — write ArchiveSlots to engine
pub fn archive_documents(
    engine: &mut StorageEngine,
    items: &[EncodedItem],
    batch: &StoreBatch,
) -> Result<Vec<u64>, MemHopError> {
    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as i64;

    let mut doc_ids = Vec::with_capacity(items.len());

    for item in items {
        let id_hash = hash_id(&item.text);

        let archive = ArchiveSlot {
            id_hash,
            content_type: crate::layers::archive::ContentType::Text,
            role: 0, // user
            context_id: item.topic_label.as_ref().map_or(0, |label| hash_id(label)),
            created_at: now,
            content: item.text.clone(),
            metadata: batch.source.to_metadata_json(),
        };

        write_slot(engine, REC_L4_ARCHIVE, id_hash, &archive)?;
        doc_ids.push(id_hash);
    }

    Ok(doc_ids)
}

/// Check for duplicate L1 node using cosine similarity
fn check_duplicate(
    engine: &StorageEngine,
    item: &EncodedItem,
    vector_dim: usize,
) -> Result<Option<u64>, MemHopError> {
    use crate::index::vector::cosine_similarity;

    const COSINE_THRESHOLD: f32 = 0.95;

    for (&id_hash, _) in engine.iter_index() {
        let Some((rt, data)) = engine.read_record(id_hash)? else {
            continue;
        };
        if rt != REC_L1_SCENE_NODE {
            continue;
        }

        if let Ok(existing_node) = bincode::deserialize::<ContextNode>(data)
            .map_err(|e| MemHopError::Serialization(e.to_string()))
        {
            if existing_node.vector_page_ref != 0 {
                let vec_id_hash = existing_node.vector_page_ref;
                if let Some((_, vec_data)) = engine.read_record(vec_id_hash)? {
                    if vec_data.len() >= vector_dim * 2 {
                        let mut existing_vec = Vec::with_capacity(vector_dim);
                        for i in 0..vector_dim {
                            let bytes = [vec_data[i * 2], vec_data[i * 2 + 1]];
                            existing_vec.push(f16::from_le_bytes(bytes));
                        }

                        let cosine_sim = cosine_similarity(&item.dense, &existing_vec);
                        if cosine_sim > COSINE_THRESHOLD {
                            return Ok(Some(id_hash));
                        }
                    }
                }
            }
        }
    }

    Ok(None)
}

/// Write L1 ContextNodes with deduplication
///
/// Returns the list of L1 node id_hashes, a map from id_hash to the page id
/// where the ContextNode is stored, and counters for created/updated/skipped.
/// Write L1 ContextNodes with deduplication
///
/// Returns the list of L1 node id_hashes and counters for created/updated/skipped.
/// `node_pages` map is no longer needed — backfill uses engine read/write directly.
type L1WriteResult = Result<(Vec<u64>, u32, u32, u32), MemHopError>;

#[allow(clippy::type_complexity)]
pub fn dedup_and_write_l1(
    engine: &mut StorageEngine,
    items: &[EncodedItem],
    sparse_index: &mut SparseIndex,
    vector_dim: usize,
) -> L1WriteResult {
    let mut node_ids = Vec::new();
    let mut created = 0u32;
    let updated = 0u32;
    let mut skipped = 0u32;

    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as i64;

    for item in items {
        let id_hash = hash_id(&item.text);

        // Check exact-content dedup via engine
        if engine.contains(id_hash) {
            skipped += 1;
            node_ids.push(id_hash);
            continue;
        }

        if let Some(existing_id) = check_duplicate(engine, item, vector_dim)? {
            skipped += 1;
            node_ids.push(existing_id);
            continue;
        }

        // Write vector to engine as type 0xF0 record
        let vector_record_hash = if !item.dense.is_empty() {
            let vec_id_hash = hash_id(&format!("v:{}", id_hash));
            let vec_bytes: Vec<u8> = item.dense.iter().flat_map(|v| v.to_ne_bytes()).collect();
            engine.write_record(0xF0, vec_id_hash, &vec_bytes)?;
            vec_id_hash
        } else {
            0
        };

        // context_id = 0 initially; linked when L2 context is created
        let node = ContextNode {
            id_hash,
            context_id: 0,
            vector_page_ref: vector_record_hash,
            importance: item.importance,
            valence: 0.0,
            arousal: 0.0,
            created_at: now,
            updated_at: now,
            version: 1,
            edge_ptrs: vec![],
        };

        let node_data =
            bincode::serialize(&node).map_err(|e| MemHopError::Serialization(e.to_string()))?;
        engine.write_record(REC_L1_SCENE_NODE, id_hash, &node_data)?;

        let terms = crate::index::sparse::tokenize(&item.text);
        sparse_index.add_document(id_hash, terms, item.text.len() as u32);

        created += 1;
        node_ids.push(id_hash);
    }

    Ok((node_ids, created, updated, skipped))
}

/// Update L2 contexts based on topic labels
///
/// Creates or updates one L2 ContextSlot per topic label, registers it in the
/// B-tree and sparse index, writes the page header, and finally backfills each
/// associated L1 ContextNode with the L2 context_id.
#[allow(clippy::too_many_arguments)]
pub fn update_topics(
    engine: &mut StorageEngine,
    items: &[EncodedItem],
    l1_node_ids: &[u64],
    archive_ids: &[u64],
    sparse_index: &mut SparseIndex,
    vector_dim: usize,
) -> Result<u32, MemHopError> {
    let mut topics_updated = 0u32;

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
        let node_ids: Vec<u64> = indices.iter().map(|&idx| l1_node_ids[idx]).collect();

        // Build summary text from all items in this topic group
        let summary_text: String = {
            let texts: Vec<&str> = indices
                .iter()
                .filter_map(|&idx| items.get(idx))
                .map(|item| item.text.as_str())
                .collect();
            let joined = texts.join("\n");
            if joined.len() > u16::MAX as usize {
                joined[..u16::MAX as usize].to_string()
            } else {
                joined
            }
        };

        // Collect archive IDs for this topic group
        let topic_archive_ids: Vec<u64> = indices
            .iter()
            .filter_map(|&idx| archive_ids.get(idx).copied())
            .collect();

        let centroid_vector = calculate_centroid_from_nodes(engine, &node_ids, vector_dim)?;

        let centroid_record_hash = if let Some(ref vec) = centroid_vector {
            let vec_id_hash = hash_id(&format!("v:{}", context_id));
            let vec_bytes: Vec<u8> = vec.iter().flat_map(|v| v.to_ne_bytes()).collect();
            engine.write_record(0xF0, vec_id_hash, &vec_bytes)?;
            vec_id_hash
        } else {
            0
        };

        // Collect LLM-preprocessed keywords from items in this topic group.
        // Falls back to the topic label if no keywords are provided.
        let mut keyword_set: std::collections::HashSet<String> = std::collections::HashSet::new();
        for &idx in &indices {
            if let Some(item) = items.get(idx) {
                if let Some(ref kws) = item.keywords {
                    for kw in kws {
                        if !kw.trim().is_empty() {
                            keyword_set.insert(kw.trim().to_string());
                        }
                    }
                }
            }
        }
        let user_keywords: Vec<String> = if keyword_set.is_empty() {
            vec![label.clone()]
        } else {
            let mut kws: Vec<String> = keyword_set.into_iter().collect();
            kws.truncate(10);
            kws
        };

        let context = ContextSlot {
            id: context_id,
            parent_id: None,
            children_ids: vec![],
            scene_id: 0,
            depth: 1,
            user_keywords,
            user_timestamp: now,
            user_l4_refs: topic_archive_ids,
            user_l3_refs: vec![],
            agent_keywords: vec![],
            agent_timestamp: now,
            agent_l4_refs: vec![],
            agent_l3_refs: vec![],
            fused_keywords: vec![],
            fused_summary: Some(summary_text.clone()),
            centroid_page_ref: centroid_record_hash,
            created_at: now,
            updated_at: now,
            version: 4,
        };

        // Write context to engine
        write_slot(engine, REC_L2_TOPIC, context_id, &context)?;

        let mut context_terms = sparse::tokenize(&label);
        context_terms.extend(sparse::tokenize(&summary_text));
        let context_doc_len = context_terms.len() as u32;
        sparse_index.add_document(context_id, context_terms, context_doc_len);

        // Backfill context_id into each L1 node via engine read-modify-write
        for node_id_hash in &node_ids {
            if let Some((_, node_data)) = engine.read_record(*node_id_hash)? {
                if let Ok(mut node) = bincode::deserialize::<ContextNode>(node_data)
                    .map_err(|e| MemHopError::Serialization(e.to_string()))
                {
                    node.context_id = context_id;
                    node.updated_at = now;
                    node.version += 1;
                    let upd_node_data = bincode::serialize(&node)
                        .map_err(|e| MemHopError::Serialization(e.to_string()))?;
                    engine.write_record(REC_L1_SCENE_NODE, *node_id_hash, &upd_node_data)?;
                }
            }
        }

        topics_updated += 1;
    }

    Ok(topics_updated)
}

/// Calculate centroid vector from a list of L1 ContextNode IDs
fn calculate_centroid_from_nodes(
    engine: &StorageEngine,
    node_ids: &[u64],
    vector_dim: usize,
) -> Result<Option<Vec<half::f16>>, MemHopError> {
    use half::f16;

    if node_ids.is_empty() {
        return Ok(None);
    }

    let mut sum = vec![0.0f32; vector_dim];
    let mut count = 0usize;

    for &id_hash in node_ids {
        if let Some((_, node_data)) = engine.read_record(id_hash)? {
            if let Ok(node) = ContextNode::deserialize(node_data)
                .map_err(|e| MemHopError::Serialization(e.to_string()))
            {
                if node.vector_page_ref != 0 {
                    let vec_id_hash = node.vector_page_ref;
                    if let Some((_, vec_data)) = engine.read_record(vec_id_hash)? {
                        if vec_data.len() >= vector_dim * 2 {
                            for i in 0..vector_dim {
                                let bytes = [vec_data[i * 2], vec_data[i * 2 + 1]];
                                sum[i] += f16::from_le_bytes(bytes).to_f32();
                            }
                            count += 1;
                        }
                    }
                }
            }
        }
    }

    if count == 0 {
        return Ok(None);
    }

    let count_f32 = count as f32;
    let centroid: Vec<f16> = sum.iter().map(|&s| f16::from_f32(s / count_f32)).collect();

    Ok(Some(centroid))
}

/// Create batch hyperedges (Association and Evolution) via engine
pub fn create_batch_hyperedges(
    engine: &mut StorageEngine,
    l1_node_ids: &[u64],
) -> Result<u32, MemHopError> {
    let mut edge_count = 0u32;

    if l1_node_ids.len() > 1 {
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

        let edge_data = bincode::serialize(&assoc_edge)
            .map_err(|e| MemHopError::Serialization(e.to_string()))?;
        engine.write_record(REC_L1_HYPEREDGE, assoc_edge.id_hash, &edge_data)?;
        edge_count += 1;

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

            let edge_data = bincode::serialize(&evol_edge)
                .map_err(|e| MemHopError::Serialization(e.to_string()))?;
            engine.write_record(REC_L1_HYPEREDGE, edge_id_hash, &edge_data)?;
            edge_count += 1;
        }
    }

    Ok(edge_count)
}

/// Main batch store function - five-phase pipeline
#[allow(clippy::too_many_arguments)]
#[cfg(feature = "grpc-encoder")]
pub fn batch_store(
    engine: &mut StorageEngine,
    batch: StoreBatch,
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
    let doc_ids = archive_documents(engine, &encoded_items, &batch)?;
    report.l4_docs = doc_ids.len() as u32;

    // Phase 3: L1 Write with deduplication
    let (l1_node_ids, created, updated, skipped) =
        dedup_and_write_l1(engine, &encoded_items, sparse_index, vector_dim)?;
    report.l1_nodes_created = created;
    report.l1_nodes_updated = updated;
    report.dedup_skipped = skipped;

    // Phase 4: L2 Topic Update
    let topics_updated = update_topics(
        engine,
        &encoded_items,
        &l1_node_ids,
        &doc_ids,
        sparse_index,
        vector_dim,
    )?;
    report.l2_topics_updated = topics_updated;

    // Phase 5: L3 Domain Write (delegated to l3::store)
    // write_l3_domains removed — use l3::store::add_node directly

    let edge_count = create_batch_hyperedges(engine, &l1_node_ids)?;
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
