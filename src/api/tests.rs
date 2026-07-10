// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Unit tests for the MemHop API surface.

use super::*;
use crate::config::MemHopConfig;
use crate::query::types::{SearchQuery, TopicListQuery, UpdateRequest};
use crate::shared::common::parse_id_to_hash;
use tempfile::TempDir;

// ============================================================================
// AC-1: 每轮对话独立成节点
// ============================================================================

#[test]
fn test_ac1_each_turn_independent_node() {
    let dir = TempDir::new().unwrap();
    let path = dir.path().join("ac1.meh");
    let mut config = MemHopConfig::new(path, 768);
    config.encoder_grpc_addr = None;
    let mut db = MemHop::open(config).unwrap();

    // Create a topic via auto_create to get a topic_id
    let search_res = db
        .search_context(SearchQuery {
            dialogue: "test conversation topic".to_string(),
            l2_id: None,
            l3_id: None,
            auto_create: true,
        })
        .unwrap();
    let topic_id = search_res.contexts[0].id.clone();
    let scene_id = format!("scene_{}", topic_id);

    // Call update_memory 3 times, each creates a new depth-1 turn node
    let mut turn_ids: Vec<String> = Vec::new();
    for i in 0..3 {
        std::thread::sleep(std::time::Duration::from_millis(2));
        let result = db
            .update_memory(UpdateRequest {
                topic_id: topic_id.clone(),
                dialogue_text: format!("This is turn {} dialogue", i),
                summary: Some(format!("Summary of turn {} content", i)),
                action_chain: None,
                instant_distill: false,
                source: Default::default(),
                scene_id: Some(scene_id.clone()),
                user_keywords: None,
                agent_keywords: None,
            })
            .unwrap();
        turn_ids.push(result.turn_node_id);
    }

    assert_eq!(
        turn_ids.len(),
        3,
        "update_memory should return 3 turn_node_ids"
    );

    // Verify each turn node: depth=1, user_keywords non-empty, children_ids empty, same scene_id
    let expected_scene_hash = parse_id_to_hash(&scene_id);
    for (i, tid) in turn_ids.iter().enumerate() {
        let detail = db
            .get_l2(tid)
            .unwrap_or_else(|_| panic!("turn {} should be retrievable via get_l2", i))
            .unwrap_or_else(|| panic!("turn {} should exist", i));

        assert_eq!(detail.depth, 1, "turn {} should be depth=1", i);
        assert!(
            !detail.user_keywords.is_empty(),
            "turn {} should have user_keywords",
            i
        );
        assert!(
            detail.children_ids.is_empty(),
            "turn {} should have empty children_ids, got {:?}",
            i,
            detail.children_ids
        );
        assert_eq!(
            detail.scene_id, expected_scene_hash,
            "turn {} scene_id mismatch",
            i
        );
    }

    // Verify all 3 turns have the same scene_id via list_l2
    let all_topics = db
        .list_l2(TopicListQuery {
            page: 1,
            page_size: 100,
            active_only: false,
            keyword: None,
        })
        .unwrap();
    let scene_nodes: Vec<_> = all_topics
        .items
        .iter()
        .filter(|t| t.scene_id == expected_scene_hash && t.depth == 1)
        .collect();
    // 1 auto-created topic + 3 turn nodes = 4 depth-1 nodes in this scene
    assert!(
        scene_nodes.len() >= 3,
        "should have at least 3 depth-1 nodes in scene, got {}",
        scene_nodes.len()
    );
}

// ============================================================================
// AC-6: 场景树查询
// ============================================================================

#[test]
fn test_ac6_scene_tree_query() {
    let dir = TempDir::new().unwrap();
    let path = dir.path().join("ac6.meh");
    let mut config = MemHopConfig::new(path, 768);
    config.encoder_grpc_addr = None;
    let mut db = MemHop::open(config).unwrap();

    // Create a topic and add 3 turn nodes
    let search_res = db
        .search_context(SearchQuery {
            dialogue: "scene tree test".to_string(),
            l2_id: None,
            l3_id: None,
            auto_create: true,
        })
        .unwrap();
    let topic_id = search_res.contexts[0].id.clone();
    let scene_id = format!("scene_{}", topic_id);

    for i in 0..3 {
        std::thread::sleep(std::time::Duration::from_millis(2));
        db.update_memory(UpdateRequest {
            topic_id: topic_id.clone(),
            dialogue_text: format!("Turn {} content", i),
            summary: Some(format!("Turn {} summary for tree test", i)),
            action_chain: None,
            instant_distill: false,
            source: Default::default(),
            scene_id: Some(scene_id.clone()),
            user_keywords: None,
            agent_keywords: None,
        })
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
    // (auto-created topic and turn nodes are siblings, no hierarchy)
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
