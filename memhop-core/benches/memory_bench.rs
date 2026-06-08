//! 内存使用基准测试 — v0.22.0 适配
//! 覆盖：Brain::open 基线、渐进负载增长、consolidate/crystallize、泄漏检测、L3 延迟加载
//!
//! v0.22.0 关键变更：
//! - F16 HNSW 量化 (50% 向量内存节省)
//! - L4 移除 HNSW (纯 ngram, ~460MB 节省)
//! - L3 延迟加载 (仅搜索时创建 HNSW)
//! - LMDB map_size 大幅缩减 (总计 4GB→1GB)
//!
//! 设计原则 (v0.22.0 修订):
//! - 每个 benchmark group 只创建 **一个** 持久 Brain，RSS 仅测量一次
//! - criterion b.iter() 只 timing 轻量操作（避免 macOS LMDB 文件描述符耗尽）
//! - 所有 RSS 数据由 eprintln! 输出

use criterion::{Criterion, criterion_group, criterion_main};
use memhop_core::{Brain, BrainConfig, RecallRequest, StoreBatch, Layer};
use memhop_core::bench_support::memory_monitor::{MemoryMonitor, format_bytes};
use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};

fn make_bench_brain(agent_id: &str) -> (Brain, tempfile::TempDir) {
    let tmp = tempfile::tempdir().unwrap();
    let cfg = BrainConfig {
        brains_dir: tmp.path().to_str().unwrap().to_string(),
        agent_id: agent_id.to_string(),
    };
    let encoder: Arc<Box<dyn memhop_core::Encoder>> = Arc::new(Box::new(memhop_core::NgramEncoder::new(1024)));
    let brain = Brain::open(cfg, encoder).unwrap();
    (brain, tmp)
}

/// 全局原子计数器
static MEM_OFFSET: AtomicUsize = AtomicUsize::new(0);

fn generate_unique_items(count: usize) -> Vec<memhop_core::StoreItem> {
    let base = MEM_OFFSET.fetch_add(count, Ordering::Relaxed);
    (0..count)
        .map(|i| {
            let idx = base + i;
            memhop_core::StoreItem {
                text: format!("Memory test item {} [unique_{}]", idx, idx),
                source: "memory_bench".to_string(),
                turn_id: Some(format!("turn_{}", idx)),
                session_id: Some("mem_session".to_string()),
                topic_label: Some(format!("topic_{}", idx % 5)),
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

// ── 基线：Brain::open RSS ──────────────────────────────────────

fn bench_memory_baseline(c: &mut Criterion) {
    let mut group = c.benchmark_group("memory/baseline");
    group.sample_size(10);

    let monitor = MemoryMonitor::new();
    let snap_before = monitor.snapshot();
    let (brain, _tmp) = make_bench_brain("mem_baseline");
    let snap_after = monitor.snapshot();
    let delta = snap_after.rss_bytes as i64 - snap_before.rss_bytes as i64;
    eprintln!(
        "  [BASELINE] before={}, after={}, delta={}",
        format_bytes(snap_before.rss_bytes),
        format_bytes(snap_after.rss_bytes),
        format_bytes(delta.unsigned_abs())
    );

    // criterion timing dummy operation（不创建新 Brain）
    group.bench_function("brain_open_memory", |b| {
        b.iter(|| std::hint::black_box(brain.l1.is_some()));
    });

    drop(brain);
    drop(_tmp);
    group.finish();
}

// ── 渐进负载 RSS 增长 ──────────────────────────────────────────

fn bench_memory_under_load(c: &mut Criterion) {
    let mut group = c.benchmark_group("memory/under_load");
    group.sample_size(10);

    let monitor = MemoryMonitor::new();
    let (_tmp, mut brain) = {
        let tmp = tempfile::tempdir().unwrap();
        let cfg = BrainConfig {
            brains_dir: tmp.path().to_str().unwrap().to_string(),
            agent_id: "mem_load".to_string(),
        };
        let encoder: Arc<Box<dyn memhop_core::Encoder>> = Arc::new(Box::new(memhop_core::NgramEncoder::new(1024)));
        let brain = Brain::open(cfg, encoder).unwrap();
        (tmp, brain)
    };
    let snap_baseline = monitor.snapshot();
    eprintln!("  [LOAD] baseline RSS: {}", format_bytes(snap_baseline.rss_bytes));

    let mut cumulative = 0usize;
    for size in [10, 50, 100, 200] {
        let items = generate_unique_items(size);
        brain.batch_store(StoreBatch { items }).unwrap();
        cumulative += size;
        let snap = monitor.snapshot();
        eprintln!(
            "  [LOAD] after +{} (total {}) RSS: {} (delta: {})",
            size,
            cumulative,
            format_bytes(snap.rss_bytes),
            format_bytes((snap.rss_bytes as i64 - snap_baseline.rss_bytes as i64).unsigned_abs())
        );
    }

    group.bench_function("progressive_load", |b| {
        b.iter(|| std::hint::black_box(brain.l1.is_some()));
    });

    drop(brain);
    drop(_tmp);
    group.finish();
}

// ── Consolidate / Crystallize 后 RSS ───────────────────────────

fn bench_memory_after_ops(c: &mut Criterion) {
    let mut group = c.benchmark_group("memory/after_ops");
    group.sample_size(10);

    // ── after_consolidate ──
    {
        let monitor = MemoryMonitor::new();
        let snap_before = monitor.snapshot();

        let (_tmp, mut brain) = {
            let tmp = tempfile::tempdir().unwrap();
            let cfg = BrainConfig {
                brains_dir: tmp.path().to_str().unwrap().to_string(),
                agent_id: "mem_ops".to_string(),
            };
            let encoder: Arc<Box<dyn memhop_core::Encoder>> = Arc::new(Box::new(memhop_core::NgramEncoder::new(1024)));
            let brain = Brain::open(cfg, encoder).unwrap();
            (tmp, brain)
        };

        let items = generate_unique_items(100);
        brain.batch_store(StoreBatch { items }).unwrap();
        brain.consolidate().unwrap();
        let snap_after = monitor.snapshot();
        eprintln!(
            "  [AFTER CONSOLIDATE] RSS: {} (delta: {})",
            format_bytes(snap_after.rss_bytes),
            format_bytes((snap_after.rss_bytes as i64 - snap_before.rss_bytes as i64).unsigned_abs())
        );
        drop(brain);
        drop(_tmp);
    }

    // ── after_crystallize ──
    {
        let monitor = MemoryMonitor::new();
        let base = MEM_OFFSET.fetch_add(30, Ordering::Relaxed);
        let chain_items: Vec<memhop_core::StoreItem> = (0..30)
            .map(|i| {
                let idx = base + i;
                memhop_core::StoreItem {
                    text: format!("Step {} of the process [unique_{}]", idx, idx),
                    source: "test".to_string(),
                    turn_id: Some(format!("turn_{}", idx)),
                    session_id: Some("test_session".to_string()),
                    topic_label: Some("test".to_string()),
                    llm_keywords: None,
                    llm_compressed_summary: None,
                    valence: None,
                    arousal: None,
                    chain_parent_id: if i > 0 { Some(format!("node_{}", idx - 1)) } else { None },
                    chain_label: Some("step".to_string()),
                    domain_id: None,
                    importance: None,
                }
            })
            .collect();

        let snap_before = monitor.snapshot();
        let (_tmp, mut brain) = {
            let tmp = tempfile::tempdir().unwrap();
            let cfg = BrainConfig {
                brains_dir: tmp.path().to_str().unwrap().to_string(),
                agent_id: "mem_cryst".to_string(),
            };
            let encoder: Arc<Box<dyn memhop_core::Encoder>> = Arc::new(Box::new(memhop_core::NgramEncoder::new(1024)));
            let brain = Brain::open(cfg, encoder).unwrap();
            (tmp, brain)
        };

        brain.batch_store(StoreBatch { items: chain_items.clone() }).unwrap();
        brain.procedural_crystallize().unwrap();
        let snap_after = monitor.snapshot();
        eprintln!(
            "  [AFTER CRYSTALLIZE] RSS: {} (delta: {})",
            format_bytes(snap_after.rss_bytes),
            format_bytes((snap_after.rss_bytes as i64 - snap_before.rss_bytes as i64).unsigned_abs())
        );
        drop(brain);
        drop(_tmp);
    }

    // criterion 需要至少一个 bench_function
    group.bench_function("ops_dummy", |b| b.iter(|| 42));
    group.finish();
}

// ── 泄漏检测 ──────────────────────────────────────────────────

fn bench_memory_leak_detection(c: &mut Criterion) {
    let mut group = c.benchmark_group("memory/leak_detection");
    group.sample_size(10);

    let monitor = MemoryMonitor::new();
    let snap_before = monitor.snapshot();

    let (_tmp, mut brain) = {
        let tmp = tempfile::tempdir().unwrap();
        let cfg = BrainConfig {
            brains_dir: tmp.path().to_str().unwrap().to_string(),
            agent_id: "mem_leak".to_string(),
        };
        let encoder: Arc<Box<dyn memhop_core::Encoder>> = Arc::new(Box::new(memhop_core::NgramEncoder::new(1024)));
        let brain = Brain::open(cfg, encoder).unwrap();
        (tmp, brain)
    };

    for i in 0..100 {
        let items = generate_unique_items(10);
        brain.batch_store(StoreBatch { items }).unwrap();
        let req = RecallRequest {
            query: format!("query_{}", i % 15),
            max_results: 5,
            target_layers: vec![Layer::L1],
            ..Default::default()
        };
        let _ = brain.recall(&req);
    }

    let snap_after = monitor.snapshot();
    // v0.22.0: 1000 items 合法数据增长 ~45MB，阈值 60MB
    let leak_result = monitor.leak_check(&snap_before, &snap_after, 60);
    eprintln!(
        "  [LEAK CHECK] initial_rss={}, final_rss={}, growth={}, passed={} (threshold: {}MB)",
        format_bytes(leak_result.initial_rss),
        format_bytes(leak_result.final_rss),
        format_bytes(leak_result.growth_bytes.unsigned_abs()),
        leak_result.passed,
        leak_result.threshold_mb
    );
    assert!(leak_result.passed,
        "Memory leak detected: growth {} bytes exceeds {}MB threshold",
        leak_result.growth_bytes, leak_result.threshold_mb);

    drop(brain);
    drop(_tmp);

    group.bench_function("leak_dummy", |b| b.iter(|| 42));
    group.finish();
}

// ── L3 延迟加载验证 ───────────────────────────────────────────

fn bench_memory_l3_lazy_verify(c: &mut Criterion) {
    let mut group = c.benchmark_group("memory/l3_lazy");
    group.sample_size(10);

    let monitor = MemoryMonitor::new();
    let snap_before = monitor.snapshot();
    let (brain, _tmp) = make_bench_brain("mem_l3lazy");
    let snap_after = monitor.snapshot();
    let has_l3 = brain.l3.is_some();
    eprintln!(
        "  [L3 LAZY] Brain::open RSS delta: {}, l3_loaded={}",
        format_bytes((snap_after.rss_bytes as i64 - snap_before.rss_bytes as i64).unsigned_abs()),
        has_l3
    );
    drop(brain);
    drop(_tmp);

    group.bench_function("l3_lazy_dummy", |b| b.iter(|| 42));
    group.finish();
}

criterion_group!(
    benches,
    bench_memory_baseline,
    bench_memory_under_load,
    bench_memory_after_ops,
    bench_memory_leak_detection,
    bench_memory_l3_lazy_verify,
);
criterion_main!(benches);
