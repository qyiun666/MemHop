//! Benchmark: Full Agent Workflow via Rust API
//!
//! Single database, all operations through `memhop::MemHop` directly.
//! The mock meowvec gRPC encoder is spawned automatically on port 27110.
//!
//! Phases:
//!   1. Setup: populate database with topics + knowledge
//!   2. Bench: measure search, update, query, session, and layer CRUD

use criterion::{black_box, criterion_group, Criterion};
use memhop::query::types::{
    ArchivePageQuery, CrystalListQuery, EngramListQuery, L6Filter, UpdateL6Fields,
};
use memhop::{
    ImportData, ImportMode, ImportRequest, KnowledgeImportItem, MemHop, MemHopConfig,
    PathwayWeightSlot, RequestSource, SearchQuery, TargetLayer, TopicListQuery, UpdateRequest,
};
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
        let mut config = MemHopConfig::new(PathBuf::from(DB_PATH), 1024);
        config.encoder_grpc_addr = Some(ENCODER_ADDR.to_string());
        config.auto_dream_archive_threshold = 20;
        config.auto_dream_summary_bytes = 2048;
        let mut db = MemHop::open(config).expect("MemHop::open failed");

        // Pre-populate: create 10 topics
        for i in 0..10 {
            let _ = db.search_context(SearchQuery {
                dialogue: format!(
                    "Topic {} about machine learning neural networks deep learning",
                    i
                ),
                l2_id: None,
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
                .search_context(SearchQuery {
                    dialogue: "neural network deep learning architecture".to_string(),
                    l2_id: None,
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

fn bench_update_memory(c: &mut Criterion) {
    // Open a small throw-away DB so criterion can iterate without exhausting
    // the global database's page pool.
    c.bench_function("update_memory", |b| {
        b.iter_batched(
            || {
                let dir = TempDir::new().expect("TempDir");
                let path = dir.path().join("update.meh");
                let mut config = MemHopConfig::new(path, 1024);
                config.encoder_grpc_addr = Some(ENCODER_ADDR.to_string());
                config.auto_dream_archive_threshold = 20;
                config.auto_dream_summary_bytes = 2048;
                let mut db = MemHop::open(config).expect("open");

                let res = db
                    .search_context(SearchQuery {
                        dialogue: "Rust ownership borrowing lifetime".to_string(),
                        l2_id: None,
                        context_id: None,
                        l3_id: None,
                        context_limit: 5,
                        auto_create: 1,
                        min_score: 0.0,
                        source: RequestSource::default(),
                    })
                    .expect("search");
                let topic_id = res.contexts[0].id.clone();
                (db, topic_id, dir)
            },
            |(mut db, topic_id, _dir)| {
                let res = db.update_memory(UpdateRequest {
                    topic_id,
                    dialogue_text: "User: How does Rust work?\nAssistant: Ownership and borrowing"
                        .to_string(),
                    summary: None,
                    action_chain: Some(vec![]),
                    instant_distill: false,
                    scene_id: None,
                    source: RequestSource::default(),
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
            db.activate_topic(black_box(&topic_id), Some(300_000));
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

fn bench_l1_engram_list(c: &mut Criterion) {
    c.bench_function("l1_engram_list", |b| {
        b.iter(|| {
            let db = db().lock().unwrap();
            let res = db
                .list_engrams(EngramListQuery {
                    page: 1,
                    page_size: 20,
                    state_filter: None,
                    min_importance: None,
                    keyword: None,
                })
                .expect("list_engrams failed");
            black_box(res.total)
        })
    });
}

// ============================================================================
// Benchmarks: L3 Knowledge read operations
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
                .search_knowledge_nodes_by_keyword(black_box(&graph_id), "memory", 5)
                .expect("search_knowledge_nodes_by_keyword failed");
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
                .get_knowledge_nodes_by_type(black_box(&graph_id), "Factual", 5)
                .expect("get_knowledge_nodes_by_type failed");
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
                .search_l4(memhop::query::types::L4SearchQuery {
                    recent: Some(10),
                    ..Default::default()
                })
                .expect("search_l4 failed");
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
                .list_archives_by_topic(
                    black_box(&topic_id),
                    ArchivePageQuery {
                        page: 1,
                        page_size: 10,
                        start_time: None,
                        end_time: None,
                        content_type: None,
                    },
                )
                .expect("list_archives_by_topic failed");
            black_box(res.total)
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
// Benchmarks: L6 Pathway (list + CRUD on throw-away DB)
// ============================================================================

fn bench_l6_pathway_list(c: &mut Criterion) {
    c.bench_function("l6_pathway_list", |b| {
        b.iter(|| {
            let db = db().lock().unwrap();
            let res = db.list_l6(None).expect("list_l6 failed");
            black_box(res.len())
        })
    });
}

fn bench_l6_pathway_crud(c: &mut Criterion) {
    // Use a throw-away DB — L6 doesn't need encoder.
    c.bench_function("l6_pathway_crud", |b| {
        b.iter_batched(
            || {
                let dir = TempDir::new().expect("TempDir");
                let path = dir.path().join("l6.meh");
                let mut config = MemHopConfig::new(path, 1024);
                config.encoder_grpc_addr = None;
                let db = MemHop::open(config).expect("open");
                (db, dir)
            },
            |(mut db, _dir)| {
                // Add
                let slot = PathwayWeightSlot {
                    id_hash: 42,
                    source_node: "condition:deploy".into(),
                    target_node: "action:restart".into(),
                    weight: 0.9,
                    trigger_count: 10,
                    success_rate: 0.85,
                    last_accessed: 1700000000000,
                    metadata: r#"{"strategy":"react"}"#.into(),
                    created_at: 1000,
                    updated_at: 2000,
                    version: 1,
                };
                db.add_l6(vec![slot]).expect("add_l6 failed");

                // Get
                let got = db.get_l6("000000000000002a").expect("get_l6 failed");
                black_box(got.is_some());

                // Update
                let updated = db
                    .update_l6(
                        "000000000000002a",
                        UpdateL6Fields {
                            weight: Some(0.95),
                            ..Default::default()
                        },
                    )
                    .expect("update_l6 failed");
                black_box(updated.weight);

                // Update weight
                let adjusted = db
                    .update_l6_weight("000000000000002a", 0.05)
                    .expect("update_l6_weight failed");
                black_box(adjusted.weight);

                // List with filter
                let filtered = db
                    .list_l6(Some(L6Filter {
                        source_prefix: Some("condition:".into()),
                        min_weight: Some(0.5),
                        ..Default::default()
                    }))
                    .expect("list_l6(filtered) failed");
                black_box(filtered.len());

                // Delete
                db.delete_l6("000000000000002a").expect("delete_l6 failed");
            },
            criterion::BatchSize::SmallInput,
        )
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
            let mut config = MemHopConfig::new(path, 1024);
            config.encoder_grpc_addr = None;
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

            // Activate
            db.activate_topic(black_box(&topic_id), Some(300_000));

            // Check count / empty
            black_box(db.session_count());
            black_box(db.sessions_empty());

            // Get active IDs
            let ids = db.get_active_topic_ids();
            black_box(ids.len());

            // Adjust activation
            db.adjust_activation(black_box(&topic_id), 1.0);

            // Deactivate
            db.deactivate_topic(black_box(&topic_id));

            // Purge expired
            db.purge_expired_sessions();
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
    // L1
    bench_l1_engram_list,
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
    // L6
    bench_l6_pathway_list,
    bench_l6_pathway_crud,
);

fn main() {
    ensure_meowvec_running(27110);
    benches();
    cleanup_global_meowvec();
}
