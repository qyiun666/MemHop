// BM25 sparse index module
use crate::index::btree::BTreeIndex;
use crate::l3::store::page_type_of;
use crate::query::slot_io::get_slot_data;
use crate::slot::context::ContextSlot;
use crate::slot::hypergraph::HypergraphNode;
use crate::slot::profile::ProfileSlot;
use crate::util::{hash_id, PageType, PAGE_SIZE};
use crate::MemHopError;
use jieba_rs::Jieba;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::sync::OnceLock;

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

impl Default for PostingList {
    fn default() -> Self {
        Self::new()
    }
}

/// Magic number identifying the new multi-page SparseIndex directory page.
pub const SPARSE_MAGIC: u32 = 0x4D485350; // "MHSP"

/// Number of hash buckets used for term and doc-length storage.
/// Fixed at 256 so the directory page (which stores the primary page id of
/// each bucket chain) comfortably fits within a single 4KB page.
pub const SPARSE_BUCKET_COUNT: u32 = 256;

/// Maximum payload bytes per page (4KB page minus 32-byte header).
pub const SPARSE_PAGE_PAYLOAD: usize = PAGE_SIZE - 32;

/// 中英文停用词列表
const STOP_WORDS: &[&str] = &[
    // 英文停用词
    "the", "a", "an", "is", "are", "was", "were", "be", "been", "being", "have", "has", "had",
    "do", "does", "did", "will", "would", "could", "should", "may", "might", "can", "shall", "to",
    "of", "in", "for", "on", "with", "at", "by", "from", "as", "into", "through", "during",
    "before", "after", "above", "below", "between", "out", "off", "over", "under", "again",
    "further", "then", "once", "here", "there", "when", "where", "why", "how", "all", "both",
    "each", "few", "more", "most", "other", "some", "such", "no", "nor", "not", "only", "own",
    "same", "so", "than", "too", "very", "just", "and", "but", "if", "or", "because", "until",
    "while", "this", "that", "these", "those", "i", "me", "my", "we", "our", "you", "your",
    "he", "him", "his", "she", "her", "it", "its", "they", "them", "their", "what", "which",
    "who",
    // 中文停用词
    "的", "了", "在", "是", "我", "有", "和", "就", "不", "人", "都", "一", "一个", "上", "也",
    "很", "到", "说", "要", "去", "你", "会", "着", "没有", "看", "好", "自己", "这", "他", "她",
    "它", "们", "那", "些", "什么", "怎么", "为什么", "哪", "谁", "吗", "呢", "吧", "啊", "哦",
    "嗯", "把", "被", "让", "给", "呀",
];

fn jieba() -> &'static Jieba {
    static JIEBA: OnceLock<Jieba> = OnceLock::new();
    JIEBA.get_or_init(Jieba::new)
}

fn has_cjk(text: &str) -> bool {
    text.chars().any(|c| {
        ('\u{4E00}'..='\u{9FFF}').contains(&c)
            || ('\u{3400}'..='\u{4DBF}').contains(&c)
            || ('\u{3040}'..='\u{30FF}').contains(&c)
    })
}

fn is_stop_word(word: &str) -> bool {
    STOP_WORDS.contains(&word)
}

fn tokenize_cjk(text: &str, keep_underscore: bool, filter_stop_words: bool) -> Vec<String> {
    jieba()
        .cut(text, true)
        .into_iter()
        .map(|w| w.trim().to_lowercase())
        .map(|w| {
            if keep_underscore {
                w.trim_matches(|c: char| !c.is_alphanumeric() && c != '_')
                    .to_string()
            } else {
                w.trim_matches(|c: char| !c.is_alphanumeric()).to_string()
            }
        })
        .filter(|w| !w.is_empty() && (!filter_stop_words || !is_stop_word(w)))
        .collect()
}

/// 智能分词：自动检测中文/英文，并过滤停用词
///
/// 用于关键词提取等需要清洗停用词的场景。
pub fn tokenize(text: &str) -> Vec<String> {
    if has_cjk(text) {
        tokenize_cjk(text, false, true)
    } else {
        text.split_whitespace()
            .map(|s| s.trim_matches(|c: char| !c.is_alphanumeric()).to_lowercase())
            .filter(|s| !s.is_empty() && !is_stop_word(s))
            .collect()
    }
}

/// Page-oriented serialization output for the SparseIndex.
///
/// The caller (e.g. `MemHop::checkpoint`) is responsible for allocating file
/// pages, writing the bucket/entity payloads, linking overflow pages, and
/// building the directory page that maps term/doc hashes to bucket chains.
#[derive(Debug, Clone)]
pub struct SparsePageData {
    pub term_bucket_count: u32,
    pub doc_bucket_count: u32,
    pub term_count: u32,
    pub doc_count: u32,
    pub total_term_count: u64,
    pub avg_doc_length: f32,
    pub k1: f32,
    pub b: f32,
    /// For each bucket, the chain of page payloads that hold the bincode-encoded
    /// `Vec<(String, PostingList)>` for that bucket (prefixed with a length header).
    pub term_buckets: Vec<Vec<Vec<u8>>>,
    /// For each bucket, the chain of page payloads that hold the bincode-encoded
    /// `Vec<(u64, u32)>` for that bucket (prefixed with a length header).
    pub doc_buckets: Vec<Vec<Vec<u8>>>,
    /// Chain of page payloads for the bincode-encoded `EntityIndex`
    /// (prefixed with a length header).
    pub entity_chain: Vec<Vec<u8>>,
}

/// Build the directory page payload from metadata and allocated primary page ids.
pub fn build_sparse_directory(
    page_data: &SparsePageData,
    term_starts: &[u32],
    doc_starts: &[u32],
    entity_start: u32,
) -> Vec<u8> {
    let mut dir = Vec::with_capacity(PAGE_SIZE);
    dir.extend_from_slice(&SPARSE_MAGIC.to_le_bytes());
    dir.extend_from_slice(&page_data.term_bucket_count.to_le_bytes());
    dir.extend_from_slice(&page_data.doc_bucket_count.to_le_bytes());
    dir.extend_from_slice(&page_data.term_count.to_le_bytes());
    dir.extend_from_slice(&page_data.doc_count.to_le_bytes());
    dir.extend_from_slice(&page_data.total_term_count.to_le_bytes());
    dir.extend_from_slice(&page_data.avg_doc_length.to_le_bytes());
    dir.extend_from_slice(&page_data.k1.to_le_bytes());
    dir.extend_from_slice(&page_data.b.to_le_bytes());
    dir.extend_from_slice(&entity_start.to_le_bytes());
    for &p in term_starts {
        dir.extend_from_slice(&p.to_le_bytes());
    }
    for &p in doc_starts {
        dir.extend_from_slice(&p.to_le_bytes());
    }
    dir
}

fn chunk_into_pages(data: &[u8]) -> Vec<Vec<u8>> {
    data.chunks(SPARSE_PAGE_PAYLOAD)
        .map(|c| c.to_vec())
        .collect()
}

fn wrap_and_chunk(data: &[u8]) -> Vec<Vec<u8>> {
    if data.is_empty() {
        return Vec::new();
    }
    let mut full = Vec::with_capacity(4 + data.len());
    full.extend_from_slice(&(data.len() as u32).to_le_bytes());
    full.extend_from_slice(data);
    chunk_into_pages(&full)
}

fn unwrap_bucket_bytes(pages: &[Vec<u8>]) -> Result<Vec<u8>, String> {
    if pages.is_empty() {
        return Ok(Vec::new());
    }
    let mut full = Vec::new();
    for page in pages {
        full.extend_from_slice(page);
    }
    if full.len() < 4 {
        return Err("Bucket data too short for length header".to_string());
    }
    let len = u32::from_le_bytes([full[0], full[1], full[2], full[3]]) as usize;
    let end = 4 + len;
    if end > full.len() {
        return Err(format!(
            "Bucket length header exceeds available data: {} > {}",
            end,
            full.len()
        ));
    }
    Ok(full[4..end].to_vec())
}

// ============================================================================
// BK-Tree for fuzzy entity matching
// ============================================================================

/// BK-Tree node: stores a word and maps edit-distance buckets to child indices.
#[derive(Serialize, Deserialize, Debug, Clone)]
struct BkNode {
    word: String,
    children: HashMap<usize, usize>, // edit_distance -> child node index
}

/// Simplified BK-Tree for fast approximate string matching.
#[derive(Serialize, Deserialize, Debug, Clone, Default)]
struct BkTree {
    nodes: Vec<BkNode>,
}

impl BkTree {
    fn new() -> Self {
        Self { nodes: Vec::new() }
    }

    /// Insert a word into the BK-Tree.
    fn insert(&mut self, word: String) {
        if self.nodes.is_empty() {
            self.nodes.push(BkNode {
                word,
                children: HashMap::new(),
            });
            return;
        }
        // Skip duplicates to avoid creating O(n) depth-0 chains.
        if self.contains(&word) {
            return;
        }
        self.insert_recursive(0, &word);
    }

    fn contains(&self, word: &str) -> bool {
        if self.nodes.is_empty() {
            return false;
        }
        self.contains_recursive(0, word)
    }

    fn contains_recursive(&self, node_idx: usize, word: &str) -> bool {
        let dist = levenshtein_distance(&self.nodes[node_idx].word, word);
        if dist == 0 {
            return true;
        }
        if let Some(&next_idx) = self.nodes[node_idx].children.get(&dist) {
            self.contains_recursive(next_idx, word)
        } else {
            false
        }
    }

    fn insert_recursive(&mut self, node_idx: usize, word: &str) {
        let dist = levenshtein_distance(&self.nodes[node_idx].word, word);
        if let Some(&next_idx) = self.nodes[node_idx].children.get(&dist) {
            self.insert_recursive(next_idx, word);
        } else {
            let new_idx = self.nodes.len();
            self.nodes.push(BkNode {
                word: word.to_string(),
                children: HashMap::new(),
            });
            self.nodes[node_idx].children.insert(dist, new_idx);
        }
    }

    /// Search for words within `max_distance` edit distance of `word`.
    fn search(&self, word: &str, max_distance: usize) -> Vec<(String, usize)> {
        let mut results = Vec::new();
        if self.nodes.is_empty() {
            return results;
        }
        self.search_recursive(0, word, max_distance, &mut results);
        results
    }

    fn search_recursive(
        &self,
        node_idx: usize,
        word: &str,
        max_distance: usize,
        results: &mut Vec<(String, usize)>,
    ) {
        let node = &self.nodes[node_idx];
        let dist = levenshtein_distance(&node.word, word);
        if dist <= max_distance {
            results.push((node.word.clone(), dist));
        }

        let min_dist = dist.saturating_sub(max_distance);
        let max_dist = dist + max_distance;

        for (&child_dist, &child_idx) in &node.children {
            if child_dist >= min_dist && child_dist <= max_dist {
                self.search_recursive(child_idx, word, max_distance, results);
            }
        }
    }
}

/// Compute Levenshtein edit distance between two strings (Unicode-aware).
fn levenshtein_distance(a: &str, b: &str) -> usize {
    let a_chars: Vec<char> = a.chars().collect();
    let b_chars: Vec<char> = b.chars().collect();
    let m = a_chars.len();
    let n = b_chars.len();

    if m == 0 {
        return n;
    }
    if n == 0 {
        return m;
    }

    let mut prev: Vec<usize> = (0..=n).collect();
    let mut curr = vec![0; n + 1];

    for i in 1..=m {
        curr[0] = i;
        for j in 1..=n {
            let cost = if a_chars[i - 1] == b_chars[j - 1] {
                0
            } else {
                1
            };
            curr[j] = (prev[j] + 1).min(curr[j - 1] + 1).min(prev[j - 1] + cost);
        }
        std::mem::swap(&mut prev, &mut curr);
    }

    prev[n]
}

// ============================================================================
// Entity index: exact + fuzzy entity matching built from L3 hypergraphs
// ============================================================================

/// Entity dictionary backed by a BK-Tree for fuzzy matching.
#[derive(Serialize, Deserialize, Debug, Clone)]
pub struct EntityIndex {
    /// entity_name (lowercase) → (l3_node_hash, l2_id_hashes)
    entities: HashMap<String, (u64, Vec<u64>)>,
    /// BK-Tree for fuzzy matching
    bk_tree: BkTree,
    /// Reverse index: l3_node_hash → deduplicated l2_id_hashes (not serialized, rebuilt)
    #[serde(skip)]
    node_to_l2: HashMap<u64, Vec<u64>>,
}

impl EntityIndex {
    pub fn new() -> Self {
        Self {
            entities: HashMap::new(),
            bk_tree: BkTree::new(),
            node_to_l2: HashMap::new(),
        }
    }

    pub fn is_empty(&self) -> bool {
        self.entities.is_empty()
    }

    /// Add a single entity with its L3 node hash and associated L2 contexts.
    pub fn add_entity(&mut self, name: &str, node_hash: u64, l2_ids: Vec<u64>) {
        let key = name.to_lowercase();
        // Update reverse index
        let entry = self.node_to_l2.entry(node_hash).or_default();
        for l2_id in &l2_ids {
            if !entry.contains(l2_id) {
                entry.push(*l2_id);
            }
        }
        self.entities.insert(key.clone(), (node_hash, l2_ids));
        self.bk_tree.insert(key);
    }

    /// Add L0 lexicon words as additional entity surface forms.
    pub fn add_lexicon(&mut self, words: &[String]) {
        for word in words {
            let key = word.to_lowercase();
            if !self.entities.contains_key(&key) {
                self.entities.insert(key.clone(), (0, Vec::new()));
                self.bk_tree.insert(key);
            }
        }
    }

    /// Exact match lookup (case-insensitive).
    pub fn exact_match(&self, term: &str) -> Option<(u64, Vec<u64>)> {
        self.entities.get(&term.to_lowercase()).cloned()
    }

    /// Fuzzy match lookup: returns (word, distance, node_hash, l2_ids).
    pub fn fuzzy_match(
        &self,
        term: &str,
        max_distance: usize,
    ) -> Vec<(String, usize, u64, Vec<u64>)> {
        let mut results = Vec::new();
        for (word, dist) in self.bk_tree.search(term, max_distance) {
            if let Some(&(node_hash, ref l2_ids)) = self.entities.get(&word) {
                results.push((word, dist, node_hash, l2_ids.clone()));
            }
        }
        results
    }

    /// Recognize entities in free text. Returns (entity_name, score, l2_ids).
    ///
    /// Score: exact match = 1.0, fuzzy match = 1.0 / (1 + edit_distance).
    /// Tries both single words and adjacent word pairs to catch multi-word
    /// entity names.
    pub fn recognize_entities(&self, text: &str) -> Vec<(String, f32, Vec<u64>)> {
        let words = tokenize_words(text);
        let mut tokens = words.clone();
        for i in 0..words.len().saturating_sub(1) {
            tokens.push(format!("{} {}", words[i], words[i + 1]));
        }

        let mut best_scores: HashMap<String, f32> = HashMap::new();

        for token in tokens {
            for (word, dist, _node_hash, _l2_ids) in self.fuzzy_match(&token, 2) {
                let score = 1.0f32 / (1.0 + dist as f32);
                let entry = best_scores.entry(word).or_insert(0.0);
                if score > *entry {
                    *entry = score;
                }
            }
        }

        best_scores
            .into_iter()
            .filter_map(|(word, score)| {
                self.entities
                    .get(&word)
                    .map(|(_, l2_ids)| (word, score, l2_ids.clone()))
            })
            .collect()
    }

    /// Build the entity dictionary from L3 hypergraph nodes and L2 context
    /// associations, then add L0 profile lexicon words.
    ///
    /// Returns the collected L3 nodes for BM25 indexing (to avoid duplicate BTree scans).
    pub fn build_from_l3(&mut self, data: &[u8], btree: &BTreeIndex) -> Result<Vec<(u64, String, Vec<String>)>, MemHopError> {
        // Collect L3 nodes grouped by graph.
        let mut nodes_by_graph: HashMap<u64, Vec<(u64, String, Vec<String>)>> = HashMap::new();
        let mut all_nodes: Vec<(u64, String, Vec<String>)> = Vec::new();
        for (&_id_hash, &page_ref) in btree.iter() {
            if page_type_of(data, page_ref) != Some(PageType::HypergraphNode as u16) {
                continue;
            }
            if let Some(slot_data) = get_slot_data(data, page_ref) {
                if let Ok(node) = HypergraphNode::deserialize(slot_data) {
                    let node_info = (node.id_hash, node.title.clone(), node.keywords.clone());
                    nodes_by_graph
                        .entry(node.graph_id)
                        .or_default()
                        .push(node_info.clone());
                    all_nodes.push(node_info);
                }
            }
        }

        // Map each graph_id to the L2 contexts that reference it.
        let mut l2_by_graph: HashMap<u64, Vec<u64>> = HashMap::new();
        for (&_id_hash, &page_ref) in btree.iter() {
            if page_type_of(data, page_ref) != Some(PageType::Context as u16) {
                continue;
            }
            if let Some(slot_data) = get_slot_data(data, page_ref) {
                if let Ok(ctx) = ContextSlot::deserialize(slot_data) {
                    for &graph_hash in &ctx.l3_refs {
                        l2_by_graph.entry(graph_hash).or_default().push(ctx.id_hash);
                    }
                }
            }
        }

        // Register entities.
        for (graph_id, nodes) in nodes_by_graph {
            let l2_ids = l2_by_graph.get(&graph_id).cloned().unwrap_or_default();
            for (node_hash, title, keywords) in nodes {
                self.add_entity(&title, node_hash, l2_ids.clone());
                for kw in &keywords {
                    self.add_entity(kw, node_hash, l2_ids.clone());
                }
            }
        }

        // Add L0 profile lexicon words.
        let profile_hash = hash_id("profile");
        if let Some(page_ref) = btree.search(profile_hash) {
            if let Some(slot_data) = get_slot_data(data, page_ref) {
                if let Ok(profile) = ProfileSlot::deserialize(slot_data) {
                    let words: Vec<String> = profile.lexicon.keys().cloned().collect();
                    self.add_lexicon(&words);
                }
            }
        }

        Ok(all_nodes)
    }

    /// Find L2 context ids associated with a given L3 node hash.
    /// This is used to resolve BM25 hits on L3 virtual documents back to L2 contexts.
    /// Uses reverse index for O(1) lookup.
    pub fn l2_ids_for_node(&self, node_hash: u64) -> Vec<u64> {
        self.node_to_l2.get(&node_hash).cloned().unwrap_or_default()
    }
}

impl Default for EntityIndex {
    fn default() -> Self {
        Self::new()
    }
}

/// Simple word tokenizer for entity recognition: lowercase and strip punctuation.
fn tokenize_words(text: &str) -> Vec<String> {
    if has_cjk(text) {
        tokenize_cjk(text, true, false)
    } else {
        text.split_whitespace()
            .map(|s| {
                s.trim_matches(|c: char| !c.is_alphanumeric() && c != '_')
                    .to_lowercase()
            })
            .filter(|s| !s.is_empty())
            .collect()
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
    entity_index: EntityIndex,              // entity matching index
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
            entity_index: EntityIndex::new(),
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
            entity_index: EntityIndex::new(),
        }
    }

    /// Simple tokenizer: split by whitespace and convert to lowercase.
    ///
    /// For CJK text, uses jieba-rs for segmentation. English behavior is
    /// preserved exactly: no stop-word filtering or punctuation stripping.
    pub fn tokenize(text: &str) -> Vec<String> {
        if has_cjk(text) {
            tokenize_cjk(text, false, false)
        } else {
            text.split_whitespace().map(|s| s.to_lowercase()).collect()
        }
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
            let posting = self.postings.entry(term).or_default();
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

    /// Search for documents matching query terms, returns top-k results.
    ///
    /// Uses the inverted index to collect candidate documents first, then
    /// scores only those candidates with BM25. Complexity drops from
    /// O(N * Q) to O(Q * avg_posting_length).
    pub fn search(&self, query_terms: &[String], k: usize) -> Vec<(u64, f32)> {
        // 1. Collect candidate documents from the union of query term postings.
        let mut candidates: std::collections::HashSet<u64> = std::collections::HashSet::new();
        for term in query_terms {
            if let Some(posting) = self.postings.get(term) {
                for &doc_id in posting.term_freq.keys() {
                    candidates.insert(doc_id);
                }
            }
        }

        // 2. Score only candidate documents.
        let mut scores: Vec<(u64, f32)> = candidates
            .iter()
            .map(|&id_hash| (id_hash, self.bm25_score(query_terms, id_hash)))
            .filter(|(_, score)| *score > 0.0)
            .collect();

        // Sort by score descending
        scores.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));

        // Return top-k
        scores.truncate(k);
        scores
    }

    /// Build the entity index from L3 hypergraph nodes and L2 context associations.
    pub fn build_entity_index(
        &mut self,
        data: &[u8],
        btree: &BTreeIndex,
    ) -> Result<(), MemHopError> {
        // Build entity index and get collected L3 nodes
        let l3_nodes = self.entity_index.build_from_l3(data, btree)?;

        // Add L3 node virtual documents to BM25 index (using collected nodes, no second scan)
        for (node_hash, title, keywords) in l3_nodes {
            let doc_terms: Vec<String> = std::iter::once(title)
                .chain(keywords)
                .flat_map(|s| SparseIndex::tokenize(&s))
                .collect();
            let doc_len = doc_terms.len() as u32;
            self.add_document(node_hash, doc_terms, doc_len);
        }

        Ok(())
    }

    /// Search for L2 contexts using entity matching.
    ///
    /// Returns `(l2_id_hash, score)` pairs sorted by score descending.
    pub fn entity_search(&self, query: &str) -> Vec<(u64, f32)> {
        let entities = self.entity_index.recognize_entities(query);
        let mut scores: HashMap<u64, f32> = HashMap::new();

        for (_name, score, l2_ids) in entities {
            for l2_id in l2_ids {
                *scores.entry(l2_id).or_insert(0.0) += score;
            }
        }

        let mut results: Vec<(u64, f32)> = scores.into_iter().collect();
        results.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
        results
    }

    /// Search for L3 entity nodes using fuzzy matching.
    ///
    /// Returns `(node_hash, l2_ids)` pairs for each matched L3 entity node.
    /// Unlike `entity_search`, this returns the actual L3 node hashes.
    pub fn entity_search_nodes(&self, query: &str) -> Vec<(u64, Vec<u64>)> {
        let words = crate::index::sparse::tokenize_words(query);
        let mut seen: HashMap<u64, Vec<u64>> = HashMap::new();

        for word in &words {
            for (_matched_word, _dist, node_hash, l2_ids) in self.entity_index.fuzzy_match(word, 2) {
                seen.entry(node_hash).or_insert(l2_ids);
            }
        }

        seen.into_iter().collect()
    }

    /// Check whether the entity index has been populated.
    pub fn has_entity_index(&self) -> bool {
        !self.entity_index.is_empty()
    }

    /// Get a reference to the entity index.
    pub fn entity_index(&self) -> &EntityIndex {
        &self.entity_index
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

    /// Serialize the SparseIndex into bucket-oriented page payloads.
    ///
    /// The returned `SparsePageData` does not contain file page ids; the caller
    /// allocates pages and builds the directory page via `build_sparse_directory`.
    pub fn serialize_to_pages(&self) -> Result<SparsePageData, String> {
        let term_bucket_count = if self.postings.is_empty() {
            0
        } else {
            SPARSE_BUCKET_COUNT
        };
        let doc_bucket_count = if self.doc_lengths.is_empty() {
            0
        } else {
            SPARSE_BUCKET_COUNT
        };

        let mut term_buckets: Vec<Vec<Vec<u8>>> = Vec::new();
        if term_bucket_count > 0 {
            let mut buckets: Vec<Vec<(String, PostingList)>> =
                vec![Vec::new(); term_bucket_count as usize];
            for (term, posting) in &self.postings {
                let idx = (hash_id(term) % term_bucket_count as u64) as usize;
                buckets[idx].push((term.clone(), posting.clone()));
            }
            for bucket in buckets {
                let bytes = if bucket.is_empty() {
                    Vec::new()
                } else {
                    bincode::serialize(&bucket)
                        .map_err(|e| format!("Term bucket serialization failed: {}", e))?
                };
                term_buckets.push(wrap_and_chunk(&bytes));
            }
        }

        let mut doc_buckets: Vec<Vec<Vec<u8>>> = Vec::new();
        if doc_bucket_count > 0 {
            let mut buckets: Vec<Vec<(u64, u32)>> = vec![Vec::new(); doc_bucket_count as usize];
            for (&doc_id, &len) in &self.doc_lengths {
                let idx = (doc_id % doc_bucket_count as u64) as usize;
                buckets[idx].push((doc_id, len));
            }
            for bucket in buckets {
                let bytes = if bucket.is_empty() {
                    Vec::new()
                } else {
                    bincode::serialize(&bucket)
                        .map_err(|e| format!("Doc bucket serialization failed: {}", e))?
                };
                doc_buckets.push(wrap_and_chunk(&bytes));
            }
        }

        let entity_chain = if self.entity_index.is_empty() {
            Vec::new()
        } else {
            let bytes = bincode::serialize(&self.entity_index)
                .map_err(|e| format!("Entity index serialization failed: {}", e))?;
            wrap_and_chunk(&bytes)
        };

        Ok(SparsePageData {
            term_bucket_count,
            doc_bucket_count,
            term_count: self.postings.len() as u32,
            doc_count: self.doc_lengths.len() as u32,
            total_term_count: self.total_term_count,
            avg_doc_length: self.avg_doc_length,
            k1: self.k1,
            b: self.b,
            term_buckets,
            doc_buckets,
            entity_chain,
        })
    }

    /// Deserialize a SparseIndex from bucket-oriented page payloads.
    pub fn deserialize_from_pages(page_data: &SparsePageData) -> Result<Self, String> {
        if page_data.term_buckets.len() != page_data.term_bucket_count as usize {
            return Err(format!(
                "Term bucket count mismatch: expected {}, got {}",
                page_data.term_bucket_count,
                page_data.term_buckets.len()
            ));
        }
        if page_data.doc_buckets.len() != page_data.doc_bucket_count as usize {
            return Err(format!(
                "Doc bucket count mismatch: expected {}, got {}",
                page_data.doc_bucket_count,
                page_data.doc_buckets.len()
            ));
        }

        let mut postings: HashMap<String, PostingList> =
            HashMap::with_capacity(page_data.term_count as usize);
        for bucket_pages in &page_data.term_buckets {
            let bytes = unwrap_bucket_bytes(bucket_pages)?;
            if bytes.is_empty() {
                continue;
            }
            let entries: Vec<(String, PostingList)> = bincode::deserialize(&bytes)
                .map_err(|e| format!("Term bucket deserialization failed: {}", e))?;
            for (term, posting) in entries {
                postings.insert(term, posting);
            }
        }

        let mut doc_lengths: HashMap<u64, u32> =
            HashMap::with_capacity(page_data.doc_count as usize);
        for bucket_pages in &page_data.doc_buckets {
            let bytes = unwrap_bucket_bytes(bucket_pages)?;
            if bytes.is_empty() {
                continue;
            }
            let entries: Vec<(u64, u32)> = bincode::deserialize(&bytes)
                .map_err(|e| format!("Doc bucket deserialization failed: {}", e))?;
            for (doc_id, len) in entries {
                doc_lengths.insert(doc_id, len);
            }
        }

        let entity_index = if page_data.entity_chain.is_empty() {
            EntityIndex::new()
        } else {
            let bytes = unwrap_bucket_bytes(&page_data.entity_chain)?;
            bincode::deserialize(&bytes)
                .map_err(|e| format!("Entity index deserialization failed: {}", e))?
        };

        Ok(Self {
            k1: page_data.k1,
            b: page_data.b,
            postings,
            doc_lengths,
            avg_doc_length: page_data.avg_doc_length,
            total_docs: page_data.doc_count,
            total_term_count: page_data.total_term_count,
            entity_index,
        })
    }
}

impl Default for SparseIndex {
    fn default() -> Self {
        Self::new()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    use crate::file::page::PageHeader;
    use crate::slot::context::{ActivationState, ContextSlot};
    use crate::slot::hypergraph::HypergraphNode;
    use crate::util::{PageType, PAGE_SIZE};
    use memmap2::MmapMut;
    use std::fs::File;
    use std::io::Write;

    fn create_test_mmap(pages: usize) -> MmapMut {
        let temp_file = tempfile::NamedTempFile::new().unwrap();
        let path = temp_file.path();
        let mut file = File::create(path).unwrap();
        file.write_all(&vec![0u8; PAGE_SIZE * pages]).unwrap();
        drop(file);

        let file = std::fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open(path)
            .unwrap();

        unsafe { MmapMut::map_mut(&file).unwrap() }
    }

    fn write_hypergraph_node_page(mmap: &mut MmapMut, page_id: u32, node: HypergraphNode) {
        let offset = (page_id as usize) * PAGE_SIZE;
        let hdr = PageHeader::new(page_id, PageType::HypergraphNode, 3, 0xFFFFFFFF);
        mmap[offset..offset + 32].copy_from_slice(&hdr.to_bytes());
        let data = node.serialize().unwrap();
        mmap[offset + 32..offset + 32 + data.len()].copy_from_slice(&data);
    }

    fn write_context_page(mmap: &mut MmapMut, page_id: u32, ctx: ContextSlot) {
        let offset = (page_id as usize) * PAGE_SIZE;
        let hdr = PageHeader::new(page_id, PageType::Context, 2, 0xFFFFFFFF);
        mmap[offset..offset + 32].copy_from_slice(&hdr.to_bytes());
        let data = ctx.serialize().unwrap();
        mmap[offset + 32..offset + 32 + data.len()].copy_from_slice(&data);
    }

    fn create_test_context(id_hash: u64, title: &str, l3_refs: Vec<u64>) -> ContextSlot {
        ContextSlot {
            id_hash,
            parent_id: None,
            depth: 1,
            title: title.to_string(),
            summary: None,
            archive_refs: Vec::new(),
            l3_refs,
            turn_count: 0,
            created_at: 0,
            updated_at: 0,
            version: 1,
            importance: 0.5,
            activation_score: 0.0,
            is_active: true,
            activation_state: ActivationState::Active,
            centroid_page_ref: 0,
            dialogue_range: (0, 0),
        }
    }

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
    fn test_tokenize_chinese() {
        let tokens = SparseIndex::tokenize("人工智能在医疗领域的应用");
        assert!(
            tokens.contains(&"人工智能".to_string()),
            "should segment 人工智能, got {:?}",
            tokens
        );
        assert!(
            tokens.contains(&"医疗".to_string()),
            "should segment 医疗, got {:?}",
            tokens
        );
        assert!(
            tokens.contains(&"领域".to_string()),
            "should segment 领域, got {:?}",
            tokens
        );
        assert!(
            tokens.contains(&"应用".to_string()),
            "should segment 应用, got {:?}",
            tokens
        );
    }

    #[test]
    fn test_tokenize_mixed() {
        let tokens = SparseIndex::tokenize("Hello 人工智能 world");
        assert_eq!(tokens, vec!["hello", "人工智能", "world"]);
    }

    #[test]
    fn test_tokenize_filters_stop_words() {
        let tokens = tokenize("The quick brown fox jumps over the lazy dog");
        assert!(!tokens.contains(&"the".to_string()));
        assert!(!tokens.contains(&"over".to_string()));
        assert!(tokens.contains(&"quick".to_string()));
        assert!(tokens.contains(&"brown".to_string()));
        assert!(tokens.contains(&"fox".to_string()));
    }

    #[test]
    fn test_tokenize_chinese_filters_stop_words() {
        let tokens = tokenize("人工智能在医疗领域的应用");
        assert!(!tokens.contains(&"在".to_string()));
        assert!(!tokens.contains(&"的".to_string()));
        assert!(tokens.contains(&"人工智能".to_string()));
        assert!(tokens.contains(&"医疗".to_string()));
        assert!(tokens.contains(&"领域".to_string()));
        assert!(tokens.contains(&"应用".to_string()));
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
        index.add_document(
            3,
            vec![
                "rust".to_string(),
                "programming".to_string(),
                "language".to_string(),
            ],
            3,
        );
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

    #[test]
    fn test_levenshtein_distance() {
        assert_eq!(levenshtein_distance("kitten", "sitting"), 3);
        assert_eq!(levenshtein_distance("", "abc"), 3);
        assert_eq!(levenshtein_distance("abc", "abc"), 0);
        assert_eq!(levenshtein_distance("book", "back"), 2);
        assert_eq!(levenshtein_distance("rust", "rusty"), 1);
    }

    #[test]
    fn test_bk_tree_exact_and_fuzzy() {
        let mut tree = BkTree::new();
        tree.insert("apple".to_string());
        tree.insert("apply".to_string());
        tree.insert("banana".to_string());
        tree.insert("orange".to_string());

        let exact: Vec<_> = tree
            .search("apple", 0)
            .into_iter()
            .map(|(w, d)| (w.to_string(), d))
            .collect();
        assert_eq!(exact, vec![("apple".to_string(), 0)]);

        let fuzzy: Vec<_> = tree
            .search("aple", 1)
            .into_iter()
            .map(|(w, d)| (w.to_string(), d))
            .collect();
        assert!(fuzzy.contains(&("apple".to_string(), 1)));

        let fuzzy2: Vec<_> = tree
            .search("aple", 2)
            .into_iter()
            .map(|(w, d)| (w.to_string(), d))
            .collect();
        assert!(fuzzy2.contains(&("apple".to_string(), 1)));
        assert!(fuzzy2.contains(&("apply".to_string(), 2)));
    }

    #[test]
    fn test_bk_tree_duplicate_insertion() {
        let mut tree = BkTree::new();
        for _ in 0..100 {
            tree.insert("apple".to_string());
        }
        assert_eq!(
            tree.nodes.len(),
            1,
            "duplicate inserts should not create new nodes"
        );
        let results = tree.search("apple", 0);
        assert_eq!(results.len(), 1);
        assert_eq!(results[0], ("apple".to_string(), 0));
    }

    #[test]
    fn test_bk_tree_duplicate_after_different_word() {
        let mut tree = BkTree::new();
        tree.insert("apple".to_string());
        tree.insert("banana".to_string());
        tree.insert("apple".to_string());
        assert_eq!(
            tree.nodes.len(),
            2,
            "duplicate should be detected even after a different word"
        );
        let results = tree.search("apple", 0);
        assert_eq!(results.len(), 1);
    }

    #[test]
    fn test_entity_index_exact_match() {
        let mut index = EntityIndex::new();
        index.add_entity("Rust Programming", 101, vec![1001, 1002]);
        index.add_entity("Machine Learning", 102, vec![1003]);

        assert_eq!(
            index.exact_match("rust programming"),
            Some((101, vec![1001, 1002]))
        );
        assert_eq!(
            index.exact_match("RUST PROGRAMMING"),
            Some((101, vec![1001, 1002]))
        );
        assert_eq!(index.exact_match("python"), None);
    }

    #[test]
    fn test_entity_index_fuzzy_match() {
        let mut index = EntityIndex::new();
        index.add_entity("memhop", 1, vec![10]);
        index.add_entity("database", 2, vec![20]);

        let results = index.fuzzy_match("memhope", 2);
        assert!(!results.is_empty());
        assert!(results.iter().any(|(w, _, _, _)| w == "memhop"));

        let results = index.fuzzy_match("data base", 2);
        assert!(results.iter().any(|(w, _, _, _)| w == "database"));
    }

    #[test]
    fn test_entity_index_recognize_entities() {
        let mut index = EntityIndex::new();
        index.add_entity("Rust", 1, vec![10, 11]);
        index.add_entity("memory database", 2, vec![20]);

        let entities = index.recognize_entities("I use rust for memory database work");
        assert!(entities.iter().any(|(name, _, _)| name == "rust"));
        assert!(entities
            .iter()
            .any(|(name, _, _)| name == "memory database"));

        let rust_entry = entities.iter().find(|(name, _, _)| name == "rust").unwrap();
        assert!((rust_entry.1 - 1.0).abs() < 1e-6);
        assert_eq!(rust_entry.2, vec![10, 11]);
    }

    #[test]
    fn test_sparse_entity_search() {
        let mut sparse = SparseIndex::new();
        sparse.entity_index.add_entity("Rust", 1, vec![1001, 1002]);
        sparse.entity_index.add_entity("Python", 2, vec![1003]);

        let results = sparse.entity_search("I love Rust and Pithon");
        // Rust -> 1001 + 1002, Python (fuzzy) -> 1003 = 3 entries
        assert_eq!(results.len(), 3);

        let rust_ids: Vec<u64> = results
            .iter()
            .filter(|(_, score)| (*score - 1.0).abs() < 1e-6)
            .map(|(id, _)| *id)
            .collect();
        assert!(rust_ids.contains(&1001));
        assert!(rust_ids.contains(&1002));

        let python = results.iter().find(|(id, _)| *id == 1003);
        assert!(python.is_some());
        assert!(python.unwrap().1 < 1.0); // fuzzy match
    }

    #[test]
    fn test_sparse_entity_search_aggregates_scores() {
        let mut sparse = SparseIndex::new();
        sparse.entity_index.add_entity("rust", 1, vec![100]);
        sparse.entity_index.add_entity("programming", 2, vec![100]);

        let results = sparse.entity_search("rust programming");
        assert_eq!(results.len(), 1);
        assert_eq!(results[0].0, 100);
        assert!(results[0].1 > 1.5); // 1.0 + ~1.0
    }

    #[test]
    fn test_serialize_deserialize_with_entity_index() {
        let mut index = SparseIndex::new();
        index.entity_index.add_entity("Rust", 1, vec![1001]);
        index.entity_index.add_entity("Python", 2, vec![1002]);

        let serialized = index.serialize().unwrap();
        let deserialized = SparseIndex::deserialize(&serialized).unwrap();

        assert!(deserialized.has_entity_index());
        assert_eq!(
            deserialized.entity_index.exact_match("rust"),
            Some((1, vec![1001]))
        );
        assert_eq!(
            deserialized.entity_index.exact_match("python"),
            Some((2, vec![1002]))
        );
    }

    #[test]
    fn test_build_entity_index_from_l3() {
        let mut mmap = create_test_mmap(8);
        let mut btree = BTreeIndex::new();

        // L3 hypergraph node representing an entity.
        let node = HypergraphNode {
            id_hash: 1001,
            graph_id: 1000,
            title: "Rust Programming".to_string(),
            node_type: "concept".to_string(),
            content: "A systems programming language".to_string(),
            keywords: vec![],
            source_ref: None,
            importance: 0.9,
            created_at: 0,
            updated_at: 0,
            version: 1,
        };
        write_hypergraph_node_page(&mut mmap, 3, node);
        btree.insert(1001, (3u64) << 16);

        // L2 context referencing the entity's graph.
        let ctx = create_test_context(2001, "Rust discussion", vec![1000]);
        write_context_page(&mut mmap, 4, ctx);
        btree.insert(2001, (4u64) << 16);

        // Another context referencing a different graph.
        let ctx2 = create_test_context(2002, "Python chat", vec![2000]);
        write_context_page(&mut mmap, 5, ctx2);
        btree.insert(2002, (5u64) << 16);

        let mut sparse = SparseIndex::new();
        let data: &[u8] = &mmap[..];
        sparse.build_entity_index(data, &btree).unwrap();

        assert!(sparse.has_entity_index());

        // Exact entity query should return the associated L2 context.
        let results = sparse.entity_search("rust programming");
        assert_eq!(results.len(), 1);
        assert_eq!(results[0].0, 2001);
        assert!((results[0].1 - 1.0).abs() < 1e-6);

        // Fuzzy entity query should still find the context.
        let fuzzy = sparse.entity_search("rustt programing");
        assert!(!fuzzy.is_empty());
        assert!(fuzzy.iter().any(|(id, _)| *id == 2001));

        // Unrelated query should not return the Rust context.
        let unrelated = sparse.entity_search("python");
        assert!(!unrelated.iter().any(|(id, _)| *id == 2001));
    }

    #[test]
    fn test_serialize_to_pages_roundtrip() {
        let mut index = SparseIndex::new();

        let terms1 = SparseIndex::tokenize("machine learning is great");
        index.add_document(1, terms1.clone(), terms1.len() as u32);

        let terms2 = SparseIndex::tokenize("deep learning neural networks");
        index.add_document(2, terms2.clone(), terms2.len() as u32);

        let terms3 = SparseIndex::tokenize("artificial intelligence algorithms");
        index.add_document(3, terms3.clone(), terms3.len() as u32);

        index.entity_index.add_entity("Rust", 1, vec![10, 11]);
        index.entity_index.add_entity("Python", 2, vec![20]);

        let page_data = index.serialize_to_pages().unwrap();
        let deserialized = SparseIndex::deserialize_from_pages(&page_data).unwrap();

        assert_eq!(deserialized.len(), index.len());
        assert_eq!(deserialized.postings.len(), index.postings.len());
        assert_eq!(deserialized.doc_lengths.len(), index.doc_lengths.len());
        assert!(deserialized.has_entity_index());

        let query = SparseIndex::tokenize("learning");
        for doc_id in [1u64, 2, 3] {
            let expected = index.bm25_score(&query, doc_id);
            let actual = deserialized.bm25_score(&query, doc_id);
            assert!(
                (expected - actual).abs() < 1e-6,
                "BM25 score mismatch for doc {}: expected {}, got {}",
                doc_id,
                expected,
                actual
            );
        }
    }

    #[test]
    fn test_serialize_to_pages_empty_roundtrip() {
        let index = SparseIndex::new();
        let page_data = index.serialize_to_pages().unwrap();
        let deserialized = SparseIndex::deserialize_from_pages(&page_data).unwrap();

        assert!(deserialized.is_empty());
        assert!(!deserialized.has_entity_index());
    }

    #[test]
    fn test_search_uses_inverted_index_pruning() {
        let mut index = SparseIndex::new();

        index.add_document(1, SparseIndex::tokenize("apple banana cherry"), 3);
        index.add_document(2, SparseIndex::tokenize("banana date elderberry"), 3);
        index.add_document(3, SparseIndex::tokenize("fig grape apple"), 3);

        // Term that only appears in document 2.
        let query = SparseIndex::tokenize("date");
        let results = index.search(&query, 10);

        assert_eq!(results.len(), 1, "Should only return candidate doc 2");
        assert_eq!(results[0].0, 2);
        assert!(
            (results[0].1 - index.bm25_score(&query, 2)).abs() < 1e-6,
            "BM25 score should match direct calculation"
        );

        // Query with no matching terms should return nothing.
        let no_match = SparseIndex::tokenize("zzzznoterm");
        assert!(index.search(&no_match, 10).is_empty());

        // Multi-term query should union postings and still score correctly.
        let multi = SparseIndex::tokenize("apple elderberry");
        let results = index.search(&multi, 10);
        let mut ids: Vec<u64> = results.iter().map(|(id, _)| *id).collect();
        ids.sort();
        assert_eq!(ids, vec![1, 2, 3]);
    }
}
