//! Retrieval benchmark using the `locomo_smoke.json` fixture.
//!
//! Ingests each conversation turn as a searchable context, then measures
//! throughput and recall@5 of `search_memory` over the provided questions.

use criterion::{black_box, criterion_group, Criterion};
use memhop::{MemHop, MemHopConfig, RequestSource, SearchQuery};
use serde::Deserialize;
use std::collections::{HashMap, HashSet};
use std::path::PathBuf;
use std::time::Duration;

mod common;
use common::{kill_mock_meowvec, spawn_mock_meowvec};

const ENCODER_ADDR: &str = "http://127.0.0.1:27110";
const FIXTURE: &str = concat!(
    env!("CARGO_MANIFEST_DIR"),
    "/benches/fixtures/locomo_smoke.json"
);
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
}

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

/// Load fixture, build a fresh database, and return the DB plus ground truth.
fn setup() -> (MemHop, Vec<Question>, HashMap<String, String>) {
    let fixture: Fixture =
        serde_json::from_reader(std::fs::File::open(FIXTURE).expect("open fixture"))
            .expect("parse fixture");

    let db_path = PathBuf::from("/tmp/memhop_retrieval_bench.meh");
    let _ = std::fs::remove_file(&db_path);
    let mut db = MemHop::open(make_config(db_path)).expect("open failed");

    let mut context_to_session: HashMap<String, String> = HashMap::new();

    for session in &fixture.sessions {
        for turn in &session.turns {
            let dialogue = format!("{}: {}", turn.speaker, turn.text);
            let res = db
                .search_memory(SearchQuery {
                    dialogue,
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

    (db, fixture.questions, context_to_session)
}

fn bench_retrieval(c: &mut Criterion) {
    let (mut db, questions, context_to_session) = setup();

    // Compute recall@K once for reporting (not part of the timed loop).
    let mut total_recall = 0.0;
    for q in &questions {
        let relevant: Vec<&str> = q.relevant_sessions.iter().map(|s| s.as_str()).collect();
        let mut retrieved: Vec<&str> = Vec::new();
        let mut seen = HashSet::new();
        if let Ok(r) = db.search_memory(SearchQuery {
            dialogue: q.question.clone(),
            context_id: None,
            l3_id: None,
            context_limit: K,
            auto_create: 0,
            min_score: 0.0,
            source: RequestSource::default(),
        }) {
            for ctx in &r.contexts {
                if let Some(sid) = context_to_session.get(&ctx.id) {
                    if seen.insert(sid.as_str()) {
                        retrieved.push(sid.as_str());
                    }
                }
            }
        }
        total_recall += common::recall_at_k(&retrieved, &relevant, K);
    }
    println!(
        "retrieval recall@{} over {} questions: {:.2}",
        K,
        questions.len(),
        total_recall / questions.len() as f64
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
                    .search_memory(SearchQuery {
                        dialogue: q.question.clone(),
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
    let mut child = spawn_mock_meowvec(27110);
    benches();
    kill_mock_meowvec(&mut child);
}
