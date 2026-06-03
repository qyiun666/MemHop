use std::collections::HashMap;
use serde::{Deserialize, Serialize};

// ── Layer 枚举 ──────────────────────────────────────────────

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub enum Layer { L1, L2, L3, L4 }

// ── HyperedgeKind ──────────────────────────────────────────

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub enum HyperedgeKind {
    Association,
    Causality,
    Evolution,
    Contradiction,
    Merged,
    Partition,
}

// ── HyperedgeSnapshot ──────────────────────────────────────

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct HyperedgeSnapshot {
    pub version: u64,
    pub node_ids: Vec<String>,
    pub weight: f32,
    pub meta: HashMap<String, String>,
    pub snapshot_at: i64,
    pub reason: String,
}

// ── NodeSource ─────────────────────────────────────────────

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub enum NodeSource {
    Perception,
    DreamFusion,
    KnowledgeMount,
    Manual,
}

// ── TopicSnapshot ──────────────────────────────────────────

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TopicSnapshot {
    pub version: u64,
    pub summary: Option<String>,
    pub keywords: Vec<String>,
    pub node_ids: Vec<String>,
    pub snapshot_at: i64,
    pub reason: String,
}

// ── TopicEdgeKind ──────────────────────────────────────────

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub enum TopicEdgeKind {
    SubTopic,
    Related,
    Evolution,
}

// ── DocumentSnapshot ───────────────────────────────────────

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct DocumentSnapshot {
    pub version: u64,
    pub text: String,
    pub snapshot_at: i64,
    pub reason: String,
}

// ── StoreBatch / StoreItem ─────────────────────────────────

#[derive(Debug, Clone)]
pub struct StoreBatch {
    pub items: Vec<StoreItem>,
    pub agent_meta: HashMap<String, String>,
}

#[derive(Debug, Clone)]
pub struct StoreItem {
    pub text: String,
    pub turn_id: Option<String>,
    pub session_id: Option<String>,
    pub source: String,
    pub topic_label: Option<String>,
    pub llm_keywords: Option<Vec<String>>,
    pub llm_compressed_summary: Option<String>,
    pub valence: Option<f64>,
    pub arousal: Option<f64>,
    pub chain_parent_id: Option<String>,
    pub chain_label: Option<String>,
    pub domain_id: Option<String>,
}

// ── BatchReport ────────────────────────────────────────────

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct BatchReport {
    pub l1_nodes_created: u32,
    pub l1_hyperedges_created: u32,
    pub l2_topics_created: u32,
    pub l3_nodes_created: u32,
    pub l4_docs_stored: u32,
    pub chains_created: u32,
    pub total_duration_us: u64,
}

// ── BrainConfig ────────────────────────────────────────────

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct BrainConfig {
    pub brains_dir: String,
    pub agent_id: String,
}

impl Default for BrainConfig {
    fn default() -> Self {
        Self {
            brains_dir: "./memhop_brains".to_string(),
            agent_id: "default".to_string(),
        }
    }
}

// ── RecallRequest / RecallResponse ─────────────────────────

#[derive(Debug, Clone)]
pub struct RecallRequest {
    pub query: String,
    pub max_results: usize,
    pub target_layers: Vec<Layer>,
    pub time_range: Option<(i64, i64)>,
}

impl Default for RecallRequest {
    fn default() -> Self {
        Self {
            query: String::new(),
            max_results: 10,
            target_layers: vec![Layer::L1, Layer::L2, Layer::L4],
            time_range: None,
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RecallResult {
    pub layer: Layer,
    pub id: String,
    pub text: String,
    pub score: f32,
    pub topic_label: Option<String>,
    pub created_at: i64,
    pub version: u64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RecallResponse {
    pub results: Vec<RecallResult>,
    pub total_count: usize,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct ConsolidateReport {
    pub chains_consolidated: u32,
    pub topics_merged: u32,
    pub duration_ms: u64,
}
