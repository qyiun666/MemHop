//! LongMemEval-S Benchmark for MemHop v0.9.0.
//!
//! Standard recall benchmark across 5 capabilities: extraction, reasoning,
//! temporal, update, abstain.
//!
//! Usage:
//!   cargo run --release --bin longmemeval_bench -- \
//!     --input /tmp/longmemeval_s.json \
//!     [--db-dir /tmp/memhop_bench]
//!
//! Input format:
//!   {
//!     "name": "longmemeval-s",
//!     "problems": [
//!       {
//!         "id": "problem_001",
//!         "sessions": [...],
//!         "questions": [...]
//!       }
//!     ]
//!   }

use std::collections::HashMap;
use std::time::Instant;

use half::f16;
use serde::Deserialize;
use memhop::{
    Brain, BrainConfig, EmotionalState, PerceptionInput, Protection, RecallRequest, VECTOR_DIM,
};
use memhop::encoder::{Encoder, NgramEncoder};

// ═══════════════════════════════════════════════════════════════
//  JSON input structures
// ═══════════════════════════════════════════════════════════════

#[derive(Deserialize)]
struct LongMemEvalInput {
    name: String,
    problems: Vec<Problem>,
}

#[derive(Deserialize)]
struct Problem {
    id: String,
    sessions: Vec<Session>,
    questions: Vec<Question>,
}

#[derive(Deserialize)]
struct Session {
    session_id: String,
    turns: Vec<Turn>,
}

#[derive(Deserialize)]
struct Turn {
    speaker: String,
    text: String,
}

#[derive(Deserialize)]
struct Question {
    id: String,
    question: String,
    answer: String,
    #[serde(rename = "type")]
    q_type: String,
}

// ═══════════════════════════════════════════════════════════════
//  Result structures
// ═══════════════════════════════════════════════════════════════

#[derive(Debug)]
struct QuestionResult {
    q_id: String,
    q_type: String,
    /// Position in combined ranked list (working_memory + associations).
    /// None means not found.
    found_at: Option<usize>,
    latency_us: u64,
}

#[derive(Debug)]
struct ProblemResult {
    id: String,
    results: Vec<QuestionResult>,
}

#[derive(Debug)]
struct Metrics {
    total_problems: usize,
    total_questions: usize,
    recall_1: f64,
    recall_5: f64,
    recall_10: f64,
    avg_latency_us: f64,
    by_type: HashMap<String, TypeMetrics>,
}

#[derive(Debug)]
struct TypeMetrics {
    count: usize,
    correct: usize,
}

// ═══════════════════════════════════════════════════════════════
//  Helpers
// ═══════════════════════════════════════════════════════════════

fn make_perception(text: &str, vector: Vec<f16>, session_id: &str) -> PerceptionInput {
    PerceptionInput {
        content: text.to_string(),
        vector,
        emotional_state: EmotionalState::default(),
        attention_anchors: vec![],
        perceived_importance: 0.5,
        session_id: session_id.to_string(),
        protection: Protection::Normal,
        manual_links: vec![],
        meta: HashMap::new(),
        plan_id: None,
        agent_response: None,
        dialogue_timestamp: None,
        source: None,
        turn_id: String::new(),
        turn_index: 0,
        segment_index: 0,
        topic_label: None,
    }
}

/// Run a single problem through the Brain pipeline.
fn run_problem(
    problem: &Problem,
    ngram_enc: &NgramEncoder,
    db_base: &str,
) -> ProblemResult {
    let db_path = format!("{}/longmemeval_{}", db_base, problem.id);
    let _ = std::fs::remove_dir_all(&db_path);
    std::fs::create_dir_all(&db_path).unwrap_or_else(|e| {
        panic!("failed to create dir {}: {}", db_path, e);
    });

    let mut brain = Brain::open(
        &format!("{}/brain.db", db_path),
        BrainConfig {
            dream_interval: 50,
            hippocampus_capacity: 1000,
            ..Default::default()
        },
        None,
    )
    .expect("Brain::open");

    // Store all assistant turns as memories
    for session in &problem.sessions {
        for turn in &session.turns {
            if turn.speaker == "assistant" {
                let enc = ngram_enc.encode(&turn.text);
                let input = make_perception(&turn.text, enc.dense, &session.session_id);
                brain.perceive(input).expect("perceive");
            }
        }
    }

    // Run dream cycle to consolidate
    let _ = brain.dream();

    // Answer each question
    let mut results = Vec::with_capacity(problem.questions.len());
    for q in &problem.questions {
        if q.answer.is_empty() {
            // Skip empty answers (would falsely match everything)
            results.push(QuestionResult {
                q_id: q.id.clone(),
                q_type: q.q_type.clone(),
                found_at: None,
                latency_us: 0,
            });
            continue;
        }

        let t = Instant::now();
        let q_vec = ngram_enc.encode(&q.question).dense;
        let resp = brain
            .recall(&RecallRequest {
                query: q.question.clone(),
                query_vector: Some(q_vec),
                session_id: String::new(),
                limit: 10,
                ..Default::default()
            })
            .expect("recall");
        let latency = t.elapsed();

        let answer_lower = q.answer.to_lowercase();
        let mut found_at: Option<usize> = None;

        // Check working_memory first (higher priority, top results)
        for (i, e) in resp.working_memory.iter().enumerate() {
            if e.text.to_lowercase().contains(&answer_lower) {
                found_at = Some(i);
                break;
            }
        }

        // If not found in working_memory, check associations
        if found_at.is_none() {
            for (i, e) in resp.associations.iter().enumerate() {
                if e.text.to_lowercase().contains(&answer_lower) {
                    found_at = Some(resp.working_memory.len() + i);
                    break;
                }
            }
        }

        results.push(QuestionResult {
            q_id: q.id.clone(),
            q_type: q.q_type.clone(),
            found_at,
            latency_us: latency.as_micros() as u64,
        });
    }

    // Cleanup
    let _ = std::fs::remove_dir_all(&db_path);

    ProblemResult {
        id: problem.id.clone(),
        results,
    }
}

/// Compute aggregate metrics from all problem results.
fn compute_metrics(results: &[ProblemResult]) -> Metrics {
    let mut total_questions = 0;
    let mut found_1 = 0usize;
    let mut found_5 = 0usize;
    let mut found_10 = 0usize;
    let mut total_latency: u64 = 0;
    let mut by_type: HashMap<String, TypeMetrics> = HashMap::new();

    for pr in results {
        for qr in &pr.results {
            total_questions += 1;
            total_latency += qr.latency_us;

            let tm = by_type
                .entry(qr.q_type.clone())
                .or_insert_with(|| TypeMetrics {
                    count: 0,
                    correct: 0,
                });
            tm.count += 1;

            if let Some(pos) = qr.found_at {
                found_10 += 1;
                if pos < 5 {
                    found_5 += 1;
                }
                if pos < 1 {
                    found_1 += 1;
                }
                tm.correct += 1;
            }
        }
    }

    let n = total_questions.max(1) as f64;
    Metrics {
        total_problems: results.len(),
        total_questions,
        recall_1: found_1 as f64 / n * 100.0,
        recall_5: found_5 as f64 / n * 100.0,
        recall_10: found_10 as f64 / n * 100.0,
        avg_latency_us: total_latency as f64 / n,
        by_type,
    }
}

/// Format microseconds as a human-friendly string (ms or us).
fn fmt_latency(us: f64) -> String {
    if us >= 1000.0 {
        format!("{:.1}ms", us / 1000.0)
    } else {
        format!("{:.0}us", us)
    }
}

// ═══════════════════════════════════════════════════════════════
//  Display
// ═══════════════════════════════════════════════════════════════

fn print_results(metrics: &Metrics) {
    println!("╔══════════════════════════════════════════════════════════════════╗");
    println!("║  LongMemEval-S Benchmark                                        ║");
    println!("╠══════════════════════════════════════════════════════════════════╣");
    println!(
        "║  Problems:  {:>4}                                                ║",
        metrics.total_problems
    );
    println!(
        "║  Questions: {:>4}                                                ║",
        metrics.total_questions
    );
    println!("╠══════════════════════════════════════════════════════════════════╣");
    println!("║  Metric              Score                                      ║");
    println!("╠══════════════════════════════════════════════════════════════════╣");
    println!(
        "║  Recall@1            {:>5.1}%                                    ║",
        metrics.recall_1
    );
    println!(
        "║  Recall@5            {:>5.1}%                                    ║",
        metrics.recall_5
    );
    println!(
        "║  Recall@10           {:>5.1}%                                    ║",
        metrics.recall_10
    );
    println!(
        "║  Avg Latency         {:>10}                                ║",
        fmt_latency(metrics.avg_latency_us)
    );
    println!("╠══════════════════════════════════════════════════════════════════╣");
    println!("║  By Type:                                                       ║");

    let mut types: Vec<(&String, &TypeMetrics)> = metrics.by_type.iter().collect();
    types.sort_by(|a, b| a.0.cmp(b.0));
    for (t, tm) in &types {
        let pct = tm.correct as f64 / tm.count.max(1) as f64 * 100.0;
        println!(
            "║    {:<20} {:>3}/{:>3} ({:>5.1}%)                           ║",
            t, tm.correct, tm.count, pct
        );
    }
    println!("╚══════════════════════════════════════════════════════════════════╝");
}

// ═══════════════════════════════════════════════════════════════
//  Main
// ═══════════════════════════════════════════════════════════════

fn main() {
    let args: Vec<String> = std::env::args().collect();
    let input_path = args
        .iter()
        .position(|a| a == "--input")
        .and_then(|i| args.get(i + 1).cloned())
        .expect("Missing --input <path>");
    let db_base: String = args
        .iter()
        .position(|a| a == "--db-dir")
        .and_then(|i| args.get(i + 1).cloned())
        .unwrap_or_else(|| {
            std::env::temp_dir()
                .join("memhop_longmemeval")
                .to_string_lossy()
                .to_string()
        });

    eprintln!("LongMemEval-S Benchmark");
    eprintln!("  input:  {}", input_path);
    eprintln!("  db-dir: {}", db_base);

    // Load input
    let raw = std::fs::read_to_string(&input_path)
        .unwrap_or_else(|e| panic!("failed to read input file '{}': {}", input_path, e));
    let input: LongMemEvalInput =
        serde_json::from_str(&raw).unwrap_or_else(|e| panic!("failed to parse input JSON: {}", e));

    eprintln!(
        "  dataset: {} ({} problems)",
        input.name,
        input.problems.len()
    );

    let ngram_enc = NgramEncoder::new(VECTOR_DIM);

    // Run each problem sequentially
    let mut problem_results = Vec::with_capacity(input.problems.len());
    for problem in &input.problems {
        eprintln!("  running {} ...", problem.id);
        let pr = run_problem(problem, &ngram_enc, &db_base);
        problem_results.push(pr);
    }

    let metrics = compute_metrics(&problem_results);
    print_results(&metrics);
}
