//! Agent 行为模拟器 — 模拟 meowAgent 完整 BrainLoop 流程。
//!
//! 设计原则：
//! - 确定性：给定相同 seed，产生相同结果
//! - 完整覆盖：覆盖 BrainLoop Stage 0-5
//! - 无外部依赖：不依赖实际 meowAgent 进程

use crate::types::{Emotion, EmotionalFeedback, StoreItem};
use std::collections::hash_map::DefaultHasher;
use std::hash::{Hash, Hasher};

/// Agent 会话状态。
pub struct AgentSimulator {
    pub agent_id: String,
    pub session_id: String,
    pub turn_counter: u32,
    pub seed: u64,
}

/// 单轮对话结果。
#[derive(Debug, Clone)]
pub struct TurnResult {
    pub turn_id: u32,
    pub user_input: String,
    pub store_items: Vec<StoreItem>,
    pub recall_query: String,
    pub topic_label: String,
    pub keywords: Vec<String>,
    pub emotion: Emotion,
    pub valence: f64,
    pub arousal: f64,
}

/// 会话报告。
#[derive(Debug)]
pub struct SessionReport {
    pub total_turns: u32,
    pub total_stored: usize,
    pub total_recalled: usize,
    pub topics_created: Vec<String>,
    pub emotions_detected: Vec<Emotion>,
}

/// 预定义的对话场景。
const DIALOGUE_SCENES: &[(&str, &str, &str, Emotion, f64, f64)] = &[
    // (user_input, topic, keyword, emotion, valence, arousal)
    ("我想学习 Rust 编程语言", "rust_programming", "rust", Emotion::Surprise, 0.7, 0.6),
    ("Rust 的所有权系统很强大", "rust_programming", "ownership", Emotion::Joy, 0.8, 0.5),
    ("如何处理 Rust 中的错误？", "rust_programming", "error_handling", Emotion::Surprise, 0.6, 0.5),
    ("Python 适合数据分析吗？", "python_data_science", "python", Emotion::Surprise, 0.5, 0.4),
    ("机器学习模型训练很慢", "machine_learning", "training", Emotion::Anger, 0.3, 0.7),
    ("今天天气不错", "daily_chat", "weather", Emotion::Joy, 0.8, 0.3),
    ("我需要优化数据库查询", "database_optimization", "sql", Emotion::Surprise, 0.6, 0.6),
    ("这个 bug 让我很困扰", "debugging", "bug", Emotion::Anger, 0.2, 0.8),
    ("终于解决了这个问题！", "debugging", "solution", Emotion::Joy, 0.9, 0.7),
    ("Kubernetes 部署很复杂", "devops", "k8s", Emotion::Anger, 0.3, 0.6),
    ("Docker 容器化很方便", "devops", "docker", Emotion::Joy, 0.7, 0.4),
    ("需要设计一个微服务架构", "architecture", "microservices", Emotion::Surprise, 0.6, 0.5),
    ("API 设计要考虑版本控制", "api_design", "versioning", Emotion::Surprise, 0.5, 0.4),
    ("性能测试显示 P99 延迟过高", "performance", "latency", Emotion::Fear, 0.3, 0.7),
    ("优化后性能提升了 50%", "performance", "optimization", Emotion::Joy, 0.9, 0.6),
    ("代码审查发现了安全漏洞", "security", "vulnerability", Emotion::Fear, 0.2, 0.8),
    ("需要实现 CI/CD 流水线", "devops", "cicd", Emotion::Surprise, 0.5, 0.5),
    ("测试覆盖率需要提高", "testing", "coverage", Emotion::Surprise, 0.4, 0.5),
    ("文档需要更新", "documentation", "docs", Emotion::Neutral, 0.5, 0.3),
    ("团队协作很重要", "teamwork", "collaboration", Emotion::Joy, 0.7, 0.4),
];

impl AgentSimulator {
    /// 创建新的 Agent 模拟器。
    pub fn new(agent_id: &str, seed: u64) -> Self {
        Self {
            agent_id: agent_id.to_string(),
            session_id: format!("session_{}", seed),
            turn_counter: 0,
            seed,
        }
    }

    /// 模拟单轮对话。
    pub fn simulate_turn(&mut self) -> TurnResult {
        let scene_idx = (self.turn_counter as usize) % DIALOGUE_SCENES.len();
        let (user_input, topic, keyword, emotion, valence, arousal) = DIALOGUE_SCENES[scene_idx];

        let turn_id = self.turn_counter;
        self.turn_counter += 1;

        // 生成 StoreItem
        let store_item = StoreItem {
            text: format!("{} [turn_{}]", user_input, turn_id),
            source: "agent_simulator".to_string(),
            turn_id: Some(format!("turn_{}", turn_id)),
            session_id: Some(self.session_id.clone()),
            topic_label: Some(topic.to_string()),
            llm_keywords: Some(vec![
                keyword.to_string(),
                format!("turn_{}", turn_id),
            ]),
            llm_compressed_summary: Some(format!(
                "User asked about {} in turn {}",
                keyword, turn_id
            )),
            valence: Some(valence),
            arousal: Some(arousal),
            chain_parent_id: if turn_id > 0 {
                Some(format!("turn_{}", turn_id - 1))
            } else {
                None
            },
            chain_label: Some("conversation".to_string()),
            domain_id: None,
            importance: Some(0.5 + (valence as f32 * 0.3)),
            is_structural: None,
            source_ref: None,
            skeletal_text: None,
        };

        TurnResult {
            turn_id,
            user_input: user_input.to_string(),
            store_items: vec![store_item],
            recall_query: format!("{} {}", keyword, topic),
            topic_label: topic.to_string(),
            keywords: vec![keyword.to_string()],
            emotion,
            valence,
            arousal,
        }
    }

    /// 模拟多轮对话。
    pub fn simulate_session(&mut self, turns: u32) -> SessionReport {
        let mut topics = Vec::new();
        let mut emotions = Vec::new();
        let mut total_stored = 0;

        for _ in 0..turns {
            let result = self.simulate_turn();
            total_stored += result.store_items.len();
            if !topics.contains(&result.topic_label) {
                topics.push(result.topic_label);
            }
            emotions.push(result.emotion);
        }

        SessionReport {
            total_turns: turns,
            total_stored,
            total_recalled: 0, // 需要在 benchmark 中实际调用 recall 后更新
            topics_created: topics,
            emotions_detected: emotions,
        }
    }

    /// 生成情感反馈。
    pub fn generate_emotional_feedback(&self, turn_id: u32, memory_id: &str) -> EmotionalFeedback {
        let scene_idx = (turn_id as usize) % DIALOGUE_SCENES.len();
        let (_, _, _, emotion, valence, _arousal) = DIALOGUE_SCENES[scene_idx];

        EmotionalFeedback {
            memory_id: memory_id.to_string(),
            emotion,
            intensity: 0.5 + (valence as f32 * 0.3),
            reason: Some(format!("Feedback from turn {}", turn_id)),
        }
    }

    /// 确定性哈希（用于生成一致的测试数据）。
    #[allow(dead_code)]
    fn deterministic_hash(&self, input: &str) -> u64 {
        let mut hasher = DefaultHasher::new();
        self.seed.hash(&mut hasher);
        input.hash(&mut hasher);
        hasher.finish()
    }
}

/// 生成确定性的 Agent 对话数据。
pub fn generate_agent_dialogue(turns: usize, seed: u64) -> Vec<TurnResult> {
    let mut simulator = AgentSimulator::new("bench_agent", seed);
    (0..turns).map(|_| simulator.simulate_turn()).collect()
}

/// 生成多会话数据。
pub fn generate_multi_session_data(
    session_count: usize,
    turns_per_session: usize,
) -> Vec<(String, Vec<TurnResult>)> {
    (0..session_count)
        .map(|s| {
            let seed = (s as u64) * 1000;
            let mut simulator = AgentSimulator::new(&format!("agent_{}", s), seed);
            let turns = (0..turns_per_session)
                .map(|_| simulator.simulate_turn())
                .collect();
            (format!("session_{}", s), turns)
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_simulator_deterministic() {
        let mut sim1 = AgentSimulator::new("test", 42);
        let mut sim2 = AgentSimulator::new("test", 42);

        let result1 = sim1.simulate_turn();
        let result2 = sim2.simulate_turn();

        assert_eq!(result1.turn_id, result2.turn_id);
        assert_eq!(result1.user_input, result2.user_input);
        assert_eq!(result1.topic_label, result2.topic_label);
    }

    #[test]
    fn test_session_simulation() {
        let mut sim = AgentSimulator::new("test", 42);
        let report = sim.simulate_session(10);

        assert_eq!(report.total_turns, 10);
        assert!(report.total_stored > 0);
        assert!(!report.topics_created.is_empty());
        assert_eq!(report.emotions_detected.len(), 10);
    }
}
