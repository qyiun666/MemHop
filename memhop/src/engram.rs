use std::collections::HashMap;
use half::f16;
use serde::{Deserialize, Serialize};
use crate::types::*;

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
}

#[allow(dead_code)]
impl KnowledgeNode {
    pub fn new(id: String, text: String, sparse: HashMap<String, f32>, vector: Vec<f16>, layer: Layer) -> Self {
        let now = chrono::Utc::now().timestamp_millis();
        KnowledgeNode {
            id,
            text,
            summary: None,
            vector,
            sparse,
            keywords: Vec::new(),
            source: NodeSource::Perception,
            layer,
            created_at: now,
            updated_at: now,
            version: 1,
            history: Vec::new(),
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
    pub dialogue_range: Option<(i64, i64)>,
    pub created_at: i64,
    pub updated_at: i64,
    pub version: u64,
    pub history: Vec<TopicSnapshot>,
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
        }
    }
}
