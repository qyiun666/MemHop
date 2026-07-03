//! End-to-end benchmark exercising the public API surface from `API.md`.
//!
//! The mock meowvec gRPC encoder is spawned automatically. Two scale groups
//! are measured: small (10 topics) and medium (100 topics).

use criterion::{black_box, criterion_group, BenchmarkId, Criterion};
use memhop::{
    ImportData, ImportMode, ImportRequest, KnowledgeImportItem, KnowledgeListQuery, MemHop,
    MemHopConfig, RequestSource, SearchQuery, TargetLayer, TopicListQuery, UpdateProfileRequest,
    UpdateRequest,
};
use std::collections::HashMap;
use std::path::PathBuf;
use std::time::Duration;
use tempfile::TempDir;

mod common;
use common::{kill_mock_meowvec, spawn_mock_meowvec};

const ENCODER_ADDR: &str = "http://127.0.0.1:27110";

fn make_config(path: PathBuf) -> MemHopConfig {
    MemHopConfig {
        db_path: path,
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
    }
}

/// Run the full public-API workflow once on a fresh temporary database.
fn run_e2e_workflow(n_topics: usize) {
    let dir = TempDir::new().expect("TempDir");
    let path = dir.path().join("e2e.meh");
    let mut db = MemHop::open(make_config(path)).expect("open failed");

    // 1. Update L0 profile
    let mut prefs = HashMap::new();
    prefs.insert("theme".to_string(), "dark".to_string());
    let _profile = db
        .update_profile(UpdateProfileRequest {
            name: Some("BenchAgent".to_string()),
            role: Some("benchmark runner".to_string()),
            personality: None,
            worldview: None,
            preferences: Some(prefs),
            lexicon: None,
            style_traits: None,
            emotion_patterns: None,
        })
        .expect("update_profile failed");

    // 2. Seed topics via search_memory with auto_create
    for i in 0..n_topics {
        let _ = db.search_memory(SearchQuery {
            dialogue: format!("end-to-end benchmark topic number {}", i),
            context_id: None,
            l3_id: None,
            context_limit: 5,
            auto_create: 1,
            min_score: 0.0,
            source: RequestSource::default(),
        });
    }

    // 3. Search with auto_create and update the resulting topic
    let search_res = db
        .search_memory(SearchQuery {
            dialogue: "agent workflow orchestration benchmark".to_string(),
            context_id: None,
            l3_id: None,
            context_limit: 5,
            auto_create: 1,
            min_score: 0.0,
            source: RequestSource::default(),
        })
        .expect("search_memory failed");
    let topic_id = search_res
        .contexts
        .first()
        .map(|c| c.id.clone())
        .unwrap_or_default();

    let _update = db
        .update_memory(UpdateRequest {
            topic_id,
            dialogue_text: "User: what is MemHop?\nAssistant: an agent memory database".to_string(),
            summary: Some("MemHop introduction".to_string()),
            action_chain: None,
            instant_distill: false,
            source: RequestSource::default(),
        })
        .expect("update_memory failed");

    // 4. Import external knowledge into L3
    let import_res = db
        .import_memory(ImportRequest {
            target_layer: TargetLayer::Knowledge,
            mode: ImportMode::Merge,
            data: ImportData::Knowledge(vec![KnowledgeImportItem {
                title: "MemHop Architecture".to_string(),
                domain: "benchmark".to_string(),
                knowledge_type: "Factual".to_string(),
                text: "MemHop uses a six-layer cognitive architecture inspired by human memory."
                    .to_string(),
                summary: None,
                keywords: vec!["memory".to_string(), "architecture".to_string()],
                source_ref: None,
            }]),
            knowledge_title: None,
        })
        .expect("import_memory failed");

    // 5. List topics and knowledge
    let topics = db
        .list_topics(TopicListQuery {
            page: 1,
            page_size: 20,
            active_only: false,
            keyword: None,
        })
        .expect("list_topics failed");
    black_box(topics.total);

    let knowledge = db
        .list_knowledge(KnowledgeListQuery {
            page: 1,
            page_size: 20,
            domain_filter: None,
            knowledge_type: None,
            keyword: None,
        })
        .expect("list_knowledge failed");
    black_box(knowledge.total);

    // 6. Query L3 knowledge graph via DSL
    let graph_id = import_res
        .id
        .clone()
        .or_else(|| knowledge.items.first().map(|k| k.id.clone()))
        .unwrap_or_default();
    let _l3 = db.l3_query(&graph_id, "MATCH (n) LIMIT 10", 1);

    // 7. Checkpoint and close
    db.checkpoint().expect("checkpoint failed");
    db.close().expect("close failed");
}

fn e2e_workflow(c: &mut Criterion) {
    let mut group = c.benchmark_group("e2e_workflow");
    group
        .sample_size(10)
        .warm_up_time(Duration::from_secs(1))
        .measurement_time(Duration::from_secs(8));

    for n in [10, 100] {
        group.bench_with_input(BenchmarkId::from_parameter(n), &n, |b, &n| {
            b.iter(|| run_e2e_workflow(n));
        });
    }

    group.finish();
}

criterion_group!(benches, e2e_workflow);

fn main() {
    let mut child = spawn_mock_meowvec(27110);

    benches();

    kill_mock_meowvec(&mut child);
}
