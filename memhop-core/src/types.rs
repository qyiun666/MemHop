use serde::{Deserialize, Serialize};
use std::collections::HashMap;

use crate::error::{MemHopError, Result};

// ── Layer 枚举 ──────────────────────────────────────────────

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub enum Layer {
    L0,
    L1,
    L2,
    L3,
    L4,
    L5,
}

// ── MemoryState 枚举 (v0.23.0) ──────────────────────────────

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, Default)]
pub enum MemoryState {
    #[default]
    Active,
    Latent,
    Dormant,
}

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

// ── Emotion (v0.24.0) ──────────────────────────────────────

/// Ekman 6 类基础情感 + Neutral
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize, Default)]
pub enum Emotion {
    Joy,
    Sadness,
    Anger,
    Fear,
    Surprise,
    Disgust,
    #[default]
    Neutral,
}

/// 情感维度（完整情感标签）
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct EmotionalDimension {
    pub emotion: Emotion,
    pub intensity: f32,
    pub valence: f32,
    pub arousal: f32,
}

impl Default for EmotionalDimension {
    fn default() -> Self {
        EmotionalDimension {
            emotion: Emotion::Neutral,
            intensity: 0.0,
            valence: 0.0,
            arousal: 0.0,
        }
    }
}

impl EmotionalDimension {
    /// 验证情感维度字段均在合法范围内且非 NaN/Inf。
    pub fn validate(&self) -> Result<()> {
        if !self.intensity.is_finite() || !(0.0..=1.0).contains(&self.intensity) {
            return Err(MemHopError::InvalidArgument(
                "EmotionalDimension.intensity must be finite and in [0.0, 1.0]".into(),
            ));
        }
        if !self.valence.is_finite() || !(-1.0..=1.0).contains(&self.valence) {
            return Err(MemHopError::InvalidArgument(
                "EmotionalDimension.valence must be finite and in [-1.0, 1.0]".into(),
            ));
        }
        if !self.arousal.is_finite() || !(0.0..=1.0).contains(&self.arousal) {
            return Err(MemHopError::InvalidArgument(
                "EmotionalDimension.arousal must be finite and in [0.0, 1.0]".into(),
            ));
        }
        Ok(())
    }
}

/// 情感反馈请求
#[derive(Debug, Clone)]
pub struct EmotionalFeedback {
    pub memory_id: String,
    pub emotion: Emotion,
    pub intensity: f32,
    pub reason: Option<String>,
}

impl EmotionalFeedback {
    /// 验证 intensity 在 [0.0, 1.0] 且非 NaN/Inf，memory_id 非空。
    pub fn validate(&self) -> Result<()> {
        if !self.intensity.is_finite() || !(0.0..=1.0).contains(&self.intensity) {
            return Err(MemHopError::InvalidArgument(
                "EmotionalFeedback.intensity must be finite and in [0.0, 1.0]".into(),
            ));
        }
        if self.memory_id.is_empty() {
            return Err(MemHopError::InvalidArgument(
                "EmotionalFeedback.memory_id must not be empty".into(),
            ));
        }
        Ok(())
    }
}

/// 按情感检索请求
#[derive(Debug, Clone)]
pub struct EmotionRecallRequest {
    pub emotion: Option<Emotion>,
    pub min_intensity: f32,
    pub time_decay_lambda: Option<f32>,
    pub max_results: usize,
}

impl Default for EmotionRecallRequest {
    fn default() -> Self {
        EmotionRecallRequest {
            emotion: None,
            min_intensity: 0.0,
            time_decay_lambda: None,
            max_results: 10,
        }
    }
}

impl EmotionRecallRequest {
    /// 验证 max_results ≤ 1000，min_intensity 在 [0.0, 1.0] 且非 NaN/Inf，
    /// time_decay_lambda 若 Some 则 ≥ 0.0 且非 NaN/Inf。
    pub fn validate(&self) -> Result<()> {
        if self.max_results > 1000 {
            return Err(MemHopError::InvalidArgument(
                "EmotionRecallRequest.max_results must be ≤ 1000".into(),
            ));
        }
        if !self.min_intensity.is_finite() || !(0.0..=1.0).contains(&self.min_intensity) {
            return Err(MemHopError::InvalidArgument(
                "EmotionRecallRequest.min_intensity must be finite and in [0.0, 1.0]".into(),
            ));
        }
        if let Some(v) = self.time_decay_lambda
            && (!v.is_finite() || v < 0.0)
        {
            return Err(MemHopError::InvalidArgument(
                "EmotionRecallRequest.time_decay_lambda must be finite and ≥ 0.0"
                    .into(),
            ));
        }
        Ok(())
    }
}

// ── MemoryMeta ─────────────────────────────────────────────

/// 记忆元数据 — 聚合重要性/激活/情感/衰减等维度
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct MemoryMeta {
    pub importance: f32,
    pub last_accessed_at: i64,
    pub activation_score: f32,
    pub memory_state: MemoryState,
    pub emotion: Emotion,
    pub emotion_intensity: f32,
    pub valence: f32,
    pub arousal: f32,
    // v1.0: 新增字段
    pub personal_decay_lambda: f32,
    pub reconsolidation_count: u32,
    pub labile_until: Option<i64>,
}

/// 记忆再巩固条目
#[derive(Debug, Clone)]
pub struct ReconsolidationEntry {
    pub node_id: String,
    pub original_text_hash: u64,
    pub labile_until: i64,
    pub recall_count: u32,
    pub reconsolidation_count: u32,
}

// ── PrefetchHint (v1.0) ────────────────────────────────────

/// 预测性记忆提示
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PrefetchHint {
    pub node_id: String,
    pub text: String,
    pub prediction_score: f32,
    pub reason: PrefetchReason,
}

/// 预测原因
#[derive(Debug, Clone, Serialize, Deserialize)]
pub enum PrefetchReason {
    HyperedgeSpread,
    SameTopicHighFreq,
    EmotionalResonance,
    CrossDomainBridge,
    TemporalProximity,
}

// ── CrossDomainLink (v1.0) ──────────────────────────────────

/// L3 域间关联 — 在 Dream 时从共现 topic 自动推断
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CrossDomainLink {
    pub domain_a: String,
    pub domain_b: String,
    pub bridge_topic_ids: Vec<String>,
    pub strength: f32,
    pub created_at: i64,
}

// ── L3 Crystallization (v0.24.0) ─────────────────────────────

/// L3 结晶化请求：从 L2 话题提炼高层知识写入 L3
#[derive(Debug, Clone)]
pub struct CrystallizeL3Request {
    pub topic_id: String,
    pub summary: String,
    pub keywords: Vec<String>,
    pub domain_name: Option<String>,
}

impl CrystallizeL3Request {
    /// 验证 topic_id 和 summary 非空，keywords 非空且 ≤ 100 个。
    pub fn validate(&self) -> Result<()> {
        if self.topic_id.is_empty() {
            return Err(MemHopError::InvalidArgument(
                "CrystallizeL3Request.topic_id must not be empty".into(),
            ));
        }
        if self.summary.is_empty() {
            return Err(MemHopError::InvalidArgument(
                "CrystallizeL3Request.summary must not be empty".into(),
            ));
        }
        if self.keywords.is_empty() {
            return Err(MemHopError::InvalidArgument(
                "CrystallizeL3Request.keywords must not be empty".into(),
            ));
        }
        if self.keywords.len() > 100 {
            return Err(MemHopError::InvalidArgument(
                "CrystallizeL3Request.keywords length must be ≤ 100".into(),
            ));
        }
        Ok(())
    }
}

/// L3 结晶化结果
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CrystallizeL3Report {
    pub domain_id: String,
    pub domain_name: String,
    pub l3_nodes_created: u32,
    pub topic_linked: bool,
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
}

impl Default for BrainConfig {
    fn default() -> Self {
        Self {
            brains_dir: "./memhop_brains".to_string(),
            agent_id: "default".to_string(),
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
pub enum ShelfDomain {
    Code,
    Doc,
    Book,
    Paper,
    Generic,
}

/// L3 超边构建策略 — 按领域类型区分超图结构
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum L3HyperedgeStrategy {
    /// Code: 同文件 Association + import 跨文件关联
    Structural,
    /// Book/Doc/Generic: 同章 + 邻章
    Sequential,
    /// Paper: 同 section + 引用
    Citation,
}

impl ShelfDomain {
    /// 返回该领域对应的超边构建策略
    pub fn hyperedge_strategy(&self) -> L3HyperedgeStrategy {
        match self {
            Self::Code => L3HyperedgeStrategy::Structural,
            Self::Doc | Self::Book | Self::Generic => L3HyperedgeStrategy::Sequential,
            Self::Paper => L3HyperedgeStrategy::Citation,
        }
    }
}

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

/// L3 领域元信息（替代裸 serde_json::Value）
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct DomainMeta {
    pub id: String,
    pub name: String,
    pub created_at: i64,
    pub node_count: u64,
    pub updated_at: i64,
    /// 反向索引 — 关联到该领域的 L2 topic ID 列表
    #[serde(default)]
    pub linked_topic_ids: Vec<String>,
    /// 关联权重 (topic_id → weight)
    #[serde(default)]
    pub topic_weights: HashMap<String, f32>,
}


// ── Dream ────────────────────────────────────────────────

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct DreamConfig {
    pub vitality_half_life_hours: f32,
    pub schema_min_topics: usize,
}

impl Default for DreamConfig {
    fn default() -> Self {
        Self {
            vitality_half_life_hours: 168.0,
            schema_min_topics: 5,
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
    /// v0.23.1: L3 Domain Router 最大域数量。None = 3。
    pub l3_max_domains: Option<usize>,
    /// v1.0: 激活的 L3 domain ID 列表
    /// Some([id1, id2]) → 仅在这些 L3 中搜索
    /// Some([]) → 跳过 L3 搜索
    /// None → 搜索所有已挂载 L3（默认行为）
    pub active_l3_domains: Option<Vec<String>>,
}

impl Default for RecallRequest {
    fn default() -> Self {
        Self {
            query: String::new(),
            max_results: 10,
            target_layers: vec![Layer::L1, Layer::L2],
            time_range: None,
            spread_depth: None,
            topic_filter: None,
            exclude_ids: Vec::new(),
            exclude_topic_ids: Vec::new(),
            l3_domain_id: None,
            l2_topic_id: None,
            session_id: None,
            time_decay_lambda: None,
            l3_max_domains: None,
            active_l3_domains: None,
        }
    }
}

// ── L0 Profile ────────────────────────────────────────────

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct L0Profile {
    /// 不可修改的唯一标识符，首次创建时设置
    pub catid: Option<String>,
    /// 可修改的名称
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
            catid: None,
            role_name: None,
            personality: Vec::new(),
            values: Vec::new(),
            worldview: Vec::new(),
            role: None,
            position: None,
            traits: HashMap::new(),
            updated_at: 0,
            version: 1,
            history: Vec::new(),
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
    /// v0.24.0: 情感维度
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub emotion: Option<EmotionalDimension>,
    /// v1.0: L3 结果关联的 domain_id（用于双向链接更新）
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub domain_id: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RecallResponse {
    pub results: Vec<RecallResult>,
    pub total_count: usize,
    pub l0_profile: Option<L0Profile>,
    pub confidence: Option<f32>,
    pub activated_topics: Vec<ActivatedTopicInfo>,
    /// v0.18.3: 程序性晶体推荐（与查询的 trigger_keywords 子串匹配）
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub recommended_crystals: Vec<ProceduralCrystal>,
    /// v1.0: 预测性记忆提示
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub prefetch: Vec<PrefetchHint>,
}

// ── Procedural Crystallization (v0.18.3) ─────────────────────

#[derive(Debug, Clone, Serialize, Deserialize)]
pub enum CrystalType {
    Sequence,
    Conditional,
    Iterative,
    Template,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CrystalStep {
    pub order: u32,
    pub action: String,
    pub expected_outcome: Option<String>,
    pub source_node_ids: Vec<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CrystalSnapshot {
    pub version: u64,
    pub label: String,
    pub steps: Vec<CrystalStep>,
    pub snapshot_at: i64,
    pub reason: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ProceduralCrystal {
    pub id: String,
    pub label: String,
    pub pattern_type: CrystalType,
    pub steps: Vec<CrystalStep>,
    pub trigger_keywords: Vec<String>,
    pub context_conditions: Vec<String>,
    pub source_chain_ids: Vec<String>,
    pub usage_count: u32,
    pub success_rate: f32,
    pub created_at: i64,
    pub updated_at: i64,
    pub version: u64,
    pub history: Vec<CrystalSnapshot>,
}

#[derive(Debug, Clone)]
pub(crate) struct ChainCluster {
    pub label_pattern: String,
    pub chain_ids: Vec<String>,
    pub frequency: u32,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct CrystallizeReport {
    pub crystals_created: u32,
    pub chains_analyzed: u32,
    pub duration_ms: u64,
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
    /// v0.25.0: L2 plan consolidation 压缩的话题数
    #[serde(default)]
    pub topics_consolidated: u32,
    /// v0.18.3: 程序性结晶生成的晶体数
    #[serde(default)]
    pub crystals_created: u32,
}

// ── StorageLayerInfo ────────────────────────────────────────

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct StorageLayerInfo {
    pub layer: String,
    pub used_bytes: u64,
    pub map_size: u64,
    pub usage_pct: f32,
}



// ── 单元测试 ──────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;

    // ── EmotionalFeedback validate ──

    #[test]
    fn test_feedback_validate_ok() {
        let fb = EmotionalFeedback {
            memory_id: "mem_1".into(),
            emotion: Emotion::Joy,
            intensity: 0.5,
            reason: None,
        };
        assert!(fb.validate().is_ok());
    }

    #[test]
    fn test_feedback_nan_intensity_rejected() {
        let fb = EmotionalFeedback {
            memory_id: "mem_1".into(),
            emotion: Emotion::Joy,
            intensity: f32::NAN,
            reason: None,
        };
        assert!(fb.validate().is_err());
    }

    #[test]
    fn test_feedback_inf_intensity_rejected() {
        let fb = EmotionalFeedback {
            memory_id: "mem_1".into(),
            emotion: Emotion::Joy,
            intensity: f32::INFINITY,
            reason: None,
        };
        assert!(fb.validate().is_err());
    }

    #[test]
    fn test_feedback_neg_intensity_rejected() {
        let fb = EmotionalFeedback {
            memory_id: "mem_1".into(),
            emotion: Emotion::Joy,
            intensity: -0.1,
            reason: None,
        };
        assert!(fb.validate().is_err());
    }

    #[test]
    fn test_feedback_too_high_intensity_rejected() {
        let fb = EmotionalFeedback {
            memory_id: "mem_1".into(),
            emotion: Emotion::Joy,
            intensity: 1.5,
            reason: None,
        };
        assert!(fb.validate().is_err());
    }

    #[test]
    fn test_feedback_empty_memory_id_rejected() {
        let fb = EmotionalFeedback {
            memory_id: "".into(),
            emotion: Emotion::Joy,
            intensity: 0.5,
            reason: None,
        };
        assert!(fb.validate().is_err());
    }

    // ── EmotionalDimension validate ──

    #[test]
    fn test_dimension_validate_ok() {
        let d = EmotionalDimension {
            emotion: Emotion::Joy,
            intensity: 0.5,
            valence: 0.3,
            arousal: 0.7,
        };
        assert!(d.validate().is_ok());
    }

    #[test]
    fn test_dimension_valence_too_high_rejected() {
        let d = EmotionalDimension {
            emotion: Emotion::Joy,
            intensity: 0.5,
            valence: 2.0,
            arousal: 0.5,
        };
        assert!(d.validate().is_err());
    }

    #[test]
    fn test_dimension_valence_too_low_rejected() {
        let d = EmotionalDimension {
            emotion: Emotion::Joy,
            intensity: 0.5,
            valence: -1.5,
            arousal: 0.5,
        };
        assert!(d.validate().is_err());
    }

    #[test]
    fn test_dimension_intensity_nan_rejected() {
        let d = EmotionalDimension {
            emotion: Emotion::Neutral,
            intensity: f32::NAN,
            valence: 0.0,
            arousal: 0.0,
        };
        assert!(d.validate().is_err());
    }

    // ── EmotionRecallRequest validate ──

    #[test]
    fn test_recall_request_validate_ok() {
        let req = EmotionRecallRequest {
            emotion: Some(Emotion::Joy),
            min_intensity: 0.3,
            time_decay_lambda: Some(0.001),
            max_results: 50,
        };
        assert!(req.validate().is_ok());
    }

    #[test]
    fn test_recall_request_max_results_too_high_rejected() {
        let req = EmotionRecallRequest {
            emotion: None,
            min_intensity: 0.0,
            time_decay_lambda: None,
            max_results: 99999,
        };
        assert!(req.validate().is_err());
    }

    #[test]
    fn test_recall_request_min_intensity_nan_rejected() {
        let req = EmotionRecallRequest {
            emotion: None,
            min_intensity: f32::NAN,
            time_decay_lambda: None,
            max_results: 10,
        };
        assert!(req.validate().is_err());
    }

    #[test]
    fn test_recall_request_negative_lambda_rejected() {
        let req = EmotionRecallRequest {
            emotion: None,
            min_intensity: 0.0,
            time_decay_lambda: Some(-0.5),
            max_results: 10,
        };
        assert!(req.validate().is_err());
    }

    #[test]
    fn test_recall_request_lambda_nan_rejected() {
        let req = EmotionRecallRequest {
            emotion: None,
            min_intensity: 0.0,
            time_decay_lambda: Some(f32::NAN),
            max_results: 10,
        };
        assert!(req.validate().is_err());
    }

    // ── CrystallizeL3Request validate ──

    #[test]
    fn test_crystallize_validate_ok() {
        let req = CrystallizeL3Request {
            topic_id: "topic_1".into(),
            summary: "test summary".into(),
            keywords: vec!["kw1".into(), "kw2".into()],
            domain_name: None,
        };
        assert!(req.validate().is_ok());
    }

    #[test]
    fn test_crystallize_empty_topic_id_rejected() {
        let req = CrystallizeL3Request {
            topic_id: "".into(),
            summary: "test".into(),
            keywords: vec!["kw1".into()],
            domain_name: None,
        };
        assert!(req.validate().is_err());
    }

    #[test]
    fn test_crystallize_empty_summary_rejected() {
        let req = CrystallizeL3Request {
            topic_id: "topic_1".into(),
            summary: "".into(),
            keywords: vec!["kw1".into()],
            domain_name: None,
        };
        assert!(req.validate().is_err());
    }

    #[test]
    fn test_crystallize_empty_keywords_rejected() {
        let req = CrystallizeL3Request {
            topic_id: "topic_1".into(),
            summary: "test".into(),
            keywords: vec![],
            domain_name: None,
        };
        assert!(req.validate().is_err());
    }

    #[test]
    fn test_crystallize_too_many_keywords_rejected() {
        let req = CrystallizeL3Request {
            topic_id: "topic_1".into(),
            summary: "test".into(),
            keywords: (0..101).map(|i| format!("kw{}", i)).collect(),
            domain_name: None,
        };
        assert!(req.validate().is_err());
    }


}
