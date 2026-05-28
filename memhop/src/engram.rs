//! Core data structures for the Brain memory system.

use half::f16;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;

// ── Vector dimension ──────────────────────────────────────────

pub const VECTOR_DIM: usize = 1024;

// ── Engram ────────────────────────────────────────────────────

/// A memory engram — the fundamental unit of storage in the Brain.
///
/// Each engram carries:
/// - A text payload and optional summary
/// - A dense vector (f16) for similarity matching
/// - Emotional metadata (valence, arousal)
/// - Forgetting resistance (vitality, protection)
/// - Lifecycle tracking (created_at, last_activated, activation_count)
/// - Type classification (Episode / Schema / Anchor / Reflection)
/// - Archival status
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Engram {
    pub id: String,
    pub text: String,
    pub summary: Option<String>,
    pub vector: Vec<f16>,
    pub keywords: Vec<String>,
    pub content_type: Option<String>,
    pub valence: f32,
    pub arousal: f32,
    pub vitality: f32,
    pub protection: Protection,
    pub created_at: i64,
    pub last_activated: i64,
    pub activation_count: u32,
    pub kind: EngramKind,
    pub meta: HashMap<String, serde_json::Value>,
    pub is_archived: bool,
    pub is_dormant: bool,
    /// v0.9.1: Reference to the DialogueTurn this engram belongs to.
    #[serde(default)]
    pub turn_id: Option<String>,
}

impl Engram {
    /// Create a new Episode engram with default values.
    pub fn new_episode(
        id: String,
        text: String,
        vector: Vec<f16>,
        keywords: Vec<String>,
        valence: f32,
        arousal: f32,
        now: i64,
    ) -> Self {
        Engram {
            id,
            text,
            summary: None,
            vector,
            keywords,
            content_type: None,
            valence,
            arousal,
            vitality: 1.0,
            protection: Protection::Normal,
            created_at: now,
            last_activated: now,
            activation_count: 1,
            kind: EngramKind::Episode,
            meta: HashMap::new(),
            is_archived: false,
            is_dormant: false,
            turn_id: None,
        }
    }

    /// Mark this engram as accessed (bump last_activated and activation_count).
    pub fn touch(&mut self, now: i64) {
        self.last_activated = now;
        self.activation_count = self.activation_count.saturating_add(1);
    }
}

// ── EngramKind ────────────────────────────────────────────────

/// Classification of an engram's role in the memory system.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub enum EngramKind {
    /// A specific remembered event or observation.
    Episode,
    /// An extracted pattern or category learned from multiple episodes.
    Schema,
    /// A named anchor point for scene-gated retrieval.
    Anchor,
    /// A self-reflective analysis (pattern, evaluation, intention, confusion).
    Reflection,
}

impl std::fmt::Display for EngramKind {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            EngramKind::Episode => write!(f, "episode"),
            EngramKind::Schema => write!(f, "schema"),
            EngramKind::Anchor => write!(f, "anchor"),
            EngramKind::Reflection => write!(f, "reflection"),
        }
    }
}

// ── Protection ────────────────────────────────────────────────

/// Protection level against forgetting/deletion.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub enum Protection {
    Normal,
    Protected,
    Permanent,
}

// ── Association ───────────────────────────────────────────────

/// A typed, weighted edge between two engrams.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Association {
    pub target_id: String,
    pub weight: f32,
    pub kind: AssociationKind,
    pub last_activated: i64,
}

// ── AssociationKind ───────────────────────────────────────────

/// Semantic classification of an associative link.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub enum AssociationKind {
    Semantic,
    Temporal,
    Causal,
    Emotional,
    Hierarchical,
    Contradicts,
    Manual,
}

impl std::fmt::Display for AssociationKind {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            AssociationKind::Semantic => write!(f, "semantic"),
            AssociationKind::Temporal => write!(f, "temporal"),
            AssociationKind::Causal => write!(f, "causal"),
            AssociationKind::Emotional => write!(f, "emotional"),
            AssociationKind::Hierarchical => write!(f, "hierarchical"),
            AssociationKind::Contradicts => write!(f, "contradicts"),
            AssociationKind::Manual => write!(f, "manual"),
        }
    }
}

// ── SchemaExtra ───────────────────────────────────────────────

/// Additional metadata stored for Schema-type engrams.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SchemaExtra {
    pub source_episodes: Vec<String>,
    pub centroid_vector: Vec<f16>,
    pub match_count: u32,
    pub stability: f32,
    pub internal_consistency: f32,
    pub contradiction_count: u32,
}

impl SchemaExtra {
    /// Create a new SchemaExtra from source episodes and centroid.
    pub fn new(source_episodes: Vec<String>, centroid_vector: Vec<f16>) -> Self {
        let count = source_episodes.len() as u32;
        SchemaExtra {
            source_episodes,
            centroid_vector,
            match_count: count,
            stability: 0.0,
            internal_consistency: 1.0,
            contradiction_count: 0,
        }
    }

    /// Recalculate stability using the sigmoid formula:
    /// stability = sigmoid(source_episodes) × consistency × (1 - contradiction_penalty)
    pub fn update_stability(&mut self) {
        let n = self.source_episodes.len() as f32;
        let source = 1.0 / (1.0 + (3.0 - n).exp()); // sigmoid centered at 3
        let penalty = 1.0 - (self.contradiction_count as f32 * 0.1).min(0.5);
        self.stability = source * self.internal_consistency * penalty;
        self.stability = self.stability.clamp(0.0, 1.0);
    }

    /// Whether this schema is stable enough to persist.
    pub fn is_active(&self) -> bool {
        self.stability > 0.3 && self.source_episodes.len() >= 3
    }

    /// Whether this schema should be dissolved (too few episodes or too unstable).
    pub fn should_dissolve(&self) -> bool {
        self.stability < 0.1
    }
}

// ── EmotionalState ────────────────────────────────────────────

/// A snapshot of emotional state at a point in time.
#[derive(Debug, Clone, Copy, Serialize, Deserialize)]
pub struct EmotionalState {
    /// Valence: negative to positive (-1.0..1.0). Default 0.0.
    pub valence: f32,
    /// Arousal: calm to excited (0.0..1.0). Default 0.5.
    pub arousal: f32,
}

impl EmotionalState {
    pub fn new(valence: f32, arousal: f32) -> Self {
        EmotionalState {
            valence: valence.clamp(-1.0, 1.0),
            arousal: arousal.clamp(0.0, 1.0),
        }
    }
}

impl Default for EmotionalState {
    fn default() -> Self {
        EmotionalState {
            valence: 0.0,
            arousal: 0.5,
        }
    }
}

// ── EmotionalContext ──────────────────────────────────────────

/// Emotional state maintained internally by MemHop.
/// `state` is the current short-term emotional state (updated per perceive).
/// `mood` is the slow-moving emotional baseline.
#[derive(Debug, Clone)]
pub struct EmotionalContext {
    pub state: EmotionalState,
    pub mood: EmotionalState,
}

impl EmotionalContext {
    pub fn new() -> Self {
        EmotionalContext {
            state: EmotionalState::default(),
            mood: EmotionalState::default(),
        }
    }

    /// Update emotional context with new state. Mood drifts slowly toward state.
    pub fn update(&mut self, valence: f32, arousal: f32) {
        self.state = EmotionalState::new(valence, arousal);
        // Mood drifts 10% toward new state each update
        self.mood.valence += (self.state.valence - self.mood.valence) * 0.1;
        self.mood.arousal += (self.state.arousal - self.mood.arousal) * 0.1;
    }
}

impl Default for EmotionalContext {
    fn default() -> Self {
        Self::new()
    }
}


// ── Plan-level types (v0.8.0 Plan architecture) ────────────────

#[derive(Debug, Clone, Copy, PartialEq, Eq, serde::Serialize)]
pub enum PlanHint { Continuing, NewTopicLikely, TimeoutNewPlan }

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub enum PlanLevel { SubTask = 0, Plan = 1, Version = 2, MajorVersion = 3, Domain = 4 }

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub enum PlanState { Active, Paused, Completed, Archived }

#[derive(Debug, Clone)]
pub struct PlanInfo { pub name: String, pub level: PlanLevel, pub state: PlanState, pub created_at: i64 }

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct StyleCompact { pub avg_sentence_len: f32, pub question_ratio: f32, pub exclamation_count: u32 }

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ToneMeta { pub valence: f32, pub arousal: f32, pub tone_tags: Vec<String>, pub filler_ratio: f32, pub sentence_style: StyleCompact }

/// v0.9.1: Source of a dialogue turn.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Default)]
#[serde(rename_all = "snake_case")]
pub enum TurnSource {
    #[default]
    User,
    Agent,
    System,
    External,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PlanNode { pub id: String, pub parent_id: Option<String>, pub name: String, pub level: PlanLevel, pub centroid_vector: Vec<f16>, pub dialogue_count: u32, pub compressed_summary: Option<String>, pub state: PlanState, pub created_at: i64, pub completed_at: Option<i64>, pub meta: HashMap<String, serde_json::Value> }

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct DialogueTurn { pub id: String, pub plan_id: String, pub user_input: String, pub agent_response: String, pub user_tone: ToneMeta, pub agent_tone: ToneMeta, pub timestamp: i64, pub vector: Vec<f16>, #[serde(default)] pub session_id: String, #[serde(default)] pub turn_index: u32, #[serde(default)] pub segment_count: u32, #[serde(default)] pub source: TurnSource, #[serde(default)] pub topic_label: Option<String> }

#[derive(Debug, Clone)]
pub struct DomainStats { pub plan_count: u32, pub dialogue_count: u32, pub avg_valence: f32, pub top_keywords: Vec<String> }

#[derive(Debug, Clone)]
pub struct TopicDistribution { pub domains: HashMap<String, DomainStats> }

#[derive(Debug, Clone)]
pub struct ToneAggregate { pub time_range_start: i64, pub time_range_end: i64, pub avg_valence: f32, pub avg_arousal: f32, pub valence_trend: f32, pub top_tone_tags: Vec<(String, u32)>, pub filler_ratio_trend: f32 }
