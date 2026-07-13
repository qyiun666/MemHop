//! Benchmark: Full Agent Workflow via Rust API
//!
//! Single database, all operations through `memhop::MemHop` directly.
//! The mock meowvec gRPC encoder is spawned automatically on port 27110.
//!
//! Phases:
//!   1. Setup: populate database with topics + knowledge
//!   2. Bench: measure search, update, query, session, and layer CRUD

use criterion::{black_box, criterion_group, Criterion};
use memhop::query::types::{ArchiveQuery, CrystalListQuery, KnowledgeNodeQuery};
use memhop::{
    ImportData, ImportMode, ImportRequest, KnowledgeImportItem, MemHop, MemHopConfig, SearchQuery,
    TargetLayer, TopicListQuery, UpdateRequest,
};
use std::collections::HashMap;
use std::path::PathBuf;
use std::sync::{Mutex, OnceLock};
use tempfile::TempDir;

mod common;
use common::{cleanup_global_meowvec, ensure_meowvec_running};

const DB_PATH: &str = "/tmp/memhop_bench_agent.meh";
const ENCODER_ADDR: &str = "http://127.0.0.1:27110";

// ============================================================================
// Global shared database — opened once, used by all benchmarks
// ============================================================================

static DB: OnceLock<Mutex<MemHop>> = OnceLock::new();

fn db() -> &'static Mutex<MemHop> {
    DB.get_or_init(|| {
        let _ = std::fs::remove_file(DB_PATH);
        let mut config = MemHopConfig::new(PathBuf::from(DB_PATH), 768);
        config.encoder_grpc_addr = ENCODER_ADDR.to_string();
        config.auto_dream_archive_threshold = 20;
        config.auto_dream_summary_bytes = 2048;
        let mut db = MemHop::open(config).expect("MemHop::open failed");

        // Pre-populate: create 10 topics
        for i in 0..10 {
            let _ = db.search(SearchQuery {
                query: format!(
                    "Topic {} about machine learning neural networks deep learning",
                    i
                ),
                layers: vec![2],
                max_results: 20,
                min_score: 0.0,
                include_profile: false,
                filters: None,
                directed_l2_id: None,
                directed_l3_id: None,
                auto_create: Some(1),
            });
        }

        // Pre-populate: import 5 knowledge items
        for i in 0..5 {
            let _ = db.import_memory(ImportRequest {
                target_layer: TargetLayer::Knowledge,
                mode: ImportMode::Merge,
                data: ImportData::Knowledge(vec![KnowledgeImportItem {
                    title: format!("Concept {}", i),
                    domain: "bench".to_string(),
                    knowledge_type: "Factual".to_string(),
                    text: "Knowledge about systems programming and memory safety".to_string(),
                    summary: None,
                    keywords: vec!["systems".to_string(), "memory".to_string()],
                    source_ref: None,
                }]),
                knowledge_title: None,
            });
        }

        Mutex::new(db)
    })
}

// ============================================================================
// Benchmarks: Recall operations
// ============================================================================

fn bench_search_recall(c: &mut Criterion) {
    c.bench_function("search_recall", |b| {
        b.iter(|| {
            let mut db = db().lock().unwrap();
            let res = db
                .search(SearchQuery {
                    query: "neural network deep learning architecture".to_string(),
                    layers: vec![2],
                    max_results: 20,
                    min_score: 0.0,
                    include_profile: false,
                    filters: None,
                    directed_l2_id: None,
                    directed_l3_id: None,
                    auto_create: None,
                })
                .expect("search failed");
            black_box(res.contexts.len())
        })
    });
}

// ============================================================================
// Benchmarks: Write operations
// ============================================================================

fn bench_update_memory(c: &mut Criterion) {
    // Open a small throw-away DB so criterion can iterate without exhausting
    // the global database's page pool.
    c.bench_function("update_memory", |b| {
        b.iter_batched(
            || {
                let dir = TempDir::new().expect("TempDir");
                let path = dir.path().join("update.meh");
                let mut config = MemHopConfig::new(path, 768);
                config.encoder_grpc_addr = ENCODER_ADDR.to_string();
                config.auto_dream_archive_threshold = 20;
                config.auto_dream_summary_bytes = 2048;
                let mut db = MemHop::open(config).expect("open");

                let res = db
                    .search(SearchQuery {
                        query: "Rust ownership borrowing lifetime".to_string(),
                        layers: vec![2],
                        max_results: 20,
                        min_score: 0.0,
                        include_profile: false,
                        filters: None,
                        directed_l2_id: None,
                        directed_l3_id: None,
                        auto_create: Some(1),
                    })
                    .expect("search");
                let topic_id = res.contexts[0].id.clone();
                (db, topic_id, dir)
            },
            |(mut db, topic_id, _dir)| {
                let res = db.update_memory(UpdateRequest {
                    id: topic_id,
                    layer: 2,
                    fields: HashMap::from([(
                        "dialogue_text".to_string(),
                        serde_json::Value::String(
                            "User: How does Rust work?\nAssistant: Ownership and borrowing"
                                .to_string(),
                        ),
                    )]),
                });
                black_box(res.is_ok());
            },
            criterion::BatchSize::SmallInput,
        )
    });
}

// ============================================================================
// Benchmarks: Query operations
// ============================================================================

fn bench_query_l2_list(c: &mut Criterion) {
    c.bench_function("query_l2_list", |b| {
        b.iter(|| {
            let db = db().lock().unwrap();
            let res = db
                .list_l2(TopicListQuery {
                    page: 1,
                    page_size: 10,
                    active_only: false,
                    keyword: None,
                })
                .expect("list_l2 failed");
            black_box(res.total)
        })
    });
}

fn bench_session_activate(c: &mut Criterion) {
    let topic_id = get_first_topic_id();

    c.bench_function("session_activate", |b| {
        b.iter(|| {
            let mut db = db().lock().unwrap();
            let status = db.session_status();
            black_box(status.count);
        })
    });
}

// ============================================================================
// Benchmarks: L0 Profile read
// ============================================================================

fn bench_l0_profile_get(c: &mut Criterion) {
    c.bench_function("l0_profile_get", |b| {
        b.iter(|| {
            let db = db().lock().unwrap();
            let res = db.get_profile().expect("get_profile failed");
            black_box(res.is_some())
        })
    });
}

// ============================================================================
// Benchmarks: L1 Engram list (may be empty — validates the code path)
// ============================================================================

fn bench_l3_knowledge_list(c: &mut Criterion) {
    c.bench_function("l3_knowledge_list", |b| {
        b.iter(|| {
            let db = db().lock().unwrap();
            let res = db
                .list_knowledge(memhop::query::types::KnowledgeListQuery {
                    page: 1,
                    page_size: 20,
                    domain_filter: None,
                    knowledge_type: None,
                    keyword: None,
                })
                .expect("list_knowledge failed");
            black_box(res.total)
        })
    });
}

fn bench_l3_get_knowledge(c: &mut Criterion) {
    let graph_id = get_first_knowledge_id();

    c.bench_function("l3_get_knowledge", |b| {
        b.iter(|| {
            let db = db().lock().unwrap();
            let res = db
                .get_knowledge(black_box(&graph_id))
                .expect("get_knowledge failed");
            black_box(res.is_some())
        })
    });
}

fn bench_l3_search_keyword(c: &mut Criterion) {
    let graph_id = get_first_knowledge_id();

    c.bench_function("l3_search_keyword", |b| {
        b.iter(|| {
            let db = db().lock().unwrap();
            let res = db
                .query_knowledge_nodes(KnowledgeNodeQuery::ByKeyword {
                    graph_id: black_box(&graph_id).clone(),
                    keyword: "memory".to_string(),
                    limit: 5,
                })
                .expect("query_knowledge_nodes failed");
            black_box(res.total)
        })
    });
}

fn bench_l3_get_by_type(c: &mut Criterion) {
    let graph_id = get_first_knowledge_id();

    c.bench_function("l3_get_by_type", |b| {
        b.iter(|| {
            let db = db().lock().unwrap();
            let res = db
                .query_knowledge_nodes(KnowledgeNodeQuery::ByType {
                    graph_id: black_box(&graph_id).clone(),
                    node_type: "Factual".to_string(),
                    limit: 5,
                })
                .expect("query_knowledge_nodes failed");
            black_box(res.total)
        })
    });
}

// ============================================================================
// Benchmarks: L4 Archive (may return empty — validates the code path)
// ============================================================================

fn bench_l4_archive_search(c: &mut Criterion) {
    c.bench_function("l4_archive_search", |b| {
        b.iter(|| {
            let db = db().lock().unwrap();
            let res = db
                .query_archives(ArchiveQuery {
                    page: 1,
                    page_size: 10,
                    topic_id: None,
                    keyword: None,
                    time_range: None,
                })
                .expect("query_archives failed");
            black_box(res.len())
        })
    });
}

fn bench_l4_list_by_topic(c: &mut Criterion) {
    let topic_id = get_first_topic_id();

    c.bench_function("l4_list_by_topic", |b| {
        b.iter(|| {
            let db = db().lock().unwrap();
            let res = db
                .query_archives(ArchiveQuery {
                    topic_id: Some(black_box(&topic_id).clone()),
                    page: 1,
                    page_size: 10,
                    keyword: None,
                    time_range: None,
                })
                .expect("query_archives failed");
            black_box(res.len())
        })
    });
}

// ============================================================================
// Benchmarks: L5 Crystal (list may be empty — validates code path)
// ============================================================================

fn bench_l5_crystal_list(c: &mut Criterion) {
    c.bench_function("l5_crystal_list", |b| {
        b.iter(|| {
            let db = db().lock().unwrap();
            let res = db
                .list_crystals(CrystalListQuery {
                    page: 1,
                    page_size: 20,
                    status_filter: None,
                    min_trigger_count: None,
                    keyword: None,
                })
                .expect("list_crystals failed");
            black_box(res.total)
        })
    });
}

// ============================================================================
// Benchmarks: L5 Crystal update (throw-away DB — needs manually created data)
// ============================================================================

fn bench_l5_crystal_get(c: &mut Criterion) {
    // Use a throw-away DB; L5 doesn't need encoder.
    // We call get_l5 with a non-existent ID to benchmark the lookup path.
    c.bench_function("l5_crystal_get_miss", |b| {
        b.iter(|| {
            let dir = TempDir::new().expect("TempDir");
            let path = dir.path().join("l5.meh");
            let mut config = MemHopConfig::new(path, 768);
            config.encoder_grpc_addr = "http://127.0.0.1:27110".to_string();
            let db = MemHop::open(config).expect("open");
            let res = db.get_l5("0000000000000060").expect("get_l5 failed");
            black_box(res.is_none());
        })
    });
}

// ============================================================================
// Benchmarks: Session management
// ============================================================================

fn bench_session_mgmt(c: &mut Criterion) {
    let topic_id = get_first_topic_id();

    c.bench_function("session_mgmt", |b| {
        b.iter(|| {
            let mut db = db().lock().unwrap();

            // Check status
            let status = db.session_status();
            black_box(status.count);
            black_box(status.is_empty);

            // Get active IDs
            // (now internal to session_status)
        })
    });
}

// ============================================================================
// Helpers
// ============================================================================

fn get_first_topic_id() -> String {
    let db = db().lock().unwrap();
    let res = db
        .list_l2(TopicListQuery {
            page: 1,
            page_size: 1,
            active_only: false,
            keyword: None,
        })
        .expect("list_l2 failed");
    res.items[0].id.clone()
}

fn get_first_knowledge_id() -> String {
    let db = db().lock().unwrap();
    let res = db
        .list_knowledge(memhop::query::types::KnowledgeListQuery {
            page: 1,
            page_size: 1,
            domain_filter: None,
            knowledge_type: None,
            keyword: None,
        })
        .expect("list_knowledge failed");
    res.items[0].id.clone()
}

criterion_group!(
    benches,
    // Core workflow
    bench_search_recall,
    bench_update_memory,
    bench_query_l2_list,
    // Session
    bench_session_activate,
    bench_session_mgmt,
    // L0
    bench_l0_profile_get,
    // L3
    bench_l3_knowledge_list,
    bench_l3_get_knowledge,
    bench_l3_search_keyword,
    bench_l3_get_by_type,
    // L4
    bench_l4_archive_search,
    bench_l4_list_by_topic,
    // L5
    bench_l5_crystal_list,
    bench_l5_crystal_get,
);

fn main() {
    ensure_meowvec_running(27110);
    benches();
    cleanup_global_meowvec();
}
