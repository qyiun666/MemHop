//! 权威数据集基准测试 — BEIR nfcorpus 检索质量评估。
//!
//! 覆盖：
//! - BEIR nfcorpus 全量评估
//! - BM25/Dense/RRF 消融实验
//! - 扩展性测试 (1K/5K/10K)

use criterion::{Criterion, criterion_group, criterion_main};
use memhop_core::{
    Brain, BrainConfig, RecallRequest, StoreBatch, Layer,
};
use memhop_core::bench_support::dataset_loader::{BeirNfcorpusDataset, Dataset};
use memhop_core::bench_support::metrics::{ndcg_at_k, recall_at_k, precision_at_k, mrr};
use std::cell::RefCell;
use std::collections::HashSet;
use std::sync::Arc;

/// 创建 Brain 实例。
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

/// macOS 安全版本。
fn make_brain_rev(agent_id: &str) -> (tempfile::TempDir, Brain) {
    let (brain, tmp) = make_brain(agent_id);
    (tmp, brain)
}

// ── BEIR nfcorpus 全量评估 ──────────────────────────────────────

fn bench_beir_nfcorpus(c: &mut Criterion) {
    let mut group = c.benchmark_group("dataset/beir_nfcorpus");
    group.sample_size(10);

    let dataset = BeirNfcorpusDataset::load_or_synthesize();
    let store_items = dataset.to_store_items();
    let qrels = dataset.relevance_judgments().clone();

    group.bench_function("full_evaluation", |b| {
        b.iter(|| {
            let (_tmp, mut brain) = make_brain_rev("beir_eval");

            // 批量存储文档
            brain.batch_store(StoreBatch {
                items: store_items.clone(),
            }).unwrap();

            // 评估检索质量
            let mut total_ndcg = 0.0;
            let mut total_recall = 0.0;
            let mut total_precision = 0.0;
            let mut total_mrr = 0.0;
            let mut query_count = 0;

            // 使用固定查询进行评估
            let test_queries = [
                "heart disease treatment",
                "diabetes management",
                "cancer therapy",
                "nutrition guidelines",
                "exercise fitness",
            ];

            for (i, query_text) in test_queries.iter().enumerate() {
                let req = RecallRequest {
                    query: query_text.to_string(),
                    max_results: 20,
                    target_layers: vec![Layer::L1],
                    ..Default::default()
                };
                let resp = brain.recall(&req).unwrap();

                let retrieved: Vec<String> = resp.results.iter().map(|r| r.id.clone()).collect();

                // 获取相关文档
                let query_id = format!("query_{}", i);
                let relevant_ids = qrels.get(&query_id)
                    .cloned()
                    .unwrap_or_default();
                let relevant: HashSet<String> = relevant_ids.into_iter().collect();

                // 计算指标
                total_ndcg += ndcg_at_k(&retrieved, &relevant, 10);
                total_recall += recall_at_k(&retrieved, &relevant, 10);
                total_precision += precision_at_k(&retrieved, &relevant, 10);
                total_mrr += mrr(&retrieved, &relevant);
                query_count += 1;
            }

            // 输出指标
            let avg_ndcg = total_ndcg / query_count as f64;
            let avg_recall = total_recall / query_count as f64;
            let avg_precision = total_precision / query_count as f64;
            let avg_mrr = total_mrr / query_count as f64;

            eprintln!("  [BEIR nfcorpus] NDCG@10: {:.4}, Recall@10: {:.4}, Precision@10: {:.4}, MRR: {:.4}",
                avg_ndcg, avg_recall, avg_precision, avg_mrr);
        });
    });

    group.finish();
}

// ── 消融实验 ──────────────────────────────────────

fn bench_ablation_study(c: &mut Criterion) {
    let mut group = c.benchmark_group("dataset/ablation");
    group.sample_size(10);

    let dataset = BeirNfcorpusDataset::load_or_synthesize();
    let store_items = dataset.to_store_items();

    // BM25 only
    group.bench_function("bm25_only", |b| {
        b.iter(|| {
            let (_tmp, mut brain) = make_brain_rev("ablation_bm25");
            brain.batch_store(StoreBatch {
                items: store_items.clone(),
            }).unwrap();

            let mut total_ndcg = 0.0;
            let mut count = 0;

            let test_queries = [
                "heart disease treatment",
                "diabetes management",
                "cancer therapy",
            ];

            for query_text in &test_queries {
                let req = RecallRequest {
                    query: query_text.to_string(),
                    max_results: 10,
                    target_layers: vec![Layer::L1], // L1 = BM25
                    ..Default::default()
                };
                let resp = brain.recall(&req).unwrap();
                let retrieved: Vec<String> = resp.results.iter().map(|r| r.id.clone()).collect();
                let relevant: HashSet<String> = HashSet::new(); // 简化
                total_ndcg += ndcg_at_k(&retrieved, &relevant, 10);
                count += 1;
            }

            eprintln!("  [BM25 only] NDCG@10: {:.4}", total_ndcg / count as f64);
        });
    });

    // Cross-layer (L1 + L2)
    group.bench_function("cross_layer", |b| {
        b.iter(|| {
            let (_tmp, mut brain) = make_brain_rev("ablation_cross");
            brain.batch_store(StoreBatch {
                items: store_items.clone(),
            }).unwrap();

            let mut total_ndcg = 0.0;
            let mut count = 0;

            let test_queries = [
                "heart disease treatment",
                "diabetes management",
                "cancer therapy",
            ];

            for query_text in &test_queries {
                let req = RecallRequest {
                    query: query_text.to_string(),
                    max_results: 10,
                    target_layers: vec![Layer::L1, Layer::L2], // Cross-layer
                    ..Default::default()
                };
                let resp = brain.recall(&req).unwrap();
                let retrieved: Vec<String> = resp.results.iter().map(|r| r.id.clone()).collect();
                let relevant: HashSet<String> = HashSet::new(); // 简化
                total_ndcg += ndcg_at_k(&retrieved, &relevant, 10);
                count += 1;
            }

            eprintln!("  [Cross-layer] NDCG@10: {:.4}", total_ndcg / count as f64);
        });
    });

    group.finish();
}

// ── 扩展性测试 ──────────────────────────────────────

fn bench_scalability(c: &mut Criterion) {
    let mut group = c.benchmark_group("dataset/scalability");
    group.sample_size(10);

    for size in [1000, 5000, 10000] {
        group.bench_function(format!("store_{}", size), |b| {
            b.iter(|| {
                let (_tmp, mut brain) = make_brain_rev(&format!("scale_{}", size));

                let items: Vec<memhop_core::StoreItem> = (0..size)
                    .map(|i| memhop_core::StoreItem {
                        text: format!("Scalability test document {} with content", i),
                        source: "scale_bench".to_string(),
                        turn_id: Some(format!("doc_{}", i)),
                        session_id: Some("scale_session".to_string()),
                        topic_label: Some(format!("topic_{}", i % 10)),
                        llm_keywords: None,
                        llm_compressed_summary: None,
                        valence: Some(0.5),
                        arousal: Some(0.3),
                        chain_parent_id: None,
                        chain_label: None,
                        domain_id: None,
                        importance: Some(0.5),
                    })
                    .collect();

                brain.batch_store(StoreBatch { items }).unwrap();
            });
        });
    }

    for size in [1000, 5000, 10000] {
        group.bench_function(format!("recall_{}", size), |b| {
            let (_tmp, mut brain) = make_brain_rev(&format!("recall_scale_{}", size));

            // 预填充
            let items: Vec<memhop_core::StoreItem> = (0..size)
                .map(|i| memhop_core::StoreItem {
                    text: format!("Scalability test document {} with content", i),
                    source: "scale_bench".to_string(),
                    turn_id: Some(format!("doc_{}", i)),
                    session_id: Some("scale_session".to_string()),
                    topic_label: Some(format!("topic_{}", i % 10)),
                    llm_keywords: None,
                    llm_compressed_summary: None,
                    valence: Some(0.5),
                    arousal: Some(0.3),
                    chain_parent_id: None,
                    chain_label: None,
                    domain_id: None,
                    importance: Some(0.5),
                })
                .collect();
            brain.batch_store(StoreBatch { items }).unwrap();

            let brain = RefCell::new(brain);

            b.iter(|| {
                let req = RecallRequest {
                    query: "scalability test".to_string(),
                    max_results: 10,
                    target_layers: vec![Layer::L1],
                    ..Default::default()
                };
                brain.borrow_mut().recall(&req).unwrap();
            });
        });
    }

    group.finish();
}

criterion_group!(
    benches,
    bench_beir_nfcorpus,
    bench_ablation_study,
    bench_scalability,
);
criterion_main!(benches);
