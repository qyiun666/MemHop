//! Sparse inverted index for two-stage retrieval.
//!
//! Stage 1: Sparse coarse screening via inverted index (ngram → memory_ids).
//! Stage 2: MHN fine ranking via Hopfield recall_among.
//!
//! This improves recall quality for short Chinese text by ensuring
//! ngram-overlapping documents are always in the candidate set,
//! even when dense-vector similarity alone might miss them.

use std::collections::{HashMap, HashSet};

use serde::{Deserialize, Serialize};

/// BM25 parameters (Okapi BM25).
const K1: f32 = 1.2;
const B: f32 = 0.75;

/// Sparse inverted index for two-stage retrieval (stage 1: coarse screening).
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SparseIndex {
    /// ngram → set of memory_ids containing this ngram
    inverted: HashMap<String, HashSet<String>>,
    /// memory_id → sparse weights (ngram → weight) for this memory
    forward: HashMap<String, HashMap<String, f32>>,
    /// memory_id → document length (character count for BM25 normalization)
    doc_len: HashMap<String, usize>,
}

#[allow(dead_code)]
impl SparseIndex {
    pub fn new() -> Self {
        SparseIndex {
            inverted: HashMap::new(),
            forward: HashMap::new(),
            doc_len: HashMap::new(),
        }
    }

    /// Add a memory's sparse representation to the index.
    pub fn add(&mut self, id: &str, sparse: &HashMap<String, f32>, doc_length: usize) {
        // If id already exists, remove old entries first
        if self.forward.contains_key(id) {
            self.remove(id);
        }

        // Build forward index
        self.forward.insert(id.to_string(), sparse.clone());
        self.doc_len.insert(id.to_string(), doc_length);

        // Build inverted index
        for ngram in sparse.keys() {
            self.inverted
                .entry(ngram.clone())
                .or_default()
                .insert(id.to_string());
        }
    }

    /// Remove a memory from the index.
    pub fn remove(&mut self, id: &str) {
        self.doc_len.remove(id);
        if let Some(old_sparse) = self.forward.remove(id) {
            for ngram in old_sparse.keys() {
                if let Some(doc_set) = self.inverted.get_mut(ngram) {
                    doc_set.remove(id);
                    if doc_set.is_empty() {
                        self.inverted.remove(ngram);
                    }
                }
            }
        }
    }

    /// Update a memory's sparse representation (remove old, add new).
    pub fn update(&mut self, id: &str, sparse: &HashMap<String, f32>, doc_length: usize) {
        self.remove(id);
        self.add(id, sparse, doc_length);
    }

    /// Coarse screening: given query's sparse weights, return candidate IDs
    /// sorted by sparse score (dot product of shared ngram weights) descending.
    ///
    /// `max_candidates`: maximum number of candidates to return.
    pub fn search(&self, query_sparse: &HashMap<String, f32>, max_candidates: usize) -> Vec<String> {
        let mut scores: HashMap<String, f32> = HashMap::new();

        for (ngram, q_weight) in query_sparse {
            if let Some(doc_ids) = self.inverted.get(ngram) {
                for doc_id in doc_ids {
                    if let Some(doc_sparse) = self.forward.get(doc_id)
                        && let Some(d_weight) = doc_sparse.get(ngram) {
                            *scores.entry(doc_id.clone()).or_insert(0.0) += q_weight * d_weight;
                        }
                }
            }
        }

        let mut candidates: Vec<(String, f32)> = scores.into_iter().collect();
        candidates.sort_by(|a, b| {
            b.1.partial_cmp(&a.1)
                .unwrap_or(std::cmp::Ordering::Equal)
        });
        candidates.truncate(max_candidates);
        candidates.into_iter().map(|(id, _)| id).collect()
    }

    /// Compute IDF map from current index state.
    /// Returns ngram → idf = ln(N / df) where N = total docs, df = docs containing ngram.
    /// Rare ngrams get high IDF weights, making them more discriminative.
    pub fn idf_map(&self) -> HashMap<String, f32> {
        let n = self.forward.len() as f32;
        if n < 2.0 {
            return HashMap::new();
        }
        self.inverted
            .iter()
            .map(|(ngram, docs)| {
                let df = docs.len() as f32;
                // Smoothed IDF: 1 + ln(N / df) — clamps to minimum 0.5
                let idf = 1.0 + (n / df.max(1.0)).ln();
                (ngram.clone(), idf.max(0.5))
            })
            .collect()
    }

    /// Search with IDF weighting: score = q_weight × d_weight × idf(ngram).
    /// Rare ngrams contribute more, making results more discriminative.
    pub fn search_weighted(
        &self,
        query_sparse: &HashMap<String, f32>,
        idf: &HashMap<String, f32>,
        max_candidates: usize,
    ) -> Vec<String> {
        let mut scores: HashMap<String, f32> = HashMap::new();

        for (ngram, q_weight) in query_sparse {
            let idf_w = idf.get(ngram).copied().unwrap_or(1.0);
            if let Some(doc_ids) = self.inverted.get(ngram) {
                for doc_id in doc_ids {
                    if let Some(doc_sparse) = self.forward.get(doc_id)
                        && let Some(d_weight) = doc_sparse.get(ngram)
                    {
                        *scores.entry(doc_id.clone()).or_insert(0.0) += q_weight * d_weight * idf_w;
                    }
                }
            }
        }

        let mut candidates: Vec<(String, f32)> = scores.into_iter().collect();
        candidates.sort_by(|a, b| {
            b.1.partial_cmp(&a.1)
                .unwrap_or(std::cmp::Ordering::Equal)
        });
        candidates.truncate(max_candidates);
        candidates.into_iter().map(|(id, _)| id).collect()
    }

    /// Average document length (used by BM25 for length normalization).
    /// Returns 0.0 when no documents are indexed.
    pub fn avg_doc_len(&self) -> f32 {
        let n = self.doc_len.len();
        if n == 0 {
            return 0.0;
        }
        let total: usize = self.doc_len.values().sum();
        total as f32 / n as f32
    }

    /// BM25 search: score = term_freq × IDF / (k1 × (1-b + b × doc_len/avg_len) + term_freq).
    ///
    /// Uses Okapi BM25 with K1=1.2, B=0.75.
    /// Returns (id, raw_bm25_score) pairs sorted descending.
    /// Falls back to `search_weighted()` if the index is empty or avg_doc_len == 0.
    pub fn bm25_search(
        &self,
        query_sparse: &HashMap<String, f32>,
        idf: &HashMap<String, f32>,
        max_candidates: usize,
    ) -> Vec<(String, f32)> {
        // Edge case: empty index → fallback to search_weighted
        if self.forward.is_empty() {
            let ids = self.search_weighted(query_sparse, idf, max_candidates);
            return ids.into_iter().map(|id| (id, 0.0)).collect();
        }
        let avg_len = self.avg_doc_len();
        if avg_len <= 0.0 {
            let ids = self.search_weighted(query_sparse, idf, max_candidates);
            return ids.into_iter().map(|id| (id, 0.0)).collect();
        }

        let mut scores: HashMap<String, f32> = HashMap::new();

        for ngram in query_sparse.keys() {
            let idf_w = idf.get(ngram).copied().unwrap_or(1.0);
            if let Some(doc_ids) = self.inverted.get(ngram) {
                for doc_id in doc_ids {
                    if let Some(doc_sparse) = self.forward.get(doc_id)
                        && let Some(term_freq) = doc_sparse.get(ngram)
                    {
                        let doc_len = *self.doc_len.get(doc_id).unwrap_or(&0) as f32;
                        let len_ratio = doc_len / avg_len;
                        let numerator = *term_freq * idf_w;
                        let denominator = K1 * (1.0 - B + B * len_ratio) + *term_freq;
                        *scores.entry(doc_id.clone()).or_insert(0.0) += numerator / denominator;
                    }
                }
            }
        }

        let mut candidates: Vec<(String, f32)> = scores.into_iter().collect();
        candidates.sort_by(|a, b| {
            b.1.partial_cmp(&a.1)
                .unwrap_or(std::cmp::Ordering::Equal)
        });
        candidates.truncate(max_candidates);
        candidates
    }

    /// Serialize the index to bytes (for LMDB index_db persistence).
    pub fn to_bytes(&self) -> Vec<u8> {
        bincode::serialize(self).unwrap_or_default()
    }

    /// Deserialize the index from bytes.
    pub fn from_bytes(data: &[u8]) -> Option<Self> {
        bincode::deserialize(data).ok()
    }

    /// Number of indexed memories.
    pub fn len(&self) -> usize {
        self.forward.len()
    }

    pub fn is_empty(&self) -> bool {
        self.forward.is_empty()
    }
}

impl Default for SparseIndex {
    fn default() -> Self {
        Self::new()
    }
}

// ── Tests ──────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;

    fn make_sparse(pairs: &[(&str, f32)]) -> HashMap<String, f32> {
        pairs
            .iter()
            .map(|(k, v)| (k.to_string(), *v))
            .collect()
    }

    #[test]
    fn test_add_and_search() {
        let mut idx = SparseIndex::new();

        idx.add("doc1", &make_sparse(&[("机器", 1.0), ("学习", 1.5)]), 4);
        idx.add("doc2", &make_sparse(&[("数据库", 2.0), ("查询", 1.0)]), 4);
        idx.add("doc3", &make_sparse(&[("机器", 1.0), ("视觉", 1.5)]), 4);

        let query = make_sparse(&[("机器", 1.0)]);
        let results = idx.search(&query, 10);

        // doc1 and doc3 both contain "机器", doc2 does not
        assert!(results.contains(&"doc1".to_string()));
        assert!(results.contains(&"doc3".to_string()));
        assert!(!results.contains(&"doc2".to_string()));
    }

    #[test]
    fn test_remove() {
        let mut idx = SparseIndex::new();

        idx.add("doc1", &make_sparse(&[("机器", 1.0)]), 2);
        idx.add("doc2", &make_sparse(&[("机器", 1.5)]), 2);

        idx.remove("doc1");

        let query = make_sparse(&[("机器", 1.0)]);
        let results = idx.search(&query, 10);

        assert!(!results.contains(&"doc1".to_string()));
        assert!(results.contains(&"doc2".to_string()));
        assert_eq!(idx.len(), 1);
    }

    #[test]
    fn test_score_ordering() {
        let mut idx = SparseIndex::new();

        // doc1 shares 2 ngrams with query → higher score
        idx.add("doc1", &make_sparse(&[("机器", 2.0), ("学习", 2.0)]), 4);
        // doc2 shares 1 ngram with query → lower score
        idx.add("doc2", &make_sparse(&[("机器", 1.0)]), 2);

        let query = make_sparse(&[("机器", 1.0), ("学习", 1.0)]);
        let results = idx.search(&query, 10);

        assert_eq!(results[0], "doc1", "doc1 should rank higher (more shared ngrams)");
        assert_eq!(results[1], "doc2");
    }

    #[test]
    fn test_serialization_roundtrip() {
        let mut idx = SparseIndex::new();

        idx.add("doc1", &make_sparse(&[("机器", 1.0), ("学习", 1.5)]), 4);
        idx.add("doc2", &make_sparse(&[("数据库", 2.0)]), 3);

        let bytes = idx.to_bytes();
        let restored = SparseIndex::from_bytes(&bytes).unwrap();

        assert_eq!(restored.len(), 2);

        let query = make_sparse(&[("机器", 1.0)]);
        let original_results = idx.search(&query, 10);
        let restored_results = restored.search(&query, 10);
        assert_eq!(original_results, restored_results);
    }

    #[test]
    fn test_update() {
        let mut idx = SparseIndex::new();

        idx.add("doc1", &make_sparse(&[("机器", 1.0)]), 2);
        idx.update("doc1", &make_sparse(&[("数据库", 2.0)]), 3);

        // Old ngram should no longer match
        let query_old = make_sparse(&[("机器", 1.0)]);
        assert!(!idx.search(&query_old, 10).contains(&"doc1".to_string()));

        // New ngram should match
        let query_new = make_sparse(&[("数据库", 1.0)]);
        assert!(idx.search(&query_new, 10).contains(&"doc1".to_string()));
    }

    #[test]
    fn test_max_candidates() {
        let mut idx = SparseIndex::new();

        for i in 0..10 {
            idx.add(&format!("doc{}", i), &make_sparse(&[("机器", 1.0)]), 2);
        }

        let query = make_sparse(&[("机器", 1.0)]);
        let results = idx.search(&query, 3);
        assert_eq!(results.len(), 3);
    }

    #[test]
    fn test_empty_index() {
        let idx = SparseIndex::new();
        let query = make_sparse(&[("机器", 1.0)]);
        let results = idx.search(&query, 10);
        assert!(results.is_empty());
        assert!(idx.is_empty());
        assert_eq!(idx.len(), 0);
    }

    #[test]
    fn test_no_matching_ngrams() {
        let mut idx = SparseIndex::new();
        idx.add("doc1", &make_sparse(&[("机器", 1.0)]), 2);

        let query = make_sparse(&[("网络", 1.0)]);
        let results = idx.search(&query, 10);
        assert!(results.is_empty());
    }

    #[test]
    fn test_chinese_short_text_recall() {
        let mut idx = SparseIndex::new();

        // Simulate 100 memories across 10 topics
        let topics = [
            "Python编程", "机器学习", "数据库设计", "网络安全", "前端开发",
            "算法竞赛", "云计算架构", "自然语言处理", "计算机视觉", "操作系统",
        ];
        for (i, topic) in topics.iter().enumerate() {
            let text = format!("{}的知识点：这是关于{}的详细内容描述", topic, topic);
            let mut sparse = HashMap::new();
            // Simulate ngram extraction: add 2-grams from topic name
            let chars: Vec<char> = topic.chars().collect();
            for w in chars.windows(2) {
                let ngram: String = w.iter().collect::<String>();
                *sparse.entry(ngram).or_insert(0.0) += 1.0;
            }
            idx.add(
                &format!("mem_{}", i),
                &sparse,
                text.chars().count(),
            );
        }

        // Query for "Python编程"
        let query_text = "Python编程";
        let mut query_sparse = HashMap::new();
        let chars: Vec<char> = query_text.chars().collect();
        for w in chars.windows(2) {
            let ngram: String = w.iter().collect::<String>();
            *query_sparse.entry(ngram).or_insert(0.0) += 1.0;
        }

        let results = idx.search(&query_sparse, 5);
        assert!(!results.is_empty(), "should find matching documents");
        assert_eq!(results[0], "mem_0", "Python编程 should rank first");
    }

    // ── BM25 tests ────────────────────────────────────────

    #[test]
    fn test_bm25_empty_index() {
        let idx = SparseIndex::new();
        let query = make_sparse(&[("机器", 1.0)]);
        let idf = idx.idf_map();
        // Empty index → falls back to search_weighted → empty results
        let results = idx.bm25_search(&query, &idf, 10);
        assert!(results.is_empty());
        assert_eq!(idx.avg_doc_len(), 0.0);
    }

    #[test]
    fn test_bm25_single_document() {
        let mut idx = SparseIndex::new();
        idx.add("doc1", &make_sparse(&[("机器", 2.0), ("学习", 1.0)]), 4);
        let idf = idx.idf_map();
        let query = make_sparse(&[("机器", 1.0), ("学习", 1.0)]);
        let results = idx.bm25_search(&query, &idf, 10);
        // Single doc should still be found even though idf_map returns empty for n<2
        assert!(!results.is_empty());
        assert_eq!(results[0].0, "doc1");
        assert!((idx.avg_doc_len() - 4.0).abs() < f32::EPSILON);
    }

    #[test]
    fn test_bm25_multiple_docs() {
        let mut idx = SparseIndex::new();
        idx.add("doc1", &make_sparse(&[("机器", 3.0), ("学习", 2.0)]), 10);
        idx.add("doc2", &make_sparse(&[("机器", 1.0)]), 5);
        idx.add("doc3", &make_sparse(&[("数据库", 2.0)]), 8);

        // doc1 has higher term_freq for "机器" → higher BM25 score
        let idf = idx.idf_map();
        let query = make_sparse(&[("机器", 1.0)]);
        let results = idx.bm25_search(&query, &idf, 10);
        assert!(results.iter().any(|(id, _)| id == "doc1"));
        assert!(results.iter().any(|(id, _)| id == "doc2"));
        assert!(results.iter().all(|(id, _)| id != "doc3"));
        // doc1 should rank higher (higher term_freq for matching ngram)
        assert_eq!(results[0].0, "doc1", "doc1 has higher term_freq for 机器");
        // Both BM25 scores should be > 0
        assert!(results[0].1 > 0.0);
        assert!(results[1].1 > 0.0);
    }

    #[test]
    fn test_bm25_remove_updates_avg_len() {
        let mut idx = SparseIndex::new();
        idx.add("doc1", &make_sparse(&[("机器", 1.0)]), 10);
        idx.add("doc2", &make_sparse(&[("学习", 1.0)]), 20);
        assert!((idx.avg_doc_len() - 15.0).abs() < f32::EPSILON);

        idx.remove("doc2");
        assert!((idx.avg_doc_len() - 10.0).abs() < f32::EPSILON);
    }
}
