//! Core data types for MemHop — pure Rust.
//!
//! This module serves dual purpose:
//! 1. Backward-compatible types for the old MemHop engine
//! 2. Public API types for the new Brain architecture (v0.7.3+)

use std::collections::HashMap;

use half::f16;

use crate::engram::Engram;
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
        }
    }
}

// ── InnateSchema ──────────────────────────────────────────

#[derive(Debug, Clone)]
pub struct InnateSchema {
    pub name: String,
    pub description: String,
    pub keywords: Vec<String>,
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
}

// ── ConflictItem ──────────────────────────────────────────

#[derive(Debug, Clone)]
pub struct ConflictItem {
    pub memory_a_id: String,
    pub memory_b_id: String,
    pub conflict_type: String,
}

// ── RecallTrace ───────────────────────────────────────────

#[derive(Debug, Clone)]
pub struct RecallTrace {
    pub latency_us: u64,
    pub gated_anchors: Vec<String>,
    pub hopfield_candidates: usize,
    pub spread_steps: usize,
    pub post_inhibition_count: usize,
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
}
