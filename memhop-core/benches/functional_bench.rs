//! 功能完整性基准测试
//! 覆盖：存储检索链路、多层架构、记忆巩固、结晶、会话管理、知识库挂载
//!
//! 设计原则：
//! - 每个 bench_function 只创建一个 Brain，避免 LMDB 环境锁冲突
//! - Recall/consolidate/crystallize: 预加载数据 + 预开所有 env（只读操作，共享安全）
//! - Store benchmark: 单 Brain 追加模式，使用 sample_size(10) 限制
//! - macOS LMDB: 必须在 b.iter 前预开所有 env，否则 EAGAIN (os error 35)

use criterion::{Criterion, criterion_group, criterion_main};
use memhop_core::{Brain, BrainConfig, RecallRequest, StoreBatch, StoreItem, Layer, ShelfDomain};
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;

/// 全局计数器，确保不同 bench_function 间 item 不重复
static FUNC_OFFSET: AtomicUsize = AtomicUsize::new(0);

fn generate_unique_items(count: usize) -> Vec<StoreItem> {
    let base = FUNC_OFFSET.fetch_add(count, Ordering::Relaxed);
    (0..count)
        .map(|i| StoreItem {
            text: format!("[func_unique_{}] memory safety in Rust programming {}", base + i, i),
            source: "func_bench".to_string(),
            turn_id: Some(format!("turn_{}", base + i)),
            session_id: Some("func_session".to_string()),
            topic_label: Some("rust_programming".to_string()),
            llm_keywords: None,
            llm_compressed_summary: None,
            valence: None,
            arousal: None,
            chain_parent_id: None,
            chain_label: None,
            domain_id: None,
            importance: None,
        })
        .collect()
}

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

/// 预开所有 LMDB 环境，避免 b.iter 中首次打开触发 EAGAIN。
/// batch_store 打开 L4→L1→L2→L3；此函数补充打开 L0 和 L5。
fn preopen_remaining_envs(brain: &mut Brain) {
    // L0: get_l0_profile 调用 ensure_l0_env
    let _ = brain.get_l0_profile();
    // L5: list_crystals 调用 ensure_l5_env
    let _ = brain.list_crystals();
}

/// 预开所有 6 个 LMDB 环境（含 L1-L4），用于 batch_store + recall 在 b.iter 中的场景。
fn preopen_all_envs(brain: &mut Brain) {
    // 用空 batch_store 不行（会 early return），用 1 条 dummy item 触发 L4→L1→L2→L3
    let dummy = vec![StoreItem {
        text: "__preopen_envs__".to_string(),
        source: "preopen".to_string(),
        turn_id: None,
        session_id: None,
        topic_label: None,
        llm_keywords: None,
        llm_compressed_summary: None,
        valence: None,
        arousal: None,
        chain_parent_id: None,
        chain_label: None,
        domain_id: None,
        importance: None,
    }];
    let _ = brain.batch_store(StoreBatch { items: dummy });
    // 补充 L0 + L5
    preopen_remaining_envs(brain);
}

// ── 存储与检索链路 ──────────────────────────────────────

fn bench_full_store_recall_cycle(c: &mut Criterion) {
    let mut group = c.benchmark_group("functional/store_recall_cycle");
    group.sample_size(10);

    group.bench_function("single_item_store_recall", |b| {
        let (mut brain, _tmp) = make_bench_brain("cycle");
        // 预开所有 env，避免 b.iter 中 EAGAIN
        preopen_all_envs(&mut brain);

        b.iter(|| {
            let items = generate_unique_items(1);
            brain.batch_store(StoreBatch { items }).unwrap();
            let req = RecallRequest {
                query: "memory safety".to_string(),
                max_results: 5,
                target_layers: vec![Layer::L1],
                ..Default::default()
            };
            brain.recall(&req).unwrap();
        });
    });

    group.finish();
}

fn bench_batch_store_large(c: &mut Criterion) {
    let mut group = c.benchmark_group("functional/batch_store_large");
    group.sample_size(10);

    group.bench_function("batch_20", |b| {
        let (mut brain, _tmp) = make_bench_brain("large_20");
        // 预开 env（batch_store 打开 L4-L3，这里补充 L0+L5 以防万一）
        preopen_remaining_envs(&mut brain);

        b.iter(|| {
            let items = generate_unique_items(20);
            brain.batch_store(StoreBatch { items }).unwrap()
        });
    });

    group.finish();
}

fn bench_dedup_performance(c: &mut Criterion) {
    let mut group = c.benchmark_group("functional/dedup");
    group.sample_size(10);

    group.bench_function("store_dedup_10", |b| {
        let (mut brain, _tmp) = make_bench_brain("dedup");
        preopen_remaining_envs(&mut brain);

        // 预填充基础数据（dedup 测试：后续存相同文本应被去重）
        let base_items: Vec<StoreItem> = (0..10)
            .map(|i| StoreItem {
                text: format!("Duplicate text {}", i % 5),
                source: "dedup_base".to_string(),
                turn_id: Some(format!("base_turn_{}", i)),
                session_id: Some("dedup_base_session".to_string()),
                topic_label: Some("dedup".to_string()),
                llm_keywords: None,
                llm_compressed_summary: None,
                valence: None,
                arousal: None,
                chain_parent_id: None,
                chain_label: None,
                domain_id: None,
                importance: None,
            })
            .collect();
        brain.batch_store(StoreBatch { items: base_items }).unwrap();

        // b.iter 中存相同文本，测量 dedup 开销
        let dedup_items: Vec<StoreItem> = (0..10)
            .map(|i| StoreItem {
                text: format!("Duplicate text {}", i % 5),
                source: "dedup_test".to_string(),
                turn_id: Some(format!("turn_{}", i)),
                session_id: Some("dedup_session".to_string()),
                topic_label: Some("dedup".to_string()),
                llm_keywords: None,
                llm_compressed_summary: None,
                valence: None,
                arousal: None,
                chain_parent_id: None,
                chain_label: None,
                domain_id: None,
                importance: None,
            })
            .collect();

        b.iter(|| {
            brain.batch_store(StoreBatch {
                items: dedup_items.clone(),
            }).unwrap()
        });
    });

    group.finish();
}

// ── 多层架构验证 ──────────────────────────────────────

fn bench_l0_profile_ops(c: &mut Criterion) {
    let mut group = c.benchmark_group("functional/l0_profile");
    group.sample_size(10);

    group.bench_function("set_l0_profile", |b| {
        let (mut brain, _tmp) = make_bench_brain("l0_test");
        b.iter(|| {
            let mut traits = std::collections::HashMap::new();
            traits.insert("style".to_string(), "formal".to_string());
            brain.set_l0(
                Some("cat_001".to_string()),
                Some("Test Assistant".to_string()),
                vec!["helpful".to_string(), "precise".to_string()],
                vec!["accuracy".to_string()],
                vec!["evidence_based".to_string()],
                traits,
            ).unwrap();
        });
    });

    group.bench_function("get_l0_profile", |b| {
        let (mut brain, _tmp) = make_bench_brain("l0_get");
        let mut traits = std::collections::HashMap::new();
        traits.insert("style".to_string(), "formal".to_string());
        brain.set_l0(
            Some("cat_002".to_string()),
            Some("Test Assistant".to_string()),
            vec!["helpful".to_string()],
            vec![],
            vec![],
            traits,
        ).unwrap();

        b.iter(|| {
            brain.get_l0_profile().unwrap();
        });
    });

    group.finish();
}

fn bench_l1_l2_l4_ops(c: &mut Criterion) {
    let mut group = c.benchmark_group("functional/l1_l2_l4");
    group.sample_size(10);

    group.bench_function("recall_cross_layer", |b| {
        let (mut brain, _tmp) = make_bench_brain("l124_recall");
        let items = generate_unique_items(200);
        brain.batch_store(StoreBatch { items }).unwrap();
        // batch_store 打开了 L4, L1, L2, L3；补充打开 L0 + L5
        preopen_remaining_envs(&mut brain);

        b.iter(|| {
            let req = RecallRequest {
                query: "Rust programming language".to_string(),
                max_results: 10,
                // v0.22.0: default layers no longer include L4
                target_layers: vec![Layer::L1, Layer::L2],
                ..Default::default()
            };
            brain.recall(&req).unwrap();
        });
    });

    group.finish();
}

fn bench_l3_domain_ops(c: &mut Criterion) {
    let mut group = c.benchmark_group("functional/l3_domain");
    group.sample_size(10);

    group.bench_function("mount_and_recall", |b| {
        let (mut brain, tmp) = make_bench_brain("l3_mount");
        let mount_dir = tmp.path().join("test_docs");
        std::fs::create_dir_all(&mount_dir).unwrap();
        for i in 0..5 {
            std::fs::write(
                mount_dir.join(format!("doc_{}.txt", i)),
                format!("Document {} about Rust programming and memory safety", i),
            ).unwrap();
        }
        memhop_core::shelf::mount(
            &mut brain,
            mount_dir.to_str().unwrap(),
            ShelfDomain::Doc,
            "Test Docs",
        ).unwrap();
        // mount 内部调用 batch_store，打开了 L4, L1, L2, L3；补充 L0 + L5
        preopen_remaining_envs(&mut brain);

        b.iter(|| {
            let req = RecallRequest {
                query: "Rust memory safety".to_string(),
                max_results: 5,
                target_layers: vec![Layer::L3],
                ..Default::default()
            };
            brain.recall(&req).unwrap();
        });
    });

    group.finish();
}

// ── 记忆巩固与结晶 ──────────────────────────────────────

fn bench_consolidate_pipeline(c: &mut Criterion) {
    let mut group = c.benchmark_group("functional/consolidate");
    group.sample_size(10);

    let items_200 = generate_unique_items(200);

    group.bench_function("consolidate_200_items", |b| {
        let (mut brain, _tmp) = make_bench_brain("consolidate");
        brain.batch_store(StoreBatch {
            items: items_200.clone(),
        }).unwrap();
        b.iter(|| {
            brain.consolidate().unwrap();
        });
    });

    group.finish();
}

fn bench_crystallize_pipeline(c: &mut Criterion) {
    let mut group = c.benchmark_group("functional/crystallize");
    group.sample_size(10);

    let base = FUNC_OFFSET.fetch_add(30, Ordering::Relaxed);
    let chain_items: Vec<StoreItem> = (0..30)
        .map(|i| StoreItem {
            text: format!("[crystallize_{}] Step {} of the deployment process", base + i, i),
            source: "chain".to_string(),
            turn_id: Some(format!("turn_{}", base + i)),
            session_id: Some("chain_session".to_string()),
            topic_label: Some("deployment".to_string()),
            llm_keywords: None,
            llm_compressed_summary: None,
            valence: None,
            arousal: None,
            chain_parent_id: if i > 0 { Some(format!("node_{}", base + i - 1)) } else { None },
            chain_label: Some("step".to_string()),
            domain_id: None,
            importance: None,
        })
        .collect();

    group.bench_function("crystallize_after_store", |b| {
        let (mut brain, _tmp) = make_bench_brain("crystallize");
        brain.batch_store(StoreBatch {
            items: chain_items.clone(),
        }).unwrap();
        // batch_store 打开 L4, L1, L2, L3；crystallize 需要 L5
        preopen_remaining_envs(&mut brain);

        b.iter(|| {
            brain.procedural_crystallize().unwrap();
        });
    });

    group.finish();
}

// ── 会话管理 ──────────────────────────────────────

fn bench_session_ops(c: &mut Criterion) {
    let mut group = c.benchmark_group("functional/session");
    group.sample_size(10);

    group.bench_function("activate_deactivate_cycle", |b| {
        let (mut brain, _tmp) = make_bench_brain("session");
        let items = generate_unique_items(50);
        brain.batch_store(StoreBatch { items }).unwrap();

        b.iter(|| {
            brain.session_mgr.activate("session_1", "topic_0", 3_600_000);
            brain.session_mgr.deactivate("session_1", "topic_0");
        });
    });

    group.finish();
}

// ── 知识库挂载 ──────────────────────────────────────

fn bench_shelf_ops(c: &mut Criterion) {
    let mut group = c.benchmark_group("functional/shelf");
    group.sample_size(10);

    group.bench_function("list_shelf", |b| {
        let (mut brain, tmp) = make_bench_brain("shelf_list");
        let mount_dir = tmp.path().join("shelf_docs");
        std::fs::create_dir_all(&mount_dir).unwrap();
        for i in 0..3 {
            std::fs::write(
                mount_dir.join(format!("file_{}.txt", i)),
                format!("Markdown file {} content", i),
            ).unwrap();
        }
        memhop_core::shelf::mount(&mut brain, mount_dir.to_str().unwrap(), ShelfDomain::Doc, "Code Docs").unwrap();

        b.iter(|| {
            memhop_core::shelf::list(&mut brain).unwrap();
        });
    });

    group.finish();
}

criterion_group!(
    benches,
    bench_full_store_recall_cycle,
    bench_batch_store_large,
    bench_dedup_performance,
    bench_l0_profile_ops,
    bench_l1_l2_l4_ops,
    bench_l3_domain_ops,
    bench_consolidate_pipeline,
    bench_crystallize_pipeline,
    bench_session_ops,
    bench_shelf_ops,
);
criterion_main!(benches);
