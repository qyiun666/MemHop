use std::collections::HashMap;
use serde::{Deserialize, Serialize};

// ── Layer 枚举 ──────────────────────────────────────────────

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub enum Layer { L0, L1, L2, L3, L4 }

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
    /// v0.16.0: 重要度评分 (0.0-1.0)，默认 0.5。影响检索排序权重。
    pub importance: Option<f32>,
}

impl Default for StoreItem {
    fn default() -> Self {
        Self {
            text: String::new(),
            turn_id: None,
            session_id: None,
            source: "chat".to_string(),
            topic_label: None,
            llm_keywords: None,
            llm_compressed_summary: None,
            valence: None,
            arousal: None,
            chain_parent_id: None,
            chain_label: None,
            domain_id: None,
            importance: None,
        }
    }
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
    /// v0.16.0: 去重跳过的 L1 节点数
    pub l1_dedup_skipped: u32,
    /// v0.17.1: 输入序号 → L1 节点 ID 映射，用于 benchmark 和 ID 回溯
    #[serde(default, skip_serializing_if = "HashMap::is_empty")]
    pub engram_ids: HashMap<String, String>,
    /// v0.17.3: 输入序号 → L3 节点 ID 映射，用于 benchmark 和 ID 回溯
    #[serde(default, skip_serializing_if = "HashMap::is_empty")]
    pub l3_engram_ids: HashMap<String, String>,
}

// ── BrainConfig ────────────────────────────────────────────

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct BrainConfig {
    pub brains_dir: String,
    pub agent_id: String,
    /// 编码器模型目录路径。Some(path) → CandleEncoder, None → NgramEncoder
    pub model_path: Option<String>,
}

impl Default for BrainConfig {
    fn default() -> Self {
        Self {
            brains_dir: "./memhop_brains".to_string(),
            agent_id: "default".to_string(),
            model_path: None,
        }
    }
}

// ── Session ──────────────────────────────────────────────

// ── Activation Entry ────────────────────────────────────

#[derive(Debug, Clone)]
pub struct ActivationEntry {
    pub topic_id: String,
    pub activated_at: i64,
    pub ttl_ms: i64,
    pub last_hit_at: i64,
}

// ── Session ────────────────────────────────────────────

#[derive(Debug, Clone, Default)]
pub struct SessionState {
    pub session_id: String,
    pub active_topics: HashMap<String, ActivationEntry>,
    pub turn_count: u32,
    pub started_at: i64,
    pub last_active_at: i64,
}

// ── Shelf ────────────────────────────────────────────────

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub enum ShelfDomain { Code, Doc, Book, Paper, Generic }

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ShelfMeta {
    pub id: String,
    pub path: String,
    pub doc_type: ShelfDomain,
    pub chunk_count: usize,
    pub mounted_at: i64,
    /// v0.17.3: 输入序号 → L1 节点 ID 映射，用于 benchmark 和 ID 回溯
    #[serde(default, skip_serializing_if = "HashMap::is_empty")]
    pub engram_ids: HashMap<String, String>,
    /// v0.17.3: 输入序号 → L3 节点 ID 映射，用于 benchmark 和 ID 回溯
    #[serde(default, skip_serializing_if = "HashMap::is_empty")]
    pub l3_engram_ids: HashMap<String, String>,
}

// ── Dream ────────────────────────────────────────────────

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct DreamConfig {
    pub vitality_half_life_hours: f32,
    pub schema_min_topics: usize,
}

impl Default for DreamConfig {
    fn default() -> Self {
        Self { vitality_half_life_hours: 168.0, schema_min_topics: 5 }
    }
}

// ── RecallRequest / RecallResponse ─────────────────────────

#[derive(Debug, Clone)]
pub struct RecallRequest {
    pub query: String,
    pub max_results: usize,
    pub target_layers: Vec<Layer>,
    pub time_range: Option<(i64, i64)>,
    pub spread_depth: Option<usize>,
    pub topic_filter: Option<String>,
    pub exclude_ids: Vec<String>,
    pub exclude_topic_ids: Vec<String>,
    pub l3_domain_id: Option<String>,
    pub l2_topic_id: Option<String>,
    /// v0.15.1: 会话 ID，用于激活队列优先检索。
    pub session_id: Option<String>,
    /// v0.16.0: 时间衰减系数。score *= exp(-λ * hours_since_creation)。
    /// None = 不衰减。Some(0.001) ≈ 42 天半衰期。
    pub time_decay_lambda: Option<f32>,
}

impl Default for RecallRequest {
    fn default() -> Self {
        Self {
            query: String::new(),
            max_results: 10,
            target_layers: vec![Layer::L1, Layer::L2, Layer::L4],
            time_range: None,
            spread_depth: None,
            topic_filter: None,
            exclude_ids: Vec::new(),
            exclude_topic_ids: Vec::new(),
            l3_domain_id: None,
            l2_topic_id: None,
            session_id: None,
            time_decay_lambda: None,
        }
    }
}

// ── L0 Profile ────────────────────────────────────────────

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct L0Profile {
    pub role_name: Option<String>,
    pub personality: Vec<String>,
    pub values: Vec<String>,
    pub worldview: Vec<String>,
    pub role: Option<String>,
    pub position: Option<String>,
    pub traits: HashMap<String, String>,
    pub updated_at: i64,
    pub version: u64,
    pub history: Vec<L0Snapshot>,
}

impl Default for L0Profile {
    fn default() -> Self {
        Self {
            role_name: None, personality: Vec::new(), values: Vec::new(),
            worldview: Vec::new(), role: None, position: None,
            traits: HashMap::new(), updated_at: 0, version: 1, history: Vec::new(),
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct L0Snapshot {
    pub version: u64,
    pub personality: Vec<String>,
    pub values: Vec<String>,
    pub worldview: Vec<String>,
    pub snapshot_at: i64,
    pub reason: String,
}

// ── Activation ────────────────────────────────────────────

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ActivatedTopicInfo {
    pub topic_id: String,
    pub activated_at: i64,
    pub ttl_ms: i64,
    pub last_hit_at: i64,
}

// ── L3 Path Info ──────────────────────────────────────────

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct L3PathInfo {
    pub domain_id: String,
    pub name: String,
    pub node_count: u64,
    pub mounted_at: i64,
}

// ── RecallRequest / RecallResponse ─────────────────────────

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
    pub l0_profile: Option<L0Profile>,
    pub confidence: Option<f32>,
    pub activated_topics: Vec<ActivatedTopicInfo>,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct ConsolidateReport {
    pub chains_consolidated: u32,
    pub topics_merged: u32,
    pub topics_reflected: u32,
    pub duration_ms: u64,
    pub vitality_decayed: u32,
    pub schemas_emerged: u32,
    pub l0_updated: bool,
    pub plans_consolidated: u32,
}
