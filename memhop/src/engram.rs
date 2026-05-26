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
