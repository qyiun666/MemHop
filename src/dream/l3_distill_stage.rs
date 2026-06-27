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
use crate::file::free_list::allocate_or_extend;
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
use std::collections::{HashMap, HashSet};
use std::fs::File;

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
    file: &mut File,
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

        // 2. Call LLM to distill concepts directly
        let llm_response = match llm.distill_concepts(&summary) {
            Ok(r) => r,
            Err(e) => {
                eprintln!(
                    "Warning: L3 distillation LLM call failed for '{}': {}",
                    ctx.title, e
                );
                continue;
            }
        };

        // 3. Use the structured distillation result directly
        let (concepts, relations) = (llm_response.concepts, llm_response.relations);

        if concepts.is_empty() {
            continue;
        }

        // 4. Determine target L3 graph ID
        let graph_id = resolve_or_create_graph(mmap, header, btree, &ctx, topic_id, now_ms, file)?;

        // 5. Create nodes for each concept
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
            match crate::l3::add_node(mmap, header, btree, node, file) {
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

        // 6. Create edges for relations
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
                kind: map_edge_kind(&rel.kind),
                node_ids: vec![from_hash, to_hash],
                weight: 1.0,
                label: Some(if rel.kind.is_empty() {
                    "related".to_string()
                } else {
                    rel.kind.clone()
                }),
                created_at: now_ms,
            };

            if let Err(e) = crate::l3::add_edge(mmap, header, btree, edge, file) {
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
    file: &mut File,
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

    if data_bytes.len() > PAGE_SIZE - 32 {
        return Err(MemHopError::Serialization(
            "HypergraphSlot too large for page".to_string(),
        ));
    }

    let page_id = allocate_or_extend(mmap, header, file, 500)?;
    let page_offset = (page_id as usize) * PAGE_SIZE;
    let data_offset = page_offset + 32;

    // Write proper page header so list_knowledge can identify HypergraphSlot pages.
    let page_header = crate::file::page::PageHeader {
        page_id,
        page_type: crate::util::PageType::HypergraphSlot.to_u16(),
        slot_count: 1,
        free_bytes: (PAGE_SIZE - 32).saturating_sub(data_bytes.len()) as u16,
        layer_id: 3,
        next_page: 0xFFFFFFFF,
        prev_page: 0xFFFFFFFF,
        reserved: [0u8; 12],
    };
    mmap[page_offset..page_offset + 32].copy_from_slice(&page_header.to_bytes());

    mmap[data_offset..data_offset + data_bytes.len()].copy_from_slice(&data_bytes);
    if data_offset + data_bytes.len() < page_offset + PAGE_SIZE {
        mmap[data_offset + data_bytes.len()..page_offset + PAGE_SIZE].fill(0);
    }
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
// Helper: Map LLM relation kind to GraphEdgeKind
// ============================================================================

fn map_edge_kind(kind: &str) -> GraphEdgeKind {
    match kind.to_lowercase().as_str() {
        "related" | "相关" => GraphEdgeKind::Related,
        "causal" | "因果" => GraphEdgeKind::Causal,
        "partof" | "part_of" | "部分" => GraphEdgeKind::PartOf,
        "sequence" | "顺序" => GraphEdgeKind::Sequence,
        "dependency" | "依赖" => GraphEdgeKind::Dependency,
        _ => GraphEdgeKind::Custom,
    }
}

// ============================================================================
// Tests
// ============================================================================

#[cfg(test)]
mod tests {
    use super::*;
    use crate::dream::llm::LlmDistillResult;

    #[test]
    fn test_distill_result_deser_plain() {
        let input = r#"{"concepts":[{"name":"Rust","type":"language","description":"A systems programming language","keywords":["systems","safe"]}],"relations":[{"from":"Rust","to":"Cargo","kind":"BuildTool"}]}"#;
        let r: LlmDistillResult = serde_json::from_str(input).unwrap();
        assert_eq!(r.concepts.len(), 1);
        assert_eq!(r.concepts[0].name, "Rust");
        assert_eq!(r.concepts[0].node_type, "language");
        assert_eq!(r.relations.len(), 1);
        assert_eq!(r.relations[0].from, "Rust");
    }

    #[test]
    fn test_distill_result_deser_empty() {
        let input = r#"{"concepts":[],"relations":[]}"#;
        let r: LlmDistillResult = serde_json::from_str(input).unwrap();
        assert_eq!(r.concepts.len(), 0);
        assert_eq!(r.relations.len(), 0);
    }

    #[test]
    fn test_map_edge_kind() {
        assert!(matches!(map_edge_kind("Related"), GraphEdgeKind::Related));
        assert!(matches!(map_edge_kind("causal"), GraphEdgeKind::Causal));
        assert!(matches!(map_edge_kind("part_of"), GraphEdgeKind::PartOf));
        assert!(matches!(map_edge_kind("Sequence"), GraphEdgeKind::Sequence));
        assert!(matches!(
            map_edge_kind("Dependency"),
            GraphEdgeKind::Dependency
        ));
        assert!(matches!(map_edge_kind("unknown"), GraphEdgeKind::Custom));
    }
}
