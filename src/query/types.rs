// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Public types for MemHop external API.

use serde::{Deserialize, Serialize};
use std::collections::HashMap;

// Re-export the canonical LlmParams from slot::context so public API types can use it directly.
pub use crate::layers::context::LlmParams;

/// Re-export GraphEdgeKind for public use in EdgeListQuery and GraphEdge.
pub use crate::layers::hypergraph::GraphEdgeKind;

/// API 请求来源信息 — 记录"是谁找的我"
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct RequestSource {
    /// 发起请求的 agent 标识（如 "claude", "cursor", "codex"）
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub source_agent: Option<String>,
    /// 发起请求的平台标识（如 "qoder", "vscode", "feishu"）
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub source_platform: Option<String>,
}

impl RequestSource {
    /// 检查是否为空（两个字段都是 None）
    pub fn is_empty(&self) -> bool {
        self.source_agent.is_none() && self.source_platform.is_none()
    }

    /// 序列化为 ArchiveSlot.metadata 可用的 JSON 字符串
    pub fn to_metadata_json(&self) -> Option<String> {
        if self.is_empty() {
            return None;
        }
        serde_json::to_string(self).ok()
    }

    /// 从 ArchiveSlot.metadata JSON 字符串反序列化
    pub fn from_metadata_json(metadata: &str) -> Self {
        serde_json::from_str(metadata).unwrap_or_else(|e| {
            tracing::warn!(
                "[RequestSource] Failed to parse metadata '{}': {}",
                metadata,
                e
            );
            Default::default()
        })
    }
}

// ============================================================================
// LLM Preprocessing types (v0.61)
// ============================================================================

/// Hint for L3 knowledge graph entity import, produced by search LLM preprocess.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct L3EntityHint {
    /// Entity name
    pub name: String,
    /// Entity type: concept, entity, skill, tool, version, framework
    #[serde(rename = "type")]
    pub entity_type: String,
}

/// Result of LLM search query preprocessing.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SearchPreprocessResult {
    /// Optimized keywords for BM25 + vector retrieval (5-10 items)
    pub keywords: Vec<String>,
    /// Whether the query should trigger L3 knowledge graph import
    pub needs_l3_import: bool,
    /// Entities to import into L3 (empty when needs_l3_import is false)
    pub l3_entities: Vec<L3EntityHint>,
}

/// Result of LLM write content preprocessing.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WritePreprocessResult {
    /// Extracted keywords for L2 storage and future retrieval (5-10 items)
    pub keywords: Vec<String>,
    /// Importance score (0.0-1.0)
    pub importance: f32,
}

// ============================================================================
// Search Memory Interface (Interface 2)
// ============================================================================

/// Search query for memory retrieval
///
/// # Routing logic
///
/// | Parameter | Behavior |
/// |-----------|----------|
/// | `auto_create=true` | Skip all retrieval, create new L2 context directly |
/// | `l2_id` present & L2 exists | Skip triple retrieval, only L1-associate from that L2 |
/// | `l3_id` present | Restrict triple retrieval to L2 contexts containing this L3 |
/// | default | Full triple retrieval (vector + BM25 + n-gram) |
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SearchQuery {
    /// Current user dialogue content (required)
    pub dialogue: String,
    /// Directed L2 context ID (optional). If present and the L2 exists,
    /// skip retrieval and directly associate from this L2.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub l2_id: Option<String>,
    /// Directed L3 hypergraph ID (optional). Restrict retrieval to L2
    /// contexts that contain this L3 in their l3_refs.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub l3_id: Option<String>,
    /// Auto-create new context when no match found (optional, replaces manual create_scene)
    #[serde(default)]
    pub auto_create: bool,
}

/// L1 ContextNode preview — lightweight summary for agent decision-making
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct L1Preview {
    pub id: String,
    /// Summary from the associated L2 context (if available)
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub summary: Option<String>,
    /// Node importance score
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub importance: Option<f64>,
    /// Dominant emotion label (derived from valence/arousal)
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub dominant_emotion: Option<String>,
    /// Keywords that matched during search
    #[serde(default)]
    pub matched_keywords: Vec<String>,
    /// Retrieval relevance score
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub recall_score: Option<f64>,
}

/// L3 knowledge graph preview — lightweight summary for agent decision-making
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct L3Preview {
    pub id: String,
    pub title: String,
    pub top_nodes: Vec<String>,
    pub keywords: Vec<String>,
    pub node_count: u32,
}

/// Search result — L0 profile + L2 contexts (depth ≤ 2) + L1 associates
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SearchResult {
    /// L0 - Agent profile
    #[serde(skip_serializing_if = "Option::is_none")]
    pub profile: Option<ProfileResult>,
    /// L2 - Primary matched contexts (depth ≤ 2, sorted by retrieval_score desc)
    pub contexts: Vec<ContextResult>,
    /// L2 - Associated contexts via L1 hypergraph edges (depth ≤ 2)
    pub associated_contexts: Vec<ContextResult>,
    /// L3 - Hypergraph IDs referenced by matched contexts
    pub l3_ids: Vec<String>,
    /// L1 - Previews of matched ContextNodes
    #[serde(default)]
    pub l1_previews: Vec<L1Preview>,
    /// LLM 预处理使用的关键词（回传给调用方用于展示/调试）
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub llm_keywords_used: Option<Vec<String>>,
    /// L3 知识图谱导入提示（needs_l3_import 为 true 时非空）
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub l3_import_hints: Option<Vec<L3EntityHint>>,
}

/// L2 context hit from search (depth ≤ 2)
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ContextResult {
    /// Context unique ID (hex)
    pub id: String,
    /// Parent context ID (hex), None for depth-1 roots
    #[serde(skip_serializing_if = "Option::is_none")]
    pub parent_id: Option<String>,
    /// Nesting depth: 1=raw turn, 2=compressed group, 3=meta (not returned)
    pub depth: u8,
    /// Scene ID (hex)
    pub scene_id: String,
    /// User-turn keywords
    pub user_keywords: Vec<String>,
    /// User message timestamp (ms)
    pub user_timestamp: i64,
    /// Agent-turn keywords
    pub agent_keywords: Vec<String>,
    /// Agent reply timestamp (ms)
    pub agent_timestamp: i64,
    /// Fused keywords (depth ≥ 2, from compressed children)
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub fused_keywords: Vec<String>,
    /// Fused summary (depth ≥ 2, LLM-generated)
    #[serde(skip_serializing_if = "Option::is_none")]
    pub fused_summary: Option<String>,
    /// Child node IDs (hex)
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub children_ids: Vec<String>,
    /// Combined L4 archive refs (hex)
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub l4_refs: Vec<String>,
    /// Combined L3 hypergraph refs (hex)
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub l3_refs: Vec<String>,
    /// Normalized retrieval fusion score [0.0, 1.0]
    pub retrieval_score: f32,
    /// Created at (ms)
    pub created_at: i64,
    /// Updated at (ms)
    pub updated_at: i64,
}

/// L4 archive reference (lightweight pointer)
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ArchiveRef {
    /// Archive unique ID (hex)
    pub id: String,
    /// Associated L2 context ID (hex)
    pub context_id: String,
    /// Content type (text/image/document/etc.)
    pub content_type: String,
    /// Timestamp
    pub created_at: i64,
    /// 请求来源信息（从 ArchiveSlot.metadata 解析）
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub source_agent: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub source_platform: Option<String>,
}

/// Agent profile
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ProfileResult {
    pub id: String,
    pub name: String,
    pub role: String,
    pub personality: String,
    pub worldview: String,
    pub preferences: HashMap<String, String>,
    /// User vocabulary: unique word → meaning mapping
    #[serde(default)]
    pub lexicon: HashMap<String, String>,
    /// Communication style trait tags
    #[serde(default)]
    pub style_traits: Vec<String>,
    /// Emotional expression patterns: expression → true meaning
    #[serde(default)]
    pub emotion_patterns: HashMap<String, String>,
    pub created_at: i64,
    pub updated_at: i64,
}

// ============================================================================
// Update Memory Interface (Interface 3)
// ============================================================================

/// Update request for activated L2 context memory updates
///
/// After search_memory activates an L2 context, this interface:
/// 1. Writes dialogue_text to L4 ArchiveSlot on disk
/// 2. Writes action_chain to L5 ActionChainSlot on disk
/// 3. Appends L4 archive_id to L2 archive_refs index
/// 4. Appends summary to L2 context summary
///
/// topic_id is required - the L2 must already be activated.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct UpdateRequest {
    /// Activated L2 topic ID (required, returned by search_memory)
    pub topic_id: String,
    /// Current round dialogue text (written to L4 on disk by this interface)
    pub dialogue_text: String,
    /// Compressed summary for current round (optional, appended to L2 context summary)
    pub summary: Option<String>,
    /// Action chain (written to L5 on disk by this interface)
    #[serde(default)]
    pub action_chain: Option<Vec<ActionItem>>,
    /// Enable instant L3 knowledge distillation (optional, default: false)
    #[serde(default)]
    pub instant_distill: bool,
    /// API 请求来源（记录是谁发起的更新，会写入 L4 ArchiveSlot.metadata）
    #[serde(default, skip_serializing_if = "RequestSource::is_empty")]
    pub source: RequestSource,
    /// Scene identifier for grouping related contexts.
    ///
    /// **强烈建议传入**：相同 `scene_id` 的上下文会被 Dream 阶段的
    /// 合并压缩（`l2_merge_compress`）检测并归入同一场景树，从而
    /// 实现跨话题的摘要合并与深度降级。不传入时每个 topic 独立成场景，
    /// 失去跨话题合并压缩的价值。
    #[serde(default)]
    pub scene_id: Option<String>,
    /// LLM 预处理后的用户关键词（传入则直接用于 L2 存储，跳过默认值）
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub user_keywords: Option<Vec<String>>,
    /// LLM 预处理后的 Agent 关键词
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub agent_keywords: Option<Vec<String>>,
}

/// Action item for L5 action chain storage
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ActionItem {
    /// Action title (e.g., "create file", "write code")
    pub title: String,
    /// Action description
    pub description: String,
    /// Action type
    pub action_type: ActionType,
    /// Action parameters (optional)
    #[serde(skip_serializing_if = "Option::is_none")]
    pub parameters: Option<HashMap<String, String>>,
}

/// Action type enumeration
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub enum ActionType {
    Create,
    Read,
    Update,
    Delete,
    Execute,
    Query,
    Custom,
}

/// Update result
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct UpdateResult {
    /// L2 topic ID
    pub topic_id: String,
    /// L4 archive ID created by this update
    pub archive_id: String,
    /// Update status
    pub status: UpdateStatus,
    /// Whether this update triggered an automatic dream consolidation
    /// because archive or summary thresholds were exceeded.
    #[serde(default)]
    pub dream_triggered: bool,
    /// Node ID for the newly created turn (if created)
    #[serde(default)]
    pub turn_node_id: String,
}

/// Update status enumeration
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub enum UpdateStatus {
    /// A new L2 context was created (e.g., via `auto_create`).
    Created,
    /// An existing L2 context was updated.
    Updated,
    /// Only an L4 archive was appended; no summary/L3/action-chain changes.
    Archived,
}

// ============================================================================
// L1 Graph — public DTOs for L1 layer visualization
// ============================================================================

/// L1 层完整图结构，供 Agent 侧构建可视化图
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct L1Graph {
    pub nodes: Vec<L1Node>,
    pub edges: Vec<L1Edge>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct L1Node {
    pub id: String,       // hex(id_hash)
    pub scene_id: String, // hex(scene_id)
    pub topic_ids: Vec<String>,
    pub depth: u32,
    pub importance: f32,
    pub valence: f64,
    pub arousal: f64,
    pub created_at: i64,
    pub updated_at: i64,
    pub edge_ids: Vec<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct L1Edge {
    pub id: String,   // hex(id_hash)
    pub kind: String, // HyperedgeKind 的字符串表示
    pub node_ids: Vec<String>,
    pub weight: f32,
    pub created_at: i64,
}

// ============================================================================
// List Query Interfaces (Interfaces 6-12)
// ============================================================================

/// Engram list query
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct EngramListQuery {
    pub page: usize,
    pub page_size: usize,
    pub state_filter: Option<String>, // Active/Latent/Dormant
    pub min_importance: Option<f32>,
    pub keyword: Option<String>,
}

/// Engram list result
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct EngramListResult {
    pub items: Vec<EngramResult>,
    pub total: usize,
    pub page: usize,
    pub page_size: usize,
    pub has_more: bool,
}

/// Engram detail
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct EngramResult {
    pub id: String,
    pub text: String,
    /// Compressed summary (if available)
    #[serde(skip_serializing_if = "Option::is_none")]
    pub summary: Option<String>,
    pub keywords: Vec<String>,
    pub memory_state: String, // Active/Latent/Dormant
    pub importance: f32,
    pub source_type: String,
    pub created_at: i64,
    pub updated_at: i64,
    pub edge_count: usize,
    pub associated_topics: Vec<String>,
}

/// Topic list query
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TopicListQuery {
    pub page: usize,
    pub page_size: usize,
    pub active_only: bool,
    pub keyword: Option<String>,
}

/// Topic list result
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TopicListResult {
    pub items: Vec<TopicSummary>,
    pub total: usize,
    pub page: usize,
    pub page_size: usize,
    pub has_more: bool,
}

/// Topic summary (L2 context list item)
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TopicSummary {
    pub id: String,
    pub depth: u8,
    pub scene_id: u64,
    pub user_keywords: Vec<String>,
    pub agent_keywords: Vec<String>,
    pub fused_keywords: Vec<String>,
    pub l4_count: usize,
    pub l3_count: usize,
    pub updated_at: i64,
}

/// Topic detail (L2 TopicSlot full view)
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TopicDetail {
    pub id: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub parent_id: Option<String>,
    pub depth: u8,
    pub scene_id: u64,
    pub user_keywords: Vec<String>,
    pub user_timestamp: i64,
    pub agent_keywords: Vec<String>,
    pub agent_timestamp: i64,
    pub fused_keywords: Vec<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub fused_summary: Option<String>,
    pub children_ids: Vec<u64>,
    pub user_l4_refs: Vec<String>,
    pub user_l3_refs: Vec<String>,
    pub agent_l4_refs: Vec<String>,
    pub agent_l3_refs: Vec<String>,
    pub created_at: i64,
    pub updated_at: i64,
}

/// Backward-compatibility alias for the unified LlmParams.
pub type LlmParamsDto = LlmParams;

/// Knowledge list query
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct KnowledgeListQuery {
    pub page: usize,
    pub page_size: usize,
    pub domain_filter: Option<String>,
    pub knowledge_type: Option<String>, // Factual/Procedural/Conceptual/Contextual
    pub keyword: Option<String>,
}

/// Knowledge list result
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct KnowledgeListResult {
    pub items: Vec<KnowledgeSummary>,
    pub total: usize,
    pub page: usize,
    pub page_size: usize,
    pub has_more: bool,
}

/// Knowledge summary
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct KnowledgeSummary {
    pub id: String,
    pub title: String,
    pub domain: String,
    pub knowledge_type: String,
    pub importance: f32,
    pub confidence: f32,
    pub updated_at: i64,
}

/// Knowledge detail
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct KnowledgeDetail {
    pub id: String,
    pub title: String,
    pub domain: String,
    pub knowledge_type: String,
    pub text: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub summary: Option<String>,
    pub keywords: Vec<String>,
    pub edge_ptrs: Vec<String>,
    pub archive_refs: Vec<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub source_ref: Option<String>,
    pub importance: f32,
    pub confidence: f32,
    pub created_at: i64,
    pub updated_at: i64,
}

/// Single L3 knowledge node detail (for batch get by IDs)
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct KnowledgeNodeDetail {
    pub id: String,
    pub title: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub text: Option<String>,
    pub keywords: Vec<String>,
    pub domain: String,
    pub knowledge_type: String,
    pub created_at: i64,
    pub importance: f32,
}

/// Unified query for L3 knowledge node retrieval.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub enum KnowledgeNodeQuery {
    ByIds {
        ids: Vec<String>,
        #[serde(default)]
        include_text: bool,
    },
    ByKeyword {
        graph_id: String,
        keyword: String,
        #[serde(default = "default_limit")]
        limit: usize,
    },
    ByType {
        graph_id: String,
        node_type: String,
        #[serde(default = "default_limit")]
        limit: usize,
    },
}

fn default_limit() -> usize {
    20
}

/// Batch result for L3 knowledge node retrieval by IDs
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct KnowledgeNodesResult {
    pub nodes: Vec<KnowledgeNodeDetail>,
    pub total: usize,
    pub requested: usize,
}

/// Archive page query
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ArchivePageQuery {
    pub page: usize,
    pub page_size: usize,
    pub start_time: Option<i64>,
    pub end_time: Option<i64>,
    pub content_type: Option<String>,
}

/// Archive list result
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ArchiveListResult {
    pub items: Vec<Archive>,
    pub total: usize,
    pub page: usize,
    pub page_size: usize,
    pub has_more: bool,
}

/// Archive
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Archive {
    pub id: String,
    pub content: String,
    pub content_type: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub source_ref: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub topic_id: Option<String>,
    pub engram_ids: Vec<String>,
    pub created_at: i64,
    /// 请求来源信息（从 ArchiveSlot.metadata 解析）
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub source_agent: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub source_platform: Option<String>,
}

/// Crystal list query
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CrystalListQuery {
    pub page: usize,
    pub page_size: usize,
    pub status_filter: Option<String>, // active/inactive/deprecated
    pub min_trigger_count: Option<u32>,
    pub keyword: Option<String>,
}

/// Crystal list result
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CrystalListResult {
    pub items: Vec<CrystalSummary>,
    pub total: usize,
    pub page: usize,
    pub page_size: usize,
    pub has_more: bool,
}

/// Crystal summary
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CrystalSummary {
    pub id: String,
    pub title: String,
    pub condition: String,
    pub status: String, // active/inactive/deprecated
    pub trigger_count: u32,
    pub success_rate: f32,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub last_triggered: Option<i64>,
    pub created_at: i64,
}

// ============================================================================
// Update Title Interfaces (Interfaces 13-16)
// ============================================================================

/// Update profile request
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct UpdateProfileRequest {
    pub name: Option<String>,
    pub role: Option<String>,
    pub personality: Option<String>,
    pub worldview: Option<String>,
    pub preferences: Option<HashMap<String, String>>,
    /// User vocabulary to merge (word → meaning)
    pub lexicon: Option<HashMap<String, String>>,
    /// Communication style traits to set
    pub style_traits: Option<Vec<String>>,
    /// Emotional expression patterns to merge
    pub emotion_patterns: Option<HashMap<String, String>>,
}

// ============================================================================
// Merge Topics Interface (Interface 18)
// ============================================================================

// ============================================================================
// Import Memory Interface (Interface 19)
// ============================================================================

/// Import request
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ImportRequest {
    pub target_layer: TargetLayer,
    pub data: ImportData,
    pub mode: ImportMode,
    pub knowledge_title: Option<String>, // When importing topics, specify associated knowledge domain title
}

/// Target layer enumeration
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub enum TargetLayer {
    Profile,
    Topic,
    Knowledge,
}

/// Import mode enumeration
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub enum ImportMode {
    Merge,     // Update if exists, create if not
    Overwrite, // Force overwrite existing data
    Skip,      // Skip if exists
}

/// Import data enumeration
#[derive(Debug, Clone, Serialize, Deserialize)]
pub enum ImportData {
    /// Profile data
    Profile {
        name: Option<String>,
        role: Option<String>,
        personality: Option<String>,
        worldview: Option<String>,
        preferences: Option<HashMap<String, String>>,
    },
    /// Topic data (supports batch)
    Topics(Vec<TopicImportItem>),
    /// Knowledge data (supports batch)
    Knowledge(Vec<KnowledgeImportItem>),
}

/// Topic import item
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TopicImportItem {
    pub title: String,
    pub summary: Option<String>,
    pub keywords: Vec<String>,
    pub knowledge_domain: Option<String>, // Associated knowledge domain title
}

/// Knowledge import item
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct KnowledgeImportItem {
    pub title: String,
    pub domain: String,
    pub knowledge_type: String, // Factual/Procedural/Conceptual/Contextual
    pub text: String,
    pub summary: Option<String>,
    pub keywords: Vec<String>,
    pub source_ref: Option<String>,
}

/// Import result
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ImportResult {
    /// Import status
    pub status: ImportStatus,
    /// Single node ID (non-batch, first created ID)
    #[serde(skip_serializing_if = "Option::is_none")]
    pub id: Option<String>,
    /// All created node IDs (batch mode)
    #[serde(skip_serializing_if = "Option::is_none")]
    pub ids: Option<Vec<String>>,
    /// Created IDs (legacy, kept for backward compatibility)
    pub created_ids: Vec<String>,
    /// Updated IDs
    pub updated_ids: Vec<String>,
    /// Skipped count
    pub skipped_count: usize,
    /// Error messages (if any)
    pub errors: Vec<ImportError>,
    /// Echoed knowledge_title from import request
    #[serde(skip_serializing_if = "Option::is_none")]
    pub knowledge_title: Option<String>,
    /// Number of created nodes
    pub node_count: usize,
}

/// Import status enumeration
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub enum ImportStatus {
    Success,
    PartialSuccess,
    Failed,
}

/// Import error
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ImportError {
    pub index: usize,
    pub message: String,
}

// ============================================================================
// L3 Hypergraph Types — public DTOs for hypergraph nodes, edges, and queries
// ============================================================================

use crate::shared::common::format_hash;

/// Session status — aggregate view of active sessions.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SessionStatus {
    pub active_topic_ids: Vec<String>,
    pub count: usize,
    pub is_empty: bool,
}

/// Public DTO for an L3 hypergraph node.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct GraphNode {
    pub id: String,
    pub graph_id: String,
    pub title: String,
    pub node_type: String,
    pub content: String,
    pub keywords: Vec<String>,
    pub source_ref: Option<String>,
    pub importance: f32,
    pub summary: Option<String>,
    pub created_at: i64,
    pub updated_at: i64,
}

impl From<crate::layers::hypergraph::HypergraphNode> for GraphNode {
    fn from(n: crate::layers::hypergraph::HypergraphNode) -> Self {
        Self {
            id: format_hash(n.id_hash),
            graph_id: format_hash(n.graph_id),
            title: n.title,
            node_type: n.node_type,
            content: n.content,
            keywords: n.keywords,
            source_ref: n.source_ref,
            importance: n.importance,
            summary: n.summary,
            created_at: n.created_at,
            updated_at: n.updated_at,
        }
    }
}

/// Public DTO for an L3 hypergraph edge.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct GraphEdge {
    pub id: String,
    pub graph_id: String,
    pub kind: GraphEdgeKind,
    pub node_ids: Vec<String>,
    pub weight: f32,
    pub label: Option<String>,
    pub description: Option<String>,
    pub confidence: f32,
    pub created_at: i64,
}

impl From<crate::layers::hypergraph::HypergraphEdge> for GraphEdge {
    fn from(e: crate::layers::hypergraph::HypergraphEdge) -> Self {
        Self {
            id: format_hash(e.id_hash),
            graph_id: format_hash(e.graph_id),
            kind: e.kind,
            node_ids: e.node_ids.iter().map(|&h| format_hash(h)).collect(),
            weight: e.weight,
            label: e.label,
            description: e.description,
            confidence: e.confidence,
            created_at: e.created_at,
        }
    }
}

/// Public DTO for an L3 hypergraph slot (container metadata).
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct GraphSlot {
    pub id: String,
    pub name: String,
    pub node_count: u32,
    pub edge_count: u32,
    pub created_at: i64,
    pub updated_at: i64,
}

impl From<crate::layers::hypergraph::HypergraphSlot> for GraphSlot {
    fn from(s: crate::layers::hypergraph::HypergraphSlot) -> Self {
        Self {
            id: format_hash(s.id_hash),
            name: s.name,
            node_count: s.node_count,
            edge_count: s.edge_count,
            created_at: s.created_at,
            updated_at: s.updated_at,
        }
    }
}

/// Result of subgraph extraction.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Subgraph {
    pub nodes: Vec<GraphNode>,
    pub edges: Vec<GraphEdge>,
}

/// A single hop in graph traversal (BFS / shortest path).
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TraversalHop {
    pub depth: usize,
    pub from_node: u64,
    pub edge: GraphEdge,
    pub to_node: u64,
}

/// Query parameters for listing L3 nodes by graph
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct NodeListQuery {
    pub page: usize,
    pub page_size: usize,
    pub node_type: Option<String>,
    pub keyword: Option<String>,
    pub min_importance: Option<f32>,
}

/// Paginated result for listing L3 nodes
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct NodeListResult {
    pub items: Vec<GraphNode>,
    pub total: usize,
    pub page: usize,
    pub page_size: usize,
    pub has_more: bool,
}

/// Query parameters for listing L3 edges by graph
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct EdgeListQuery {
    pub page: usize,
    pub page_size: usize,
    pub kind: Option<GraphEdgeKind>,
    pub node_id: Option<String>,
}

/// Paginated result for listing L3 edges
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct EdgeListResult {
    pub items: Vec<GraphEdge>,
    pub total: usize,
    pub page: usize,
    pub page_size: usize,
    pub has_more: bool,
}

// ============================================================================
// CRUD update fields (Agent D v0.54)
// ============================================================================

/// Partial update fields for an L2 TopicSlot.
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct UpdateL2Fields {
    /// Replace user_keywords
    pub user_keywords: Option<Vec<String>>,
    /// Replace agent_keywords
    pub agent_keywords: Option<Vec<String>>,
    /// Replace fused_summary
    pub fused_summary: Option<String>,
    /// Replace user_l3_refs (agent cleared)
    pub l3_refs: Option<Vec<String>>,
}

/// Partial update fields for an L3 hypergraph container.
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct UpdateL3Fields {
    pub name: Option<String>,
}

/// Partial update fields for an L5 action chain.
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct UpdateL5Fields {
    pub title: Option<String>,
    pub trigger: Option<String>,
    pub status: Option<String>,
    pub confidence: Option<f32>,
    pub success_rate: Option<f32>,
    pub trigger_count: Option<u32>,
    pub last_triggered: Option<i64>,
}

/// Query for L4 archive searches.
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct L4SearchQuery {
    /// Return the N most recent archives.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub recent: Option<usize>,
    /// Filter by inclusive time range (start_ms, end_ms).
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub time_range: Option<(i64, i64)>,
    /// Filter by keywords matched against archive content.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub keywords: Option<Vec<String>>,
}

/// Unified query for L4 archive retrieval.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ArchiveQuery {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub topic_id: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub keyword: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub time_range: Option<(i64, i64)>,
    #[serde(default)]
    pub page: usize,
    #[serde(default = "default_page_size")]
    pub page_size: usize,
}

fn default_page_size() -> usize {
    20
}

/// Detailed view of an L3 hypergraph: container + nodes + edges.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct L3Detail {
    pub slot: GraphSlot,
    pub nodes: Vec<GraphNode>,
    pub edges: Vec<GraphEdge>,
}

/// Result of merging multiple L2 contexts into a primary context.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct MergeResult {
    pub primary: TopicDetail,
    pub merged_ids: Vec<String>,
}

/// Result of querying a scene tree — all nodes in a scene with edge topology.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SceneTreeResult {
    pub scene_id: String,
    pub total_turns: u32,
    pub depth_distribution: [u32; 4],
    pub nodes: Vec<TopicDetail>,
    pub edges: Vec<(String, String)>,
}

/// Request to merge secondary scenes into a main scene.
///
/// All nodes from `secondary_scene_ids` have their `scene_id` changed to
/// `main_scene_id`.  No other metadata is modified — pure scene reassignment.
/// Dream pipeline later decides whether to compress within the merged scene.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct MergeNodesRequest {
    /// The target scene that absorbs all secondary scene nodes.
    pub main_scene_id: String,
    /// Scenes whose nodes will be reassigned to the main scene.
    pub secondary_scene_ids: Vec<String>,
}

/// Result of merging secondary scenes into a main scene.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct MergeNodesResult {
    pub main_scene_id: String,
    /// Total number of nodes that had their scene_id reassigned.
    pub merged_node_count: u32,
}

/// Result of a merge-compress dream cycle.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct MergeCompressResult {
    pub groups_detected: u32,
    pub nodes_merged: u32,
    pub parent_nodes_created: u32,
    pub nodes_sunk: u32,
    pub nodes_removed: u32,
}

// ============================================================================
// Diagnostic / Inspection Types (Interface 20-21)
// ============================================================================

/// MemHop instance health status.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct HealthStatus {
    /// Overall health (true when no issues detected)
    pub ok: bool,
    /// Database file size in bytes
    pub db_size_bytes: u64,
    /// Count of entries per memory layer
    pub layer_counts: std::collections::HashMap<String, usize>,
    /// Timestamp of the last Dream consolidation (None if never run)
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub last_dream_at: Option<String>,
    /// Whether an encoder (gRPC vector model) is configured
    pub encoder_configured: bool,
    /// Whether the IVF vector index has been built
    pub ivf_index_built: bool,
    /// Detected issues / warnings
    #[serde(default)]
    pub issues: Vec<String>,
}

/// MemHop memory layer statistics.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct MemHopStats {
    /// L0 agent profile exists
    pub l0_profile_exists: bool,
    /// L1 ContextNode (engram) count
    pub l1_engram_count: usize,
    /// L2 topic context count
    pub l2_topic_count: usize,
    /// L3 hypergraph container count
    pub l3_graph_count: usize,
    /// L4 archive count
    pub l4_archive_count: usize,
    /// L5 action-chain / crystal count
    pub l5_crystal_count: usize,
    /// Database file size in bytes
    pub db_size_bytes: u64,
    /// IVF cluster count (0 if index not built)
    pub ivf_cluster_count: usize,
    /// Cache hit rate (0.0 if cache metrics not available)
    pub cache_hit_rate: f64,
}
