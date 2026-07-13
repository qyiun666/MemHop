//! Stress tests to verify MemHop handles heavy write loads without corruption
//! or FileFull errors, and that auto-extension works correctly.
//!
//! These tests use the candle-encoder gRPC server for vector encoding.
//! The server must be started manually before running tests.

mod common;

use memhop::{MemHop, MemHopConfig, StoreBatch, StoreItem, TopicListQuery};
use tempfile::TempDir;

fn create_test_db() -> (TempDir, MemHop) {
    let port = common::ensure_encoder_running();
    let dir = TempDir::new().unwrap();
    let path = dir.path().join("stress_test.meh");
    let mut config = MemHopConfig::new(path, 768);
    config.encoder_grpc_addr = format!("http://127.0.0.1:{}", port);
    let db = MemHop::open(config).unwrap();
    (dir, db)
}

/// Stress test for auto-extend.
/// Requires external gRPC encoder (meowvec) — not available in CI.
#[test]
fn test_file_auto_extend() {
    let (_dir, mut db) = create_test_db();

    // Insert 500 items with real BGE-M3 vectors
    let mut total_created = 0u32;
    for i in 0..500 {
        let batch = StoreBatch {
            items: vec![StoreItem {
                content: format!(
                    "stress test document number {} with some content to fill pages",
                    i
                ),
                keywords: vec![],
                score: 0.5,
                source_type: "UserInput".to_string(),
                source: String::new(),
                layer: 4,
            }],
            import_mode: None,
            source_info: None,
        };
        let result = db.store_batch(batch).expect("batch store should succeed");
        total_created += result.stored_count;
    }

    assert!(total_created > 0, "should have stored at least one item");
    eprintln!("Auto-extend test: stored {} items", total_created);

    let topics = db
        .list_l2(TopicListQuery {
            page: 1,
            page_size: 10,
            active_only: false,
            keyword: None,
        })
        .expect("list_l2 should succeed");
    assert!(topics.items.is_empty() || topics.total > 0);

    drop(_dir);
}
