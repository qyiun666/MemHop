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
use std::collections::HashMap;

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
}

impl ModernHopfield {
    pub fn new(dim: usize, beta: f32) -> Self {
        ModernHopfield {
            dim,
            beta,
            id_to_idx: HashMap::new(),
            idx_to_id: Vec::new(),
            patterns: Vec::new(),
        }
    }

    /// Add a pattern (f16, L2-normalized before storage).
    /// If the id already exists, the pattern is replaced in-place.
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
        }
    }

    /// Remove a pattern by id using swap-remove.
    pub fn remove_pattern(&mut self, id: &str) -> bool {
        let idx = match self.id_to_idx.remove(id) {
            Some(i) => i,
            None => return false,
        };

        let last_idx = self.idx_to_id.len() - 1;

        if idx != last_idx {
            let src_start = last_idx * self.dim;
            let dst_start = idx * self.dim;
            let src_slice = self.patterns[src_start..src_start + self.dim].to_vec();
            self.patterns[dst_start..dst_start + self.dim].copy_from_slice(&src_slice);

            let swapped_id = self.idx_to_id[last_idx].clone();
            self.id_to_idx.insert(swapped_id.clone(), idx);
            self.idx_to_id[idx] = swapped_id;
        }

        self.idx_to_id.pop();
        self.patterns.truncate(self.patterns.len() - self.dim);

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
}
