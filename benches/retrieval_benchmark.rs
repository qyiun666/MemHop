//! Retrieval benchmark using the `locomo_smoke.json` fixture.
//!
//! Ingests each conversation turn as a searchable context, then measures
//! throughput and recall@5 of `search_memory` over the provided questions.

use criterion::{black_box, criterion_group, Criterion};
use memhop::{MemHop, MemHopConfig, RequestSource, SearchQuery, SearchWeights};
use serde::Deserialize;
use std::collections::{HashMap, HashSet};
use std::path::PathBuf;
use std::time::{Duration, Instant};

mod common;
use common::{cleanup_global_meowvec, ensure_meowvec_running};

fn encoder_addr() -> String {
    std::env::var("MEMHOP_BENCH_ENCODER_ADDR")
        .unwrap_or_else(|_| "http://127.0.0.1:27110".to_string())
}

fn fixture_path() -> String {
    let manifest = env!("CARGO_MANIFEST_DIR");
    let is_full = std::env::var("BENCH_FULL").ok().is_some();
    if is_full {
        format!("{}/benches/fixtures/locomo_full.json", manifest)
    } else {
        format!("{}/benches/fixtures/locomo_smoke.json", manifest)
    }
}

const K: usize = 5;

#[derive(Debug, Deserialize)]
struct Fixture {
    sessions: Vec<Session>,
    questions: Vec<Question>,
}

#[derive(Debug, Deserialize)]
struct Session {
    id: String,
    turns: Vec<Turn>,
}

#[derive(Debug, Deserialize)]
struct Turn {
    text: String,
    speaker: String,
}

#[derive(Debug, Deserialize)]
struct Question {
    question: String,
    #[serde(rename = "session_refs")]
    relevant_sessions: Vec<String>,
    #[serde(default)]
    category: String,
}

fn bench_vector_dim() -> usize {
    std::env::var("BENCH_VECTOR_DIM")
        .ok()
        .and_then(|s| s.parse().ok())
        .unwrap_or(1024)
}

fn make_config(path: PathBuf) -> MemHopConfig {
    MemHopConfig {
        db_path: path,
        encoder_grpc_addr: Some(encoder_addr()),
        vector_dim: bench_vector_dim(),
        crystal_path: None,
        llm: Default::default(),
        auto_dream_on_evict: false,
        auto_dream_archive_threshold: 20,
        auto_dream_summary_bytes: 2048,
        ivf_initial_k: 16,
        search_weights: Some(SearchWeights {
            bm25_weight: 0.45,
            vector_weight: 0.55,
            n_probes: 8,
            enable_reranker: true,
            rerank_max_candidates: 1, // 减少 rerank 编码次数以控制基准耗时
        }),
        decay_config: None,
        session_config: None,
        dream_idle_threshold_secs: None,
        auto_checkpoint_interval: None,
        adjacency_cache_max_entries: 128,
    }
}

/// Load fixture, build a fresh database, and return the DB plus ground truth.
fn setup() -> (MemHop, Vec<Question>, HashMap<String, String>) {
    let fixture: Fixture =
        serde_json::from_reader(std::fs::File::open(fixture_path()).expect("open fixture"))
            .expect("parse fixture");

    // Filter questions: skip open_domain in full mode, apply max count
    let max_q = std::env::var("BENCH_MAX_QUESTIONS")
        .ok()
        .and_then(|s| s.parse::<usize>().ok());
    let skip_open = std::env::var("BENCH_FULL").ok().is_some();
    let mut questions: Vec<Question> = fixture
        .questions
        .into_iter()
        .filter(|q| {
            if skip_open {
                q.category != "open_domain"
            } else {
                true
            }
        })
        .collect();
    if let Some(max) = max_q {
        questions.truncate(max);
    }
    eprintln!(
        "[setup] {} sessions, {} questions (full={}, max={:?}, skip_open={})",
        fixture.sessions.len(),
        questions.len(),
        std::env::var("BENCH_FULL").ok().is_some(),
        max_q,
        skip_open
    );

    let db_path = PathBuf::from("/tmp/memhop_retrieval_bench.meh");
    let _ = std::fs::remove_file(&db_path);
    let mut db = MemHop::open(make_config(db_path)).expect("open failed");

    let mut context_to_session: HashMap<String, String> = HashMap::new();

    for session in &fixture.sessions {
        for turn in &session.turns {
            let dialogue = format!("{}: {}", turn.speaker, turn.text);
            let res = db
                .search_context(SearchQuery {
                    dialogue,
                    l2_id: None,
                    context_id: None,
                    l3_id: None,
                    context_limit: 1,
                    auto_create: 1,
                    min_score: 0.0,
                    source: RequestSource::default(),
                })
                .expect("ingest search failed");
            if let Some(ctx) = res.contexts.first() {
                context_to_session.insert(ctx.id.clone(), session.id.clone());
            }
        }
    }

    (db, questions, context_to_session)
}

fn bench_retrieval(c: &mut Criterion) {
    let (mut db, questions, context_to_session) = setup();

    // Compute recall@K and nDCG@K once for reporting (not part of the timed loop).
    let mut total_recall = 0.0;
    let mut total_ndcg = 0.0;
    let mut latencies: Vec<Duration> = Vec::with_capacity(questions.len());
    for q in &questions {
        let relevant: Vec<&str> = q.relevant_sessions.iter().map(|s| s.as_str()).collect();
        let mut retrieved: Vec<&str> = Vec::new();
        let mut seen = HashSet::new();

        let start = Instant::now();
        let search_result = db.search_context(SearchQuery {
            dialogue: q.question.clone(),
            l2_id: None,
            context_id: None,
            l3_id: None,
            context_limit: K,
            auto_create: 0,
            min_score: 0.0,
            source: RequestSource::default(),
        });
        latencies.push(start.elapsed());

        if let Ok(r) = search_result {
            for ctx in &r.contexts {
                if let Some(sid) = context_to_session.get(&ctx.id) {
                    if seen.insert(sid.as_str()) {
                        retrieved.push(sid.as_str());
                    }
                }
            }
        }
        total_recall += common::recall_at_k(&retrieved, &relevant, K);
        total_ndcg += common::ndcg_at_k(&retrieved, &relevant, K);
    }
    let stats = common::latency_stats(&latencies);
    println!(
        "retrieval recall@{} over {} questions: {:.2}",
        K,
        questions.len(),
        total_recall / questions.len() as f64
    );
    println!(
        "retrieval nDCG@{} over {} questions: {:.2}",
        K,
        questions.len(),
        total_ndcg / questions.len() as f64
    );
    println!(
        "retrieval per-query latency P99: {:?}, max: {:?}",
        stats.p99, stats.max
    );

    let mut group = c.benchmark_group("retrieval");
    group
        .sample_size(10)
        .warm_up_time(Duration::from_millis(500))
        .measurement_time(Duration::from_secs(5));

    group.bench_function("search_memory throughput", |b| {
        b.iter(|| {
            for q in &questions {
                let res = db
                    .search_context(SearchQuery {
                        dialogue: q.question.clone(),
                        l2_id: None,
                        context_id: None,
                        l3_id: None,
                        context_limit: K,
                        auto_create: 0,
                        min_score: 0.0,
                        source: RequestSource::default(),
                    })
                    .expect("search failed");
                black_box(res.contexts.len());
            }
        });
    });

    group.finish();
}

criterion_group!(benches, bench_retrieval);

fn main() {
    let external_addr = std::env::var("MEMHOP_BENCH_ENCODER_ADDR").ok();
    if external_addr.is_none() {
        // Let ensure_meowvec_running handle health-check + spawn.
        ensure_meowvec_running(27110);
    } else {
        // Verify the externally-provided encoder is reachable.
        let addr = encoder_addr();
        memhop::encoder::GrpcEncoder::new(&addr, bench_vector_dim())
            .expect("external meowvec encoder is not ready");
    }
    benches();
    cleanup_global_meowvec();
}
