// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L2 Data Structure Refactoring Tests
//!
//! Tests for L2 CRUD operations with the v0.57+ API.
//! All integration tests require a running gRPC encoder.

mod common;

use memhop::{LlmConfig, MemHop, MemHopConfig, SearchQuery, TopicListQuery};
use std::collections::HashMap;
use std::path::PathBuf;

fn llm_config_from_env() -> LlmConfig {
    let api_key = std::env::var("MEMHOP_LLM_API_KEY").unwrap_or_default();
    let api_url = std::env::var("MEMHOP_LLM_API_URL")
        .unwrap_or_else(|_| "https://api.deepseek.com/v1/chat/completions".to_string());
    let model = std::env::var("MEMHOP_LLM_MODEL").unwrap_or_else(|_| "deepseek-chat".to_string());
    LlmConfig::new(api_url, api_key, model, "zh".to_string())
}

fn test_config(db_path: &str) -> MemHopConfig {
    let port = common::ensure_encoder_running();
    MemHopConfig {
        db_path: PathBuf::from(db_path),
        encoder_grpc_addr: format!("http://127.0.0.1:{}", port),
        vector_dim: 768,
        crystal_path: None,
        llm: llm_config_from_env(),
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
// L2 CRUD: open, list_l2, close
// ============================================================================

#[test]
fn test_l2_crud_basic() {
    let db_path = "/tmp/memhop_l2_crud.meh";
    let _ = std::fs::remove_file(db_path);

    let config = test_config(db_path);
    let mut db = MemHop::open(config.clone()).expect("open failed");

    // List empty DB
    let topics = db
        .list_l2(TopicListQuery {
            page: 1,
            page_size: 100,
            active_only: false,
            keyword: None,
        })
        .expect("list_l2 failed");
    assert!(topics.items.is_empty(), "fresh DB should have no topics");

    // get_l2 on non-existent returns error
    let result = db.get_l2("0000000000000001");
    assert!(result.is_err() || result.unwrap().is_none());

    // update_l2 on non-existent returns error
    let result = db.update_l2("0000000000000001", memhop::UpdateL2Fields::default());
    assert!(result.is_err());

    // delete_turn on non-existent returns error
    let result = db.delete_turn("0000000000000001", 0..0);
    assert!(result.is_err());

    drop(db);
    let _ = std::fs::remove_file(db_path);
}

// ============================================================================
// TopicListQuery: query parameters
// ============================================================================

#[test]
fn test_topic_list_query() {
    let db_path = "/tmp/memhop_list_query.meh";
    let _ = std::fs::remove_file(db_path);

    let config = test_config(db_path);
    let mut db = MemHop::open(config.clone()).expect("open failed");

    // List with active_only and keyword filters
    let topics = db
        .list_l2(TopicListQuery {
            page: 1,
            page_size: 10,
            active_only: true,
            keyword: Some("test".to_string()),
        })
        .expect("list_l2 with filters");
    assert!(topics.items.is_empty());

    drop(db);
    let _ = std::fs::remove_file(db_path);
}

// ============================================================================
// Scene: create → assign L2 nodes → verify via scene tree
// ============================================================================

#[test]
#[ignore = "requires LLM API key (MEMHOP_LLM_API_KEY)"]
fn test_scene_with_topic_nodes() {
    let db_path = "/tmp/memhop_scene_topics.meh";
    let _ = std::fs::remove_file(db_path);

    let config = test_config(db_path);
    let mut db = MemHop::open(config.clone()).expect("open failed");

    // Create a topic via auto-create search
    let search_res = db
        .search(SearchQuery {
            query: "scene test topic".to_string(),
            layers: vec![2],
            max_results: 20,
            min_score: 0.0,
            include_profile: false,
            filters: None,
            directed_l2_id: None,
            directed_l3_id: None,
            auto_create: Some(1),
        })
        .expect("search failed");
    let topic_id = search_res.contexts[0].id.clone();

    // Write two turns
    for i in 0..2 {
        std::thread::sleep(std::time::Duration::from_millis(2));
        let mut fields = HashMap::new();
        fields.insert(
            "dialogue_text".to_string(),
            serde_json::Value::String(format!("Turn {} in scene", i)),
        );
        fields.insert(
            "scene_id".to_string(),
            serde_json::Value::String(topic_id.clone()),
        );
        db.update_memory(memhop::UpdateRequest {
            id: topic_id.clone(),
            layer: 2,
            fields,
        })
        .expect("update_memory failed");
    }

    // Query scene tree — should find the turn nodes
    let tree = db.list_scene_tree(&topic_id).expect("list_scene_tree");
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
#[ignore = "requires LLM API key (MEMHOP_LLM_API_KEY)"]
fn test_topic_slot_v4_fields_persist() {
    let db_path = "/tmp/memhop_v4_fields.meh";
    let _ = std::fs::remove_file(db_path);

    let config = test_config(db_path);
    let mut db = MemHop::open(config.clone()).expect("open failed");

    // Create topic + write dialogue
    let search_res = db
        .search(SearchQuery {
            query: "v4 fields test".to_string(),
            layers: vec![2],
            max_results: 20,
            min_score: 0.0,
            include_profile: false,
            filters: None,
            directed_l2_id: None,
            directed_l3_id: None,
            auto_create: Some(1),
        })
        .expect("search failed");
    let topic_id = search_res.contexts[0].id.clone();

    let mut fields = HashMap::new();
    fields.insert(
        "dialogue_text".to_string(),
        serde_json::Value::String("User: hello\nAssistant: hi there".to_string()),
    );
    fields.insert(
        "summary".to_string(),
        serde_json::Value::String("greeting exchange".to_string()),
    );
    db.update_memory(memhop::UpdateRequest {
        id: topic_id.clone(),
        layer: 2,
        fields,
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
// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0
