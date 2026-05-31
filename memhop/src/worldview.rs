//! WorldviewPattern — 三观模式 (v0.12.1)
//!
//! 从纠缠事件中涌现的高阶认知模式，反映 Agent 的思维风格、价值取向等。

use serde::{Deserialize, Serialize};

use crate::brain::Brain;
use crate::error::MemHopError;
use crate::error::Result;

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

// ── 公开查询方法 ─────────────────────────────────────────

/// v0.12.1: 获取所有三观模式
pub fn get_all_worldviews(brain: &Brain) -> Result<Vec<WorldviewPattern>> {
    let rtxn = brain
        .storage
        .begin_read()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    let worldviews = brain
        .storage
        .get_all_worldviews(&rtxn)
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    Ok(worldviews)
}

/// v0.12.1: 获取单个三观模式
pub fn get_worldview(brain: &Brain, wv_id: &str) -> Result<Option<WorldviewPattern>> {
    let rtxn = brain
        .storage
        .begin_read()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    brain
        .storage
        .get_worldview(&rtxn, wv_id)
        .map_err(|e| MemHopError::Storage(e.to_string()))
}

// ── 内部方法 ─────────────────────────────────────────────

/// v0.12.1: 三观模式介入 — 提取稳定度 > 0.7 的模式上下文和认知冲突。
pub(crate) fn extract_worldview_context(
    brain: &Brain,
    query: &str,
) -> (Vec<String>, Vec<String>) {
    let rtxn = match brain.storage.begin_read() {
        Ok(t) => t,
        Err(_) => return (Vec::new(), Vec::new()),
    };
    let worldviews = match brain.storage.get_all_worldviews(&rtxn) {
        Ok(w) => w,
        Err(_) => return (Vec::new(), Vec::new()),
    };
    drop(rtxn);

    let mut worldview_context = Vec::new();
    let mut cognitive_conflicts = Vec::new();

    for wv in &worldviews {
        if wv.stability > 0.7 {
            worldview_context.push(wv.pattern.clone());
        }
        if wv.stability > 0.5 {
            let query_lower = query.to_lowercase();
            if query_lower.contains("不应该")
                || query_lower.contains("不对")
                || query_lower.contains("相反")
            {
                cognitive_conflicts.push(format!(
                    "当前输入与模式 '{}' 可能冲突",
                    wv.pattern
                ));
            }
        }
    }

    (worldview_context, cognitive_conflicts)
}
