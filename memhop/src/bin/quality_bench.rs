//! MemHop Quality Benchmark — standard IR evaluation with pre-encoded vectors.
//!
//! Accepts JSON input (documents, queries, qrels) and outputs JSON results.
//! Uses BGE-M3 ONNX for semantic encoding, with Ngram and zero-vector baselines.
//!
//! Usage:
//!   cargo run --release --features onnx --bin quality_bench -- \
//!     --input /tmp/bench_input.json \
//!     --output /tmp/bench_result.json
//!
//! Input format:
//!   {
//!     "name": "dataset-name",
//!     "documents": [{"id": "d1", "text": "...", "vector": [...]}, ...],
//!     "queries": [{"id": "q1", "text": "...", "vector": [...]}, ...],
//!     "qrels": {"q1": {"d1": 1}, ...},
//!     "dream_interval": 50,
//!     "spread_top_k": 10,
//!     "limit": 10
//!   }

use std::collections::{HashMap, HashSet};
use std::path::Path;
use std::process::Command;
use std::time::Instant;

use half::f16;
use serde::{Deserialize, Serialize};
use memhop::encoder::{Encoder, HybridEncoder, NgramEncoder};
#[cfg(feature = "onnx")]
use memhop::encoder::OnnxEncoder;
use memhop::{
    Brain, BrainConfig, EmotionalState, PerceptionInput, Protection, RecallRequest, VECTOR_DIM,
};

// ═══════════════════════════════════════════════════════════════
//  JSON I/O structures
// ═══════════════════════════════════════════════════════════════

#[derive(Deserialize)]
struct BenchInput {
    name: String,
    documents: Vec<DocInput>,
    queries: Vec<QueryInput>,
    qrels: HashMap<String, HashMap<String, i32>>,
    #[serde(default = "default_dream_interval")]
    dream_interval: usize,
    #[serde(default = "default_spread_top_k")]
    spread_top_k: usize,
    #[serde(default = "default_limit")]
    limit: usize,
}

fn default_dream_interval() -> usize { 50 }
fn default_spread_top_k() -> usize { 10 }
fn default_limit() -> usize { 10 }

#[derive(Deserialize)]
struct DocInput {
    id: String,
    text: String,
    vector: Option<Vec<f32>>,
}

#[derive(Deserialize)]
struct QueryInput {
    id: String,
    text: String,
    vector: Option<Vec<f32>>,
}

#[derive(Serialize)]
struct BenchOutput {
    dataset: String,
    num_docs: usize,
    num_queries: usize,
    results: Vec<MethodResult>,
}

#[derive(Serialize)]
struct MethodResult {
    method: String,
    ndcg_10: Stats,
    mrr: Stats,
    recall_1: Stats,
    recall_5: Stats,
    recall_10: Stats,
    precision_10: Stats,
    avg_recall_latency_us: f64,
}

#[derive(Serialize)]
struct Stats {
    mean: f64,
    std: f64,
}

// ═══════════════════════════════════════════════════════════════
//  Metrics (mirrors peers in Python metrics.py)
// ═══════════════════════════════════════════════════════════════

fn ndcg_at_k(ranked: &[String], relevant: &HashSet<&str>, k: usize) -> f64 {
    let cutoff = ranked.len().min(k);
    if cutoff == 0 { return 0.0; }

    let mut dcg = 0.0;
    for (pos, id) in ranked.iter().enumerate().take(cutoff) {
        let rel = if relevant.contains(id.as_str()) { 1.0 } else { 0.0 };
        if pos == 0 {
            dcg += rel;
        } else {
            dcg += rel / ((pos + 1) as f64).log2();
        }
    }

    let total_relevant = relevant.len().min(cutoff);
    let mut idcg = 0.0;
    for pos in 0..total_relevant {
        if pos == 0 {
            idcg += 1.0;
        } else {
            idcg += 1.0 / ((pos + 1) as f64).log2();
        }
    }
    if idcg == 0.0 { return 0.0; }
    dcg / idcg
}

fn recall_at_k(ranked: &[String], relevant: &HashSet<&str>, k: usize) -> f64 {
    if relevant.is_empty() { return 0.0; }
    let found = ranked.iter().take(k).filter(|id| relevant.contains(id.as_str())).count();
    found as f64 / relevant.len() as f64
}

fn precision_at_k(ranked: &[String], relevant: &HashSet<&str>, k: usize) -> f64 {
    if k == 0 { return 0.0; }
    let found = ranked.iter().take(k).filter(|id| relevant.contains(id.as_str())).count();
    found as f64 / k as f64
}

fn mrr_score(ranked: &[String], relevant: &HashSet<&str>) -> f64 {
    for (pos, id) in ranked.iter().enumerate() {
        if relevant.contains(id.as_str()) {
            return 1.0 / (pos + 1) as f64;
        }
    }
    0.0
}

fn stats(values: &[f64]) -> Stats {
    let n = values.len().max(1) as f64;
    let mean = values.iter().sum::<f64>() / n;
    let variance = if values.len() > 1 {
        values.iter().map(|x| (x - mean).powi(2)).sum::<f64>() / (values.len() - 1) as f64
    } else {
        0.0
    };
    Stats { mean, std: variance.sqrt() }
}

// ═══════════════════════════════════════════════════════════════
//  Helpers
// ═══════════════════════════════════════════════════════════════

fn make_perception(text: &str, vector: Vec<f16>) -> PerceptionInput {
    PerceptionInput {
        content: text.to_string(),
        vector,
        emotional_state: EmotionalState::default(),
        attention_anchors: vec![],
        perceived_importance: 0.5,
        session_id: "bench".to_string(),
        protection: Protection::Normal,
        manual_links: vec![],
        meta: HashMap::new(),
        plan_id: None,
        agent_response: None,
        dialogue_timestamp: None,
        source: None,
    }
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

// ═══════════════════════════════════════════════════════════════
//  Main
// ═══════════════════════════════════════════════════════════════

fn main() {
    // Parse CLI args
    let args: Vec<String> = std::env::args().collect();
    let input_path = args.iter().position(|a| a == "--input")
        .and_then(|i| args.get(i + 1).cloned())
        .expect("Missing --input <path>");
    let output_path = args.iter().position(|a| a == "--output")
        .and_then(|i| args.get(i + 1).cloned())
        .expect("Missing --output <path>");
    let db_dir: String = args.iter().position(|a| a == "--db-dir")
        .and_then(|i| args.get(i + 1).cloned())
        .unwrap_or_else(|| std::env::temp_dir().to_string_lossy().to_string());
    let mode_str: String = args.iter().position(|a| a == "--mode")
        .and_then(|i| args.get(i + 1).cloned())
        .unwrap_or_else(|| "retrieval".to_string());
    let mode = match mode_str.as_str() {
        "retrieval" | "r" => memhop::RecallMode::Retrieval,
        "associative" | "a" => memhop::RecallMode::Associative,
        _ => {
            eprintln!("Unknown mode '{}', defaulting to Retrieval", mode_str);
            memhop::RecallMode::Retrieval
        }
    };

    eprintln!("MemHop Quality Benchmark");
    eprintln!("  input:  {}", input_path);
    eprintln!("  output: {}", output_path);
    eprintln!("  mode:   {:?}", mode);

    // Load input
    let raw = std::fs::read_to_string(&input_path).expect("read input JSON");
    let input: BenchInput = serde_json::from_str(&raw).expect("parse input JSON");

    eprintln!("  dataset: {} ({} docs, {} queries)",
        input.name, input.documents.len(), input.queries.len());

    // Determine whether pre-computed vectors are provided
    let has_vectors = input.documents.iter().any(|d| d.vector.is_some());

    // Load encoders — only load ONNX when we need it for encoding fallback
    let hybrid: Option<HybridEncoder> = if has_vectors {
        eprintln!("  using pre-computed vectors, skipping ONNX encoder");
        None
    } else {
        #[cfg(feature = "onnx")]
        {
            let onnx_enc = OnnxEncoder::from_path("models/bge-m3")
                .expect("failed to load BGE-M3 model from models/bge-m3");
            eprintln!("  encoder: BGE-M3 ONNX loaded");
            Some(HybridEncoder::with_secondary(
                NgramEncoder::new(VECTOR_DIM),
                Box::new(onnx_enc),
            ).with_weights(0.3, 0.7))
        }
        #[cfg(not(feature = "onnx"))]
        {
            eprintln!("  encoder: Ngram-only (ONNX feature disabled)");
            Some(HybridEncoder::new(NgramEncoder::new(VECTOR_DIM)))
        }
    };

    // Pre-encode documents
    let doc_embeddings: Vec<Vec<f16>> = if has_vectors {
        input.documents.iter().map(|d| {
            d.vector.as_ref().map(|v| v.iter().map(|x| f16::from_f32(*x)).collect())
                .unwrap_or_else(|| {
                    hybrid.as_ref().expect("no encoder available").encode(&d.text).dense
                })
        }).collect()
    } else {
        let enc = hybrid.as_ref().expect("no encoder available");
        input.documents.iter().map(|d| enc.encode(&d.text).dense).collect()
    };

    eprintln!("  encoded {} documents", doc_embeddings.len());

    // Pre-encode queries
    let query_embeddings: Vec<Vec<f16>> = if has_vectors {
        input.queries.iter().map(|q| {
            q.vector.as_ref().map(|v| v.iter().map(|x| f16::from_f32(*x)).collect())
                .unwrap_or_else(|| {
                    hybrid.as_ref().expect("no encoder available").encode(&q.text).dense
                })
        }).collect()
    } else {
        let enc = hybrid.as_ref().expect("no encoder available");
        input.queries.iter().map(|q| enc.encode(&q.text).dense).collect()
    };

    // Build relevance sets
    let relevance_sets: Vec<HashSet<&str>> = input.queries.iter().map(|q| {
        let mut s = HashSet::new();
        if let Some(rel_docs) = input.qrels.get(&q.id) {
            for doc_id in rel_docs.keys() {
                s.insert(doc_id.as_str());
            }
        }
        s
    }).collect();

    // ═══════════════════════════════════════════════════════
    //  Run benchmarks: ONNX, Ngram, Zero-vector
    // ═══════════════════════════════════════════════════════

    let mut output = BenchOutput {
        dataset: input.name.clone(),
        num_docs: input.documents.len(),
        num_queries: input.queries.len(),
        results: Vec::new(),
    };

    // ── Run: ONNX+BGE-M3 (pre-encoded vectors) ──
    {
        let d = std::path::PathBuf::from(&db_dir)
            .join(format!("memhop_qual_onnx_{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&d);
        std::fs::create_dir_all(&d).unwrap();
        let db_path = d.join("qual_bench.db");
        let mut brain = Brain::open(
            db_path.to_str().unwrap(),
            BrainConfig {
                dream_interval: input.dream_interval,
                hippocampus_capacity: (input.documents.len() * 2).max(1000),
                ..Default::default()
            },
            None,
        ).expect("Brain::open");

        // Store
        let mut id_map: HashMap<usize, String> = HashMap::new(); // doc_idx -> engram_id
        for (i, doc) in input.documents.iter().enumerate() {
            let out = brain.perceive(make_perception(&doc.text, doc_embeddings[i].clone()))
                .expect("perceive");
            id_map.insert(i, out.engram_id);
        }

        let disk_mb = dir_size_mb(&d);
        eprintln!("  stored {} docs, disk={:.1} MB", input.documents.len(), disk_mb);

        // Run queries
        let mut ndcg_scores = Vec::with_capacity(input.queries.len());
        let mut mrr_scores = Vec::with_capacity(input.queries.len());
        let mut r1_scores = Vec::with_capacity(input.queries.len());
        let mut r5_scores = Vec::with_capacity(input.queries.len());
        let mut r10_scores = Vec::with_capacity(input.queries.len());
        let mut p10_scores = Vec::with_capacity(input.queries.len());
        let mut latencies = Vec::with_capacity(input.queries.len());

        for (q_idx, query) in input.queries.iter().enumerate() {
            let rel_set = &relevance_sets[q_idx];

            let t = Instant::now();
            let resp = brain.recall(&RecallRequest {
                query: query.text.clone(),
                query_vector: Some(query_embeddings[q_idx].clone()),
                session_id: "bench".to_string(),
                spread_top_k: input.spread_top_k,
                limit: input.limit,
                ..Default::default()
            }).expect("recall");
            latencies.push(t.elapsed().as_micros() as f64);

            // Map engram_id -> doc_id
            let ranked: Vec<String> = resp.working_memory.iter()
                .chain(resp.associations.iter())
                .filter_map(|e| {
                    id_map.iter()
                        .find(|(_, v)| *v == &e.id)
                        .map(|(k, _)| input.documents[*k].id.clone())
                })
                .collect();

            ndcg_scores.push(ndcg_at_k(&ranked, rel_set, 10));
            mrr_scores.push(mrr_score(&ranked, rel_set));
            r1_scores.push(recall_at_k(&ranked, rel_set, 1));
            r5_scores.push(recall_at_k(&ranked, rel_set, 5));
            r10_scores.push(recall_at_k(&ranked, rel_set, 10));
            p10_scores.push(precision_at_k(&ranked, rel_set, 10));
        }

        let avg_lat = latencies.iter().sum::<f64>() / latencies.len().max(1) as f64;
        output.results.push(MethodResult {
            method: "ONNX+BGE-M3".to_string(),
            ndcg_10: stats(&ndcg_scores),
            mrr: stats(&mrr_scores),
            recall_1: stats(&r1_scores),
            recall_5: stats(&r5_scores),
            recall_10: stats(&r10_scores),
            precision_10: stats(&p10_scores),
            avg_recall_latency_us: avg_lat,
        });
    }

    // ── Run: Ngram-only baseline ──
    {
        let d = std::path::PathBuf::from(&db_dir)
            .join(format!("memhop_qual_ng_{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&d);
        std::fs::create_dir_all(&d).unwrap();
        let db_path = d.join("qual_bench_ng.db");
        let mut brain = Brain::open(
            db_path.to_str().unwrap(),
            BrainConfig {
                dream_interval: 0, // disabled for Ngram baseline
                hippocampus_capacity: (input.documents.len() * 2).max(1000),
                ..Default::default()
            },
            None,
        ).expect("Brain::open");

        let ngram_enc = NgramEncoder::new(VECTOR_DIM);

        // Store with Ngram encoding
        let mut id_map: HashMap<usize, String> = HashMap::new();
        for (i, doc) in input.documents.iter().enumerate() {
            let vec = ngram_enc.encode(&doc.text).dense;
            let out = brain.perceive(make_perception(&doc.text, vec))
                .expect("perceive");
            id_map.insert(i, out.engram_id);
        }

        // Run queries
        let mut ndcg_scores = Vec::with_capacity(input.queries.len());
        let mut mrr_scores = Vec::with_capacity(input.queries.len());
        let mut r1_scores = Vec::with_capacity(input.queries.len());
        let mut r5_scores = Vec::with_capacity(input.queries.len());
        let mut r10_scores = Vec::with_capacity(input.queries.len());
        let mut p10_scores = Vec::with_capacity(input.queries.len());

        for (q_idx, query) in input.queries.iter().enumerate() {
            let rel_set = &relevance_sets[q_idx];
            let q_vec = ngram_enc.encode(&query.text).dense;

            let resp = brain.recall(&RecallRequest {
                query: query.text.clone(),
                query_vector: Some(q_vec),
                session_id: "bench".to_string(),
                spread_top_k: input.spread_top_k,
                limit: input.limit,
                ..Default::default()
            }).expect("recall");

            let ranked: Vec<String> = resp.working_memory.iter()
                .chain(resp.associations.iter())
                .filter_map(|e| {
                    id_map.iter()
                        .find(|(_, v)| *v == &e.id)
                        .map(|(k, _)| input.documents[*k].id.clone())
                })
                .collect();

            ndcg_scores.push(ndcg_at_k(&ranked, rel_set, 10));
            mrr_scores.push(mrr_score(&ranked, rel_set));
            r1_scores.push(recall_at_k(&ranked, rel_set, 1));
            r5_scores.push(recall_at_k(&ranked, rel_set, 5));
            r10_scores.push(recall_at_k(&ranked, rel_set, 10));
            p10_scores.push(precision_at_k(&ranked, rel_set, 10));
        }

        output.results.push(MethodResult {
            method: "Ngram".to_string(),
            ndcg_10: stats(&ndcg_scores),
            mrr: stats(&mrr_scores),
            recall_1: stats(&r1_scores),
            recall_5: stats(&r5_scores),
            recall_10: stats(&r10_scores),
            precision_10: stats(&p10_scores),
            avg_recall_latency_us: 0.0,
        });
    }

    // ── Run: Zero-vector baseline ──
    {
        let d = std::path::PathBuf::from(&db_dir)
            .join(format!("memhop_qual_zero_{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&d);
        std::fs::create_dir_all(&d).unwrap();
        let db_path = d.join("qual_bench_zero.db");
        let mut brain = Brain::open(
            db_path.to_str().unwrap(),
            BrainConfig {
                dream_interval: 0,
                hippocampus_capacity: (input.documents.len() * 2).max(1000),
                ..Default::default()
            },
            None,
        ).expect("Brain::open");

        let zero = vec![f16::from_f32(0.0); VECTOR_DIM];

        // Store with zero vectors
        let mut id_map: HashMap<usize, String> = HashMap::new();
        for (i, doc) in input.documents.iter().enumerate() {
            let out = brain.perceive(make_perception(&doc.text, zero.clone()))
                .expect("perceive");
            id_map.insert(i, out.engram_id);
        }

        // Run queries
        let mut ndcg_scores = Vec::with_capacity(input.queries.len());
        let mut mrr_scores = Vec::with_capacity(input.queries.len());
        let mut r1_scores = Vec::with_capacity(input.queries.len());
        let mut r5_scores = Vec::with_capacity(input.queries.len());
        let mut r10_scores = Vec::with_capacity(input.queries.len());
        let mut p10_scores = Vec::with_capacity(input.queries.len());

        for (q_idx, query) in input.queries.iter().enumerate() {
            let rel_set = &relevance_sets[q_idx];

            let resp = brain.recall(&RecallRequest {
                query: query.text.clone(),
                query_vector: Some(zero.clone()),
                session_id: "bench".to_string(),
                spread_top_k: input.spread_top_k,
                limit: input.limit,
                mode,
                ..Default::default()
            }).expect("recall");

            let ranked: Vec<String> = resp.working_memory.iter()
                .chain(resp.associations.iter())
                .filter_map(|e| {
                    id_map.iter()
                        .find(|(_, v)| *v == &e.id)
                        .map(|(k, _)| input.documents[*k].id.clone())
                })
                .collect();

            ndcg_scores.push(ndcg_at_k(&ranked, rel_set, 10));
            mrr_scores.push(mrr_score(&ranked, rel_set));
            r1_scores.push(recall_at_k(&ranked, rel_set, 1));
            r5_scores.push(recall_at_k(&ranked, rel_set, 5));
            r10_scores.push(recall_at_k(&ranked, rel_set, 10));
            p10_scores.push(precision_at_k(&ranked, rel_set, 10));
        }

        output.results.push(MethodResult {
            method: "Zero-vector".to_string(),
            ndcg_10: stats(&ndcg_scores),
            mrr: stats(&mrr_scores),
            recall_1: stats(&r1_scores),
            recall_5: stats(&r5_scores),
            recall_10: stats(&r10_scores),
            precision_10: stats(&p10_scores),
            avg_recall_latency_us: 0.0,
        });
    }

    // ── Write output ──
    let json_out = serde_json::to_string_pretty(&output).expect("serialize output");
    std::fs::write(&output_path, &json_out).expect("write output JSON");

    // Print summary to stdout
    println!("╔══════════════════════════════════════════════════════════════╗");
    println!("║  MemHop Quality Benchmark — {}", input.name);
    println!("╠══════════════════════════════════════════════════════════════╣");
    println!("║  {:20} {:>10} {:>10} {:>10}", "", "NDCG@10", "MRR", "R@10");
    println!("╠══════════════════════════════════════════════════════════════╣");
    for r in &output.results {
        println!("║  {:20} {:>10.4} {:>10.4} {:>10.4}",
            r.method, r.ndcg_10.mean, r.mrr.mean, r.recall_10.mean);
    }
    println!("╚══════════════════════════════════════════════════════════════╝");
    println!("\nResults written to: {}", output_path);
}

