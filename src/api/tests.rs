// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Unit tests for the MemHop API surface.

use super::*;
use crate::config::{LlmConfig, MemHopConfig};
use crate::query::types::{SearchQuery, UpdateRequest};
use crate::shared::common::parse_id_to_hash;
use std::collections::HashMap;
use tempfile::TempDir;

/// Build a topic by search auto-create and return its ID.
fn create_topic(db: &mut MemHop, query: &str) -> String {
    let res = db
        .search(SearchQuery {
            query: query.to_string(),
            layers: vec![2],
            max_results: 20,
            min_score: 0.0,
            include_profile: false,
            filters: None,
            directed_l2_id: None,
            directed_l3_id: None,
            auto_create: Some(1),
        })
        .unwrap();
    res.contexts[0].id.clone()
}

fn make_update_req(id: &str, turn_text: &str, scene_id: Option<&str>) -> UpdateRequest {
    let mut fields = HashMap::new();
    fields.insert(
        "dialogue_text".to_string(),
        serde_json::Value::String(turn_text.to_string()),
    );
    if let Some(sid) = scene_id {
        fields.insert(
            "scene_id".to_string(),
            serde_json::Value::String(sid.to_string()),
        );
    }
    UpdateRequest {
        id: id.to_string(),
        layer: 2,
        fields,
    }
}

/// Return the encoder port from environment or default.
fn encoder_port() -> u16 {
    std::env::var("MEMHOP_ENCODER_GRPC_ADDR")
        .ok()
        .and_then(|addr| addr.split(':').last().and_then(|p| p.parse().ok()))
        .unwrap_or(27110)
}

fn llm_config_from_env() -> LlmConfig {
    let api_key = std::env::var("MEMHOP_LLM_API_KEY").unwrap_or_default();
    let api_url = std::env::var("MEMHOP_LLM_API_URL")
        .unwrap_or_else(|_| "https://api.deepseek.com/v1/chat/completions".to_string());
    let model = std::env::var("MEMHOP_LLM_MODEL").unwrap_or_else(|_| "deepseek-chat".to_string());
    LlmConfig::new(api_url, api_key, model, "zh".to_string())
}

fn make_config(path: std::path::PathBuf) -> MemHopConfig {
    let mut config = MemHopConfig::new(path, 768);
    config.encoder_grpc_addr = format!("http://127.0.0.1:{}", encoder_port());
    config.llm = llm_config_from_env();
    config
}

// ============================================================================
// AC-1: 每轮对话独立成节点
// ============================================================================

#[test]
#[ignore = "requires LLM API key (MEMHOP_LLM_API_KEY)"]
fn test_ac1_each_turn_independent_node() {
    let dir = TempDir::new().unwrap();
    let path = dir.path().join("ac1.meh");
    let config = make_config(path);
    let mut db = MemHop::open(config).unwrap();

    // Create a topic via auto_create to get a topic_id
    let topic_id = create_topic(&mut db, "test conversation topic");
    let scene_id = format!("scene_{}", topic_id);

    // Call update_memory 3 times, each creates a new depth-1 turn node
    for i in 0..3 {
        std::thread::sleep(std::time::Duration::from_millis(2));
        db.update_memory(make_update_req(
            &topic_id,
            &format!("This is turn {} dialogue", i),
            Some(&scene_id),
        ))
        .unwrap();
    }

    // Query the scene tree to get actual turn node IDs
    // Note: UpdateResult.id returns the topic_id, not the turn node id
    let tree = db.list_scene_tree(&scene_id).unwrap();
    let turn_nodes: Vec<_> = tree.nodes.iter().filter(|n| n.depth == 1).collect();

    assert!(
        turn_nodes.len() >= 3,
        "should have at least 3 depth-1 nodes in scene, got {}",
        turn_nodes.len()
    );

    // Verify each turn node: depth=1, children_ids empty, same scene_id
    let expected_scene_id = crate::shared::common::format_hash(parse_id_to_hash(&scene_id));
    for (i, node) in turn_nodes.iter().enumerate() {
        assert_eq!(node.depth, 1, "turn {} should be depth=1", i);
        assert!(
            node.children_ids.is_empty(),
            "turn {} should have empty children_ids, got {:?}",
            i,
            node.children_ids
        );
        assert_eq!(
            node.scene_id, expected_scene_id,
            "turn {} scene_id mismatch",
            i
        );
    }
}

// ============================================================================
// AC-6: 场景树查询
// ============================================================================

#[test]
#[ignore = "requires LLM API key (MEMHOP_LLM_API_KEY)"]
fn test_ac6_scene_tree_query() {
    let dir = TempDir::new().unwrap();
    let path = dir.path().join("ac6.meh");
    let config = make_config(path);
    let mut db = MemHop::open(config).unwrap();

    // Create a topic and add 3 turn nodes
    let topic_id = create_topic(&mut db, "scene tree test");
    let scene_id = format!("scene_{}", topic_id);

    for i in 0..3 {
        std::thread::sleep(std::time::Duration::from_millis(2));
        db.update_memory(make_update_req(
            &topic_id,
            &format!("Turn {} content", i),
            Some(&scene_id),
        ))
        .unwrap();
    }

    // Query the scene tree
    let tree = db.list_scene_tree(&scene_id).unwrap();

    // Should have 4 nodes (1 auto-created + 3 turn nodes)
    assert!(
        tree.total_turns >= 3,
        "total_turns should be >= 3, got {}",
        tree.total_turns
    );

    // All nodes are depth=1, so depth_distribution[0] >= 3
    assert!(
        tree.depth_distribution[0] >= 3,
        "depth_distribution[0] should be >= 3, got {:?}",
        tree.depth_distribution
    );

    // Depth 2, 3, 4 should be 0 (no hierarchy in this test)
    assert_eq!(tree.depth_distribution[1], 0, "no depth=2 nodes expected");
    assert_eq!(tree.depth_distribution[2], 0, "no depth=3 nodes expected");
    assert_eq!(tree.depth_distribution[3], 0, "no depth=4 nodes expected");

    // Should have nodes in the result
    assert!(!tree.nodes.is_empty(), "scene tree should have nodes");

    // Edge: standalone depth-1 nodes have no parent-child edges
    assert!(
        tree.edges.is_empty(),
        "standalone depth-1 nodes should have no edges"
    );

    // scene_id should match
    assert_eq!(
        tree.scene_id,
        crate::shared::common::format_hash(parse_id_to_hash(&scene_id))
    );
}
