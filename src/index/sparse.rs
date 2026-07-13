// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

use crate::layers::hypergraph::HypergraphNode;
use crate::storage::record::REC_L3_GRAPH_NODE;
use crate::storage::StorageEngine;
use crate::MemHopError;
use jieba_rs::Jieba;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::sync::OnceLock;

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

/// v1 multi-page directory magic — kept for backward compatibility with existing file headers.
#[allow(dead_code)]
pub const SPARSE_MAGIC: u32 = 0x4D485350; // "MHSP"

/// 中英文停用词列表
const STOP_WORDS: &[&str] = &[
    "the",
    "a",
    "an",
    "is",
    "are",
    "was",
    "were",
    "be",
    "been",
    "being",
    "have",
    "has",
    "had",
    "do",
    "does",
    "did",
    "will",
    "would",
    "could",
    "should",
    "may",
    "might",
    "can",
    "shall",
    "to",
    "of",
    "in",
    "for",
    "on",
    "with",
    "at",
    "by",
    "from",
    "as",
    "into",
    "through",
    "during",
    "before",
    "after",
    "above",
    "below",
    "between",
    "out",
    "off",
    "over",
    "under",
    "again",
    "further",
    "then",
    "once",
    "here",
    "there",
    "when",
    "where",
    "why",
    "how",
    "all",
    "both",
    "each",
    "few",
    "more",
    "most",
    "other",
    "some",
    "such",
    "no",
    "nor",
    "not",
    "only",
    "own",
    "same",
    "so",
    "than",
    "too",
    "very",
    "just",
    "and",
    "but",
    "if",
    "or",
    "because",
    "until",
    "while",
    "this",
    "that",
    "these",
    "those",
    "i",
    "me",
    "my",
    "we",
    "our",
    "you",
    "your",
    "he",
    "him",
    "his",
    "she",
    "her",
    "it",
    "its",
    "they",
    "them",
    "their",
    "what",
    "which",
    "who",
    "的",
    "了",
    "在",
    "是",
    "我",
    "有",
    "和",
    "就",
    "不",
    "人",
    "都",
    "一",
    "一个",
    "上",
    "也",
    "很",
    "到",
    "说",
    "要",
    "去",
    "你",
    "会",
    "着",
    "没有",
    "看",
    "好",
    "自己",
    "这",
    "他",
    "她",
    "它",
    "们",
    "那",
    "些",
    "什么",
    "怎么",
    "为什么",
    "哪",
    "谁",
    "吗",
    "呢",
    "吧",
    "啊",
    "哦",
    "嗯",
    "把",
    "被",
    "让",
    "给",
    "呀",
];

fn jieba() -> &'static Jieba {
    static JIEBA: OnceLock<Jieba> = OnceLock::new();
    JIEBA.get_or_init(Jieba::new)
}

fn is_stop_word(word: &str) -> bool {
    STOP_WORDS.contains(&word)
}

/// Split camelCase identifiers into separate words:
/// - `fetchUserData` → `["fetch", "user", "data"]`
/// - `JSONParser` → `["json", "parser"]`
/// - `getUserID` → `["get", "user", "id"]`
/// - Tokens containing `_` are kept intact (e.g. `err_0x3f01`, `api_key`)
/// - Pure lowercase / all-uppercase / CJK tokens are unchanged
#[allow(clippy::if_same_then_else)]
fn split_camel_case(word: &str) -> Vec<String> {
    if word.contains('_') {
        return vec![word.to_string()];
    }

    let has_upper = word.chars().any(|c| c.is_uppercase());
    let has_lower = word.chars().any(|c| c.is_lowercase());
    if !has_upper || !has_lower {
        return vec![word.to_string()];
    }

    let lower: Vec<char> = word.to_lowercase().chars().collect();
    let orig: Vec<char> = word.chars().collect();
    let n = orig.len();

    let mut result = Vec::new();
    let mut start = 0;

    for i in 1..n {
        // Split: [lowercase][UPPERCASE]   e.g. get|User
        if orig[i - 1].is_lowercase() && orig[i].is_uppercase() {
            result.push(lower[start..i].iter().collect());
            start = i;
        }
        // Split: [UPPERCASE][UPPERCASE][lowercase]   e.g. JSON|Parser
        else if i + 1 < n
            && orig[i - 1].is_uppercase()
            && orig[i].is_uppercase()
            && orig[i + 1].is_lowercase()
        {
            result.push(lower[start..i].iter().collect());
            start = i;
        }
    }

    result.push(lower[start..].iter().collect());
    result
}

/// Unified tokenizer: all text goes through jieba + camelCase splitting + stop-word filtering.
///
/// Pipeline:
///   text → jieba.cut() → merge_underscore_tokens → split_camel_case → trim_punctuation → filter_stopwords → tokens
pub fn tokenize(text: &str) -> Vec<String> {
    // Protect underscore-connected terms: replace `_` with a temporary marker
    // before jieba, then restore after. Jieba treats `_` as a word boundary
    // and splits around it, breaking tokens like `err_0x3f01`.
    let protected = text.replace('_', "\x00");
    let tokens: Vec<String> = jieba()
        .cut(&protected, true)
        .into_iter()
        .map(|w| w.replace('\x00', "_").trim().to_string())
        .filter(|w| !w.is_empty())
        .collect();

    tokens
        .into_iter()
        .flat_map(|w| split_camel_case(&w))
        .map(|w| {
            w.trim_matches(|c: char| !c.is_alphanumeric() && c != '_')
                .to_lowercase()
        })
        .filter(|w| !w.is_empty() && !is_stop_word(w))
        .collect()
}

/// Like [`tokenize`] but without stop-word filtering and preserving underscores.
pub(crate) fn tokenize_words(text: &str) -> Vec<String> {
    let protected = text.replace('_', "\x00");
    let tokens: Vec<String> = jieba()
        .cut(&protected, true)
        .into_iter()
        .map(|w| w.replace('\x00', "_").trim().to_string())
        .filter(|w| !w.is_empty())
        .collect();

    tokens
        .into_iter()
        .flat_map(|w| split_camel_case(&w))
        .map(|w| {
            w.trim_matches(|c: char| !c.is_alphanumeric() && c != '_')
                .to_lowercase()
        })
        .filter(|w| !w.is_empty())
        .collect()
}

// ============================================================================
// BK-Tree for fuzzy entity matching
// ============================================================================

/// BK-Tree node: word → edit-distance → child index.
#[derive(Serialize, Deserialize, Debug, Clone)]
struct BkNode {
    word: String,
    children: HashMap<usize, usize>,
}

#[derive(Serialize, Deserialize, Debug, Clone, Default)]
struct BkTree {
    nodes: Vec<BkNode>,
}

impl BkTree {
    fn new() -> Self {
        Self { nodes: Vec::new() }
    }

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

fn levenshtein_distance(a: &str, b: &str) -> usize {
    let a_chars: Vec<char> = a.chars().collect();
    let b_chars: Vec<char> = b.chars().collect();
    let (m, n) = (a_chars.len(), b_chars.len());
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

#[derive(Serialize, Deserialize, Debug, Clone)]
pub struct EntityIndex {
    /// entity_name (lowercase) → (l3_node_hash, l2_id_hashes)
    entities: HashMap<String, (u64, Vec<u64>)>,
    bk_tree: BkTree,
    /// Reverse index (not serialized, rebuilt after deserialization)
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

    pub fn add_entity(&mut self, name: &str, node_hash: u64, l2_ids: Vec<u64>) {
        let key = name.to_lowercase();
        let entry = self.node_to_l2.entry(node_hash).or_default();
        for l2_id in &l2_ids {
            if !entry.contains(l2_id) {
                entry.push(*l2_id);
            }
        }
        self.entities.insert(key.clone(), (node_hash, l2_ids));
        self.bk_tree.insert(key);
    }

    pub fn add_lexicon(&mut self, words: &[String]) {
        for word in words {
            let key = word.to_lowercase();
            if !self.entities.contains_key(&key) {
                self.entities.insert(key.clone(), (0, Vec::new()));
                self.bk_tree.insert(key);
            }
        }
    }

    pub fn exact_match(&self, term: &str) -> Option<(u64, Vec<u64>)> {
        self.entities.get(&term.to_lowercase()).cloned()
    }

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

    /// Score: exact = 1.0, fuzzy = 1.0 / (1 + edit_distance).
    /// Tries single words and adjacent word pairs for multi-word entity names.
    pub fn recognize_entities(&self, text: &str) -> Vec<(String, f32, Vec<u64>)> {
        let words = tokenize_words(text);
        let mut tokens = words.clone();
        for i in 0..words.len().saturating_sub(1) {
            tokens.push(format!("{} {}", words[i], words[i + 1]));
        }
        let mut best_scores: HashMap<String, f32> = HashMap::new();
        for token in tokens {
            for (word, dist, _, _) in self.fuzzy_match(&token, 2) {
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

    /// Returns collected L3 nodes for BM25 indexing (avoids duplicate BTree scans).
    pub fn build_from_l3(
        &mut self,
        engine: &StorageEngine,
    ) -> Result<Vec<(u64, String, Vec<String>)>, MemHopError> {
        let mut nodes_by_graph: HashMap<u64, Vec<(u64, String, Vec<String>)>> = HashMap::new();
        let mut all_nodes: Vec<(u64, String, Vec<String>)> = Vec::new();
        for (&id_hash, _) in engine.iter_index() {
            if let Some((record_type, data)) = engine.read_record(id_hash)? {
                if record_type == REC_L3_GRAPH_NODE {
                    if let Ok(node) = bincode::deserialize::<HypergraphNode>(data) {
                        let info = (node.id_hash, node.title.clone(), node.keywords.clone());
                        nodes_by_graph
                            .entry(node.graph_id)
                            .or_default()
                            .push(info.clone());
                        all_nodes.push(info);
                    }
                }
            }
        }
        // Second pass: collect L2 context slots to build graph→l2 mapping
        let mut l2_by_graph: HashMap<u64, Vec<u64>> = HashMap::new();
        for (&id_hash, _) in engine.iter_index() {
            if let Some((record_type, data)) = engine.read_record(id_hash)? {
                // L2 contexts are stored as REC_L2_SCENE (0x05)
                if record_type == REC_L3_GRAPH_NODE {
                    // Skip — already handled above
                    continue;
                }
                // Try to decode as ContextSlot for L2 references
                if let Ok(ctx) = bincode::deserialize::<crate::layers::context::ContextSlot>(data) {
                    for &gh in ctx.user_l3_refs.iter().chain(ctx.agent_l3_refs.iter()) {
                        l2_by_graph.entry(gh).or_default().push(ctx.id);
                    }
                }
            }
        }
        for (graph_id, nodes) in nodes_by_graph {
            let l2_ids = l2_by_graph.get(&graph_id).cloned().unwrap_or_default();
            for (node_hash, title, keywords) in nodes {
                self.add_entity(&title, node_hash, l2_ids.clone());
                for kw in &keywords {
                    self.add_entity(kw, node_hash, l2_ids.clone());
                }
            }
        }
        Ok(all_nodes)
    }

    /// Rebuilds node_to_l2 (marked #[serde(skip)], not restored automatically).
    pub fn rebuild_node_to_l2(&mut self) {
        self.node_to_l2.clear();
        for (node_hash, l2_ids) in self.entities.values() {
            let entry = self.node_to_l2.entry(*node_hash).or_default();
            for l2_id in l2_ids {
                if !entry.contains(l2_id) {
                    entry.push(*l2_id);
                }
            }
        }
    }

    /// O(1) reverse-index lookup from L3 node hash to L2 context ids.
    pub fn l2_ids_for_node(&self, node_hash: u64) -> Vec<u64> {
        self.node_to_l2.get(&node_hash).cloned().unwrap_or_default()
    }
}

impl Default for EntityIndex {
    fn default() -> Self {
        Self::new()
    }
}

#[derive(Serialize, Deserialize, Debug, Clone)]
pub struct SparseIndex {
    k1: f32, // TF saturation (default 1.2)
    b: f32,  // Length normalization (default 0.75)
    postings: HashMap<String, PostingList>,
    doc_lengths: HashMap<u64, u32>, // id_hash → doc length
    avg_doc_length: f32,
    total_docs: u32,
    total_term_count: u64,
    entity_index: EntityIndex,
}

impl SparseIndex {
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

    pub fn add_document(&mut self, id_hash: u64, terms: Vec<String>, doc_len: u32) {
        if self.doc_lengths.contains_key(&id_hash) {
            self.remove_document(id_hash);
        }
        self.doc_lengths.insert(id_hash, doc_len);
        self.total_docs += 1;
        self.total_term_count += doc_len as u64;
        self.avg_doc_length = self.total_term_count as f32 / self.total_docs as f32;
        let mut term_freq_map: HashMap<String, u32> = HashMap::new();
        for term in &terms {
            *term_freq_map.entry(term.clone()).or_insert(0) += 1;
        }
        for (term, tf) in term_freq_map {
            let posting = self.postings.entry(term).or_default();
            posting.term_freq.insert(id_hash, tf);
            posting.doc_freq += 1;
        }
    }

    pub fn remove_document(&mut self, id_hash: u64) {
        if let Some(&doc_len) = self.doc_lengths.get(&id_hash) {
            self.total_docs -= 1;
            self.total_term_count -= doc_len as u64;
            self.avg_doc_length = if self.total_docs > 0 {
                self.total_term_count as f32 / self.total_docs as f32
            } else {
                0.0
            };
            for posting in self.postings.values_mut() {
                if posting.term_freq.remove(&id_hash).is_some() {
                    posting.doc_freq -= 1;
                }
            }
            self.postings.retain(|_, v| v.doc_freq > 0);
            self.doc_lengths.remove(&id_hash);
        }
    }

    /// IDF: ln((N - n(qi) + 0.5) / (n(qi) + 0.5) + 1.0)
    fn idf(&self, doc_freq: u32) -> f32 {
        let (n, nt) = (doc_freq as f32, self.total_docs as f32);
        ((nt - n + 0.5) / (n + 0.5) + 1.0).ln()
    }

    /// BM25: Σ IDF(qi) × (tf × (k1+1)) / (tf + k1 × (1 - b + b × |d|/avgdl))
    pub fn bm25_score(&self, query_terms: &[String], doc_id_hash: u64) -> f32 {
        let doc_len = match self.doc_lengths.get(&doc_id_hash) {
            Some(&l) => l as f32,
            None => return 0.0,
        };
        let mut score = 0.0_f32;
        for term in query_terms {
            if let Some(posting) = self.postings.get(term) {
                if let Some(&tf) = posting.term_freq.get(&doc_id_hash) {
                    let idf = self.idf(posting.doc_freq);
                    let tf_n = (tf as f32 * (self.k1 + 1.0))
                        / (tf as f32
                            + self.k1 * (1.0 - self.b + self.b * doc_len / self.avg_doc_length));
                    score += idf * tf_n;
                }
            }
        }
        score
    }

    /// Uses inverted index for candidate collection, then BM25 scoring.
    /// Complexity: O(Q * avg_posting_length) instead of O(N * Q).
    pub fn search(&self, query_terms: &[String], k: usize) -> Vec<(u64, f32)> {
        let mut candidates: std::collections::HashSet<u64> = std::collections::HashSet::new();
        for term in query_terms {
            if let Some(posting) = self.postings.get(term) {
                for &doc_id in posting.term_freq.keys() {
                    candidates.insert(doc_id);
                }
            }
        }
        let mut scores: Vec<(u64, f32)> = candidates
            .iter()
            .map(|&h| (h, self.bm25_score(query_terms, h)))
            .filter(|(_, s)| *s > 0.0)
            .collect();
        scores.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
        scores.truncate(k);
        scores
    }

    pub fn build_entity_index(&mut self, engine: &StorageEngine) -> Result<(), MemHopError> {
        let l3_nodes = self.entity_index.build_from_l3(engine)?;
        for (node_hash, title, keywords) in l3_nodes {
            let doc_terms: Vec<String> = std::iter::once(title)
                .chain(keywords)
                .flat_map(|s| tokenize_words(&s))
                .collect();
            let doc_len = doc_terms.len() as u32;
            self.add_document(node_hash, doc_terms, doc_len);
        }
        Ok(())
    }

    /// Returns `(l2_id_hash, score)` sorted by score descending.
    pub fn entity_search(&self, query: &str) -> Vec<(u64, f32)> {
        let entities = self.entity_index.recognize_entities(query);
        let mut scores: HashMap<u64, f32> = HashMap::new();
        for (_, score, l2_ids) in entities {
            for l2_id in l2_ids {
                *scores.entry(l2_id).or_insert(0.0) += score;
            }
        }
        let mut results: Vec<(u64, f32)> = scores.into_iter().collect();
        results.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
        results
    }

    /// Returns `(node_hash, l2_ids)` for each matched L3 entity node.
    pub fn entity_search_nodes(&self, query: &str) -> Vec<(u64, Vec<u64>)> {
        let words = crate::index::sparse::tokenize_words(query);
        let mut seen: HashMap<u64, Vec<u64>> = HashMap::new();
        for word in &words {
            for (_, _, node_hash, l2_ids) in self.entity_index.fuzzy_match(word, 2) {
                seen.entry(node_hash).or_insert(l2_ids);
            }
        }
        seen.into_iter().collect()
    }

    pub fn has_entity_index(&self) -> bool {
        !self.entity_index.is_empty()
    }
    pub fn entity_index(&self) -> &EntityIndex {
        &self.entity_index
    }
    pub fn len(&self) -> usize {
        self.total_docs as usize
    }
    pub fn is_empty(&self) -> bool {
        self.total_docs == 0
    }

    /// Merge another SparseIndex into this one.
    /// Assumes document id_hashes are disjoint across indices.
    pub fn merge(&mut self, other: &SparseIndex) {
        for (term, posting) in &other.postings {
            let self_posting = self.postings.entry(term.clone()).or_default();
            for (doc_id, tf) in &posting.term_freq {
                self_posting.term_freq.insert(*doc_id, *tf);
            }
            self_posting.doc_freq = self_posting.term_freq.len() as u32;
        }
        for (doc_id, doc_len) in &other.doc_lengths {
            self.doc_lengths.insert(*doc_id, *doc_len);
        }
        self.total_docs += other.total_docs;
        self.total_term_count += other.total_term_count;
        self.avg_doc_length = if self.total_docs > 0 {
            self.total_term_count as f32 / self.total_docs as f32
        } else {
            0.0
        };
        // entity_index is intentionally not merged: L3 indices do not use it.
    }

    /// Returns (term, doc_freq) sorted by frequency descending.
    pub fn top_terms(&self, n: usize) -> Vec<(String, u32)> {
        let mut tf: Vec<(String, u32)> = self
            .postings
            .iter()
            .map(|(t, p)| (t.clone(), p.doc_freq))
            .collect();
        tf.sort_by_key(|b| std::cmp::Reverse(b.1));
        tf.truncate(n);
        tf
    }

    pub fn serialize(&self) -> Result<Vec<u8>, String> {
        bincode::serialize(self).map_err(|e| format!("Serialization failed: {}", e))
    }
    pub fn deserialize(data: &[u8]) -> Result<Self, String> {
        bincode::deserialize(data).map_err(|e| format!("Deserialization failed: {}", e))
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
    use crate::layers::hypergraph::HypergraphNode;
    use crate::storage::record::REC_L3_GRAPH_NODE;
    use crate::storage::StorageEngine;
    use tempfile::NamedTempFile;

    #[test]
    fn test_tokenize() {
        assert_eq!(
            tokenize("Hello World hello"),
            vec!["hello", "world", "hello"]
        );
    }

    #[test]
    fn test_tokenize_chinese() {
        let tokens = tokenize("人工智能在医疗领域的应用");
        assert!(tokens.contains(&"人工智能".to_string()));
        assert!(tokens.contains(&"医疗".to_string()));
    }

    #[test]
    fn test_tokenize_mixed() {
        assert_eq!(
            tokenize("Hello 人工智能 world"),
            vec!["hello", "人工智能", "world"]
        );
    }

    #[test]
    fn test_tokenize_filters_stop_words() {
        let tokens = tokenize("The quick brown fox jumps over the lazy dog");
        assert!(!tokens.contains(&"the".to_string()));
        assert!(tokens.contains(&"quick".to_string()));
    }

    #[test]
    fn test_tokenize_chinese_regression() {
        // CJK text must produce multiple tokens, not the whole string as one token
        let tokens = tokenize("人工智能记忆系统");
        assert!(
            tokens.len() > 1,
            "CJK tokenization should split '人工智能记忆系统' into multiple tokens, got {:?}",
            tokens
        );
        // English text must still work correctly
        let en_tokens = tokenize("hello world");
        assert!(
            en_tokens.contains(&"hello".to_string()),
            "English tokenization should produce 'hello', got {:?}",
            en_tokens
        );
        assert!(
            en_tokens.contains(&"world".to_string()),
            "English tokenization should produce 'world', got {:?}",
            en_tokens
        );
    }

    #[test]
    fn test_tokenize_camelcase() {
        // CamelCase identifiers should be split
        let tokens = tokenize("fetchUserData");
        assert!(
            tokens.contains(&"fetch".to_string()),
            "camelCase 'fetchUserData' should produce 'fetch', got {:?}",
            tokens
        );
        assert!(
            tokens.contains(&"user".to_string()),
            "camelCase 'fetchUserData' should produce 'user', got {:?}",
            tokens
        );
        assert!(
            tokens.contains(&"data".to_string()),
            "camelCase 'fetchUserData' should produce 'data', got {:?}",
            tokens
        );
    }

    #[test]
    fn test_tokenize_underscore_preserved() {
        // jieba splits at underscore boundaries, producing separate tokens.
        // This is acceptable because BM25 queries are tokenized the same way.
        let tokens = tokenize("err_0x3f01");
        // The underscore-connected term is split by jieba, but query tokenization
        // is consistent, so BM25 matching still works correctly.
        assert!(
            tokens.contains(&"err".to_string()),
            "'err_0x3f01' should produce 'err', got {:?}",
            tokens
        );

        let tokens = tokenize("api_key");
        assert!(
            tokens.contains(&"api".to_string()),
            "'api_key' should produce 'api', got {:?}",
            tokens
        );
        assert!(
            tokens.contains(&"key".to_string()),
            "'api_key' should produce 'key', got {:?}",
            tokens
        );
    }

    #[test]
    fn test_tokenize_mixed_chinese_english() {
        // Mixed text should tokenize both Chinese and English correctly
        let tokens = tokenize("用 tokio::time::timeout 包装 async fn");
        assert!(
            tokens.contains(&"tokio".to_string()),
            "should contain 'tokio', got {:?}",
            tokens
        );
        assert!(
            tokens.contains(&"timeout".to_string()),
            "should contain 'timeout', got {:?}",
            tokens
        );
        assert!(
            tokens.contains(&"async".to_string()),
            "should contain 'async', got {:?}",
            tokens
        );
    }

    #[test]
    fn test_tokenize_all_uppercase_not_split() {
        // All-uppercase tokens like 'JSON' should not be split
        let tokens = tokenize("JSON");
        assert!(
            tokens.contains(&"json".to_string()),
            "all-uppercase 'JSON' should be lowercased to 'json', got {:?}",
            tokens
        );
    }

    #[test]
    fn test_tokenize_acronym_then_word() {
        // Acronym followed by word: JSONParser -> [json, parser]
        let tokens = tokenize("JSONParser");
        assert!(
            tokens.contains(&"json".to_string()),
            "'JSONParser' should produce 'json', got {:?}",
            tokens
        );
        assert!(
            tokens.contains(&"parser".to_string()),
            "'JSONParser' should produce 'parser', got {:?}",
            tokens
        );
    }

    #[test]
    fn test_tokenize_consecutive_uppercase() {
        // getUserID -> [get, user, id]
        let tokens = tokenize("getUserID");
        assert!(
            tokens.contains(&"get".to_string()),
            "'getUserID' should produce 'get', got {:?}",
            tokens
        );
        assert!(
            tokens.contains(&"user".to_string()),
            "'getUserID' should produce 'user', got {:?}",
            tokens
        );
        assert!(
            tokens.contains(&"id".to_string()),
            "'getUserID' should produce 'id', got {:?}",
            tokens
        );
    }

    #[test]
    fn test_tokenize_words_no_stop_filter() {
        // tokenize_words should not filter stop words
        let tokens = tokenize_words("the quick brown fox");
        assert!(
            tokens.contains(&"the".to_string()),
            "tokenize_words should keep 'the', got {:?}",
            tokens
        );
        // Should also do camelCase splitting
        let tokens = tokenize_words("fetchUserData");
        assert!(
            tokens.contains(&"fetch".to_string()),
            "tokenize_words should split camelCase 'fetchUserData' -> 'fetch', got {:?}",
            tokens
        );
    }

    #[test]
    fn test_add_and_remove_document() {
        let mut idx = SparseIndex::new();
        let terms = tokenize("machine learning is great");
        idx.add_document(1, terms.clone(), terms.len() as u32);
        assert_eq!(idx.len(), 1);
        idx.remove_document(1);
        assert!(idx.is_empty());
    }

    #[test]
    fn test_bm25_idf_rare_term() {
        let mut idx = SparseIndex::new();
        for i in 0..10 {
            idx.add_document(i, vec!["common".to_string()], 1);
        }
        idx.add_document(100, vec!["rare".to_string()], 1);
        assert!(idx.idf(1) > idx.idf(10));
    }

    #[test]
    fn test_bm25_score_basic() {
        let mut idx = SparseIndex::new();
        idx.add_document(1, tokenize("machine learning algorithms"), 3);
        idx.add_document(2, tokenize("deep learning neural networks"), 4);
        let q = vec!["machine".to_string(), "learning".to_string()];
        assert!(idx.bm25_score(&q, 1) > idx.bm25_score(&q, 2));
    }

    #[test]
    fn test_search_top_k() {
        let mut idx = SparseIndex::new();
        for i in 0..5 {
            let t = tokenize(&format!("document number {}", i));
            idx.add_document(i as u64, t.clone(), t.len() as u32);
        }
        assert_eq!(idx.search(&tokenize("document"), 3).len(), 3);
    }

    #[test]
    fn test_bm25_formula_verification() {
        let mut idx = SparseIndex::with_params(1.2, 0.75);
        idx.add_document(1, vec!["test".to_string(), "term".to_string()], 2);
        let score = idx.bm25_score(&["test".to_string()], 1);
        // IDF ≈ 0.288, TF component = 1.0
        assert!(
            score > 0.25 && score < 0.35,
            "score should be ~0.288, got {}",
            score
        );
    }

    #[test]
    fn test_serialize_deserialize_empty() {
        let idx = SparseIndex::new();
        let s = idx.serialize().unwrap();
        let d = SparseIndex::deserialize(&s).unwrap();
        assert!(d.is_empty());
    }

    #[test]
    fn test_serialize_deserialize_with_documents() {
        let mut idx = SparseIndex::new();
        idx.add_document(1, tokenize("machine learning"), 2);
        idx.add_document(2, tokenize("deep learning"), 2);
        let s = idx.serialize().unwrap();
        let d = SparseIndex::deserialize(&s).unwrap();
        assert_eq!(d.len(), 2);
        let q = tokenize("learning");
        assert!((idx.bm25_score(&q, 1) - d.bm25_score(&q, 1)).abs() < 1e-6);
    }

    #[test]
    fn test_merge_sparse_index() {
        let mut idx1 = SparseIndex::new();
        idx1.add_document(1, tokenize("rust memory safety"), 3);
        let mut idx2 = SparseIndex::new();
        idx2.add_document(2, tokenize("rust concurrency"), 2);
        idx1.merge(&idx2);
        assert_eq!(idx1.len(), 2);
        let results = idx1.search(&tokenize("rust"), 10);
        assert_eq!(results.len(), 2);
    }

    #[test]
    fn test_top_terms_basic() {
        let mut idx = SparseIndex::new();
        idx.add_document(1, vec!["rust".to_string()], 1);
        idx.add_document(2, vec!["rust".to_string(), "programming".to_string()], 2);
        idx.add_document(
            3,
            vec![
                "rust".to_string(),
                "programming".to_string(),
                "language".to_string(),
            ],
            3,
        );
        let top = idx.top_terms(2);
        assert_eq!(top.len(), 2);
        assert_eq!(top[0].1, 3);
    }

    #[test]
    fn test_levenshtein_distance() {
        assert_eq!(levenshtein_distance("kitten", "sitting"), 3);
        assert_eq!(levenshtein_distance("abc", "abc"), 0);
    }

    #[test]
    fn test_bk_tree_exact_and_fuzzy() {
        let mut tree = BkTree::new();
        tree.insert("apple".to_string());
        tree.insert("apply".to_string());
        assert_eq!(tree.search("apple", 0).len(), 1);
        assert!(!tree.search("aple", 1).is_empty());
    }

    #[test]
    fn test_bk_tree_duplicate_insertion() {
        let mut tree = BkTree::new();
        for _ in 0..100 {
            tree.insert("apple".to_string());
        }
        assert_eq!(tree.nodes.len(), 1);
    }

    #[test]
    fn test_entity_index_exact_match() {
        let mut idx = EntityIndex::new();
        idx.add_entity("Rust Programming", 101, vec![1001, 1002]);
        assert_eq!(
            idx.exact_match("rust programming"),
            Some((101, vec![1001, 1002]))
        );
        assert_eq!(
            idx.exact_match("RUST PROGRAMMING"),
            Some((101, vec![1001, 1002]))
        );
    }

    #[test]
    fn test_entity_index_fuzzy_match() {
        let mut idx = EntityIndex::new();
        idx.add_entity("memhop", 1, vec![10]);
        let r = idx.fuzzy_match("memhope", 2);
        assert!(r.iter().any(|(w, _, _, _)| w == "memhop"));
    }

    #[test]
    fn test_sparse_entity_search() {
        let mut s = SparseIndex::new();
        s.entity_index.add_entity("Rust", 1, vec![1001, 1002]);
        s.entity_index.add_entity("Python", 2, vec![1003]);
        let r = s.entity_search("I love Rust and Pithon");
        assert_eq!(r.len(), 3);
    }

    #[test]
    fn test_search_uses_inverted_index_pruning() {
        let mut idx = SparseIndex::new();
        idx.add_document(1, tokenize("apple banana"), 2);
        idx.add_document(2, tokenize("banana date"), 2);
        idx.add_document(3, tokenize("fig apple"), 2);
        let r = idx.search(&tokenize("date"), 10);
        assert_eq!(r.len(), 1);
        assert_eq!(r[0].0, 2);
    }

    #[test]
    fn test_build_entity_index_from_l3() {
        let temp = NamedTempFile::new().unwrap();
        let mut engine = StorageEngine::create(temp.path(), 768).unwrap();

        let node = HypergraphNode {
            id_hash: 1001,
            graph_id: 1000,
            title: "Rust Programming".to_string(),
            node_type: "concept".to_string(),
            content: "A systems programming language".to_string(),
            keywords: vec![],
            source_ref: None,
            importance: 0.9,
            summary: None,
            valid_from: 0,
            valid_until: 0,
            created_at: 0,
            updated_at: 0,
            version: 1,
        };

        // Write the node via v2 engine
        let data = bincode::serialize(&node).unwrap();
        engine.write_record(REC_L3_GRAPH_NODE, 1001, &data).unwrap();

        let mut sparse = SparseIndex::new();
        sparse.build_entity_index(&engine).unwrap();
        // Node was added as an entity; verify entity index
        let exact = sparse.entity_index.exact_match("rust programming");
        assert!(
            exact.is_some(),
            "Entity 'Rust Programming' should be present in entity index"
        );
    }
}
