// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L2 Data Structure Refactoring Tests
//!
//! Covers the ContextSlot → TopicSlot rename, new SceneSlot CRUD,
//! and merge_nodes scene-reassignment API.

use memhop::{LlmConfig, MemHop, MemHopConfig, SearchQuery, TopicListQuery, UpdateRequest};
use std::path::PathBuf;

fn test_config(db_path: &str) -> MemHopConfig {
    MemHopConfig {
        db_path: PathBuf::from(db_path),
        encoder_grpc_addr: None,
        vector_dim: 768,
        crystal_path: None,
        llm: LlmConfig::default(),
        auto_dream_on_evict: false,
        auto_dream_archive_threshold: 20,
        auto_dream_summary_bytes: 2048,
        ivf_initial_k: 16,
        search_weights: None,
        decay_config: None,
        session_config: None,
        dream_idle_threshold_secs: None,
        auto_checkpoint_interval: None,
        adjacency_cache_max_entries: 128,
        llm_preprocess: memhop::LlmPreprocessConfig::default(),
    }
}

// ============================================================================
// Scene: create → assign L2 nodes → verify via scene tree
// ============================================================================

#[test]
fn test_scene_with_topic_nodes() {
    let db_path = "/tmp/memhop_scene_topics.meh";
    let _ = std::fs::remove_file(db_path);

    let config = test_config(db_path);
    let mut db = MemHop::open(config.clone()).expect("open failed");

    // Create a topic directly (scene CRUD removed in v0.57+)
    let search_res = db
        .search_context(SearchQuery {
            dialogue: "scene test topic".to_string(),
            l2_id: None,
            l3_id: None,
            auto_create: true,
        })
        .expect("search_context failed");
    let topic_id = search_res.contexts[0].id.clone();
    let scene_id_hex = format!("scene_{}", topic_id);

    // Write two turns with this scene
    for i in 0..2 {
        std::thread::sleep(std::time::Duration::from_millis(2));
        db.update_memory(UpdateRequest {
            topic_id: topic_id.clone(),
            dialogue_text: format!("Turn {} in scene", i),
            summary: None,
            action_chain: None,
            instant_distill: false,
            scene_id: Some(scene_id_hex.clone()),
            source: Default::default(),
            user_keywords: None,
            agent_keywords: None,
        })
        .expect("update_memory failed");
    }

    // Query scene tree — should find the turn nodes
    let tree = db.list_scene_tree(&scene_id_hex).expect("list_scene_tree");
    assert!(
        tree.total_turns >= 2,
        "scene tree should have >= 2 nodes, got {}",
        tree.total_turns
    );

    drop(db);
    let _ = std::fs::remove_file(db_path);
}

// ============================================================================
// TopicSlot v4 fields: write dialogue and verify persistence
// ============================================================================

#[test]
fn test_topic_slot_v4_fields_persist() {
    let db_path = "/tmp/memhop_v4_fields.meh";
    let _ = std::fs::remove_file(db_path);

    let config = test_config(db_path);
    let mut db = MemHop::open(config.clone()).expect("open failed");

    // Create topic + write dialogue
    let search_res = db
        .search_context(SearchQuery {
            dialogue: "v4 fields test".to_string(),
            l2_id: None,
            l3_id: None,
            auto_create: true,
        })
        .expect("search_context failed");
    let topic_id = search_res.contexts[0].id.clone();

    db.update_memory(UpdateRequest {
        topic_id: topic_id.clone(),
        dialogue_text: "User: hello\nAssistant: hi there".to_string(),
        summary: Some("greeting exchange".to_string()),
        action_chain: None,
        instant_distill: false,
        scene_id: None,
        user_keywords: None,
        agent_keywords: None,
        source: Default::default(),
    })
    .expect("update_memory failed");

    // Sync (checkpoint) and reopen to verify persistence
    drop(db);

    let db2 = MemHop::open(config).expect("reopen");
    let topics = db2
        .list_l2(TopicListQuery {
            page: 1,
            page_size: 100,
            active_only: false,
            keyword: None,
        })
        .expect("list_l2");

    // auto-created topic + 1 turn node = at least 2
    assert!(
        topics.total >= 2,
        "should have >= 2 L2 nodes after write, got {}",
        topics.total
    );

    // All nodes should have depth=1
    for item in &topics.items {
        assert_eq!(item.depth, 1, "all nodes should be depth=1");
    }

    drop(db2);
    let _ = std::fs::remove_file(db_path);
}
