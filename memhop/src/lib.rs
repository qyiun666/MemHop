//! MemHop — 人脑启发的记忆系统（纯 Rust）。
//!
//! v0.7.3 新增 Brain 三层架构 API，与原有 MemHop 引擎共存。

// ============================================================
// 内部模块（按依赖顺序声明）
// ============================================================

// ── 数据模型 ────────────────────────────────────────────────
mod types;       // 公开 API 类型 + 向后兼容类型
mod engram;      // 新版 Brain 核心数据结构（Engram, Association 等）

// ── 基础设施 ────────────────────────────────────────────────
mod error;
mod storage;     // LMDB 存储层（6 数据库）
mod hopfield;    // Modern Hopfield 网络
mod index;       // 稀疏索引
mod meta_index;  // 元数据索引
mod filter;      // 搜索过滤条件解析
pub mod hnsw;    // HNSW 近似近邻索引 (v0.9.0)
pub mod shelf;   // Knowledge shelf mount/query/unmount (v0.9.0)

// ── 编码器 ──────────────────────────────────────────────────
pub mod encoder;     // NgramEncoder + Hybrid + ONNX

// ── 新版 Brain 组件 ─────────────────────────────────────────
mod activation;      // 竞争扩散激活
mod cortex;          // L0 工作记忆 ring buffer
mod hippocampus;     // L1 海马体暂存
mod unified_graph;   // 统一图（邻接表 + LMDB）
mod personality;     // Personality + GrowthState
mod vitality;        // 生命力衰减 + 再巩固
mod schema;          // Schema 涌现 + 稳定度
pub(crate) mod llm_provider;    // LLM Provider trait + PromptTemplates
mod scene_gating;    // Anchor 倒排索引 + 场景门控
pub mod plan_gate;   // Plan 边界检测 + PlanIndex (v0.8.0)
pub(crate) mod tone_extractor;  // Rule-based tone extraction

// ── 旧版引擎（向后兼容） ────────────────────────────────────
mod engine;      // MemHop 旧版引擎

// ── 顶层 Brain API ──────────────────────────────────────────
mod brain;       // 新版 Brain 三层架构 API

// ============================================================
// 公开 API
// ============================================================

// ── 新版 Brain API ─────────────────────────────────────────
pub use brain::Brain;
pub use types::{
    BrainConfig, ChunkMeta, ConflictItem, DreamReport, InnateSchema, PerceptionInput,
    PerceptionOutput, RecallMode, RecallRequest, RecallResponse, RecallTrace, ReflectionInput,
    ReflectionKind, ShelfDomain, ShelfResult,
};
pub use shelf::ShelfManager;

// ── 新版数据类型 ────────────────────────────────────────────
pub use engram::{
    Association, AssociationKind, EmotionalContext, EmotionalState, Engram, EngramKind,
    Protection, SchemaExtra, VECTOR_DIM,
    PlanHint, PlanLevel, PlanState, PlanInfo, PlanNode,
    DialogueTurn, ToneMeta, StyleCompact,
    ToneAggregate, TopicDistribution, DomainStats,
};


// ── 编码器 — 公开类型 ──────────────────────────────────────
pub use encoder::{Encoder, EncoderOutput, NgramEncoder, HybridEncoder};
#[cfg(feature = "onnx")]
pub use encoder::OnnxEncoder;
#[cfg(feature = "api-encoder")]
pub use encoder::ApiEncoder;
// ── 旧版引擎 API（向后兼容） ────────────────────────────────
pub use engine::MemHop;
pub use error::{MemHopError, Result};
pub use types::{DreamConfig, Memory, StoreOptions};

// ── 新版 Personality ───────────────────────────────────────
pub use personality::{GrowthState, Personality};

// ── LLM Provider ────────────────────────────────────────────
pub use llm_provider::LlmProvider;
