use crate::types::*;
use half::f16;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;

// ── Hyperedge (超边) — L1/L3 核心 ──────────────────────────

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Hyperedge {
    pub id: String,
    pub node_ids: Vec<String>,
    pub kind: HyperedgeKind,
    pub weight: f32,
    pub created_at: i64,
    pub updated_at: i64,
    pub version: u64,
    pub history: Vec<HyperedgeSnapshot>,
    pub meta: HashMap<String, String>,
    pub chain_prev: Option<String>,
    pub chain_next: Option<String>,
    pub chain_label: Option<String>,
}

// ── KnowledgeNode (知识节点) — L1/L3 基础单元 ──────────────

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct KnowledgeNode {
    pub id: String,
    pub text: String,
    pub summary: Option<String>,
    pub vector: Vec<f16>,
    pub sparse: HashMap<String, f32>,
    pub keywords: Vec<String>,
    pub source: NodeSource,
    pub layer: Layer,
    pub created_at: i64,
    pub updated_at: i64,
    pub version: u64,
    pub history: Vec<HyperedgeSnapshot>,
    /// v0.16.0: 重要度评分 (0.0-1.0)，默认 0.5
    pub importance: f32,
    // ── v0.23.0 新增：记忆激活系统字段 ──
    /// 上次 recall 命中时间（毫秒）
    pub last_accessed_at: i64,
    /// 当前激活分数 [0.0, 1.0]
    pub activation_score: f32,
    /// 记忆状态：Active | Latent | Dormant
    pub memory_state: MemoryState,
    // ── v0.24.0 新增：情感维度字段 ──
    /// 情感类型
    #[serde(default)]
    pub emotion: Emotion,
    /// 情感强度 [0.0, 1.0]
    #[serde(default)]
    pub emotion_intensity: f32,
    /// 效价 [-1.0, 1.0]
    #[serde(default)]
    pub valence: f32,
    /// 唤醒度 [0.0, 1.0]
    #[serde(default)]
    pub arousal: f32,
    /// 文档长度（字符数），用于 BM25 长度归一化
    pub doc_len: usize,
}

impl KnowledgeNode {
    pub fn new(
        id: String,
        text: String,
        sparse: HashMap<String, f32>,
        vector: Vec<f16>,
        layer: Layer,
        source: NodeSource,
    ) -> Self {
        let now = chrono::Utc::now().timestamp_millis();
        let doc_len = text.len();
        KnowledgeNode {
            id,
            text,
            summary: None,
            vector,
            sparse,
            keywords: Vec::new(),
            source,
            layer,
            created_at: now,
            updated_at: now,
            version: 1,
            history: Vec::new(),
            importance: 0.5,
            // v0.23.0: 初始化记忆激活字段
            last_accessed_at: now,
            activation_score: 0.5, // 初始等于 importance
            memory_state: MemoryState::Active,
            // v0.24.0: 初始化情感字段
            emotion: Emotion::Neutral,
            emotion_intensity: 0.0,
            valence: 0.0,
            arousal: 0.0,
            doc_len,
        }
    }
}

// ── Topic (话题) — L2 核心 ──────────────────────────────────

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Topic {
    pub id: String,
    pub label: String,
    pub summary: Option<String>,
    pub keywords: Vec<String>,
    pub centroid: Vec<f16>,
    pub node_ids: Vec<String>,
    pub linked_domain_ids: Vec<String>,
    pub doc_ids: Vec<String>,
    pub dialogue_range: Option<(i64, i64)>,
    pub created_at: i64,
    pub updated_at: i64,
    pub version: u64,
    pub history: Vec<TopicSnapshot>,
    /// v0.17.0: LLM 扩展元数据，用于 plan_progress/next_steps 等
    pub extended_meta: HashMap<String, String>,
    /// v0.18.0: 领域关联强度 (domain_id → weight)
    #[serde(default)]
    pub domain_weights: HashMap<String, f32>,
    /// v0.18.0: 节点关联强度 (node_id → weight)
    #[serde(default)]
    pub node_weights: HashMap<String, f32>,
}

// ── TopicEdge (话题图边) — L2 ──────────────────────────────

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TopicEdge {
    pub source_id: String,
    pub target_id: String,
    pub kind: TopicEdgeKind,
    pub weight: f32,
    pub created_at: i64,
}

// ── RawDocument (原文) — L4 核心 ───────────────────────────

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RawDocument {
    pub id: String,
    pub text: String,
    pub turn_id: Option<String>,
    pub session_id: Option<String>,
    pub source: String,
    pub created_at: i64,
    pub version: u64,
    pub history: Vec<DocumentSnapshot>,
    /// v0.16.0: 编码向量，用于 dense 检索通道
    pub vector: Vec<f16>,
}

impl RawDocument {
    pub fn new(id: String, text: String, source: String, turn_id: Option<String>) -> Self {
        let now = chrono::Utc::now().timestamp_millis();
        RawDocument {
            id,
            text,
            turn_id,
            session_id: None,
            source,
            created_at: now,
            version: 1,
            history: Vec::new(),
            vector: Vec::new(),
        }
    }
}
