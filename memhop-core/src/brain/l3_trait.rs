//! MemoryOrganHypergraph trait — MeowAgent 等外部调用方的适配接口。
//!
//! 该 trait 为同步 trait（当前项目无 async 运行时依赖），
//! 外部调用方通过 `tokio::spawn_blocking` 或等效方式包装。

use crate::error::Result;
use crate::types::{HyperedgeKind, RecallResult, SourceRef};

/// 外部超图操作结果节点信息（轻量视图）
#[derive(Debug, Clone)]
pub struct NeighborInfo {
    pub node_id: String,
    pub text_snippet: String,
    pub weight: f32,
    pub is_structural: bool,
}

/// L3 记忆超图操作接口。
///
/// 供 MeowAgent 等外部调用方实现适配器，
/// 将 MemHop 的 L3 操作封装为统一的契约调用。
pub trait MemoryOrganHypergraph: Send + Sync {
    /// 在指定领域添加知识节点。
    fn add_knowledge(
        &self,
        domain_id: &str,
        text: &str,
        is_structural: bool,
        source_ref: Option<&SourceRef>,
    ) -> Result<String>;

    /// 在指定领域添加关联（超边）。
    fn add_relation(
        &self,
        domain_id: &str,
        source_node: &str,
        target_node: &str,
        kind: HyperedgeKind,
    ) -> Result<String>;

    /// 查询节点的超图邻居。
    fn query_neighbors(
        &self,
        node_id: &str,
        depth: usize,
    ) -> Result<Vec<NeighborInfo>>;

    /// 搜索知识。
    fn search_knowledge(
        &self,
        query: &str,
        domain_id: Option<&str>,
        max: usize,
    ) -> Result<Vec<RecallResult>>;

    /// 获取来源上下文（原文片段）。
    fn get_source_context(
        &self,
        source_ref: &SourceRef,
    ) -> Result<String>;
}
