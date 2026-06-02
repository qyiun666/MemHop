//! Core data types for MemHop — pure Rust.
//!
//! This module serves dual purpose:
//! 1. Backward-compatible types for the old MemHop engine
//! 2. Public API types for the new Brain architecture (v0.7.3+)

use std::collections::HashMap;

use half::f16;

use crate::engram::{AssociationKind, Engram, DialogueTurn, EngramKind};
use crate::personality::Personality;

// ── Protection levels (backward compat) ───────────────────

pub use crate::engram::Protection;

// ── Public Memory type (backward compat) ──────────────────

#[derive(Debug, Clone)]
pub struct Memory {
    pub id: String,
    pub text: String,
    pub meta: HashMap<String, serde_json::Value>,
    pub confidence: f32,
    pub created_at: String,
    pub content_type: Option<String>,
    pub blob: Option<Vec<u8>>,
    pub is_archived: bool,
}

// ── StoreOptions (backward compat) ────────────────────────

pub struct StoreOptions {
    pub auto_entangle: bool,
    pub context_snippet: Option<String>,
    pub manual_links: Vec<String>,
    pub ttl_secs: Option<u64>,
}

impl Default for StoreOptions {
    fn default() -> Self {
        StoreOptions {
            auto_entangle: true,
            context_snippet: None,
            manual_links: Vec::new(),
            ttl_secs: None,
        }
    }
}

// ── DreamConfig (backward compat) ─────────────────────────

#[derive(Debug, Clone)]
pub struct DreamConfig {
    pub auto_trigger_interval: usize,
    pub merge_threshold: f32,
    pub weaken_threshold: f32,
    pub max_duration_ms: u64,
}

impl Default for DreamConfig {
    fn default() -> Self {
        DreamConfig {
            auto_trigger_interval: 100,
            merge_threshold: 0.95,
            weaken_threshold: 0.3,
            max_duration_ms: 500,
        }
    }
}

// ── BrainConfig (v0.7.3+ Brain API) ───────────────────────

/// v0.13.0: Default directory for per-agent brain storage.
/// Used by the MCP server when MEMHOP_BRAINS_DIR is not set.
/// Resolved relative to the user's home directory.
pub const DEFAULT_BRAINS_DIR: &str = ".memhop/brains";

#[derive(Debug, Clone)]
pub struct BrainConfig {
    pub personality: Personality,
    pub innate_schemas: Vec<InnateSchema>,
    pub initial_anchors: Vec<String>,
    pub cortex_capacity: usize,
    pub hippocampus_capacity: usize,
    pub dream_interval: usize,
    pub dream_max_ms: u64,
    /// v0.8.0: Optional override for PlanGate boundary threshold (default 0.55).
    pub plan_boundary_threshold: Option<f32>,
    /// v0.9.0: Optional API base URL for the ApiEncoder (e.g. SiliconFlow, OpenAI).
    pub api_base_url: Option<String>,
    /// v0.10.0: Path to Cross-Encoder reranker model directory (e.g. "models/bge-reranker-v2-m3").
    /// None disables reranker. Default: Some("models/bge-reranker-v2-m3").
    pub reranker_model_path: Option<String>,
    /// v0.11.0: Path to ONNX encoder model directory (e.g. "models/bge-m3").
    /// When set, replaces the built-in NgramEncoder for semantic dense vectors.
    /// NgramEncoder is still used for sparse indexing. None uses NgramEncoder.
    /// Default: None.
    pub onnx_model_path: Option<String>,
    /// v0.11.0: Vitality decay configuration (per kind).
    pub vitality: VitalityConfig,
    /// v0.11.0: Hopfield network configuration.
    pub hopfield: HopfieldConfig,
    /// v0.12.0: Warmup rounds before full context matching activates.
    pub warmup_rounds: u32,
    /// v0.12.0: Active context match cosine threshold.
    pub context_match_threshold: f32,
    /// v0.12.0: Time decay half-life in hours for context match score.
    pub context_half_life_hours: f32,
    /// v0.12.0: Maximum number of active contexts.
    pub max_active_contexts: usize,
    /// v0.12.0: Early recall limit for warmup phase.
    pub early_recall_limit: usize,
    /// v0.12.2: Allow fallback to NgramEncoder when Candle is not configured.
    /// Default: false (Candle is required for production).
    pub allow_fallback_encoder: bool,
    /// v0.13.0: Default directory for per-agent brain storage.
    /// Used by the MCP server to resolve agent_id → db_path.
    pub brains_dir: Option<String>,
    /// v0.13.0: Maximum number of dormant (inactive but recallable) contexts.
    pub max_dormant_contexts: usize,
    /// v0.13.0: Context idle time (hours) before auto-dormant move.
    pub context_idle_dormant_hours: f32,
    /// v0.13.0: Threshold for reactivating a dormant context (cosine similarity).
    pub dormant_reactivate_threshold: f32,
}

impl Default for BrainConfig {
    fn default() -> Self {
        BrainConfig {
            personality: Personality::default(),
            innate_schemas: Vec::new(),
            initial_anchors: Vec::new(),
            cortex_capacity: 7,
            hippocampus_capacity: 500,
            dream_interval: 50,
            dream_max_ms: 500,
            plan_boundary_threshold: None,
            api_base_url: None,
            reranker_model_path: None,
            onnx_model_path: None,
            vitality: VitalityConfig::default(),
            hopfield: HopfieldConfig::default(),
            warmup_rounds: 5,
            context_match_threshold: 0.75,
            context_half_life_hours: 12.0,
            max_active_contexts: 5,
            early_recall_limit: 3,
            allow_fallback_encoder: false,
            brains_dir: None,
            max_dormant_contexts: 1000,
            context_idle_dormant_hours: 24.0,
            dormant_reactivate_threshold: 0.65,
        }
    }
}

// ── RecallMode (v0.9.0) ───────────────────────────────────

/// v0.9.0: Dual recall mode.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub enum RecallMode {
    /// Pure semantic retrieval: HNSW → cosine sort → return.
    /// Skips emotional_alignment and ngram_overlap main ranking.
    #[default]
    Retrieval,
    /// Associative recall: HNSW → Hopfield spread → emotional/ngram boost (×0.9-1.1).
    Associative,
}

// ── InnateSchema ──────────────────────────────────────────

#[derive(Debug, Clone)]
pub struct InnateSchema {
    pub name: String,
    pub description: String,
    pub keywords: Vec<String>,
}

// ── Shelf types (v0.9.0) ─────────────────────────────────

/// v0.9.0: Knowledge base domain type.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, serde::Serialize, serde::Deserialize)]
pub enum ShelfDomain {
    /// v0.11.0: Generic domain for externally mounted knowledge.
    Generic,
    Code,
    Doc,
    Book,
    Paper,
    Custom,
}

impl std::fmt::Display for ShelfDomain {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            ShelfDomain::Generic => write!(f, "generic"),
            ShelfDomain::Code => write!(f, "code"),
            ShelfDomain::Doc => write!(f, "doc"),
            ShelfDomain::Book => write!(f, "book"),
            ShelfDomain::Paper => write!(f, "paper"),
            ShelfDomain::Custom => write!(f, "custom"),
        }
    }
}

/// v0.9.0: Metadata for a chunk from a knowledge source.
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct ChunkMeta {
    pub source: String,       // Original file path or URL
    pub location: String,     // Location within source (e.g., line range, heading)
    pub url: Option<String>,  // URL if source is a web resource
}

/// v0.9.0: Result of a knowledge shelf search.
#[derive(Debug, Clone, serde::Serialize)]
pub struct ShelfResult {
    pub text: String,
    pub location: String,
    pub score: f32,
    pub source: String,
}

/// v0.11.0: Confidence level for Knowledge engram extraction.
#[allow(dead_code)]
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum Confidence {
    Extracted,
    Verified,
    Inferred,
    Contradicted,
}

// ── PerceptionInput ───────────────────────────────────────

#[derive(Debug, Clone)]
pub struct PerceptionInput {
    pub content: String,
    pub vector: Vec<f16>,
    pub emotional_state: crate::engram::EmotionalState,
    pub attention_anchors: Vec<String>,
    pub perceived_importance: f32,
    pub session_id: String,
    pub protection: Protection,
    pub manual_links: Vec<String>,
    pub meta: HashMap<String, serde_json::Value>,
    /// v0.8.0: Agent-optional plan ID (auto-matched by PlanGate when None).
    pub plan_id: Option<String>,
    /// v0.8.0: Agent's response for this turn (creates DialogueTurn).
    pub agent_response: Option<String>,
    /// v0.8.0: Dialogue timestamp (Unix ms, defaults to now).
    pub dialogue_timestamp: Option<i64>,
    /// v0.9.0: Knowledge source identifier (e.g. shelf_id, file path).
    pub source: Option<String>,
    /// v0.9.1: Turn-level identifier (auto-generated if empty).
    pub turn_id: String,
    /// v0.9.1: 0-based turn index within session.
    pub turn_index: u32,
    /// v0.9.1: Segment index for long text / multi-topic turns (0 = first segment).
    pub segment_index: u32,
    /// v0.9.1: Optional topic label (e.g. "jwt", "logo") for single-turn multi-topic.
    pub topic_label: Option<String>,
    /// v0.12.1: Optional knowledge tree ID for this perception.
    /// When set, the engram is automatically associated with the specified tree.
    pub tree_id: Option<String>,
}

/// v0.8.0: Return type of Brain::perceive() after plan-gating integration.
#[derive(Debug, Clone, serde::Serialize)]
pub struct PerceptionOutput {
    /// The newly created engram ID.
    pub engram_id: String,
    /// The plan this engram was assigned to.
    pub current_plan_id: String,
    /// Hint for the Agent about plan boundary status.
    pub plan_hint: crate::engram::PlanHint,
    /// Human-readable name of the current plan.
    pub plan_name: String,
    /// v0.12.0: Context ID for active context tracking.
    #[serde(default)]
    pub context_id: Option<String>,
    /// v0.12.0: Phase of memory processing (warmup, early, full).
    #[serde(default)]
    pub phase: String,
}

// ── RecallRequest ─────────────────────────────────────────

#[derive(Debug, Clone)]
pub struct RecallRequest {
    pub query: String,
    pub query_vector: Option<Vec<f16>>,
    pub session_id: String,
    pub emotional_state: crate::engram::EmotionalState,
    pub attention_anchors: Vec<String>,
    pub current_goal: Option<String>,
    pub recent_limit: usize,
    pub spread_depth: usize,
    pub spread_top_k: usize,
    /// v0.8.0: Currently active plan ID (narrows PGT search).
    pub active_plan_id: Option<String>,
    /// v0.8.0: Whether to deep-search archived dialogue turns.
    pub deep_search: bool,
    /// v0.8.0: Plan ID to deep-search within.
    pub deep_search_plan_id: Option<String>,
    /// v0.8.0: Domain filter for PGT retrieval.
    pub domain_filter: Vec<String>,
    /// v0.8.0: Expected number of results (PGT accumulates toward this target).
    pub limit: usize,
    /// v0.9.0: Recall mode (Retrieval by default).
    pub mode: RecallMode,
    /// v0.9.0: Whether to use Cross-Encoder reranking on top-k results.
    pub use_reranker: bool,
    /// v0.11.0: Optional tree path filter. None = all, tree = tree + conversation.
    pub tree: Option<String>,
    /// v0.12.1: Optional tree ID filter (via tree_ref). None = all.
    pub tree_id: Option<String>,
    /// v0.11.0: Optional kind filter. Empty = all kinds.
    pub kind_filter: Vec<EngramKind>,
    /// v0.12.0: Earliest timestamp (Unix ms) for time-based filtering.
    pub time_from: Option<i64>,
    /// v0.12.0: Latest timestamp (Unix ms) for time-based filtering.
    pub time_to: Option<i64>,
    /// v0.12.0: Whether to include knowledge engrams in recall results.
    pub attach_knowledge: bool,
    /// v0.12.0: Context ID to scope recall within.
    pub context_id: Option<String>,
}

impl Default for RecallRequest {
    fn default() -> Self {
        RecallRequest {
            query: String::new(),
            query_vector: None,
            session_id: String::new(),
            emotional_state: crate::engram::EmotionalState::default(),
            attention_anchors: Vec::new(),
            current_goal: None,
            recent_limit: 5,
            spread_depth: 3,
            spread_top_k: 10,
            active_plan_id: None,
            deep_search: false,
            deep_search_plan_id: None,
            domain_filter: Vec::new(),
            limit: 10,
            mode: RecallMode::Retrieval,
            use_reranker: false,
            tree: None,
            tree_id: None,
            kind_filter: vec![],
            time_from: None,
            time_to: None,
            attach_knowledge: true,
            context_id: None,
        }
    }
}

// ── RecallResponse ────────────────────────────────────────

#[derive(Debug, Clone)]
pub struct RecallResponse {
    pub working_memory: Vec<Engram>,
    pub associations: Vec<Engram>,
    pub schemas: Vec<Engram>,
    pub emotional_echoes: Vec<Engram>,
    pub conflicts: Vec<ConflictItem>,
    pub trace: RecallTrace,
    /// Deep-search archive results (future). Always None for now.
    pub archive_results: Option<Vec<DialogueTurn>>,
    /// v0.9.1: Per-turn hits with scores and snippets.
    pub hit_turns: Vec<TurnHit>,
    /// v0.9.1: Per-session aggregated scores.
    pub aggregated_sessions: Vec<SessionScore>,
    /// v0.11.0: Knowledge engrams in recall results.
    pub knowledge_memories: Vec<Engram>,
    /// v0.11.0: Knowledge tree contexts for returned results.
    pub tree_contexts: Vec<TreeContext>,
    /// v0.11.0: Cross-engram associations discovered by EntangleGraph.
    pub graph_associations: Vec<GraphAssociation>,
    /// v0.12.1: 三观模式上下文 — 稳定度 > 0.7 的模式描述。
    pub worldview_context: Vec<String>,
    /// v0.12.1: 认知冲突 — 与当前输入矛盾的稳定模式。
    pub cognitive_conflicts: Vec<String>,
    /// v0.13.2: Per-result relevance scores (engram_id → fused_score).
    pub scores: HashMap<String, f32>,
}

// ── ConflictItem ──────────────────────────────────────────

#[derive(Debug, Clone)]
pub struct ConflictItem {
    pub memory_a_id: String,
    pub memory_b_id: String,
    pub conflict_type: String,
}

// ── v0.9.1: Turn-level types ────────────────────────────────

/// A dialogue turn hit during recall.
#[derive(Debug, Clone, serde::Serialize)]
pub struct TurnHit {
    pub engram_id: String,
    pub turn_id: String,
    pub session_id: String,
    pub score: f32,
    pub snippet: String,
}

/// Per-session aggregation of turn hits.
#[derive(Debug, Clone, serde::Serialize)]
pub struct SessionScore {
    pub session_id: String,
    pub total_score: f32,
    pub top_turn_ids: Vec<String>,
}

// ── RecallTrace ───────────────────────────────────────────

#[derive(Debug, Clone)]
pub struct RecallTrace {
    pub latency_us: u64,
    pub gated_anchors: Vec<String>,
    pub hopfield_candidates: usize,
    pub spread_steps: usize,
    pub post_inhibition_count: usize,
    /// v0.8.0: The PGT layer that produced the results (L0/L1/L2/L3/hopfield/None).
    pub pgt_layer: Option<String>,
}

// ── ReflectionInput ───────────────────────────────────────

#[derive(Debug, Clone)]
pub struct ReflectionInput {
    pub content: String,
    pub kind: ReflectionKind,
    pub anchored_to: Vec<String>,
    pub emotional_state: crate::engram::EmotionalState,
    pub session_id: String,
}

// ── ReflectionKind ────────────────────────────────────────

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ReflectionKind {
    Pattern,
    Evaluation,
    Intention,
    Confusion,
}

impl std::fmt::Display for ReflectionKind {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            ReflectionKind::Pattern => write!(f, "pattern"),
            ReflectionKind::Evaluation => write!(f, "evaluation"),
            ReflectionKind::Intention => write!(f, "intention"),
            ReflectionKind::Confusion => write!(f, "confusion"),
        }
    }
}

// ── DreamReport ───────────────────────────────────────────

#[derive(Debug, Clone, Default)]
pub struct DreamReport {
    pub vitality_decayed: usize,
    pub archived_count: usize,
    pub forgotten_count: usize,
    pub interference_applied: usize,
    pub new_edges: usize,
    pub pruned_edges: usize,
    pub consolidated_count: usize,
    pub new_schemas: usize,
    pub schemas_dissolved: usize,
    pub conflicts_detected: usize,
    pub duration_ms: u64,
    /// v0.9.0: Number of engrams that received LLM-suggested keywords.
    pub llm_keywords_added: usize,
    /// v0.9.0: Number of contradictions confirmed by LLM.
    pub llm_contradictions: usize,
    /// v0.9.1: Number of turn clusters merged into schemas by crystallizer.
    pub turn_schemas_created: usize,
    /// v0.10.0: Number of turn-type engrams archived by piggyback detection (>30 days inactive).
    pub turns_archived: usize,
    /// v0.11.0: Number of Knowledge engrams processed in Dream.
    pub knowledge_processed: usize,
    /// v0.11.0: New cross-kind associations discovered (Knowledge<->Episode).
    pub cross_kind_new_associations: usize,
    /// v0.11.0: Number of tombstoned nodes removed from HNSW by compact.
    pub hnsw_compacted: usize,
    /// v0.12.1: Entanglement events decayed (strength < 0.1 → deleted).
    pub entanglements_decayed: usize,
    /// v0.12.1: New entanglement events created during REM phase.
    pub entanglements_created: usize,
    /// v0.12.1: New worldview patterns emerged during REM phase.
    pub worldviews_emerged: usize,
    /// v0.13.2: Contexts compressed during Dream.
    pub contexts_compressed: usize,
    /// v0.13.2: Contexts moved to dormant pool.
    pub dormant_moved: usize,
    /// v0.13.2: Engrams archived during Dream.
    pub archived: usize,
}

/// v0.13.0: Enhanced dream output with newly emerged entities.
#[derive(Debug, Clone, Default, serde::Serialize, serde::Deserialize)]
pub struct DreamOutput {
    /// Original DreamReport stats
    pub consolidated_count: usize,
    pub pruned_edges: usize,
    pub duration_ms: u64,
    pub knowledge_processed: usize,
    pub hnsw_compacted: usize,
    /// v0.13.0: Context compression stats
    pub contexts_compressed: usize,
    /// v0.13.0: Dormant pool stats
    pub dormant_moved: usize,
    pub archived: usize,
}

// ── v0.11.0: New types ───────────────────────────────────────

/// v0.11.0: Vitality decay configuration, per EngramKind.
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct VitalityConfig {
    /// Episode base decay rate (fraction reduced per dream cycle).
    pub episode_decay_rate: f32,
    /// Knowledge base decay rate (~3-5x slower than Episode).
    pub knowledge_decay_rate: f32,
    /// Vitality boost for recently-activated engrams.
    pub activation_boost: f32,
    /// Vitality floor before engram enters sleep.
    pub sleep_threshold: f32,
    /// Vitality floor before engram enters archive.
    pub archive_threshold: f32,
}

impl Default for VitalityConfig {
    fn default() -> Self {
        VitalityConfig {
            episode_decay_rate: 0.05,
            knowledge_decay_rate: 0.015,
            activation_boost: 0.1,
            sleep_threshold: 0.1,
            archive_threshold: 0.01,
        }
    }
}

/// v0.11.0: Hopfield network configuration for weighted pattern participation.
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct HopfieldConfig {
    /// Whether Knowledge engrams participate in Hopfield.
    pub include_knowledge: bool,
    /// Pattern weight multiplier for Knowledge engrams (0.5 = auxiliary).
    pub knowledge_pattern_weight: f32,
    /// Maximum number of patterns stored in Hopfield.
    pub max_patterns: usize,
}

impl Default for HopfieldConfig {
    fn default() -> Self {
        HopfieldConfig {
            include_knowledge: true,
            knowledge_pattern_weight: 0.5,
            max_patterns: 200_000,
        }
    }
}

/// v0.11.0: Result of an ADD-only store() operation.
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct StoreResult {
    pub engram_id: String,
    pub status: StoreStatus,
    pub duplicate_of: Option<String>,
}

#[derive(Debug, Clone, PartialEq, Eq, serde::Serialize, serde::Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum StoreStatus {
    Stored,
    Duplicate,
}

/// v0.11.0: Filter for forget_batch deletion.
#[allow(clippy::enum_variant_names)]
#[derive(Debug, Clone)]
pub enum ForgetFilter {
    ByTreePath(String),
    ByTurnId(String),
    ByEngramId(String),
}

/// v0.11.0: Result of mount_tree.
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct MountResult {
    pub tree_path: String,
    pub chunk_count: usize,
    pub domain: String,
    pub warnings: Vec<String>,
}

/// v0.11.0: Result of unmount_tree.
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct UnmountResult {
    pub tree_path: String,
    pub deleted_count: usize,
}

/// v0.11.0: Context information for a knowledge tree in recall results.
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct TreeContext {
    pub tree_path: String,
    pub domain: String,
    pub source_count: usize,
}

/// v0.11.0: A cross-engram association discovered by EntangleGraph.
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct GraphAssociation {
    pub source_id: String,
    pub target_id: String,
    pub kind: AssociationKind,
    pub weight: f32,
    pub description: String,
}
