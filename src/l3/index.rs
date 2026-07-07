// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L3 Hypergraph Index — keyword, type, and BM25 content search within a single L3 hypergraph.

use crate::file::page::PageHeader;
use crate::index::sparse::{self, SparseIndex};
use crate::layers::hypergraph::HypergraphNode;
use crate::util::{PageType, SENTINEL_PAGE_ID};
use crate::MemHopError;
use memmap2::Mmap;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;

const PAGE_DATA_BYTES: usize = 4064; // PAGE_SIZE(4096) - header(32)
const SENTINEL: u32 = SENTINEL_PAGE_ID;

/// L3Index provides search capabilities within a single L3 hypergraph.
///
/// # Architecture
/// - `keyword_index`: Maps keywords to node IDs (inverted index)
/// - `type_index`: Maps node_type strings to node IDs
/// - `content_index`: BM25 sparse index for full-text search on node content
#[derive(Serialize, Deserialize, Debug, Clone)]
pub struct L3Index {
    /// keyword -> list of node id_hashes
    pub keyword_index: HashMap<String, Vec<u64>>,
    /// node_type -> list of node id_hashes
    pub type_index: HashMap<String, Vec<u64>>,
    /// BM25 sparse index for content search
    pub content_index: SparseIndex,
}

/// Query parameters for L3Index search
pub struct L3IndexQuery {
    pub query: String,
    pub node_type: Option<String>,
    pub limit: usize,
    pub min_importance: Option<f32>,
}

impl L3Index {
    /// Create a new empty L3Index
    pub fn new() -> Self {
        Self {
            keyword_index: HashMap::new(),
            type_index: HashMap::new(),
            content_index: SparseIndex::new(),
        }
    }

    /// Add a node to the index
    pub fn add_node(&mut self, node: &HypergraphNode) {
        let id_hash = node.id_hash;

        for keyword in &node.keywords {
            self.keyword_index
                .entry(keyword.clone())
                .or_default()
                .push(id_hash);
        }

        self.type_index
            .entry(node.node_type.clone())
            .or_default()
            .push(id_hash);

        let content_text = format!("{} {}", node.title, node.content);
        let tokens = sparse::tokenize(&content_text);
        let doc_len = tokens.len() as u32;
        self.content_index.add_document(id_hash, tokens, doc_len);
    }

    /// Remove a node from the index
    pub fn remove_node(&mut self, node_id: u64, node: &HypergraphNode) {
        for keyword in &node.keywords {
            if let Some(ids) = self.keyword_index.get_mut(keyword) {
                ids.retain(|&id| id != node_id);
                if ids.is_empty() {
                    self.keyword_index.remove(keyword);
                }
            }
        }

        if let Some(ids) = self.type_index.get_mut(&node.node_type) {
            ids.retain(|&id| id != node_id);
            if ids.is_empty() {
                self.type_index.remove(&node.node_type);
            }
        }

        self.content_index.remove_document(node_id);
    }

    /// Search for nodes matching the query.
    /// Returns (node_id_hash, relevance_score) tuples sorted by score descending.
    pub fn search(&self, query: &L3IndexQuery) -> Vec<(u64, f32)> {
        let candidates: Option<Vec<u64>> = query
            .node_type
            .as_ref()
            .and_then(|t| self.type_index.get(t).cloned());

        let query_terms = sparse::tokenize(&query.query);
        let content_results = self.content_index.search(&query_terms, query.limit * 2);

        let filtered: Vec<(u64, f32)> = content_results
            .into_iter()
            .filter(|(id, _score)| {
                if let Some(ref cands) = candidates {
                    cands.contains(id)
                } else {
                    true
                }
            })
            .collect();

        let final_results: Vec<(u64, f32)> = if let Some(min_imp) = query.min_importance {
            filtered
                .into_iter()
                .filter(|(_, score)| *score >= min_imp)
                .collect()
        } else {
            filtered
        };

        final_results.into_iter().take(query.limit).collect()
    }

    /// Search by keyword only (faster, no BM25)
    pub fn search_by_keyword(&self, keyword: &str, limit: usize) -> Vec<u64> {
        self.keyword_index
            .get(keyword)
            .map(|ids| ids.iter().take(limit).cloned().collect())
            .unwrap_or_default()
    }

    /// Get all node IDs of a specific type
    pub fn get_nodes_by_type(&self, node_type: &str, limit: usize) -> Vec<u64> {
        self.type_index
            .get(node_type)
            .map(|ids| ids.iter().take(limit).cloned().collect())
            .unwrap_or_default()
    }

    /// Serialize the index to bytes
    pub fn serialize(&self) -> Result<Vec<u8>, MemHopError> {
        bincode::serialize(self).map_err(|e| MemHopError::Serialization(e.to_string()))
    }

    /// Deserialize the index from bytes
    pub fn deserialize(data: &[u8]) -> Result<Self, MemHopError> {
        bincode::deserialize(data).map_err(|e| MemHopError::Serialization(e.to_string()))
    }

    /// Merge another L3Index into this one.
    /// Node id_hashes are assumed to be disjoint across indices.
    pub fn merge(&mut self, other: &L3Index) {
        for (keyword, ids) in &other.keyword_index {
            let entry = self.keyword_index.entry(keyword.clone()).or_default();
            for id in ids {
                if !entry.contains(id) {
                    entry.push(*id);
                }
            }
        }
        for (node_type, ids) in &other.type_index {
            let entry = self.type_index.entry(node_type.clone()).or_default();
            for id in ids {
                if !entry.contains(id) {
                    entry.push(*id);
                }
            }
        }
        self.content_index.merge(&other.content_index);
    }

    /// Write L3Index across a chain of pages in mmap
    ///
    /// Serializes the index with bincode, then writes it across multiple
    /// pages linked via PageHeader.next_page. Returns the first page_id.
    /// Caller must allocate pages and pass page_ids (at least ceil(data.len() / 4064)).
    pub fn write_to_pages(
        &self,
        mmap: &mut memmap2::MmapMut,
        page_ids: &[u32],
    ) -> Result<(), String> {
        let data = self.serialize().map_err(|e| e.to_string())?;
        let chunks: Vec<&[u8]> = data.chunks(PAGE_DATA_BYTES).collect();
        if page_ids.len() < chunks.len() {
            return Err(format!(
                "Not enough pages: need {}, got {}",
                chunks.len(),
                page_ids.len()
            ));
        }

        for (i, chunk) in chunks.iter().enumerate() {
            let page_id = page_ids[i];
            let next = if i + 1 < chunks.len() {
                page_ids[i + 1]
            } else {
                SENTINEL
            };
            let offset = (page_id as usize) * crate::util::PAGE_SIZE;

            let hdr = PageHeader::new(page_id, PageType::L3IndexPage, 0, next);
            mmap[offset..offset + 32].copy_from_slice(&hdr.to_bytes());

            let data_start = offset + 32;
            mmap[data_start..data_start + chunk.len()].copy_from_slice(chunk);
            if chunk.len() < PAGE_DATA_BYTES {
                mmap[data_start + chunk.len()..data_start + PAGE_DATA_BYTES].fill(0);
            }
        }
        Ok(())
    }

    /// Read L3Index from a chain of pages in mmap
    ///
    /// Follows the next_page chain starting from `first_page_id`.
    pub fn read_from_pages(mmap: &Mmap, first_page_id: u32) -> Result<Self, String> {
        let mut data = Vec::new();
        let mut current = first_page_id;
        let mut visited = std::collections::HashSet::new();

        while current != SENTINEL {
            if !visited.insert(current) {
                return Err("Cycle detected in page chain".to_string());
            }
            let offset = (current as usize) * crate::util::PAGE_SIZE;
            if offset + 32 > mmap.len() {
                return Err(format!("Page {} out of bounds", current));
            }

            let hdr_bytes: [u8; 32] = mmap[offset..offset + 32]
                .try_into()
                .map_err(|e: std::array::TryFromSliceError| e.to_string())?;
            let hdr = PageHeader::from_bytes(&hdr_bytes)
                .map_err(|e| format!("Header read error: {:?}", e))?;

            let available = std::cmp::min(PAGE_DATA_BYTES, mmap.len() - offset - 32);
            data.extend_from_slice(&mmap[offset + 32..offset + 32 + available]);

            current = hdr.next_page;
        }

        Self::deserialize(&data).map_err(|e| e.to_string())
    }

    /// Calculate number of pages needed to store this index
    pub fn pages_needed(&self) -> Result<usize, String> {
        let data = self.serialize().map_err(|e| e.to_string())?;
        Ok(data.len().div_ceil(PAGE_DATA_BYTES))
    }
}

impl Default for L3Index {
    fn default() -> Self {
        Self::new()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::layers::hypergraph::HypergraphNode;

    fn create_test_node(id: u64, title: &str, content: &str, node_type: &str) -> HypergraphNode {
        HypergraphNode {
            id_hash: id,
            graph_id: 12345,
            title: title.to_string(),
            node_type: node_type.to_string(),
            content: content.to_string(),
            keywords: vec!["test".to_string(), "keyword".to_string()],
            source_ref: None,
            importance: 0.8,
            created_at: 0,
            updated_at: 0,
            version: 1,
        }
    }

    #[test]
    fn test_add_and_search() {
        let mut index = L3Index::new();
        let node1 = create_test_node(1, "Test Node", "This is test content", "concept");
        let node2 = create_test_node(2, "Another Node", "More test content", "function");

        index.add_node(&node1);
        index.add_node(&node2);

        let query = L3IndexQuery {
            query: "test".to_string(),
            node_type: None,
            limit: 10,
            min_importance: None,
        };

        let results = index.search(&query);
        assert!(!results.is_empty());
    }

    #[test]
    fn test_type_filter() {
        let mut index = L3Index::new();
        let node1 = create_test_node(1, "Concept", "Content", "concept");
        let node2 = create_test_node(2, "Function", "Content", "function");

        index.add_node(&node1);
        index.add_node(&node2);

        let query = L3IndexQuery {
            query: "Content".to_string(),
            node_type: Some("concept".to_string()),
            limit: 10,
            min_importance: None,
        };

        let results = index.search(&query);
        assert_eq!(results.len(), 1);
        assert_eq!(results[0].0, 1);
    }

    #[test]
    fn test_merge_l3_index() {
        let mut index1 = L3Index::new();
        let node1 = create_test_node(1, "Rust Node", "Rust content", "concept");
        index1.add_node(&node1);

        let mut index2 = L3Index::new();
        let node2 = create_test_node(2, "Python Node", "Python content", "function");
        index2.add_node(&node2);

        index1.merge(&index2);

        let query = L3IndexQuery {
            query: "Rust".to_string(),
            node_type: None,
            limit: 10,
            min_importance: None,
        };
        let rust_results = index1.search(&query);
        assert_eq!(rust_results.len(), 1);
        assert_eq!(rust_results[0].0, 1);

        let query = L3IndexQuery {
            query: "Python".to_string(),
            node_type: None,
            limit: 10,
            min_importance: None,
        };
        let python_results = index1.search(&query);
        assert_eq!(python_results.len(), 1);
        assert_eq!(python_results[0].0, 2);

        assert!(index1.type_index.contains_key("function"));
        assert!(index1.type_index.contains_key("concept"));
    }
}
