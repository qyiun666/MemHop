//! End-to-end benchmark exercising the public API surface from `API.md`.
//!
//! The mock meowvec gRPC encoder is spawned automatically. Two scale groups
//! are measured: small (10 topics) and medium (100 topics).

use criterion::{black_box, criterion_group, BenchmarkId, Criterion};
use memhop::{
    ArchiveQuery, ImportData, ImportMode, ImportRequest, KnowledgeImportItem, KnowledgeListQuery,
    MemHop, MemHopConfig, SearchQuery, TargetLayer, TopicListQuery, UpdateL2Fields, UpdateRequest,
};
use std::collections::HashMap;
use std::path::PathBuf;
use std::time::Duration;
use tempfile::TempDir;

mod common;
use common::{cleanup_global_meowvec, ensure_meowvec_running};

const ENCODER_ADDR: &str = "http://127.0.0.1:27110";

fn make_config(path: PathBuf) -> MemHopConfig {
    MemHopConfig {
        db_path: path,
        encoder_grpc_addr: ENCODER_ADDR.to_string(),
        vector_dim: 768,
        crystal_path: None,
        llm: Default::default(),
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
        llm_preprocess: Default::default(),
    }
}

/// Run the full public-API workflow once on a fresh temporary database.
fn run_e2e_workflow(n_topics: usize) {
    let dir = TempDir::new().expect("TempDir");
    let path = dir.path().join("e2e.meh");
    let mut db = MemHop::open(make_config(path)).expect("open failed");

    // 1. Seed topics via search_memory with auto_create (profile update removed in v0.57+)
    for i in 0..n_topics {
        let _ = db.search(SearchQuery {
            query: format!("end-to-end benchmark topic number {}", i),
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

    // 3. Search with auto_create and update the resulting topic
    let search_res = db
        .search(SearchQuery {
            query: "agent workflow orchestration benchmark".to_string(),
            layers: vec![2],
            max_results: 20,
            min_score: 0.0,
            include_profile: false,
            filters: None,
            directed_l2_id: None,
            directed_l3_id: None,
            auto_create: Some(1),
        })
        .expect("search_context failed");
    let topic_id = search_res
        .contexts
        .first()
        .map(|c| c.id.clone())
        .unwrap_or_default();

    let _update = db
        .update_memory(UpdateRequest {
            id: topic_id,
            layer: 2,
            fields: HashMap::from([
                (
                    "dialogue_text".to_string(),
                    serde_json::Value::String(
                        "User: what is MemHop?\nAssistant: an agent memory database".to_string(),
                    ),
                ),
                (
                    "summary".to_string(),
                    serde_json::Value::String("MemHop introduction".to_string()),
                ),
            ]),
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

    // 5. List topics and knowledge (needed for subsequent steps)
    let topics = db
        .list_l2(TopicListQuery {
            page: 1,
            page_size: 20,
            active_only: false,
            keyword: None,
        })
        .expect("list_l2 failed");
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

    // 6. Get & update L2 detail, search archives, get profile
    let topic_id_for_detail = topics
        .items
        .first()
        .map(|t| t.id.clone())
        .unwrap_or_default();
    let _detail = db.get_l2(&topic_id_for_detail).expect("get_l2 failed");
    let _updated = db
        .update_l2(
            &topic_id_for_detail,
            UpdateL2Fields {
                fused_summary: Some("Updated benchmark topic".to_string()),
                ..Default::default()
            },
        )
        .expect("update_l2 failed");
    black_box(_updated.fused_summary);

    // Search L4 archives
    let _archives = db
        .query_archives(ArchiveQuery {
            page: 1,
            page_size: 10,
            topic_id: None,
            keyword: None,
            time_range: None,
        })
        .expect("query_archives failed");
    black_box(_archives.len());

    // Read L0 profile
    let _profile = db.get_profile().expect("get_profile failed");
    black_box(_profile.is_some());

    // 7. Query L3 knowledge: list, get detail, search keywords
    let graph_id = import_res
        .id
        .clone()
        .or_else(|| knowledge.items.first().map(|k| k.id.clone()))
        .unwrap_or_default();
    let _l3 = db.l3_query(&graph_id, "MATCH (n) LIMIT 10", 1);

    // 8. Session operations (activate_topic is now internal; use session_status)
    let status = db.session_status();
    black_box(status.count);
    black_box(status.is_empty);

    // 9. Close
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
    ensure_meowvec_running(27110);
    benches();
    cleanup_global_meowvec();
}
