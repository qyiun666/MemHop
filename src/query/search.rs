//! Search implementation for MemHop
//!
//! Implements the search_memory() interface with L2-centric retrieval model.

use crate::file::header::FileHeader;
use crate::index::btree::BTreeIndex;
use crate::index::sparse::SparseIndex;
use crate::query::types::*;
use crate::slot::archive::ArchiveSlot;
use crate::slot::engram::EngramSlot;
use crate::slot::knowledge::KnowledgeSlot;
use crate::slot::profile::ProfileSlot;
use crate::slot::topic::TopicSlot;
use crate::util::hash_id;
use crate::MemHopError;
use memmap2::MmapMut;

const PAGE_SIZE: usize = 4096;

/// Core search implementation
pub fn search_memory(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    query: SearchQuery,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    vector_dim: usize,
) -> Result<SearchResult, MemHopError> {
    let page_count = header.page_count;

    // Fast path: if auto_create=1, skip search and directly create new L2
    let filtered_l2 = if query.auto_create == 1 {
        // Directly create a new L2 topic from the dialogue, no retrieval needed
        let new_topic = create_new_l2_topic(
            mmap,
            header,
            btree,
            sparse_index,
            &query.dialogue,
            vector_dim,
        )?;
        vec![new_topic]
    } else {
        // Normal search path

        // Step 1: LLM enhancement (optional)
        let search_text = if let Some(llm_config) = &query.llm_enhance {
            // Try to enhance query using LLM
            match enhance_query_with_llm(llm_config, &query.dialogue) {
                Ok(enhanced) => {
                    eprintln!("[LLM Enhancement] Original: {}, Enhanced: {}", 
                              &query.dialogue[..50.min(query.dialogue.len())], 
                              &enhanced[..50.min(enhanced.len())]);
                    enhanced
                }
                Err(e) => {
                    // Fallback to original query on LLM failure
                    eprintln!("[LLM Enhancement] Failed: {}, using original query", e);
                    query.dialogue.clone()
                }
            }
        } else {
            query.dialogue.clone()
        };

        // Step 2: L2 topic retrieval using BM25 + vector similarity
        let data = &mmap[..];
        let l2_candidates = retrieve_l2_topics(
            data,
            &search_text,
            sparse_index,
            btree,
            page_count,
            query.l2_limit,
        )?;

        // Step 3: Filter L2 results by l2_id and l3_id if specified
        filter_l2_topics(&l2_candidates, &query.l2_id, &query.l3_id)
    };

    // Step 4-8: Get associated data (re-borrow mmap as immutable)
    let data = &mmap[..];
    let l1_engrams = get_associated_l1_engrams(data, &filtered_l2, btree, page_count)?;
    let l3_knowledge = get_associated_l3_knowledge(data, &filtered_l2, btree, page_count)?;
    let l4_archives = get_associated_l4_archives(data, &filtered_l2, btree, page_count)?;
    let l1_associated_l2 = get_l1_associated_l2(data, &l1_engrams, btree, page_count)?;
    let l0_profile = get_l0_profile(mmap, btree, page_count)?;

    // Step 9: Update activation scores
    update_activation_scores(mmap, &filtered_l2, &l1_engrams, btree)?;

    // Step 10: Collect memory IDs
    let memory_ids = collect_memory_ids(&filtered_l2, &l3_knowledge);

    // Convert internal structures to public API types
    let result = SearchResult {
        memory_ids,
        l0_profile,
        l2_topics: convert_l2_topics(&filtered_l2),
        l3_knowledge: convert_l3_knowledge(&l3_knowledge),
        l1_associated_l2: convert_l2_topics(&l1_associated_l2),
        l4_archives: convert_l4_archives(&l4_archives),
    };

    Ok(result)
}

/// Retrieve L2 topics using BM25 and vector similarity
fn retrieve_l2_topics(
    data: &[u8],
    query_text: &str,
    sparse_index: &SparseIndex,
    btree: &BTreeIndex,
    _page_count: u32,
    limit: usize,
) -> Result<Vec<TopicSlot>, MemHopError> {
    // Use BM25 search on sparse index - need to tokenize query
    let query_terms: Vec<String> = query_text.split_whitespace().map(|s| s.to_string()).collect();
    let bm25_results = sparse_index.search(&query_terms, limit * 2); // Get more candidates for filtering

    let mut scored_topics = Vec::new();

    for (id_hash, score) in bm25_results {
        if let Some(page_ref) = btree.search(id_hash) {
            if let Some(slot_data) = crate::query::slot_io::get_slot_data(data, page_ref) {
                if let Ok(topic) = TopicSlot::deserialize(slot_data) {
                    scored_topics.push((topic, score));
                }
            }
        }
    }

    // Sort by score and take top limit
    scored_topics.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
    let topics: Vec<TopicSlot> = scored_topics.into_iter().take(limit).map(|(t, _)| t).collect();

    Ok(topics)
}

/// Filter L2 topics by ID constraints
fn filter_l2_topics(
    topics: &[TopicSlot],
    l2_id_filter: &Option<String>,
    l3_id_filter: &Option<String>,
) -> Vec<TopicSlot> {
    topics
        .iter()
        .filter(|topic| {
            // Filter by L2 ID if specified
            if let Some(ref l2_id) = l2_id_filter {
                let topic_id = format!("{:016x}", topic.id_hash);
                if topic_id != *l2_id {
                    return false;
                }
            }

            // Filter by L3 ID if specified
            // Check if any of the topic's L3 refs match the filter
            if let Some(ref l3_id) = l3_id_filter {
                // Convert l3_id to hash for comparison
                let l3_id_hash = crate::util::hash_id(l3_id);
                if !topic.l3_refs.contains(&l3_id_hash) {
                    return false;
                }
            }

            true
        })
        .cloned()
        .collect()
}

/// Get associated L1 engrams through TopicSlot.node_ids
fn get_associated_l1_engrams(
    data: &[u8],
    topics: &[TopicSlot],
    btree: &BTreeIndex,
    _page_count: u32,
) -> Result<Vec<EngramSlot>, MemHopError> {
    let mut engrams = Vec::new();

    for topic in topics {
        for node_id_hash in &topic.node_ids {
            if let Some(page_ref) = btree.search(*node_id_hash) {
                if let Some(slot_data) = crate::query::slot_io::get_slot_data(data, page_ref) {
                    if let Ok(engram) = EngramSlot::deserialize(slot_data) {
                        engrams.push(engram);
                    }
                }
            }
        }
    }

    Ok(engrams)
}

/// Get associated L3 knowledge through TopicSlot.l3_refs
fn get_associated_l3_knowledge(
    data: &[u8],
    topics: &[TopicSlot],
    btree: &BTreeIndex,
    _page_count: u32,
) -> Result<Vec<KnowledgeSlot>, MemHopError> {
    let mut knowledge_list = Vec::new();
    let mut seen_ids = std::collections::HashSet::new();

    for topic in topics {
        for l3_id_hash in &topic.l3_refs {
            if seen_ids.insert(*l3_id_hash) {
                // Avoid duplicates
                if let Some(page_ref) = btree.search(*l3_id_hash) {
                    if let Some(slot_data) = crate::query::slot_io::get_slot_data(data, page_ref) {
                        if let Ok(knowledge) = KnowledgeSlot::deserialize(slot_data) {
                            knowledge_list.push(knowledge);
                        }
                    }
                }
            }
        }
    }

    Ok(knowledge_list)
}

/// Get associated L4 archives through TopicSlot.l4_refs
fn get_associated_l4_archives(
    data: &[u8],
    topics: &[TopicSlot],
    btree: &BTreeIndex,
    _page_count: u32,
) -> Result<Vec<ArchiveSlot>, MemHopError> {
    let mut archives = Vec::new();

    for topic in topics {
        for l4_id_hash in &topic.l4_refs {
            if let Some(page_ref) = btree.search(*l4_id_hash) {
                if let Some(slot_data) = crate::query::slot_io::get_slot_data(data, page_ref) {
                    if let Ok(archive) = ArchiveSlot::deserialize(slot_data) {
                        archives.push(archive);
                    }
                }
            }
        }
    }

    Ok(archives)
}

/// Get L2 topics associated with L1 engrams (through similarity threshold)
fn get_l1_associated_l2(
    data: &[u8],
    engrams: &[EngramSlot],
    btree: &BTreeIndex,
    _page_count: u32,
) -> Result<Vec<TopicSlot>, MemHopError> {
    let mut associated_topics = Vec::new();
    let mut seen_ids = std::collections::HashSet::new();

    for engram in engrams {
        // Traverse hyperedges from engram to find associated L2 topics
        for edge_ptr in &engram.edge_ptrs {
            if let Some(page_ref) = btree.search(*edge_ptr) {
                if let Some(slot_data) = crate::query::slot_io::get_slot_data(data, page_ref) {
                    // Check if this is a TopicSlot (L2)
                    // For simplicity, try to deserialize as TopicSlot
                    if let Ok(topic) = TopicSlot::deserialize(slot_data) {
                        if seen_ids.insert(topic.id_hash) {
                            associated_topics.push(topic);
                        }
                    }
                }
            }
        }
    }

    Ok(associated_topics)
}

/// Get L0 profile
fn get_l0_profile(
    mmap: &MmapMut,
    btree: &BTreeIndex,
    _page_count: u32,
) -> Result<Option<L0Profile>, MemHopError> {
    // Delegate to unified L0 CRUD implementation
    crate::query::l0_crud::read_profile(mmap, btree)
}

/// Update activation scores for retrieved topics and engrams
fn update_activation_scores(
    mmap: &mut MmapMut,
    topics: &[TopicSlot],
    engrams: &[EngramSlot],
    btree: &BTreeIndex,
) -> Result<(), MemHopError> {
    let now_ms = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as i64;

    // Update topic activation scores
    for topic in topics {
        if let Some(page_ref) = btree.search(topic.id_hash) {
            let page_id = (page_ref >> 16) as u32;
            let offset = (page_id as usize) * PAGE_SIZE + 32;

            if offset + 100 <= mmap.len() {
                if let Ok(mut t) = TopicSlot::deserialize(&mmap[offset..]) {
                    // Increase activation score when retrieved
                    t.activation_score = (t.activation_score + 0.1).min(1.0);
                    t.updated_at = now_ms;
                    
                    let data = t.serialize()
                        .map_err(|e| MemHopError::Serialization(e.to_string()))?;
                    if offset + data.len() <= mmap.len() {
                        mmap[offset..offset + data.len()].copy_from_slice(&data);
                    }
                }
            }
        }
    }

    // Update engram activation (importance boost)
    for engram in engrams {
        if let Some(page_ref) = btree.search(engram.id_hash) {
            let page_id = (page_ref >> 16) as u32;
            let offset = (page_id as usize) * PAGE_SIZE + 32;

            if offset + 100 <= mmap.len() {
                if let Ok(mut e) = EngramSlot::deserialize(&mmap[offset..]) {
                    // Slightly increase importance when retrieved
                    e.importance = (e.importance + 0.05).min(1.0);
                    e.updated_at = now_ms;
                    
                    let data = e.serialize()
                        .map_err(|e| MemHopError::Serialization(e.to_string()))?;
                    if offset + data.len() <= mmap.len() {
                        mmap[offset..offset + data.len()].copy_from_slice(&data);
                    }
                }
            }
        }
    }

    Ok(())
}

/// Collect memory IDs from results
fn collect_memory_ids(topics: &[TopicSlot], knowledge: &[KnowledgeSlot]) -> Vec<String> {
    let mut ids = Vec::new();

    for topic in topics {
        ids.push(format!("{:016x}", topic.id_hash));
    }

    for k in knowledge {
        ids.push(format!("{:016x}", k.id_hash));
    }

    ids
}

/// Convert internal TopicSlot to public L2TopicResult
fn convert_l2_topics(topics: &[TopicSlot]) -> Vec<L2TopicResult> {
    topics
        .iter()
        .map(|topic| L2TopicResult {
            id: format!("{:016x}", topic.id_hash),
            title: topic.title.clone(),
            summary: topic.summary.clone(),
            activation_score: topic.activation_score,
            l1_count: topic.node_ids.len(),
            l3_refs: topic
                .l3_refs
                .iter()
                .map(|h| format!("{:016x}", h))
                .collect(),
            l4_refs: topic
                .l4_refs
                .iter()
                .map(|h| format!("{:016x}", h))
                .collect(),
        })
        .collect()
}

/// Convert internal KnowledgeSlot to public L3KnowledgeResult
fn convert_l3_knowledge(knowledge: &[KnowledgeSlot]) -> Vec<L3KnowledgeResult> {
    knowledge
        .iter()
        .map(|k| L3KnowledgeResult {
            id: format!("{:016x}", k.id_hash),
            title: k.title.clone(),
            domain: k.domain.clone(),
            text: k.text.clone(),
            knowledge_type: format!("{:?}", k.knowledge_type), // Convert enum to string
            confidence: k.confidence,
        })
        .collect()
}

/// Convert internal ArchiveSlot to public L4ArchiveResult
fn convert_l4_archives(archives: &[ArchiveSlot]) -> Vec<L4ArchiveResult> {
    archives
        .iter()
        .map(|a| L4ArchiveResult {
            id: format!("{:016x}", a.id_hash),
            topic_id: format!("{:016x}", a.topic_id),
            content: a.content.clone(),
            timestamp: a.created_at,
        })
        .collect()
}

/// Create a new L2 topic from dialogue content
fn create_new_l2_topic(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    dialogue: &str,
    vector_dim: usize,
) -> Result<TopicSlot, MemHopError> {
    let now_ms = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as i64;

    // Use a unique counter for ID generation
    use std::sync::atomic::{AtomicU64, Ordering};
    static COUNTER: AtomicU64 = AtomicU64::new(0);
    let counter = COUNTER.fetch_add(1, Ordering::SeqCst);

    // Generate a new unique ID from dialogue content, timestamp, and counter
    // This ensures uniqueness even for identical dialogues at the same time
    let id_str = format!("unique_{}_{}_{}_{}", dialogue.len(), now_ms, counter, dialogue.chars().take(10).collect::<String>());
    let id_hash = hash_id(&id_str);

    // Use first 50 characters of dialogue as title
    let title = if dialogue.len() > 50 {
        dialogue[..50].to_string()
    } else {
        dialogue.to_string()
    };

    // Create new TopicSlot
    let new_topic = TopicSlot {
        id_hash,
        title,
        summary: None,
        node_ids: Vec::new(),
        l3_refs: Vec::new(),
        l4_refs: Vec::new(),
        parent_id: None,
        created_at: now_ms,
        updated_at: now_ms,
        version: 1,
        importance: 0.5,
        activation_score: 0.8,
        is_active: true,
        activation_state: crate::slot::topic::ActivationState::Active,
        centroid_vector: None,
        domain_weights: Vec::new(),
        dialogue_range: (now_ms, now_ms),
        reserved: [0u8; 16],
    };

    // Serialize the topic
    let topic_data = new_topic.serialize()
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;

    // Allocate a new page
    let page_id = crate::file::free_list::allocate_from_free_list(mmap, header)?;
    let page_offset = (page_id as usize) * PAGE_SIZE;

    // Write page header
    let page_header = crate::file::page::PageHeader {
        page_id,
        page_type: crate::util::PageType::Topic.to_u16(),
        slot_count: 1,
        free_bytes: (PAGE_SIZE - 32 - topic_data.len()) as u16,
        layer_id: 2, // L2 layer
        next_page: 0xFFFFFFFF,
        prev_page: 0xFFFFFFFF,
        reserved: [0u8; 12],
    };
    crate::file::page::write_page_header(mmap, page_id, &page_header)?;

    // Write topic data (after 32-byte header)
    let data_offset = page_offset + 32;
    if data_offset + topic_data.len() <= mmap.len() {
        mmap[data_offset..data_offset + topic_data.len()].copy_from_slice(&topic_data);
    }

    // Update B-tree index
    let page_ref = ((page_id as u64) << 16) | 0;
    btree.insert(id_hash, page_ref);

    // Update sparse index (tokenize title and add to index)
    let terms: Vec<String> = new_topic.title.split_whitespace().map(|s| s.to_string()).collect();
    sparse_index.add_document(id_hash, terms, new_topic.title.len() as u32);

    Ok(new_topic)
}

/// Enhance query using LLM for keyword extraction and query expansion
///
/// This function calls the LLM to:
/// 1. Extract key concepts from the dialogue
/// 2. Expand with synonyms and related terms
/// 3. Understand user intent
///
/// # Arguments
/// * `llm_config` - LLM configuration (api_url, api_key, model)
/// * `dialogue` - Original user dialogue
///
/// # Returns
/// Enhanced query string with keywords and expanded terms
fn enhance_query_with_llm(llm_config: &crate::query::types::LlmConfig, dialogue: &str) -> Result<String, crate::MemHopError> {
    use crate::dream::deepseek_llm::DeepSeekLlmProvider;
    
    // Create LLM provider
    let provider = DeepSeekLlmProvider::new_with_config(
        llm_config.api_key.clone(),
        llm_config.api_url.clone(),
        llm_config.model.clone(),
    );
    
    // Construct prompt for query enhancement
    let prompt = format!(
        "你是一个查询优化助手。请分析以下用户对话，提取核心关键词并扩展相关术语，\n\
         以便更好地检索相关记忆。\n\n\
         要求：\n\
         1. 提取3-5个核心关键词\n\
         2. 为每个关键词提供1-2个同义词或相关词\n\
         3. 返回格式：关键词1 同义词1 同义词2 | 关键词2 同义词1 | ...\n\
         4. 只返回优化后的查询字符串，不要其他解释\n\n\
         用户对话：{}",
        dialogue
    );
    
    // Call LLM with timeout
    let client = reqwest::blocking::Client::builder()
        .timeout(std::time::Duration::from_secs(10))
        .build()
        .map_err(|e| crate::MemHopError::Serialization(format!("Create HTTP client failed: {}", e)))?;
    
    let body = serde_json::json!({
        "model": llm_config.model,
        "messages": [
            {"role": "system", "content": "You are a query optimization assistant."},
            {"role": "user", "content": prompt}
        ],
        "max_tokens": 128,
        "temperature": 0.3,
    });
    
    let response = client
        .post(&llm_config.api_url)
        .bearer_auth(&llm_config.api_key)
        .json(&body)
        .send()
        .map_err(|e| crate::MemHopError::Serialization(format!("LLM API call failed: {}", e)))?;
    
    if !response.status().is_success() {
        return Err(crate::MemHopError::Serialization(
            format!("LLM API request failed: {} - {}", response.status(), response.text().unwrap_or_default())
        ));
    }
    
    let json: serde_json::Value = response.json()
        .map_err(|e| crate::MemHopError::Serialization(format!("Parse LLM response failed: {}", e)))?;
    
    let enhanced_query = json["choices"][0]["message"]["content"]
        .as_str()
        .map(|s| s.trim().to_string())
        .ok_or_else(|| crate::MemHopError::Serialization("No content in LLM response".to_string()))?;
    
    // Parse the enhanced query (format: "keyword1 synonym1 synonym2 | keyword2 synonym1")
    // Convert to space-separated format for BM25 search
    let parsed_query = enhanced_query
        .split('|')
        .flat_map(|part| part.split_whitespace())
        .collect::<Vec<_>>()
        .join(" ");
    
    Ok(if parsed_query.is_empty() {
        dialogue.to_string()
    } else {
        parsed_query
    })
}
