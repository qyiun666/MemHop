//! Update implementation for MemHop
//!
//! Implements the update_memory() interface with multi-level联动 updates.

use crate::file::free_list::allocate_from_free_list;
use crate::file::header::FileHeader;
use crate::index::btree::BTreeIndex;
use crate::index::sparse::SparseIndex;
use crate::query::types::*;
use crate::slot::archive::ArchiveSlot;
use crate::slot::crystal::CrystalSlot;
use crate::slot::engram::EngramSlot;
use crate::slot::hyperedge::HyperedgeSlot;
use crate::slot::knowledge::KnowledgeSlot;
use crate::slot::topic::TopicSlot;
use crate::util::hash_id;
use crate::MemHopError;
use memmap2::MmapMut;
use std::time::{SystemTime, UNIX_EPOCH};

const PAGE_SIZE: usize = 4096;

/// Core update implementation
/// 
/// This function implements the L2-centric update model:
/// 1. Find or create L2 topic by l2_id
/// 2. Create L1 engram for current dialogue
/// 3. Create L4 archive for current dialogue
/// 4. Update L2 summary with compressed content
/// 5. Link L4 archive to L2
/// 6. Store action chain to L5 crystals
/// 7. Create hyperedges for associations
pub fn update_memory(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    request: UpdateRequest,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    _vector_dim: usize,
) -> Result<UpdateResult, MemHopError> {
    let now_ms = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as i64;

    // Get page_count from header for node traversal
    let page_count = header.page_count;

    // Step 1: Find or create L2 topic
    let (l2_id_hash, _l2_page_ref, is_new_l2) = if let Some(ref l2_id) = request.l2_id {
        // Existing L2 topic
        let l2_hash = hash_id(l2_id);
        if let Some(page_ref) = btree.search(l2_hash) {
            (l2_hash, page_ref, false)
        } else {
            return Err(MemHopError::PageNotFound(0)); // L2 not found
        }
    } else {
        // Create new L2 topic with dialogue as title
        let l2_hash = hash_id(&request.dialogue_text);
        let page_ref = allocate_and_write_l2_topic(
            mmap,
            header,
            l2_hash,
            &request.dialogue_text,
            0, // No L1 node yet
            now_ms,
            btree,
            sparse_index,
            page_count,
        )?;
        (l2_hash, page_ref, true)
    };

    // Step 2: Create L1 Engram for current dialogue
    let l1_id_hash = hash_id(&format!("{}-{}", l2_id_hash, now_ms));
    let _l1_page_ref = allocate_and_write_l1_engram(
        mmap,
        header,
        l1_id_hash,
        &request.dialogue_text,
        now_ms,
        btree,
    )?;

    // Step 3: Create L4 Archive for current dialogue
    let l4_id_hash = hash_id(&format!("{}-{}", l2_id_hash, now_ms));
    let _l4_page_ref = allocate_and_write_l4_archive(
        mmap,
        header,
        l4_id_hash,
        &request.dialogue_text,
        l2_id_hash,
        now_ms,
        btree,
    )?;

    // Step 4: Update L2 topic with new L1 and L4 references
    update_l2_with_new_data(
        mmap,
        l2_id_hash,
        l1_id_hash,
        l4_id_hash,
        request.summary.as_deref(),
        btree,
        sparse_index,
        page_count,
    )?;

    // Step 5: Create hyperedges for associations
    create_association_edges_l2_centric(
        mmap,
        header,
        l1_id_hash,
        l2_id_hash,
        l4_id_hash,
        btree,
    )?;

    // Step 6: Create L5 Crystals from action_chain
    let mut crystal_ids = Vec::new();
    for action in &request.action_chain {
        let crystal_id_hash = hash_id(&format!(
            "{}-{:?}-{}",
            l2_id_hash, action.action_type, now_ms
        ));
        let _crystal_page_ref = allocate_and_write_l5_crystal(
            mmap,
            header,
            crystal_id_hash,
            action,
            now_ms,
            btree,
        )?;
        crystal_ids.push(format!("{:016x}", crystal_id_hash));
    }

    Ok(UpdateResult {
        memory_id: format!("{:016x}", l2_id_hash),
        l1_engram_id: format!("{:016x}", l1_id_hash),
        l2_topic_id: format!("{:016x}", l2_id_hash),
        l3_knowledge_id: String::new(), // No L3 in this model
        l4_archive_id: format!("{:016x}", l4_id_hash),
        l5_crystal_ids: crystal_ids,
        status: if is_new_l2 { UpdateStatus::Created } else { UpdateStatus::Updated },
    })
}

/// Allocate page and write L1 Engram
fn allocate_and_write_l1_engram(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    id_hash: u64,
    text: &str,
    now_ms: i64,
    btree: &mut BTreeIndex,
) -> Result<u64, MemHopError> {
    // Check if already exists
    if let Some(page_ref) = btree.search(id_hash) {
        return Ok(page_ref);
    }

    // Allocate new page
    let page_id = allocate_from_free_list(mmap, header)?;
    let offset = (page_id as usize) * PAGE_SIZE + 32;

    // Create EngramSlot
    let engram = EngramSlot {
        id_hash,
        text: text.to_string(),
        summary: None,
        keywords: extract_keywords(text),
        created_at: now_ms,
        updated_at: now_ms,
        version: 1,
        edge_count: 0,
        doc_len: text.len() as u16,
        vector_page_ref: 0, // TODO: Allocate vector page
        is_structural: false,
        source_type: 0, // User input
        memory_state: 1, // Active
        emotion_type: 0,
        valence: 0.0,
        arousal: 0.0,
        importance: 0.5,
        edge_ptrs: [0; 8],
    };

    // Serialize and write
    let data = engram.serialize().map_err(|e| MemHopError::Serialization(e.to_string()))?;
    if offset + data.len() <= mmap.len() {
        mmap[offset..offset + data.len()].copy_from_slice(&data);
    }

    // Insert into B-tree
    let page_ref = (page_id as u64) << 16; // slot index 0
    btree.insert(id_hash, page_ref);

    Ok(page_ref)
}

/// Extract search terms from L2 topic including associated nodes
/// Primary key: title
/// Secondary keys: all L1 node texts + all L3 knowledge texts
fn extract_l2_search_terms_with_nodes(
    topic: &TopicSlot,
    data: &[u8],
    btree: &BTreeIndex,
    _page_count: u32,
) -> Vec<String> {
    let mut terms = Vec::new();
    
    // Primary key: title
    terms.extend(topic.title.split_whitespace().map(|s| s.to_lowercase()));
    
    // Secondary keys: summary
    if let Some(ref summary) = topic.summary {
        terms.extend(summary.split_whitespace().map(|s| s.to_lowercase()));
    }
    
    // Secondary keys: L1 node contents (main nodes)
    for &node_id_hash in &topic.node_ids {
        if let Some(page_ref) = btree.search(node_id_hash) {
            let page_id = (page_ref >> 16) as u32;
            let slot_offset = (page_id as usize) * PAGE_SIZE + 32;
            
            if slot_offset < data.len() {
                if let Ok(engram) = EngramSlot::deserialize(&data[slot_offset..]) {
                    // Add L1 text
                    terms.extend(engram.text.split_whitespace().map(|s| s.to_lowercase()));
                    // Add L1 summary if exists
                    if let Some(ref summary) = engram.summary {
                        terms.extend(summary.split_whitespace().map(|s| s.to_lowercase()));
                    }
                }
            }
        }
    }
    
    // Secondary keys: L3 knowledge contents (secondary nodes)
    for &l3_id_hash in &topic.l3_refs {
        if let Some(page_ref) = btree.search(l3_id_hash) {
            let page_id = (page_ref >> 16) as u32;
            let slot_offset = (page_id as usize) * PAGE_SIZE + 32;
            
            if slot_offset < data.len() {
                if let Ok(knowledge) = KnowledgeSlot::deserialize(&data[slot_offset..]) {
                    // Add L3 title
                    terms.extend(knowledge.title.split_whitespace().map(|s| s.to_lowercase()));
                    // Add L3 text
                    terms.extend(knowledge.text.split_whitespace().map(|s| s.to_lowercase()));
                    // Add L3 summary if exists
                    if let Some(ref summary) = knowledge.summary {
                        terms.extend(summary.split_whitespace().map(|s| s.to_lowercase()));
                    }
                }
            }
        }
    }
    
    terms
}

/// Extract search terms from L1 engram (primary key + secondary keys)
#[allow(dead_code)]
fn extract_l1_search_terms(engram: &EngramSlot) -> Vec<String> {
    let mut terms = Vec::new();
    
    // Primary key: text
    terms.extend(engram.text.split_whitespace().map(|s| s.to_lowercase()));
    
    // Secondary keys: summary
    if let Some(ref summary) = engram.summary {
        terms.extend(summary.split_whitespace().map(|s| s.to_lowercase()));
    }
    
    // Secondary keys: keywords
    for keyword in &engram.keywords {
        terms.extend(keyword.split_whitespace().map(|s| s.to_lowercase()));
    }
    
    terms
}

/// Extract search terms from L3 knowledge (primary key + secondary keys)
#[allow(dead_code)]
fn extract_l3_search_terms(knowledge: &KnowledgeSlot) -> Vec<String> {
    let mut terms = Vec::new();
    
    // Primary key: title
    terms.extend(knowledge.title.split_whitespace().map(|s| s.to_lowercase()));
    
    // Secondary keys: text content
    terms.extend(knowledge.text.split_whitespace().map(|s| s.to_lowercase()));
    
    // Secondary keys: summary
    if let Some(ref summary) = knowledge.summary {
        terms.extend(summary.split_whitespace().map(|s| s.to_lowercase()));
    }
    
    // Secondary keys: keywords
    for keyword in &knowledge.keywords {
        terms.extend(keyword.split_whitespace().map(|s| s.to_lowercase()));
    }
    
    terms
}

/// Allocate page and write L2 Topic
#[allow(clippy::too_many_arguments)]
fn allocate_and_write_l2_topic(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    id_hash: u64,
    title: &str,
    l1_node_id: u64,
    now_ms: i64,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    page_count: u32,
) -> Result<u64, MemHopError> {
    // Check if already exists
    let mut is_new = false;
    let page_ref = if let Some(existing_ref) = btree.search(id_hash) {
        existing_ref
    } else {
        is_new = true;
        // Allocate new page
        let page_id = allocate_from_free_list(mmap, header)?;
        let offset = (page_id as usize) * PAGE_SIZE + 32;

        // Create TopicSlot
        let topic = TopicSlot {
            id_hash,
            title: title.to_string(),
            summary: None,
            node_ids: vec![l1_node_id],
            l3_refs: vec![],
            l4_refs: vec![],
            parent_id: None,
            created_at: now_ms,
            updated_at: now_ms,
            version: 1,
            importance: 0.5,
            activation_score: 1.0,
            is_active: true,
            activation_state: crate::slot::topic::ActivationState::Active,
            centroid_vector: None,
            domain_weights: vec![],
            dialogue_range: (now_ms, now_ms),
            reserved: [0; 16],
        };

        // Serialize and write
        let data = topic.serialize().map_err(|e| MemHopError::Serialization(e.to_string()))?;
        if offset + data.len() <= mmap.len() {
            mmap[offset..offset + data.len()].copy_from_slice(&data);
        }

        // Insert into B-tree
        let page_ref = (page_id as u64) << 16;
        btree.insert(id_hash, page_ref);

        // Add to sparse index using title + L1 node contents
        let terms = extract_l2_search_terms_with_nodes(&topic, mmap, btree, page_count);
        
        // Calculate doc_len from title + summary + L1 content
        let l1_page_ref = btree.search(l1_node_id);
        let l1_doc_len = if let Some(page_ref) = l1_page_ref {
            let l1_page_id = (page_ref >> 16) as u32;
            let l1_offset = (l1_page_id as usize) * PAGE_SIZE + 32;
            if l1_offset < mmap.len() {
                if let Ok(engram) = EngramSlot::deserialize(&mmap[l1_offset..]) {
                    engram.text.len() + engram.summary.as_ref().map_or(0, |s| s.len())
                } else {
                    0
                }
            } else {
                0
            }
        } else {
            0
        };
        
        let doc_len = topic.title.len() 
            + topic.summary.as_ref().map_or(0, |s| s.len())
            + l1_doc_len;
        sparse_index.add_document(id_hash, terms, doc_len as u32);

        page_ref
    };

    // If exists, add l1_node_id if not already present
    if !is_new {
        let page_id = (page_ref >> 16) as u32;
        let offset = (page_id as usize) * PAGE_SIZE + 32;

        if offset < mmap.len() {
            if let Ok(mut topic) = TopicSlot::deserialize(&mmap[offset..]) {
                if !topic.node_ids.contains(&l1_node_id) {
                    topic.node_ids.push(l1_node_id);
                    topic.updated_at = now_ms;
                    topic.version += 1;

                    let data = topic.serialize().map_err(|e| MemHopError::Serialization(e.to_string()))?;
                    if offset + data.len() <= mmap.len() {
                        mmap[offset..offset + data.len()].copy_from_slice(&data);
                    }
                }
            }
        }
    }

    Ok(page_ref)
}

/// Allocate page and write L3 Knowledge
#[allow(dead_code, clippy::too_many_arguments)]
fn allocate_and_write_l3_knowledge(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    id_hash: u64,
    title: &str,
    text: &str,
    now_ms: i64,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
) -> Result<u64, MemHopError> {
    // Check if already exists
    if let Some(page_ref) = btree.search(id_hash) {
        return Ok(page_ref);
    }

    // Allocate new page
    let page_id = allocate_from_free_list(mmap, header)?;
    let offset = (page_id as usize) * PAGE_SIZE + 32;

    // Create KnowledgeSlot
    use crate::slot::knowledge::KnowledgeType;
    let knowledge = KnowledgeSlot {
        id_hash,
        title: title.to_string(),
        domain: infer_domain_from_content(text, title),
        knowledge_type: KnowledgeType::Factual,
        text: text.to_string(),
        summary: None,
        keywords: extract_keywords(text),
        edge_count: 0,
        edge_ptrs: [0; 8],
        archive_refs: vec![],
        source_ref: None,
        created_at: now_ms,
        updated_at: now_ms,
        version: 1,
        importance: 0.5,
        confidence: 0.8,
    };

    // Serialize and write
    let data = knowledge.serialize().map_err(|e| MemHopError::Serialization(e.to_string()))?;
    if offset + data.len() <= mmap.len() {
        mmap[offset..offset + data.len()].copy_from_slice(&data);
    }

    // Insert into B-tree
    let page_ref = (page_id as u64) << 16;
    btree.insert(id_hash, page_ref);

    // Add to sparse index using primary + secondary keys
    let terms = extract_l3_search_terms(&knowledge);
    let doc_len = knowledge.title.len() + knowledge.text.len() 
        + knowledge.summary.as_ref().map_or(0, |s| s.len())
        + knowledge.keywords.iter().map(|k| k.len()).sum::<usize>();
    sparse_index.add_document(id_hash, terms, doc_len as u32);

    Ok(page_ref)
}

/// Allocate page and write L4 Archive
fn allocate_and_write_l4_archive(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    id_hash: u64,
    content: &str,
    topic_id: u64,
    now_ms: i64,
    btree: &mut BTreeIndex,
) -> Result<u64, MemHopError> {
    // Allocate new page
    let page_id = allocate_from_free_list(mmap, header)?;
    let offset = (page_id as usize) * PAGE_SIZE + 32;

    // Create ArchiveSlot
    use crate::slot::archive::ContentType;
    let archive = ArchiveSlot {
        id_hash,
        content_type: ContentType::Text,
        role: 0, // user
        session_id: 0, // TODO: Get from context
        topic_id,
        created_at: now_ms,
        version: 1,
        content: content.to_string(),
        metadata: None,
    };

    // Serialize and write
    let data = archive.serialize().map_err(|e| MemHopError::Serialization(e.to_string()))?;
    if offset + data.len() <= mmap.len() {
        mmap[offset..offset + data.len()].copy_from_slice(&data);
    }

    // Insert into B-tree
    let page_ref = (page_id as u64) << 16;
    btree.insert(id_hash, page_ref);

    Ok(page_ref)
}

/// Allocate page and write L5 Crystal
fn allocate_and_write_l5_crystal(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    id_hash: u64,
    action: &ActionItem,
    now_ms: i64,
    btree: &mut BTreeIndex,
) -> Result<u64, MemHopError> {
    // Allocate new page
    let page_id = allocate_from_free_list(mmap, header)?;
    let offset = (page_id as usize) * PAGE_SIZE + 32;

    // Create CrystalSlot
    use crate::slot::crystal::CrystalStatus;
    let crystal = CrystalSlot {
        id_hash,
        title: format!("{:?} Action", action.action_type),
        condition: format!("action:{:?}", action.action_type),
        action: action.description.clone(),
        raw_steps: action.description.clone(),
        status: CrystalStatus::Crystallized,
        confidence: 0.8,
        trigger_count: 0,
        last_triggered: 0,
        created_at: now_ms,
        version: 1,
    };

    // Serialize and write
    let data = crystal.serialize().map_err(|e| MemHopError::Serialization(e.to_string()))?;
    if offset + data.len() <= mmap.len() {
        mmap[offset..offset + data.len()].copy_from_slice(&data);
    }

    // Insert into B-tree
    let page_ref = (page_id as u64) << 16;
    btree.insert(id_hash, page_ref);

    Ok(page_ref)
}



/// Update L2 topic with new L1 and L4 references, and optional summary
#[allow(clippy::too_many_arguments)]
fn update_l2_with_new_data(
    mmap: &mut MmapMut,
    l2_id: u64,
    l1_id: u64,
    l4_id: u64,
    summary: Option<&str>,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    page_count: u32,
) -> Result<(), MemHopError> {
    if let Some(page_ref) = btree.search(l2_id) {
        let page_id = (page_ref >> 16) as u32;
        let offset = (page_id as usize) * PAGE_SIZE + 32;

        if offset < mmap.len() {
            if let Ok(mut topic) = TopicSlot::deserialize(&mmap[offset..]) {
                let mut updated = false;

                // Add L1 node reference if not present
                if l1_id != 0 && !topic.node_ids.contains(&l1_id) {
                    topic.node_ids.push(l1_id);
                    updated = true;
                }

                // Add L4 reference if not present
                if !topic.l4_refs.contains(&l4_id) {
                    topic.l4_refs.push(l4_id);
                    updated = true;
                }

                // Update summary if provided
                if let Some(new_summary) = summary {
                    topic.summary = Some(new_summary.to_string());
                    updated = true;
                }

                if updated {
                    let now_ms = SystemTime::now()
                        .duration_since(UNIX_EPOCH)
                        .unwrap_or_default()
                        .as_millis() as i64;
                    topic.updated_at = now_ms;
                    topic.version += 1;

                    // Update sparse index: remove old terms and add new terms
                    sparse_index.remove_document(topic.id_hash);
                    let terms = extract_l2_search_terms_with_nodes(&topic, mmap, btree, page_count);
                    
                    // Calculate doc_len
                    let mut doc_len = topic.title.len() 
                        + topic.summary.as_ref().map_or(0, |s| s.len());
                    
                    // Add L1 content length
                    for &node_id in &topic.node_ids {
                        if let Some(page_ref) = btree.search(node_id) {
                            let node_page_id = (page_ref >> 16) as u32;
                            let node_offset = (node_page_id as usize) * PAGE_SIZE + 32;
                            if node_offset < mmap.len() {
                                if let Ok(engram) = EngramSlot::deserialize(&mmap[node_offset..]) {
                                    doc_len += engram.text.len() 
                                        + engram.summary.as_ref().map_or(0, |s| s.len());
                                }
                            }
                        }
                    }
                    
                    sparse_index.add_document(topic.id_hash, terms, doc_len as u32);

                    let data = topic.serialize().map_err(|e| MemHopError::Serialization(e.to_string()))?;
                    if offset + data.len() <= mmap.len() {
                        mmap[offset..offset + data.len()].copy_from_slice(&data);
                    }
                }
            }
        }
    }

    Ok(())
}

/// Create hyperedges for L2-centric associations
fn create_association_edges_l2_centric(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    l1_id: u64,
    l2_id: u64,
    l4_id: u64,
    btree: &mut BTreeIndex,
) -> Result<(), MemHopError> {
    let now_ms = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as i64;

    use crate::slot::hyperedge::HyperedgeKind;

    // Create L1->L2 edge
    let edge_hash = hash_id(&format!("edge-{:016x}-{:016x}", l1_id, l2_id));
    if btree.search(edge_hash).is_none() {
        let page_id = allocate_from_free_list(mmap, header)?;
        let offset = (page_id as usize) * PAGE_SIZE + 32;

        let edge = HyperedgeSlot {
            id_hash: edge_hash,
            kind: HyperedgeKind::Association,
            node_ptrs: vec![l1_id, l2_id],
            meta: vec![],
            weight: 1.0,
            created_at: now_ms,
            updated_at: now_ms,
            version: 1,
            overflow_page: 0,
        };

        let data = edge.serialize().map_err(|e| MemHopError::Serialization(e.to_string()))?;
        if offset + data.len() <= mmap.len() {
            mmap[offset..offset + data.len()].copy_from_slice(&data);
        }

        btree.insert(edge_hash, (page_id as u64) << 16);
    }

    // Create L2->L4 edge
    let edge_hash = hash_id(&format!("edge-{:016x}-{:016x}", l2_id, l4_id));
    if btree.search(edge_hash).is_none() {
        let page_id = allocate_from_free_list(mmap, header)?;
        let offset = (page_id as usize) * PAGE_SIZE + 32;

        let edge = HyperedgeSlot {
            id_hash: edge_hash,
            kind: HyperedgeKind::Hierarchical,
            node_ptrs: vec![l2_id, l4_id],
            meta: vec![],
            weight: 1.0,
            created_at: now_ms,
            updated_at: now_ms,
            version: 1,
            overflow_page: 0,
        };

        let data = edge.serialize().map_err(|e| MemHopError::Serialization(e.to_string()))?;
        if offset + data.len() <= mmap.len() {
            mmap[offset..offset + data.len()].copy_from_slice(&data);
        }

        btree.insert(edge_hash, (page_id as u64) << 16);
    }

    Ok(())
}

/// Simple keyword extraction (split by whitespace and filter common words)
/// Uses the shared implementation from organize module
fn extract_keywords(text: &str) -> Vec<String> {
    // Use the more comprehensive implementation from organize module
    crate::organize::extract_keywords(text, 10)
}

/// Infer domain from content based on keyword matching
///
/// This function uses simple keyword-based domain classification.
/// For better accuracy, consider using an LLM or ML classifier in production.
#[allow(dead_code)]
fn infer_domain_from_content(text: &str, title: &str) -> String {
    let combined = format!("{} {}", title.to_lowercase(), text.to_lowercase());
    
    // Define domain keywords mapping
    let domain_keywords: &[(&str, &[&str])] = &[
        ("programming", &["code", "program", "function", "class", "variable", "algorithm", "debug", "compile", "rust", "python", "java", "javascript"]),
        ("database", &["sql", "query", "table", "index", "transaction", "schema", "postgresql", "mysql", "mongodb"]),
        ("networking", &["http", "api", "server", "client", "socket", "tcp", "udp", "dns", "protocol"]),
        ("machine_learning", &["model", "training", "neural", "dataset", "prediction", "classification", "regression", "tensorflow", "pytorch"]),
        ("devops", &["docker", "kubernetes", "deployment", "ci/cd", "pipeline", "container", "infrastructure"]),
        ("web_development", &["html", "css", "react", "vue", "angular", "frontend", "backend", "responsive"]),
        ("security", &["encryption", "authentication", "authorization", "vulnerability", "firewall", "ssl", "tls"]),
        ("data_science", &["analysis", "visualization", "statistics", "pandas", "numpy", "jupyter"]),
    ];
    
    // Count keyword matches for each domain
    let mut domain_scores: Vec<(String, usize)> = domain_keywords.iter()
        .map(|(domain, keywords)| {
            let score = keywords.iter()
                .filter(|kw| combined.contains(*kw))
                .count();
            (domain.to_string(), score)
        })
        .collect();
    
    // Sort by score descending
    domain_scores.sort_by_key(|(_, score)| std::cmp::Reverse(*score));
    
    // Return the highest scoring domain, or "General" if no matches
    if let Some((domain, score)) = domain_scores.first() {
        if *score > 0 {
            return domain.clone();
        }
    }
    
    "General".to_string()
}
