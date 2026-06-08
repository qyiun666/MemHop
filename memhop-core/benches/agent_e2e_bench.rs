//! Agent 端到端基准测试 — 模拟 meowAgent 完整 BrainLoop 流程。
//!
//! 覆盖：
//! - 单会话/多会话场景
//! - BrainLoop Stage 0-5 完整链路
//! - 情感维度系统
//! - L3 结晶化
//! - ActivationManager 串联

use criterion::{Criterion, criterion_group, criterion_main};
use memhop_core::{
    Brain, BrainConfig, RecallRequest, StoreBatch, StoreItem, Layer,
    EmotionalFeedback, CrystallizeL3Request, Emotion,
};
use memhop_core::bench_support::agent_simulator::{AgentSimulator, generate_multi_session_data};
use std::cell::RefCell;
use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};

/// 全局计数器
static AGENT_OFFSET: AtomicUsize = AtomicUsize::new(0);

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
    let _ = brain.batch_store(StoreBatch { items: dummy });
    let _ = brain.get_l0_profile();
    let _ = brain.list_crystals();
}

// ── 单会话 20 轮对话 ──────────────────────────────────────

fn bench_agent_single_session(c: &mut Criterion) {
    let mut group = c.benchmark_group("agent/single_session");
    group.sample_size(10);

    let (_tmp, mut brain) = make_brain_rev("agent_single");
    preopen_all_envs(&mut brain);

    // 初始化 L0 Profile
    let mut traits = std::collections::HashMap::new();
    traits.insert("language".to_string(), "zh".to_string());
    brain.set_l0(
        Some("cat_001".to_string()),
        Some("BenchAgent".to_string()),
        vec!["helpful".to_string()],
        vec!["accuracy".to_string()],
        vec!["evidence_based".to_string()],
        traits,
    ).unwrap();

    let brain = RefCell::new(brain);
    let mut simulator = AgentSimulator::new("bench_agent", 42);

    group.bench_function("20_turns", |b| {
        b.iter(|| {
            // 模拟 20 轮对话
            for turn in 0..20 {
                let result = simulator.simulate_turn();

                // 1. Store
                brain.borrow_mut().batch_store(StoreBatch {
                    items: result.store_items.clone(),
                }).unwrap();

                // 2. Recall
                let req = RecallRequest {
                    query: result.recall_query.clone(),
                    max_results: 5,
                    target_layers: vec![Layer::L1, Layer::L2],
                    ..Default::default()
                };
                let resp = brain.borrow_mut().recall(&req).unwrap();

                // 3. Emotional Feedback (every 5 turns)
                if turn % 5 == 0 && !resp.results.is_empty() {
                    let feedback = simulator.generate_emotional_feedback(
                        turn as u32,
                        &resp.results[0].id,
                    );
                    let _ = brain.borrow_mut().emotional_feedback(&feedback);
                }
            }
        });
    });

    group.finish();
}

// ── 多会话场景 ──────────────────────────────────────

fn bench_agent_multi_session(c: &mut Criterion) {
    let mut group = c.benchmark_group("agent/multi_session");
    group.sample_size(10);

    let sessions = generate_multi_session_data(5, 10);

    group.bench_function("5_sessions_x_10_turns", |b| {
        b.iter(|| {
            for (session_id, turns) in &sessions {
                let (_tmp, mut brain) = make_brain_rev(&format!("agent_{}", session_id));
                preopen_all_envs(&mut brain);

                for result in turns {
                    brain.batch_store(StoreBatch {
                        items: result.store_items.clone(),
                    }).unwrap();

                    let req = RecallRequest {
                        query: result.recall_query.clone(),
                        max_results: 5,
                        target_layers: vec![Layer::L1],
                        ..Default::default()
                    };
                    brain.recall(&req).unwrap();
                }
            }
        });
    });

    group.finish();
}

// ── BrainLoop Stage 分解 ──────────────────────────────────────

fn bench_brainloop_stages(c: &mut Criterion) {
    let mut group = c.benchmark_group("agent/brainloop_stages");
    group.sample_size(10);

    let (_tmp, mut brain) = make_brain_rev("agent_stages");
    preopen_all_envs(&mut brain);
    let brain = RefCell::new(brain);

    // Stage: Thalamus (topic extraction)
    group.bench_function("thalamus_topic_extraction", |b| {
        let mut simulator = AgentSimulator::new("bench", 42);
        b.iter(|| {
            let result = simulator.simulate_turn();
            std::hint::black_box(result.topic_label);
        });
    });

    // Stage: Recall
    group.bench_function("recall_query", |b| {
        // 预填充数据
        let items: Vec<StoreItem> = (0..100)
            .map(|i| StoreItem {
                text: format!("Memory item {} about programming", i),
                source: "bench".to_string(),
                turn_id: Some(format!("turn_{}", i)),
                session_id: Some("bench_session".to_string()),
                topic_label: Some("programming".to_string()),
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
        brain.borrow_mut().batch_store(StoreBatch { items }).unwrap();

        b.iter(|| {
            let req = RecallRequest {
                query: "programming language".to_string(),
                max_results: 10,
                target_layers: vec![Layer::L1, Layer::L2],
                ..Default::default()
            };
            brain.borrow_mut().recall(&req).unwrap();
        });
    });

    // Stage: Express (store)
    group.bench_function("express_store", |b| {
        let mut simulator = AgentSimulator::new("bench", 42);
        b.iter(|| {
            let result = simulator.simulate_turn();
            brain.borrow_mut().batch_store(StoreBatch {
                items: result.store_items,
            }).unwrap();
        });
    });

    // Stage: Reflect (emotional feedback)
    group.bench_function("reflect_emotional_feedback", |b| {
        let mut simulator = AgentSimulator::new("bench", 42);
        b.iter(|| {
            let feedback = simulator.generate_emotional_feedback(0, "node_0");
            let _ = brain.borrow_mut().emotional_feedback(&feedback);
        });
    });

    // Stage: Crystallize (L3)
    group.bench_function("crystallize_l3", |b| {
        // 预填充 L2 话题数据
        let items: Vec<StoreItem> = (0..50)
            .map(|i| StoreItem {
                text: format!("Topic memory {} about rust programming", i),
                source: "bench".to_string(),
                turn_id: Some(format!("turn_{}", i)),
                session_id: Some("crystallize_session".to_string()),
                topic_label: Some("rust_programming".to_string()),
                llm_keywords: Some(vec!["rust".to_string(), "programming".to_string()]),
                llm_compressed_summary: Some(format!("Summary {}", i)),
                valence: Some(0.6),
                arousal: Some(0.4),
                chain_parent_id: if i > 0 { Some(format!("turn_{}", i - 1)) } else { None },
                chain_label: Some("follow_up".to_string()),
                domain_id: None,
                importance: Some(0.7),
            })
            .collect();
        brain.borrow_mut().batch_store(StoreBatch { items }).unwrap();

        b.iter(|| {
            let req = CrystallizeL3Request {
                topic_id: "rust_programming".to_string(),
                summary: "Rust programming language fundamentals and best practices".to_string(),
                keywords: vec!["rust".to_string(), "programming".to_string(), "memory".to_string()],
                domain_name: Some("rust_knowledge".to_string()),
            };
            brain.borrow_mut().crystallize_l3(&req).unwrap();
        });
    });

    // Stage: Dream (consolidate)
    group.bench_function("dream_consolidate", |b| {
        b.iter(|| {
            brain.borrow_mut().consolidate().unwrap();
        });
    });

    group.finish();
}

// ── 情感维度系统 ──────────────────────────────────────

fn bench_emotion_system(c: &mut Criterion) {
    let mut group = c.benchmark_group("agent/emotion_system");
    group.sample_size(10);

    let (_tmp, mut brain) = make_brain_rev("agent_emotion");
    preopen_all_envs(&mut brain);

    // 预填充带情感的数据
    let items: Vec<StoreItem> = (0..100)
        .map(|i| {
            let emotion_idx = i % 5;
            let (valence, arousal) = match emotion_idx {
                0 => (0.8, 0.3), // Joy
                1 => (0.2, 0.7), // Anger
                2 => (0.6, 0.5), // Surprise
                3 => (0.3, 0.8), // Fear
                _ => (0.5, 0.5), // Neutral
            };
            StoreItem {
                text: format!("Emotional memory {} with feeling", i),
                source: "emotion_bench".to_string(),
                turn_id: Some(format!("turn_{}", i)),
                session_id: Some("emotion_session".to_string()),
                topic_label: Some("emotion_test".to_string()),
                llm_keywords: None,
                llm_compressed_summary: None,
                valence: Some(valence),
                arousal: Some(arousal),
                chain_parent_id: None,
                chain_label: None,
                domain_id: None,
                importance: Some(0.5 + valence as f32 * 0.3),
            }
        })
        .collect();
    brain.batch_store(StoreBatch { items }).unwrap();

    let brain = RefCell::new(brain);

    // emotional_feedback 延迟
    group.bench_function("emotional_feedback_latency", |b| {
        b.iter(|| {
            let feedback = EmotionalFeedback {
                memory_id: format!("node_{}", AGENT_OFFSET.fetch_add(1, Ordering::Relaxed) % 100),
                emotion: Emotion::Joy,
                intensity: 0.7,
                reason: Some("benchmark test".to_string()),
            };
            brain.borrow_mut().emotional_feedback(&feedback);
        });
    });

    // recall_by_emotion 延迟
    group.bench_function("recall_by_emotion_latency", |b| {
        b.iter(|| {
            let req = memhop_core::EmotionRecallRequest {
                emotion: Some(Emotion::Joy),
                max_results: 10,
                min_intensity: 0.3,
                ..Default::default()
            };
            brain.borrow_mut().recall_by_emotion(&req).unwrap();
        });
    });

    group.finish();
}

// ── L3 结晶化管道 ──────────────────────────────────────

fn bench_l3_crystallize(c: &mut Criterion) {
    let mut group = c.benchmark_group("agent/l3_crystallize");
    group.sample_size(10);

    group.bench_function("full_pipeline", |b| {
        b.iter(|| {
            let (_tmp, mut brain) = make_brain_rev("l3_cryst");
            preopen_all_envs(&mut brain);

            // 1. 存储 L1 数据
            let items: Vec<StoreItem> = (0..30)
                .map(|i| StoreItem {
                    text: format!("L3 candidate memory {} about deep learning", i),
                    source: "l3_bench".to_string(),
                    turn_id: Some(format!("turn_{}", i)),
                    session_id: Some("l3_session".to_string()),
                    topic_label: Some("deep_learning".to_string()),
                    llm_keywords: Some(vec!["deep".to_string(), "learning".to_string()]),
                    llm_compressed_summary: Some(format!("Summary {}", i)),
                    valence: Some(0.6),
                    arousal: Some(0.4),
                    chain_parent_id: if i > 0 { Some(format!("turn_{}", i - 1)) } else { None },
                    chain_label: Some("sequence".to_string()),
                    domain_id: None,
                    importance: Some(0.7),
                })
                .collect();
            brain.batch_store(StoreBatch { items }).unwrap();

            // 2. 结晶化
            let req = CrystallizeL3Request {
                topic_id: "deep_learning".to_string(),
                summary: "Deep learning fundamentals and applications".to_string(),
                keywords: vec!["deep".to_string(), "learning".to_string(), "neural".to_string()],
                domain_name: Some("dl_knowledge".to_string()),
            };
            brain.crystallize_l3(&req).unwrap();

            // 3. 从 L3 检索
            let recall_req = RecallRequest {
                query: "neural network".to_string(),
                max_results: 5,
                target_layers: vec![Layer::L3],
                ..Default::default()
            };
            brain.recall(&recall_req).unwrap();
        });
    });

    group.finish();
}

// ── ActivationManager 串联 ──────────────────────────────────────

fn bench_activation_lifecycle(c: &mut Criterion) {
    let mut group = c.benchmark_group("agent/activation_lifecycle");
    group.sample_size(10);

    let (_tmp, mut brain) = make_brain_rev("agent_activation");
    preopen_all_envs(&mut brain);

    // 预填充数据
    let items: Vec<StoreItem> = (0..50)
        .map(|i| StoreItem {
            text: format!("Activation test memory {}", i),
            source: "activation_bench".to_string(),
            turn_id: Some(format!("turn_{}", i)),
            session_id: Some("activation_session".to_string()),
            topic_label: Some(format!("topic_{}", i % 5)),
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

    // Activate/Deactivate cycle
    group.bench_function("activate_deactivate_cycle", |b| {
        b.iter(|| {
            brain.borrow_mut().activate_topic("session_1", "topic_0", 3_600_000);
            brain.borrow_mut().deactivate_topic("session_1", "topic_0");
        });
    });

    // Recall with activation boost
    group.bench_function("recall_with_activation", |b| {
        brain.borrow_mut().activate_topic("session_1", "topic_0", 3_600_000);

        b.iter(|| {
            let req = RecallRequest {
                query: "activation test".to_string(),
                max_results: 10,
                target_layers: vec![Layer::L1],
                ..Default::default()
            };
            brain.borrow_mut().recall(&req).unwrap();
        });
    });

    group.finish();
}

// ── 并发 Agent 测试 ──────────────────────────────────────

fn bench_concurrent_agents(c: &mut Criterion) {
    let mut group = c.benchmark_group("agent/concurrent");
    group.sample_size(10);

    // 预创建 5 个独立 Brain
    let brains: Vec<_> = (0..5)
        .map(|i| {
            let (_tmp, mut brain) = make_brain_rev(&format!("concurrent_{}", i));
            let items: Vec<StoreItem> = (0..20)
                .map(|j| StoreItem {
                    text: format!("Agent {} memory {}", i, j),
                    source: "concurrent_bench".to_string(),
                    turn_id: Some(format!("turn_{}", j)),
                    session_id: Some(format!("session_{}", i)),
                    topic_label: Some(format!("topic_{}", j % 3)),
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
            (_tmp, RefCell::new(brain))
        })
        .collect();

    group.bench_function("recall_across_5_agents", |b| {
        b.iter(|| {
            for (_, brain) in &brains {
                let req = RecallRequest {
                    query: "memory".to_string(),
                    max_results: 5,
                    target_layers: vec![Layer::L1],
                    ..Default::default()
                };
                brain.borrow_mut().recall(&req).unwrap();
            }
        });
    });

    group.finish();
}

criterion_group!(
    benches,
    bench_agent_single_session,
    bench_agent_multi_session,
    bench_brainloop_stages,
    bench_emotion_system,
    bench_l3_crystallize,
    bench_activation_lifecycle,
    bench_concurrent_agents,
);
criterion_main!(benches);
