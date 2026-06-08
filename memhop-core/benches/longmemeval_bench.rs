//! LongMemEval 基准测试 — 评估 MemHop 长期记忆能力。
//!
//! 评估 5 个核心能力：
//! 1. 信息提取 (Information Extraction)
//! 2. 多跳推理 (Multi-hop Reasoning)
//! 3. 时序推理 (Temporal Reasoning)
//! 4. 知识更新 (Knowledge Update)
//! 5. 会话摘要 (Session Summary)

use criterion::{criterion_group, criterion_main, Criterion};
use memhop_core::{
    Brain, BrainConfig, Encoder, NgramEncoder, RecallRequest, StoreBatch, StoreItem, Layer,
};
use memhop_core::bench_support::dataset_loader::LongMemEvalDataset;
use std::cell::RefCell;
use std::sync::Arc;
use std::time::Duration;
use tempfile::TempDir;

/// 创建 Brain 实例。
fn make_brain(agent_id: &str) -> (TempDir, Brain) {
    let tmp = tempfile::tempdir().unwrap();
    let cfg = BrainConfig {
        brains_dir: tmp.path().to_str().unwrap().to_string(),
        agent_id: agent_id.to_string(),
    };
    let encoder: Arc<Box<dyn Encoder>> = Arc::new(Box::new(NgramEncoder::new(1024)));
    let brain = Brain::open(cfg, encoder).unwrap();
    (tmp, brain)
}

/// 预开所有 LMDB 环境。
fn preopen_all_envs(brain: &mut Brain) {
    let dummy = vec![StoreItem {
        text: "__preopen__".to_string(),
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
    brain.batch_store(StoreBatch { items: dummy }).unwrap();
    brain.recall(&RecallRequest {
        query: "__preopen__".to_string(),
        max_results: 1,
        target_layers: vec![Layer::L1, Layer::L2, Layer::L3, Layer::L4],
        ..Default::default()
    }).unwrap();
}

/// 将 LongMemEval 数据转换为 StoreItem。
fn session_to_store_items(session: &memhop_core::bench_support::dataset_loader::MemorySession) -> Vec<StoreItem> {
    let mut items = Vec::new();
    
    for (i, turn) in session.turns.iter().enumerate() {
        items.push(StoreItem {
            text: turn.content.clone(),
            source: "longmemeval".to_string(),
            turn_id: Some(format!("{}_{}", session.session_id, i)),
            session_id: Some(session.session_id.clone()),
            topic_label: Some(format!("topic_{}", i % 5)),
            llm_keywords: Some(vec![
                turn.content.split_whitespace().next().unwrap_or("word").to_lowercase(),
            ]),
            llm_compressed_summary: Some(turn.content[..turn.content.len().min(50)].to_string()),
            valence: Some(0.5),
            arousal: Some(0.3),
            chain_parent_id: if i > 0 {
                Some(format!("{}_{}", session.session_id, i - 1))
            } else {
                None
            },
            chain_label: Some("conversation".to_string()),
            domain_id: None,
            importance: Some(0.6),
        });
    }
    
    items
}

/// 评估信息提取能力。
fn eval_information_extraction(brain: &RefCell<Brain>, dataset: &LongMemEvalDataset) -> (usize, usize) {
    let mut correct = 0;
    let mut total = 0;
    
    for session in &dataset.sessions {
        // 存储会话数据
        let items = session_to_store_items(session);
        brain.borrow_mut().batch_store(StoreBatch { items }).unwrap();
        
        // 测试信息提取问题
        for question in &session.questions {
            let req = RecallRequest {
                query: question.question.clone(),
                max_results: 5,
                target_layers: vec![Layer::L1, Layer::L2],
                ..Default::default()
            };
            
            if let Ok(resp) = brain.borrow_mut().recall(&req) {
                total += 1;
                // 检查是否召回了相关 turn
                for relevant_id in &question.relevant_turn_ids {
                    let expected_id = format!("{}_{}", session.session_id, relevant_id);
                    if resp.results.iter().any(|r| r.id.contains(&expected_id)) {
                        correct += 1;
                        break;
                    }
                }
            }
        }
    }
    
    (correct, total)
}

/// 评估多跳推理能力。
fn eval_multi_hop_reasoning(brain: &RefCell<Brain>, dataset: &LongMemEvalDataset) -> (usize, usize) {
    let mut correct = 0;
    let mut total = 0;
    
    // 多跳问题：需要跨多个 turn 推理
    for session in &dataset.sessions {
        // 构造多跳查询
        if session.turns.len() >= 4 {
            let turn_0 = &session.turns[0];
            
            // 查询："What was discussed after X?"
            let query = format!("What was discussed after: {}", turn_0.content);
            let req = RecallRequest {
                query: query.clone(),
                max_results: 5,
                target_layers: vec![Layer::L1, Layer::L2],
                ..Default::default()
            };
            
            if let Ok(resp) = brain.borrow_mut().recall(&req) {
                total += 1;
                // 检查是否召回了 turn 2
                let has_turn_2 = resp.results.iter().any(|r| {
                    r.id.contains(&format!("{}_{}", session.session_id, 2))
                });
                if has_turn_2 {
                    correct += 1;
                }
            }
        }
    }
    
    (correct, total)
}

/// 评估时序推理能力。
fn eval_temporal_reasoning(brain: &RefCell<Brain>, dataset: &LongMemEvalDataset) -> (usize, usize) {
    let mut correct = 0;
    let mut total = 0;
    
    // 时序问题：需要理解时间顺序
    for session in &dataset.sessions {
        if session.turns.len() >= 6 {
            // 查询最近的对话
            let query = format!("What was the last topic discussed in {}?", session.session_id);
            let req = RecallRequest {
                query: query.clone(),
                max_results: 3,
                target_layers: vec![Layer::L1, Layer::L2],
                ..Default::default()
            };
            
            if let Ok(resp) = brain.borrow_mut().recall(&req) {
                total += 1;
                // 检查是否召回了最后几个 turn
                let last_turn_idx = session.turns.len() - 1;
                let has_recent = resp.results.iter().any(|r| {
                    r.id.contains(&format!("{}_{}", session.session_id, last_turn_idx))
                });
                if has_recent {
                    correct += 1;
                }
            }
        }
    }
    
    (correct, total)
}

/// LongMemEval 端到端评估。
fn bench_longmemeval_e2e(c: &mut Criterion) {
    let mut group = c.benchmark_group("longmemeval");
    group.sample_size(10);
    group.measurement_time(Duration::from_secs(10));
    
    let dataset = LongMemEvalDataset::synthesize();
    
    group.bench_function("e2e_store_and_eval", |b| {
        b.iter(|| {
            let (_tmp, mut brain) = make_brain("longmemeval");
            preopen_all_envs(&mut brain);
            let brain = RefCell::new(brain);
            
            // 评估信息提取
            let (ie_correct, ie_total) = eval_information_extraction(&brain, &dataset);
            
            // 评估多跳推理
            let (mh_correct, mh_total) = eval_multi_hop_reasoning(&brain, &dataset);
            
            // 评估时序推理
            let (tr_correct, tr_total) = eval_temporal_reasoning(&brain, &dataset);
            
            // 计算总分
            let total_correct = ie_correct + mh_correct + tr_correct;
            let total_questions = ie_total + mh_total + tr_total;
            let _accuracy = if total_questions > 0 {
                total_correct as f64 / total_questions as f64
            } else {
                0.0
            };
        });
    });
    
    group.finish();
}

/// 信息提取基准测试。
fn bench_information_extraction(c: &mut Criterion) {
    let mut group = c.benchmark_group("longmemeval/information_extraction");
    group.sample_size(10);
    
    let dataset = LongMemEvalDataset::synthesize();
    let (_tmp, mut brain) = make_brain("longmemeval_ie");
    preopen_all_envs(&mut brain);
    let brain = RefCell::new(brain);
    
    // 预填充数据
    for session in &dataset.sessions {
        let items = session_to_store_items(session);
        brain.borrow_mut().batch_store(StoreBatch { items }).unwrap();
    }
    
    group.bench_function("extract_50_questions", |b| {
        b.iter(|| {
            let (correct, total) = eval_information_extraction(&brain, &dataset);
            let _accuracy = correct as f64 / total.max(1) as f64;
        });
    });
    
    group.finish();
}

/// 多跳推理基准测试。
fn bench_multi_hop_reasoning(c: &mut Criterion) {
    let mut group = c.benchmark_group("longmemeval/multi_hop_reasoning");
    group.sample_size(10);
    
    let dataset = LongMemEvalDataset::synthesize();
    let (_tmp, mut brain) = make_brain("longmemeval_mh");
    preopen_all_envs(&mut brain);
    let brain = RefCell::new(brain);
    
    // 预填充数据
    for session in &dataset.sessions {
        let items = session_to_store_items(session);
        brain.borrow_mut().batch_store(StoreBatch { items }).unwrap();
    }
    
    group.bench_function("reason_10_sessions", |b| {
        b.iter(|| {
            let (correct, total) = eval_multi_hop_reasoning(&brain, &dataset);
            let _accuracy = correct as f64 / total.max(1) as f64;
        });
    });
    
    group.finish();
}

/// 时序推理基准测试。
fn bench_temporal_reasoning(c: &mut Criterion) {
    let mut group = c.benchmark_group("longmemeval/temporal_reasoning");
    group.sample_size(10);
    
    let dataset = LongMemEvalDataset::synthesize();
    let (_tmp, mut brain) = make_brain("longmemeval_tr");
    preopen_all_envs(&mut brain);
    let brain = RefCell::new(brain);
    
    // 预填充数据
    for session in &dataset.sessions {
        let items = session_to_store_items(session);
        brain.borrow_mut().batch_store(StoreBatch { items }).unwrap();
    }
    
    group.bench_function("temporal_10_sessions", |b| {
        b.iter(|| {
            let (correct, total) = eval_temporal_reasoning(&brain, &dataset);
            let _accuracy = correct as f64 / total.max(1) as f64;
        });
    });
    
    group.finish();
}

criterion_group!(
    benches,
    bench_longmemeval_e2e,
    bench_information_extraction,
    bench_multi_hop_reasoning,
    bench_temporal_reasoning,
);
criterion_main!(benches);
