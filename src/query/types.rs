// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Public types for MemHop external API.

use serde::{Deserialize, Serialize};
use std::collections::HashMap;

// Re-export the canonical LlmParams from slot::context so public API types can use it directly.
pub use crate::layers::context::LlmParams;

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
// Search Memory Interface (Interface 2)
// ============================================================================

/// Search query for memory retrieval
///
/// # Routing logic
///
/// | Parameter   | Behavior |
/// |-------------|----------|
/// | `auto_create=1` | Skip all retrieval, create new L2 context directly |
/// | `l2_id`/`context_id` present & L2 exists | Skip triple retrieval, only L1-associate from that L2 |
/// | `l3_id` present | Restrict triple retrieval to L2 contexts containing this L3 |
/// | default | Full triple retrieval (vector + BM25 + n-gram) |
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SearchQuery {
    /// Current dialogue content (for BM25 + ngram + vector search)
    pub dialogue: String,
    /// L2 context ID (hex). If present and the L2 exists, skip retrieval
    /// and only find L1-associated contexts from this L2.
    pub l2_id: Option<String>,
    /// Backwards-compatible alias for `l2_id`.
    pub context_id: Option<String>,
    /// L3 hypergraph ID (hex). If present, restrict retrieval to L2
    /// contexts that contain this L3 in their l3_refs.
    pub l3_id: Option<String>,
    /// Maximum number of contexts to return (default: 10)
    #[serde(default = "default_context_limit")]
    pub context_limit: usize,
    /// Auto-create context when search result is empty (0: no, 1: yes, default: 0)
    #[serde(default)]
    pub auto_create: u8,
    /// Minimum relevance score threshold for search pruning (0.0-1.0, default: 0.0)
    #[serde(default)]
    pub min_score: f32,
    /// API 请求来源（记录是谁发起的搜索）
    #[serde(default, skip_serializing_if = "RequestSource::is_empty")]
    pub source: RequestSource,
}

/// Default context limit for search
fn default_context_limit() -> usize {
    10
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

/// Search result containing multi-layer memory content
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SearchResult {
    /// L0 - Agent profile
    #[serde(skip_serializing_if = "Option::is_none")]
    pub profile: Option<ProfileResult>,
    /// L2 - Matched contexts from retrieval
    pub contexts: Vec<ContextResult>,
    /// L2 - Associated depth-1 contexts (via L1 hypergraph edges)
    pub associated_contexts: Vec<ContextResult>,
    /// L3 - Hypergraph IDs referenced by matched contexts
    pub l3_ids: Vec<String>,
    /// L3 - Previews of matched knowledge graphs
    #[serde(default)]
    pub l3_previews: Vec<L3Preview>,
    /// L4 - Archive references from matched contexts
    pub archive_refs: Vec<ArchiveRef>,
}

/// L2 context result from search
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ContextResult {
    /// Context unique ID (hex)
    pub id: String,
    /// Parent context ID (hex), None for depth-1 scenes
    #[serde(skip_serializing_if = "Option::is_none")]
    pub parent_id: Option<String>,
    /// Nesting depth: 1=scene, 2=sub-scene, 3=turn group
    pub depth: u8,
    /// Scene name / title
    pub title: String,
    /// Compressed summary (if available)
    #[serde(skip_serializing_if = "Option::is_none")]
    pub summary: Option<String>,
    /// Activation score (retrieval relevance)
    pub activation_score: f32,
    /// Number of conversation turns
    pub turn_count: u32,
    /// L3 hypergraph IDs referenced by this context
    pub l3_refs: Vec<String>,
    /// L4 archive IDs referenced by this context
    pub archive_refs: Vec<String>,
    /// Scene-level recommended LLM parameters (refreshed during dream)
    #[serde(skip_serializing_if = "Option::is_none")]
    pub llm_params: Option<LlmParams>,
    /// Normalized retrieval fusion score [0.0, 1.0]
    pub retrieval_score: f32,
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
    pub title: String,
    pub depth: u8,
    pub scene_id: u64,
    pub children_ids: Vec<u64>,
    pub archive_count: usize,
    pub turn_count: u32,
    pub is_active: bool,
    pub updated_at: i64,
}

/// Topic detail (L2 ContextSlot detail)
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TopicDetail {
    pub id: String,
    pub title: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub summary: Option<String>,
    pub depth: u8,
    pub scene_id: u64,
    pub children_ids: Vec<u64>,
    pub archive_refs: Vec<String>,
    pub l3_refs: Vec<String>,
    pub turn_count: u32,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub parent_id: Option<String>,
    pub is_active: bool,
    pub importance: f32,
    pub activation_score: f32,
    pub activation_state: String,
    pub created_at: i64,
    pub updated_at: i64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub llm_params: Option<LlmParams>,
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
// L3 Hypergraph Types (used by src/l3/ engine)
// ============================================================================

/// Result of subgraph extraction
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Subgraph {
    pub nodes: Vec<crate::layers::hypergraph::HypergraphNode>,
    pub edges: Vec<crate::layers::hypergraph::HypergraphEdge>,
}

/// A single hop in graph traversal (BFS / shortest path)
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TraversalHop {
    pub depth: usize,
    #[serde(
        serialize_with = "crate::layers::hypergraph::serialize_hash_as_hex",
        deserialize_with = "crate::layers::hypergraph::deserialize_hash_from_hex"
    )]
    pub from_node: u64,
    pub edge: crate::layers::hypergraph::HypergraphEdge,
    #[serde(
        serialize_with = "crate::layers::hypergraph::serialize_hash_as_hex",
        deserialize_with = "crate::layers::hypergraph::deserialize_hash_from_hex"
    )]
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
    pub items: Vec<crate::layers::hypergraph::HypergraphNode>,
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
    pub kind: Option<crate::layers::hypergraph::GraphEdgeKind>,
    pub node_id: Option<String>,
}

/// Paginated result for listing L3 edges
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct EdgeListResult {
    pub items: Vec<crate::layers::hypergraph::HypergraphEdge>,
    pub total: usize,
    pub page: usize,
    pub page_size: usize,
    pub has_more: bool,
}

// ============================================================================
// CRUD update fields (Agent D v0.54)
// ============================================================================

/// Partial update fields for an L2 context.
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct UpdateL2Fields {
    pub title: Option<String>,
    pub summary: Option<String>,
    pub is_active: Option<bool>,
    pub importance: Option<f32>,
    pub activation_score: Option<f32>,
    pub activation_state: Option<String>,
    pub l3_refs: Option<Vec<String>>,
    pub llm_params: Option<LlmParams>,
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

/// Partial update fields for an L6 pathway weight.
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct UpdateL6Fields {
    pub source_node: Option<String>,
    pub target_node: Option<String>,
    pub weight: Option<f32>,
    pub success_rate: Option<f32>,
    pub trigger_count: Option<u32>,
    pub last_accessed: Option<u64>,
    pub metadata: Option<String>,
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

/// Filter for listing L6 pathway weights.
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct L6Filter {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub source_prefix: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub target_prefix: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub min_weight: Option<f32>,
}

/// Detailed view of an L3 hypergraph: container + nodes + edges.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct L3Detail {
    pub slot: crate::layers::hypergraph::HypergraphSlot,
    pub nodes: Vec<crate::layers::hypergraph::HypergraphNode>,
    pub edges: Vec<crate::layers::hypergraph::HypergraphEdge>,
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

/// Request to merge multiple L2 context nodes under a single scene parent.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct MergeNodesRequest {
    pub node_ids: Vec<String>,
    pub scene_id: String,
}

/// Result of a merge-nodes operation.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct MergeNodesResult {
    pub new_parent_node_id: String,
    pub sunk_node_ids: Vec<String>,
    pub removed_node_ids: Vec<String>,
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
