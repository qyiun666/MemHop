//! MemHop v0.22.0 — 6 层仿人脑记忆引擎。
//!
//! 6 层架构：L0 角色画像 + L1 纠缠超图 + L2 话题图 + L3 领域超图 + L4 原文库 + L5 程序性晶体。
//! 双通道检索：BM25（始终可用）+ HNSW 语义向量 + 双编码器路由 (zh/en)。

// ============================================================
// 内部模块
// ============================================================

// ── 数据模型 ────────────────────────────────────────────────
mod engram;
mod types;

// ── 基础设施 ────────────────────────────────────────────────
mod error;
mod index; // SparseIndex + BM25

// ── 编码器 ──────────────────────────────────────────────────
pub mod encoder; // NgramEncoder (default)

// ── 存储层 ──────────────────────────────────────────────────
mod lmdb; // 4 独立 LMDB 环境

// ── 4 层核心 ────────────────────────────────────────────────
mod domain_graph; // L3 领域超图
mod hypergraph; // L1 纠缠超图 + 超边链
pub mod profile;
mod raw_archive; // L4 原文库
mod topic_graph; // L2 话题标准图 // L0 角色画像

// ── 业务逻辑 ────────────────────────────────────────────────
mod batch_store; // 批量存储（唯一写入接口）
mod brain;
mod query_engine; // 按层检索引擎 // Brain 顶层 API

// ── v0.23.0 新增模块 ─────────────────────────────────────────
pub mod activation; // 记忆激活管理器 (Active/Latent/Dormant)

// ── v0.15.x 恢复模块 ─────────────────────────────────────────
mod dream; // 记忆巩固管线（consolidate 实现）
pub mod organize; // 记忆组织：话题反思、关键词精炼、边界检测
pub mod procedural; // v0.18.3: 程序性结晶 — 链分析引擎
mod recall;
pub mod session; // 会话上下文管理（纯内存）
pub mod shelf; // 知识库挂载（L3 领域图扩展）
mod splitter; // 长文本分段

// ── 基准测试支撑 ──────────────────────────────────────────────
#[cfg(any(test, feature = "bench"))]
pub mod bench_support; // 基准测试工具：MCP 客户端、内存监控、IR 指标

// ============================================================
// 公开 API
// ============================================================

pub use activation::{ActivationConfig, ActivationManager};
pub use brain::{Brain, PrewarmLayerResult};
pub use encoder::{Encoder, EncoderOutput, NgramEncoder};
pub use engram::{Hyperedge, KnowledgeNode, RawDocument, Topic, TopicEdge};
pub use error::{MemHopError, Result};
pub use index::{HnswIndex, MemHopHnswConfig, SparseIndex, SparseIndexV2};
pub use session::SessionManager;
pub use lmdb::L5Env;
pub use types::{
    ActivatedTopicInfo, ActivationEntry, BatchReport, BrainConfig, ConsolidateReport,
    CrystalStep, CrystalType, CrystallizeReport, CrystalSnapshot, DocumentSnapshot,
    DreamConfig, HyperedgeKind, HyperedgeSnapshot, L0Profile, L0Snapshot,
    L3PathInfo, Layer, MemoryState, NodeSource, ProceduralCrystal, RecallRequest, RecallResponse,
    RecallResult, ShelfDomain, ShelfMeta, StorageLayerInfo, StoreBatch, StoreItem, TopicEdgeKind,
    TopicSnapshot,
};
