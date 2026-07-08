// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Stage: L3 Knowledge Distillation — LLM-based concept extraction from active L2 contexts into L3 hypergraph.

use crate::dream::llm::{L3Extraction, LlmDistillResult};
use crate::file::free_list::allocate_or_extend;
use crate::file::header::FileHeader;
use crate::file::page::PageHeader;
use crate::index::btree::BTreeIndex;
use crate::index::sparse::SparseIndex;
use crate::layers::context::ContextSlot;
use crate::layers::hypergraph::{
    GraphEdgeKind, HypergraphEdge, HypergraphNode, HypergraphSlot, HypergraphSource,
};
use crate::shared::slot_io::get_slot_data;
use crate::util::{hash_id, PageType, DEFAULT_GROW_PAGES, PAGE_SIZE, SENTINEL_PAGE_ID};
use crate::MemHopError;
use memmap2::MmapMut;
use std::collections::HashMap;
use std::fs::File;

// ============================================================================
// Apply pre-computed L3 extractions
// ============================================================================
pub fn apply_distill_extractions(
    extractions: &[L3Extraction],
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    _sparse_index: &mut SparseIndex,
    file: &mut File,
) -> Result<Vec<String>, MemHopError> {
    let now_ms = crate::shared::common::now_ms();
    let mut all_new_ids: Vec<String> = Vec::new();

    for extraction in extractions {
        let ctx = match read_context(mmap, btree, extraction.context_id) {
            Some(c) => c,
            None => continue,
        };
        let result = LlmDistillResult {
            concepts: extraction.concepts.clone(),
            relations: extraction.relations.clone(),
        };
        create_l3_nodes_and_edges(
            mmap,
            header,
            btree,
            &ctx,
            extraction.context_id,
            now_ms,
            &mut all_new_ids,
            file,
            result,
        )?;
    }

    Ok(all_new_ids)
}

/// Create L3 hypergraph nodes and edges from LLM distillation results.
#[allow(clippy::too_many_arguments)]
fn create_l3_nodes_and_edges(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    ctx: &ContextSlot,
    topic_id: u64,
    now_ms: i64,
    all_new_ids: &mut Vec<String>,
    file: &mut File,
    llm_response: LlmDistillResult,
) -> Result<(), MemHopError> {
    let (concepts, relations) = (llm_response.concepts, llm_response.relations);

    if concepts.is_empty() {
        return Ok(());
    }

    let graph_id = resolve_or_create_graph(mmap, header, btree, ctx, topic_id, now_ms, file)?;

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
        match crate::l3::add_node(mmap, header, btree, node, file, None, None) {
            Ok(id) => {
                all_new_ids.push(id);
                concept_id_map.insert(concept.name.clone(), node_hash);
            }
            Err(e) => {
                tracing::warn!("Failed to create concept node '{}': {}", concept.name, e);
            }
        }
    }

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

        if let Err(e) = crate::l3::add_edge(mmap, header, btree, edge, file, None) {
            tracing::warn!("Failed to create relation edge: {}", e);
        }
    }

    Ok(())
}

// ============================================================================
// Helper: Read ContextSlot by ID hash
// ============================================================================

fn read_context(mmap: &MmapMut, btree: &BTreeIndex, id_hash: u64) -> Option<ContextSlot> {
    let page_ref = btree.search(id_hash)?;
    let data: &[u8] = &mmap[..];
    let slot_data = get_slot_data(data, page_ref)?;
    ContextSlot::deserialize(slot_data).ok()
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
    // Collect all L3 refs from both user and agent tracks
    let all_l3_refs: Vec<u64> = ctx
        .user_l3_refs
        .iter()
        .chain(ctx.agent_l3_refs.iter())
        .copied()
        .collect();

    // Reuse existing L3 graph if already linked
    if let Some(&existing_graph_id) = all_l3_refs.first() {
        if btree.search(existing_graph_id).is_some() {
            return Ok(existing_graph_id);
        }
    }

    let new_graph_id = hash_id(&format!("l3_distill_{:016x}", topic_id));
    let display_name = if ctx.fused_keywords.is_empty() {
        ctx.user_keywords.join(", ")
    } else {
        ctx.fused_keywords.join(", ")
    };
    let slot = HypergraphSlot {
        id_hash: new_graph_id,
        name: format!("Distilled: {}", display_name),
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

    let page_id = allocate_or_extend(mmap, header, file, DEFAULT_GROW_PAGES)?;
    let page_offset = (page_id as usize) * PAGE_SIZE;
    let data_offset = page_offset + 32;

    // Write proper page header so list_knowledge can identify HypergraphSlot pages
    let mut page_header = PageHeader::new(page_id, PageType::HypergraphSlot, 3, SENTINEL_PAGE_ID);
    page_header.slot_count = 1;
    page_header.free_bytes = (PAGE_SIZE - 32).saturating_sub(data_bytes.len()) as u16;
    mmap[page_offset..page_offset + 32].copy_from_slice(&page_header.to_bytes());

    mmap[data_offset..data_offset + data_bytes.len()].copy_from_slice(&data_bytes);
    if data_offset + data_bytes.len() < page_offset + PAGE_SIZE {
        mmap[data_offset + data_bytes.len()..page_offset + PAGE_SIZE].fill(0);
    }
    btree.insert(new_graph_id, (page_id as u64) << 16);

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

    let mut page_buf = vec![0u8; PAGE_SIZE];
    page_buf.copy_from_slice(&mmap[offset..offset + PAGE_SIZE]);

    if let Ok(mut ctx) = ContextSlot::deserialize(&page_buf[32..]) {
        let already_has =
            ctx.user_l3_refs.contains(&graph_hash) || ctx.agent_l3_refs.contains(&graph_hash);
        if !already_has {
            ctx.agent_l3_refs.push(graph_hash);
            ctx.updated_at = now_ms;

            let ctx_bytes = ctx
                .serialize()
                .map_err(|e| MemHopError::Serialization(e.to_string()))?;
            if ctx_bytes.len() > PAGE_SIZE - 32 {
                return Err(MemHopError::Serialization(
                    "ContextSlot too large after adding l3_ref".to_string(),
                ));
            }

            page_buf[32..32 + ctx_bytes.len()].copy_from_slice(&ctx_bytes);
            if 32 + ctx_bytes.len() < PAGE_SIZE {
                page_buf[32 + ctx_bytes.len()..].fill(0);
            }

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
