// v0.6.2: Domain Tree auto-routing via fingerprint/session matching.
// Entire module is reserved for the auto-routing infrastructure, not wired in v0.6.0.
#![allow(dead_code)]

//! Scene-gated recall — automatic context-aware candidate filtering.
//!
//! Three-layer scene detection:
//!   Layer 1: Session fingerprint matching (< 0.5ms)
//!   Layer 2: Knowledge tree path prediction (< 2ms)
//!   Layer 3: Implicit scene anchoring (miss tracking)
//!
//! Automatically narrows the candidate set during recall without requiring
//! an explicit scope parameter from the caller.

use half::f16;
use std::collections::HashMap;

use crate::storage::{LmdbStorage, StorageError};
use crate::meta_index::MetaIndex;

// ── Utility: vector ops on f16/f32 ──────────────────────────

fn dot_f16_f32(a: &[f16], b: &[f32]) -> f32 {
    let mut sum = 0.0f32;
    for i in 0..a.len() {
        sum += a[i].to_f32() * b[i];
    }
    sum
}

fn l2_norm_f16(v: &[f16]) -> f32 {
    v.iter().map(|x| x.to_f32().powi(2)).sum::<f32>().sqrt()
}

fn l2_normalize_f16(v: &[f16]) -> Vec<f16> {
    let norm = l2_norm_f16(v);
    if norm == 0.0 {
        return v.to_vec();
    }
    v.iter().map(|x| f16::from_f32(x.to_f32() / norm)).collect()
}

fn cosine_sim_f16_f32(a: &[f16], b: &[f32]) -> f32 {
    if a.is_empty() || b.is_empty() {
        return 0.0;
    }
    let dot = dot_f16_f32(a, b);
    let norm_a = l2_norm_f16(a);
    let norm_b: f32 = b.iter().map(|x| x.powi(2)).sum::<f32>().sqrt();
    if norm_a == 0.0 || norm_b == 0.0 {
        return 0.0;
    }
    dot / (norm_a * norm_b)
}

fn l2_normalize_f32_inplace(v: &mut [f32]) {
    let norm: f32 = v.iter().map(|x| x.powi(2)).sum::<f32>().sqrt();
    if norm > 0.0 {
        for x in v.iter_mut() {
            *x /= norm;
        }
    }
}

fn f16_to_f32(v: &[f16]) -> Vec<f32> {
    v.iter().map(|x| x.to_f32()).collect()
}

// ── ActiveScene ────────────────────────────────────────────

/// Implicit scene anchoring state (Layer 3).
#[derive(Debug, Clone)]
pub struct ActiveScene {
    pub session_id: Option<String>,
    pub domain: Option<String>,
    pub tree_root: Option<String>,
    pub confidence: f32,
    pub anchored_at: i64,       // millis timestamp
    pub miss_count: u8,         // consecutive misses, ≥ 3 clears anchor
}

// ── SceneState ─────────────────────────────────────────────

/// Scene gating state — manages fingerprints and active scene.
pub struct SceneState {
    /// Session_id → averaged pattern (f16)
    pub session_fingerprints: HashMap<String, Vec<f16>>,
    /// Session_id → pattern count (for running average)
    pub session_counts: HashMap<String, usize>,
    /// Knowledge tree node_id → averaged pattern (f16)
    pub node_fingerprints: HashMap<String, Vec<f16>>,
    /// Currently anchored scene (Layer 3)
    pub active_scene: Option<ActiveScene>,
    /// Whether gating is enabled (default true)
    pub gating_enabled: bool,
    /// Cosine similarity threshold for fingerprint matching (default 0.6)
    pub gating_threshold: f32,
    /// Rolling average of recent turn vectors for context-aware matching
    pub recent_turn_summary: Option<Vec<f16>>,
    /// How many turns the rolling summary tracks (default 5)
    pub recent_turn_window: usize,
    /// Current count of turns in the rolling summary
    pub recent_turn_count: usize,
}

impl SceneState {
    pub fn new() -> Self {
        SceneState {
            session_fingerprints: HashMap::new(),
            session_counts: HashMap::new(),
            node_fingerprints: HashMap::new(),
            active_scene: None,
            gating_enabled: true,
            gating_threshold: 0.6,
            recent_turn_summary: None,
            recent_turn_window: 5,
            recent_turn_count: 0,
        }
    }

    /// Update running average fingerprint for a session.
    /// Called from `remember()` after storing a new memory.
    pub fn update_session_fingerprint(&mut self, session_id: &str, pattern: &[f16]) {
        let count = self.session_counts.get(session_id).copied().unwrap_or(0);
        let new_count = count + 1;

        let avg = if let Some(existing) = self.session_fingerprints.get(session_id) {
            // Running average: avg = (avg * count + pattern) / (count + 1)
            let dim = pattern.len();
            let mut new_avg = Vec::with_capacity(dim);
            for i in 0..dim {
                let val = (existing[i].to_f32() * count as f32 + pattern[i].to_f32())
                    / new_count as f32;
                new_avg.push(f16::from_f32(val));
            }
            l2_normalize_f16(&new_avg)
        } else {
            // First pattern in this session
            l2_normalize_f16(pattern)
        };

        self.session_fingerprints.insert(session_id.to_string(), avg);
        self.session_counts.insert(session_id.to_string(), new_count);
    }

    /// Update running average fingerprint for a knowledge tree node.
    /// Called from `remember()` when the memory has a `parent` field.
    pub fn update_node_fingerprint(&mut self, node_id: &str, pattern: &[f16]) {
        // Use simple first-pattern or replace strategy for nodes
        // (node fingerprints are less frequently updated than sessions)
        if !self.node_fingerprints.contains_key(node_id) {
            self.node_fingerprints
                .insert(node_id.to_string(), l2_normalize_f16(pattern));
        } else {
            // Running average with rolling window (cap at 10 for nodes)
            let existing = &self.node_fingerprints[node_id];
            let dim = pattern.len();
            let mut new_avg = Vec::with_capacity(dim);
            for i in 0..dim {
                let val = (existing[i].to_f32() * 9.0 + pattern[i].to_f32()) / 10.0;
                new_avg.push(f16::from_f32(val));
            }
            self.node_fingerprints
                .insert(node_id.to_string(), l2_normalize_f16(&new_avg));
        }
    }

    /// Update the rolling recent-turn summary with a new turn's pattern.
    /// Uses a running average: new = α * new_turn + (1-α) * old_summary
    /// where α = min(1.0, 1.0 / recent_turn_window).
    pub fn update_recent_turns(&mut self, pattern: &[f16]) {
        let alpha = 1.0f32 / self.recent_turn_window.max(1) as f32;
        let normalized = l2_normalize_f16(pattern);

        match self.recent_turn_summary {
            Some(ref existing) => {
                let dim = existing.len().min(normalized.len());
                let mut merged = Vec::with_capacity(dim);
                for i in 0..dim {
                    let val = normalized[i].to_f32() * alpha + existing[i].to_f32() * (1.0 - alpha);
                    merged.push(f16::from_f32(val));
                }
                self.recent_turn_summary = Some(l2_normalize_f16(&merged));
            }
            None => {
                self.recent_turn_summary = Some(normalized);
            }
        }
        self.recent_turn_count = self.recent_turn_count.saturating_add(1);
    }

    /// Layer 1: Match query against all session fingerprints.
    /// Returns the session_id with the highest cosine similarity above threshold.
    pub fn match_session_fingerprint(&self, query: &[f32]) -> Option<String> {
        if !self.gating_enabled || self.session_fingerprints.is_empty() {
            return None;
        }

        let context_f32 = self.recent_turn_summary.as_ref().map(|v| f16_to_f32(v));

        let mut best_id: Option<String> = None;
        let mut best_sim = 0.0f32;

        for (sid, fp) in &self.session_fingerprints {
            let query_sim = cosine_sim_f16_f32(fp, query);
            let combined = match &context_f32 {
                Some(ctx) => {
                    // Weighted average: 60% query, 40% recent context
                    let ctx_sim = cosine_sim_f16_f32(fp, ctx);
                    query_sim * 0.6 + ctx_sim * 0.4
                }
                None => query_sim,
            };
            if combined > best_sim {
                best_sim = combined;
                best_id = Some(sid.clone());
            }
        }

        match best_id {
            Some(id) if best_sim >= self.gating_threshold => Some(id),
            _ => None,
        }
    }

    /// Layer 2: Predict the most likely knowledge tree path from the query.
    /// Returns the node_id with the highest cosine similarity above threshold.
    pub fn predict_tree_path(&self, query: &[f32]) -> Option<String> {
        if !self.gating_enabled || self.node_fingerprints.is_empty() {
            return None;
        }

        let mut best_id: Option<String> = None;
        let mut best_sim = 0.0f32;

        for (nid, fp) in &self.node_fingerprints {
            let sim = cosine_sim_f16_f32(fp, query);
            if sim > best_sim {
                best_sim = sim;
                best_id = Some(nid.clone());
            }
        }

        match best_id {
            Some(id) if best_sim >= self.gating_threshold => Some(id),
            _ => None,
        }
    }

    /// Layer 3: Anchor the current scene to a specific session/context.
    pub fn anchor_scene(&mut self, session_id: &str, confidence: f64, now_ms: i64) {
        self.active_scene = Some(ActiveScene {
            session_id: Some(session_id.to_string()),
            domain: None,
            tree_root: None,
            confidence: confidence as f32,
            anchored_at: now_ms,
            miss_count: 0,
        });
    }

    /// Clear the active scene anchor.
    pub fn reset_scene(&mut self) {
        self.active_scene = None;
    }

    /// Increment miss counter. Returns true if anchor should be cleared (≥3 misses).
    pub fn record_miss(&mut self) -> bool {
        if let Some(ref mut scene) = self.active_scene {
            scene.miss_count = scene.miss_count.saturating_add(1);
            scene.miss_count >= 3
        } else {
            false
        }
    }

    /// Rebuild all fingerprints from stored data.
    /// Called once during engine `open()` to initialize scene state.
    pub fn rebuild_fingerprints(
        &mut self,
        storage: &LmdbStorage,
        meta_index: &MetaIndex,
    ) -> Result<(), StorageError> {
        let dim = 1024; // VECTOR_DIM

        // Rebuild session fingerprints
        for session_id in meta_index.all_session_ids() {
            let ids = match meta_index.session_memory_ids(session_id) {
                Some(ids) => ids,
                None => continue,
            };

            if ids.is_empty() {
                continue;
            }

            // Accumulate patterns for this session
            let mut sum_vec = vec![0.0f32; dim];
            let mut count: usize = 0;

            for mem_id in ids {
                if let Some(pattern) = storage.get_pattern(mem_id)? {
                    for i in 0..dim {
                        sum_vec[i] += if i < pattern.len() {
                            pattern[i].to_f32()
                        } else {
                            0.0
                        };
                    }
                    count += 1;
                }
            }

            if count > 0 {
                for v in sum_vec.iter_mut() {
                    *v /= count as f32;
                }
                l2_normalize_f32_inplace(&mut sum_vec);
                let fp: Vec<f16> = sum_vec.iter().map(|&x| f16::from_f32(x)).collect();
                self.session_fingerprints.insert(session_id.clone(), fp);
                self.session_counts.insert(session_id.clone(), count);
            }
        }

        // Note: node_fingerprints are not rebuilt from scratch — they are
        // populated incrementally via remember(). On startup they will be
        // empty and Layer 2 will simply return None (graceful degradation).
        // Full knowledge tree fingerprint rebuild would require scanning
        // all memories for parent references, which adds startup latency.

        self.active_scene = None;
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use half::f16;

    fn make_f16_vec(values: &[f32]) -> Vec<f16> {
        values.iter().map(|&x| f16::from_f32(x)).collect()
    }

    fn make_f32_vec(values: &[f32]) -> Vec<f32> {
        values.to_vec()
    }

    #[test]
    fn test_update_session_fingerprint() {
        let mut state = SceneState::new();

        let p1 = make_f16_vec(&[1.0, 0.0, 0.0]);
        let p2 = make_f16_vec(&[0.0, 1.0, 0.0]);

        state.update_session_fingerprint("s1", &p1);
        assert_eq!(state.session_fingerprints.len(), 1);
        assert_eq!(*state.session_counts.get("s1").unwrap(), 1);

        state.update_session_fingerprint("s1", &p2);
        assert_eq!(*state.session_counts.get("s1").unwrap(), 2);
    }

    #[test]
    fn test_match_session_fingerprint() {
        let mut state = SceneState::new();
        state.gating_threshold = 0.5;

        let p1 = make_f16_vec(&[1.0, 0.0, 0.0]);
        state.update_session_fingerprint("s1", &p1);

        // Query close to the fingerprint
        let q_close = make_f32_vec(&[0.9, 0.1, 0.0]);
        let matched = state.match_session_fingerprint(&q_close);
        assert_eq!(matched, Some("s1".to_string()));

        // Query distant from the fingerprint
        let q_far = make_f32_vec(&[0.0, 0.0, 1.0]);
        let matched = state.match_session_fingerprint(&q_far);
        assert_eq!(matched, None);
    }

    #[test]
    fn test_gating_disabled() {
        let mut state = SceneState::new();
        state.gating_enabled = false;

        let p = make_f16_vec(&[1.0, 0.0, 0.0]);
        state.update_session_fingerprint("s1", &p);

        let q = make_f32_vec(&[1.0, 0.0, 0.0]);
        assert!(state.match_session_fingerprint(&q).is_none());
    }

    #[test]
    fn test_reset_scene() {
        let mut state = SceneState::new();
        state.anchor_scene("s1", 0.9, 1000);
        assert!(state.active_scene.is_some());

        state.reset_scene();
        assert!(state.active_scene.is_none());
    }

    #[test]
    fn test_record_miss() {
        let mut state = SceneState::new();
        state.anchor_scene("s1", 0.9, 1000);

        assert!(!state.record_miss()); // miss 1
        assert!(!state.record_miss()); // miss 2
        assert!(state.record_miss());  // miss 3 → clear
    }
}
