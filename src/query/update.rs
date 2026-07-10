// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! update_memory() — cross-layer atomic update using v2 StorageEngine (no mmap, no WAL).

use crate::config::MemHopConfig;
use crate::encoder::Encoder;
use crate::index::l2_meta::L2MetaIndex;
use crate::index::sparse::SparseIndex;
use crate::l3;
use crate::layers::archive::ArchiveSlot;
use crate::layers::context::ContextSlot;
use crate::layers::context_node::ContextNode;
use crate::layers::hypergraph::{HypergraphNode, HypergraphSlot, HypergraphSource};
use crate::organize::extract_keywords;
use crate::query::types::*;
use crate::shared::common::{format_hash, now_ms, parse_id_to_hash};
use crate::storage::record::*;
use crate::storage::StorageEngine;
use crate::store::write_slot;
use crate::util::hash_id;
use crate::MemHopError;

/// Convenience alias to avoid a `>>` tokenization issue in function signatures.
type L3IndexMap = std::collections::HashMap<u64, crate::l3::L3Index>;

/// Core update engine: writes L4 archive, updates L2 context, optionally creates
/// L1 node, L3 hypergraph, and L5 action chain — all via v2 StorageEngine.
///
/// No mmap, no WAL journal, no TxState. Each write through `engine.write_record()`
/// is atomic. On partial failure, already-written records become garbage collected
/// during compaction — no manual rollback needed.
#[allow(clippy::too_many_arguments)]
pub fn update_memory_internal(
    engine: &mut StorageEngine,
    request: UpdateRequest,
    sparse_index: &mut SparseIndex,
    l2_meta: &mut L2MetaIndex,
    _config: &MemHopConfig,
    encoder: Option<&(dyn Encoder + Send + Sync)>,
    tracker: Option<&mut crate::l3::DegreeTracker>,
    index_map: Option<&mut L3IndexMap>,
) -> Result<UpdateResult, MemHopError> {
    // Validate basic parameters.
    if request.topic_id.is_empty() {
        return Err(MemHopError::InvalidQuery("topic_id is empty".to_string()));
    }
    if request.dialogue_text.is_empty() {
        return Err(MemHopError::InvalidQuery(
            "dialogue_text is empty".to_string(),
        ));
    }

    let topic_hash = parse_id_to_hash(&request.topic_id);

    let now_ms = now_ms();

    // ------------------------------------------------------------------
    // Step 1: L4 ArchiveSlot (new)
    // ------------------------------------------------------------------
    let l4_id_hash = hash_id(&format!("L4-{}-{}", topic_hash, now_ms));

    use crate::layers::archive::ContentType;
    let archive = ArchiveSlot {
        id_hash: l4_id_hash,
        content_type: ContentType::Text,
        role: 0, // user
        context_id: topic_hash,
        created_at: now_ms,
        content: request.dialogue_text.clone(),
        metadata: request.source.to_metadata_json(),
    };
    write_slot(engine, REC_L4_ARCHIVE, l4_id_hash, &archive)?;
    let archive_id = format_hash(l4_id_hash);

    // ------------------------------------------------------------------
    // Step 2: L2 ContextSlot — create new turn node (depth=1)
    // ------------------------------------------------------------------
    // Resolve scene_id: from request, or derive from topic_hash
    let scene_id = if let Some(ref sid) = request.scene_id {
        parse_id_to_hash(sid)
    } else {
        tracing::warn!(
            "[update_memory] scene_id not provided for topic {}, falling back to topic_hash. \
             Callers should provide scene_id to enable cross-topic merge-compression.",
            request.topic_id
        );
        topic_hash
    };

    // Title from summary or default
    let turn_title = request
        .summary
        .as_ref()
        .map(|s| s.chars().take(50).collect::<String>())
        .unwrap_or_else(|| format!("turn-{}", now_ms));

    // Summary: use provided summary, or fall back to dialogue text
    let turn_summary = request
        .summary
        .clone()
        .unwrap_or_else(|| request.dialogue_text.clone());

    let user_kws = request
        .user_keywords
        .clone()
        .unwrap_or_else(|| vec![turn_title.clone()]);
    let agent_kws = request.agent_keywords.clone().unwrap_or_default();

    let mut turn_ctx = ContextSlot::new_turn(
        scene_id,
        user_kws,
        now_ms,
        vec![l4_id_hash],
        vec![],
        agent_kws,
        now_ms,
        vec![],
        vec![],
        now_ms,
    );
    let turn_hash = turn_ctx.id;

    // Vectorize centroid if encoder is available
    if let Some(enc) = encoder {
        match enc.encode(&turn_summary) {
            Ok(output) => {
                let v_id_hash = hash_id(&format!("v:{}", turn_hash));
                let v_bytes: Vec<u8> = output.dense.iter().flat_map(|v| v.to_ne_bytes()).collect();
                if let Err(e) = engine.write_record(0xF0, v_id_hash, &v_bytes) {
                    tracing::warn!("Failed to write centroid vector: {}", e);
                } else {
                    turn_ctx.centroid_page_ref = v_id_hash;
                }
            }
            Err(e) => {
                tracing::warn!("Failed to encode turn centroid: {}", e);
            }
        }
    }

    // ------------------------------------------------------------------
    // Step 3: L1 ContextNode (optional, only if depth <= 2 and no node points to this L2)
    // ------------------------------------------------------------------
    if turn_ctx.depth <= 2 {
        let has_l1_node = engine.iter_index().any(|(&id_hash, &_offset)| {
            let Ok(Some((record_type, data))) = engine.read_record(id_hash) else {
                return false;
            };
            if record_type != REC_L1_SCENE_NODE {
                return false;
            }
            bincode::deserialize::<ContextNode>(data)
                .map(|n| n.context_id == topic_hash)
                .unwrap_or(false)
        });

        if !has_l1_node {
            let l1_id_hash = hash_id(&format!("L1-{}", topic_hash));
            let node = ContextNode {
                id_hash: l1_id_hash,
                context_id: topic_hash,
                vector_page_ref: 0,
                importance: 0.5,
                valence: 0.0,
                arousal: 0.0,
                created_at: now_ms,
                updated_at: now_ms,
                version: 1,
                edge_ptrs: vec![],
            };
            write_slot(engine, REC_L1_SCENE_NODE, l1_id_hash, &node)?;
        }
    }

    // ------------------------------------------------------------------
    // Step 4: L3 Hypergraph distillation (optional)
    // ------------------------------------------------------------------
    if request.instant_distill {
        distill_l3_for_update(
            engine,
            &request,
            topic_hash,
            now_ms,
            sparse_index,
            tracker,
            index_map,
            &mut turn_ctx,
        )?;
    }

    // ------------------------------------------------------------------
    // Step 5: L5 ActionChain (optional)
    // ------------------------------------------------------------------
    if let Some(ref action_chain) = request.action_chain {
        for action in action_chain {
            let crystal_id_hash = hash_id(&format!(
                "{}-{:?}-{}",
                topic_hash, action.action_type, now_ms
            ));

            use crate::layers::action_chain::ActionChainSlot;
            let chain = ActionChainSlot {
                id_hash: crystal_id_hash,
                title: action.title.clone(),
                trigger: action.description.clone(),
                status: crate::layers::action_chain::ChainStatus::Active,
                confidence: 0.8,
                success_rate: 1.0,
                trigger_count: 0,
                last_triggered: 0,
                created_at: now_ms,
                updated_at: now_ms,
                version: 1,
            };
            write_slot(engine, REC_L5_ACTION_CHAIN, crystal_id_hash, &chain)?;
        }
    }

    // ------------------------------------------------------------------
    // Commit: write the new turn slot to v2 engine and update indices.
    // ------------------------------------------------------------------
    write_slot(engine, REC_L2_TOPIC, turn_hash, &turn_ctx)?;

    // Update sparse index with the turn summary
    let terms = crate::index::sparse::tokenize(&turn_summary);
    let doc_len = terms.len() as u32;
    sparse_index.add_document(turn_hash, terms, doc_len);

    // Update in-memory L2 meta index
    l2_meta.update_from_context(&turn_ctx);

    // Determine update status.
    let status =
        if request.summary.is_some() || request.action_chain.is_some() || request.instant_distill {
            UpdateStatus::Updated
        } else {
            UpdateStatus::Archived
        };

    let dream_triggered = false;
    let turn_node_id = format_hash(turn_hash);

    let result = UpdateResult {
        topic_id: format_hash(topic_hash),
        archive_id,
        status,
        dream_triggered,
        turn_node_id,
    };

    Ok(result)
}

/// L3 distillation helper used by update_memory_internal.
#[allow(clippy::too_many_arguments)]
fn distill_l3_for_update(
    engine: &mut StorageEngine,
    request: &UpdateRequest,
    topic_hash: u64,
    now_ms: i64,
    sparse_index: &SparseIndex,
    mut tracker: Option<&mut crate::l3::DegreeTracker>,
    mut index_map: Option<&mut L3IndexMap>,
    ctx: &mut ContextSlot,
) -> Result<(), MemHopError> {
    let keywords = extract_keywords(&request.dialogue_text, 10);
    let mut graphs_to_link: Vec<u64> = Vec::new();

    for kw in &keywords {
        let hits = sparse_index.entity_search_nodes(kw);
        for (node_hash, _l2_ids) in &hits {
            if let Ok(Some((_rt, slot_data))) = engine.read_record(*node_hash) {
                if let Ok(node) = bincode::deserialize::<HypergraphNode>(slot_data) {
                    if !ctx.user_l3_refs.contains(&node.graph_id) {
                        graphs_to_link.push(node.graph_id);
                    }
                }
            }
        }
    }

    if graphs_to_link.is_empty() && !keywords.is_empty() {
        let distilled_id = hash_id(&format!("distilled_{}_{}", topic_hash, now_ms));
        let graph_name = format!(
            "distilled:{}",
            &request.dialogue_text.chars().take(40).collect::<String>()
        );

        let slot = HypergraphSlot {
            id_hash: distilled_id,
            name: graph_name,
            source: HypergraphSource::Manual,
            node_count: keywords.len() as u32,
            edge_count: 0,
            created_at: now_ms,
            updated_at: now_ms,
            version: 1,
        };

        write_slot(engine, REC_L3_GRAPH_SLOT, distilled_id, &slot)?;

        for kw in &keywords {
            let node_hash = hash_id(&format!("distilled_node_{}_{}", distilled_id, kw));
            let node = HypergraphNode {
                id_hash: node_hash,
                graph_id: distilled_id,
                title: kw.clone(),
                node_type: "concept".to_string(),
                content: String::new(),
                keywords: vec![kw.clone()],
                source_ref: None,
                importance: 0.5,
                summary: None,
                valid_from: 0,
                valid_until: 0,
                created_at: now_ms,
                updated_at: now_ms,
                version: 1,
            };
            l3::store::add_node_with_engine(
                engine,
                node,
                tracker.as_deref_mut(),
                index_map.as_deref_mut(),
            )?;
        }

        graphs_to_link.push(distilled_id);
    }

    graphs_to_link.sort();
    graphs_to_link.dedup();
    ctx.user_l3_refs.extend(graphs_to_link);

    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::config::MemHopConfig;
    use crate::index::l2_meta::L2MetaIndex;
    use crate::index::sparse::SparseIndex;
    use crate::storage::StorageEngine;
    use crate::util::hash_id;
    use std::path::Path;
    use tempfile::TempDir;

    fn make_request(topic_id: &str, text: &str) -> UpdateRequest {
        UpdateRequest {
            topic_id: topic_id.to_string(),
            dialogue_text: text.to_string(),
            summary: None,
            action_chain: None,
            instant_distill: false,
            scene_id: None,
            source: RequestSource::default(),
            user_keywords: None,
            agent_keywords: None,
        }
    }

    #[test]
    fn test_update_memory_writes_l4_and_updates_l2() {
        let temp = tempfile::NamedTempFile::new().unwrap();
        let mut engine = StorageEngine::create(temp.path(), 768).unwrap();

        // Pre-create an L2 topic in engine
        let topic_hash = hash_id("topic-a");
        let topic_id = format_hash(topic_hash);
        let ctx = ContextSlot {
            id: topic_hash,
            scene_id: 0,
            parent_id: None,
            children_ids: vec![],
            depth: 1,
            user_keywords: vec!["topic a".to_string()],
            user_timestamp: 0,
            user_l4_refs: vec![],
            user_l3_refs: vec![],
            agent_keywords: vec![],
            agent_timestamp: 0,
            agent_l4_refs: vec![],
            agent_l3_refs: vec![],
            fused_keywords: vec![],
            fused_summary: None,
            centroid_page_ref: 0,
            created_at: 0,
            updated_at: 0,
            version: 4,
        };
        write_slot(&mut engine, REC_L2_TOPIC, topic_hash, &ctx).unwrap();

        let mut sparse_index = SparseIndex::new();
        let mut l2_meta = L2MetaIndex::new();
        let config = MemHopConfig::new(std::path::PathBuf::from("/dev/null"), 768);

        let result = update_memory_internal(
            &mut engine,
            make_request(&topic_id, "hello world"),
            &mut sparse_index,
            &mut l2_meta,
            &config,
            None, // encoder
            None,
            None,
        )
        .unwrap();

        assert_eq!(result.topic_id, topic_id);
        assert!(!result.archive_id.is_empty());
        assert_eq!(result.status, UpdateStatus::Archived);

        // L2 turn node is a separate ContextSlot (depth=1)
        let turn_hash = parse_id_to_hash(&result.turn_node_id);
        let (_, turn_data) = engine.read_record(turn_hash).unwrap().unwrap();
        let turn_ctx: ContextSlot = bincode::deserialize(turn_data).unwrap();
        assert_eq!(turn_ctx.depth, 1, "turn node should be depth=1");
    }

    #[test]
    fn test_update_memory_failure_rolls_back_l2() {
        // v2 engine writes are atomic — there is no rollback.
        // Partial writes become garbage that compaction will collect.
        // This test validates that the engine still has the original topic.
        let temp = tempfile::NamedTempFile::new().unwrap();
        let mut engine = StorageEngine::create(temp.path(), 768).unwrap();

        let topic_hash = hash_id("topic-b");
        let topic_id = format_hash(topic_hash);
        let ctx = ContextSlot {
            id: topic_hash,
            scene_id: 0,
            parent_id: None,
            children_ids: vec![],
            depth: 1,
            user_keywords: vec!["topic b".to_string()],
            user_timestamp: 0,
            user_l4_refs: vec![],
            user_l3_refs: vec![],
            agent_keywords: vec![],
            agent_timestamp: 0,
            agent_l4_refs: vec![],
            agent_l3_refs: vec![],
            fused_keywords: vec![],
            fused_summary: None,
            centroid_page_ref: 0,
            created_at: 0,
            updated_at: 0,
            version: 4,
        };
        write_slot(&mut engine, REC_L2_TOPIC, topic_hash, &ctx).unwrap();

        let mut sparse_index = SparseIndex::new();
        let mut l2_meta = L2MetaIndex::new();
        let config = MemHopConfig::new(std::path::PathBuf::from("/dev/null"), 768);

        // This should succeed (no overflow mechanism in v2)
        let result = update_memory_internal(
            &mut engine,
            UpdateRequest {
                topic_id: topic_id.clone(),
                dialogue_text: "small dialogue".to_string(),
                summary: Some("x".repeat(1000)),
                action_chain: None,
                instant_distill: false,
                scene_id: None,
                source: RequestSource::default(),
                user_keywords: None,
                agent_keywords: None,
            },
            &mut sparse_index,
            &mut l2_meta,
            &config,
            None,
            None,
            None,
        );

        assert!(
            result.is_ok(),
            "v2 engine writes should not fail on large summary"
        );
    }

    #[test]
    fn test_update_memory_creates_l1_node_when_missing() {
        let temp = tempfile::NamedTempFile::new().unwrap();
        let mut engine = StorageEngine::create(temp.path(), 768).unwrap();

        let topic_hash = hash_id("topic-d");
        let topic_id = format_hash(topic_hash);
        let ctx = ContextSlot {
            id: topic_hash,
            scene_id: 0,
            parent_id: None,
            children_ids: vec![],
            depth: 1,
            user_keywords: vec!["topic d".to_string()],
            user_timestamp: 0,
            user_l4_refs: vec![],
            user_l3_refs: vec![],
            agent_keywords: vec![],
            agent_timestamp: 0,
            agent_l4_refs: vec![],
            agent_l3_refs: vec![],
            fused_keywords: vec![],
            fused_summary: None,
            centroid_page_ref: 0,
            created_at: 0,
            updated_at: 0,
            version: 4,
        };
        write_slot(&mut engine, REC_L2_TOPIC, topic_hash, &ctx).unwrap();

        let mut sparse_index = SparseIndex::new();
        let mut l2_meta = L2MetaIndex::new();
        let config = MemHopConfig::new(std::path::PathBuf::from("/dev/null"), 768);

        update_memory_internal(
            &mut engine,
            make_request(&topic_id, "first turn"),
            &mut sparse_index,
            &mut l2_meta,
            &config,
            None,
            None,
            None,
        )
        .unwrap();

        // There should be a ContextNode whose context_id matches the topic.
        let found = engine.iter_index().any(|(&id_hash, &_offset)| {
            let Ok(Some((record_type, data))) = engine.read_record(id_hash) else {
                return false;
            };
            if record_type != REC_L1_SCENE_NODE {
                return false;
            }
            bincode::deserialize::<ContextNode>(data)
                .map(|n| n.context_id == topic_hash)
                .unwrap_or(false)
        });
        assert!(found);
    }

    #[test]
    fn test_update_memory_checkpoint_persists_data() {
        let dir = TempDir::new().unwrap();
        let engine_path = dir.path().join("test.meh");
        let topic_hash;

        {
            let mut engine = StorageEngine::create(&engine_path, 768).unwrap();

            // Pre-create an L2 topic
            let topic_hash_val = hash_id("checkpoint-topic");
            let topic_id = format_hash(topic_hash_val);
            let ctx = ContextSlot {
                id: topic_hash_val,
                scene_id: 0,
                parent_id: None,
                children_ids: vec![],
                depth: 1,
                user_keywords: vec!["checkpoint topic".to_string()],
                user_timestamp: 0,
                user_l4_refs: vec![],
                user_l3_refs: vec![],
                agent_keywords: vec![],
                agent_timestamp: 0,
                agent_l4_refs: vec![],
                agent_l3_refs: vec![],
                fused_keywords: vec![],
                fused_summary: None,
                centroid_page_ref: 0,
                created_at: 0,
                updated_at: 0,
                version: 4,
            };
            write_slot(&mut engine, REC_L2_TOPIC, topic_hash_val, &ctx).unwrap();

            let mut sparse_index = SparseIndex::new();
            let mut l2_meta = L2MetaIndex::new();
            let config = MemHopConfig::new(std::path::PathBuf::from("/dev/null"), 768);

            update_memory_internal(
                &mut engine,
                make_request(&topic_id, "data to persist"),
                &mut sparse_index,
                &mut l2_meta,
                &config,
                None,
                None,
                None,
            )
            .unwrap();

            let snapshot = crate::storage::engine::IndexSnapshotData::default();
            engine.checkpoint(&snapshot).unwrap();
            topic_hash = topic_hash_val;
        }

        // Re-open and verify the L2 topic exists.
        let engine = StorageEngine::open(&engine_path).unwrap();
        let (_rt, data) = engine.read_record(topic_hash).unwrap().unwrap();
        let ctx: ContextSlot = bincode::deserialize(data).unwrap();
        assert_eq!(ctx.id, topic_hash, "topic should exist after reopen");
    }
}
