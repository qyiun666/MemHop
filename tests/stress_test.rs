//! Stress tests to verify MemHop handles heavy write loads without corruption
//! or FileFull errors, and that auto-extension works correctly.
//!
//! These tests use the real BGE-M3 ONNX gRPC encoder (via meowvec_server.py/Python onnxruntime) for
//! authentic vector encoding. The server is started once per test binary.

mod common;

use memhop::{
    EngramListQuery, ImportData, ImportMode, ImportRequest, KnowledgeImportItem, MemHop,
    MemHopConfig, SourceMeta, SourceType, StoreBatch, StoreItem, TargetLayer, TopicListQuery,
};
use tempfile::TempDir;

/// Start the ORT encoder server once for all stress tests.
fn setup_encoder() {
    let _guard = common::ensure_python_meowvec(27110);
}

fn create_test_db() -> (TempDir, MemHop) {
    let dir = TempDir::new().unwrap();
    let path = dir.path().join("stress_test.meh");
    let mut config = MemHopConfig::new(path, 1024);
    config.encoder_grpc_addr = Some("http://127.0.0.1:27110".to_string());
    let db = MemHop::open(config).unwrap();
    (dir, db)
}

/// Test 1: Verify file auto-extends when pages run out
/// Creates a small DB, fills it with many items, and verifies no FileFull error.
// Requires external gRPC encoder (meowvec) — not available in CI.
#[ignore]
#[test]
fn test_file_auto_extend() {
    setup_encoder();
    let (_dir, mut db) = create_test_db();

    // Insert 500 items with real BGE-M3 vectors
    let mut total_created = 0u32;
    for i in 0..500 {
        let batch = StoreBatch {
            items: vec![StoreItem {
                text: format!(
                    "stress test document number {} with some content to fill pages",
                    i
                ),
                topic_label: Some(format!("topic_{}", i % 20)),
                domain_id: None,
                importance: Some(0.5),
                valence: None,
                arousal: None,
                source: SourceMeta::new(SourceType::UserInput, None),
                is_structural: false,
                source_ref: None,
            }],
            session_id: Some("stress_session".to_string()),
            turn_id: Some(format!("{}", i)),
            source: Default::default(),
        };
        let report = db
            .batch_store(batch)
            .expect("batch_store should succeed with auto-extend");
        total_created += report.l1_nodes_created;
    }

    assert_eq!(total_created, 500, "all 500 items should be created");
}

/// Test 2: Verify batch_store with many items doesn't cause partial writes
// Requires external gRPC encoder (meowvec) — not available in CI.
#[ignore]
#[test]
fn test_batch_store_no_partial_write() {
    setup_encoder();
    let (_dir, mut db) = create_test_db();

    // Create a batch with 100 items and real BGE-M3 vectors
    let items: Vec<StoreItem> = (0..100)
        .map(|i| StoreItem {
            text: format!("batch item {} with padding text to increase size", i),
            topic_label: Some(format!("batch_topic_{}", i % 5)),
            domain_id: None,
            importance: Some(0.5),
            valence: None,
            arousal: None,
            source: SourceMeta::new(SourceType::UserInput, None),
            is_structural: false,
            source_ref: None,
        })
        .collect();

    let batch = StoreBatch {
        items,
        session_id: Some("batch_session".to_string()),
        turn_id: Some("0".to_string()),
        source: Default::default(),
    };

    let report = db.batch_store(batch).expect("large batch should succeed");
    assert_eq!(
        report.l1_nodes_created, 100,
        "all 100 items should be stored"
    );
    assert!(report.l2_topics_updated > 0, "L2 topics should be created");
    assert!(report.edges_created > 0, "hyperedges should be created");

    // Verify DB is still readable after large batch
    let engrams = db
        .list_engrams(EngramListQuery {
            page: 1,
            page_size: 200,
            keyword: None,
            min_importance: None,
            state_filter: None,
        })
        .expect("DB should be readable after batch");
    assert!(engrams.total >= 100, "all engrams should be listable");
}

/// Test 3: Rapid alternating write + sync doesn't corrupt DB
// Requires external gRPC encoder (meowvec) — not available in CI.
#[ignore]
#[test]
fn test_rapid_write_and_sync() {
    setup_encoder();
    let (dir, mut db) = create_test_db();

    // Perform 100 rounds of write + sync with real BGE-M3 vectors
    for round in 0..100 {
        let batch = StoreBatch {
            items: vec![StoreItem {
                text: format!("round {} document with content", round),
                topic_label: Some(format!("round_topic_{}", round % 10)),
                domain_id: None,
                importance: Some(0.5),
                valence: None,
                arousal: None,
                source: SourceMeta::new(SourceType::UserInput, None),
                is_structural: false,
                source_ref: None,
            }],
            session_id: Some(format!("session_{}", round)),
            turn_id: Some("0".to_string()),
            source: Default::default(),
        };
        db.batch_store(batch).expect("write should succeed");
        db.sync().expect("sync should succeed");
    }

    // Close and reopen DB to verify persistence
    drop(db);
    let path = dir.path().join("stress_test.meh");
    let mut config = MemHopConfig::new(path, 1024);
    config.encoder_grpc_addr = None;
    let db2 = MemHop::open(config).expect("DB should reopen without corruption");

    let engrams = db2
        .list_engrams(EngramListQuery {
            page: 1,
            page_size: 200,
            keyword: None,
            min_importance: None,
            state_filter: None,
        })
        .expect("reopened DB should be readable");
    assert!(engrams.total >= 100, "all 100 rounds should be persisted");
}

/// Test 4: Import many L3 documents without corruption
#[test]
fn test_import_many_l3_documents() {
    // No encoder needed for L3 import — use minimal config
    let dir = TempDir::new().unwrap();
    let path = dir.path().join("stress_import.meh");
    let mut config = MemHopConfig::new(path, 1024);
    config.encoder_grpc_addr = None;
    let mut db = MemHop::open(config).unwrap();

    let mut total_created = 0usize;

    // Import 200 L3 knowledge items across 5 domains (no encoder needed)
    for i in 0..200 {
        let request = ImportRequest {
            target_layer: TargetLayer::Knowledge,
            data: ImportData::Knowledge(vec![KnowledgeImportItem {
                title: format!("knowledge_item_{}", i),
                domain: format!("domain_{}", i % 5),
                knowledge_type: "Factual".to_string(),
                text: format!("This is a long text document {} that should be truncated in L3 since L3 only stores summaries not original text", i),
                summary: Some(format!("Summary of document {}", i)),
                keywords: vec![format!("kw_{}", i), "test".to_string()],
                source_ref: Some(format!("source_{}", i)),
            }]),
            mode: ImportMode::Merge,
            knowledge_title: None,
        };
        let result = db.import_memory(request).expect("import should succeed");
        assert!(
            result.errors.is_empty(),
            "import {} had errors: {:?}",
            i,
            result.errors
        );
        total_created += result.created_ids.len();
    }

    assert_eq!(
        total_created, 200,
        "all 200 knowledge items should be created, got {}",
        total_created
    );

    // Verify DB is still consistent by listing topics (no encoder needed)
    let topics = db
        .list_l2(TopicListQuery {
            page: 1,
            page_size: 10,
            active_only: false,
            keyword: None,
        })
        .expect("list_l2 should work after imports");
    let _ = topics;
}
