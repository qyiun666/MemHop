//! MemHop Latency Benchmark — pure performance at scale.
//!
//! Measures store/recall P50/P95/P99 latency, throughput, and memory usage
//! at multiple scales (1K / 5K / 10K / 50K).
//!
//! Uses internal Ngram encoding (no ONNX dependency) for reproducible,
//! dependency-light latency measurements.
//!
//! Usage:
//!   cargo run --release --bin latency_bench -- \
//!     --scales 1000,5000,10000,50000 \
//!     --queries 50 \
//!     --output /tmp/latency_result.json

use std::path::Path;
use std::process::Command;
use std::time::{Duration, Instant};
use serde::Serialize;
use memhop::{
    Brain, BrainConfig, PerceptionInput, RecallRequest,
    EmotionalState, Protection, VECTOR_DIM,
};

// ═══════════════════════════════════════════════════════════════
//  JSON Output
// ═══════════════════════════════════════════════════════════════

#[derive(Serialize)]
struct LatencyOutput {
    scales: Vec<ScaleResult>,
}

#[derive(Serialize)]
struct ScaleResult {
    scale: usize,
    store_p50_us: f64,
    store_p95_us: f64,
    store_p99_us: f64,
    store_ops_per_sec: f64,
    recall_p50_us: f64,
    recall_p95_us: f64,
    recall_p99_us: f64,
    recall_ops_per_sec: f64,
    disk_size_mb: f64,
    total_memories: usize,
}

// ═══════════════════════════════════════════════════════════════
//  Text generation
// ═══════════════════════════════════════════════════════════════

const SUBJECTS: &[&str] = &[
    "Alice", "Bob", "Charlie", "Dana", "Eli", "Fiona", "Greg", "Hana",
    "Ivan", "Jules", "Kira", "Liam", "Maya", "Noah",
];

const VERBS: &[&str] = &[
    "refactored", "deployed", "debugged", "profiled", "reviewed",
    "merged", "shipped", "reverted", "audited", "hardened",
    "investigated", "documented", "migrated", "optimized",
];

const MODULES: &[&str] = &[
    "auth handler", "payment gateway", "search index", "rate limiter",
    "telemetry agent", "schema registry", "event bus", "checkout flow",
    "session store", "config loader", "fraud detector", "cache layer",
];

fn make_text(i: usize) -> String {
    let s = SUBJECTS[i % SUBJECTS.len()];
    let v = VERBS[(i / 3) % VERBS.len()];
    let m = MODULES[(i / 7) % MODULES.len()];
    format!("Memo #{}: {} {} the {}. build-{}-r{}", i, s, v, m, i % 97, i % 31)
}

// ═══════════════════════════════════════════════════════════════
//  Helpers
// ═══════════════════════════════════════════════════════════════

fn percentile(sorted: &[Duration], pct: usize) -> Duration {
    if sorted.is_empty() { return Duration::ZERO; }
    let idx = (sorted.len() * pct / 100).min(sorted.len() - 1);
    sorted[idx]
}

fn dur_us(d: Duration) -> f64 { d.as_secs_f64() * 1_000_000.0 }

fn throughput(lats: &[Duration]) -> f64 {
    let total: Duration = lats.iter().sum();
    if total.as_secs_f64() == 0.0 { return 0.0; }
    lats.len() as f64 / total.as_secs_f64()
}

fn dir_size_mb(path: &Path) -> f64 {
    if let Ok(out) = Command::new("du").arg("-sk").arg(path).output() {
        if out.status.success() {
            let s = String::from_utf8_lossy(&out.stdout);
            if let Some(n) = s.split_whitespace().next().and_then(|n| n.parse::<u64>().ok()) {
                return n as f64 / 1024.0;
            }
        }
    }
    0.0
}

fn make_perception(text: &str) -> PerceptionInput {
    PerceptionInput {
        content: text.to_string(),
        vector: vec![half::f16::from_f32(0.0); VECTOR_DIM],
        emotional_state: EmotionalState::default(),
        attention_anchors: vec![],
        perceived_importance: 0.5,
        session_id: "bench".to_string(),
        protection: Protection::Normal,
        manual_links: vec![],
        meta: std::collections::HashMap::new(),
        plan_id: None,
        agent_response: None,
        dialogue_timestamp: None,
        source: None,
    }
}

/// Generate diverse recall queries
fn make_query(i: usize) -> String {
    let s = SUBJECTS[(i * 7 + 3) % SUBJECTS.len()];
    let m = MODULES[(i * 3 + 2) % MODULES.len()];
    format!("what did {} change in the {}", s, m)
}

// ═══════════════════════════════════════════════════════════════
//  Main
// ═══════════════════════════════════════════════════════════════

fn main() {
    let args: Vec<String> = std::env::args().collect();
    let scales: Vec<usize> = args.iter().position(|a| a == "--scales")
        .and_then(|i| args.get(i + 1))
        .map(|s| s.split(',').filter_map(|n| n.parse().ok()).collect())
        .unwrap_or_else(|| vec![1000, 5000, 10000]);
    let num_queries: usize = args.iter().position(|a| a == "--queries")
        .and_then(|i| args.get(i + 1).and_then(|n| n.parse().ok()))
        .unwrap_or(50);
    let output_path: Option<String> = args.iter().position(|a| a == "--output")
        .and_then(|i| args.get(i + 1).cloned());
    let db_dir: String = args.iter().position(|a| a == "--db-dir")
        .and_then(|i| args.get(i + 1).cloned())
        .unwrap_or_else(|| std::env::temp_dir().to_string_lossy().to_string());

    println!("╔══════════════════════════════════════════════════╗");
    println!("║  MemHop Latency Benchmark                       ║");
    println!("║  Scales: {:?}            ║", scales);
    println!("║  Queries per scale: {}                           ║", num_queries);
    println!("╚══════════════════════════════════════════════════╝\n");

    let mut output = LatencyOutput { scales: Vec::new() };

    for &scale in &scales {
        println!("━━━ Scale: {} ━━━", scale);

        let d = std::path::PathBuf::from(&db_dir)
            .join(format!("memhop_lat_{}_{}", scale, std::process::id()));
        let _ = std::fs::remove_dir_all(&d);
        std::fs::create_dir_all(&d).unwrap();
        let db_path = d.join("lat_bench.db");
        let mut brain = Brain::open(
            db_path.to_str().unwrap(),
            BrainConfig {
                dream_interval: 100000, // disabled
                hippocampus_capacity: (scale * 2).max(1000),
                ..Default::default()
            },
            None,
        ).expect("Brain::open");

        // ── Store ──
        let mut store_lats: Vec<Duration> = Vec::with_capacity(scale);
        for i in 0..scale {
            let text = make_text(i);
            let t = Instant::now();
            brain.perceive(make_perception(&text)).expect("perceive");
            store_lats.push(t.elapsed());
        }
        store_lats.sort();

        let store_p50 = dur_us(percentile(&store_lats, 50));
        let store_p95 = dur_us(percentile(&store_lats, 95));
        let store_p99 = dur_us(percentile(&store_lats, 99));
        let store_ops = throughput(&store_lats);

        println!("  Store:  P50={:>8.1} µs  P95={:>8.1} µs  P99={:>8.1} µs  {:.0} ops/s",
            store_p50, store_p95, store_p99, store_ops);

        // ── Recall ──
        let queries: Vec<String> = (0..num_queries).map(|i| make_query(i)).collect();
        let mut recall_lats: Vec<Duration> = Vec::with_capacity(num_queries);

        for q_text in &queries {
            let t = Instant::now();
            brain.recall(&RecallRequest {
                query: q_text.clone(),
                session_id: "bench".to_string(),
                spread_top_k: 10,
                limit: 10,
                ..Default::default()
            }).expect("recall");
            recall_lats.push(t.elapsed());
        }
        recall_lats.sort();

        let recall_p50 = dur_us(percentile(&recall_lats, 50));
        let recall_p95 = dur_us(percentile(&recall_lats, 95));
        let recall_p99 = dur_us(percentile(&recall_lats, 99));
        let recall_ops = throughput(&recall_lats);

        println!("  Recall: P50={:>8.1} µs  P95={:>8.1} µs  P99={:>8.1} µs  {:.0} ops/s",
            recall_p50, recall_p95, recall_p99, recall_ops);

        // ── Memory ──
        let disk_mb = dir_size_mb(&d);
        let total = brain.hippocampus_len() + brain.memory_count();
        println!("  Disk: {:.1} MB | Total memories: {}\n", disk_mb, total);

        output.scales.push(ScaleResult {
            scale,
            store_p50_us: store_p50,
            store_p95_us: store_p95,
            store_p99_us: store_p99,
            store_ops_per_sec: store_ops,
            recall_p50_us: recall_p50,
            recall_p95_us: recall_p95,
            recall_p99_us: recall_p99,
            recall_ops_per_sec: recall_ops,
            disk_size_mb: disk_mb,
            total_memories: total,
        });
    }

    // ── Output ──
    if let Some(path) = &output_path {
        let json = serde_json::to_string_pretty(&output).expect("serialize");
        std::fs::write(path, &json).expect("write");
        println!("\nResults written to: {}", path);
    }

    // Summary table
    println!("\n╔══════════════════════════════════════════════════════════════════╗");
    println!("║  Summary                                                        ║");
    println!("╠════════╦════════════════════════════════════════════════════════╣");
    println!("║ Scale  ║  Store P95 / P99     Recall P95 / P99     Disk       ║");
    println!("╠════════╬════════════════════════════════════════════════════════╣");
    for r in &output.scales {
        println!("║ {:>4}K  ║  {:>6.0} / {:>6.0} µs   {:>6.0} / {:>6.0} µs   {:>6.1} MB  ║",
            r.scale / 1000, r.store_p95_us, r.store_p99_us,
            r.recall_p95_us, r.recall_p99_us, r.disk_size_mb);
    }
    println!("╚════════╩════════════════════════════════════════════════════════╝");
    println!("\nBenchmark complete.");
}
