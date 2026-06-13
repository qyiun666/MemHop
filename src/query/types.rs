//! Public types for MemHop external API
//!
//! This module defines all the data structures used in the new external API
//! as specified in API_NEW.md.

use std::collections::HashMap;

// ============================================================================
// LLM Configuration
// ============================================================================

/// LLM configuration for dream stages and query enhancement
#[derive(Debug, Clone)]
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
#[derive(Debug, Clone)]
pub struct SearchQuery {
    /// Current dialogue content (for BM25 + vector search)
    pub dialogue: String,
    /// L2 topic unique identifier (exact match)
    pub l2_id: Option<String>,
    /// L3 knowledge domain unique identifier (exact match)
    pub l3_id: Option<String>,
    /// Maximum number of L2 topics to return (default: 10)
    pub l2_limit: usize,
    /// Maximum number of L3 knowledge items to return (default: 10)
    pub l3_limit: usize,
    /// Optional LLM enhancement configuration
    pub llm_enhance: Option<LlmConfig>,
    /// Auto-create L2 topic when search result is empty (0: no, 1: yes, default: 0)
    pub auto_create: u8,
}

/// Search result containing multi-layer memory content
#[derive(Debug, Clone)]
pub struct SearchResult {
    /// Retrieved memory IDs (for subsequent updates)
    pub memory_ids: Vec<String>,
    /// L0 - Agent profile
    pub l0_profile: Option<L0Profile>,
    /// L2 - Semantic topic list
    pub l2_topics: Vec<L2TopicResult>,
    /// L3 - Knowledge domain list
    pub l3_knowledge: Vec<L3KnowledgeResult>,
    /// L2 content associated via L1 (filtered by similarity threshold)
    pub l1_associated_l2: Vec<L2TopicResult>,
    /// L4 - Original archives (corresponding to L2)
    pub l4_archives: Vec<L4ArchiveResult>,
}

/// L0 Agent profile
#[derive(Debug, Clone)]
pub struct L0Profile {
    pub id: String,
    pub name: String,
    pub role: String,
    pub personality: String,
    pub worldview: String,
    pub preferences: HashMap<String, String>,
    pub created_at: i64,
    pub updated_at: i64,
}

/// L2 topic result
#[derive(Debug, Clone)]
pub struct L2TopicResult {
    pub id: String,
    pub title: String,
    pub summary: Option<String>,
    pub activation_score: f32,
    pub l1_count: usize,
    pub l3_refs: Vec<String>,
    pub l4_refs: Vec<String>,
}

/// L3 knowledge result
#[derive(Debug, Clone)]
pub struct L3KnowledgeResult {
    pub id: String,
    pub title: String,
    pub domain: String,
    pub text: String,
    pub knowledge_type: String,
    pub confidence: f32,
}

/// L4 archive result
#[derive(Debug, Clone)]
pub struct L4ArchiveResult {
    pub id: String,
    pub topic_id: String,
    pub content: String,
    pub timestamp: i64,
}

// ============================================================================
// Update Memory Interface (Interface 3)
// ============================================================================

/// Update request for creating or updating multi-layer memory
/// 
/// When l2_id is provided, updates the existing L2 topic:
/// - Appends current dialogue to L4 archive
/// - Updates L2 summary with compressed content
/// - Links L4 archive to L2
/// - Stores action chain to L5 crystals
/// 
/// When l2_id is None, creates a new L2 topic with the dialogue as title.
#[derive(Debug, Clone)]
pub struct UpdateRequest {
    /// L2 topic unique identifier (None means create new L2 topic)
    pub l2_id: Option<String>,
    /// Current round dialogue text
    pub dialogue_text: String,
    /// Compressed summary for current round (optional, will be appended to L2)
    pub summary: Option<String>,
    /// Action chain (stored to L5)
    pub action_chain: Vec<ActionItem>,
}

/// Action item for L5 crystal storage
#[derive(Debug, Clone)]
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
#[derive(Debug, Clone, PartialEq)]
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
#[derive(Debug, Clone)]
pub struct UpdateResult {
    /// Memory ID (newly created or updated)
    pub memory_id: String,
    /// L1 engram ID
    pub l1_engram_id: String,
    /// L2 topic ID
    pub l2_topic_id: String,
    /// L3 knowledge ID
    pub l3_knowledge_id: String,
    /// L4 archive ID
    pub l4_archive_id: String,
    /// L5 crystal IDs (one per action)
    pub l5_crystal_ids: Vec<String>,
    /// Update status
    pub status: UpdateStatus,
}

/// Update status enumeration
#[derive(Debug, Clone, PartialEq)]
pub enum UpdateStatus {
    Created,
    Updated,
    Merged,
}

// ============================================================================
// List Query Interfaces (Interfaces 6-12)
// ============================================================================

/// L1 list query
#[derive(Debug, Clone)]
pub struct L1ListQuery {
    pub page: usize,
    pub page_size: usize,
    pub state_filter: Option<String>, // Active/Latent/Dormant
    pub min_importance: Option<f32>,
    pub keyword: Option<String>,
}

/// L1 list result
#[derive(Debug, Clone)]
pub struct L1ListResult {
    pub items: Vec<L1Engram>,
    pub total: usize,
    pub page: usize,
    pub page_size: usize,
    pub has_more: bool,
}

/// L1 engram detail
#[derive(Debug, Clone)]
pub struct L1Engram {
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

/// L2 list query
#[derive(Debug, Clone)]
pub struct L2ListQuery {
    pub page: usize,
    pub page_size: usize,
    pub active_only: bool,
    pub keyword: Option<String>,
}

/// L2 list result
#[derive(Debug, Clone)]
pub struct L2ListResult {
    pub items: Vec<L2TopicSummary>,
    pub total: usize,
    pub page: usize,
    pub page_size: usize,
    pub has_more: bool,
}

/// L2 topic summary
#[derive(Debug, Clone)]
pub struct L2TopicSummary {
    pub id: String,
    pub title: String,
    pub node_count: usize,
    pub is_active: bool,
    pub updated_at: i64,
}

/// L2 topic detail
#[derive(Debug, Clone)]
pub struct L2TopicDetail {
    pub id: String,
    pub title: String,
    pub summary: Option<String>,
    pub node_ids: Vec<String>,
    pub l3_refs: Vec<String>,
    pub l4_refs: Vec<String>,
    pub parent_id: Option<String>,
    pub is_active: bool,
    pub importance: f32,
    pub activation_score: f32,
    pub created_at: i64,
    pub updated_at: i64,
}

/// L3 list query
#[derive(Debug, Clone)]
pub struct L3ListQuery {
    pub page: usize,
    pub page_size: usize,
    pub domain_filter: Option<String>,
    pub knowledge_type: Option<String>, // Factual/Procedural/Conceptual/Contextual
    pub keyword: Option<String>,
}

/// L3 list result
#[derive(Debug, Clone)]
pub struct L3ListResult {
    pub items: Vec<L3DomainSummary>,
    pub total: usize,
    pub page: usize,
    pub page_size: usize,
    pub has_more: bool,
}

/// L3 domain summary
#[derive(Debug, Clone)]
pub struct L3DomainSummary {
    pub id: String,
    pub title: String,
    pub domain: String,
    pub knowledge_type: String,
    pub importance: f32,
    pub confidence: f32,
    pub updated_at: i64,
}

/// L3 domain detail
#[derive(Debug, Clone)]
pub struct L3DomainDetail {
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

/// L4 page query
#[derive(Debug, Clone)]
pub struct L4PageQuery {
    pub page: usize,
    pub page_size: usize,
    pub start_time: Option<i64>,
    pub end_time: Option<i64>,
    pub content_type: Option<String>,
}

/// L4 list result
#[derive(Debug, Clone)]
pub struct L4ListResult {
    pub items: Vec<L4Archive>,
    pub total: usize,
    pub page: usize,
    pub page_size: usize,
    pub has_more: bool,
}

/// L4 archive
#[derive(Debug, Clone)]
pub struct L4Archive {
    pub id: String,
    pub content: String,
    pub content_type: String,
    pub source_ref: Option<String>,
    pub topic_id: Option<String>,
    pub node_ids: Vec<String>,
    pub created_at: i64,
}

/// L5 list query
#[derive(Debug, Clone)]
pub struct L5ListQuery {
    pub page: usize,
    pub page_size: usize,
    pub status_filter: Option<String>, // active/inactive/deprecated
    pub min_trigger_count: Option<u32>,
    pub keyword: Option<String>,
}

/// L5 list result
#[derive(Debug, Clone)]
pub struct L5ListResult {
    pub items: Vec<L5SkillSummary>,
    pub total: usize,
    pub page: usize,
    pub page_size: usize,
    pub has_more: bool,
}

/// L5 skill summary
#[derive(Debug, Clone)]
pub struct L5SkillSummary {
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

/// Update L0 profile request
#[derive(Debug, Clone)]
pub struct UpdateL0Request {
    pub name: Option<String>,
    pub role: Option<String>,
    pub personality: Option<String>,
    pub worldview: Option<String>,
    pub preferences: Option<HashMap<String, String>>,
}

// ============================================================================
// Merge L2 Topics Interface (Interface 18)
// ============================================================================

// L2TopicDetail is already defined above

// ============================================================================
// Import Memory Interface (Interface 19)
// ============================================================================

/// Import request
#[derive(Debug, Clone)]
pub struct ImportRequest {
    pub target_layer: TargetLayer,
    pub data: ImportData,
    pub mode: ImportMode,
    pub l3_title: Option<String>, // When importing L2, specify associated L3 domain title
}

/// Target layer enumeration
#[derive(Debug, Clone, PartialEq)]
pub enum TargetLayer {
    L0,
    L2,
    L3,
}

/// Import mode enumeration
#[derive(Debug, Clone, PartialEq)]
pub enum ImportMode {
    Merge,     // Update if exists, create if not
    Overwrite, // Force overwrite existing data
    Skip,      // Skip if exists
}

/// Import data enumeration
#[derive(Debug, Clone)]
pub enum ImportData {
    /// L0 profile data
    L0Profile {
        name: Option<String>,
        role: Option<String>,
        personality: Option<String>,
        worldview: Option<String>,
        preferences: Option<HashMap<String, String>>,
    },
    /// L2 topic data (supports batch)
    L2Topics(Vec<L2ImportItem>),
    /// L3 knowledge data (supports batch)
    L3Knowledge(Vec<L3ImportItem>),
}

/// L2 import item
#[derive(Debug, Clone)]
pub struct L2ImportItem {
    pub title: String,
    pub summary: Option<String>,
    pub keywords: Vec<String>,
    pub l3_domain: Option<String>, // Associated L3 knowledge domain title
}

/// L3 import item
#[derive(Debug, Clone)]
pub struct L3ImportItem {
    pub title: String,
    pub domain: String,
    pub knowledge_type: String, // Factual/Procedural/Conceptual/Contextual
    pub text: String,
    pub summary: Option<String>,
    pub keywords: Vec<String>,
    pub source_ref: Option<String>,
}

/// Import result
#[derive(Debug, Clone)]
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
#[derive(Debug, Clone, PartialEq)]
pub enum ImportStatus {
    Success,
    PartialSuccess,
    Failed,
}

/// Import error
#[derive(Debug, Clone)]
pub struct ImportError {
    pub index: usize,
    pub message: String,
}
