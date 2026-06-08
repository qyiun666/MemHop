//! Brain 层级集成基准测试
//! 覆盖：端到端存储、检索、巩固流程
//!
//! 设计原则：
//! - Store benchmark: 使用全局原子计数器生成唯一 items，避免数据累积
//! - Recall/consolidate: 预加载数据到一个 Brain（只读操作，共享安全）
//! - batch_store 单次调用较慢（L4+L1+L2+L3 四层写入），使用小 sample_size

use criterion::{Criterion, criterion_group, criterion_main};
use memhop_core::{Brain, BrainConfig, RecallRequest, StoreBatch, Layer};
use memhop_core::bench_support::test_data;
use std::cell::RefCell;
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

/// macOS 安全版本：返回 (TempDir, Brain) 使 Brain 在 TempDir 前析构
fn make_bench_brain_rev(agent_id: &str) -> (tempfile::TempDir, Brain) {
    let (brain, tmp) = make_bench_brain(agent_id);
    (tmp, brain)
}

/// 全局原子计数器，为每次迭代生成唯一 items
static MCP_OFFSET: AtomicUsize = AtomicUsize::new(0);

/// 生成一批唯一 items
fn generate_unique_items(count: usize) -> Vec<memhop_core::StoreItem> {
    let base = MCP_OFFSET.fetch_add(count, Ordering::Relaxed);
    (0..count)
        .map(|i| {
            let idx = base + i;
            memhop_core::StoreItem {
                text: format!("Integration test item {} [unique_{}]", idx, idx),
                source: "bench".to_string(),
                turn_id: Some(format!("turn_{}", idx)),
                session_id: Some("bench_session".to_string()),
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

// ── 直接 Brain API 基准（模拟 handler 开销）──────────────

fn bench_handler_store_latency(c: &mut Criterion) {
    let mut group = c.benchmark_group("integration/handler_store");
    group.sample_size(10); // batch_store 单次调用较慢

    group.bench_function("batch_1", |b| {
        // macOS LMDB 限制：Brain 必须在 b.iter 外创建，避免 EAGAIN
        let (_tmp, mut brain) = make_bench_brain_rev("handler_s1");
        b.iter(|| {
            let items = generate_unique_items(1);
            brain.batch_store(StoreBatch { items }).unwrap()
        });
    });

    group.bench_function("batch_10", |b| {
        let (_tmp, mut brain) = make_bench_brain_rev("handler_s10");
        b.iter(|| {
            let items = generate_unique_items(10);
            brain.batch_store(StoreBatch { items }).unwrap()
        });
    });

    group.finish();
}

fn bench_handler_recall_latency(c: &mut Criterion) {
    let mut group = c.benchmark_group("integration/handler_recall");

    let (_tmp, mut brain) = make_bench_brain_rev("handler_recall");
    let items = test_data::generate_store_items(200);
    brain.batch_store(StoreBatch { items }).unwrap();

    for max_results in [5, 10, 50] {
        group.bench_function(
            format!("recall_top_{}", max_results),
            |b| {
                b.iter(|| {
                    let req = RecallRequest {
                        query: "programming language".to_string(),
                        max_results,
                        target_layers: vec![Layer::L1, Layer::L2],
                        ..Default::default()
                    };
                    brain.recall(&req).unwrap()
                });
            },
        );
    }

    group.finish();
}

// ── 多 Brain 实例（模拟 LRU 缓存）────────────────────
// macOS: 预创建所有 Brain 在 b.iter() 外，避免 LMDB EAGAIN

fn bench_multi_brain_concurrent(c: &mut Criterion) {
    let mut group = c.benchmark_group("integration/multi_brain");

    // 预创建 10 个独立 Brain
    let brains: Vec<_> = (0..10)
        .map(|i| {
            let (tmp, mut brain) = make_bench_brain_rev(&format!("mb_{}", i));
            let items = generate_unique_items(20);
            brain.batch_store(StoreBatch { items }).unwrap();
            (tmp, RefCell::new(brain))
        })
        .collect();

    group.bench_function("recall_across_10_brains", |b| {
        b.iter(|| {
            for (_, brain_cell) in &brains {
                let req = RecallRequest {
                    query: "test item".to_string(),
                    max_results: 5,
                    target_layers: vec![Layer::L1],
                    ..Default::default()
                };
                brain_cell.borrow_mut().recall(&req).unwrap();
            }
        });
    });

    group.finish();
}

// ── 端到端流程 ──────────────────────────────────────
// macOS: consolidate 在 b.iter() 外执行，b.iter() 只做 recall

fn bench_e2e_conversation(c: &mut Criterion) {
    let mut group = c.benchmark_group("integration/e2e");

    // 预填充 + 一次性 consolidate（L5 write tx，在 b.iter 外安全）
    let (_tmp, mut brain) = make_bench_brain_rev("e2e");
    let items = generate_unique_items(50);
    brain.batch_store(StoreBatch { items }).unwrap();
    brain.consolidate().unwrap();
    let brain = RefCell::new(brain);

    group.bench_function("recall_after_e2e", |b| {
        b.iter(|| {
            let req = RecallRequest {
                query: "test item".to_string(),
                max_results: 10,
                target_layers: vec![Layer::L1, Layer::L2],
                ..Default::default()
            };
            brain.borrow_mut().recall(&req).unwrap();
        });
    });

    group.finish();
}

// ── L0 Profile 基准 ─────────────────────────────────

fn bench_l0_set_get(c: &mut Criterion) {
    let mut group = c.benchmark_group("integration/l0_profile");

    group.bench_function("set_l0_profile_full", |b| {
        // macOS LMDB 限制：Brain 在 b.iter 外创建
        let (_tmp, mut brain) = make_bench_brain_rev("l0_full");
        b.iter(|| {
            let mut traits = std::collections::HashMap::new();
            traits.insert("language".to_string(), "English".to_string());
            traits.insert("style".to_string(), "formal".to_string());
            brain.set_l0(
                Some("agent_001".to_string()),
                Some("TestBot".to_string()),
                vec!["helpful".to_string(), "precise".to_string(), "fast".to_string()],
                vec!["accuracy".to_string(), "efficiency".to_string()],
                vec!["evidence_based".to_string(), "scientific".to_string()],
                traits,
            ).unwrap();
        });
    });

    group.finish();
}

criterion_group!(
    benches,
    bench_handler_store_latency,
    bench_handler_recall_latency,
    bench_multi_brain_concurrent,
    bench_e2e_conversation,
    bench_l0_set_get,
);
criterion_main!(benches);
