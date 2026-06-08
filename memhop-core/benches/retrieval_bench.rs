//! 纯检索性能基准测试
//! 覆盖：召回延迟、存储延迟、吞吐量、IR 指标、P99 延迟目标验证
//!
//! 设计原则：
//! - 每个 benchmark 函数只创建 **1 个** Brain（macOS LMDB 文件锁限制）
//! - 多 variant 共享 Brain 通过 RefCell 实现
//! - 全局原子计数器生成唯一 items，确保每次迭代数据不同
//! - batch_store 单次调用较慢（L4+L1+L2+L3 四层写入），使用小 sample_size

use criterion::{Criterion, criterion_group, criterion_main};
use memhop_core::{Brain, BrainConfig, RecallRequest, StoreBatch, StoreItem, Layer};
use memhop_core::bench_support::test_data;
use memhop_core::bench_support::metrics;
use std::cell::RefCell;
use std::collections::HashSet;
use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::Instant;

/// 创建独立 temp dir 的 Brain
fn make_brain(agent_id: &str) -> (Brain, tempfile::TempDir) {
    let tmp = tempfile::tempdir().unwrap();
    let cfg = BrainConfig {
        brains_dir: tmp.path().to_str().unwrap().to_string(),
        agent_id: agent_id.to_string(),
    };
    let encoder: Arc<Box<dyn memhop_core::Encoder>> =
        Arc::new(Box::new(memhop_core::NgramEncoder::new(1024)));
    let brain = Brain::open(cfg, encoder).unwrap();
    (brain, tmp)
}

/// macOS 安全版本：返回 (TempDir, Brain) 使 Brain 在 TempDir 前析构
fn make_brain_rev(agent_id: &str) -> (tempfile::TempDir, Brain) {
    let (brain, tmp) = make_brain(agent_id);
    (tmp, brain)
}

/// 全局原子计数器，为每次迭代生成唯一 items
static ITEM_OFFSET: AtomicUsize = AtomicUsize::new(0);

/// 生成一批唯一 items（简化版，无 keywords/summary，专注测 store 延迟）
fn generate_unique_store_batch(count: usize) -> Vec<StoreItem> {
    let base = ITEM_OFFSET.fetch_add(count, Ordering::Relaxed);
    let topics = [
        "rust_programming", "python_data_science", "web_development",
        "machine_learning", "database_design",
    ];
    (0..count)
        .map(|i| {
            let idx = base + i;
            StoreItem {
                text: format!("Store bench item {} [unique_{}]", idx, idx),
                source: "store_bench".to_string(),
                turn_id: Some(format!("turn_{}", idx)),
                session_id: Some(format!("session_{}", idx % 5)),
                topic_label: Some(topics[idx % topics.len()].to_string()),
                llm_keywords: None,
                llm_compressed_summary: None,
                valence: None,
                arousal: None,
                chain_parent_id: None,
                chain_label: None,
                domain_id: None,
                importance: None,
            }
        })
        .collect()
}

// ── 存储延迟：预填充 + 唯一增量 items ──────────────────
// 每个 batch_size 独立函数，单个 Brain + RefCell（macOS EAGAIN workaround）

fn bench_store_latency_b10(c: &mut Criterion) {
    let mut group = c.benchmark_group("retrieval/store_latency");
    group.sample_size(10).warm_up_time(std::time::Duration::from_secs(1));

    let (_tmp, mut brain) = make_brain_rev("s_10");
    let baseline = test_data::generate_store_items(20);
    brain.batch_store(StoreBatch { items: baseline }).unwrap();
    let brain = RefCell::new(brain);

    group.bench_function("batch_10", |b| {
        b.iter(|| {
            let items = generate_unique_store_batch(10);
            brain.borrow_mut().batch_store(StoreBatch { items }).unwrap()
        });
    });

    group.finish();
}

fn bench_store_latency_b50(c: &mut Criterion) {
    let mut group = c.benchmark_group("retrieval/store_latency");
    group.sample_size(10).warm_up_time(std::time::Duration::from_secs(1));

    let (_tmp, mut brain) = make_brain_rev("s_50");
    let baseline = test_data::generate_store_items(20);
    brain.batch_store(StoreBatch { items: baseline }).unwrap();
    let brain = RefCell::new(brain);

    group.bench_function("batch_50", |b| {
        b.iter(|| {
            let items = generate_unique_store_batch(50);
            brain.borrow_mut().batch_store(StoreBatch { items }).unwrap()
        });
    });

    group.finish();
}

fn bench_store_latency_b100(c: &mut Criterion) {
    let mut group = c.benchmark_group("retrieval/store_latency");
    group.sample_size(10).warm_up_time(std::time::Duration::from_secs(1));

    let (_tmp, mut brain) = make_brain_rev("s_100");
    let baseline = test_data::generate_store_items(20);
    brain.batch_store(StoreBatch { items: baseline }).unwrap();
    let brain = RefCell::new(brain);

    group.bench_function("batch_100", |b| {
        b.iter(|| {
            let items = generate_unique_store_batch(100);
            brain.borrow_mut().batch_store(StoreBatch { items }).unwrap()
        });
    });

    group.finish();
}

// ── 召回延迟 ──────────────────────────────────────────────

fn bench_recall_latency(c: &mut Criterion) {
    let mut group = c.benchmark_group("retrieval/recall_latency");

    // 单个 Brain + RefCell，多 variant 共享（macOS: 避免 EAGAIN）
    let (_tmp, mut brain) = make_brain_rev("recall_lat");
    let items = test_data::generate_store_items(200);
    brain.batch_store(StoreBatch { items }).unwrap();
    let brain = RefCell::new(brain);

    for max_results in [5, 10, 50, 100] {
        group.bench_function(
            format!("recall_top_{}", max_results),
            |b| {
                b.iter(|| {
                    let req = RecallRequest {
                        query: "memory safety".to_string(),
                        max_results,
                        target_layers: vec![Layer::L1],
                        ..Default::default()
                    };
                    brain.borrow_mut().recall(&req).unwrap()
                });
            },
        );
    }

    group.finish();
}

// ── 召回质量 (IR 指标) ─────────────────────────────────────

fn bench_recall_quality(c: &mut Criterion) {
    let mut group = c.benchmark_group("retrieval/recall_quality");

    // 单个 Brain + RefCell，多 variant 共享（macOS: 避免 EAGAIN）
    let (_tmp, mut brain) = make_brain_rev("quality");
    let items = test_data::generate_store_items(200);
    brain.batch_store(StoreBatch { items }).unwrap();
    let brain = RefCell::new(brain);

    let queries = test_data::generate_recall_queries(10);

    group.bench_function("ndcg_at_10", |b| {
        b.iter(|| {
            let mut total_ndcg = 0.0;
            for query in &queries {
                let req = RecallRequest {
                    query: query.clone(),
                    max_results: 10,
                    target_layers: vec![Layer::L1],
                    ..Default::default()
                };
                let resp = brain.borrow_mut().recall(&req).unwrap();
                let retrieved: Vec<String> = resp.results.iter().map(|r| r.id.clone()).collect();
                let relevant: HashSet<String> = resp
                    .results
                    .iter()
                    .filter(|r| r.score > 0.3)
                    .map(|r| r.id.clone())
                    .collect();
                total_ndcg += metrics::ndcg_at_k(&retrieved, &relevant, 10);
            }
            total_ndcg / queries.len() as f64
        });
    });

    group.bench_function("recall_at_10", |b| {
        b.iter(|| {
            let mut total_recall = 0.0;
            for query in &queries {
                let req = RecallRequest {
                    query: query.clone(),
                    max_results: 10,
                    target_layers: vec![Layer::L1],
                    ..Default::default()
                };
                let resp = brain.borrow_mut().recall(&req).unwrap();
                let retrieved: Vec<String> = resp.results.iter().map(|r| r.id.clone()).collect();
                let relevant: HashSet<String> = resp
                    .results
                    .iter()
                    .filter(|r| r.score > 0.3)
                    .map(|r| r.id.clone())
                    .collect();
                total_recall += metrics::recall_at_k(&retrieved, &relevant, 10);
            }
            total_recall / queries.len() as f64
        });
    });

    group.finish();
}

// ── 吞吐量 (QPS) ─────────────────────────────────────────

fn bench_throughput(c: &mut Criterion) {
    let mut group = c.benchmark_group("retrieval/throughput");

    // 单个 Brain + RefCell（macOS: 避免 EAGAIN）
    let (_tmp, mut brain) = make_brain_rev("throughput");
    let items = test_data::generate_store_items(200);
    brain.batch_store(StoreBatch { items }).unwrap();
    let brain = RefCell::new(brain);

    group.bench_function("queries_per_second", |b| {
        b.iter_custom(|iters| {
            let start = Instant::now();
            for i in 0..iters {
                let req = RecallRequest {
                    query: format!("query_{}", i % 15),
                    max_results: 10,
                    target_layers: vec![Layer::L1],
                    ..Default::default()
                };
                brain.borrow_mut().recall(&req).unwrap();
            }
            start.elapsed()
        });
    });

    group.finish();
}

// ── P99 延迟验证 ─────────────────────────────────────────

fn bench_p99_latency(c: &mut Criterion) {
    let mut group = c.benchmark_group("retrieval/p99_latency");

    // 单个 Brain + RefCell（macOS: 避免 EAGAIN）
    let (_tmp, mut brain) = make_brain_rev("p99");
    let items = test_data::generate_store_items(200);
    brain.batch_store(StoreBatch { items }).unwrap();
    let brain = RefCell::new(brain);

    // 预热
    for i in 0..50 {
        let req = RecallRequest {
            query: format!("warmup_{}", i),
            max_results: 10,
            target_layers: vec![Layer::L1],
            ..Default::default()
        };
        let _ = brain.borrow_mut().recall(&req);
    }

    group.bench_function("recall_p99_check", |b| {
        let mut latencies: Vec<std::time::Duration> = Vec::with_capacity(500);
        b.iter(|| {
            let start = Instant::now();
            let req = RecallRequest {
                query: "memory safety programming".to_string(),
                max_results: 10,
                target_layers: vec![Layer::L1],
                ..Default::default()
            };
            brain.borrow_mut().recall(&req).unwrap();
            latencies.push(start.elapsed());
        });
        if latencies.len() >= 50 {
            latencies.sort();
            let p99 = latencies[latencies.len() * 99 / 100];
            eprintln!(
                "  [P99 CHECK] p99 = {:?} (target < 5ms) -> {}",
                p99,
                if p99.as_millis() < 5 {
                    "PASS"
                } else {
                    "WARN (NgramEncoder)"
                }
            );
        }
    });

    group.finish();
}

criterion_group!(
    benches,
    bench_store_latency_b10,
    bench_store_latency_b50,
    bench_store_latency_b100,
    bench_recall_latency,
    bench_recall_quality,
    bench_throughput,
    bench_p99_latency,
);
criterion_main!(benches);
