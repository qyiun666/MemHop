//! MemHop v0.14 — 超图驱动的关联记忆引擎。
//!
//! 4 层架构：L1 超图 + L2 话题图 + L3 领域超图 + L4 原文库。
//! 纯 ngram + BM25 检索，零外部 AI 模型依赖。

// ============================================================
// 内部模块
// ============================================================

// ── 数据模型 ────────────────────────────────────────────────
mod types;
mod engram;

// ── 基础设施 ────────────────────────────────────────────────
mod error;
mod index;       // SparseIndex + BM25
pub mod session;

// ── 编码器 ──────────────────────────────────────────────────
pub mod encoder; // NgramEncoder (only)

// ── 存储层 ──────────────────────────────────────────────────
mod lmdb;        // 4 独立 LMDB 环境

// ── 4 层核心 ────────────────────────────────────────────────
mod hypergraph;   // L1 超图 + 超边链
mod topic_graph;  // L2 话题标准图
mod domain_graph; // L3 领域超图
mod raw_archive;  // L4 原文库

// ── 业务逻辑 ────────────────────────────────────────────────
mod batch_store;  // 批量存储（唯一写入接口）
mod query_engine; // 按层检索引擎
mod brain;        // Brain 顶层 API

// ============================================================
// 公开 API
// ============================================================

pub use brain::Brain;
pub use types::{
    Layer, HyperedgeKind, HyperedgeSnapshot,
    NodeSource, TopicEdgeKind, TopicSnapshot,
    DocumentSnapshot,
    StoreBatch, StoreItem, BatchReport,
    BrainConfig,
    RecallRequest, RecallResponse, RecallResult,
    ConsolidateReport,
};
pub use engram::{
    Hyperedge, KnowledgeNode, Topic, TopicEdge, RawDocument,
};
pub use error::{MemHopError, Result};
pub use encoder::{Encoder, EncoderOutput, NgramEncoder};
