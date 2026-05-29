//! Knowledge shelf module — mount, query, and unmount external knowledge trees.
//!
//! v0.11.0: ShelfManager is a metadata-only manager for mounted knowledge trees.
//! All Knowledge engrams are stored directly in Brain's LMDB via brain.store().
//! No longer owns HNSW, SparseIndex, or text content — HNSW search is handled
//! by Brain::recall() with tree_path filter.

pub mod scanner;
pub mod chunker;

use std::collections::HashMap;

use serde::{Deserialize, Serialize};

use crate::brain::Brain;
use crate::engram::EngramKind;
use crate::types::{ChunkMeta, ForgetFilter, MountResult, ShelfDomain, StoreStatus, UnmountResult};

/// v0.11.0: Mounted knowledge tree metadata.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TreeMeta {
    pub tree_path: String,
    pub domain: ShelfDomain,
    pub file_count: usize,
    pub chunk_count: usize,
    pub mounted_at: i64,
}

/// v0.11.0: Metadata-only manager for mounted knowledge trees.
/// No longer owns HNSW, SparseIndex, or text content.
/// All Knowledge engrams are stored in the Brain's LMDB.
#[derive(Serialize, Deserialize, Default)]
pub struct ShelfManager {
    /// tree_path → metadata
    pub trees: HashMap<String, TreeMeta>,
}

impl ShelfManager {
    pub fn new() -> Self {
        ShelfManager {
            trees: HashMap::new(),
        }
    }

    /// Mount a knowledge tree: scan → chunk → brain.store(kind=Knowledge).
    pub fn mount(
        &mut self,
        brain: &mut Brain,
        path: &str,
        domain: ShelfDomain,
    ) -> Result<MountResult, String> {
        // 1. Scan
        let files = scanner::scan_directory(path, &domain)?;

        // 2. Chunk
        let mut chunks: Vec<(String, ChunkMeta)> = Vec::new();
        for file in &files {
            let file_chunks = match domain {
                ShelfDomain::Code => chunker::chunk_code(&file.path, &file.text),
                ShelfDomain::Doc => chunker::chunk_doc(&file.text),
                ShelfDomain::Book | ShelfDomain::Paper => chunker::chunk_paper(&file.text),
                _ => chunker::chunk_custom(&file.text),
            };
            for (text, meta) in file_chunks {
                chunks.push((text, meta));
            }
        }

        if chunks.is_empty() {
            return Ok(MountResult {
                tree_path: path.to_string(),
                chunk_count: 0,
                domain: domain.to_string(),
                warnings: vec!["No readable content found".to_string()],
            });
        }

        // 3. Encode & store each chunk
        let mut stored = 0usize;
        let mut warnings = Vec::new();

        for (text, meta) in &chunks {
            let vector = brain.encode_text(text);
            let result = brain
                .store(
                    text,
                    &vector,
                    EngramKind::Knowledge,
                    Some(path.to_string()),
                    Some(meta.source.clone()),
                    Some(meta.location.clone()),
                )
                .map_err(|e| format!("store failed: {}", e))?;

            match result.status {
                StoreStatus::Stored => stored += 1,
                StoreStatus::Duplicate => {}
            }
        }

        // 4. Register in metadata
        let now = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap_or_default()
            .as_millis() as i64;
        self.trees.insert(
            path.to_string(),
            TreeMeta {
                tree_path: path.to_string(),
                domain,
                file_count: files.len(),
                chunk_count: chunks.len(),
                mounted_at: now,
            },
        );

        if stored == 0 {
            warnings.push("All chunks are duplicates (no new content stored)".to_string());
        }

        Ok(MountResult {
            tree_path: path.to_string(),
            chunk_count: stored,
            domain: domain.to_string(),
            warnings,
        })
    }

    /// Unmount a knowledge tree: forget_batch all chunks.
    pub fn unmount(
        &mut self,
        brain: &mut Brain,
        tree_path: &str,
    ) -> Result<UnmountResult, String> {
        if !self.trees.contains_key(tree_path) {
            return Err(format!("Tree not found: {}", tree_path));
        }

        let filter = ForgetFilter::ByTreePath(tree_path.to_string());
        let deleted = brain
            .forget_batch(&filter)
            .map_err(|e| format!("forget_batch failed: {}", e))?;

        self.trees.remove(tree_path);

        Ok(UnmountResult {
            tree_path: tree_path.to_string(),
            deleted_count: deleted,
        })
    }

    /// Rebuild tree registry from LMDB by scanning Knowledge engrams.
    pub fn rebuild_registry(&mut self, brain: &Brain) -> Result<(), String> {
        let rtxn = brain
            .storage
            .begin_read()
            .map_err(|e| format!("LMDB read: {}", e))?;
        let entries = brain
            .storage
            .all_hippocampus_entries(&rtxn)
            .map_err(|e| format!("scan: {}", e))?;
        drop(rtxn);

        self.trees.clear();
        for (_, engram) in entries {
            if engram.kind == EngramKind::Knowledge
                && let Some(ref tp) = engram.tree_path
            {
                let entry = self.trees.entry(tp.clone()).or_insert(TreeMeta {
                        tree_path: tp.clone(),
                        domain: ShelfDomain::Generic,
                        file_count: 0,
                        chunk_count: 0,
                        mounted_at: engram.created_at,
                    });
                    entry.chunk_count += 1;

                    // Use earliest created_at as the mount time
                    if engram.created_at < entry.mounted_at {
                        entry.mounted_at = engram.created_at;
                    }
                }
        }
        Ok(())
    }

    /// List all mounted trees.
    pub fn get_trees(&self) -> Vec<&TreeMeta> {
        self.trees.values().collect()
    }

    /// Get metadata for a specific tree.
    pub fn get_tree(&self, tree_path: &str) -> Option<&TreeMeta> {
        self.trees.get(tree_path)
    }

    /// Check if a tree is mounted.
    pub fn is_mounted(&self, tree_path: &str) -> bool {
        self.trees.contains_key(tree_path)
    }
}
