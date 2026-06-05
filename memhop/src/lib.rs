//! MemHop v0.17.0 — 5 层仿人脑记忆引擎。
//!
//! 5 层架构：L0 角色画像 + L1 纠缠超图 + L2 话题图 + L3 领域超图 + L4 原文库。
//! 双通道检索：BM25（始终可用）+ HNSW 语义向量 + 双编码器路由 (zh/en)。

// ============================================================
// 内部模块
// ============================================================

// ── 数据模型 ────────────────────────────────────────────────
mod types;
mod engram;

// ── 基础设施 ────────────────────────────────────────────────
mod error;
mod index;       // SparseIndex + BM25

// ── 编码器 ──────────────────────────────────────────────────
pub mod encoder; // NgramEncoder (default) + CandleEncoder (feature-gated)

// ── 存储层 ──────────────────────────────────────────────────
mod lmdb;        // 4 独立 LMDB 环境

// ── 4 层核心 ────────────────────────────────────────────────
mod hypergraph;   // L1 纠缠超图 + 超边链
mod topic_graph;  // L2 话题标准图
mod domain_graph; // L3 领域超图
mod raw_archive;  // L4 原文库
pub mod profile;  // L0 角色画像

// ── 业务逻辑 ────────────────────────────────────────────────
mod batch_store;  // 批量存储（唯一写入接口）
mod query_engine; // 按层检索引擎
mod brain;        // Brain 顶层 API

// ── v0.15.x 恢复模块 ─────────────────────────────────────────
pub mod session;   // 会话上下文管理（纯内存）
pub mod shelf;     // 知识库挂载（L3 领域图扩展）
pub mod organize;  // 记忆组织：话题反思、关键词精炼、边界检测
mod recall;       // 高级检索：关联扩散、话题过滤
mod dream;        // 记忆巩固管线（consolidate 实现）
mod splitter;     // 长文本分段

// ============================================================
// 公开 API
// ============================================================

pub use brain::Brain;
pub use session::SessionManager;
pub use types::{
    Layer, HyperedgeKind, HyperedgeSnapshot,
    NodeSource, TopicEdgeKind, TopicSnapshot,
    DocumentSnapshot,
    StoreBatch, StoreItem, BatchReport,
    BrainConfig,
    RecallRequest, RecallResponse, RecallResult,
    ConsolidateReport,
    ShelfDomain, ShelfMeta, DreamConfig,
    L0Profile, L0Snapshot, ActivatedTopicInfo, L3PathInfo, ActivationEntry,
};
pub use engram::{
    Hyperedge, KnowledgeNode, Topic, TopicEdge, RawDocument,
};
pub use error::{MemHopError, Result};
pub use encoder::{Encoder, EncoderOutput, NgramEncoder};
pub use index::{SparseIndex, HnswIndex};
