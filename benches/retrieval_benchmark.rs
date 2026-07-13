//! Retrieval benchmark using the `locomo_smoke.json` fixture.
//!
//! Ingests each conversation turn as a searchable context, then measures
//! throughput and recall@5 of `search_memory` over the provided questions.

use criterion::{black_box, criterion_group, Criterion};
use memhop::{LlmConfig, MemHop, MemHopConfig, SearchQuery, SearchWeights};
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
        .unwrap_or(768)
}

fn llm_config_from_env() -> LlmConfig {
    let api_key = std::env::var("MEMHOP_LLM_API_KEY").unwrap_or_default();
    let api_url = std::env::var("MEMHOP_LLM_API_URL")
        .unwrap_or_else(|_| "https://api.deepseek.com/v1/chat/completions".to_string());
    let model = std::env::var("MEMHOP_LLM_MODEL").unwrap_or_else(|_| "deepseek-chat".to_string());
    let lang = std::env::var("MEMHOP_LLM_LANGUAGE").unwrap_or_else(|_| "zh".to_string());
    LlmConfig::new(api_url, api_key, model, lang)
}

fn make_config(path: PathBuf) -> MemHopConfig {
    MemHopConfig {
        db_path: path,
        encoder_grpc_addr: encoder_addr(),
        vector_dim: bench_vector_dim(),
        crystal_path: None,
        llm: llm_config_from_env(),
        auto_dream_on_evict: false,
        auto_dream_archive_threshold: 20,
        auto_dream_summary_bytes: 2048,
        ivf_initial_k: 16,
        search_weights: Some(SearchWeights {
            bm25_weight: 0.45,
            vector_weight: 0.55,
            n_probes: 8,
            enable_reranker: true,
            rerank_max_candidates: 1,
            activation_boost: 1.3,
        }),
        decay_config: None,
        session_config: None,
        dream_idle_threshold_secs: None,
        auto_checkpoint_interval: None,
        adjacency_cache_max_entries: 128,
        llm_preprocess: if std::env::var("BENCH_LLM_PREPROCESS").ok().is_some() {
            Default::default()
        } else {
            memhop::LlmPreprocessConfig {
                preprocess_temperature: 0.1,
                preprocess_max_tokens: 0,
            }
        },
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
                .search(SearchQuery {
                    query: dialogue,
                    layers: vec![2],
                    max_results: 20,
                    min_score: 0.0,
                    include_profile: false,
                    filters: None,
                    directed_l2_id: None,
                    directed_l3_id: None,
                    auto_create: Some(1),
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
    let sessions: Vec<_> = {
        let path = fixture_path();
        let f: Fixture = serde_json::from_reader(std::fs::File::open(path).expect("open fixture"))
            .expect("parse fixture");
        f.sessions
    };

    // Compute all metrics once for reporting (not part of the timed loop).
    let mut recall_sums = [0.0f64; 4]; // indices: 0=R@1, 1=R@3, 2=R@5, 3=R@10
    let ks = [1, 3, 5, 10];
    let mut ndcg5_sum = 0.0;
    let mut mrr_sum = 0.0;
    let mut latencies: Vec<Duration> = Vec::with_capacity(questions.len());

    for q in &questions {
        let relevant: Vec<&str> = q.relevant_sessions.iter().map(|s| s.as_str()).collect();
        let mut retrieved: Vec<&str> = Vec::new();
        let mut seen = HashSet::new();

        let start = Instant::now();
        let search_result = db.search(SearchQuery {
            query: q.question.clone(),
            layers: vec![2],
            max_results: 20,
            min_score: 0.0,
            include_profile: false,
            filters: None,
            directed_l2_id: None,
            directed_l3_id: None,
            auto_create: None,
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

        for (i, &k) in ks.iter().enumerate() {
            recall_sums[i] += common::recall_at_k(&retrieved, &relevant, k);
        }
        ndcg5_sum += common::ndcg_at_k(&retrieved, &relevant, 5);
        mrr_sum += common::mrr(&retrieved, &relevant);
    }

    let n = questions.len() as f64;
    let stats = common::latency_stats(&latencies);
    println!();
    println!("================================================================");
    println!("  MemHop Retrieval Benchmark Results");
    println!(
        "  Dataset: LOCOMO full ({} sessions, {} questions)",
        sessions.len(),
        questions.len()
    );
    println!("================================================================");
    println!("  Recall@1    = {:.4}", recall_sums[0] / n);
    println!("  Recall@3    = {:.4}", recall_sums[1] / n);
    println!("  Recall@5    = {:.4}", recall_sums[2] / n);
    println!("  Recall@10   = {:.4}", recall_sums[3] / n);
    println!("  nDCG@5      = {:.4}", ndcg5_sum / n);
    println!("  MRR         = {:.4}", mrr_sum / n);
    println!("----------------------------------------------------------------");
    println!("  Latency P50  = {:?}", stats.p50);
    println!("  Latency P95  = {:?}", stats.p95);
    println!("  Latency P99  = {:?}", stats.p99);
    println!("  Latency max  = {:?}", stats.max);
    println!("  Latency mean = {:?}", stats.mean);
    println!("================================================================");
    println!();

    let mut group = c.benchmark_group("retrieval");
    group
        .sample_size(10)
        .warm_up_time(Duration::from_millis(500))
        .measurement_time(Duration::from_secs(5));

    group.bench_function("search_memory throughput", |b| {
        b.iter(|| {
            for q in &questions {
                let res = db
                    .search(SearchQuery {
                        query: q.question.clone(),
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
