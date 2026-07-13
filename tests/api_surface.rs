// Compile-time + runtime smoke test for the public APIs.
//
// This test verifies that every API listed in API.md is reachable from an
// external crate and accepts the documented argument types.

mod common;

use memhop::{
    ArchiveQuery, LlmConfig, MemHop, MemHopConfig, ProfileResult, SearchQuery, SearchResult,
    TopicListQuery, UpdateL2Fields, UpdateL3Fields, UpdateL5Fields, UpdateRequest, UpdateResult,
};
use std::collections::HashMap;

fn llm_config_from_env() -> LlmConfig {
    let api_key = std::env::var("MEMHOP_LLM_API_KEY").unwrap_or_default();
    let api_url = std::env::var("MEMHOP_LLM_API_URL")
        .unwrap_or_else(|_| "https://api.deepseek.com/v1/chat/completions".to_string());
    let model = std::env::var("MEMHOP_LLM_MODEL").unwrap_or_else(|_| "deepseek-chat".to_string());
    LlmConfig::new(api_url, api_key, model, "zh".to_string())
}

fn make_config(path: std::path::PathBuf) -> MemHopConfig {
    let port = common::ensure_encoder_running();
    let mut config = MemHopConfig::new(path, 768);
    config.encoder_grpc_addr = format!("http://127.0.0.1:{}", port);
    config.llm = llm_config_from_env();
    config
}

#[test]
#[ignore = "requires LLM API key (MEMHOP_LLM_API_KEY)"]
fn api_surface_is_reachable() {
    let dir = tempfile::TempDir::new().unwrap();
    let path = dir.path().join("surface.meh");

    // API-1 open
    let mut db = MemHop::open(make_config(path)).unwrap();

    // API-2 search
    let _: SearchResult = db
        .search(SearchQuery {
            query: "hello".into(),
            layers: vec![2],
            max_results: 10,
            min_score: 0.0,
            include_profile: false,
            filters: None,
            directed_l2_id: None,
            directed_l3_id: None,
            auto_create: None,
        })
        .unwrap_or_else(|_| SearchResult {
            profile: None,
            contexts: vec![],
            associated_contexts: vec![],
            l3_ids: vec![],
            l1_previews: vec![],
        });

    // API-3 get_profile
    let _: Option<ProfileResult> = db.get_profile().unwrap();

    // API-4 update profile via generic update
    let mut fields = HashMap::new();
    fields.insert(
        "dialogue_text".to_string(),
        serde_json::Value::String("test dialogue".into()),
    );
    fields.insert(
        "name".to_string(),
        serde_json::Value::String("test-agent".into()),
    );
    let _: UpdateResult = db
        .update_memory(UpdateRequest {
            id: "profile_test".into(),
            layer: 2,
            fields,
        })
        .unwrap();

    // API-5 L2 CRUD
    let _ = db.list_l2(TopicListQuery {
        page: 1,
        page_size: 10,
        active_only: false,
        keyword: None,
    });

    // API-6 update_l2 / delete_turn accept valid but non-existent IDs
    let _ = db.update_l2(
        "0000000000000001",
        UpdateL2Fields {
            ..Default::default()
        },
    );
    let _ = db.delete_turn("0000000000000001", 0..0);

    // API-7 L3 CRUD
    let _ = db.get_l3("0000000000000003");
    let _ = db.update_l3(
        "0000000000000003",
        UpdateL3Fields {
            name: Some("k".into()),
        },
    );
    let _ = db.delete_l3("0000000000000003");

    // API-8 L4 archive query
    let _ = db.query_archives(ArchiveQuery {
        page: 1,
        page_size: 10,
        topic_id: None,
        keyword: None,
        time_range: None,
    });

    // API-9 L5 CRUD
    let _ = db.get_l5("0000000000000006");
    let _ = db.update_l5(
        "0000000000000006",
        UpdateL5Fields {
            title: Some("c".into()),
            ..Default::default()
        },
    );
    let _ = db.delete_l5("0000000000000006");

    // API-10 get / set / update profile
    // set_profile and update_profile removed in v0.57+

    // API-11 L1 graph
    let _ = db.get_l1_graph(None);

    // API-12 dream
    let _ = db.dream(None);

    // API-13 close
    db.close().unwrap();
}
