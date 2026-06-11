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
    /// 文档长度（字符数），用于 BM25 长度归一化
    pub doc_len: usize,
    /// v0.26.0: 聚合记忆元数据
    pub memory: MemoryMeta,
    /// v1.0: E5 模型稠密向量（第三检索通道）
    #[serde(default)]
    pub vector_e5: Vec<f16>,
    /// L3 骨架化: 是否为结构节点（函数签名/标题/段落首句等）
    #[serde(default)]
    pub is_structural: bool,
    /// L3 骨架化: 来源引用
    #[serde(default)]
    pub source_ref: Option<crate::types::SourceRef>,
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
            doc_len,
            memory: MemoryMeta {
                importance: 0.5,
                last_accessed_at: now,
                activation_score: 0.5,
                memory_state: MemoryState::Active,
                emotion: Emotion::Neutral,
                emotion_intensity: 0.0,
                valence: 0.0,
                arousal: 0.0,
                personal_decay_lambda: 0.01,
                reconsolidation_count: 0,
                labile_until: None,
            },
            vector_e5: Vec::new(),
            is_structural: false,
            source_ref: None,
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
