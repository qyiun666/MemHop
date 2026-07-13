//! Minimal search diagnostic API smoke test.
//!
//! Verifies that the API surface compiles and basic operations don't panic.
//! Requires a running gRPC encoder to execute at runtime.

mod common;

use memhop::{LlmConfig, MemHop, MemHopConfig, SearchQuery};
use tempfile::TempDir;

fn llm_config_from_env() -> LlmConfig {
    let api_key = std::env::var("MEMHOP_LLM_API_KEY").unwrap_or_default();
    let api_url = std::env::var("MEMHOP_LLM_API_URL")
        .unwrap_or_else(|_| "https://api.deepseek.com/v1/chat/completions".to_string());
    let model = std::env::var("MEMHOP_LLM_MODEL").unwrap_or_else(|_| "deepseek-chat".to_string());
    LlmConfig::new(api_url, api_key, model, "zh".to_string())
}

#[test]
fn test_basic_search_smoke() {
    let port = common::ensure_encoder_running();
    let dir = TempDir::new().unwrap();
    let path = dir.path().join("diag.meh");
    let mut config = MemHopConfig::new(path, 768);
    config.encoder_grpc_addr = format!("http://127.0.0.1:{}", port);
    config.llm = llm_config_from_env();
    let mut db = MemHop::open(config).unwrap();

    let result = db.search(SearchQuery {
        query: "smoke test".into(),
        layers: vec![2],
        max_results: 5,
        min_score: 0.0,
        include_profile: false,
        filters: None,
        directed_l2_id: None,
        directed_l3_id: None,
        auto_create: None,
    });
    // Should not panic
    let _ = result;

    db.close().unwrap();
}
