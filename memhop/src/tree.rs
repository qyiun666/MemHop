//! Knowledge Tree — a domain area in the mind.
//!
//! v0.12.1: Replaces the flat `tree_path: Option<String>` field on Engram
//! with a proper entity that carries statistics, shelf associations, and
//! lifecycle metadata.

use serde::{Deserialize, Serialize};

/// 知识树 — 人脑中的一个领域（工作、旅游、孩子...）。
///
/// Each Tree represents a coherent knowledge domain. Engrams are associated
/// with a Tree via `TreeRef` (embedded in the Engram struct). Trees provide:
/// - Memory count tracking per domain
/// - Last-active timestamp for recency-based matching
/// - Shelf path associations (sources of knowledge)
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Tree {
    /// 唯一标识，格式 "tree_<timestamp>"
    pub id: String,
    /// 显示名称，例如 "工作"、"旅游"
    pub name: String,
    /// 领域标签，用于自动匹配，例如 "work"、"travel"、"parenting"
    pub domain: String,
    /// 可选的描述
    #[serde(default)]
    pub description: Option<String>,
    /// 该树下的 engram 总数
    #[serde(default)]
    pub memory_count: u64,
    /// 最后一次活跃时间 (Unix ms)
    #[serde(default)]
    pub last_active_at: i64,
    /// 关联的书架目录路径
    #[serde(default)]
    pub shelf_paths: Vec<String>,
    /// 创建时间 (Unix ms)
    pub created_at: i64,
}

/// 知识树引用（嵌入在 Engram 中，替代 `tree_path` 字段）。
///
/// Denormalised to avoid joins: frequently accessed together with Engram.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TreeRef {
    /// → Tree.id
    pub tree_id: String,
    /// 反范式化，避免 join
    pub tree_name: String,
    /// 树所属领域
    pub tree_domain: String,
}
