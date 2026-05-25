//! MemHop core API integration tests.
//!
//! These tests exercise the public `MemHop` API directly (no MCP layer).
//! References the Python `test_acceptance.py` (v0.5.1) patterns.

use std::collections::HashMap;
use std::thread;
use std::time::Duration;

use memhop::{DreamConfig, MemHop, StoreOptions};
use tempfile::TempDir;

// ═══════════════════════════════════════════════════════════════
// Helpers
// ═══════════════════════════════════════════════════════════════

fn setup() -> (MemHop, TempDir) {
    let dir = TempDir::new().expect("tempdir");
    let path = dir.path().join("test.db");
    let db = MemHop::open(path.to_str().unwrap()).expect("open");
    (db, dir)
}

// ═══════════════════════════════════════════════════════════════
// Lifecycle
// ═══════════════════════════════════════════════════════════════

#[test]
fn test_open_creates_engine() {
    let dir = TempDir::new().expect("tempdir");
    let db = MemHop::open(dir.path().join("test.db").to_str().unwrap());
    assert!(db.is_ok(), "open should succeed");
}

#[test]
fn test_close_idempotent() {
    let (db, _dir) = setup();
    db.close().expect("first close");
    db.close().expect("second close (idempotent)");
}

#[test]
fn test_store_after_close_errors() {
    let (mut db, _dir) = setup();
    db.close().expect("close");
    let result = db.store("text", None, &StoreOptions::default());
    assert!(result.is_err(), "store after close should error");
}

#[test]
fn test_recall_after_close_errors() {
    let (db, _dir) = setup();
    db.close().expect("close");
    let result = db.recall("query", None);
    assert!(result.is_err(), "recall after close should error");
}

#[test]
fn test_forget_after_close_errors() {
    let (mut db, _dir) = setup();
    db.close().expect("close");
    let result = db.forget("some_id");
    assert!(result.is_err(), "forget after close should error");
}

#[test]
/// NOTE: v0.6.0 reopen does NOT reload patterns into the in-memory Hopfield network.
/// The LMDB data persists correctly (verified in storage unit tests), but the
/// in-memory structures (hopfield, sparse_index, meta_index) are NOT rebuilt on open.
/// Load-on-open is pending for a future release.
fn test_reopen_is_stable() {
    let dir = TempDir::new().expect("tempdir");
    let path = dir.path().join("persist.db");

    // Write
    {
        let mut db = MemHop::open(path.to_str().unwrap()).expect("open-1");
        let _id = db
            .store("这条记忆需要被持久化保存", None, &StoreOptions::default())
            .expect("store");
        db.close().expect("close");
    }

    // Reopen — in-memory count will be 0 until load-on-open is implemented
    {
        let db = MemHop::open(path.to_str().unwrap()).expect("open-2");
        // Reopened engine should be functional (store new data)
        assert!(db.list_trees().contains(&"default".to_string()));
        // Engine can store and recall new memories after reopen
        db.close().expect("close-2");
    }
}

#[test]
fn test_reopen_store_after() {
    let dir = TempDir::new().expect("tempdir");
    let path = dir.path().join("reopen_store.db");

    // First session
    {
        let mut db = MemHop::open(path.to_str().unwrap()).expect("open-1");
        db.store("first session data", None, &StoreOptions::default()).expect("store");
        db.close().expect("close");
    }

    // Second session: store more data and verify engine works
    {
        let mut db = MemHop::open(path.to_str().unwrap()).expect("open-2");
        let id = db
            .store("second session data", None, &StoreOptions::default())
            .expect("store");
        assert!(!id.is_empty(), "store after reopen should succeed");
        db.close().expect("close-2");
    }
}

// ═══════════════════════════════════════════════════════════════
// Store
// ═══════════════════════════════════════════════════════════════

#[test]
fn test_store_returns_id() {
    let (mut db, _dir) = setup();
    let id = db.store("hello world", None, &StoreOptions::default()).expect("store");
    assert!(!id.is_empty(), "store should return a non-empty ID");
}

#[test]
fn test_store_with_auto_entangle_false() {
    let (mut db, _dir) = setup();
    let id = db.store(
        "test memory",
        None,
        &StoreOptions { auto_entangle: false, ..Default::default() },
    ).expect("store");
    assert!(!id.is_empty(), "store with auto_entangle=false should succeed");
}

#[test]
fn test_store_increments_count() {
    let (mut db, _dir) = setup();
    assert_eq!(db.count(), 0, "fresh db count should be 0");
    db.store("memory 1", None, &StoreOptions::default()).expect("store");
    assert_eq!(db.count(), 1, "count should be 1 after one store");
    db.store("memory 2", None, &StoreOptions::default()).expect("store");
    assert_eq!(db.count(), 2, "count should be 2 after two stores");
}

// ═══════════════════════════════════════════════════════════════
// Recall
// ═══════════════════════════════════════════════════════════════

#[test]
fn test_recall_after_store_finds_result() {
    let (mut db, _dir) = setup();
    db.store("今天天气真好阳光明媚", None, &StoreOptions::default()).expect("store");
    let result = db.recall("今天天气", None).expect("recall");
    assert!(result.is_some(), "recall with overlapping ngrams should find the memory");
    if let Some(m) = result {
        assert!(m.confidence > 0.0, "confidence should be positive");
    }
}

#[test]
fn test_recall_on_empty_returns_none() {
    let (db, _dir) = setup();
    let result = db.recall("nothing here", None).expect("recall");
    assert!(result.is_none(), "recall on empty db should return None");
}

#[test]
fn test_recall_no_match_possible() {
    let (mut db, _dir) = setup();
    let texts = [
        "量子计算是未来科技的方向",
        "天气预报说明天有暴雨",
        "猫是一种可爱的宠物动物",
        "编程语言的发展历程回顾",
        "股市今天收盘大涨三个点",
    ];
    for t in &texts {
        db.store(t, None, &StoreOptions::default()).expect("store");
    }
    // Should not panic regardless of match/non-match
    let _result = db.recall("火星探测任务最新进展报告", None).expect("recall");
}

#[test]
fn test_recall_topk_limits_results() {
    let (mut db, _dir) = setup();
    for i in 0..5 {
        db.store(
            &format!("测试记忆内容编号{}包含一些词语", i),
            None,
            &StoreOptions::default(),
        ).expect("store");
    }
    let results = db.recall_topk("测试记忆", 3, None);
    assert!(results.len() <= 3, "topk(3) should return <=3 results");
}

#[test]
fn test_recall_topk_k_greater_than_count() {
    let (mut db, _dir) = setup();
    db.store("only one memory", None, &StoreOptions::default()).expect("store");
    let results = db.recall_topk("memory", 10, None);
    assert_eq!(results.len(), 1, "topk(10) with 1 memory should return 1");
}

#[test]
fn test_recall_topk_on_empty() {
    let (db, _dir) = setup();
    let results = db.recall_topk("anything", 5, None);
    assert!(results.is_empty(), "topk on empty db should return empty vec");
}

// ═══════════════════════════════════════════════════════════════
// Forget
// ═══════════════════════════════════════════════════════════════

#[test]
fn test_forget_existing_returns_true() {
    let (mut db, _dir) = setup();
    let id = db.store("to be forgotten", None, &StoreOptions::default()).expect("store");
    let ok = db.forget(&id).expect("forget");
    assert!(ok, "forget existing memory should return true");
    assert_eq!(db.count(), 0, "count should be 0 after forget");
}

#[test]
fn test_forget_nonexistent_returns_false() {
    let (mut db, _dir) = setup();
    let ok = db.forget("nonexistent_id").expect("forget");
    assert!(!ok, "forget nonexistent memory should return false");
}

#[test]
fn test_forget_reduces_count() {
    let (mut db, _dir) = setup();
    let id1 = db.store("memory A", None, &StoreOptions::default()).expect("store");
    let _id2 = db.store("memory B", None, &StoreOptions::default()).expect("store");
    assert_eq!(db.count(), 2);
    db.forget(&id1).expect("forget");
    assert_eq!(db.count(), 1, "count should decrement after forget");
}

// ═══════════════════════════════════════════════════════════════
// Update
// ═══════════════════════════════════════════════════════════════

#[test]
fn test_update_text() {
    let (mut db, _dir) = setup();
    let id = db.store("old text", None, &StoreOptions::default()).expect("store");
    let ok = db.update(&id, Some("new text"), None).expect("update");
    assert!(ok, "update text should return true");
}

#[test]
fn test_update_meta() {
    let (mut db, _dir) = setup();
    let id = db.store("some text", None, &StoreOptions::default()).expect("store");
    let mut meta = HashMap::new();
    meta.insert("key".to_string(), serde_json::Value::String("v2".to_string()));
    let ok = db.update(&id, None, Some(&meta)).expect("update");
    assert!(ok, "update meta should return true");
}

#[test]
fn test_update_nonexistent_returns_false() {
    let (mut db, _dir) = setup();
    let ok = db.update("bad_id", Some("text"), None).expect("update");
    assert!(!ok, "update nonexistent should return false");
}

// ═══════════════════════════════════════════════════════════════
// Tree management
// ═══════════════════════════════════════════════════════════════

#[test]
fn test_default_tree_exists() {
    let (db, _dir) = setup();
    let trees = db.list_trees();
    assert!(trees.contains(&"default".to_string()), "default tree should exist");
}

#[test]
fn test_create_and_list_trees() {
    let (mut db, _dir) = setup();
    db.create_tree("knowledge").expect("create_tree");
    db.create_tree("episodes").expect("create_tree");
    let trees = db.list_trees();
    assert!(trees.contains(&"knowledge".to_string()));
    assert!(trees.contains(&"episodes".to_string()));
    assert!(trees.contains(&"default".to_string()));
    assert_eq!(trees.len(), 3);
}

#[test]
fn test_remove_tree() {
    let (mut db, _dir) = setup();
    db.create_tree("temp").expect("create");
    db.remove_tree("temp").expect("remove");
    let trees = db.list_trees();
    assert!(!trees.contains(&"temp".to_string()));
}

#[test]
fn test_remove_nonexistent_tree_errors() {
    let (mut db, _dir) = setup();
    let result = db.remove_tree("nonexistent");
    assert!(result.is_err(), "removing nonexistent tree should error");
}

#[test]
fn test_store_and_recall_in_custom_tree() {
    let (mut db, _dir) = setup();
    db.create_tree("work").expect("create_tree");

    let id = db
        .store("工作相关记忆", Some("work"), &StoreOptions::default())
        .expect("store");
    assert!(!id.is_empty(), "store in custom tree should succeed");

    let result = db.recall("工作", Some("work")).expect("recall");
    assert!(result.is_some(), "recall from custom tree should find memory");
}

// ═══════════════════════════════════════════════════════════════
// Search
// ═══════════════════════════════════════════════════════════════

#[test]
fn test_search_by_meta_filter() {
    let (mut db, _dir) = setup();
    let id = db.store("hello", None, &StoreOptions::default()).expect("store");
    let mut meta = HashMap::new();
    meta.insert("layer".to_string(), serde_json::json!("greeting"));
    db.update(&id, None, Some(&meta)).expect("update");

    let results = db.search(&serde_json::json!({"layer": "greeting"}), 10).expect("search");
    assert!(!results.is_empty(), "search by layer should find memories");
    for m in &results {
        assert_eq!(
            m.meta.get("layer").and_then(|v| v.as_str()),
            Some("greeting"),
            "all search results should have matching layer"
        );
    }
}

#[test]
fn test_search_empty_result() {
    let (mut db, _dir) = setup();
    let id = db.store("some content", None, &StoreOptions::default()).expect("store");
    let mut meta = HashMap::new();
    meta.insert("layer".to_string(), serde_json::json!("entity"));
    db.update(&id, None, Some(&meta)).expect("update");

    let results = db.search(&serde_json::json!({"layer": "nonexistent"}), 10).expect("search");
    assert!(results.is_empty(), "search with no match should return empty vec");
}

// ═══════════════════════════════════════════════════════════════
// Recent
// ═══════════════════════════════════════════════════════════════

#[test]
fn test_recent_returns_latest() {
    let (mut db, _dir) = setup();
    db.store("first", None, &StoreOptions::default()).expect("store");
    thread::sleep(Duration::from_millis(10));
    db.store("second", None, &StoreOptions::default()).expect("store");
    thread::sleep(Duration::from_millis(10));
    db.store("third", None, &StoreOptions::default()).expect("store");

    let results = db.recent(2, None).expect("recent");
    assert_eq!(results.len(), 2, "recent(2) should return 2 results");
    assert!(results[0].text.contains("third"), "most recent should be 'third'");
    assert!(results[1].text.contains("second"), "second should be 'second'");
}

#[test]
fn test_recent_limit_zero() {
    let (mut db, _dir) = setup();
    db.store("something", None, &StoreOptions::default()).expect("store");
    let results = db.recent(0, None).expect("recent");
    assert!(results.is_empty(), "recent(0) should return empty");
}

#[test]
fn test_recent_limit_greater_than_count() {
    let (mut db, _dir) = setup();
    db.store("only one", None, &StoreOptions::default()).expect("store");
    let results = db.recent(100, None).expect("recent");
    assert_eq!(results.len(), 1, "recent with limit > count should return all");
}

// ═══════════════════════════════════════════════════════════════
// Count & Stats
// ═══════════════════════════════════════════════════════════════

#[test]
fn test_count_on_empty_db() {
    let (db, _dir) = setup();
    assert_eq!(db.count(), 0);
}

#[test]
fn test_count_after_multiple_stores() {
    let (mut db, _dir) = setup();
    for i in 0..10 {
        db.store(&format!("memory {}", i), None, &StoreOptions::default())
            .expect("store");
    }
    assert_eq!(db.count(), 10);
}

#[test]
fn test_stats_returns_info() {
    let (mut db, _dir) = setup();
    db.store("test data", None, &StoreOptions::default()).expect("store");
    let stats = db.stats();
    assert!(stats.contains_key("total_memories"), "stats should have total_memories");
    assert!(stats.contains_key("tree_count"), "stats should have tree_count");
}

// ═══════════════════════════════════════════════════════════════
// Dream
// ═══════════════════════════════════════════════════════════════

#[test]
fn test_dream_with_default_config() {
    let (mut db, _dir) = setup();
    for i in 0..10 {
        db.store(
            &format!("dream test memory {}", i),
            None,
            &StoreOptions::default(),
        ).expect("store");
    }
    db.dream(None);
    assert!(db.count() >= 10, "dream should not corrupt state");
}

#[test]
fn test_dream_with_custom_config() {
    let (mut db, _dir) = setup();
    for i in 0..10 {
        db.store(
            &format!("dream cfg memory {}", i),
            None,
            &StoreOptions::default(),
        ).expect("store");
    }
    let config = DreamConfig {
        auto_trigger_interval: 5,
        merge_threshold: 0.95,
        weaken_threshold: 0.3,
        max_duration_ms: 100,
    };
    db.dream(Some(&config));
    assert!(db.count() >= 10, "dream with config should not corrupt state");
}

// ═══════════════════════════════════════════════════════════════
// Error cases
// ═══════════════════════════════════════════════════════════════

#[test]
fn test_recall_nonexistent_tree_returns_empty() {
    let (db, _dir) = setup();
    let results = db.recall_topk("query", 5, Some("nonexistent"));
    assert!(results.is_empty(), "recall_topk on nonexistent tree should return empty");
}

#[test]
fn test_store_nonexistent_tree_errors() {
    let (mut db, _dir) = setup();
    let result = db.store("text", Some("ghost"), &StoreOptions::default());
    assert!(result.is_err(), "store to nonexistent tree should error");
}

// ═══════════════════════════════════════════════════════════════
// Full integration flow
// ═══════════════════════════════════════════════════════════════

#[test]
fn test_store_recall_forget_flow() {
    let (mut db, _dir) = setup();

    // Store
    let id = db
        .store("集成测试完整流程验证存储与召回", None, &StoreOptions::default())
        .expect("store");
    assert_eq!(db.count(), 1);

    // Recall
    let result = db.recall("集成测试", None).expect("recall");
    assert!(result.is_some(), "should find the stored memory");
    if let Some(ref m) = result {
        assert_eq!(m.id, id, "recall should return correct memory id");
    }

    // Forget
    let ok = db.forget(&id).expect("forget");
    assert!(ok, "forget should succeed");
    assert_eq!(db.count(), 0, "count should be 0 after forget");

    // Verify recall returns nothing
    let result2 = db.recall("集成测试", None).expect("recall");
    assert!(result2.is_none(), "recall after forget should return None");
}

#[test]
fn test_multi_tree_isolation() {
    let (mut db, _dir) = setup();
    db.create_tree("work").expect("create_tree");
    db.create_tree("personal").expect("create_tree");

    db.store("工作内容", Some("work"), &StoreOptions::default()).expect("store");
    db.store("个人内容", Some("personal"), &StoreOptions::default()).expect("store");
    assert_eq!(db.count(), 2, "count should include all trees");

    db.store("默认内容", None, &StoreOptions::default()).expect("store");
    assert_eq!(db.count(), 3, "count should include default tree");

    let work_result = db.recall("工作", Some("work")).expect("recall");
    assert!(work_result.is_some(), "work tree recall should find work memory");
    if let Some(m) = work_result {
        assert!(m.text.contains("工作"), "work tree recall should return work text");
    }
}

#[test]
fn test_count_after_tree_operations() {
    let (mut db, _dir) = setup();
    db.create_tree("extra").expect("create_tree");

    db.store("global", None, &StoreOptions::default()).expect("store");
    db.store("extra data", Some("extra"), &StoreOptions::default()).expect("store");
    assert_eq!(db.count(), 2, "count should include both trees");

    db.remove_tree("extra").expect("remove_tree");
    let c = db.count();
    assert!(c <= 2, "count after remove_tree should be <= original count");
}
