//! Benchmark: Full Agent Workflow via Rust API
//!
//! Single database, all operations through `memhop::MemHop` directly.
//! The mock meowvec gRPC encoder is spawned automatically on port 27110.
//!
//! Phases:
//!   1. Setup: populate database with topics + knowledge
//!   2. Bench: measure search, update, query, session

use criterion::{black_box, criterion_group, Criterion};
use memhop::{
    ImportData, ImportMode, ImportRequest, KnowledgeImportItem, MemHop, MemHopConfig,
    RequestSource, SearchQuery, TargetLayer, TopicListQuery, UpdateRequest,
};
use std::path::PathBuf;
use std::sync::{Mutex, OnceLock};
use std::time::Instant;

mod common;
use common::{kill_mock_meowvec, spawn_mock_meowvec};

const DB_PATH: &str = "/tmp/memhop_bench_agent.meh";
const ENCODER_ADDR: &str = "http://127.0.0.1:27110";

// ============================================================================
// Global shared database — opened once, used by all benchmarks
// ============================================================================

static DB: OnceLock<Mutex<MemHop>> = OnceLock::new();

fn db() -> &'static Mutex<MemHop> {
    DB.get_or_init(|| {
        let _ = std::fs::remove_file(DB_PATH);
        let config = MemHopConfig {
            db_path: PathBuf::from(DB_PATH),
            encoder_grpc_addr: Some(ENCODER_ADDR.to_string()),
            vector_dim: 384,
            crystal_path: None,
            llm: Default::default(),
            auto_dream_on_evict: false,
            ivf_initial_k: 16,
            search_weights: None,
            decay_config: None,
            session_config: None,
            auto_dream_archive_threshold: None,
            auto_dream_summary_bytes: None,
        };
        let mut db = MemHop::open(config).expect("MemHop::open failed");

        // Pre-populate: create 10 topics
        for i in 0..10 {
            let _ = db.search_memory(SearchQuery {
                dialogue: format!(
                    "Topic {} about machine learning neural networks deep learning",
                    i
                ),
                context_id: None,
                l3_id: None,
                context_limit: 10,
                auto_create: 1,
                min_score: 0.0,
                source: RequestSource::default(),
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

        // Sync to ensure all data is persisted before benchmarks
        let _ = db.sync();

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
                .search_memory(SearchQuery {
                    dialogue: "neural network deep learning architecture".to_string(),
                    context_id: None,
                    l3_id: None,
                    context_limit: 5,
                    auto_create: 0,
                    min_score: 0.0,
                    source: RequestSource::default(),
                })
                .expect("search failed");
            black_box(res.contexts.len())
        })
    });
}

// ============================================================================
// Benchmarks: Write operations
// ============================================================================

fn bench_update_memory(_c: &mut Criterion) {
    let mut db = db().lock().unwrap();

    // Get first topic from the pre-populated database
    let res = db
        .list_topics(TopicListQuery {
            page: 1,
            page_size: 1,
            active_only: false,
            keyword: None,
        })
        .expect("list topics failed");
    let topic_id = res.items[0].id.clone();

    // Measure a single update call (each update allocates a page, so we
    // can't iterate without exhausting the fixed-size .meh file)
    let start = Instant::now();
    let res = db.update_memory(UpdateRequest {
        topic_id,
        dialogue_text: "User: How does Rust work?\nAssistant: Ownership and borrowing".to_string(),
        summary: None,
        action_chain: Some(vec![]),
        instant_distill: false,
        source: RequestSource::default(),
    });
    let elapsed = start.elapsed();
    assert!(res.is_ok(), "update failed: {:?}", res);
    println!("update_memory (single): {:?}", elapsed);
}

// ============================================================================
// Benchmarks: Query operations
// ============================================================================

fn bench_query_l2_list(c: &mut Criterion) {
    c.bench_function("query_l2_list", |b| {
        b.iter(|| {
            let db = db().lock().unwrap();
            let res = db
                .list_topics(TopicListQuery {
                    page: 1,
                    page_size: 10,
                    active_only: false,
                    keyword: None,
                })
                .expect("list topics failed");
            black_box(res.total)
        })
    });
}

fn bench_session_activate(c: &mut Criterion) {
    let mut db = db().lock().unwrap();

    let res = db
        .list_topics(TopicListQuery {
            page: 1,
            page_size: 1,
            active_only: false,
            keyword: None,
        })
        .expect("list topics failed");
    let topic_id = res.items[0].id.clone();

    c.bench_function("session_activate", |b| {
        b.iter(|| {
            db.activate_topic(black_box(&topic_id), Some(300_000));
        })
    });
}

criterion_group!(
    benches,
    bench_search_recall,
    bench_update_memory,
    bench_query_l2_list,
    bench_session_activate,
);

fn main() {
    let mut child = spawn_mock_meowvec(27110);

    benches();

    kill_mock_meowvec(&mut child);
}
