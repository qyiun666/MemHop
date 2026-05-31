//! Active context tracking for the memory system.
//!
//! v0.12.0: Provides context matching, time decay, and context lifecycle
//! management (warmup → early → full phases).

#![allow(dead_code)]

use half::f16;
use std::collections::VecDeque;

// ── Phase ──────────────────────────────────────────────────────

/// Processing phase for memory operations.
#[derive(Debug, Clone, Copy, PartialEq)]
pub enum Phase {
    /// System is still accumulating initial observations.
    Warmup,
    /// System has minimal context, early retrieval with limited budget.
    Early,
    /// Full context matching and recall is active.
    Full,
}

impl Phase {
    /// Returns `true` if this phase allows full recall.
    pub fn is_full(&self) -> bool {
        matches!(self, Phase::Full)
    }

    /// Returns `true` if this phase allows at least early recall.
    pub fn can_recall(&self) -> bool {
        !matches!(self, Phase::Warmup)
    }
}

impl std::fmt::Display for Phase {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Phase::Warmup => write!(f, "warmup"),
            Phase::Early => write!(f, "early"),
            Phase::Full => write!(f, "full"),
        }
    }
}

// ── Cosine similarity helper ───────────────────────────────────

/// Compute cosine similarity between an f32 query and an f16 centroid.
fn cosine_similarity(query: &[f32], centroid: &[f16]) -> f32 {
    let len = query.len().min(centroid.len());
    if len == 0 {
        return 0.0;
    }
    let mut dot = 0.0f32;
    let mut norm_q = 0.0f32;
    let mut norm_c = 0.0f32;
    for i in 0..len {
        let q = query[i];
        let c = centroid[i].to_f32();
        dot += q * c;
        norm_q += q * q;
        norm_c += c * c;
    }
    let denom = norm_q.sqrt() * norm_c.sqrt();
    if denom < 1e-10 {
        0.0
    } else {
        dot / denom
    }
}

// ── ContextSnapshot ────────────────────────────────────────────

/// A snapshot of an active memory context.
#[derive(Debug, Clone)]
pub struct ContextSnapshot {
    /// Unique context identifier.
    pub id: String,
    /// Optional tree ID this context belongs to.
    pub tree_id: Option<String>,
    /// Current plan ID for this context.
    pub plan_id: String,
    /// Centroid vector (f16) representing the context's semantic center.
    pub centroid: Vec<f16>,
    /// Human-readable summary of this context.
    pub summary: String,
    /// Unix timestamp (ms) when this context was created.
    pub created_at: i64,
    /// Unix timestamp (ms) of the last hit.
    pub last_active: i64,
    /// Number of times this context has been matched.
    pub hit_count: u32,
    /// Consecutive misses (no match).
    pub miss_streak: u32,
    /// Dialogue turn indices associated with this context.
    pub turn_indices: Vec<u32>,
    /// Plan IDs that have been completed in this context.
    pub completed_plan_ids: Vec<String>,
}

impl ContextSnapshot {
    /// Create a new `ContextSnapshot`.
    pub fn new(id: String, plan_id: String, centroid: Vec<f16>, now: i64) -> Self {
        ContextSnapshot {
            id,
            tree_id: None,
            plan_id,
            centroid,
            summary: String::new(),
            created_at: now,
            last_active: now,
            hit_count: 1,
            miss_streak: 0,
            turn_indices: Vec::new(),
            completed_plan_ids: Vec::new(),
        }
    }

    /// Compute a match score combining cosine similarity and time decay.
    ///
    /// Time decay follows an exponential decay with configurable half-life.
    pub fn match_score(&self, query: &[f32], half_life_hours: f32, now: i64) -> f32 {
        let sim = cosine_similarity(query, &self.centroid);
        let dt_ms = (now - self.last_active).max(0) as f32;
        let half_life_ms = half_life_hours * 3600.0 * 1000.0;
        if half_life_ms < 1e-6 {
            return sim;
        }
        let time_factor = (-dt_ms / half_life_ms).exp();
        sim * time_factor
    }

    /// Record a hit: update centroid, bump last_active, increment hit_count.
    pub fn on_hit(&mut self, query: &[f32], now: i64) {
        self.update_centroid(query);
        self.last_active = now;
        self.hit_count = self.hit_count.saturating_add(1);
        self.miss_streak = 0;
    }

    /// Record a miss: increment miss_streak.
    pub fn on_miss(&mut self) {
        self.miss_streak = self.miss_streak.saturating_add(1);
    }

    /// Incorporate a new query vector into the centroid via incremental average.
    fn update_centroid(&mut self, query: &[f32]) {
        let n = self.hit_count as f32;
        let alpha = 1.0 / (n + 1.0); // weight of new query
        let beta = n / (n + 1.0); // weight of existing centroid
        let len = self.centroid.len().min(query.len());
        for (c, &q) in self.centroid.iter_mut().zip(query.iter()).take(len) {
            let old = c.to_f32();
            let new_val = old * beta + q * alpha;
            *c = f16::from_f32(new_val);
        }
    }

    /// Convert centroid to f32 vector.
    fn centroid_f32(&self) -> Vec<f32> {
        self.centroid.iter().map(|&v| v.to_f32()).collect()
    }
}

// ── ActiveContextSet ───────────────────────────────────────────

/// A bounded set of active memory contexts with matching and eviction.
pub struct ActiveContextSet {
    contexts: VecDeque<ContextSnapshot>,
    max_contexts: usize,
    match_threshold: f32,
    half_life_hours: f32,
}

impl ActiveContextSet {
    /// Create a new `ActiveContextSet`.
    pub fn new(max_contexts: usize, match_threshold: f32, half_life_hours: f32) -> Self {
        ActiveContextSet {
            contexts: VecDeque::new(),
            max_contexts,
            match_threshold,
            half_life_hours,
        }
    }

    /// Find the best-matching context for the given query vector.
    ///
    /// Returns `None` if no context exceeds the match threshold.
    pub fn match_context(&mut self, query: &[f32], now: i64) -> Option<&mut ContextSnapshot> {
        if self.contexts.is_empty() {
            return None;
        }

        // Find the best match index
        let mut best_idx = None;
        let mut best_score = self.match_threshold;

        for (i, ctx) in self.contexts.iter().enumerate() {
            let score = ctx.match_score(query, self.half_life_hours, now);
            if score > best_score {
                best_score = score;
                best_idx = Some(i);
            }
        }

        if let Some(idx) = best_idx {
            let ctx = &mut self.contexts[idx];
            ctx.on_hit(query, now);
            Some(ctx)
        } else {
            // Record miss on all contexts
            for ctx in &mut self.contexts {
                ctx.on_miss();
            }
            None
        }
    }

    /// Create a new context and add it to the set.
    ///
    /// If the set is full, the stalest context is evicted first.
    pub fn create(
        &mut self,
        tree_id: Option<String>,
        plan_id: String,
        centroid: Vec<f16>,
        now: i64,
    ) -> &mut ContextSnapshot {
        // Evict if full
        if self.contexts.len() >= self.max_contexts {
            self.evict_stale();
        }

        let id = format!("ctx_{}", now);
        let mut snapshot = ContextSnapshot::new(id, plan_id, centroid, now);
        snapshot.tree_id = tree_id;

        self.contexts.push_back(snapshot);
        self.contexts.back_mut().unwrap()
    }

    /// Get a context by ID (immutable).
    pub fn get(&self, id: &str) -> Option<&ContextSnapshot> {
        self.contexts.iter().find(|c| c.id == id)
    }

    /// Get a context by ID (mutable).
    pub fn get_mut(&mut self, id: &str) -> Option<&mut ContextSnapshot> {
        self.contexts.iter_mut().find(|c| c.id == id)
    }

    /// Evict stale contexts (miss_streak >= 5 or oldest when over capacity).
    pub fn evict_stale(&mut self) {
        // First pass: remove contexts with high miss_streak
        self.contexts.retain(|c| c.miss_streak < 5);

        // If still over capacity, remove the oldest (front of VecDeque)
        while self.contexts.len() >= self.max_contexts {
            self.contexts.pop_front();
        }
    }

    /// Immutable reference to all contexts.
    pub fn contexts(&self) -> &VecDeque<ContextSnapshot> {
        &self.contexts
    }
}
