// BM25 sparse index module
use serde::{Deserialize, Serialize};
use std::collections::HashMap;

/// Posting list for a term in the inverted index
#[derive(Serialize, Deserialize, Debug, Clone)]
pub struct PostingList {
    pub term_freq: HashMap<u64, u32>, // id_hash -> term frequency
    pub doc_freq: u32,                // number of documents containing this term
}

impl PostingList {
    pub fn new() -> Self {
        Self {
            term_freq: HashMap::new(),
            doc_freq: 0,
        }
    }
}

/// BM25 sparse index for text search
#[derive(Serialize, Deserialize, Debug, Clone)]
pub struct SparseIndex {
    k1: f32,                                // Term frequency saturation parameter (default 1.2)
    b: f32,                                 // Length normalization parameter (default 0.75)
    postings: HashMap<String, PostingList>, // term -> posting list
    doc_lengths: HashMap<u64, u32>,         // id_hash -> document length (number of terms)
    avg_doc_length: f32,                    // average document length
    total_docs: u32,                        // total number of documents
    total_term_count: u64,                  // sum of all document lengths for calculating avg
}

impl SparseIndex {
    /// Create a new SparseIndex with default BM25 parameters
    pub fn new() -> Self {
        Self {
            k1: 1.2,
            b: 0.75,
            postings: HashMap::new(),
            doc_lengths: HashMap::new(),
            avg_doc_length: 0.0,
            total_docs: 0,
            total_term_count: 0,
        }
    }

    /// Create a new SparseIndex with custom BM25 parameters
    pub fn with_params(k1: f32, b: f32) -> Self {
        Self {
            k1,
            b,
            postings: HashMap::new(),
            doc_lengths: HashMap::new(),
            avg_doc_length: 0.0,
            total_docs: 0,
            total_term_count: 0,
        }
    }

    /// Simple tokenizer: split by whitespace and convert to lowercase
    pub fn tokenize(text: &str) -> Vec<String> {
        text.split_whitespace().map(|s| s.to_lowercase()).collect()
    }

    /// Add a document to the index
    /// id_hash: unique identifier for the document
    /// terms: pre-tokenized terms
    /// doc_len: document length (number of terms)
    pub fn add_document(&mut self, id_hash: u64, terms: Vec<String>, doc_len: u32) {
        // If document already exists, remove it first
        if self.doc_lengths.contains_key(&id_hash) {
            self.remove_document(id_hash);
        }

        // Update document statistics
        self.doc_lengths.insert(id_hash, doc_len);
        self.total_docs += 1;
        self.total_term_count += doc_len as u64;
        self.avg_doc_length = self.total_term_count as f32 / self.total_docs as f32;

        // Calculate term frequencies
        let mut term_freq_map: HashMap<String, u32> = HashMap::new();
        for term in &terms {
            *term_freq_map.entry(term.clone()).or_insert(0) += 1;
        }

        // Update posting lists
        for (term, tf) in term_freq_map {
            let posting = self.postings.entry(term).or_insert_with(PostingList::new);
            posting.term_freq.insert(id_hash, tf);
            posting.doc_freq += 1;
        }
    }

    /// Remove a document from the index
    pub fn remove_document(&mut self, id_hash: u64) {
        if let Some(&doc_len) = self.doc_lengths.get(&id_hash) {
            // Update statistics
            self.total_docs -= 1;
            self.total_term_count -= doc_len as u64;
            self.avg_doc_length = if self.total_docs > 0 {
                self.total_term_count as f32 / self.total_docs as f32
            } else {
                0.0
            };

            // Remove from posting lists
            for posting in self.postings.values_mut() {
                if posting.term_freq.remove(&id_hash).is_some() {
                    posting.doc_freq -= 1;
                }
            }

            // Remove empty posting lists
            self.postings.retain(|_, v| v.doc_freq > 0);

            self.doc_lengths.remove(&id_hash);
        }
    }

    /// Calculate IDF (Inverse Document Frequency) for a term
    /// Formula: ln((N - n(qi) + 0.5) / (n(qi) + 0.5) + 1.0)
    fn idf(&self, doc_freq: u32) -> f32 {
        let n = doc_freq as f32;
        let n_total = self.total_docs as f32;
        ((n_total - n + 0.5) / (n + 0.5) + 1.0).ln()
    }

    /// Calculate BM25 score for a document given query terms
    /// Formula: Σ IDF(qi) × (tf × (k1+1)) / (tf + k1 × (1 - b + b × |d|/avgdl))
    pub fn bm25_score(&self, query_terms: &[String], doc_id_hash: u64) -> f32 {
        let doc_len = match self.doc_lengths.get(&doc_id_hash) {
            Some(&len) => len as f32,
            None => return 0.0,
        };

        let mut score = 0.0_f32;

        for term in query_terms {
            if let Some(posting) = self.postings.get(term) {
                if let Some(&tf) = posting.term_freq.get(&doc_id_hash) {
                    let idf = self.idf(posting.doc_freq);
                    let tf_normalized = (tf as f32 * (self.k1 + 1.0))
                        / (tf as f32
                            + self.k1 * (1.0 - self.b + self.b * doc_len / self.avg_doc_length));
                    score += idf * tf_normalized;
                }
            }
        }

        score
    }

    /// Search for documents matching query terms, returns top-k results
    pub fn search(&self, query_terms: &[String], k: usize) -> Vec<(u64, f32)> {
        let mut scores: Vec<(u64, f32)> = self
            .doc_lengths
            .keys()
            .map(|&id_hash| (id_hash, self.bm25_score(query_terms, id_hash)))
            .filter(|(_, score)| *score > 0.0)
            .collect();

        // Sort by score descending
        scores.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));

        // Return top-k
        scores.truncate(k);
        scores
    }

    /// Get the number of documents in the index
    pub fn len(&self) -> usize {
        self.total_docs as usize
    }

    /// Check if the index is empty
    pub fn is_empty(&self) -> bool {
        self.total_docs == 0
    }

    /// Get top-N most frequent terms in the index
    ///
    /// Returns a vector of (term, document_frequency) tuples sorted by frequency descending.
    /// This is useful for generating user profiles or identifying key topics.
    ///
    /// # Arguments
    /// * `n` - Number of top terms to return
    ///
    /// # Returns
    /// Vector of (term, doc_freq) pairs sorted by document frequency (descending)
    pub fn top_terms(&self, n: usize) -> Vec<(String, u32)> {
        let mut term_freqs: Vec<(String, u32)> = self
            .postings
            .iter()
            .map(|(term, posting)| (term.clone(), posting.doc_freq))
            .collect();

        // Sort by document frequency descending
        term_freqs.sort_by_key(|b| std::cmp::Reverse(b.1));

        // Return top-n
        term_freqs.truncate(n);
        term_freqs
    }

    /// Serialize SparseIndex to binary format using bincode
    pub fn serialize(&self) -> Result<Vec<u8>, String> {
        bincode::serialize(self).map_err(|e| format!("Serialization failed: {}", e))
    }

    /// Deserialize SparseIndex from binary format using bincode
    pub fn deserialize(data: &[u8]) -> Result<Self, String> {
        bincode::deserialize(data).map_err(|e| format!("Deserialization failed: {}", e))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_tokenize() {
        let tokens = SparseIndex::tokenize("Hello World hello");
        assert_eq!(tokens, vec!["hello", "world", "hello"]);
    }

    #[test]
    fn test_tokenize_case_insensitive() {
        let tokens = SparseIndex::tokenize("The Quick Brown Fox");
        assert_eq!(tokens, vec!["the", "quick", "brown", "fox"]);
    }

    #[test]
    fn test_add_and_remove_document() {
        let mut index = SparseIndex::new();

        let terms = SparseIndex::tokenize("machine learning is great");
        index.add_document(1, terms.clone(), terms.len() as u32);

        assert_eq!(index.len(), 1);
        assert_eq!(index.doc_lengths.get(&1), Some(&4));

        index.remove_document(1);
        assert_eq!(index.len(), 0);
        assert!(index.is_empty());
    }

    #[test]
    fn test_bm25_idf_rare_term() {
        // Rare terms should have higher IDF
        let mut index = SparseIndex::new();

        // Add 10 documents with common term
        for i in 0..10 {
            let terms = vec!["common".to_string()];
            index.add_document(i, terms, 1);
        }

        // Add 1 document with rare term
        index.add_document(100, vec!["rare".to_string()], 1);

        // IDF for "common" (appears in 10 docs)
        let idf_common = index.idf(10);
        // IDF for "rare" (appears in 1 doc)
        let idf_rare = index.idf(1);

        assert!(idf_rare > idf_common, "Rare term should have higher IDF");
    }

    #[test]
    fn test_bm25_score_basic() {
        let mut index = SparseIndex::new();

        // Document 1: about machine learning
        let terms1 = SparseIndex::tokenize("machine learning algorithms");
        let doc_len1 = terms1.len() as u32;
        index.add_document(1, terms1, doc_len1);

        // Document 2: about deep learning
        let terms2 = SparseIndex::tokenize("deep learning neural networks");
        let doc_len2 = terms2.len() as u32;
        index.add_document(2, terms2, doc_len2);

        // Query for "machine learning"
        let query = vec!["machine".to_string(), "learning".to_string()];

        let score1 = index.bm25_score(&query, 1);
        let score2 = index.bm25_score(&query, 2);

        // Document 1 should score higher as it contains "machine"
        assert!(score1 > score2, "Doc 1 should score higher than Doc 2");
        assert!(score1 > 0.0, "Score should be positive");
    }

    #[test]
    fn test_bm25_term_frequency_effect() {
        let mut index = SparseIndex::new();

        // Document with term appearing once
        let terms1 = vec!["python".to_string()];
        let doc_len1 = terms1.len() as u32;
        index.add_document(1, terms1, doc_len1);

        // Document with term appearing multiple times
        let terms2 = vec!["python".to_string(); 5];
        let doc_len2 = terms2.len() as u32;
        index.add_document(2, terms2, doc_len2);

        let query = vec!["python".to_string()];

        let score1 = index.bm25_score(&query, 1);
        let score2 = index.bm25_score(&query, 2);

        // Higher term frequency should give higher score (but with saturation)
        assert!(score2 > score1, "Higher TF should give higher score");
    }

    #[test]
    fn test_bm25_document_length_normalization() {
        let mut index = SparseIndex::new();

        // Short document with query term
        let terms1 = vec!["ai".to_string()];
        let doc_len1 = terms1.len() as u32;
        index.add_document(1, terms1, doc_len1);

        // Long document with query term (same TF but longer)
        let mut terms2 = vec!["ai".to_string()];
        for _ in 0..9 {
            terms2.push("other".to_string());
        }
        let doc_len2 = terms2.len() as u32;
        index.add_document(2, terms2, doc_len2);

        let query = vec!["ai".to_string()];

        let score1 = index.bm25_score(&query, 1);
        let score2 = index.bm25_score(&query, 2);

        // Shorter document should score higher due to length normalization
        assert!(score1 > score2, "Shorter document should score higher");
    }

    #[test]
    fn test_search_top_k() {
        let mut index = SparseIndex::new();

        for i in 0..5 {
            let terms = SparseIndex::tokenize(&format!("document number {}", i));
            let doc_len = terms.len() as u32;
            index.add_document(i as u64, terms, doc_len);
        }

        let query = SparseIndex::tokenize("document");
        let results = index.search(&query, 3);

        assert_eq!(results.len(), 3, "Should return top-3 results");
        // All documents contain "document", so they should all have similar scores
        for (_, score) in &results {
            assert!(*score > 0.0);
        }
    }

    #[test]
    fn test_bm25_formula_verification() {
        // Verify BM25 formula matches specification
        let mut index = SparseIndex::with_params(1.2, 0.75);

        let terms = vec!["test".to_string(), "term".to_string()];
        index.add_document(1, terms.clone(), terms.len() as u32);

        let query = vec!["test".to_string()];
        let score = index.bm25_score(&query, 1);

        // Manual calculation:
        // IDF = ln((1 - 1 + 0.5) / (1 + 0.5) + 1.0) = ln(0.5/1.5 + 1.0) = ln(1.333) ≈ 0.288
        // TF component = (1 * 2.2) / (1 + 1.2 * (1 - 0.75 + 0.75 * 2/2)) = 2.2 / (1 + 1.2) = 2.2/2.2 = 1.0
        // Score = 0.288 * 1.0 ≈ 0.288

        assert!(
            score > 0.25 && score < 0.35,
            "BM25 score should be approximately 0.288, got {}",
            score
        );
    }

    #[test]
    fn test_serialize_deserialize_empty() {
        let index = SparseIndex::new();
        let serialized = index.serialize().unwrap();
        let deserialized = SparseIndex::deserialize(&serialized).unwrap();

        assert_eq!(deserialized.len(), 0);
        assert!(deserialized.is_empty());
    }

    #[test]
    fn test_serialize_deserialize_with_documents() {
        let mut index = SparseIndex::new();

        // Add some documents
        let terms1 = SparseIndex::tokenize("machine learning is great");
        index.add_document(1, terms1.clone(), terms1.len() as u32);

        let terms2 = SparseIndex::tokenize("deep learning neural networks");
        index.add_document(2, terms2.clone(), terms2.len() as u32);

        let terms3 = SparseIndex::tokenize("artificial intelligence algorithms");
        index.add_document(3, terms3.clone(), terms3.len() as u32);

        // Serialize and deserialize
        let serialized = index.serialize().unwrap();
        let deserialized = SparseIndex::deserialize(&serialized).unwrap();

        // Verify data integrity
        assert_eq!(deserialized.len(), 3);

        // Test BM25 scoring consistency
        let query = SparseIndex::tokenize("learning");
        let original_score_1 = index.bm25_score(&query, 1);
        let restored_score_1 = deserialized.bm25_score(&query, 1);

        assert!(
            (original_score_1 - restored_score_1).abs() < 1e-6,
            "BM25 scores should match after deserialization"
        );

        let original_score_2 = index.bm25_score(&query, 2);
        let restored_score_2 = deserialized.bm25_score(&query, 2);

        assert!(
            (original_score_2 - restored_score_2).abs() < 1e-6,
            "BM25 scores should match after deserialization"
        );
    }

    #[test]
    fn test_top_terms_basic() {
        let mut index = SparseIndex::new();

        // Add documents with different term frequencies
        index.add_document(1, vec!["rust".to_string()], 1);
        index.add_document(2, vec!["rust".to_string(), "programming".to_string()], 2);
        index.add_document(3, vec!["rust".to_string(), "programming".to_string(), "language".to_string()], 3);
        index.add_document(4, vec!["python".to_string(), "programming".to_string()], 2);

        // Get top 3 terms
        let top = index.top_terms(3);

        assert_eq!(top.len(), 3);
        // "rust" and "programming" both appear in 3 docs, "python" and "language" in 1 doc each
        // The order between rust and programming may vary due to HashMap iteration order
        assert_eq!(top[0].1, 3); // First should have freq 3
        assert_eq!(top[1].1, 3); // Second should also have freq 3
        assert!(top[2].1 == 1); // Third should have freq 1
    }

    #[test]
    fn test_top_terms_empty_index() {
        let index = SparseIndex::new();
        let top = index.top_terms(5);
        assert!(top.is_empty());
    }

    #[test]
    fn test_top_terms_limit() {
        let mut index = SparseIndex::new();

        for i in 0..10 {
            index.add_document(i, vec![format!("term{}", i)], 1);
        }

        // Request only top 3 from 10 terms
        let top = index.top_terms(3);
        assert_eq!(top.len(), 3);
    }
}
