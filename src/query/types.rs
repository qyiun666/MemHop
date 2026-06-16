//! Public types for MemHop external API
//!
//! This module defines all the data structures used in the new external API
//! as specified in API_NEW.md.

use serde::{Deserialize, Serialize};
use std::collections::HashMap;

// ============================================================================
// LLM Configuration
// ============================================================================

/// LLM configuration for dream stages and query enhancement
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct LlmConfig {
    /// API endpoint URL
    pub api_url: String,
    /// API key
    pub api_key: String,
    /// Model name
    pub model: String,
    /// API format (1 = OpenAI format)
    pub api_format: u8,
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
/// | `context_id` present & L2 exists | Skip triple retrieval, only L1-associate from that L2 |
/// | `l3_id` present | Restrict triple retrieval to L2 contexts containing this L3 |
/// | default | Full triple retrieval (vector + BM25 + n-gram) |
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SearchQuery {
    /// Current dialogue content (for BM25 + ngram + vector search)
    pub dialogue: String,
    /// Context ID (hex). If present and the L2 exists, skip retrieval
    /// and only find L1-associated contexts from this L2.
    pub context_id: Option<String>,
    /// L3 hypergraph ID (hex). If present, restrict retrieval to L2
    /// contexts that contain this L3 in their l3_refs.
    pub l3_id: Option<String>,
    /// Maximum number of contexts to return (default: 10)
    pub context_limit: usize,
    /// Optional LLM enhancement configuration
    pub llm_enhance: Option<LlmConfig>,
    /// Auto-create context when search result is empty (0: no, 1: yes, default: 0)
    pub auto_create: u8,
    /// Minimum relevance score threshold for search pruning (0.0-1.0, default: 0.0)
    pub min_score: f32,
    /// Previous conversation context (optional). Used by LLM enhancement
    /// to resolve anaphora and fill in missing context for follow-up queries.
    pub context_history: Option<String>,
}

/// Search result containing multi-layer memory content
///
/// Retrieval flow:
/// 1. Triple retrieval (vector + BM25 + n-gram) on L2 context titles (depth 1 & 2)
/// 2. Via L1 hypergraph, find associated depth-1 contexts
/// 3. Return L0 profile, L3 ID list, L4 archive references
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SearchResult {
    /// L0 - Agent profile
    pub profile: Option<ProfileResult>,
    /// L2 - Matched contexts from retrieval
    pub contexts: Vec<ContextResult>,
    /// L2 - Associated depth-1 contexts (via L1 hypergraph edges)
    pub associated_contexts: Vec<ContextResult>,
    /// L3 - Hypergraph IDs referenced by matched contexts
    pub l3_ids: Vec<String>,
    /// L4 - Archive references from matched contexts
    pub archive_refs: Vec<ArchiveRef>,
}

/// L2 context result from search
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ContextResult {
    /// Context unique ID (hex)
    pub id: String,
    /// Parent context ID (hex), None for depth-1 scenes
    pub parent_id: Option<String>,
    /// Nesting depth: 1=scene, 2=sub-scene, 3=turn group
    pub depth: u8,
    /// Scene name / title
    pub title: String,
    /// Compressed summary (if available)
    pub summary: Option<String>,
    /// Activation score (retrieval relevance)
    pub activation_score: f32,
    /// Number of conversation turns
    pub turn_count: u32,
    /// L3 hypergraph IDs referenced by this context
    pub l3_refs: Vec<String>,
    /// L4 archive IDs referenced by this context
    pub archive_refs: Vec<String>,
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
    pub action_chain: Vec<ActionItem>,
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
}

/// Update status enumeration
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub enum UpdateStatus {
    Updated,
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
    pub summary: Option<String>,
    pub depth: u8,
    pub archive_refs: Vec<String>,
    pub l3_refs: Vec<String>,
    pub turn_count: u32,
    pub parent_id: Option<String>,
    pub is_active: bool,
    pub importance: f32,
    pub activation_score: f32,
    pub activation_state: String,
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
    pub summary: Option<String>,
    pub keywords: Vec<String>,
    pub edge_ptrs: Vec<String>,
    pub archive_refs: Vec<String>,
    pub source_ref: Option<String>,
    pub importance: f32,
    pub confidence: f32,
    pub created_at: i64,
    pub updated_at: i64,
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
    pub source_ref: Option<String>,
    pub topic_id: Option<String>,
    pub engram_ids: Vec<String>,
    pub created_at: i64,
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
}

// ============================================================================
// Merge Topics Interface (Interface 18)
// ============================================================================

// TopicDetail is already defined above

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
    /// Created IDs
    pub created_ids: Vec<String>,
    /// Updated IDs
    pub updated_ids: Vec<String>,
    /// Skipped count
    pub skipped_count: usize,
    /// Error messages (if any)
    pub errors: Vec<ImportError>,
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
    pub nodes: Vec<crate::slot::hypergraph::HypergraphNode>,
    pub edges: Vec<crate::slot::hypergraph::HypergraphEdge>,
}

/// A single hop in graph traversal (BFS / shortest path)
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TraversalHop {
    pub depth: usize,
    pub from_node: u64,
    pub edge: crate::slot::hypergraph::HypergraphEdge,
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
    pub items: Vec<crate::slot::hypergraph::HypergraphNode>,
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
    pub kind: Option<crate::slot::hypergraph::GraphEdgeKind>,
    pub node_id: Option<String>,
}

/// Paginated result for listing L3 edges
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct EdgeListResult {
    pub items: Vec<crate::slot::hypergraph::HypergraphEdge>,
    pub total: usize,
    pub page: usize,
    pub page_size: usize,
    pub has_more: bool,
}
