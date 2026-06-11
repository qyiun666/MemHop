//! MemHop v0.25.1 — 6 层仿人脑记忆引擎。
//!
//! 6 层架构：L0 角色画像 + L1 纠缠超图 + L2 话题图 + L3 领域超图 + L4 原文库 + L5 程序性晶体。
//! 三重检索：BM25 稀疏 + HNSW 稠密 + E5 多语言语义。

// Clippy configuration for FFI safety
#![allow(clippy::not_unsafe_ptr_arg_deref)] // FFI functions use raw pointers by design

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
mod lmdb; // LMDB 存储类型定义 — 仅用于数据迁移工具 (storage/migrate.rs)
mod storage; // redb 单文件存储引擎 (迁移目标)

// ── 4 层核心 ────────────────────────────────────────────────
mod domain_graph; // L3 领域超图
mod hypergraph; // L1 纠缠超图 + 超边链
pub mod profile;
mod raw_archive; // L4 原文库
mod topic_graph; // L2 话题标准图 // L0 角色画像

// ── 业务逻辑 ────────────────────────────────────────────────
mod batch_store; // 外部输入写入接口（Dream 为内部维护写入）
mod brain;
mod query_engine; // 按层检索引擎 // Brain 顶层 API

// ── v0.23.0 新增模块 ─────────────────────────────────────────
pub mod activation; // 记忆激活管理器 (Active/Latent/Dormant)
pub mod reconsolidation; // v1.0: 记忆再巩固管理器

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

// ── SDK 入口 ──────────────────────────────────────────────────
pub mod sdk; // SDK 初始化 + 全局编码器共享

// ── FFI 接口 (C ABI) ──────────────────────────────────────────
pub mod ffi; // C ABI 动态库接口

// ============================================================
// 公开 API
// ============================================================

pub use activation::{ActivationConfig, ActivationManager};
pub use reconsolidation::ReconsolidationManager;
pub use brain::{Brain, PrewarmLayerResult};
pub use encoder::{Encoder, EncoderOutput, EncoderRouter, NgramEncoder, TripleEncoder, TripleEncoderOutput};
#[cfg(feature = "candle")]
pub use encoder::CandleEncoder;
pub use sdk::{MemHopConfig, MemHopInstance, MemHopSDK};
pub use engram::{Hyperedge, KnowledgeNode, RawDocument, Topic, TopicEdge};
pub use error::{MemHopError, Result};
pub use index::{HnswIndex, MemHopHnswConfig, RrfWeights, SparseIndex, SparseIndexV2};
pub use session::SessionManager;
pub use types::{
    ActivatedTopicInfo, ActivationEntry, BatchReport, BrainConfig, ConsolidateReport,
    CrystalStep, CrystalType, CrystallizeReport, CrystalSnapshot, DocumentSnapshot,
    DomainMeta, DreamConfig, HyperedgeKind, HyperedgeSnapshot, L0Profile, L0Snapshot,
    L3PathInfo, Layer, MemoryMeta, MemoryState, NodeSource, ProceduralCrystal, RecallRequest, RecallResponse,
    RecallResult, ShelfDomain, L3HyperedgeStrategy, ShelfMeta, StorageLayerInfo, StoreBatch, StoreItem, TopicEdgeKind,
    TopicSnapshot,
    // v0.24.0: Emotional system
    Emotion, EmotionalDimension, EmotionalFeedback, EmotionRecallRequest,
    // v0.24.0: L3 Crystallization
    CrystallizeL3Request, CrystallizeL3Report,
    // L3 骨架化
    SourceKind, SourceRef, NeighborResult, MountSourceInput, MountSourceItem,
};

pub use brain::l3_trait::{MemoryOrganHypergraph, NeighborInfo};
