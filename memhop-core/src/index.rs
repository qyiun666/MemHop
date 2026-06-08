//! Sparse inverted index for two-stage retrieval.
//!
//! Stage 1: Sparse coarse screening via inverted index (ngram → memory_ids).
//! Stage 2: MHN fine ranking via Hopfield recall_among.
//!
//! This improves recall quality for short Chinese text by ensuring
//! ngram-overlapping documents are always in the candidate set,
//! even when dense-vector similarity alone might miss them.

use std::collections::{HashMap, HashSet};
use std::num::NonZeroUsize;
use std::sync::Mutex;

use crate::error::{MemHopError, Result};
use heed::types::{Bytes, Str};
use heed::{RoTxn, RwTxn};
use lru::LruCache;
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
    pub fn search(
        &self,
        query_sparse: &HashMap<String, f32>,
        max_candidates: usize,
    ) -> Vec<String> {
        let mut scores: HashMap<String, f32> = HashMap::new();

        for (ngram, q_weight) in query_sparse {
            if let Some(doc_ids) = self.inverted.get(ngram) {
                for doc_id in doc_ids {
                    if let Some(doc_sparse) = self.forward.get(doc_id)
                        && let Some(d_weight) = doc_sparse.get(ngram)
                    {
                        *scores.entry(doc_id.clone()).or_insert(0.0) += q_weight * d_weight;
                    }
                }
            }
        }

        let mut candidates: Vec<(String, f32)> = scores.into_iter().collect();
        candidates.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
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
        candidates.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
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
        candidates.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
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

// ── HnswIndex ──────────────────────────────────────────────

/// HNSW 向量索引 (fast-hnsw)，O(log N) 近似搜索。
/// 使用纯 Rust 实现，无 C++ 依赖，跨平台兼容。
use fast_hnsw::{Builder, distance::Cosine};
use fast_hnsw::labeled::LabeledIndex;

pub struct HnswIndex {
    index: LabeledIndex<Cosine, String>,
    dims: usize,
}

impl std::fmt::Debug for HnswIndex {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("HnswIndex")
            .field("dims", &self.dims)
            .field("size", &self.index.len())
            .finish()
    }
}

/// v0.18.0: HNSW 配置参数
#[derive(Debug, Clone)]
pub struct MemHopHnswConfig {
    pub connectivity: usize,
    pub expansion_add: usize,
    pub expansion_search: usize,
}

impl Default for MemHopHnswConfig {
    fn default() -> Self {
        MemHopHnswConfig {
            connectivity: 16,
            expansion_add: 128,
            expansion_search: 64,
        }
    }
}

impl MemHopHnswConfig {
    /// 根据数据规模动态调整配置
    pub fn for_scale(vector_count: usize) -> Self {
        match vector_count {
            0..=1000 => MemHopHnswConfig {
                connectivity: 16,
                expansion_add: 128,
                expansion_search: 64,
            },
            1001..=10000 => MemHopHnswConfig {
                connectivity: 20,
                expansion_add: 196,
                expansion_search: 96,
            },
            10001..=100000 => MemHopHnswConfig {
                connectivity: 24,
                expansion_add: 256,
                expansion_search: 128,
            },
            _ => MemHopHnswConfig {
                connectivity: 32,
                expansion_add: 512,
                expansion_search: 256,
            },
        }
    }
}

impl HnswIndex {
    /// 创建新的 HNSW 索引。dims 必须与编码器输出维度一致。
    pub fn new(dims: usize) -> Self {
        Self::new_with_config(dims, MemHopHnswConfig::default())
    }

    /// v0.22.0: 使用指定配置创建 HNSW 索引。
    pub fn new_with_config(dims: usize, config: MemHopHnswConfig) -> Self {
        let index = Builder::new()
            .m(config.connectivity)
            .ef_construction(config.expansion_add)
            .seed(42)
            .build_labeled(Cosine);
        
        HnswIndex {
            index,
            dims,
        }
    }

    /// v0.22.0: 添加 f16 向量（内部 F16 量化存储，API 用 f32）。
    pub fn add(&mut self, id: &str, vector: &[half::f16]) {
        if vector.is_empty() || vector.len() != self.dims {
            eprintln!(
                "HnswIndex::add: dim mismatch for '{}': expected {}, got {}",
                id,
                self.dims,
                vector.len()
            );
            return;
        }

        let f32_vec: Vec<f32> = vector.iter().map(|v| v.to_f32()).collect();
        self.index.insert(f32_vec, id.to_string());
    }

    /// 移除向量。fast-hnsw 不支持删除，返回 false。
    pub fn remove(&mut self, _id: &str) -> bool {
        // fast-hnsw doesn't support deletion
        false
    }

    /// 更新向量 (remove + add)。
    pub fn update(&mut self, id: &str, vector: &[half::f16]) {
        // fast-hnsw doesn't support removal, just add new version
        self.add(id, vector);
    }

    /// v0.22.0: Cosine 近似搜索（内部 F16 量化，API 用 f32）。
    pub fn cosine_search(&self, query: &[half::f16], top_k: usize) -> Vec<(String, f32)> {
        if self.index.is_empty() || query.is_empty() || query.len() != self.dims {
            return Vec::new();
        }

        let f32_query: Vec<f32> = query.iter().map(|v| v.to_f32()).collect();
        let count = top_k.min(self.index.len());

        let hits = self.index.search(&f32_query, count, 100); // ef=100
        let mut results = Vec::with_capacity(hits.len());
        for hit in hits {
            let similarity = 1.0 - hit.distance;
            results.push((hit.payload.clone(), similarity));
        }
        results
    }

    /// v0.24.0: 计算两个向量的余弦相似度
    pub fn cosine_similarity(&self, a: &[half::f16], b: &[half::f16]) -> f32 {
        if a.len() != b.len() || a.is_empty() {
            return 0.0;
        }

        let mut dot = 0.0f32;
        let mut norm_a = 0.0f32;
        let mut norm_b = 0.0f32;

        for i in 0..a.len() {
            let va = a[i].to_f32();
            let vb = b[i].to_f32();
            dot += va * vb;
            norm_a += va * va;
            norm_b += vb * vb;
        }

        dot / (norm_a.sqrt() * norm_b.sqrt() + 1e-8)
    }

    pub fn len(&self) -> usize {
        self.index.len()
    }

    pub fn is_empty(&self) -> bool {
        self.index.len() == 0
    }

    pub fn dims(&self) -> usize {
        self.dims
    }

    /// 序列化为 bytes (fast-hnsw 不支持原生序列化，返回空)
    pub fn to_bytes(&self) -> Vec<u8> {
        // fast-hnsw doesn't support native serialization
        Vec::new()
    }

    /// 从 bytes 反序列化 (fast-hnsw 不支持原生序列化)
    pub fn from_bytes(_data: &[u8]) -> Option<Self> {
        None
    }
}

impl Default for HnswIndex {
    fn default() -> Self {
        Self::new(384) // Default dim for multilingual-e5-small
    }
}

// ── Tests ──────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;

    fn make_sparse(pairs: &[(&str, f32)]) -> HashMap<String, f32> {
        pairs.iter().map(|(k, v)| (k.to_string(), *v)).collect()
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

        assert_eq!(
            results[0], "doc1",
            "doc1 should rank higher (more shared ngrams)"
        );
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
            "Python编程",
            "机器学习",
            "数据库设计",
            "网络安全",
            "前端开发",
            "算法竞赛",
            "云计算架构",
            "自然语言处理",
            "计算机视觉",
            "操作系统",
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
            idx.add(&format!("mem_{}", i), &sparse, text.chars().count());
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

// ── SparseIndexV2 (LMDB-backed forward index with LRU cache) ──────────────

/// v0.24.0: Sparse inverted index with forward index stored in LMDB + LRU cache.
/// 
/// Forward index (memory_id → sparse weights) is stored in LMDB to reduce RAM usage.
/// Inverted index (ngram → memory_ids) remains in memory for fast lookup.
/// Doc length map remains in memory for BM25 normalization.
/// LRU cache reduces LMDB I/O for frequently accessed forward entries.
pub struct SparseIndexV2 {
    /// ngram → set of memory_ids containing this ngram (in memory)
    inverted: HashMap<String, HashSet<String>>,
    /// memory_id → document length (in memory)
    doc_len: HashMap<String, usize>,
    /// LMDB database for forward index (memory_id → serialized sparse weights)
    forward_db: Option<heed::Database<Str, Bytes>>,
    /// LRU cache for forward index entries (reduces LMDB I/O)
    /// Default: 10000 entries (uses ~1MB RAM for typical sparse vectors)
    forward_cache: Mutex<LruCache<String, HashMap<String, f32>>>,
}

impl SparseIndexV2 {
    /// Create a new SparseIndexV2 with optional LMDB forward database.
    /// Uses LRU cache with 10000 entries to reduce LMDB I/O.
    pub fn new(forward_db: Option<heed::Database<Str, Bytes>>) -> Self {
        SparseIndexV2 {
            inverted: HashMap::new(),
            doc_len: HashMap::new(),
            forward_db,
            forward_cache: Mutex::new(LruCache::new(NonZeroUsize::new(10000).unwrap())),
        }
    }

    /// Create with custom cache size (for testing or memory-constrained environments).
    pub fn with_cache_size(forward_db: Option<heed::Database<Str, Bytes>>, cache_size: usize) -> Self {
        SparseIndexV2 {
            inverted: HashMap::new(),
            doc_len: HashMap::new(),
            forward_db,
            forward_cache: Mutex::new(LruCache::new(NonZeroUsize::new(cache_size.max(1)).unwrap())),
        }
    }

    /// Add a memory's sparse representation to the index.
    pub fn add(
        &mut self,
        id: &str,
        sparse: &HashMap<String, f32>,
        doc_length: usize,
        wtxn: &mut RwTxn<'_>,
    ) -> Result<()> {
        // If id already exists, remove old entries first
        if self.doc_len.contains_key(id) {
            self.remove(id, wtxn)?;
        }

        // Store forward index in LMDB
        if let Some(db) = self.forward_db {
            let sparse_bytes = bincode::serialize(sparse)
                .map_err(|e| MemHopError::Internal(format!("serialize sparse: {}", e)))?;
            db.put(wtxn, id, &sparse_bytes)
                .map_err(|e| MemHopError::Storage(format!("put forward: {}", e)))?;
        }

        // Store doc length in memory
        self.doc_len.insert(id.to_string(), doc_length);

        // Build inverted index in memory
        for ngram in sparse.keys() {
            self.inverted
                .entry(ngram.clone())
                .or_default()
                .insert(id.to_string());
        }

        Ok(())
    }

    /// Remove a memory from the index.
    pub fn remove(&mut self, id: &str, wtxn: &mut RwTxn<'_>) -> Result<()> {
        self.doc_len.remove(id);

        // Remove from LRU cache
        if let Ok(mut cache) = self.forward_cache.lock() {
            cache.pop(id);
        }

        // Remove from LMDB forward index
        if let Some(db) = self.forward_db {
            // Get old sparse weights to clean up inverted index
            if let Some(old_sparse_bytes) = db.get(wtxn, id)
                .map_err(|e| MemHopError::Storage(format!("get forward: {}", e)))? 
            {
                let old_sparse: HashMap<String, f32> = bincode::deserialize(old_sparse_bytes)
                    .map_err(|e| MemHopError::Internal(format!("deserialize sparse: {}", e)))?;
                
                // Remove from inverted index
                for ngram in old_sparse.keys() {
                    if let Some(doc_set) = self.inverted.get_mut(ngram) {
                        doc_set.remove(id);
                        if doc_set.is_empty() {
                            self.inverted.remove(ngram);
                        }
                    }
                }
            }

            // Remove from forward DB
            db.delete(wtxn, id)
                .map_err(|e| MemHopError::Storage(format!("delete forward: {}", e)))?;
        }

        Ok(())
    }

    /// Update a memory's sparse representation (remove old, add new).
    pub fn update(
        &mut self,
        id: &str,
        sparse: &HashMap<String, f32>,
        doc_length: usize,
        wtxn: &mut RwTxn<'_>,
    ) -> Result<()> {
        self.remove(id, wtxn)?;
        self.add(id, sparse, doc_length, wtxn)
    }

    /// Load forward index from LRU cache or LMDB.
    /// Uses LRU cache to reduce LMDB I/O for frequently accessed entries.
    fn load_forward(&self, id: &str, rtxn: &RoTxn<'_>) -> Result<Option<HashMap<String, f32>>> {
        // Check LRU cache first
        if let Ok(mut cache) = self.forward_cache.lock()
            && let Some(sparse) = cache.get(id) {
                return Ok(Some(sparse.clone()));
        }

        // Cache miss: load from LMDB
        if let Some(db) = self.forward_db {
            match db.get(rtxn, id) {
                Ok(Some(bytes)) => {
                    let sparse: HashMap<String, f32> = bincode::deserialize(bytes)
                        .map_err(|e| MemHopError::Internal(format!("deserialize sparse: {}", e)))?;
                    
                    // Store in LRU cache
                    if let Ok(mut cache) = self.forward_cache.lock() {
                        cache.put(id.to_string(), sparse.clone());
                    }
                    
                    Ok(Some(sparse))
                }
                Ok(None) => Ok(None),
                Err(e) => Err(MemHopError::Storage(format!("get forward: {}", e))),
            }
        } else {
            Ok(None)
        }
    }

    /// Bulk preload forward index for multiple IDs (reduces LMDB I/O).
    /// Used before batch search operations.
    pub fn preload_forward(&self, ids: &[String], rtxn: &RoTxn<'_>) -> Result<()> {
        if let Some(db) = self.forward_db {
            let mut cache = self.forward_cache.lock()
                .map_err(|e| MemHopError::Internal(format!("lock cache: {}", e)))?;
            
            for id in ids {
                if cache.contains(id) {
                    continue;
                }
                match db.get(rtxn, id.as_str()) {
                    Ok(Some(bytes)) => {
                        let sparse: HashMap<String, f32> = bincode::deserialize(bytes)
                            .map_err(|e| MemHopError::Internal(format!("deserialize sparse: {}", e)))?;
                        cache.put(id.clone(), sparse);
                    }
                    Ok(None) => {}
                    Err(e) => return Err(MemHopError::Storage(format!("get forward: {}", e))),
                }
            }
        }
        Ok(())
    }

    /// Coarse screening: given query's sparse weights, return candidate IDs
    /// sorted by sparse score (dot product of shared ngram weights) descending.
    pub fn search(
        &self,
        query_sparse: &HashMap<String, f32>,
        max_candidates: usize,
        rtxn: &RoTxn<'_>,
    ) -> Result<Vec<String>> {
        let mut scores: HashMap<String, f32> = HashMap::new();

        for (ngram, q_weight) in query_sparse {
            if let Some(doc_ids) = self.inverted.get(ngram) {
                for doc_id in doc_ids {
                    // Load forward index from LMDB
                    if let Some(doc_sparse) = self.load_forward(doc_id, rtxn)?
                        && let Some(d_weight) = doc_sparse.get(ngram) 
                    {
                        *scores.entry(doc_id.clone()).or_insert(0.0) += q_weight * d_weight;
                    }
                }
            }
        }

        let mut candidates: Vec<(String, f32)> = scores.into_iter().collect();
        candidates.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
        candidates.truncate(max_candidates);
        Ok(candidates.into_iter().map(|(id, _)| id).collect())
    }

    /// BM25 search with LMDB-backed forward index.
    pub fn bm25_search(
        &self,
        query_sparse: &HashMap<String, f32>,
        idf: &HashMap<String, f32>,
        max_candidates: usize,
        rtxn: &RoTxn<'_>,
    ) -> Result<Vec<(String, f32)>> {
        // Edge case: empty index
        if self.doc_len.is_empty() {
            return Ok(Vec::new());
        }
        let avg_len = self.avg_doc_len();
        if avg_len <= 0.0 {
            return Ok(Vec::new());
        }

        let mut scores: HashMap<String, f32> = HashMap::new();

        for ngram in query_sparse.keys() {
            let idf_w = idf.get(ngram).copied().unwrap_or(1.0);
            if let Some(doc_ids) = self.inverted.get(ngram) {
                for doc_id in doc_ids {
                    // Load forward index from LMDB
                    if let Some(doc_sparse) = self.load_forward(doc_id, rtxn)?
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
        candidates.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
        candidates.truncate(max_candidates);
        Ok(candidates)
    }

    /// Average document length (used by BM25 for length normalization).
    pub fn avg_doc_len(&self) -> f32 {
        let n = self.doc_len.len();
        if n == 0 {
            return 0.0;
        }
        let total: usize = self.doc_len.values().sum();
        total as f32 / n as f32
    }

    /// Compute IDF map from current index state.
    pub fn idf_map(&self) -> HashMap<String, f32> {
        let n = self.doc_len.len() as f32;
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

    /// Number of indexed memories.
    pub fn len(&self) -> usize {
        self.doc_len.len()
    }

    pub fn is_empty(&self) -> bool {
        self.doc_len.is_empty()
    }

    /// Rebuild inverted index and doc_len from LMDB forward database.
    /// Used during startup to restore in-memory state.
    pub fn rebuild_from_lmdb(&mut self, rtxn: &RoTxn<'_>) -> Result<()> {
        // Clear cache on rebuild
        if let Ok(mut cache) = self.forward_cache.lock() {
            cache.clear();
        }
        
        if let Some(db) = self.forward_db {
            let iter = db.iter(rtxn)
                .map_err(|e| MemHopError::Storage(format!("iter forward: {}", e)))?;
            
            for result in iter {
                let (id, sparse_bytes) = result
                    .map_err(|e| MemHopError::Storage(format!("iter forward: {}", e)))?;
                
                let id = id.to_string();
                let sparse: HashMap<String, f32> = bincode::deserialize(sparse_bytes)
                    .map_err(|e| MemHopError::Internal(format!("deserialize sparse: {}", e)))?;
                
                // Rebuild inverted index
                for ngram in sparse.keys() {
                    self.inverted
                        .entry(ngram.clone())
                        .or_default()
                        .insert(id.clone());
                }
                
                // We don't have doc_len in forward DB, so we estimate from sparse weights
                // This is a limitation - doc_len should be stored separately
                let estimated_doc_len = sparse.values().map(|v| *v as usize).sum::<usize>().max(1);
                self.doc_len.insert(id, estimated_doc_len);
            }
        }
        Ok(())
    }

    /// Get cache statistics (for monitoring).
    pub fn cache_stats(&self) -> (usize, usize) {
        if let Ok(cache) = self.forward_cache.lock() {
            (cache.len(), cache.cap().get())
        } else {
            (0, 0)
        }
    }
}

impl std::fmt::Debug for SparseIndexV2 {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("SparseIndexV2")
            .field("inverted_len", &self.inverted.len())
            .field("doc_len_len", &self.doc_len.len())
            .field("has_forward_db", &self.forward_db.is_some())
            .finish()
    }
}
