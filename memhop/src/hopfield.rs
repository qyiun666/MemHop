//! Modern Hopfield Network (MHN) core.
//!
//! Implements one-step attractor convergence via softmax energy function:
//!     E(x) = -lse(β, Xᵀx) + ½xᵀx
//!     x_new = softmax(β Xᵀx) · X
//!
//! Key properties:
//!     - Storage capacity: N ∝ exp(d)  (exponential, not 0.14d)
//!     - Convergence: One step to nearest attractor
//!     - O(N·d) recall: dot products against all stored patterns
//!
//! Patterns stored in f16 for memory efficiency (2KB/record vs 4KB),
//! converted to f32 on-the-fly during dot product computation.

use half::f16;
use rayon::prelude::*;
use std::collections::{HashMap, HashSet};

// ── Numerically stable softmax ───────────────────────────────

fn softmax(logits: &[f32]) -> Vec<f32> {
    if logits.is_empty() {
        return Vec::new();
    }
    let max_val = logits.iter().cloned().fold(f32::NEG_INFINITY, f32::max);
    let exps: Vec<f32> = logits.iter().map(|&x| (x - max_val).exp()).collect();
    let sum: f32 = exps.iter().sum();
    if sum == 0.0 {
        let n = logits.len() as f32;
        return vec![1.0 / n; logits.len()];
    }
    exps.iter().map(|&x| x / sum).collect()
}

// ── Vector utilities ─────────────────────────────────────────

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

fn l2_normalize_f32(v: &[f32]) -> Vec<f32> {
    let norm: f32 = v.iter().map(|x| x.powi(2)).sum::<f32>().sqrt();
    if norm == 0.0 {
        return v.to_vec();
    }
    v.iter().map(|x| x / norm).collect()
}

/// Dot product: f16 pattern · f32 query.
/// LLVM auto-vectorizes this simple loop into SIMD instructions.
#[inline]
fn dot_f16_f32(a: &[f16], b: &[f32]) -> f32 {
    let mut sum = 0.0f32;
    for i in 0..a.len() {
        sum += a[i].to_f32() * b[i];
    }
    sum
}

// ── PlasticityConfig ─────────────────────────────────────────

/// Configuration for pattern plasticity — controls how memories evolve during use.
#[derive(Debug, Clone)]
#[allow(dead_code)]
pub struct PlasticityConfig {
    /// Attention below this threshold does not drift (default 0.05)
    pub min_drift_attention: f32,
    /// Attention above this threshold triggers discrimination (default 0.15)
    pub discrimination_threshold: f32,
    /// Learning rate for reinforcement of the winner (default 0.01)
    pub reinforce_rate: f32,
    /// Learning rate for discrimination of non-winners (default 0.005)
    pub discriminate_rate: f32,
    /// Days since last access before decay kicks in (default 90)
    pub decay_threshold_days: u32,
    /// Decay rate per additional day past threshold (default 0.001)
    pub decay_rate: f32,
}

impl Default for PlasticityConfig {
    fn default() -> Self {
        PlasticityConfig {
            min_drift_attention: 0.05,
            discrimination_threshold: 0.15,
            reinforce_rate: 0.01,
            discriminate_rate: 0.005,
            decay_threshold_days: 90,
            decay_rate: 0.001,
        }
    }
}

// ── ModernHopfield ───────────────────────────────────────────

pub struct ModernHopfield {
    dim: usize,
    beta: f32,
    /// id → row index in patterns matrix
    id_to_idx: HashMap<String, usize>,
    /// row index → id
    idx_to_id: Vec<String>,
    /// Flattened pattern matrix: N rows × dim cols, row-major, f16 storage
    patterns: Vec<f16>,

    // ── v0.4.0 plasticity fields ──
    /// Number of times each pattern has been accessed via plasticity recall
    pub access_counts: Vec<u64>,
    /// Last access timestamp in unix millis
    pub last_access: Vec<u64>,
    /// Whether pattern plasticity is enabled (default false)
    #[allow(dead_code)]
    pub drift_enabled: bool,
    /// Plasticity configuration
    pub plasticity_cfg: PlasticityConfig,

    // ── v0.11.0 weighted pattern recall ──
    /// Per-pattern weights for weighted recall (index i matches pattern i).
    /// weight=1.0 for Episode, weight=0.5 for Knowledge (per PRD §4.10).
    pub pattern_weights: Vec<f32>,
}

impl ModernHopfield {
    pub fn new(dim: usize, beta: f32) -> Self {
        ModernHopfield {
            dim,
            beta,
            id_to_idx: HashMap::new(),
            idx_to_id: Vec::new(),
            patterns: Vec::new(),
            access_counts: Vec::new(),
            last_access: Vec::new(),
            drift_enabled: false,
            plasticity_cfg: PlasticityConfig::default(),
            pattern_weights: Vec::new(),
        }
    }

    /// Add a pattern (f16, L2-normalized before storage).
    /// If the id already exists, the pattern is replaced in-place
    /// but access_count and last_access are preserved.
    pub fn add_pattern(&mut self, id: &str, pattern: &[f16]) {
        assert_eq!(pattern.len(), self.dim, "pattern dimension mismatch");

        let normalized = l2_normalize_f16(pattern);

        if let Some(&idx) = self.id_to_idx.get(id) {
            let start = idx * self.dim;
            self.patterns[start..start + self.dim].copy_from_slice(&normalized);
        } else {
            let idx = self.idx_to_id.len();
            self.idx_to_id.push(id.to_string());
            self.id_to_idx.insert(id.to_string(), idx);
            self.patterns.extend_from_slice(&normalized);
            self.access_counts.push(0);
            self.last_access.push(0);
            // v0.11.0: Default weight = 1.0 for new patterns
            self.pattern_weights.push(1.0);
        }
    }

    /// v0.11.0: Add a pattern with an explicit weight.
    /// weight=1.0 for Episode, weight=0.5 for Knowledge (per PRD §4.10).
    pub fn add_pattern_weighted(&mut self, id: &str, pattern: &[f16], weight: f32) {
        // Reuse core insertion logic from add_pattern
        self.add_pattern(id, pattern);
        // Override the weight at the pattern's index
        if let Some(&idx) = self.id_to_idx.get(id) {
            while self.pattern_weights.len() <= idx {
                self.pattern_weights.push(1.0);
            }
            self.pattern_weights[idx] = weight;
        }
    }

    /// Remove a pattern by id using swap-remove.
    /// Also maintains access_counts and last_access vectors.
    #[allow(dead_code)]
    pub fn remove_pattern(&mut self, id: &str) -> bool {
        let idx = match self.id_to_idx.remove(id) {
            Some(i) => i,
            None => return false,
        };

        let last_idx = self.idx_to_id.len() - 1;

        if idx != last_idx {
            // Swap patterns
            let src_start = last_idx * self.dim;
            let dst_start = idx * self.dim;
            let src_slice = self.patterns[src_start..src_start + self.dim].to_vec();
            self.patterns[dst_start..dst_start + self.dim].copy_from_slice(&src_slice);

            // Swap access stats
            self.access_counts[idx] = self.access_counts[last_idx];
            self.last_access[idx] = self.last_access[last_idx];
            // v0.11.0: Swap pattern weights
            self.pattern_weights[idx] = self.pattern_weights[last_idx];

            // Swap id mapping
            let swapped_id = self.idx_to_id[last_idx].clone();
            self.id_to_idx.insert(swapped_id.clone(), idx);
            self.idx_to_id[idx] = swapped_id;
        }

        self.idx_to_id.pop();
        self.patterns.truncate(self.patterns.len() - self.dim);
        self.access_counts.pop();
        self.last_access.pop();
        // v0.11.0: Pop pattern weight
        self.pattern_weights.pop();

        true
    }

    /// Associative recall: returns (winner_id, confidence).
    /// query is f32 (converted from f16 encoder output once by caller).
    /// Dot products computed in parallel via rayon.
    #[allow(dead_code)]
    pub fn recall(&self, query: &[f32]) -> Option<(String, f32)> {
        let n = self.len();
        if n == 0 {
            return None;
        }
        assert_eq!(query.len(), self.dim, "query dimension mismatch");

        let similarities: Vec<f32> = (0..n)
            .into_par_iter()
            .map(|i| {
                let pattern = &self.patterns[i * self.dim..(i + 1) * self.dim];
                let sim = self.beta * dot_f16_f32(pattern, query);
                // v0.11.0: Apply pattern weight before softmax
                sim * self.pattern_weights.get(i).copied().unwrap_or(1.0)
            })
            .collect();

        let weights = softmax(&similarities);

        let (winner_idx, &confidence) = weights
            .iter()
            .enumerate()
            .max_by(|(_, a), (_, b)| a.partial_cmp(b).unwrap_or(std::cmp::Ordering::Equal))?;

        Some((self.idx_to_id[winner_idx].clone(), confidence))
    }

    /// Top-K recall: returns [(id, confidence)] sorted by confidence descending.
    /// Dot products computed in parallel via rayon.
    pub fn recall_topk(&self, query: &[f32], k: usize) -> Vec<(String, f32)> {
        let n = self.len();
        if n == 0 || k == 0 {
            return Vec::new();
        }
        assert_eq!(query.len(), self.dim, "query dimension mismatch");

        let similarities: Vec<f32> = (0..n)
            .into_par_iter()
            .map(|i| {
                let pattern = &self.patterns[i * self.dim..(i + 1) * self.dim];
                let sim = self.beta * dot_f16_f32(pattern, query);
                // v0.11.0: Apply pattern weight before softmax
                sim * self.pattern_weights.get(i).copied().unwrap_or(1.0)
            })
            .collect();

        let weights = softmax(&similarities);

        let k = k.min(n);
        let mut indexed: Vec<(usize, f32)> = weights.into_iter().enumerate().collect();
        indexed.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));

        indexed
            .into_iter()
            .take(k)
            .map(|(idx, conf)| (self.idx_to_id[idx].clone(), conf))
            .collect()
    }

    pub fn len(&self) -> usize {
        self.idx_to_id.len()
    }

    pub fn is_empty(&self) -> bool {
        self.idx_to_id.is_empty()
    }

    /// v0.6.1: Recall among a subset of candidate ids (for two-stage retrieval).
    /// Dot products computed in parallel via rayon.
    #[allow(dead_code)]
    pub fn recall_among(&self, query: &[f32], candidate_ids: &[&str]) -> Option<(String, f32)> {
        if candidate_ids.is_empty() {
            return None;
        }
        assert_eq!(query.len(), self.dim, "query dimension mismatch");

        let indices: Vec<usize> = candidate_ids
            .iter()
            .filter_map(|id| self.id_to_idx.get(*id).copied())
            .collect();

        if indices.is_empty() {
            return None;
        }

        let similarities: Vec<f32> = indices
            .par_iter()
            .map(|&idx| {
                let pattern = &self.patterns[idx * self.dim..(idx + 1) * self.dim];
                let sim = self.beta * dot_f16_f32(pattern, query);
                // v0.11.0: Apply pattern weight before softmax
                sim * self.pattern_weights.get(idx).copied().unwrap_or(1.0)
            })
            .collect();

        let weights = softmax(&similarities);

        let (local_winner, &confidence) = weights
            .iter()
            .enumerate()
            .max_by(|(_, a), (_, b)| a.partial_cmp(b).unwrap_or(std::cmp::Ordering::Equal))?;

        let global_idx = indices[local_winner];
        Some((self.idx_to_id[global_idx].clone(), confidence))
    }

    /// Recall among candidates, returning raw dot products (no softmax).
    /// v0.6.1: Recall among raw (for engine-layer strategy logit adjustments).
    #[allow(dead_code)]
    pub fn recall_among_raw(
        &self,
        query: &[f32],
        candidate_ids: &[&str],
    ) -> Vec<(String, f32)> {
        if candidate_ids.is_empty() {
            return Vec::new();
        }
        assert_eq!(query.len(), self.dim, "query dimension mismatch");

        let indices: Vec<usize> = candidate_ids
            .iter()
            .filter_map(|id| self.id_to_idx.get(*id).copied())
            .collect();

        if indices.is_empty() {
            return Vec::new();
        }

        let raw_scores: Vec<(String, f32)> = indices
            .par_iter()
            .map(|&idx| {
                let pattern = &self.patterns[idx * self.dim..(idx + 1) * self.dim];
                let score = self.beta * dot_f16_f32(pattern, query);
                // v0.11.0: Apply pattern weight for consistent ranking
                let weighted = score * self.pattern_weights.get(idx).copied().unwrap_or(1.0);
                (self.idx_to_id[idx].clone(), weighted)
            })
            .collect();

        raw_scores
    }

    // ── v0.4.0 Plasticity ─────────────────────────────────

    /// Recall with pattern plasticity: drift patterns toward/away from query.
    ///
    /// Winner pattern reinforces toward query; high-attention non-winners
    /// discriminate away. Operates on all stored patterns (full softmax).
    ///
    /// Requires `&mut self` (write lock) — callers must hold write access.
    /// Returns (winner_id, confidence, drifted_indices) — the third element
    /// contains the Hopfield row indices that were modified by drift.
    #[allow(dead_code)]
    pub fn recall_with_plasticity(
        &mut self,
        query: &[f32],
        now_ms: u64,
    ) -> Option<(String, f32, Vec<usize>)> {
        if !self.drift_enabled || self.is_empty() {
            return self.recall(query).map(|(id, conf)| (id, conf, Vec::new()));
        }

        let n = self.len();
        assert_eq!(query.len(), self.dim, "query dimension mismatch");

        // Step 1: compute similarities + softmax weights (same as recall)
        let similarities: Vec<f32> = (0..n)
            .into_par_iter()
            .map(|i| {
                let pattern = &self.patterns[i * self.dim..(i + 1) * self.dim];
                let sim = self.beta * dot_f16_f32(pattern, query);
                // v0.11.0: Apply pattern weight before softmax
                sim * self.pattern_weights.get(i).copied().unwrap_or(1.0)
            })
            .collect();

        let weights = softmax(&similarities);

        // Step 2: find winner
        let (winner_idx, &confidence) = weights
            .iter()
            .enumerate()
            .max_by(|(_, a), (_, b)| a.partial_cmp(b).unwrap_or(std::cmp::Ordering::Equal))?;

        let cfg = &self.plasticity_cfg;
        let mut drifted_indices = Vec::new();

        // Step 3: drift all eligible patterns
        for (i, &attention) in weights.iter().enumerate().take(n) {
            if attention < cfg.min_drift_attention {
                continue;
            }

            let direction = if i == winner_idx {
                cfg.reinforce_rate
            } else if attention > cfg.discrimination_threshold {
                -cfg.discriminate_rate
            } else {
                continue;
            };

            // Convert pattern to f32, apply drift, L2 normalize, convert back
            let start = i * self.dim;
            let mut pattern_f32: Vec<f32> = (0..self.dim)
                .map(|j| self.patterns[start + j].to_f32())
                .collect();

            for j in 0..self.dim {
                pattern_f32[j] += direction * attention * query[j];
            }

            let drifted = l2_normalize_f32(&pattern_f32);
            for (j, &val) in drifted.iter().enumerate().take(self.dim) {
                self.patterns[start + j] = f16::from_f32(val);
            }

            self.access_counts[i] += 1;
            self.last_access[i] = now_ms;
            drifted_indices.push(i);
        }

        Some((self.idx_to_id[winner_idx].clone(), confidence, drifted_indices))
    }

    /// Get access statistics for a memory.
    #[allow(dead_code)]
    pub fn get_access_stats(&self, id: &str) -> Option<(u64, u64)> {
        self.id_to_idx.get(id).map(|&idx| {
            (self.access_counts[idx], self.last_access[idx])
        })
    }

    /// v0.6.x: Collect (id, pattern) pairs for a set of row indices.
    #[allow(dead_code)]
    pub fn collect_patterns_by_indices(&self, indices: &HashSet<usize>) -> Vec<(String, Vec<f16>)> {
        let mut result = Vec::with_capacity(indices.len());
        for &idx in indices {
            if idx >= self.len() {
                continue;
            }
            let start = idx * self.dim;
            let pattern = self.patterns[start..start + self.dim].to_vec();
            result.push((self.idx_to_id[idx].clone(), pattern));
        }
        result
    }

    /// Enable or disable pattern drift.
    #[allow(dead_code)]
    pub fn enable_plasticity(&mut self, enabled: bool) {
        self.drift_enabled = enabled;
    }

    /// v0.6.1: Update plasticity configuration.
    #[allow(dead_code)]
    pub fn set_plasticity_config(&mut self, cfg: PlasticityConfig) {
        self.plasticity_cfg = cfg;
    }

    /// Apply natural decay: attenuate patterns that haven't been accessed
    /// beyond the decay threshold. Patterns with extremely low L2 norm
    /// are flagged (caller should mark them as dormant).
    ///
    /// Returns a list of memory IDs that have decayed below the dormant threshold.
    #[allow(dead_code)]
    pub fn apply_decay(&mut self, now_ms: u64) -> Vec<String> {
        if self.is_empty() {
            return Vec::new();
        }

        let cfg = &self.plasticity_cfg;
        let threshold_days = cfg.decay_threshold_days as u64;
        let mut dormant_candidates = Vec::new();

        let dim = self.dim;
        let ln_1 = |x: f64| (1.0 + x).ln();

        for i in 0..self.len() {
            let days_since_access = if self.last_access[i] == 0 {
                // Never accessed via plasticity — skip decay
                continue;
            } else {
                let elapsed = now_ms.saturating_sub(self.last_access[i]);
                elapsed / 86400000
            };

            if days_since_access <= threshold_days {
                continue;
            }

            let extra_days = (days_since_access - threshold_days) as f64;
            let scale = 1.0f64 - (cfg.decay_rate as f64) * ln_1(extra_days);
            let scale = scale.max(0.0) as f32;

            let start = i * dim;
            for j in 0..dim {
                let val = self.patterns[start + j].to_f32() * scale;
                self.patterns[start + j] = f16::from_f32(val);
            }

            // Check if pattern's L2 norm has dropped below dormant threshold
            let norm = l2_norm_f16(&self.patterns[start..start + dim]);
            if norm < 0.1 {
                dormant_candidates.push(self.idx_to_id[i].clone());
            }
        }

        dormant_candidates
    }
}

/// Compute cosine similarity between two f16 vectors.
pub fn cosine_similarity_f16(a: &[half::f16], b: &[half::f16]) -> f32 {
    if a.len() != b.len() || a.is_empty() {
        return 0.0;
    }
    let mut dot = 0.0_f64;
    let mut na = 0.0_f64;
    let mut nb = 0.0_f64;
    for (x, y) in a.iter().zip(b.iter()) {
        let xf = x.to_f32() as f64;
        let yf = y.to_f32() as f64;
        dot += xf * yf;
        na += xf * xf;
        nb += yf * yf;
    }
    let denom = (na.sqrt() * nb.sqrt()).max(f64::EPSILON);
    (dot / denom) as f32
}

// ── Tests ────────────────────────────────────────────────────
// Moved to hopfield_test.rs to keep this file under 600 lines (G-01 limit).

#[cfg(test)]
#[path = "hopfield_test.rs"]
mod tests;
