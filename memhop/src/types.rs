//! Core data types for MemHop — pure Rust.
//!
//! This module serves dual purpose:
//! 1. Backward-compatible types for the old MemHop engine
//! 2. Public API types for the new Brain architecture (v0.7.3+)

use std::collections::HashMap;

use half::f16;

use crate::engram::{Engram, DialogueTurn};
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
        }
    }
}

// ── RecallMode (v0.9.0) ───────────────────────────────────

/// v0.9.0: Dual recall mode.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum RecallMode {
    /// Pure semantic retrieval: HNSW → cosine sort → return.
    /// Skips emotional_alignment and ngram_overlap main ranking.
    Retrieval,
    /// Associative recall: HNSW → Hopfield spread → emotional/ngram boost (×0.9-1.1).
    Associative,
}

impl Default for RecallMode {
    fn default() -> Self {
        RecallMode::Retrieval
    }
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
    Code,
    Doc,
    Book,
    Paper,
    Custom,
}

impl std::fmt::Display for ShelfDomain {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
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
}
