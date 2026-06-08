//! MemHop 性能基准测试
//! 使用 criterion 进行精确的性能测量和回归检测
//!
//! 设计原则：
//! - batch_store: 使用全局原子计数器生成唯一 items，避免数据累积
//! - recall/bm25: 预加载数据到 Brain（只读操作，共享安全）
//! - encoder: 纯计算，无 I/O

use criterion::{BenchmarkId, Criterion, criterion_group, criterion_main};
use memhop_core::{Brain, BrainConfig, Layer, RecallRequest, StoreBatch, StoreItem};
use std::cell::RefCell;
use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};

thread_local! {
    static BRAIN: RefCell<Option<(Brain, tempfile::TempDir)>> = const { RefCell::new(None) };
}

/// 获取共享的 Brain 实例
fn with_test_brain<F, R>(f: F) -> R
where
    F: FnOnce(&mut Brain) -> R,
{
    BRAIN.with(|brain_cell| {
        let mut brain_opt = brain_cell.borrow_mut();
        if brain_opt.is_none() {
            let tmp = tempfile::tempdir().unwrap();
            let cfg = BrainConfig {
                brains_dir: tmp.path().to_str().unwrap().to_string(),
                agent_id: "bench".to_string(),
            };
            let encoder: Arc<Box<dyn memhop_core::Encoder>> = Arc::new(Box::new(memhop_core::NgramEncoder::new(1024)));
            *brain_opt = Some((Brain::open(cfg, encoder).unwrap(), tmp));
        }
        let brain = &mut brain_opt.as_mut().unwrap().0;
        f(brain)
    })
}

/// 全局原子计数器，为 batch_store 生成唯一 items
static BENCH_OFFSET: AtomicUsize = AtomicUsize::new(0);

/// 生成一批唯一 items（确保每次调用内容不同，避免数据累积）
fn generate_unique_items(count: usize) -> Vec<StoreItem> {
    let base = BENCH_OFFSET.fetch_add(count, Ordering::Relaxed);
    (0..count)
        .map(|i| {
            let idx = base + i;
            StoreItem {
                text: format!("Benchmark item {} with some content for testing [unique_{}]", idx, idx),
                source: "benchmark".to_string(),
                turn_id: Some(format!("turn_{}", idx)),
                session_id: Some("bench_session".to_string()),
                topic_label: Some(format!("topic_{}", idx % 10)),
                llm_keywords: Some(vec![format!("keyword_{}", idx), "benchmark".to_string()]),
                llm_compressed_summary: Some(format!("Summary {}", idx)),
                valence: Some(0.5 + (idx as f64 * 0.01)),
                arousal: Some(0.3 + (idx as f64 * 0.005)),
                chain_parent_id: None,
                chain_label: None,
                domain_id: None,
                importance: Some(0.5 + (idx as f32 * 0.01)),
            }
        })
        .collect()
}

/// 生成固定测试数据（用于预填充，只调用一次）
fn generate_test_items(count: usize) -> Vec<StoreItem> {
    (0..count)
        .map(|i| StoreItem {
            text: format!("Benchmark test item {} with some content for testing", i),
            source: "benchmark".to_string(),
            turn_id: Some(format!("turn_{}", i)),
            session_id: Some("bench_session".to_string()),
            topic_label: Some(format!("topic_{}", i % 10)),
            llm_keywords: Some(vec![format!("keyword_{}", i), "benchmark".to_string()]),
            llm_compressed_summary: Some(format!("Summary {}", i)),
            valence: Some(0.5 + (i as f64 * 0.01)),
            arousal: Some(0.3 + (i as f64 * 0.005)),
            chain_parent_id: None,
            chain_label: None,
            domain_id: None,
            importance: Some(0.5 + (i as f32 * 0.01)),
        })
        .collect()
}

fn bench_batch_store(c: &mut Criterion) {
    let mut group = c.benchmark_group("batch_store");
    group.sample_size(10); // batch_store 单次调用较慢

    // 预填充 Brain 到稳态（避免空索引的冷启动效应）
    with_test_brain(|brain| {
        let baseline = generate_test_items(20);
        brain.batch_store(StoreBatch { items: baseline }).unwrap();
    });

    for size in [10, 50, 100].iter() {
        group.bench_with_input(BenchmarkId::from_parameter(size), size, |b, &size| {
            b.iter(|| {
                with_test_brain(|brain| {
                    let items = generate_unique_items(size);
                    brain.batch_store(StoreBatch { items }).unwrap()
                })
            });
        });
    }

    group.finish();
}

fn bench_recall(c: &mut Criterion) {
    let mut group = c.benchmark_group("recall");

    // 预先存储数据（只读操作，共享安全）
    with_test_brain(|brain| {
        let items = generate_test_items(1000);
        brain.batch_store(StoreBatch { items }).unwrap();
    });

    for max_results in [5, 10, 50, 100].iter() {
        group.bench_with_input(
            BenchmarkId::from_parameter(max_results),
            max_results,
            |b, &max_results| {
                b.iter(|| {
                    with_test_brain(|brain| {
                        let req = RecallRequest {
                            query: "benchmark test".to_string(),
                            max_results,
                            target_layers: vec![Layer::L1],
                            ..Default::default()
                        };
                        brain.recall(&req).unwrap()
                    })
                });
            },
        );
    }

    group.finish();
}

fn bench_consolidate(c: &mut Criterion) {
    let mut group = c.benchmark_group("consolidate");
    group.sample_size(10); // consolidate 较慢

    group.bench_function("consolidate_50", |b| {
        b.iter(|| {
            with_test_brain(|brain| {
                let items = generate_unique_items(50);
                brain.batch_store(StoreBatch { items }).unwrap();
                brain.consolidate().unwrap()
            })
        });
    });

    group.finish();
}

fn bench_encoder(c: &mut Criterion) {
    let mut group = c.benchmark_group("encoder");

    group.bench_function("encode_short_text", |b| {
        b.iter(|| with_test_brain(|brain| brain.encoder.encode("This is a test sentence")));
    });

    group.bench_function("encode_long_text", |b| {
        b.iter(|| {
            with_test_brain(|brain| {
                let long_text = "word ".repeat(500);
                brain.encoder.encode(&long_text)
            })
        });
    });

    group.finish();
}

fn bench_bm25_search(c: &mut Criterion) {
    let mut group = c.benchmark_group("bm25_search");

    // 预先存储数据（只读操作，共享安全）
    with_test_brain(|brain| {
        let items = generate_test_items(500);
        brain.batch_store(StoreBatch { items }).unwrap();
    });

    group.bench_function("search_single_term", |b| {
        let mut query = std::collections::HashMap::new();
        query.insert("benchmark".to_string(), 1.0);
        b.iter(|| with_test_brain(|brain| {
            let l1 = brain.l1.as_ref().unwrap();
            let l1_env = brain.l1_env.as_ref().unwrap();
            let rtxn = l1_env.env.read_txn().unwrap();
            l1.bm25.search(&query, 10, &rtxn)
        }));
    });

    group.bench_function("search_multiple_terms", |b| {
        let mut query = std::collections::HashMap::new();
        query.insert("benchmark".to_string(), 1.0);
        query.insert("test".to_string(), 1.0);
        query.insert("content".to_string(), 1.0);
        b.iter(|| with_test_brain(|brain| {
            let l1 = brain.l1.as_ref().unwrap();
            let l1_env = brain.l1_env.as_ref().unwrap();
            let rtxn = l1_env.env.read_txn().unwrap();
            l1.bm25.search(&query, 10, &rtxn)
        }));
    });

    group.finish();
}

criterion_group!(
    benches,
    bench_batch_store,
    bench_recall,
    bench_consolidate,
    bench_encoder,
    bench_bm25_search
);
criterion_main!(benches);
