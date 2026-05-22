/// Modern Hopfield Network (MHN) core.
///
/// Implements one-step attractor convergence via softmax energy function:
///     E(x) = -lse(β, Xᵀx) + ½xᵀx
///     x_new = softmax(β Xᵀx) · X
///
/// Key properties:
///     - Storage capacity: N ∝ exp(d)  (exponential, not 0.14d)
///     - Convergence: One step to nearest attractor
///     - O(N·d) recall: dot products against all stored patterns
///
/// Patterns stored in f16 for memory efficiency (2KB/record vs 4KB),
/// converted to f32 on-the-fly during dot product computation.

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
    pub drift_enabled: bool,
    /// Plasticity configuration
    pub plasticity_cfg: PlasticityConfig,
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
        }
    }

    /// Remove a pattern by id using swap-remove.
    /// Also maintains access_counts and last_access vectors.
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

            // Swap id mapping
            let swapped_id = self.idx_to_id[last_idx].clone();
            self.id_to_idx.insert(swapped_id.clone(), idx);
            self.idx_to_id[idx] = swapped_id;
        }

        self.idx_to_id.pop();
        self.patterns.truncate(self.patterns.len() - self.dim);
        self.access_counts.pop();
        self.last_access.pop();

        true
    }

    /// Associative recall: returns (winner_id, confidence).
    /// query is f32 (converted from f16 encoder output once by caller).
    /// Dot products computed in parallel via rayon.
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
                self.beta * dot_f16_f32(pattern, query)
            })
            .collect();

        let weights = softmax(&similarities);

        let (winner_idx, &confidence) = weights
            .iter()
            .enumerate()
            .max_by(|(_, a), (_, b)| a.partial_cmp(b).unwrap())?;

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
                self.beta * dot_f16_f32(pattern, query)
            })
            .collect();

        let weights = softmax(&similarities);

        let k = k.min(n);
        let mut indexed: Vec<(usize, f32)> = weights.into_iter().enumerate().collect();
        indexed.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap());

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

    /// Recall among a subset of candidate ids (for two-stage retrieval).
    /// Dot products computed in parallel via rayon.
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
                self.beta * dot_f16_f32(pattern, query)
            })
            .collect();

        let weights = softmax(&similarities);

        let (local_winner, &confidence) = weights
            .iter()
            .enumerate()
            .max_by(|(_, a), (_, b)| a.partial_cmp(b).unwrap())?;

        let global_idx = indices[local_winner];
        Some((self.idx_to_id[global_idx].clone(), confidence))
    }

    /// Recall among candidates, returning raw dot products (no softmax).
    /// Used by engine-layer strategies (time_alpha, importance_alpha) that
    /// need raw scores for custom logit adjustments.
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
                (self.idx_to_id[idx].clone(), score)
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
                self.beta * dot_f16_f32(pattern, query)
            })
            .collect();

        let weights = softmax(&similarities);

        // Step 2: find winner
        let (winner_idx, &confidence) = weights
            .iter()
            .enumerate()
            .max_by(|(_, a), (_, b)| a.partial_cmp(b).unwrap())?;

        let cfg = &self.plasticity_cfg;
        let mut drifted_indices = Vec::new();

        // Step 3: drift all eligible patterns
        for i in 0..n {
            let attention = weights[i];
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
            for j in 0..self.dim {
                self.patterns[start + j] = f16::from_f32(drifted[j]);
            }

            self.access_counts[i] += 1;
            self.last_access[i] = now_ms;
            drifted_indices.push(i);
        }

        Some((self.idx_to_id[winner_idx].clone(), confidence, drifted_indices))
    }

    /// Get access statistics for a memory.
    pub fn get_access_stats(&self, id: &str) -> Option<(u64, u64)> {
        self.id_to_idx.get(id).map(|&idx| {
            (self.access_counts[idx], self.last_access[idx])
        })
    }

    /// Collect (id, pattern) pairs for a set of row indices.
    /// Used by engine on close() to persist drifted patterns to LMDB.
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
    pub fn enable_plasticity(&mut self, enabled: bool) {
        self.drift_enabled = enabled;
    }

    /// Update plasticity configuration.
    pub fn set_plasticity_config(&mut self, cfg: PlasticityConfig) {
        self.plasticity_cfg = cfg;
    }

    /// Apply natural decay: attenuate patterns that haven't been accessed
    /// beyond the decay threshold. Patterns with extremely low L2 norm
    /// are flagged (caller should mark them as dormant).
    ///
    /// Returns a list of memory IDs that have decayed below the dormant threshold.
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

// ── Tests ────────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;
    use rand::Rng;
    use rand::rngs::StdRng;
    use rand::SeedableRng;

    fn make_f16_vector(dim: usize, seed: u64) -> Vec<f16> {
        let mut rng = StdRng::seed_from_u64(seed);
        let v: Vec<f32> = (0..dim).map(|_| rng.gen_range(-1.0f32..1.0f32)).collect();
        l2_normalize_f16(&v.iter().map(|&x| f16::from_f32(x)).collect::<Vec<_>>())
    }

    fn to_f32_vec(v: &[f16]) -> Vec<f32> {
        v.iter().map(|x| x.to_f32()).collect()
    }

    #[test]
    fn test_empty_recall_returns_none() {
        let mhn = ModernHopfield::new(8, 8.0);
        let query = vec![0.0f32; 8];
        assert!(mhn.recall(&query).is_none());
        assert!(mhn.recall_topk(&query, 3).is_empty());
        assert!(mhn.recall_among(&query, &["a"]).is_none());
    }

    #[test]
    fn test_single_pattern_recall_confidence_near_one() {
        let mut mhn = ModernHopfield::new(8, 8.0);
        let pattern = make_f16_vector(8, 42);
        mhn.add_pattern("mem1", &pattern);

        let (id, confidence) = mhn.recall(&to_f32_vec(&pattern)).unwrap();
        assert_eq!(id, "mem1");
        assert!((confidence - 1.0).abs() < 1e-5, "confidence = {confidence}");
    }

    #[test]
    fn test_orthogonal_patterns_recall_correctly() {
        let dim = 512;
        let beta = 8.0;
        let mut mhn = ModernHopfield::new(dim, beta);

        let n = 10;
        let mut patterns = Vec::with_capacity(n);
        for i in 0..n {
            let v = make_f16_vector(dim, i as u64 * 7919 + 12345);
            mhn.add_pattern(&format!("mem_{i}"), &v);
            patterns.push(v);
        }

        for (i, pattern) in patterns.iter().enumerate() {
            let (id, confidence) = mhn.recall(&to_f32_vec(pattern)).unwrap();
            assert_eq!(id, format!("mem_{i}"), "pattern {i} misidentified");
            assert!(
                confidence > 0.9,
                "pattern {i} confidence too low: {confidence}"
            );
        }
    }

    #[test]
    fn test_remove_pattern() {
        let dim = 16;
        let mut mhn = ModernHopfield::new(dim, 8.0);

        let v1 = make_f16_vector(dim, 100);
        let v2 = make_f16_vector(dim, 200);
        let v3 = make_f16_vector(dim, 300);

        mhn.add_pattern("a", &v1);
        mhn.add_pattern("b", &v2);
        mhn.add_pattern("c", &v3);

        assert_eq!(mhn.len(), 3);

        let removed = mhn.remove_pattern("b");
        assert!(removed);
        assert_eq!(mhn.len(), 2);

        let (id, _) = mhn.recall(&to_f32_vec(&v2)).unwrap();
        assert_ne!(id, "b", "removed pattern still recalled");

        let (id_a, _) = mhn.recall(&to_f32_vec(&v1)).unwrap();
        assert_eq!(id_a, "a");

        let (id_c, _) = mhn.recall(&to_f32_vec(&v3)).unwrap();
        assert_eq!(id_c, "c");

        assert!(!mhn.remove_pattern("nonexistent"));
    }

    #[test]
    fn test_recall_topk() {
        let dim = 16;
        let mut mhn = ModernHopfield::new(dim, 8.0);

        let v1 = make_f16_vector(dim, 1000);
        let v2 = make_f16_vector(dim, 2000);
        let v3 = make_f16_vector(dim, 3000);

        mhn.add_pattern("m1", &v1);
        mhn.add_pattern("m2", &v2);
        mhn.add_pattern("m3", &v3);

        let results = mhn.recall_topk(&to_f32_vec(&v3), 2);
        assert_eq!(results.len(), 2);
        assert_eq!(results[0].0, "m3", "top-1 should be m3");
        for window in results.windows(2) {
            assert!(
                window[0].1 >= window[1].1,
                "topk results not sorted by confidence descending"
            );
        }
    }

    #[test]
    fn test_recall_among() {
        let dim = 16;
        let mut mhn = ModernHopfield::new(dim, 8.0);

        let v1 = make_f16_vector(dim, 111);
        let v2 = make_f16_vector(dim, 222);
        let v3 = make_f16_vector(dim, 333);

        mhn.add_pattern("x", &v1);
        mhn.add_pattern("y", &v2);
        mhn.add_pattern("z", &v3);

        let result = mhn.recall_among(&to_f32_vec(&v1), &["y", "z"]);
        assert!(result.is_some());
        let (id, _) = result.unwrap();
        assert_ne!(id, "x", "recall_among returned non-candidate");

        let result = mhn.recall_among(&to_f32_vec(&v1), &["x"]);
        let (id, conf) = result.unwrap();
        assert_eq!(id, "x");
        assert!(conf > 0.9, "single candidate confidence should be ~1.0: {conf}");

        assert!(mhn.recall_among(&to_f32_vec(&v1), &[]).is_none());
        assert!(mhn.recall_among(&to_f32_vec(&v1), &["nonexistent"]).is_none());
    }

    #[test]
    fn test_add_pattern_replaces_existing() {
        let dim = 8;
        let mut mhn = ModernHopfield::new(dim, 8.0);

        let v1 = make_f16_vector(dim, 50);
        let v2 = make_f16_vector(dim, 99);

        mhn.add_pattern("id1", &v1);
        assert_eq!(mhn.len(), 1);

        mhn.add_pattern("id1", &v2);
        assert_eq!(mhn.len(), 1, "replace should not increase count");

        let (id, conf) = mhn.recall(&to_f32_vec(&v2)).unwrap();
        assert_eq!(id, "id1");
        assert!(conf > 0.99, "after replace, confidence should be ~1.0: {conf}");
    }

    #[test]
    fn test_is_empty() {
        let mut mhn = ModernHopfield::new(4, 8.0);
        assert!(mhn.is_empty());

        let v = vec![f16::from_f32(1.0), f16::ZERO, f16::ZERO, f16::ZERO];
        mhn.add_pattern("a", &v);
        assert!(!mhn.is_empty());

        mhn.remove_pattern("a");
        assert!(mhn.is_empty());
    }

    #[test]
    fn test_swap_remove_consistency() {
        let dim = 8;
        let mut mhn = ModernHopfield::new(dim, 8.0);

        let vs: Vec<Vec<f16>> = (0..5).map(|i| make_f16_vector(dim, i * 7777)).collect();
        for (i, v) in vs.iter().enumerate() {
            mhn.add_pattern(&format!("p{i}"), v);
        }

        mhn.remove_pattern("p2");
        assert_eq!(mhn.len(), 4);

        for (i, v) in vs.iter().enumerate() {
            let id_str = format!("p{i}");
            if id_str == "p2" {
                continue;
            }
            let (id, conf) = mhn.recall(&to_f32_vec(v)).unwrap();
            assert_eq!(id, id_str, "after remove, pattern {id_str} misidentified as {id}");
            assert!(conf > 0.9, "after remove, confidence for {id_str} too low: {conf}");
        }

        mhn.remove_pattern("p0");
        mhn.remove_pattern("p4");
        assert_eq!(mhn.len(), 2);

        let (id, _) = mhn.recall(&to_f32_vec(&vs[1])).unwrap();
        assert_eq!(id, "p1");
        let (id, _) = mhn.recall(&to_f32_vec(&vs[3])).unwrap();
        assert_eq!(id, "p3");
    }

    // ── v0.4.0 plasticity tests ────────────────────────────

    #[test]
    fn test_drift_disabled_equals_recall() {
        let dim = 64;
        let mut mhn = ModernHopfield::new(dim, 8.0);

        for i in 0..5 {
            let v = make_f16_vector(dim, i * 131);
            mhn.add_pattern(&format!("m{i}"), &v);
        }

        let query = make_f16_vector(dim, 42);
        let query_f32 = to_f32_vec(&query);

        // drift_enabled is false by default
        let (id1, conf1) = mhn.recall(&query_f32).unwrap();
        let (id2, conf2, _drifted) = mhn.recall_with_plasticity(&query_f32, 0).unwrap();

        assert_eq!(id1, id2);
        assert!((conf1 - conf2).abs() < 1e-5);
    }

    #[test]
    fn test_winner_reinforcement() {
        let dim = 128;
        let mut mhn = ModernHopfield::new(dim, 8.0);
        mhn.enable_plasticity(true);

        // Add a close pattern and distant patterns
        let target = make_f16_vector(dim, 1000);
        let target_f32 = to_f32_vec(&target);
        mhn.add_pattern("target", &target);

        for i in 1..5 {
            let v = make_f16_vector(dim, 1000 + i * 7777);
            mhn.add_pattern(&format!("dist{i}"), &v);
        }

        // Record similarity before drift
        let (_, conf_before) = mhn.recall(&target_f32).unwrap();

        // Drift toward target
        mhn.recall_with_plasticity(&target_f32, 1000);

        // After drift, target should be even closer
        let (id_after, conf_after) = mhn.recall(&target_f32).unwrap();
        assert_eq!(id_after, "target");
        assert!(
            conf_after >= conf_before - 0.01,
            "winner confidence should not decrease: before={conf_before}, after={conf_after}"
        );
    }

    #[test]
    fn test_access_counts_increment() {
        let dim = 32;
        let mut mhn = ModernHopfield::new(dim, 8.0);
        mhn.enable_plasticity(true);

        let v = make_f16_vector(dim, 42);
        let q = to_f32_vec(&v);
        mhn.add_pattern("a", &v);

        let (id, _, _) = mhn.recall_with_plasticity(&q, 5000).unwrap();
        assert_eq!(id, "a");

        let (count, last_access) = mhn.get_access_stats("a").unwrap();
        assert_eq!(count, 1);
        assert_eq!(last_access, 5000);
    }

    #[test]
    fn test_get_access_stats_nonexistent() {
        let mhn = ModernHopfield::new(16, 8.0);
        assert!(mhn.get_access_stats("nonexistent").is_none());
    }

    #[test]
    fn test_enable_plasticity_toggle() {
        let mut mhn = ModernHopfield::new(8, 8.0);
        assert!(!mhn.drift_enabled, "default should be disabled");

        mhn.enable_plasticity(true);
        assert!(mhn.drift_enabled);

        mhn.enable_plasticity(false);
        assert!(!mhn.drift_enabled);
    }

    #[test]
    fn test_set_plasticity_config() {
        let mut mhn = ModernHopfield::new(8, 8.0);
        let mut cfg = PlasticityConfig::default();
        cfg.reinforce_rate = 0.02;
        cfg.discriminate_rate = 0.01;

        mhn.set_plasticity_config(cfg.clone());

        assert!((mhn.plasticity_cfg.reinforce_rate - 0.02).abs() < 1e-6);
        assert!((mhn.plasticity_cfg.discriminate_rate - 0.01).abs() < 1e-6);
    }

    #[test]
    fn test_apply_decay_triggers_after_threshold() {
        let dim = 16;
        let mut mhn = ModernHopfield::new(dim, 8.0);

        let v = make_f16_vector(dim, 42);
        mhn.add_pattern("old_mem", &v);

        // Set a low decay threshold so decay kicks in quickly
        mhn.plasticity_cfg.decay_threshold_days = 1;
        mhn.plasticity_cfg.decay_rate = 0.5;

        // Mark as accessed in the past
        mhn.last_access[0] = 0;  // Never accessed via plasticity

        // apply_decay skips patterns with last_access=0
        let dormant = mhn.apply_decay(1000000);
        assert!(dormant.is_empty(), "patterns with last_access=0 should not decay");

        // Now set last_access in the past
        let past = 1000; // 1000 ms after epoch = way in the past
        mhn.last_access[0] = past;

        let _dormant = mhn.apply_decay(past + 3 * 86400000); // 3 days after past
        // Pattern should have decayed
        let decayed_norm = l2_norm_f16(&mhn.patterns[0..dim]);
        assert!(
            decayed_norm < 1.0,
            "decayed pattern should have lower norm: {decayed_norm}"
        );
    }

    #[test]
    fn test_remove_pattern_maintains_access_stats() {
        let dim = 8;
        let mut mhn = ModernHopfield::new(dim, 8.0);

        let v1 = make_f16_vector(dim, 10);
        let v2 = make_f16_vector(dim, 20);
        let v3 = make_f16_vector(dim, 30);

        mhn.add_pattern("a", &v1);
        mhn.add_pattern("b", &v2);
        mhn.add_pattern("c", &v3);

        mhn.access_counts[0] = 5;
        mhn.last_access[0] = 100;
        mhn.access_counts[1] = 3;
        mhn.last_access[1] = 200;

        // Remove middle element
        mhn.remove_pattern("b");

        // "a" should still have its stats
        let (count, access) = mhn.get_access_stats("a").unwrap();
        assert_eq!(count, 5);
        assert_eq!(access, 100);

        // "c" should still have its stats
        let (count, access) = mhn.get_access_stats("c").unwrap();
        assert_eq!(count, 0);
        assert_eq!(access, 0);

        assert_eq!(mhn.access_counts.len(), 2);
        assert_eq!(mhn.last_access.len(), 2);
    }
}
