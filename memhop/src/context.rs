//! Active context tracking for the memory system.
//!
//! v0.12.0: Provides context matching, time decay, and context lifecycle
//! management (warmup → early → full phases).
//! v0.13.0: DormantContextPool for three-stage context lifecycle
//! (active → dormant → archive).

#![allow(dead_code)]

use half::f16;
use serde::{Deserialize, Serialize};
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
pub fn cosine_similarity(query: &[f32], centroid: &[f16]) -> f32 {
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

/// Cosine similarity between two f16 vectors.
pub fn cosine_similarity_f16(a: &[f16], b: &[f16]) -> f32 {
    let len = a.len().min(b.len());
    if len == 0 {
        return 0.0;
    }
    let mut dot = 0.0f32;
    let mut norm_a = 0.0f32;
    let mut norm_b = 0.0f32;
    for i in 0..len {
        let av = a[i].to_f32();
        let bv = b[i].to_f32();
        dot += av * bv;
        norm_a += av * av;
        norm_b += bv * bv;
    }
    let denom = norm_a.sqrt() * norm_b.sqrt();
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
    /// v0.13.0: Cumulative dialogue turns in this context.
    pub turn_count: u32,
    /// v0.13.0: Heuristic compressed summary text.
    pub compressed_summary: Option<String>,
    /// v0.13.0: Auto-created knowledge tree ID for this context.
    pub auto_tree_id: Option<String>,
    /// v0.13.0: Timestamp of last compression (Unix ms).
    pub last_compressed_at: i64,
    /// v0.13.0: Related knowledge tree IDs with association strength.
    pub related_tree_ids: Vec<(String, f32)>,
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
            turn_count: 0,
            compressed_summary: None,
            auto_tree_id: None,
            last_compressed_at: 0,
            related_tree_ids: Vec::new(),
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

    /// v0.13.0: Check if context should be moved to dormant pool.
    pub fn should_dormant(&self, idle_hours: f32, now: i64) -> bool {
        let dt_ms = (now - self.last_active).max(0) as f32;
        let idle_ms = idle_hours * 3600.0 * 1000.0;
        dt_ms > idle_ms || self.miss_streak >= 5
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

    /// v0.13.0: Add or update a relationship to a knowledge tree.
    pub fn add_tree_relation(&mut self, tree_id: &str, strength: f32) {
        if let Some(pos) = self.related_tree_ids.iter().position(|(id, _)| id == tree_id) {
            self.related_tree_ids[pos].1 = (self.related_tree_ids[pos].1 + strength).min(1.0);
        } else {
            self.related_tree_ids.push((tree_id.to_string(), strength));
        }
    }
}

// ── DormantContext ──────────────────────────────────────────

/// v0.13.0: Persistent state for a dormant context.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct DormantContext {
    pub id: String,
    pub summary: String,
    pub centroid: Vec<f16>,
    pub tree_id: Option<String>,
    pub related_tree_ids: Vec<(String, f32)>,
    pub created_at: i64,
    pub last_active: i64,
    pub hit_count: u32,
    pub turn_count: u32,
    pub compressed_summary: Option<String>,
    pub auto_tree_id: Option<String>,
}

impl DormantContext {
    pub fn from_snapshot(s: &ContextSnapshot) -> Self {
        DormantContext {
            id: s.id.clone(),
            summary: s.summary.clone(),
            centroid: s.centroid.clone(),
            tree_id: s.tree_id.clone(),
            related_tree_ids: s.related_tree_ids.clone(),
            created_at: s.created_at,
            last_active: s.last_active,
            hit_count: s.hit_count,
            turn_count: s.turn_count,
            compressed_summary: s.compressed_summary.clone(),
            auto_tree_id: s.auto_tree_id.clone(),
        }
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

    /// v0.13.0: Evict stale contexts. Returns the evicted context for dormant storage.
    /// Returns None when nothing was evicted.
    pub fn evict_stale(&mut self) -> Option<ContextSnapshot> {
        // First pass: remove contexts with high miss_streak
        let evicted = self
            .contexts
            .iter()
            .position(|c| c.miss_streak >= 5)
            .and_then(|idx| self.contexts.remove(idx));
        if evicted.is_some() {
            return evicted;
        }
        // If still over capacity, remove the oldest (front of VecDeque)
        if self.contexts.len() >= self.max_contexts {
            return self.contexts.pop_front();
        }
        None
    }

    /// v0.13.0: Reactivate a dormant context back into the active set.
    /// The reactivated context replaces the stalest active context if full.
    pub fn reactivate(&mut self, dormant: DormantContext, now: i64) -> String {
        if self.contexts.len() >= self.max_contexts {
            self.contexts.pop_front();
        }
        let mut snapshot = ContextSnapshot::new(
            dormant.id.clone(),
            format!("plan_{}", now),
            dormant.centroid.clone(),
            now,
        );
        snapshot.summary = dormant.summary;
        snapshot.tree_id = dormant.tree_id;
        snapshot.related_tree_ids = dormant.related_tree_ids;
        snapshot.turn_count = dormant.turn_count;
        snapshot.compressed_summary = dormant.compressed_summary;
        snapshot.auto_tree_id = dormant.auto_tree_id;
        snapshot.hit_count = dormant.hit_count;
        let id = snapshot.id.clone();
        self.contexts.push_back(snapshot);
        id
    }

    /// v0.13.0: Increment turn count for the matched context.
    pub fn increment_turn_count(&mut self, ctx_id: &str) {
        if let Some(ctx) = self.contexts.iter_mut().find(|c| c.id == ctx_id) {
            ctx.turn_count = ctx.turn_count.saturating_add(1);
        }
    }

    /// Immutable reference to all contexts.
    pub fn contexts(&self) -> &VecDeque<ContextSnapshot> {
        &self.contexts
    }
}

// ── DormantContextPool ─────────────────────────────────────

/// v0.13.0: Dormant context pool — in-memory index for fast reactivation matching.
pub struct DormantContextPool {
    /// In-memory cache of dormant context centroids for fast matching.
    centroids: Vec<(String, Vec<f16>)>,
    /// Maximum number of dormant contexts (beyond this → archive).
    max_dormant: usize,
    /// Dormant contexts older than this (hours) → auto archive.
    #[allow(dead_code)]
    archive_after_hours: f32,
}

impl DormantContextPool {
    pub fn new(max_dormant: usize, archive_after_hours: f32) -> Self {
        DormantContextPool {
            centroids: Vec::new(),
            max_dormant,
            archive_after_hours,
        }
    }

    /// Add a context snapshot to the dormant pool in-memory index.
    /// The caller is responsible for persisting to LMDB.
    pub fn add_from_snapshot(&mut self, snapshot: &ContextSnapshot) {
        let centroid = snapshot.centroid.clone();
        self.centroids
            .push((snapshot.id.clone(), centroid));
        // Keep centroids list bounded
        if self.centroids.len() > self.max_dormant * 2 {
            self.centroids.truncate(self.max_dormant);
        }
    }

    /// Search dormant pool for the best matching context, remove it from the
    /// in-memory index, and reactivate it in the active set.
    /// Returns the reactivated context's ID, or None.
    pub fn search_and_reactivate(
        &mut self,
        query: &[f32],
        threshold: f32,
        active_set: &mut ActiveContextSet,
        now: i64,
    ) -> Option<String> {
        if self.centroids.is_empty() {
            return None;
        }
        // Linear scan over dormant centroids (capped at 1000, acceptable)
        let mut best_idx = None;
        let mut best_score = threshold;
        for (i, (_id, centroid)) in self.centroids.iter().enumerate() {
            let score = cosine_similarity(query, centroid);
            if score > best_score {
                best_score = score;
                best_idx = Some(i);
            }
        }
        if let Some(idx) = best_idx {
            let (id, _) = self.centroids.remove(idx);
            // Note: the full DormantContext data is stored in LMDB.
            // Here we create a minimal entry; the full data is reloaded by Brain.
            let dormant = DormantContext {
                id: id.clone(),
                summary: String::new(),
                centroid: Vec::new(),
                tree_id: None,
                related_tree_ids: Vec::new(),
                created_at: now,
                last_active: now,
                hit_count: 0,
                turn_count: 0,
                compressed_summary: None,
                auto_tree_id: None,
            };
            let ctx_id = active_set.reactivate(dormant, now);
            Some(ctx_id)
        } else {
            None
        }
    }

    /// Load dormant contexts from LMDB storage (called during Brain::open).
    pub fn load_from_storage(&mut self, dcs: &[DormantContext]) {
        self.centroids = dcs
            .iter()
            .map(|dc| (dc.id.clone(), dc.centroid.clone()))
            .collect();
    }

    /// Number of dormant contexts in the in-memory index.
    pub fn len(&self) -> usize {
        self.centroids.len()
    }

    /// Returns true if the dormant pool is empty.
    pub fn is_empty(&self) -> bool {
        self.centroids.is_empty()
    }
}
