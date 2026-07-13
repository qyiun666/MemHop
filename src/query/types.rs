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

/// Search filters for memory retrieval
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct SearchFilters {
    /// Filter by scene ID
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub scene_id: Option<String>,
    /// Filter by keywords (only return results matching these keywords)
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub keywords: Option<Vec<String>>,
}

/// Search query for memory retrieval
///
/// A general-purpose search interface that retrieves memories across specified
/// cognitive layers (L0-L5). Results are ranked by relevance scores.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SearchQuery {
    /// The search text / query string (required)
    pub query: String,
    /// Which cognitive layers to search (e.g., [0, 2, 3, 5])
    #[serde(default)]
    pub layers: Vec<u8>,
    /// Maximum number of results to return
    #[serde(default = "default_max_results")]
    pub max_results: usize,
    /// Minimum relevance score threshold [0.0, 1.0]
    #[serde(default)]
    pub min_score: f64,
    /// Whether to include L0 profile in results
    #[serde(default)]
    pub include_profile: bool,
    /// Optional filters to narrow results
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub filters: Option<SearchFilters>,
    /// Directly route to a specific L2 context by ID (skip vector search)
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub directed_l2_id: Option<String>,
    /// Directly route to a specific L3 hypergraph by ID
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub directed_l3_id: Option<String>,
    /// Auto-create a new L2 context when search returns no matches (0=off, 1=on)
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub auto_create: Option<u8>,
}

fn default_max_results() -> usize {
    20
}

/// Internal search query for the search pipeline — not part of public API.
/// Contains routing fields used by the internal retrieval engine.
#[derive(Debug, Clone)]
pub(crate) struct InternalSearchQuery {
    /// Current user dialogue content (required)
    pub dialogue: String,
    /// LLM-extracted keywords for encoding (when available)
    pub keywords: Vec<String>,
    /// Directed L2 context ID (optional)
    pub l2_id: Option<String>,
    /// Directed L3 hypergraph ID (optional)
    pub l3_id: Option<String>,
    /// Auto-create new context when no match found
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

// Re-export canonical StageReport and StageStatus from diagnostics module.
pub use crate::query::diagnostics::{StageReport, StageStatus};

/// Alias: DreamStage is the same as StageReport.
pub use crate::query::diagnostics::StageReport as DreamStage;

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

/// Update request for layer-generic memory updates
///
/// After `search` activates a memory context, this interface updates
/// the specified layer's fields.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct UpdateRequest {
    /// Target memory ID
    pub id: String,
    /// Target layer (0=profile, 2=context, 3=knowledge, 4=archive, 5=crystal)
    pub layer: u8,
    /// Fields to update as a map of field names to JSON values
    pub fields: std::collections::HashMap<String, serde_json::Value>,
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
    /// Update status
    pub status: UpdateStatus,
    /// Updated memory ID
    pub id: String,
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
    /// LLM-generated summary (from associated L2 context)
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub summary: Option<String>,
    /// Dominant emotion label derived from valence/arousal
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub dominant_emotion: Option<String>,
    /// Keywords extracted from the associated topic
    #[serde(default)]
    pub keywords: Vec<String>,
    /// Recall/relevance score (derived from importance)
    pub recall_score: f32,
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
    pub scene_id: String,
    pub user_keywords: Vec<String>,
    pub agent_keywords: Vec<String>,
    pub fused_keywords: Vec<String>,
    /// LLM-generated fused summary of the topic
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub fused_summary: Option<String>,
    /// Total number of turns (user + agent L4 refs)
    pub turn_count: usize,
    /// Whether this topic is currently active in a session
    pub is_active: bool,
    pub created_at: i64,
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
    pub scene_id: String,
    pub user_keywords: Vec<String>,
    pub user_timestamp: i64,
    pub agent_keywords: Vec<String>,
    pub agent_timestamp: i64,
    pub fused_keywords: Vec<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub fused_summary: Option<String>,
    pub children_ids: Vec<String>,
    pub user_l4_refs: Vec<String>,
    pub user_l3_refs: Vec<String>,
    pub agent_l4_refs: Vec<String>,
    pub agent_l3_refs: Vec<String>,
    pub created_at: i64,
    pub updated_at: i64,
}

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
}

/// Crystal list query
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CrystalListQuery {
    pub page: u32,
    pub page_size: u32,
    pub status_filter: Option<String>, // active/inactive/deprecated
    pub min_trigger_count: Option<u32>,
    pub keyword: Option<String>,
}

/// Crystal list result
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CrystalListResult {
    pub crystals: Vec<CrystalSummary>,
    pub total: u32,
    pub page: u32,
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

/// Update profile request (also used as ProfileDelta)
pub type ProfileDelta = UpdateProfileRequest;

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
// Store Batch Interface
// ============================================================================

/// Store result for batch store operations
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct StoreResult {
    /// Number of items successfully stored
    pub stored_count: u32,
    /// IDs of stored items
    pub item_ids: Vec<String>,
}

/// Store batch request containing multiple items
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct StoreBatch {
    /// List of items to store
    pub items: Vec<StoreItem>,
    /// Source information (e.g., which agent/system triggered this)
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub source_info: Option<String>,
    /// Import mode for handling duplicates
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub import_mode: Option<ImportMode>,
}

/// Individual item in a batch store operation
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct StoreItem {
    /// The text content to store
    pub content: String,
    /// Keywords for retrieval indexing
    #[serde(default)]
    pub keywords: Vec<String>,
    /// Source identifier string
    #[serde(default)]
    pub source: String,
    /// Target memory layer (0=profile, 1=L1, 2=context, 3=knowledge, 4=archive, 5=crystal)
    pub layer: u8,
    /// Source type categorization
    #[serde(default = "default_store_source_type")]
    pub source_type: String,
    /// Importance/relevance score (0.0 - 1.0)
    #[serde(default)]
    pub score: f64,
}

fn default_store_source_type() -> String {
    "UserInput".to_string()
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

/// Type aliases for L3 subgraph nodes and edges.
pub type SubgraphNode = GraphNode;
/// Type alias for L3 subgraph edges.
pub type SubgraphEdge = GraphEdge;
/// Type alias for L3 edge kind.
pub type EdgeKind = GraphEdgeKind;

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
    /// ID of the primary (surviving) context
    pub primary_id: String,
    /// Number of secondary contexts absorbed
    pub merged_count: u32,
    /// Total number of turns after merge
    pub new_turn_count: u32,
    /// IDs of the absorbed secondary contexts
    pub absorbed_topic_ids: Vec<String>,
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
