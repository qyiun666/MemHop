//! Knowledge Tree — a domain area in the mind.
//!
//! v0.12.1: Replaces the flat `tree_path: Option<String>` field on Engram
//! with a proper entity that carries statistics, shelf associations, and
//! lifecycle metadata.
//!
//! v0.12.2: Tree CRUD methods extracted from brain.rs.
//! v0.13.0: auto_created, centroid, find_similar_tree.

use crate::brain::{now_millis, Brain};
use crate::error::{MemHopError, Result};
use half::f16;
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
    /// v0.13.0: Whether this tree was auto-created from conversation context.
    #[serde(default)]
    pub auto_created: bool,
    /// v0.13.0: Centroid vector (f16) of the tree's engrams for similarity matching.
    #[serde(default)]
    pub centroid: Option<Vec<f16>>,
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

// ── Tree CRUD (v0.12.2: extracted from brain.rs) ─────────────

/// v0.12.1: 创建知识树
pub(crate) fn create_tree(
    brain: &mut Brain,
    name: &str,
    domain: &str,
    auto_created: bool,
) -> Result<Tree> {
    let now = now_millis();
    let id = format!("tree_{}", now);
    let tree = Tree {
        id: id.clone(),
        name: name.to_string(),
        domain: domain.to_string(),
        description: None,
        memory_count: 0,
        last_active_at: now,
        shelf_paths: vec![],
        created_at: now,
        auto_created,
        centroid: None,
    };
    let mut wtxn = brain
        .storage
        .begin_write()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    brain
        .storage
        .put_tree(&mut wtxn, &tree)
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    wtxn
        .commit()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    Ok(tree)
}

/// v0.12.1: 列出所有知识树
pub(crate) fn list_trees(brain: &Brain) -> Result<Vec<Tree>> {
    let rtxn = brain
        .storage
        .begin_read()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    let trees = brain
        .storage
        .get_all_trees(&rtxn)
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    Ok(trees)
}

/// v0.12.1: 获取单个知识树
pub(crate) fn get_tree(brain: &Brain, tree_id: &str) -> Result<Option<Tree>> {
    let rtxn = brain
        .storage
        .begin_read()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    brain
        .storage
        .get_tree(&rtxn, tree_id)
        .map_err(|e| MemHopError::Storage(e.to_string()))
}

/// v0.12.1: 删除知识树（不解绑 engram）
pub(crate) fn delete_tree(brain: &mut Brain, tree_id: &str) -> Result<()> {
    let mut wtxn = brain
        .storage
        .begin_write()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    brain
        .storage
        .delete_tree(&mut wtxn, tree_id)
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    wtxn
        .commit()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    Ok(())
}

/// v0.12.1: 将 engram 移动到指定树
pub(crate) fn move_to_tree(brain: &mut Brain, engram_id: &str, tree_id: &str) -> Result<()> {
    // 1. Read engram from hippocampus
    let rtxn = brain
        .storage
        .begin_read()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    let mut engram = brain
        .storage
        .get_hippocampus(&rtxn, engram_id)
        .map_err(|e| MemHopError::Storage(e.to_string()))?
        .ok_or_else(|| MemHopError::NotFound(format!("engram '{}' not found", engram_id)))?;
    drop(rtxn);

    // 2. Read Tree to get name and domain
    let tree = get_tree(brain, tree_id)?
        .ok_or_else(|| MemHopError::NotFound(format!("tree '{}' not found", tree_id)))?;

    // 3. Update tree_ref and deprecated tree_path
    engram.tree_ref = Some(TreeRef {
        tree_id: tree.id.clone(),
        tree_name: tree.name.clone(),
        tree_domain: tree.domain.clone(),
    });
    engram.tree_path = Some(tree.name.clone());

    // 4. Write back
    let mut wtxn = brain
        .storage
        .begin_write()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    brain
        .storage
        .put_hippocampus(&mut wtxn, engram_id, &engram)
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    wtxn
        .commit()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;

    Ok(())
}

/// v0.13.0: Find a tree whose centroid is similar to the given query vector.
/// Returns the tree_id if cosine similarity > threshold.
pub fn find_similar_tree(brain: &Brain, query: &[f16], threshold: f32) -> Option<String> {
    let trees = list_trees(brain).ok()?;
    let mut best_id: Option<String> = None;
    let mut best_sim = threshold;
    for tree in &trees {
        if let Some(ref centroid) = tree.centroid {
            let sim = cosine_similarity_f16(query, centroid);
            if sim > best_sim {
                best_sim = sim;
                best_id = Some(tree.id.clone());
            }
        }
    }
    best_id
}

/// Compute cosine similarity between two f16 vectors.
fn cosine_similarity_f16(a: &[f16], b: &[f16]) -> f32 {
    let len = a.len().min(b.len());
    if len == 0 {
        return 0.0;
    }
    let mut dot = 0.0f32;
    let mut norm_a = 0.0f32;
    let mut norm_b = 0.0f32;
    for i in 0..len {
        let av = a[i].to_f32();
        let bv = b[i].to_f32();
        dot += av * bv;
        norm_a += av * av;
        norm_b += bv * bv;
    }
    let denom = norm_a.sqrt() * norm_b.sqrt();
    if denom < 1e-10 {
        0.0
    } else {
        dot / denom
    }
}
