//! WorldviewPattern — 三观模式 (v0.12.1)
//!
//! 从纠缠事件中涌现的高阶认知模式，反映 Agent 的思维风格、价值取向等。

use serde::{Deserialize, Serialize};

/// v0.12.1: 三观模式
///
/// 由 Dream REM 阶段从纠缠事件中涌现，随使用而加强/稳定。
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WorldviewPattern {
    /// 唯一标识
    #[serde(default)]
    pub id: String,
    /// 来源的纠缠事件 ID 列表
    #[serde(default)]
    pub source_events: Vec<String>,
    /// 模式描述文本
    #[serde(default)]
    pub pattern: String,
    /// 模式分类
    #[serde(default)]
    pub category: PatternCategory,
    /// 出现次数（累积）
    #[serde(default)]
    pub occurrence_count: u64,
    /// 稳定度 0-1
    #[serde(default)]
    pub stability: f32,
    /// 首次涌现时间（Unix ms）
    #[serde(default)]
    pub emerged_at: i64,
    /// 最后强化时间（Unix ms）
    #[serde(default)]
    pub last_reinforced_at: i64,
}

/// v0.12.1: 模式分类
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Serialize, Deserialize)]
pub enum PatternCategory {
    /// 思维风格（Default）
    #[default]
    ThinkingStyle,
    /// 价值优先
    ValuePriority,
    /// 决策倾向
    DecisionBias,
    /// 审美偏好
    AestheticPreference,
    /// 情绪模式
    EmotionalPattern,
}
