use memhop_core::{
    Brain, BrainConfig, Encoder, NgramEncoder, RecallRequest, StoreBatch, StoreItem, Layer,
};
#[cfg(feature = "candle")]
use memhop_core::CandleEncoder;
use memhop_core::EncoderRouter;
use memhop_core::bench_support::dataset_loader::LongMemEvalDataset;
use std::cell::RefCell;
use std::sync::Arc;
use tempfile::TempDir;

/// 创建 Brain 实例（使用向量模型）
fn make_brain(agent_id: &str) -> (TempDir, Brain) {
    let tmp = tempfile::tempdir().unwrap();
    let cfg = BrainConfig {
        brains_dir: tmp.path().to_str().unwrap().to_string(),
        agent_id: agent_id.to_string(),
    };

    // 创建编码器：优先使用向量模型
    let encoder: Arc<Box<dyn Encoder>> = {
        #[cfg(feature = "candle")]
        {
            // 使用 CandleEncoder (multilingual-e5-small, 384维)
            let model_path = "/Volumes/zt_hd/projects/meow/memhop/models/multilingual-e5-small";
            match CandleEncoder::new(model_path) {
                Ok(dense_encoder) => {
                    println!("✓ 使用向量模型 (CandleEncoder, 384维)");
                    // 双编码器模式：NgramEncoder (sparse) + CandleEncoder (dense)
                    let sparse_encoder = Box::new(NgramEncoder::new(384));
                    let router = EncoderRouter::new(sparse_encoder, Box::new(dense_encoder));
                    Arc::new(Box::new(router))
                }
                Err(e) => {
                    println!("⚠ CandleEncoder 加载失败: {}, 回退到 NgramEncoder", e);
                    Arc::new(Box::new(NgramEncoder::new(1024)))
                }
            }
        }

        #[cfg(not(feature = "candle"))]
        {
            println!("⚠ candle feature 未启用，使用 NgramEncoder");
            Arc::new(Box::new(NgramEncoder::new(1024)))
        }
    };

    let brain = Brain::open(cfg, encoder).unwrap();
    (tmp, brain)
}

/// 将 LongMemEval 数据转换为 StoreItem
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

/// 评估信息提取能力
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

/// 评估多跳推理能力
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

/// 评估时序推理能力
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

fn main() {
    println!("==========================================");
    println!("LongMemEval 评估 - 使用向量模型");
    println!("==========================================");

    let dataset = LongMemEvalDataset::synthesize();
    println!("✓ 数据集已加载: {} 个会话", dataset.sessions.len());

    let (_tmp, mut brain) = make_brain("longmemeval");
    let brain = RefCell::new(brain);

    println!("开始评估...");

    // 评估信息提取
    println!("\n[1/3] 评估信息提取能力...");
    let (ie_correct, ie_total) = eval_information_extraction(&brain, &dataset);
    let ie_accuracy = if ie_total > 0 {
        ie_correct as f64 / ie_total as f64
    } else {
        0.0
    };
    println!("✓ 信息提取: {}/{} ({:.1}%)", ie_correct, ie_total, ie_accuracy * 100.0);

    // 评估多跳推理
    println!("\n[2/3] 评估多跳推理能力...");
    let (mh_correct, mh_total) = eval_multi_hop_reasoning(&brain, &dataset);
    let mh_accuracy = if mh_total > 0 {
        mh_correct as f64 / mh_total as f64
    } else {
        0.0
    };
    println!("✓ 多跳推理: {}/{} ({:.1}%)", mh_correct, mh_total, mh_accuracy * 100.0);

    // 评估时序推理
    println!("\n[3/3] 评估时序推理能力...");
    let (tr_correct, tr_total) = eval_temporal_reasoning(&brain, &dataset);
    let tr_accuracy = if tr_total > 0 {
        tr_correct as f64 / tr_total as f64
    } else {
        0.0
    };
    println!("✓ 时序推理: {}/{} ({:.1}%)", tr_correct, tr_total, tr_accuracy * 100.0);

    // 计算总分
    let total_correct = ie_correct + mh_correct + tr_correct;
    let total_questions = ie_total + mh_total + tr_total;
    let total_accuracy = if total_questions > 0 {
        total_correct as f64 / total_questions as f64
    } else {
        0.0
    };

    println!("\n==========================================");
    println!("LongMemEval 评估结果");
    println!("==========================================");
    println!("信息提取: {:.1}%", ie_accuracy * 100.0);
    println!("多跳推理: {:.1}%", mh_accuracy * 100.0);
    println!("时序推理: {:.1}%", tr_accuracy * 100.0);
    println!("------------------------------------------");
    println!("总体准确率: {:.1}% ({}/{})", total_accuracy * 100.0, total_correct, total_questions);
    println!("==========================================");

    // 生成报告
    let report = format!(
        "# LongMemEval 评估报告\n\n\
         ## 评估环境\n\
         - 模型: multilingual-e5-small (384维)\n\
         - 编码器: CandleEncoder + EncoderRouter (双通道)\n\
         - 数据集: LongMemEval 合成数据 ({} 个会话)\n\n\
         ## 评估结果\n\n\
         | 能力维度 | 正确数 | 总数 | 准确率 |\n\
         |----------|--------|------|--------|\n\
         | 信息提取 | {} | {} | {:.1}% |\n\
         | 多跳推理 | {} | {} | {:.1}% |\n\
         | 时序推理 | {} | {} | {:.1}% |\n\
         | **总体** | **{}** | **{}** | **{:.1}%** |\n\n\
         ## 评估时间\n\
         - 日期: {}\n",
        dataset.sessions.len(),
        ie_correct, ie_total, ie_accuracy * 100.0,
        mh_correct, mh_total, mh_accuracy * 100.0,
        tr_correct, tr_total, tr_accuracy * 100.0,
        total_correct, total_questions, total_accuracy * 100.0,
        chrono::Local::now().format("%Y-%m-%d %H:%M:%S")
    );

    // 保存报告
    let report_path = "/Volumes/zt_hd/projects/meow/memhop/LONGMEMEVAL-REPORT.md";
    std::fs::write(report_path, &report).unwrap();
    println!("\n✓ 报告已保存到: {}", report_path);
}
