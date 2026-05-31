//! EntanglementEvent — 跨树纠缠事件（v0.12.1）
//!
//! 当不同知识树中的记忆在 recall / plan 压缩 / dream 中
//! 被同时激活时，记录为纠缠事件，用于后续展开和衰减。

use serde::{Deserialize, Serialize};

/// 纠缠事件 — 记录来自不同知识树的记忆节点之间的关联。
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct EntanglementEvent {
    #[serde(default)]
    pub id: String,
    /// 参与的记忆节点 engram IDs
    #[serde(default)]
    pub nodes: Vec<String>,
    /// 跨了哪些知识树
    #[serde(default)]
    pub tree_ids: Vec<String>,
    /// 纠缠描述
    #[serde(default)]
    pub context: String,
    #[serde(default)]
    pub trigger: EntanglementTrigger,
    /// 纠缠强度 0–1
    #[serde(default)]
    pub strength: f32,
    /// 关联的计划 IDs
    #[serde(default)]
    pub plan_ids: Vec<String>,
    /// 创建时间（Unix ms）
    #[serde(default)]
    pub created_at: i64,
    /// 最后命中时间（Unix ms）
    #[serde(default)]
    pub last_hit_at: i64,
    /// 命中次数
    #[serde(default)]
    pub hit_count: u32,
}

/// 纠缠事件触发原因。
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub enum EntanglementTrigger {
    /// 召回时跨树命中
    RecallCrossTree,
    /// Plan 压缩时发现
    PlanCompression,
    /// Dream REM 阶段涌现
    DreamEmergence,
    /// 手动创建
    #[default]
    Manual,
}
