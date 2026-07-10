// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Stage: L3 Knowledge Distillation — LLM-based concept extraction from active L2 contexts into L3 hypergraph.

use crate::dream::llm::{L3Extraction, LlmDistillResult};
use crate::index::sparse::SparseIndex;
use crate::layers::context::TopicSlot;
use crate::layers::hypergraph::{GraphEdge, GraphEdgeKind, GraphNode, GraphSlot, HypergraphSource};
use crate::storage::record::{
    REC_L2_TOPIC, REC_L3_GRAPH_EDGE, REC_L3_GRAPH_NODE, REC_L3_GRAPH_SLOT,
};
use crate::storage::StorageEngine;
use crate::util::hash_id;
use crate::MemHopError;
use std::collections::HashMap;

// ============================================================================
// Apply pre-computed L3 extractions
// ============================================================================
pub fn apply_distill_extractions(
    extractions: &[L3Extraction],
    engine: &mut StorageEngine,
    _sparse_index: &mut SparseIndex,
) -> Result<Vec<String>, MemHopError> {
    let now_ms = crate::shared::common::now_ms();
    let mut all_new_ids: Vec<String> = Vec::new();

    for extraction in extractions {
        let ctx = match read_context(engine, extraction.context_id) {
            Some(c) => c,
            None => continue,
        };
        let result = LlmDistillResult {
            concepts: extraction.concepts.clone(),
            relations: extraction.relations.clone(),
        };
        create_l3_nodes_and_edges(
            engine,
            &ctx,
            extraction.context_id,
            now_ms,
            &mut all_new_ids,
            result,
        )?;
    }

    Ok(all_new_ids)
}

/// Create L3 hypergraph nodes and edges from LLM distillation results.
#[allow(clippy::too_many_arguments)]
fn create_l3_nodes_and_edges(
    engine: &mut StorageEngine,
    ctx: &TopicSlot,
    topic_id: u64,
    now_ms: i64,
    all_new_ids: &mut Vec<String>,
    llm_response: LlmDistillResult,
) -> Result<(), MemHopError> {
    let (concepts, relations) = (llm_response.concepts, llm_response.relations);

    if concepts.is_empty() {
        return Ok(());
    }

    let graph_id = resolve_or_create_graph(engine, ctx, topic_id, now_ms)?;

    let mut concept_id_map: HashMap<String, u64> = HashMap::new();
    for concept in &concepts {
        let node_hash = hash_id(&format!("{:016x}_{}", graph_id, concept.name));
        let node = GraphNode {
            id_hash: node_hash,
            graph_id,
            title: concept.name.clone(),
            node_type: concept.node_type.clone(),
            content: concept.description.clone(),
            keywords: concept.keywords.clone(),
            source_ref: None,
            importance: 0.7,
            valid_from: now_ms,
            valid_until: 0,
            summary: None,
            created_at: now_ms,
            updated_at: now_ms,
            version: 1,
        };
        let node_data =
            bincode::serialize(&node).map_err(|e| MemHopError::Serialization(e.to_string()))?;
        engine.write_record(REC_L3_GRAPH_NODE, node_hash, &node_data)?;
        all_new_ids.push(format!("{:016x}", node_hash));
        concept_id_map.insert(concept.name.clone(), node_hash);
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

        let edge = GraphEdge {
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
            description: None,
            confidence: 1.0,
            valid_from: now_ms,
            valid_until: 0,
            created_at: now_ms,
        };

        let edge_data =
            bincode::serialize(&edge).map_err(|e| MemHopError::Serialization(e.to_string()))?;
        engine.write_record(REC_L3_GRAPH_EDGE, edge.id_hash, &edge_data)?;
    }

    Ok(())
}

// ============================================================================
// Helper: Read TopicSlot by ID hash via engine
// ============================================================================

fn read_context(engine: &StorageEngine, id_hash: u64) -> Option<TopicSlot> {
    let (_, data) = engine.read_record(id_hash).ok()??;
    bincode::deserialize::<TopicSlot>(data).ok()
}

// ============================================================================
// Helper: Resolve or create an L3 hypergraph for a context via engine
// ============================================================================

fn resolve_or_create_graph(
    engine: &mut StorageEngine,
    ctx: &TopicSlot,
    topic_id: u64,
    now_ms: i64,
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
        if engine.contains(existing_graph_id) {
            return Ok(existing_graph_id);
        }
    }

    let new_graph_id = hash_id(&format!("l3_distill_{:016x}", topic_id));
    let display_name = if ctx.fused_keywords.is_empty() {
        ctx.user_keywords.join(", ")
    } else {
        ctx.fused_keywords.join(", ")
    };
    let slot = GraphSlot {
        id_hash: new_graph_id,
        name: format!("Distilled: {}", display_name),
        source: HypergraphSource::Context(topic_id),
        node_count: 0,
        edge_count: 0,
        created_at: now_ms,
        updated_at: now_ms,
        version: 1,
    };

    let data_bytes =
        bincode::serialize(&slot).map_err(|e| MemHopError::Serialization(e.to_string()))?;

    engine.write_record(REC_L3_GRAPH_SLOT, new_graph_id, &data_bytes)?;

    add_l3_ref_to_context(engine, topic_id, new_graph_id, now_ms)?;

    Ok(new_graph_id)
}

// ============================================================================
// Helper: Add an l3_ref to a TopicSlot via engine
// ============================================================================

/// Add an L3 graph reference to a TopicSlot's l3_refs list.
fn add_l3_ref_to_context(
    engine: &mut StorageEngine,
    ctx_id: u64,
    graph_hash: u64,
    now_ms: i64,
) -> Result<(), MemHopError> {
    let (_, data) = match engine.read_record(ctx_id)? {
        Some(v) => v,
        None => return Ok(()),
    };

    let mut ctx: TopicSlot =
        bincode::deserialize(data).map_err(|e| MemHopError::Serialization(e.to_string()))?;

    let already_has =
        ctx.user_l3_refs.contains(&graph_hash) || ctx.agent_l3_refs.contains(&graph_hash);
    if !already_has {
        ctx.agent_l3_refs.push(graph_hash);
        ctx.updated_at = now_ms;

        let ctx_bytes =
            bincode::serialize(&ctx).map_err(|e| MemHopError::Serialization(e.to_string()))?;
        engine.write_record(REC_L2_TOPIC, ctx_id, &ctx_bytes)?;
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
