//! Knowledge shelf module — mount external knowledge as HNSW-only indexes.
//!
//! v0.9.0: Knowledge shelves are separate from episodic memory.
//! They use HNSW-only indexing (no Hopfield, no SparseIndex, no Graph).
//! Lifecycle: mount → search → unmount.

use std::collections::HashMap;
use std::path::Path;

use half::f16;
use serde::{Deserialize, Serialize};

use crate::engram::VECTOR_DIM;
use crate::hnsw::HnswIndex;
use crate::types::{ChunkMeta, ShelfDomain, ShelfResult};

/// A mounted knowledge shelf with its own HNSW index.
#[derive(Serialize, Deserialize)]
pub struct Shelf {
    pub shelf_id: String,
    pub path: String,
    pub domain: ShelfDomain,
    pub hnsw: HnswIndex,
    /// Chunk metadata: node_id → ChunkMeta
    pub chunk_meta: HashMap<u64, ChunkMeta>,
    /// Text content: node_id → text
    pub texts: HashMap<u64, String>,
}

/// Manages multiple mounted shelves.
#[derive(Serialize, Deserialize, Default)]
pub struct ShelfManager {
    pub shelves: HashMap<String, Shelf>,
}

impl ShelfManager {
    pub fn new() -> Self {
        ShelfManager {
            shelves: HashMap::new(),
        }
    }

    /// Mount a knowledge source at the given path.
    /// Scans, chunks, encodes, and indexes.
    pub fn mount(&mut self, path: &str, domain: ShelfDomain) -> Result<String, String> {
        let shelf_id = format!(
            "shelf_{}",
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap_or_default()
                .as_micros()
        );

        let path_obj = Path::new(path);
        if !path_obj.exists() {
            return Err(format!("Path does not exist: {}", path));
        }

        // Scan and chunk
        let chunks = Self::scan_and_chunk(path, &domain)?;

        // Create HNSW index
        let hnsw = HnswIndex::new(VECTOR_DIM);
        let mut chunk_meta = HashMap::new();
        let mut texts = HashMap::new();

        // Store text and metadata; vectors are inserted later via encode_shelf
        for (i, (text, meta)) in chunks.into_iter().enumerate() {
            let node_id = i as u64;
            texts.insert(node_id, text);
            chunk_meta.insert(node_id, meta);
        }

        let shelf = Shelf {
            shelf_id: shelf_id.clone(),
            path: path.to_string(),
            domain,
            hnsw,
            chunk_meta,
            texts,
        };

        self.shelves.insert(shelf_id.clone(), shelf);
        Ok(shelf_id)
    }

    /// Encode all texts in a shelf using the provided encoder function.
    /// This fills in the HNSW vectors.
    pub fn encode_shelf<F>(&mut self, shelf_id: &str, encode_fn: F) -> Result<(), String>
    where
        F: Fn(&str) -> Vec<f16>,
    {
        let shelf = self
            .shelves
            .get_mut(shelf_id)
            .ok_or_else(|| format!("Shelf not found: {}", shelf_id))?;

        for (node_id, text) in &shelf.texts.clone() {
            let vector = encode_fn(text);
            shelf.hnsw.insert(*node_id, &vector);
        }

        Ok(())
    }

    /// Search within a shelf.
    pub fn search(
        &self,
        shelf_id: &str,
        query_vector: &[f16],
        limit: usize,
    ) -> Result<Vec<ShelfResult>, String> {
        let shelf = self
            .shelves
            .get(shelf_id)
            .ok_or_else(|| format!("Shelf not found: {}", shelf_id))?;

        if shelf.hnsw.is_empty() {
            return Err("Shelf has no indexed content (not encoded yet)".to_string());
        }

        let results = shelf.hnsw.search(query_vector, limit);

        let out: Vec<ShelfResult> = results
            .into_iter()
            .filter_map(|(node_id, score)| {
                let text = shelf.texts.get(&node_id)?;
                let meta = shelf.chunk_meta.get(&node_id)?;
                Some(ShelfResult {
                    text: text.clone(),
                    location: meta.location.clone(),
                    score,
                    source: meta.source.clone(),
                })
            })
            .collect();

        Ok(out)
    }

    /// Unmount a shelf (remove from manager).
    pub fn unmount(&mut self, shelf_id: &str) -> Result<(), String> {
        self.shelves
            .remove(shelf_id)
            .ok_or_else(|| format!("Shelf not found: {}", shelf_id))?;
        Ok(())
    }

    /// Scan a path and chunk into (text, metadata) pairs.
    fn scan_and_chunk(
        path: &str,
        domain: &ShelfDomain,
    ) -> Result<Vec<(String, ChunkMeta)>, String> {
        let path_obj = Path::new(path);

        if path_obj.is_file() {
            // Single file
            let text =
                std::fs::read_to_string(path).map_err(|e| format!("Failed to read file {}: {}", path, e))?;
            let chunks = match domain {
                ShelfDomain::Code => chunk_code_file(path, &text),
                ShelfDomain::Doc => chunk_by_heading(&text),
                ShelfDomain::Book | ShelfDomain::Paper => chunk_by_paragraph(&text),
                ShelfDomain::Custom => chunk_by_tokens(&text, 512),
            };
            Ok(chunks)
        } else if path_obj.is_dir() {
            // Directory - iterate files
            let mut all_chunks = Vec::new();
            let entries =
                std::fs::read_dir(path).map_err(|e| format!("Failed to read dir {}: {}", path, e))?;

            // Only process common text files
            let extensions = [
                "rs", "py", "js", "ts", "go", "md", "txt", "toml", "json", "yaml", "yml",
            ];

            for entry in entries {
                let entry = entry.map_err(|e| format!("Dir entry error: {}", e))?;
                let entry_path = entry.path();
                if entry_path.is_file() {
                    if let Some(ext) = entry_path.extension() {
                        if extensions.contains(&ext.to_str().unwrap_or("")) {
                            let file_path = entry_path.to_string_lossy().to_string();
                            if let Ok(text) = std::fs::read_to_string(&file_path) {
                                let chunks = match domain {
                                    ShelfDomain::Code => chunk_code_file(&file_path, &text),
                                    ShelfDomain::Doc => chunk_by_heading(&text),
                                    ShelfDomain::Book | ShelfDomain::Paper => {
                                        chunk_by_paragraph(&text)
                                    }
                                    ShelfDomain::Custom => chunk_by_tokens(&text, 512),
                                };
                                all_chunks.extend(chunks);
                            }
                        }
                    }
                }
            }
            Ok(all_chunks)
        } else {
            Err(format!("Path is neither file nor directory: {}", path))
        }
    }
}

// ── Chunking strategies ─────────────────────────────────────

/// Chunk source file by line, annotating with file path.
fn chunk_code_file(path: &str, text: &str) -> Vec<(String, ChunkMeta)> {
    let source = path.to_string();
    // Simple approach: treat the whole file as one chunk
    // (AST-based chunking is for future iterations)
    vec![(
        text.to_string(),
        ChunkMeta {
            source,
            location: "1-".to_string(),
            url: None,
        },
    )]
}

/// Chunk by markdown headings (doc domain).
fn chunk_by_heading(text: &str) -> Vec<(String, ChunkMeta)> {
    let mut chunks = Vec::new();
    let mut current_section = String::new();
    let mut current_heading = "top".to_string();

    for line in text.lines() {
        let trimmed = line.trim();
        if trimmed.starts_with('#') && trimmed.len() > 1 {
            // Save previous section
            if !current_section.is_empty() {
                chunks.push((
                    current_section.clone(),
                    ChunkMeta {
                        source: String::new(),
                        location: current_heading.clone(),
                        url: None,
                    },
                ));
                current_section.clear();
            }
            current_heading = trimmed.to_string();
        } else {
            if !current_section.is_empty() {
                current_section.push('\n');
            }
            current_section.push_str(line);
        }
    }

    // Last section
    if !current_section.is_empty() {
        chunks.push((
            current_section,
            ChunkMeta {
                source: String::new(),
                location: current_heading,
                url: None,
            },
        ));
    }

    chunks
}

/// Chunk by paragraph (blank-line separated).
fn chunk_by_paragraph(text: &str) -> Vec<(String, ChunkMeta)> {
    let mut chunks = Vec::new();
    let mut current = String::new();
    let mut para_idx = 0;

    for line in text.lines() {
        if line.trim().is_empty() {
            if !current.is_empty() {
                chunks.push((
                    current.clone(),
                    ChunkMeta {
                        source: String::new(),
                        location: format!("paragraph_{}", para_idx),
                        url: None,
                    },
                ));
                current.clear();
                para_idx += 1;
            }
        } else {
            if !current.is_empty() {
                current.push(' ');
            }
            current.push_str(line.trim());
        }
    }

    if !current.is_empty() {
        chunks.push((
            current,
            ChunkMeta {
                source: String::new(),
                location: format!("paragraph_{}", para_idx),
                url: None,
            },
        ));
    }

    chunks
}

/// Chunk by fixed token window (approximate: ~4 chars per token).
fn chunk_by_tokens(text: &str, approx_tokens: usize) -> Vec<(String, ChunkMeta)> {
    let char_limit = approx_tokens * 4;
    let mut chunks = Vec::new();
    let mut chunk_idx = 0;
    let mut pos = 0;

    while pos < text.len() {
        let end = (pos + char_limit).min(text.len());
        let chunk = &text[pos..end];
        chunks.push((
            chunk.to_string(),
            ChunkMeta {
                source: String::new(),
                location: format!("chunk_{}", chunk_idx),
                url: None,
            },
        ));
        pos = end;
        chunk_idx += 1;
    }

    chunks
}
