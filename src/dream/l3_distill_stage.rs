//! Stage: L3 Knowledge Distillation
//!
//! Distills knowledge from active L2 contexts into L3 hypergraph nodes and edges
//! using LLM-based concept extraction.
//!
//! # Flow
//! 1. Collect active depth-1 L2 contexts with summaries
//! 2. For each context, call LLM to extract concepts and relations via JSON
//! 3. Create HypergraphSlot (if not already linked) + nodes + edges
//! 4. Update L2 ContextSlot's l3_refs to point to the new graph
//! 5. Return IDs of newly created nodes
//!
//! # Degradation
//! LLM call failure or JSON parse failure → skip (log warning, don't block pipeline)

use crate::dream::llm::LlmProvider;
use crate::file::free_list::{allocate_from_free_list, free_page};
use crate::file::header::FileHeader;
use crate::index::btree::BTreeIndex;
use crate::index::sparse::SparseIndex;
use crate::query::slot_io::get_slot_data;
use crate::slot::context::ContextSlot;
use crate::slot::hypergraph::{
    GraphEdgeKind, HypergraphEdge, HypergraphNode, HypergraphSlot, HypergraphSource,
};
use crate::util::hash_id;
use crate::util::PAGE_SIZE;
use crate::MemHopError;
use memmap2::MmapMut;
use serde::Deserialize;
use std::collections::{HashMap, HashSet};

// ============================================================================
// Data structures for LLM JSON response parsing
// ============================================================================

#[derive(Deserialize, Debug)]
struct LlmConcept {
    name: String,
    #[serde(rename = "type")]
    node_type: String,
    description: String,
    #[serde(default)]
    keywords: Vec<String>,
}

#[derive(Deserialize, Debug)]
struct LlmRelation {
    from: String,
    to: String,
    #[serde(default = "default_relation_kind")]
    kind: String,
}

fn default_relation_kind() -> String {
    "Dependency".to_string()
}

#[derive(Deserialize, Debug)]
struct LlmDistillResult {
    #[serde(default)]
    concepts: Vec<LlmConcept>,
    #[serde(default)]
    relations: Vec<LlmRelation>,
}

// ============================================================================
// Core distillation logic
// ============================================================================

/// Distill L3 hypergraph knowledge from active L2 contexts via LLM.
///
/// For each active depth-1 L2 context that has a non-empty summary:
/// 1. Call LLM to extract concepts and relations as JSON
/// 2. Create an L3 hypergraph (or reuse existing one linked via l3_refs)
/// 3. Add nodes and edges to the L3 graph
///
/// # Returns
/// List of hex-formatted IDs for newly created nodes.
pub fn distill_l3_knowledge(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    _sparse_index: &mut SparseIndex,
    llm: &dyn LlmProvider,
    active_topic_ids: &HashSet<u64>,
) -> Result<Vec<String>, MemHopError> {
    let now_ms = crate::query::common::now_ms();

    let mut all_new_ids: Vec<String> = Vec::new();

    for &topic_id in active_topic_ids {
        // 1. Read the context from disk
        let ctx = match read_context(mmap, btree, topic_id) {
            Some(c) => c,
            None => continue,
        };

        // Only process depth=1 active topics with a non-empty summary
        if ctx.depth != 1 {
            continue;
        }
        let summary = match ctx.summary {
            Some(ref s) if !s.is_empty() => s.clone(),
            _ => continue,
        };

        // 2. Build LLM prompt
        let prompt = format!(
            "从以下知识摘要中提取核心概念和概念之间的关系。\
            返回严格的 JSON 格式(不要包含markdown代码块标记):\n\
            {{\"concepts\":[{{\"name\":\"概念名\",\"type\":\"concept\",\
             \"description\":\"描述\",\"keywords\":[\"关键词\"]}}],\
             \"relations\":[{{\"from\":\"概念名\",\"to\":\"概念名\",\"kind\":\"Dependency\"}}]}}\n\n\
             摘要:\n{}",
            summary
        );

        // 3. Call LLM
        let llm_response = match llm.summarize(&[prompt]) {
            Ok(r) => r,
            Err(e) => {
                eprintln!(
                    "Warning: L3 distillation LLM call failed for '{}': {}",
                    ctx.title, e
                );
                continue;
            }
        };

        // 4. Parse JSON from LLM response
        let (concepts, relations) = match parse_distill_json(&llm_response) {
            Some(result) => (result.concepts, result.relations),
            None => {
                eprintln!(
                    "Warning: Failed to parse LLM JSON for context '{}'",
                    ctx.title
                );
                continue;
            }
        };

        if concepts.is_empty() {
            continue;
        }

        // 5. Determine target L3 graph ID
        let graph_id = resolve_or_create_graph(mmap, header, btree, &ctx, topic_id, now_ms)?;

        // 6. Create nodes for each concept
        let mut concept_id_map: HashMap<String, u64> = HashMap::new();
        for concept in &concepts {
            let node_hash = hash_id(&format!("{:016x}_{}", graph_id, concept.name));
            let node = HypergraphNode {
                id_hash: node_hash,
                graph_id,
                title: concept.name.clone(),
                node_type: concept.node_type.clone(),
                content: concept.description.clone(),
                keywords: concept.keywords.clone(),
                source_ref: None,
                importance: 0.7,
                created_at: now_ms,
                updated_at: now_ms,
                version: 1,
            };
            match crate::l3::add_node(mmap, header, btree, node) {
                Ok(id) => {
                    all_new_ids.push(id);
                    concept_id_map.insert(concept.name.clone(), node_hash);
                }
                Err(e) => {
                    eprintln!(
                        "Warning: Failed to create concept node '{}': {}",
                        concept.name, e
                    );
                }
            }
        }

        // 7. Create edges for relations
        for rel in &relations {
            let from_hash = match concept_id_map.get(&rel.from) {
                Some(h) => *h,
                None => continue,
            };
            let to_hash = match concept_id_map.get(&rel.to) {
                Some(h) => *h,
                None => continue,
            };

            let edge = HypergraphEdge {
                id_hash: hash_id(&format!("{:016x}->{:016x}", from_hash, to_hash)),
                graph_id,
                kind: GraphEdgeKind::Dependency,
                node_ids: vec![from_hash, to_hash],
                weight: 1.0,
                label: Some(if rel.kind.is_empty() {
                    "related".to_string()
                } else {
                    rel.kind.clone()
                }),
                created_at: now_ms,
            };

            if let Err(e) = crate::l3::add_edge(mmap, header, btree, edge) {
                eprintln!("Warning: Failed to create relation edge: {}", e);
            }
        }
    }

    Ok(all_new_ids)
}

// ============================================================================
// Helper: Read ContextSlot by ID hash
// ============================================================================

fn read_context(mmap: &MmapMut, btree: &BTreeIndex, id_hash: u64) -> Option<ContextSlot> {
    let page_ref = btree.search(id_hash)?;
    let data: &[u8] = &mmap[..];
    let slot_data = get_slot_data(data, page_ref)?;
    ContextSlot::deserialize_slot(slot_data).ok()
}

// ============================================================================
// Helper: Resolve or create an L3 hypergraph for a context
// ============================================================================

fn resolve_or_create_graph(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    ctx: &ContextSlot,
    topic_id: u64,
    now_ms: i64,
) -> Result<u64, MemHopError> {
    // Reuse existing L3 graph if already linked
    if let Some(&existing_graph_id) = ctx.l3_refs.first() {
        if btree.search(existing_graph_id).is_some() {
            return Ok(existing_graph_id);
        }
    }

    // Create a new HypergraphSlot
    let new_graph_id = hash_id(&format!("l3_distill_{:016x}", topic_id));
    let slot = HypergraphSlot {
        id_hash: new_graph_id,
        name: format!("Distilled: {}", ctx.title),
        source: HypergraphSource::Manual,
        node_count: 0,
        edge_count: 0,
        created_at: now_ms,
        updated_at: now_ms,
        version: 1,
    };

    let data_bytes = slot
        .serialize()
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;

    let page_id = allocate_from_free_list(mmap, header)?;
    let offset = (page_id as usize) * PAGE_SIZE + 32;

    if offset + data_bytes.len() > mmap.len() {
        // Rollback: free the allocated page
        free_page(mmap, header, page_id)?;
        return Err(MemHopError::Serialization(
            "HypergraphSlot too large for page".to_string(),
        ));
    }

    mmap[offset..offset + data_bytes.len()].copy_from_slice(&data_bytes);
    btree.insert(new_graph_id, (page_id as u64) << 16);

    // Now update the context's l3_refs
    add_l3_ref_to_context(mmap, btree, topic_id, new_graph_id, now_ms)?;

    Ok(new_graph_id)
}

// ============================================================================
// Helper: Add an l3_ref to a ContextSlot
// ============================================================================

/// Add an L3 graph reference to a ContextSlot's l3_refs list.
fn add_l3_ref_to_context(
    mmap: &mut MmapMut,
    btree: &BTreeIndex,
    ctx_id: u64,
    graph_hash: u64,
    now_ms: i64,
) -> Result<(), MemHopError> {
    let page_ref = match btree.search(ctx_id) {
        Some(pr) => pr,
        None => return Ok(()),
    };

    let page_id = (page_ref >> 16) as u32;
    let offset = (page_id as usize) * PAGE_SIZE;

    if offset + PAGE_SIZE > mmap.len() {
        return Err(MemHopError::PageNotFound(page_id));
    }

    // Read full page
    let mut page_buf = vec![0u8; PAGE_SIZE];
    page_buf.copy_from_slice(&mmap[offset..offset + PAGE_SIZE]);

    // Deserialize ContextSlot from the data region
    if let Ok(mut ctx) = ContextSlot::deserialize_slot(&page_buf[32..]) {
        if !ctx.l3_refs.contains(&graph_hash) {
            ctx.l3_refs.push(graph_hash);
            ctx.updated_at = now_ms;

            let ctx_bytes = ctx
                .serialize()
                .map_err(|e| MemHopError::Serialization(e.to_string()))?;
            if ctx_bytes.len() > PAGE_SIZE - 32 {
                return Err(MemHopError::Serialization(
                    "ContextSlot too large after adding l3_ref".to_string(),
                ));
            }

            // Write modified slot data back
            page_buf[32..32 + ctx_bytes.len()].copy_from_slice(&ctx_bytes);
            if 32 + ctx_bytes.len() < PAGE_SIZE {
                page_buf[32 + ctx_bytes.len()..].fill(0);
            }

            // Write full page back
            mmap[offset..offset + PAGE_SIZE].copy_from_slice(&page_buf);
        }
    }

    Ok(())
}

// ============================================================================
// Helper: Parse LLM JSON response
// ============================================================================

/// Parse the LLM response into a structured distillation result.
///
/// Strips markdown code block markers (```json ... ```) if present,
/// then attempts JSON deserialization.
fn parse_distill_json(response: &str) -> Option<LlmDistillResult> {
    let trimmed = response.trim();

    // Strip markdown code block markers if present
    let json_str = if trimmed.starts_with("```") {
        let start = trimmed.find('\n').map(|i| i + 1).unwrap_or(0);
        let end = trimmed.rfind("```").unwrap_or(trimmed.len());
        if end > start {
            trimmed[start..end].trim()
        } else {
            trimmed
        }
    } else {
        trimmed
    };

    serde_json::from_str::<LlmDistillResult>(json_str).ok()
}

// ============================================================================
// Tests
// ============================================================================

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_parse_distill_json_plain() {
        let input = r#"{"concepts":[{"name":"Rust","type":"language","description":"A systems programming language","keywords":["systems","safe"]}],"relations":[{"from":"Rust","to":"Cargo","kind":"BuildTool"}]}"#;
        let result = parse_distill_json(input);
        assert!(result.is_some());
        let r = result.unwrap();
        assert_eq!(r.concepts.len(), 1);
        assert_eq!(r.concepts[0].name, "Rust");
        assert_eq!(r.relations.len(), 1);
        assert_eq!(r.relations[0].from, "Rust");
    }

    #[test]
    fn test_parse_distill_json_with_markdown() {
        let input = "```json\n{\"concepts\":[{\"name\":\"Test\",\"type\":\"concept\",\"description\":\"A test\",\"keywords\":[]}]}\n```";
        let result = parse_distill_json(input);
        assert!(result.is_some());
        let r = result.unwrap();
        assert_eq!(r.concepts.len(), 1);
        assert_eq!(r.concepts[0].name, "Test");
    }

    #[test]
    fn test_parse_distill_json_empty() {
        let input = r#"{"concepts":[],"relations":[]}"#;
        let result = parse_distill_json(input);
        assert!(result.is_some());
        let r = result.unwrap();
        assert_eq!(r.concepts.len(), 0);
        assert_eq!(r.relations.len(), 0);
    }
}
