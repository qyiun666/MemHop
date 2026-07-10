// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L2 Data Structure Refactoring Tests
//!
//! Covers the ContextSlot → TopicSlot rename, new SceneSlot CRUD,
//! and merge_nodes scene-reassignment API.

use memhop::{
    LlmConfig, MemHop, MemHopConfig, MergeNodesRequest, SearchQuery, TopicListQuery, UpdateRequest,
};
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
// Scene CRUD: create / get / list
// ============================================================================

#[test]
fn test_scene_create_get_list() {
    let db_path = "/tmp/memhop_scene_crud.meh";
    let _ = std::fs::remove_file(db_path);

    let config = test_config(db_path);
    let mut db = MemHop::open(config.clone()).expect("open failed");

    // Create two scenes
    let id_a = db.create_scene("coding").expect("create_scene coding");
    let id_b = db.create_scene("travel").expect("create_scene travel");
    assert_ne!(id_a, id_b, "different names produce different IDs");

    // Idempotent
    let id_a2 = db
        .create_scene("coding")
        .expect("create_scene coding again");
    assert_eq!(id_a, id_a2, "create_scene should be idempotent");

    // get_scene — "coding" is 6 chars, parse_id_to_hash will hash_id it
    let scene = db.get_scene("coding").expect("get_scene");
    assert!(scene.is_some(), "scene 'coding' should exist");
    let (sid, name) = scene.unwrap();
    assert_eq!(sid, id_a);
    assert_eq!(name, "coding");

    // get_scene nonexistent
    let missing = db
        .get_scene("nonexistent_scene")
        .expect("get_scene missing");
    assert!(missing.is_none(), "nonexistent scene should return None");

    // list_scenes
    let all = db.list_scenes().expect("list_scenes");
    assert_eq!(all.len(), 2, "should have 2 scenes");
    let names: Vec<&str> = all.iter().map(|(_, n)| n.as_str()).collect();
    assert!(names.contains(&"coding"));
    assert!(names.contains(&"travel"));

    drop(db);
    let _ = std::fs::remove_file(db_path);
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

    // Create a scene and get its hex ID
    let scene_id = db.create_scene("test_scene").expect("create_scene");
    let scene_id_hex = format!("{:016x}", scene_id);

    // Create a topic
    let search_res = db
        .search_context(SearchQuery {
            dialogue: "scene test topic".to_string(),
            l2_id: None,
            context_id: None,
            l3_id: None,
            context_limit: 5,
            auto_create: 1,
            min_score: 0.0,
            source: Default::default(),
            llm_keywords: None,
            enable_llm_preprocess: false,
        })
        .expect("search_context failed");
    let topic_id = search_res.contexts[0].id.clone();

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
// merge_nodes: scene reassignment
// ============================================================================

#[test]
fn test_merge_nodes_scene_reassignment() {
    let db_path = "/tmp/memhop_merge_nodes.meh";
    let _ = std::fs::remove_file(db_path);

    let config = test_config(db_path);
    let mut db = MemHop::open(config.clone()).expect("open failed");

    // Create two scenes
    let main_id = db.create_scene("main_scene").expect("create main");
    let sec_id = db.create_scene("sec_scene").expect("create secondary");
    let main_hex = format!("{:016x}", main_id);
    let sec_hex = format!("{:016x}", sec_id);

    // Create topic and assign turns to secondary scene
    let search_res = db
        .search_context(SearchQuery {
            dialogue: "merge nodes test".to_string(),
            l2_id: None,
            context_id: None,
            l3_id: None,
            context_limit: 5,
            auto_create: 1,
            min_score: 0.0,
            source: Default::default(),
            llm_keywords: None,
            enable_llm_preprocess: false,
        })
        .expect("search_context failed");
    let topic_id = search_res.contexts[0].id.clone();

    for i in 0..3 {
        std::thread::sleep(std::time::Duration::from_millis(2));
        db.update_memory(UpdateRequest {
            topic_id: topic_id.clone(),
            dialogue_text: format!("Secondary turn {}", i),
            summary: None,
            action_chain: None,
            instant_distill: false,
            scene_id: Some(sec_hex.clone()),
            user_keywords: None,
            agent_keywords: None,
            source: Default::default(),
        })
        .expect("update_memory failed");
    }

    // Verify secondary scene has nodes
    let tree_before = db.list_scene_tree(&sec_hex).expect("list_scene_tree");
    let nodes_before = tree_before.total_turns;
    assert!(nodes_before >= 3, "secondary scene should have >= 3 nodes");

    // Merge secondary → main
    let result = db
        .merge_nodes(MergeNodesRequest {
            main_scene_id: main_hex.clone(),
            secondary_scene_ids: vec![sec_hex.clone()],
        })
        .expect("merge_nodes");
    assert!(
        result.merged_node_count >= 3,
        "should merge >= 3 nodes, got {}",
        result.merged_node_count
    );

    // Secondary scene should now be empty
    let tree_after = db
        .list_scene_tree(&sec_hex)
        .expect("list_scene_tree after merge");
    assert_eq!(
        tree_after.total_turns, 0,
        "secondary scene should be empty after merge"
    );

    // Main scene should have the nodes
    let main_tree = db.list_scene_tree(&main_hex).expect("list_scene_tree main");
    assert!(
        main_tree.total_turns >= 3,
        "main scene should have inherited the nodes"
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
            context_id: None,
            l3_id: None,
            context_limit: 5,
            auto_create: 1,
            min_score: 0.0,
            source: Default::default(),
            llm_keywords: None,
            enable_llm_preprocess: false,
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

    // Sync and reopen to verify persistence
    db.sync().expect("sync");
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
