//! MemHop v0.7.3 Brain API Performance Benchmark
//!
//! Measures perceive / recall latency (P50/P95/P99), throughput (ops/sec),
//! and on-disk memory footprint at 1K / 5K / 10K scale.
//!
//! Run:
//!   cargo run --example benchmark --release

use std::path::Path;
use std::process::Command;
use std::time::{Duration, Instant};

use memhop::{
    Brain, BrainConfig, PerceptionInput, RecallRequest, EmotionalState, Protection,
};
use tempfile::TempDir;

const SUBJECTS: &[&str] = &[
    "Alice", "Bob", "Charlie", "Dana", "Eli", "Fiona", "Greg", "Hana", "Ivan",
    "Jules", "Kira", "Liam", "Maya", "Noah", "Olga", "Paul", "Quinn", "Riya",
    "Sam", "Tara", "Uma", "Vik", "Wren", "Xena", "Yael", "Zoe",
];
const VERBS: &[&str] = &[
    "refactored", "deployed", "debugged", "profiled", "reviewed", "merged",
    "benchmarked", "shipped", "reverted", "audited", "hardened", "rolled out",
    "investigated", "documented", "migrated", "throttled", "cached",
    "instrumented", "sharded", "upgraded", "reorganized", "validated",
];
const MODULES: &[&str] = &[
    "auth handler", "payment gateway", "billing pipeline", "search index",
    "recommendation engine", "feature flag service", "notification queue",
    "image transcoder", "audit log", "rate limiter", "telemetry agent",
    "schema registry", "event bus", "checkout flow", "session store",
    "replication slot", "job scheduler", "contact importer",
    "metrics aggregator", "webhook dispatcher", "config loader",
    "pricing service", "fraud detector", "data lake exporter",
];
const CONTEXTS: &[&str] = &[
    "after the latency spike", "before the regional rollout",
    "during the security audit", "because the SLO was missed",
    "to unblock the design partner", "following the postmortem",
    "in the staging cluster", "alongside the API redesign",
    "thanks to the canary build", "prior to the freeze",
    "with the new circuit breaker", "using the experiment cohort",
    "behind the feature flag", "once the migration finished",
    "after the chaos drill", "under the new quota policy",
];
const SUFFIXES: &[&str] = &[
    "and updated the runbook.", "and notified the on-call team.",
    "and filed an incident ticket.", "and pushed metrics to dashboards.",
    "and queued a follow-up task.", "and added regression tests.",
    "and rolled the change forward.", "and confirmed customer impact was zero.",
    "and shared findings with the SRE channel.", "and tagged the release notes.",
];

fn make_text(i: usize) -> String {
    let s = SUBJECTS[i % SUBJECTS.len()];
    let v = VERBS[(i / 3) % VERBS.len()];
    let m = MODULES[(i / 7) % MODULES.len()];
    let c = CONTEXTS[(i / 11) % CONTEXTS.len()];
    let suf = SUFFIXES[(i / 13) % SUFFIXES.len()];
    format!(
        "Memo #{}: {} {} the {} {}, version build-{}-r{} {}",
        i, s, v, m, c, i % 97, i % 31, suf,
    )
}

const RECALL_QUERIES: &[&str] = &[
    "what did Alice change in the auth handler",
    "latency spike investigation in payment gateway",
    "who deployed the search index migration",
    "recent rate limiter configuration update",
    "feature flag rollout for checkout flow",
    "chaos drill findings on session store",
    "runbook update after the security audit",
    "how was the schema registry hardened",
    "cache strategy for the recommendation engine",
    "on-call notes about the webhook dispatcher",
];

fn percentile(sorted: &[Duration], pct: usize) -> Duration {
    if sorted.is_empty() {
        return Duration::ZERO;
    }
    let idx = (sorted.len() * pct / 100).min(sorted.len() - 1);
    sorted[idx]
}

fn dur_us(d: Duration) -> f64 {
    d.as_secs_f64() * 1_000_000.0
}

fn throughput(latencies: &[Duration]) -> f64 {
    let total: Duration = latencies.iter().sum();
    if total.as_secs_f64() == 0.0 {
        return 0.0;
    }
    latencies.len() as f64 / total.as_secs_f64()
}

fn dir_size_mb(path: &Path) -> f64 {
    let output = Command::new("du")
        .arg("-sk")
        .arg(path)
        .output();
    match output {
        Ok(out) if out.status.success() => {
            let s = String::from_utf8_lossy(&out.stdout);
            let kb_str = s.split_whitespace().next().unwrap_or("0");
            let kb: u64 = kb_str.parse().unwrap_or(0);
            (kb as f64) / 1024.0
        }
        _ => 0.0,
    }
}

fn print_lat(label: &str, lats: &[Duration]) {
    let p50 = percentile(lats, 50);
    let p95 = percentile(lats, 95);
    let p99 = percentile(lats, 99);
    println!(
        "  {:<22} P50={:>8.1} µs  P95={:>8.1} µs  P99={:>8.1} µs  ({:>5} samples, {:.0} ops/s)",
        label,
        dur_us(p50),
        dur_us(p95),
        dur_us(p99),
        lats.len(),
        throughput(lats),
    );
}

fn make_perception(text: &str) -> PerceptionInput {
    PerceptionInput {
        content: text.to_string(),
        vector: vec![half::f16::from_f32(0.0); memhop::VECTOR_DIM],
        emotional_state: EmotionalState::default(),
        attention_anchors: vec![],
        perceived_importance: 0.5,
        session_id: "bench".to_string(),
        protection: Protection::Normal,
        manual_links: vec![],
        meta: std::collections::HashMap::new(),
    }
}

fn bulk_fill(brain: &mut Brain, start: usize, n: usize) {
    for i in 0..n {
        let text = make_text(start + i);
        let _ = brain.perceive(make_perception(&text)).unwrap();
    }
}

fn measured_perceive(brain: &mut Brain, start: usize, n: usize) -> Vec<Duration> {
    let mut latencies = Vec::with_capacity(n);
    for i in 0..n {
        let text = make_text(start + i);
        let start_t = Instant::now();
        brain.perceive(make_perception(&text)).unwrap();
        latencies.push(start_t.elapsed());
    }
    latencies
}

fn run_recall(brain: &Brain, queries: &[&str], rounds: usize) -> Vec<Duration> {
    let mut latencies = Vec::with_capacity(queries.len() * rounds);
    for _ in 0..rounds {
        for q in queries {
            let t = Instant::now();
            let _ = brain.recall(&RecallRequest {
                query: q.to_string(),
                session_id: "bench".to_string(),
                spread_top_k: 1,
                ..Default::default()
            });
            latencies.push(t.elapsed());
        }
    }
    latencies.sort();
    latencies
}

fn run_recall_topk(brain: &Brain, queries: &[&str], k: usize, rounds: usize) -> Vec<Duration> {
    let mut latencies = Vec::with_capacity(queries.len() * rounds);
    for _ in 0..rounds {
        for q in queries {
            let t = Instant::now();
            let _ = brain.recall(&RecallRequest {
                query: q.to_string(),
                session_id: "bench".to_string(),
                spread_top_k: k,
                ..Default::default()
            });
            latencies.push(t.elapsed());
        }
    }
    latencies.sort();
    latencies
}

fn main() {
    println!("╔══════════════════════════════════════════════════════════╗");
    println!("║   MemHop v0.7.3 Brain — Performance Benchmark           ║");
    println!("╚══════════════════════════════════════════════════════════╝\n");

    let dir = TempDir::new().expect("failed to create temp dir");
    let db_path = dir.path().join("bench.db");
    let db_path_str = db_path.to_str().expect("non-utf8 path");
    let mut brain = Brain::open(db_path_str, BrainConfig::default(), None)
        .expect("failed to open Brain");

    let queries: Vec<&str> = RECALL_QUERIES.to_vec();

    println!("━━━ Phase 1: Perceive Latency & DB Size at 1K / 5K / 10K ━━━\n");

    let perceive_cold = measured_perceive(&mut brain, 0, 200);
    let mut pc = perceive_cold.clone();
    pc.sort();
    print_lat("perceive@200 (cold)", &pc);

    bulk_fill(&mut brain, 200, 800);
    let mb_1k = dir_size_mb(&db_path);
    println!("  After  1 000 entries — disk = {:>6.2} MB", mb_1k);

    bulk_fill(&mut brain, 1_000, 4_000);
    let mb_5k = dir_size_mb(&db_path);
    println!("  After  5 000 entries — disk = {:>6.2} MB", mb_5k);

    bulk_fill(&mut brain, 5_000, 4_800);

    let perceive_10k = measured_perceive(&mut brain, 9_800, 200);
    let mb_10k = dir_size_mb(&db_path);
    println!("  After 10 000 entries — disk = {:>6.2} MB\n", mb_10k);

    let mut perceive_10k_sorted = perceive_10k.clone();
    perceive_10k_sorted.sort();
    print_lat("perceive@10K (measured)", &perceive_10k_sorted);
    println!();

    let total_stored = brain.hippocampus_len() + brain.memory_count();
    println!("  Total memories in Brain: {}\n", total_stored);

    let store_lats = perceive_10k_sorted;

    println!("━━━ Phase 2: Recall Latency @ 10K ━━━\n");

    let _ = run_recall(&brain, &queries, 1);
    let recall_lats = run_recall(&brain, &queries, 50);
    print_lat("recall (top-1)", &recall_lats);

    let recall_topk5_lats = run_recall_topk(&brain, &queries, 5, 50);
    print_lat("recall_topk (k=5)", &recall_topk5_lats);

    let recall_topk10_lats = run_recall_topk(&brain, &queries, 10, 50);
    print_lat("recall_topk (k=10)", &recall_topk10_lats);
    println!();

    println!("━━━ Phase 3: Target Verification ━━━\n");
    let store_p95_us = dur_us(percentile(&store_lats, 95));
    let recall_p95_us = dur_us(percentile(&recall_lats, 95));

    let pass = |ok: bool| if ok { "PASS" } else { "WARN" };
    println!(
        "  [{}] recall P95 < 100 µs        — actual: {:.1} µs",
        pass(recall_p95_us < 100.0),
        recall_p95_us
    );
    println!(
        "  [{}] perceive P95 < 1000 µs     — actual: {:.1} µs",
        pass(store_p95_us < 1000.0),
        store_p95_us
    );
    println!(
        "  [INFO] memory @10K = {:.2} MB (target < 70 MB)",
        mb_10k
    );
    println!();

    let store_p50 = dur_us(percentile(&store_lats, 50));
    let store_p99 = dur_us(percentile(&store_lats, 99));
    let recall_p50 = dur_us(percentile(&recall_lats, 50));
    let recall_p99 = dur_us(percentile(&recall_lats, 99));
    let topk5_p50 = dur_us(percentile(&recall_topk5_lats, 50));
    let topk5_p95 = dur_us(percentile(&recall_topk5_lats, 95));
    let topk5_p99 = dur_us(percentile(&recall_topk5_lats, 99));

    let store_ops = throughput(&store_lats);
    let recall_ops = throughput(&recall_lats);
    let topk_ops = throughput(&recall_topk5_lats);

    println!("===BENCH_JSON_BEGIN===");
    println!("{{");
    println!("  \"perceive_latency\":   {{ \"p50_us\": {:.2}, \"p95_us\": {:.2}, \"p99_us\": {:.2}, \"entries\": 10000 }},", store_p50, store_p95_us, store_p99);
    println!("  \"recall_latency\":     {{ \"p50_us\": {:.2}, \"p95_us\": {:.2}, \"p99_us\": {:.2}, \"queries\": 500 }},", recall_p50, recall_p95_us, recall_p99);
    println!("  \"recall_topk_latency\":{{ \"p50_us\": {:.2}, \"p95_us\": {:.2}, \"p99_us\": {:.2}, \"k\": 5, \"queries\": 500 }},", topk5_p50, topk5_p95, topk5_p99);
    println!("  \"memory_usage_mb\":    {{ \"1k_entries\": {:.2}, \"5k_entries\": {:.2}, \"10k_entries\": {:.2} }},", mb_1k, mb_5k, mb_10k);
    println!("  \"throughput_ops_per_sec\": {{ \"perceive\": {:.1}, \"recall\": {:.1}, \"recall_topk\": {:.1} }}", store_ops, recall_ops, topk_ops);
    println!("}}");
    println!("===BENCH_JSON_END===");

    println!("\nBenchmark complete.");
}
