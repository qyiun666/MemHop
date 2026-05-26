//! Plan boundary detection engine — rule-based, no LLM.
//!
//! `PlanGate` detects topic shifts by fusing four signals:
//! semantic drift, emotional rupture, anchor change, and time gap.
//! It accumulates scores over multiple rounds before deciding whether
//! a new plan is likely needed.
//!
//! Also includes the in-memory `PlanIndex` (shared with `storage.rs`).

use std::collections::{HashMap, VecDeque};

use crate::engram::{PlanHint, PlanInfo, ToneMeta};

// ── PlanIndex (in-memory auxiliary index) ───────────────────────

/// In-memory auxiliary index for fast plan lookups without full LMDB scans.
///
/// Maintained by `LmdbStorage`; referenced by `PlanGate::match_to_plan()`.
/// See spec §2.8 and arch §4.2.
#[derive(Debug)]
pub struct PlanIndex {
    /// plan_id → engram_id list for all engrams in that plan
    pub entries: HashMap<String, Vec<String>>,
    /// plan_id → lightweight plan metadata
    pub plan_info: HashMap<String, PlanInfo>,
    /// Currently active plan ID
    pub active_plan_id: Option<String>,
    /// plan_id → child plan_id list (tree structure)
    pub children: HashMap<String, Vec<String>>,
    /// plan_id → centroid vector (sliding average, f16)
    pub centroids: HashMap<String, Vec<half::f16>>,
}

impl PlanIndex {
    /// Create an empty PlanIndex.
    pub fn new() -> Self {
        PlanIndex {
            entries: HashMap::new(),
            plan_info: HashMap::new(),
            active_plan_id: None,
            children: HashMap::new(),
            centroids: HashMap::new(),
        }
    }

    /// Get candidate engram IDs for a plan.
    pub fn candidates(&self, plan_id: Option<&str>) -> Vec<String> {
        match plan_id {
            Some(pid) => self.entries.get(pid).cloned().unwrap_or_default(),
            None => self.entries.values().flatten().cloned().collect(),
        }
    }

    /// Add an engram ID to a plan's entry list.
    pub fn add_engram(&mut self, plan_id: &str, engram_id: &str) {
        self.entries
            .entry(plan_id.to_string())
            .or_default()
            .push(engram_id.to_string());
    }

    /// Update the centroid vector for a plan using a simple moving average.
    pub fn update_centroid(&mut self, plan_id: &str, new_vec: &[f32]) {
        let new_f16: Vec<half::f16> = new_vec.iter().map(|&x| half::f16::from_f32(x)).collect();
        self.centroids
            .entry(plan_id.to_string())
            .and_modify(|existing| {
                for i in 0..existing.len().min(new_f16.len()) {
                    let avg = (existing[i].to_f32() + new_f16[i].to_f32()) / 2.0;
                    existing[i] = half::f16::from_f32(avg);
                }
            })
            .or_insert(new_f16);
    }

    /// Rebuild the index from stored PlanNodes.
    pub fn rebuild(&mut self, plans: &[crate::engram::PlanNode]) {
        for plan in plans {
            self.plan_info.insert(plan.id.clone(), crate::engram::PlanInfo {
                name: plan.name.clone(),
                level: plan.level.clone(),
                state: plan.state.clone(),
                created_at: plan.created_at,
            });
            if !plan.centroid_vector.is_empty() {
                self.centroids.insert(plan.id.clone(), plan.centroid_vector.clone());
            }
        }
    }
}

impl Default for PlanIndex {
    fn default() -> Self {
        Self::new()
    }
}

// ── PlanContext ─────────────────────────────────────────────────

/// Contextual data from the current plan for boundary detection.
pub struct PlanContext<'a> {
    pub centroid: Option<&'a [f32]>,
    pub avg_tone: Option<&'a ToneMeta>,
    pub anchors: &'a [String],
}

// ── PlanGate ────────────────────────────────────────────────────

pub struct PlanGate {
    pub boundary_threshold: f32,
    pub confirm_rounds: u32,
    pub timeout_hours: u32,
    score_history: VecDeque<f32>,
    last_activity: i64,
}

impl PlanGate {
    pub fn new(boundary_threshold: f32, confirm_rounds: u32, timeout_hours: u32) -> Self {
        PlanGate {
            boundary_threshold,
            confirm_rounds,
            timeout_hours,
            score_history: VecDeque::with_capacity(confirm_rounds as usize),
            last_activity: 0,
        }
    }

    /// Compute the boundary score for the current dialogue round.
    pub fn boundary_score(
        &self,
        current_embedding: &[f32],
        current_tone: &ToneMeta,
        current_anchors: &[String],
        plan_ctx: PlanContext<'_>,
        time_gap_minutes: f64,
    ) -> f32 {
        let semantic_drift = match plan_ctx.centroid {
            Some(centroid) => {
                let cos = cosine_similarity(current_embedding, centroid);
                (1.0 - cos).clamp(0.0, 1.0)
            }
            None => 0.0,
        };

        let emotional_shift = match plan_ctx.avg_tone {
            Some(avg) => {
                let valence_diff = (current_tone.valence - avg.valence).abs();
                let arousal_diff = (current_tone.arousal - avg.arousal).abs();
                valence_diff * 0.5 + arousal_diff * 0.5
            }
            None => 0.0,
        };

        let anchor_change = jaccard_distance(current_anchors, plan_ctx.anchors);

        let max_minutes = (self.timeout_hours as f64) * 60.0;
        let time_gap = if max_minutes > 0.0 {
            (time_gap_minutes / max_minutes).min(1.0) as f32
        } else {
            0.0
        };

        (semantic_drift * 0.40 + emotional_shift * 0.25 + anchor_change * 0.25 + time_gap * 0.10)
            .clamp(0.0, 1.0)
    }

    pub fn decide(&mut self, score: f32, timestamp: i64) -> PlanHint {
        while self.score_history.len() >= self.confirm_rounds as usize {
            self.score_history.pop_front();
        }
        self.score_history.push_back(score);

        if self.score_history.len() < self.confirm_rounds as usize {
            self.last_activity = timestamp;
            return PlanHint::Continuing;
        }

        let timeout_ms = (self.timeout_hours as i64) * 3600 * 1000;
        if self.last_activity > 0 && timeout_ms > 0 && timestamp - self.last_activity > timeout_ms
        {
            self.score_history.clear();
            self.last_activity = timestamp;
            return PlanHint::TimeoutNewPlan;
        }

        let avg: f32 = self.score_history.iter().sum::<f32>() / self.score_history.len() as f32;
        self.last_activity = timestamp;
        if avg > self.boundary_threshold {
            self.score_history.clear();
            return PlanHint::NewTopicLikely;
        }

        self.last_activity = timestamp;
        PlanHint::Continuing
    }

    pub fn match_to_plan(
        &self,
        plan_id: Option<&str>,
        plan_index: &PlanIndex,
        _current_embedding: &[f32],
        boundary_score: f32,
    ) -> Option<String> {
        if let Some(pid) = plan_id {
            return Some(pid.to_string());
        }
        if let Some(ref active_id) = plan_index.active_plan_id {
            if boundary_score < self.boundary_threshold {
                return Some(active_id.clone());
            }
            return None;
        }
        None
    }

    #[allow(dead_code)]
    pub(crate) fn history_len(&self) -> usize {
        self.score_history.len()
    }
    #[allow(dead_code)]
    pub(crate) fn last_activity_ts(&self) -> i64 {
        self.last_activity
    }
    #[allow(dead_code)]
    pub(crate) fn reset_history(&mut self) {
        self.score_history.clear();
    }
}

// ── utility functions ───────────────────────────────────────────

fn cosine_similarity(a: &[f32], b: &[f32]) -> f32 {
    let len = a.len().min(b.len());
    if len == 0 {
        return 0.0;
    }
    let dot: f32 = a[..len].iter().zip(b[..len].iter()).map(|(x, y)| x * y).sum();
    let norm_a: f32 = a[..len].iter().map(|x| x * x).sum::<f32>().sqrt();
    let norm_b: f32 = b[..len].iter().map(|x| x * x).sum::<f32>().sqrt();
    if norm_a == 0.0 || norm_b == 0.0 {
        return 0.0;
    }
    dot / (norm_a * norm_b)
}

fn jaccard_distance(a: &[String], b: &[String]) -> f32 {
    if a.is_empty() && b.is_empty() {
        return 0.0;
    }
    let set_a: std::collections::HashSet<&str> = a.iter().map(|s| s.as_str()).collect();
    let set_b: std::collections::HashSet<&str> = b.iter().map(|s| s.as_str()).collect();
    let intersection = set_a.intersection(&set_b).count();
    let union = set_a.union(&set_b).count().max(1);
    (1.0 - intersection as f32 / union as f32).clamp(0.0, 1.0)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_cosine_identical() {
        let v = vec![1.0_f32, 2.0, 3.0];
        let sim = cosine_similarity(&v, &v);
        assert!((sim - 1.0).abs() < 0.001);
    }

    #[test]
    fn test_cosine_orthogonal() {
        let a = vec![1.0_f32, 0.0, 0.0];
        let b = vec![0.0_f32, 1.0, 0.0];
        assert!((cosine_similarity(&a, &b) - 0.0).abs() < 0.001);
    }

    #[test]
    fn test_cosine_opposite() {
        let a = vec![1.0_f32, 0.0, 0.0];
        let b = vec![-1.0_f32, 0.0, 0.0];
        assert!((cosine_similarity(&a, &b) - (-1.0)).abs() < 0.001);
    }

    #[test]
    fn test_jaccard_identical() {
        let a = vec!["foo".to_string(), "bar".to_string()];
        let b = vec!["foo".to_string(), "bar".to_string()];
        assert_eq!(jaccard_distance(&a, &b), 0.0);
    }

    #[test]
    fn test_jaccard_disjoint() {
        let a = vec!["foo".to_string()];
        let b = vec!["bar".to_string()];
        assert_eq!(jaccard_distance(&a, &b), 1.0);
    }

    fn make_tone(valence: f32, arousal: f32) -> ToneMeta {
        ToneMeta {
            valence,
            arousal,
            tone_tags: Vec::new(),
            filler_ratio: 0.0,
            sentence_style: crate::engram::StyleCompact {
                avg_sentence_len: 0.0,
                question_ratio: 0.0,
                exclamation_count: 0,
            },
        }
    }

    #[test]
    fn test_boundary_score_same_vector() {
        let gate = PlanGate::new(0.55, 3, 24);
        let v = vec![1.0_f32; 1024];
        let tone = make_tone(0.5, 0.5);
        let score = gate.boundary_score(
            &v, &tone, &[],
            PlanContext { centroid: Some(&v), avg_tone: Some(&tone), anchors: &[] },
            0.0,
        );
        assert!(score < 0.01);
    }

    #[test]
    fn test_boundary_score_different_vector() {
        let gate = PlanGate::new(0.55, 3, 24);
        let current = vec![1.0_f32; 1024];
        let centroid: Vec<f32> = vec![-1.0_f32; 1024];
        let tone = make_tone(0.0, 0.5);
        let score = gate.boundary_score(
            &current, &tone, &[],
            PlanContext { centroid: Some(&centroid), avg_tone: Some(&tone), anchors: &[] },
            0.0,
        );
        assert!(score > 0.35);
    }

    #[test]
    fn test_boundary_score_no_centroid() {
        let gate = PlanGate::new(0.55, 3, 24);
        let v = vec![0.0_f32; 1024];
        let tone = make_tone(0.0, 0.5);
        let score = gate.boundary_score(
            &v, &tone, &[],
            PlanContext { centroid: None, avg_tone: None, anchors: &[] },
            0.0,
        );
        assert!(score < 0.01);
    }

    #[test]
    fn test_boundary_score_emotional_shift() {
        let gate = PlanGate::new(0.55, 3, 24);
        let v = vec![0.0_f32; 1024];
        let current_tone = make_tone(1.0, 0.0);
        let avg_tone = make_tone(-1.0, 1.0);
        let score = gate.boundary_score(
            &v, &current_tone, &[],
            PlanContext { centroid: None, avg_tone: Some(&avg_tone), anchors: &[] },
            0.0,
        );
        assert!(score >= 0.35 && score <= 0.40);
    }

    #[test]
    fn test_boundary_score_anchor_change() {
        let gate = PlanGate::new(0.55, 3, 24);
        let v = vec![0.0_f32; 1024];
        let tone = make_tone(0.0, 0.5);
        let current_anchors = vec!["auth".to_string(), "jwt".to_string()];
        let plan_anchors = vec!["storage".to_string(), "upload".to_string()];
        let score = gate.boundary_score(
            &v, &tone, &current_anchors,
            PlanContext { centroid: None, avg_tone: None, anchors: &plan_anchors },
            0.0,
        );
        assert!(score >= 0.20 && score <= 0.30);
    }

    #[test]
    fn test_decide_continuing_not_full() {
        let mut gate = PlanGate::new(0.55, 3, 24);
        assert_eq!(gate.decide(0.9, 1000), PlanHint::Continuing);
    }

    #[test]
    fn test_decide_new_topic() {
        let mut gate = PlanGate::new(0.55, 3, 24);
        gate.decide(0.9, 1000);
        gate.decide(0.8, 2000);
        assert_eq!(gate.decide(0.7, 3000), PlanHint::NewTopicLikely);
    }

    #[test]
    fn test_decide_continuing_low_scores() {
        let mut gate = PlanGate::new(0.55, 3, 24);
        gate.decide(0.1, 1000);
        gate.decide(0.2, 2000);
        assert_eq!(gate.decide(0.1, 3000), PlanHint::Continuing);
    }

    #[test]
    fn test_decide_timeout() {
        let mut gate = PlanGate::new(0.55, 3, 24);
        gate.decide(0.1, 1000);
        gate.decide(0.1, 2000);
        let gap: i64 = 25 * 3600 * 1000;
        assert_eq!(gate.decide(0.1, 2000 + gap), PlanHint::TimeoutNewPlan);
    }

    #[test]
    fn test_match_to_plan_explicit_id() {
        let gate = PlanGate::new(0.55, 3, 24);
        let index = PlanIndex::new();
        let v = vec![0.0_f32; 10];
        assert_eq!(gate.match_to_plan(Some("plan_123"), &index, &v, 0.0), Some("plan_123".to_string()));
    }

    #[test]
    fn test_match_to_plan_uses_active() {
        let gate = PlanGate::new(0.55, 3, 24);
        let mut index = PlanIndex::new();
        index.active_plan_id = Some("active".to_string());
        let v = vec![0.0_f32; 10];
        assert_eq!(gate.match_to_plan(None, &index, &v, 0.3), Some("active".to_string()));
    }

    #[test]
    fn test_match_to_plan_high_score_rejects() {
        let gate = PlanGate::new(0.55, 3, 24);
        let mut index = PlanIndex::new();
        index.active_plan_id = Some("active".to_string());
        let v = vec![0.0_f32; 10];
        assert_eq!(gate.match_to_plan(None, &index, &v, 0.8), None);
    }

    #[test]
    fn test_match_to_plan_no_active() {
        let gate = PlanGate::new(0.55, 3, 24);
        let index = PlanIndex::new();
        let v = vec![0.0_f32; 10];
        assert_eq!(gate.match_to_plan(None, &index, &v, 0.3), None);
    }

    #[test]
    fn test_plan_index_default() {
        let index = PlanIndex::default();
        assert!(index.active_plan_id.is_none());
        assert!(index.entries.is_empty());
        assert!(index.plan_info.is_empty());
    }
}
