// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Public MemHop API surface.

mod action_ops;
mod archive_ops;
mod checkpoint;
mod crud_ops;
mod dream_ops;
mod graph_ops;
mod import_ops;
mod l2_ops;
mod pathway_ops;
mod profile_ops;
mod search_ops;
mod session_ops;
mod update_ops;

pub mod index_chain;
pub(crate) mod page_chain;

use memmap2::{Mmap, MmapMut};
use std::collections::HashMap;
use std::fs::{File, OpenOptions};
use std::io;
use thiserror::Error;

use crate::config::{self, MemHopConfig};
use crate::file::free_list::init_free_list;
use crate::file::header::{
    read_headers, select_valid_header, FileHeader, LAYER_ROOT_L1_INVERTED, LAYER_ROOT_L3,
    LAYER_ROOT_L6,
};
use crate::file::journal::replay_journal;
use crate::index::btree::BTreeIndex as BTree;
use crate::index::l2_meta::L2MetaIndex;
use crate::index::sparse::SparseIndex;
use crate::query::search::L1ReverseIndex;
use crate::session::SessionManager;
use crate::util::PAGE_SIZE;

/// MemHop error types
#[non_exhaustive]
#[derive(Error, Debug)]
pub enum MemHopError {
    #[error("IO error: {0}")]
    Io(#[from] io::Error),

    #[error("Invalid magic bytes")]
    InvalidMagic,

    #[error("CRC mismatch")]
    CrcMismatch,

    #[error("Invalid version: expected {expected}, got {actual}")]
    InvalidVersion { expected: u16, actual: u16 },

    #[error("Page not found: {0}")]
    PageNotFound(u32),

    #[error("File is full and extension failed or was disabled")]
    FileFull,

    #[error("Invalid page type")]
    InvalidPageType,

    #[error("Serialization error: {0}")]
    Serialization(String),

    #[error("Deserialization error: {0}")]
    Deserialization(String),

    #[error("Vector dimension mismatch: expected {expected}, got {actual}")]
    VectorDimensionMismatch { expected: usize, actual: usize },

    #[error("Configuration error: {0}")]
    ConfigError(String),

    #[error("Encoder error: {0}")]
    EncoderError(String),

    #[error("DSL parse error: {0}")]
    DslParseError(String),

    #[error("Invalid query: {0}")]
    InvalidQuery(String),
}

pub type Result<T> = std::result::Result<T, MemHopError>;

/// Main MemHop database instance
pub struct MemHop {
    pub(crate) mmap: MmapMut,
    pub(crate) file: File, // Kept for mmap lifecycle (file handle management)
    pub(crate) header: FileHeader,
    pub(crate) config: MemHopConfig,
    pub(crate) btree: BTree,
    pub(crate) sparse_index: SparseIndex,
    pub(crate) session_manager: SessionManager,
    #[cfg(feature = "grpc-encoder")]
    pub(crate) encoder: Option<Box<dyn crate::encoder::Encoder + Send + Sync>>,
    pub(crate) l1_reverse_index: L1ReverseIndex,
    pub(crate) ivf_index: Option<crate::index::vector::IVFIndex>,
    pub(crate) adjacency_cache: crate::l3::AdjacencyCache,
    pub(crate) degree_tracker: crate::l3::DegreeTracker,
    pub(crate) l3_index_map: std::collections::HashMap<u64, crate::l3::L3Index>,
    pub(crate) pathways: Vec<crate::layers::pathway::PathwayWeightSlot>,
    /// Buffered, uncommitted WAL entries produced by `update_memory`.
    pub(crate) journal_buffer: Vec<crate::file::journal::JournalEntry>,
    /// In-memory L2 context metadata index (rebuilt on open, updated on write).
    pub(crate) l2_meta: L2MetaIndex,
    pub(crate) closed: bool, // Prevent Drop from re-checkpointing after close()
}

impl MemHop {
    /// Open or create a MemHop database
    pub fn open(config: MemHopConfig) -> Result<Self> {
        let db_path = &config.db_path;

        let db_exists = db_path.exists();

        let file = if db_exists {
            let file = OpenOptions::new().read(true).write(true).open(db_path)?;
            // Verify file has minimum size (at least 2000 pages)
            let metadata = file.metadata()?;
            let min_size = 2000 * PAGE_SIZE as u64;
            if metadata.len() < min_size {
                tracing::warn!(
                    "Database file is too small ({} bytes), extending to {} bytes",
                    metadata.len(),
                    min_size
                );
                file.set_len(min_size)?;
            }
            file
        } else {
            // Create new file with initial size (2000 pages: 2 headers + 1 free list + 14 layer roots + 1983 data pages)
            // This allows storing ~990 documents (each needs engram + vector page)
            let file = OpenOptions::new()
                .read(true)
                .write(true)
                .create(true)
                .truncate(true)
                .open(db_path)?;
            file.set_len(2000 * 4096)?; // Initial 2000 pages (~8MB)
            file
        };

        let mut mmap = unsafe { MmapMut::map_mut(&file)? };

        // A/B dual-header recovery: select valid header, validate dims
        let header = if db_exists {
            // Create a read-only view for reading headers
            let mmap_readonly = unsafe { Mmap::map(&file)? };
            let (header_a, header_b) = read_headers(&mmap_readonly)?;
            let header = select_valid_header(&header_a, &header_b)?;

            // Validate vector dimension matches config
            if header.vector_dim != config.vector_dim as u16 {
                return Err(MemHopError::VectorDimensionMismatch {
                    expected: config.vector_dim,
                    actual: header.vector_dim as usize,
                });
            }

            header
        } else {
            // Initialize new header
            let mut header = FileHeader::new(config.vector_dim as u16);
            init_free_list(&mut header)?;

            // Add all data pages (from page 18 onwards) to free list in reverse order (LIFO)
            // Pages 0-1: headers, Page 2: free list head, Pages 3-17: reserved for layer roots
            // Pages 18-1999: available data pages
            for page_id in (18..2000).rev() {
                use crate::file::free_list::free_page;
                free_page(&mut mmap, &mut header, page_id)?;
            }

            header.page_count = 2000;

            // Write headers directly to mmap
            let header_bytes = header.to_bytes();
            mmap[..PAGE_SIZE].copy_from_slice(&header_bytes); // Header A
            mmap[PAGE_SIZE..PAGE_SIZE * 2].copy_from_slice(&header_bytes); // Header B
            mmap.flush()?;

            header
        };

        // Replay WAL journal to restore uncommitted writes
        let mmap_readonly = unsafe { Mmap::map(&file)? };
        let journal_entries = replay_journal(&mmap_readonly, &header)?;

        if !journal_entries.is_empty() {
            let mut sorted_entries = journal_entries;
            sorted_entries.sort_by_key(|e| e.commit_id);

            for entry in sorted_entries {
                for (page_id, data) in entry.pages {
                    let offset = (page_id as usize) * PAGE_SIZE;
                    if offset + PAGE_SIZE <= mmap.len() {
                        mmap[offset..offset + PAGE_SIZE].copy_from_slice(&data);
                    }
                }
            }
            mmap.flush()?;
        }

        let btree = Self::load_btree(&mmap_readonly, &header);
        let sparse_index = Self::load_sparse_index(&mmap_readonly, &header);

        let l1_reverse_index = if header.layer_roots[LAYER_ROOT_L1_INVERTED] != 0 {
            match Self::read_l1_reverse_pages(
                &mmap_readonly,
                &header,
                header.layer_roots[LAYER_ROOT_L1_INVERTED],
            ) {
                Ok(data) => match L1ReverseIndex::deserialize(&data) {
                    Ok(idx) => idx,
                    Err(e) => {
                        tracing::warn!(
                            "Failed to load L1 reverse index from disk: {}. Rebuilding.",
                            e
                        );
                        L1ReverseIndex::build(&mmap_readonly, &btree)?
                    }
                },
                Err(e) => {
                    tracing::warn!("Failed to read L1 reverse index pages: {}. Rebuilding.", e);
                    L1ReverseIndex::build(&mmap_readonly, &btree)?
                }
            }
        } else {
            L1ReverseIndex::build(&mmap_readonly, &btree)?
        };

        let ivf_index = match crate::index::vector::read_ivf_index(&mmap, &header) {
            Ok(Some(idx)) => {
                tracing::info!("Loaded IVF index with {} centroids", idx.k);
                Some(idx)
            }
            Ok(None) => {
                tracing::info!("No existing IVF index found, creating new one");
                Some(crate::index::vector::IVFIndex::new(
                    config.vector_dim,
                    config.ivf_initial_k,
                ))
            }
            Err(e) => {
                tracing::warn!(
                    "Failed to load IVF index from disk: {}. Creating new one.",
                    e
                );
                Some(crate::index::vector::IVFIndex::new(
                    config.vector_dim,
                    config.ivf_initial_k,
                ))
            }
        };

        let session_manager = SessionManager::new(
            config
                .session_config
                .as_ref()
                .unwrap_or(&config::SessionConfig::default()),
        );

        #[cfg(feature = "grpc-encoder")]
        let encoder: Option<Box<dyn crate::encoder::Encoder + Send + Sync>> = {
            use crate::encoder::GrpcEncoder;

            let grpc_addr = config
                .encoder_grpc_addr
                .clone()
                .or_else(|| std::env::var("MEMHOP_ENCODER_GRPC_ADDR").ok());

            match grpc_addr {
                Some(addr) => Some(Box::new(GrpcEncoder::new(&addr, config.vector_dim)?)),
                None => None,
            }
        };

        // Load L6 procedural memory pathway weights from disk.
        let pathways = if header.layer_roots[LAYER_ROOT_L6] != 0 {
            match Self::read_pathway_pages(
                &mmap_readonly,
                &header,
                header.layer_roots[LAYER_ROOT_L6],
            ) {
                Ok(data) => {
                    match crate::layers::pathway::PathwayWeightSlot::deserialize_pathways(&data) {
                        Ok(pw) => pw,
                        Err(e) => {
                            tracing::warn!(
                                "Failed to deserialize L6 pathway weights: {}. Starting empty.",
                                e
                            );
                            Vec::new()
                        }
                    }
                }
                Err(e) => {
                    tracing::warn!(
                        "Failed to read L6 pathway weight pages: {}. Starting empty.",
                        e
                    );
                    Vec::new()
                }
            }
        } else {
            Vec::new()
        };

        // Load L3 hypergraph index map from disk.
        let l3_index_map = if header.layer_roots[LAYER_ROOT_L3] != 0 {
            match Self::read_l3_index_pages(
                &mmap_readonly,
                &header,
                header.layer_roots[LAYER_ROOT_L3],
            ) {
                Ok(map) => map,
                Err(e) => {
                    tracing::warn!(
                        "Failed to load L3 index map from disk: {}. Starting empty.",
                        e
                    );
                    HashMap::new()
                }
            }
        } else {
            HashMap::new()
        };

        let l2_meta = L2MetaIndex::build(&mmap_readonly, &btree);
        let adjacency_cache_max_entries = config.adjacency_cache_max_entries;

        Ok(MemHop {
            mmap,
            file,
            header,
            config,
            btree,
            sparse_index,
            session_manager,
            #[cfg(feature = "grpc-encoder")]
            encoder,
            l1_reverse_index,
            ivf_index,
            adjacency_cache: crate::l3::AdjacencyCache::with_capacity(adjacency_cache_max_entries),
            degree_tracker: crate::l3::DegreeTracker::new(),
            l3_index_map,
            pathways,
            journal_buffer: Vec::new(),
            l2_meta,
            closed: false,
        })
    }

    // ===================================================================
    // L1ReverseIndex / L6 PathwayWeight page chain helpers
    // ===================================================================

    fn read_l1_reverse_pages(mmap: &Mmap, header: &FileHeader, start_page: u32) -> Result<Vec<u8>> {
        crate::api::page_chain::read_magic_chain(
            mmap,
            header,
            start_page,
            crate::api::page_chain::L1REVERSE_MAGIC,
        )
    }

    fn write_l1_reverse_pages(&mut self, data: &[u8]) -> Result<u32> {
        crate::api::page_chain::write_magic_chain(
            &mut self.mmap,
            &mut self.header,
            &mut self.file,
            data,
            crate::util::PageType::L1ReverseIndex,
            crate::api::page_chain::L1REVERSE_MAGIC,
        )
    }

    fn read_pathway_pages(mmap: &Mmap, header: &FileHeader, start_page: u32) -> Result<Vec<u8>> {
        crate::api::page_chain::read_magic_chain(
            mmap,
            header,
            start_page,
            crate::api::page_chain::PATHWAY_MAGIC,
        )
    }

    fn write_pathway_pages(&mut self, data: &[u8]) -> Result<u32> {
        crate::api::page_chain::write_magic_chain(
            &mut self.mmap,
            &mut self.header,
            &mut self.file,
            data,
            crate::util::PageType::PathwayWeight,
            crate::api::page_chain::PATHWAY_MAGIC,
        )
    }

    /// Rebuild IVF index from all btree entries
    fn rebuild_ivf_index(&mut self) {
        let Some(ref mut ivf) = self.ivf_index else {
            return;
        };

        // Create fresh IVF and scan btree
        let mut new_ivf =
            crate::index::vector::IVFIndex::new(self.config.vector_dim, self.config.ivf_initial_k);

        let page_data: &[u8] = &self.mmap[..];
        let dim = self.config.vector_dim;
        for (&id_hash, &page_ref) in self.btree.iter_unsorted() {
            // Only process Context-type pages (skip L4/L5/L1 etc.)
            if crate::l3::store::page_type_of(page_data, page_ref)
                != Some(crate::util::PageType::Context as u16)
            {
                continue;
            }
            if let Some(slot_data) = crate::shared::slot_io::get_slot_data(page_data, page_ref) {
                if let Ok(ctx) = crate::layers::context::ContextSlot::deserialize_slot(slot_data) {
                    if ctx.centroid_page_ref != 0 {
                        let (vec_page, vec_slot) =
                            crate::file::page::decode_page_ref(ctx.centroid_page_ref);
                        if let Ok(vector) =
                            crate::index::vector::read_vector(page_data, vec_page, vec_slot, dim)
                        {
                            if vector.len() == dim {
                                new_ivf.add_vector(id_hash, &vector, vec_page, vec_slot);
                                new_ivf.rebuild_if_needed(self.btree.len());
                            }
                        }
                    }
                }
            }
        }

        *ivf = new_ivf;
    }

    /// Sync all changes to disk
    pub fn sync(&self) -> Result<()> {
        self.mmap.flush()?;
        Ok(())
    }

    /// Set a custom encoder for vector operations
    ///
    /// # Arguments
    /// * `encoder` - Encoder implementation (e.g., GrpcEncoder)
    #[cfg(feature = "grpc-encoder")]
    pub fn set_encoder<E: crate::encoder::Encoder + Send + Sync + 'static>(&mut self, encoder: E) {
        self.encoder = Some(Box::new(encoder));
    }

    /// Close the database and release resources
    pub fn close(mut self) -> Result<()> {
        // 1. Final checkpoint to persist all changes
        self.checkpoint()?;

        // 2. Truncate Journal: 将 journal_start 和 journal_len 置零
        self.header.journal_start = 0;
        self.header.journal_len = 0;
        let header_bytes = self.header.to_bytes();
        // Write B first (backup), then A (primary) — alternating for crash safety
        self.mmap[PAGE_SIZE..PAGE_SIZE * 2].copy_from_slice(&header_bytes);
        self.mmap.flush_range(PAGE_SIZE, PAGE_SIZE)?;
        self.mmap[..PAGE_SIZE].copy_from_slice(&header_bytes);
        self.mmap.flush_range(0, PAGE_SIZE)?;

        // 3. Mark as closed to prevent Drop from re-checkpointing
        self.closed = true;

        // 4. File will be closed when dropped
        Ok(())
    }
}

impl Drop for MemHop {
    fn drop(&mut self) {
        if !self.closed {
            if let Err(e) = self.checkpoint() {
                tracing::error!("Failed to checkpoint on drop: {}", e);
            }
        }
    }
}

#[cfg(test)]
mod tests;
