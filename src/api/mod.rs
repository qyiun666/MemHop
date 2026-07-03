// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Public MemHop API surface.

mod checkpoint;
mod crud_ops;
mod dream_ops;
mod graph_ops;
mod import_ops;
mod pathway_ops;
mod search_ops;
mod session_ops;
mod update_ops;

use memmap2::{Mmap, MmapMut};
use std::collections::HashMap;
use std::fs::{File, OpenOptions};
use std::io;
use thiserror::Error;

use crate::config::{self, MemHopConfig};
use crate::file::free_list::init_free_list;
use crate::file::header::{
    read_headers, select_valid_header, FileHeader, LAYER_ROOT_BTREE, LAYER_ROOT_L1_INVERTED,
    LAYER_ROOT_L3, LAYER_ROOT_L6, LAYER_ROOT_SPARSE,
};
use crate::file::journal::replay_journal;
use crate::file::page::{read_page_data, write_page_data};
use crate::index::btree::BTreeIndex as BTree;
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

/// Parsed contents of a multi-page SparseIndex directory page.
struct SparseDirectory {
    term_bucket_count: u32,
    doc_bucket_count: u32,
    term_count: u32,
    doc_count: u32,
    total_term_count: u64,
    avg_doc_length: f32,
    k1: f32,
    b: f32,
    entity_start: u32,
    term_primary_pages: Vec<u32>,
    doc_primary_pages: Vec<u32>,
}

impl SparseDirectory {
    fn parse(data: &[u8]) -> Option<Self> {
        if data.len() < 44 {
            return None;
        }
        let magic = u32::from_le_bytes([data[0], data[1], data[2], data[3]]);
        if magic != crate::index::sparse::SPARSE_MAGIC {
            return None;
        }
        let term_bucket_count = u32::from_le_bytes([data[4], data[5], data[6], data[7]]);
        let doc_bucket_count = u32::from_le_bytes([data[8], data[9], data[10], data[11]]);
        let term_count = u32::from_le_bytes([data[12], data[13], data[14], data[15]]);
        let doc_count = u32::from_le_bytes([data[16], data[17], data[18], data[19]]);
        let total_term_count = u64::from_le_bytes([
            data[20], data[21], data[22], data[23], data[24], data[25], data[26], data[27],
        ]);
        let avg_doc_length = f32::from_le_bytes([data[28], data[29], data[30], data[31]]);
        let k1 = f32::from_le_bytes([data[32], data[33], data[34], data[35]]);
        let b = f32::from_le_bytes([data[36], data[37], data[38], data[39]]);
        let entity_start = u32::from_le_bytes([data[40], data[41], data[42], data[43]]);

        let mut offset = 44usize;
        let term_pages_len = term_bucket_count as usize * 4;
        let doc_pages_len = doc_bucket_count as usize * 4;
        if data.len() < offset + term_pages_len + doc_pages_len {
            return None;
        }

        let mut term_primary_pages = Vec::with_capacity(term_bucket_count as usize);
        for _ in 0..term_bucket_count {
            term_primary_pages.push(u32::from_le_bytes([
                data[offset],
                data[offset + 1],
                data[offset + 2],
                data[offset + 3],
            ]));
            offset += 4;
        }

        let mut doc_primary_pages = Vec::with_capacity(doc_bucket_count as usize);
        for _ in 0..doc_bucket_count {
            doc_primary_pages.push(u32::from_le_bytes([
                data[offset],
                data[offset + 1],
                data[offset + 2],
                data[offset + 3],
            ]));
            offset += 4;
        }

        Some(Self {
            term_bucket_count,
            doc_bucket_count,
            term_count,
            doc_count,
            total_term_count,
            avg_doc_length,
            k1,
            b,
            entity_start,
            term_primary_pages,
            doc_primary_pages,
        })
    }
}

/// Main MemHop database instance
pub struct MemHop {
    pub(crate) mmap: MmapMut,
    #[allow(dead_code)]
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
            adjacency_cache: crate::l3::AdjacencyCache::new(),
            degree_tracker: crate::l3::DegreeTracker::new(),
            l3_index_map,
            pathways,
            closed: false,
        })
    }

    /// Load the B-tree index from disk.
    ///
    /// Reads the Linear Hash bucket layout starting at the page recorded in
    /// `header.reserved[8..12]`. Falls back to the legacy single-page format
    /// stored at `header.layer_roots[LAYER_ROOT_BTREE]` if the new metadata is missing or
    /// invalid, and finally falls back to an empty index.
    fn load_btree(mmap: &Mmap, header: &FileHeader) -> BTree {
        use crate::index::btree::EMPTY_PAGE;

        let bucket_count = u32::from_le_bytes([
            header.reserved[0],
            header.reserved[1],
            header.reserved[2],
            header.reserved[3],
        ]);
        let split_pointer = u32::from_le_bytes([
            header.reserved[4],
            header.reserved[5],
            header.reserved[6],
            header.reserved[7],
        ]);
        let directory_page = u32::from_le_bytes([
            header.reserved[8],
            header.reserved[9],
            header.reserved[10],
            header.reserved[11],
        ]);

        // Try new multi-page Linear Hash format using a directory page.
        if directory_page != 0 && directory_page < header.page_count && bucket_count > 0 {
            let dir_offset = (directory_page as usize) * PAGE_SIZE + 32;
            let dir_data = &mmap[dir_offset..dir_offset + PAGE_SIZE - 32];
            let primary_pages = Self::read_directory_page(dir_data, bucket_count);
            if primary_pages.len() == bucket_count as usize {
                let mut buckets: Vec<Vec<Vec<u8>>> = Vec::with_capacity(bucket_count as usize);
                let mut valid = true;

                for primary_page in primary_pages {
                    if primary_page == 0 || primary_page >= header.page_count {
                        valid = false;
                        break;
                    }

                    let mut bucket_pages: Vec<Vec<u8>> = Vec::new();
                    let mut current_page = primary_page;
                    let mut chain_len = 0u32;
                    while current_page != EMPTY_PAGE && current_page < header.page_count {
                        match read_page_data(mmap, current_page) {
                            Ok(data) => bucket_pages.push(data.to_vec()),
                            Err(_) => break,
                        }
                        match crate::file::page::read_page_header(mmap, current_page) {
                            Ok(page_header) => current_page = page_header.next_page,
                            Err(_) => break,
                        }
                        chain_len += 1;
                        // Safety guard against corrupt circular chains.
                        if chain_len > header.page_count {
                            valid = false;
                            break;
                        }
                    }
                    buckets.push(bucket_pages);
                }

                if valid {
                    match BTree::deserialize_from_pages(&buckets, bucket_count, split_pointer) {
                        Ok(index) => return index,
                        Err(e) => {
                            tracing::warn!(
                                "Failed to load Linear Hash B-tree from disk: {}. Trying legacy format.",
                                e
                            );
                        }
                    }
                }
            }
        }

        // Fall back to legacy single-page format.
        let btree_page = header.layer_roots[LAYER_ROOT_BTREE];
        if btree_page != 0 && btree_page < header.page_count {
            match read_page_data(mmap, btree_page) {
                Ok(data) => match BTree::deserialize(data) {
                    Ok(index) => return index,
                    Err(e) => {
                        tracing::warn!(
                            "Failed to load legacy B-tree from disk: {}. Using empty index.",
                            e
                        );
                    }
                },
                Err(e) => {
                    tracing::warn!(
                        "Failed to read legacy B-tree page: {}. Using empty index.",
                        e
                    );
                }
            }
        }

        BTree::new()
    }

    /// Parse the bucket directory page data and return the primary page id for each bucket.
    fn read_directory_page(dir_data: &[u8], bucket_count: u32) -> Vec<u32> {
        let mut primary_pages = Vec::new();
        let needed = 8 + bucket_count as usize * 4;
        if dir_data.len() < needed {
            return primary_pages;
        }
        for i in 0..bucket_count as usize {
            let off = 8 + i * 4;
            primary_pages.push(u32::from_le_bytes([
                dir_data[off],
                dir_data[off + 1],
                dir_data[off + 2],
                dir_data[off + 3],
            ]));
        }
        primary_pages
    }

    /// Free all pages belonging to the current on-disk B-tree index.
    #[allow(dead_code)]
    fn free_btree_pages(&mut self) -> Result<()> {
        let directory_page = u32::from_le_bytes([
            self.header.reserved[8],
            self.header.reserved[9],
            self.header.reserved[10],
            self.header.reserved[11],
        ]);
        let bucket_count = u32::from_le_bytes([
            self.header.reserved[0],
            self.header.reserved[1],
            self.header.reserved[2],
            self.header.reserved[3],
        ]);

        if directory_page != 0 && directory_page < self.header.page_count && bucket_count > 0 {
            // Free new-format bucket chains via directory page.
            let dir_offset = (directory_page as usize) * PAGE_SIZE + 32;
            let dir_data = &self.mmap[dir_offset..dir_offset + PAGE_SIZE - 32];
            let primary_pages = Self::read_directory_page(dir_data, bucket_count);
            for primary_page in primary_pages {
                if primary_page != 0 && primary_page < self.header.page_count {
                    Self::free_page_chain(&mut self.mmap, &mut self.header, primary_page)?;
                }
            }
            // Free directory page itself.
            crate::file::free_list::free_page(&mut self.mmap, &mut self.header, directory_page)?;
        } else {
            // Free legacy single-page btree if present.
            let btree_page = self.header.layer_roots[LAYER_ROOT_BTREE];
            if btree_page != 0 && btree_page < self.header.page_count {
                crate::file::free_list::free_page(&mut self.mmap, &mut self.header, btree_page)?;
            }
        }

        // Clear btree metadata in header.
        self.header.reserved[0..12].fill(0);
        self.header.layer_roots[LAYER_ROOT_BTREE] = 0;

        Ok(())
    }

    /// Free a chain of pages starting at `start_page`.
    fn free_page_chain(mmap: &mut MmapMut, header: &mut FileHeader, start_page: u32) -> Result<()> {
        use crate::index::btree::EMPTY_PAGE;

        let mut current = start_page;
        let mut chain_len = 0u32;
        while current != EMPTY_PAGE && current < header.page_count {
            let next = {
                let offset = (current as usize) * PAGE_SIZE;
                let mut bytes = [0u8; 32];
                bytes.copy_from_slice(&mmap[offset..offset + 32]);
                match crate::file::page::PageHeader::from_bytes(&bytes) {
                    Ok(h) => h.next_page,
                    Err(_) => break,
                }
            };
            crate::file::free_list::free_page(mmap, header, current)?;
            current = next;
            chain_len += 1;
            if chain_len > header.page_count {
                break;
            }
        }
        Ok(())
    }

    // ===================================================================
    // SparseIndex multi-page serialization helpers
    // ===================================================================

    /// Read all page payloads in a chain starting at `start_page`.
    fn read_sparse_chain(mmap: &Mmap, header: &FileHeader, start_page: u32) -> Vec<Vec<u8>> {
        use crate::index::btree::EMPTY_PAGE;

        let mut pages = Vec::new();
        let mut current = start_page;
        let mut chain_len = 0u32;
        while current != EMPTY_PAGE && current < header.page_count {
            match read_page_data(mmap, current) {
                Ok(data) => pages.push(data.to_vec()),
                Err(_) => break,
            }
            match crate::file::page::read_page_header(mmap, current) {
                Ok(page_header) => current = page_header.next_page,
                Err(_) => break,
            }
            chain_len += 1;
            if chain_len > header.page_count {
                break;
            }
        }
        pages
    }

    /// Allocate a chain of file pages and write payloads to them.
    /// Returns the primary page id, or 0 if `payloads` is empty.
    fn allocate_sparse_chain(&mut self, payloads: &[Vec<u8>]) -> Result<u32> {
        use crate::index::btree::EMPTY_PAGE;

        if payloads.is_empty() {
            return Ok(0);
        }

        let mut next = EMPTY_PAGE;
        let mut page_ids = vec![0u32; payloads.len()];
        for (i, _payload) in payloads.iter().enumerate().rev() {
            let page_type = if i == 0 {
                crate::util::PageType::SparseIndex
            } else {
                crate::util::PageType::Overflow
            };
            let page_id = self.allocate_page(page_type, 0, next)?;
            page_ids[i] = page_id;
            next = page_id;
        }

        for (page_id, payload) in page_ids.iter().zip(payloads.iter()) {
            write_page_data(&mut self.mmap, *page_id, payload)?;
        }

        Ok(page_ids[0])
    }

    /// Free all pages used by the current on-disk SparseIndex.
    #[allow(dead_code)]
    fn free_sparse_pages(&mut self) -> Result<()> {
        let directory_page = self.header.layer_roots[LAYER_ROOT_SPARSE];
        if directory_page == 0 || directory_page >= self.header.page_count {
            return Ok(());
        }

        let dir_data = match read_page_data(&self.mmap, directory_page) {
            Ok(d) => d,
            Err(_) => return Ok(()),
        };

        // Multi-page format has a magic header.
        if dir_data.len() >= 4 {
            let magic = u32::from_le_bytes([dir_data[0], dir_data[1], dir_data[2], dir_data[3]]);
            if magic == crate::index::sparse::SPARSE_MAGIC {
                if let Some(dir) = SparseDirectory::parse(dir_data) {
                    for &page_id in &dir.term_primary_pages {
                        if page_id != 0 {
                            Self::free_page_chain(&mut self.mmap, &mut self.header, page_id)?;
                        }
                    }
                    for &page_id in &dir.doc_primary_pages {
                        if page_id != 0 {
                            Self::free_page_chain(&mut self.mmap, &mut self.header, page_id)?;
                        }
                    }
                    if dir.entity_start != 0 {
                        Self::free_page_chain(&mut self.mmap, &mut self.header, dir.entity_start)?;
                    }
                }
            }
        }

        crate::file::free_list::free_page(&mut self.mmap, &mut self.header, directory_page)?;
        self.header.layer_roots[LAYER_ROOT_SPARSE] = 0;
        Ok(())
    }

    /// Write SparseIndex page chains and return the directory page id.
    fn write_sparse_pages(
        &mut self,
        page_data: &crate::index::sparse::SparsePageData,
    ) -> Result<u32> {
        let mut term_starts = Vec::with_capacity(page_data.term_bucket_count as usize);
        for bucket in &page_data.term_buckets {
            term_starts.push(self.allocate_sparse_chain(bucket)?);
        }

        let mut doc_starts = Vec::with_capacity(page_data.doc_bucket_count as usize);
        for bucket in &page_data.doc_buckets {
            doc_starts.push(self.allocate_sparse_chain(bucket)?);
        }

        let entity_start = self.allocate_sparse_chain(&page_data.entity_chain)?;

        let directory_payload = crate::index::sparse::build_sparse_directory(
            page_data,
            &term_starts,
            &doc_starts,
            entity_start,
        );
        let directory_page = self.allocate_page(
            crate::util::PageType::SparseIndex,
            0,
            crate::index::btree::EMPTY_PAGE,
        )?;
        write_page_data(&mut self.mmap, directory_page, &directory_payload)?;
        Ok(directory_page)
    }

    /// Load the SparseIndex from disk, supporting both the new multi-page
    /// format and the legacy single-page bincode format.
    fn load_sparse_index(mmap: &Mmap, header: &FileHeader) -> SparseIndex {
        let directory_page = header.layer_roots[LAYER_ROOT_SPARSE];
        if directory_page == 0 || directory_page >= header.page_count {
            return SparseIndex::new();
        }

        let dir_data = match read_page_data(mmap, directory_page) {
            Ok(d) => d,
            Err(_) => return SparseIndex::new(),
        };

        if dir_data.len() >= 4 {
            let magic = u32::from_le_bytes([dir_data[0], dir_data[1], dir_data[2], dir_data[3]]);
            if magic == crate::index::sparse::SPARSE_MAGIC {
                if let Some(dir) = SparseDirectory::parse(dir_data) {
                    let mut term_buckets = Vec::with_capacity(dir.term_bucket_count as usize);
                    for &page_id in &dir.term_primary_pages {
                        if page_id == 0 {
                            term_buckets.push(Vec::new());
                        } else {
                            term_buckets.push(Self::read_sparse_chain(mmap, header, page_id));
                        }
                    }

                    let mut doc_buckets = Vec::with_capacity(dir.doc_bucket_count as usize);
                    for &page_id in &dir.doc_primary_pages {
                        if page_id == 0 {
                            doc_buckets.push(Vec::new());
                        } else {
                            doc_buckets.push(Self::read_sparse_chain(mmap, header, page_id));
                        }
                    }

                    let entity_chain = if dir.entity_start == 0 {
                        Vec::new()
                    } else {
                        Self::read_sparse_chain(mmap, header, dir.entity_start)
                    };

                    let page_data = crate::index::sparse::SparsePageData {
                        term_bucket_count: dir.term_bucket_count,
                        doc_bucket_count: dir.doc_bucket_count,
                        term_count: dir.term_count,
                        doc_count: dir.doc_count,
                        total_term_count: dir.total_term_count,
                        avg_doc_length: dir.avg_doc_length,
                        k1: dir.k1,
                        b: dir.b,
                        term_buckets,
                        doc_buckets,
                        entity_chain,
                    };

                    match SparseIndex::deserialize_from_pages(&page_data) {
                        Ok(index) => return index,
                        Err(e) => {
                            tracing::warn!(
                                "Failed to load multi-page Sparse Index: {}. Trying legacy format.",
                                e
                            );
                            // Fall through to legacy single-page format below.
                        }
                    }
                }
            }
        }

        // Legacy single-page bincode format.
        match SparseIndex::deserialize(dir_data) {
            Ok(index) => index,
            Err(e) => {
                tracing::warn!(
                    "Failed to load Sparse Index from disk: {}. Using empty index.",
                    e
                );
                SparseIndex::new()
            }
        }
    }

    // ===================================================================
    // L3Index multi-page persistence helpers
    // ===================================================================

    /// Magic number for the L3 index directory page.
    const L3_INDEX_DIRECTORY_MAGIC: u32 = 0x4C334444; // "L3DD"

    /// Read the L3 hypergraph index map from disk.
    fn read_l3_index_pages(
        mmap: &Mmap,
        header: &FileHeader,
        root_page: u32,
    ) -> Result<HashMap<u64, crate::l3::L3Index>> {
        const HEADER_SIZE: usize = 8;
        const ENTRY_SIZE: usize = 12;

        if root_page == 0 || root_page >= header.page_count {
            return Err(MemHopError::InvalidPageType);
        }

        let dir_data = read_page_data(mmap, root_page)?;
        if dir_data.len() < HEADER_SIZE {
            return Err(MemHopError::Serialization(
                "L3 index directory page too small".to_string(),
            ));
        }

        let magic = u32::from_le_bytes([dir_data[0], dir_data[1], dir_data[2], dir_data[3]]);
        if magic != Self::L3_INDEX_DIRECTORY_MAGIC {
            return Err(MemHopError::Serialization(
                "L3 index directory magic mismatch".to_string(),
            ));
        }

        let count =
            u32::from_le_bytes([dir_data[4], dir_data[5], dir_data[6], dir_data[7]]) as usize;
        if dir_data.len() < HEADER_SIZE + count * ENTRY_SIZE {
            return Err(MemHopError::Serialization(
                "L3 index directory entries truncated".to_string(),
            ));
        }

        let mut map = HashMap::with_capacity(count);
        for i in 0..count {
            let off = HEADER_SIZE + i * ENTRY_SIZE;
            let graph_id = u64::from_le_bytes([
                dir_data[off],
                dir_data[off + 1],
                dir_data[off + 2],
                dir_data[off + 3],
                dir_data[off + 4],
                dir_data[off + 5],
                dir_data[off + 6],
                dir_data[off + 7],
            ]);
            let first_page = u32::from_le_bytes([
                dir_data[off + 8],
                dir_data[off + 9],
                dir_data[off + 10],
                dir_data[off + 11],
            ]);

            if first_page == 0 || first_page >= header.page_count {
                tracing::warn!(
                    "Skipping L3 index entry for graph {}: invalid first page {}",
                    graph_id,
                    first_page
                );
                continue;
            }

            match crate::l3::L3Index::read_from_pages(mmap, first_page)
                .map_err(MemHopError::Serialization)
            {
                Ok(index) => {
                    map.insert(graph_id, index);
                }
                Err(e) => {
                    tracing::warn!(
                        "Failed to read L3 index for graph {}: {}. Skipping.",
                        graph_id,
                        e
                    );
                }
            }
        }

        Ok(map)
    }

    // ===================================================================
    // L1ReverseIndex multi-page serialization helpers
    // ===================================================================

    /// Magic number for the L1 reverse index page chain.
    const L1REVERSE_MAGIC: u32 = 0x4C315256; // "L1RV"

    /// Header size on the first page: [magic: u32][total_length: u32].
    const L1REVERSE_HEADER_SIZE: usize = 8;

    /// Maximum serialized data bytes stored in the first page (after header).
    const L1REVERSE_FIRST_PAGE_DATA_CAPACITY: usize = PAGE_SIZE - 32 - Self::L1REVERSE_HEADER_SIZE;

    /// Maximum serialized data bytes stored in each subsequent overflow page.
    const L1REVERSE_OVERFLOW_DATA_CAPACITY: usize = PAGE_SIZE - 32;

    /// Read the L1 reverse index page chain starting at `start_page` and
    /// return the serialized bytes.
    fn read_l1_reverse_pages(mmap: &Mmap, header: &FileHeader, start_page: u32) -> Result<Vec<u8>> {
        use crate::index::btree::EMPTY_PAGE;

        if start_page == 0 || start_page >= header.page_count {
            return Err(MemHopError::InvalidPageType);
        }

        let first_payload = read_page_data(mmap, start_page)?;
        if first_payload.len() < Self::L1REVERSE_HEADER_SIZE {
            return Err(MemHopError::Serialization(
                "L1 reverse index page too small".to_string(),
            ));
        }

        let magic = u32::from_le_bytes([
            first_payload[0],
            first_payload[1],
            first_payload[2],
            first_payload[3],
        ]);
        if magic != Self::L1REVERSE_MAGIC {
            return Err(MemHopError::Serialization(
                "L1 reverse index magic mismatch".to_string(),
            ));
        }

        let total_length = u32::from_le_bytes([
            first_payload[4],
            first_payload[5],
            first_payload[6],
            first_payload[7],
        ]) as usize;

        // Gather payloads from the chain.
        let mut pages = Vec::new();
        pages.push(first_payload.to_vec());
        let mut current = start_page;
        let mut chain_len = 0u32;
        loop {
            let page_header = crate::file::page::read_page_header(mmap, current)?;
            current = page_header.next_page;
            if current == EMPTY_PAGE || current >= header.page_count {
                break;
            }
            pages.push(read_page_data(mmap, current)?.to_vec());
            chain_len += 1;
            if chain_len > header.page_count {
                return Err(MemHopError::Serialization(
                    "L1 reverse index chain too long".to_string(),
                ));
            }
        }

        // Concatenate data from all pages, skipping the header on the first page.
        let mut result = Vec::with_capacity(total_length);
        let first_data = &first_payload[Self::L1REVERSE_HEADER_SIZE..];
        let first_take = first_data.len().min(total_length);
        result.extend_from_slice(&first_data[..first_take]);

        for payload in pages.iter().skip(1) {
            if result.len() >= total_length {
                break;
            }
            let remaining = total_length - result.len();
            let take = payload.len().min(remaining);
            result.extend_from_slice(&payload[..take]);
        }

        if result.len() != total_length {
            return Err(MemHopError::Serialization(
                "L1 reverse index length mismatch".to_string(),
            ));
        }

        Ok(result)
    }

    /// Write serialized L1 reverse index bytes to a page chain.
    /// Returns the primary page id, or 0 if `data` is empty.
    fn write_l1_reverse_pages(&mut self, data: &[u8]) -> Result<u32> {
        use crate::index::btree::EMPTY_PAGE;

        if data.is_empty() {
            return Ok(0);
        }

        let total_length = data.len();
        let first_capacity = Self::L1REVERSE_FIRST_PAGE_DATA_CAPACITY;
        let overflow_capacity = Self::L1REVERSE_OVERFLOW_DATA_CAPACITY;

        let overflow_needed = if total_length > first_capacity {
            (total_length - first_capacity).div_ceil(overflow_capacity)
        } else {
            0
        };
        let page_count = 1 + overflow_needed;

        // Allocate pages in reverse order so we can link next_page easily.
        let mut page_ids = vec![0u32; page_count];
        let mut next = EMPTY_PAGE;
        for i in (0..page_count).rev() {
            let page_type = if i == 0 {
                crate::util::PageType::L1ReverseIndex
            } else {
                crate::util::PageType::Overflow
            };
            let page_id = self.allocate_page(page_type, 0, next)?;
            page_ids[i] = page_id;
            next = page_id;
        }

        // Build the first page payload.
        let first_data_len = total_length.min(first_capacity);
        let mut first_payload = Vec::with_capacity(Self::L1REVERSE_HEADER_SIZE + first_data_len);
        first_payload.extend_from_slice(&Self::L1REVERSE_MAGIC.to_le_bytes());
        first_payload.extend_from_slice(&(total_length as u32).to_le_bytes());
        first_payload.extend_from_slice(&data[..first_data_len]);
        write_page_data(&mut self.mmap, page_ids[0], &first_payload)?;

        // Write overflow pages.
        let mut offset = first_data_len;
        for &page_id in page_ids.iter().skip(1) {
            let end = (offset + overflow_capacity).min(total_length);
            write_page_data(&mut self.mmap, page_id, &data[offset..end])?;
            offset = end;
        }

        Ok(page_ids[0])
    }

    /// Free all pages used by the current on-disk L1 reverse index.
    #[allow(dead_code)]
    fn free_l1_reverse_pages(&mut self) -> Result<()> {
        let start_page = self.header.layer_roots[LAYER_ROOT_L1_INVERTED];
        if start_page == 0 || start_page >= self.header.page_count {
            return Ok(());
        }

        Self::free_page_chain(&mut self.mmap, &mut self.header, start_page)?;
        self.header.layer_roots[LAYER_ROOT_L1_INVERTED] = 0;
        Ok(())
    }

    // ===================================================================
    // L6 PathwayWeight multi-page serialization helpers
    // ===================================================================

    /// Magic number for the L6 pathway weight page chain.
    const PATHWAY_MAGIC: u32 = 0x4C365057; // "L6PW"

    /// Header size on the first page: [magic: u32][total_length: u32].
    const PATHWAY_HEADER_SIZE: usize = 8;

    /// Maximum serialized data bytes stored in the first page (after header).
    const PATHWAY_FIRST_PAGE_DATA_CAPACITY: usize = PAGE_SIZE - 32 - Self::PATHWAY_HEADER_SIZE;

    /// Maximum serialized data bytes stored in each subsequent overflow page.
    const PATHWAY_OVERFLOW_DATA_CAPACITY: usize = PAGE_SIZE - 32;

    /// Read the L6 pathway weight page chain starting at `start_page`.
    fn read_pathway_pages(mmap: &Mmap, header: &FileHeader, start_page: u32) -> Result<Vec<u8>> {
        use crate::index::btree::EMPTY_PAGE;

        if start_page == 0 || start_page >= header.page_count {
            return Err(MemHopError::InvalidPageType);
        }

        let first_payload = read_page_data(mmap, start_page)?;
        if first_payload.len() < Self::PATHWAY_HEADER_SIZE {
            return Err(MemHopError::Serialization(
                "L6 pathway weight page too small".to_string(),
            ));
        }

        let magic = u32::from_le_bytes([
            first_payload[0],
            first_payload[1],
            first_payload[2],
            first_payload[3],
        ]);
        if magic != Self::PATHWAY_MAGIC {
            return Err(MemHopError::Serialization(
                "L6 pathway weight magic mismatch".to_string(),
            ));
        }

        let total_length = u32::from_le_bytes([
            first_payload[4],
            first_payload[5],
            first_payload[6],
            first_payload[7],
        ]) as usize;

        let mut pages = Vec::new();
        pages.push(first_payload.to_vec());
        let mut current = start_page;
        let mut chain_len = 0u32;
        loop {
            let page_header = crate::file::page::read_page_header(mmap, current)?;
            current = page_header.next_page;
            if current == EMPTY_PAGE || current >= header.page_count {
                break;
            }
            pages.push(read_page_data(mmap, current)?.to_vec());
            chain_len += 1;
            if chain_len > header.page_count {
                return Err(MemHopError::Serialization(
                    "L6 pathway weight chain too long".to_string(),
                ));
            }
        }

        let mut result = Vec::with_capacity(total_length);
        let first_data = &first_payload[Self::PATHWAY_HEADER_SIZE..];
        let first_take = first_data.len().min(total_length);
        result.extend_from_slice(&first_data[..first_take]);

        for payload in pages.iter().skip(1) {
            if result.len() >= total_length {
                break;
            }
            let remaining = total_length - result.len();
            let take = payload.len().min(remaining);
            result.extend_from_slice(&payload[..take]);
        }

        if result.len() != total_length {
            return Err(MemHopError::Serialization(
                "L6 pathway weight length mismatch".to_string(),
            ));
        }

        Ok(result)
    }

    /// Write serialized L6 pathway weight bytes to a page chain.
    /// Returns the primary page id, or 0 if `data` is empty.
    fn write_pathway_pages(&mut self, data: &[u8]) -> Result<u32> {
        use crate::index::btree::EMPTY_PAGE;

        if data.is_empty() {
            return Ok(0);
        }

        let total_length = data.len();
        let first_capacity = Self::PATHWAY_FIRST_PAGE_DATA_CAPACITY;
        let overflow_capacity = Self::PATHWAY_OVERFLOW_DATA_CAPACITY;

        let overflow_needed = if total_length > first_capacity {
            (total_length - first_capacity).div_ceil(overflow_capacity)
        } else {
            0
        };
        let page_count = 1 + overflow_needed;

        let mut page_ids = vec![0u32; page_count];
        let mut next = EMPTY_PAGE;
        for i in (0..page_count).rev() {
            let page_type = if i == 0 {
                crate::util::PageType::PathwayWeight
            } else {
                crate::util::PageType::Overflow
            };
            let page_id = self.allocate_page(page_type, 0, next)?;
            page_ids[i] = page_id;
            next = page_id;
        }

        let first_data_len = total_length.min(first_capacity);
        let mut first_payload = Vec::with_capacity(Self::PATHWAY_HEADER_SIZE + first_data_len);
        first_payload.extend_from_slice(&Self::PATHWAY_MAGIC.to_le_bytes());
        first_payload.extend_from_slice(&(total_length as u32).to_le_bytes());
        first_payload.extend_from_slice(&data[..first_data_len]);
        write_page_data(&mut self.mmap, page_ids[0], &first_payload)?;

        let mut offset = first_data_len;
        for &page_id in page_ids.iter().skip(1) {
            let end = (offset + overflow_capacity).min(total_length);
            write_page_data(&mut self.mmap, page_id, &data[offset..end])?;
            offset = end;
        }

        Ok(page_ids[0])
    }

    /// Free all pages used by the current on-disk L6 pathway weights.
    #[allow(dead_code)]
    fn free_pathway_pages(&mut self) -> Result<()> {
        let start_page = self.header.layer_roots[LAYER_ROOT_L6];
        if start_page == 0 || start_page >= self.header.page_count {
            return Ok(());
        }

        Self::free_page_chain(&mut self.mmap, &mut self.header, start_page)?;
        self.header.layer_roots[LAYER_ROOT_L6] = 0;
        Ok(())
    }

    /// Write bucket page chains for the btree and return the directory page id.
    fn write_btree_pages(&mut self, page_data: &crate::index::btree::BTreePageData) -> Result<u32> {
        use crate::index::btree::EMPTY_PAGE;

        let bucket_count = page_data.bucket_count as usize;
        if bucket_count == 0 {
            return Ok(0);
        }

        // Allocate primary bucket pages (not required to be contiguous).
        let mut primary_pages = Vec::with_capacity(bucket_count);
        for _ in 0..bucket_count {
            let page_id = self.allocate_page(crate::util::PageType::BTreeLeaf, 0, EMPTY_PAGE)?;
            primary_pages.push(page_id);
        }

        // Write each bucket's chain.
        for (bucket_idx, bucket) in page_data.buckets.iter().enumerate() {
            let primary_page = primary_pages[bucket_idx];
            let mut prev_page = primary_page;

            for (page_idx, page_payload) in bucket.iter().enumerate() {
                let page_id = if page_idx == 0 {
                    primary_page
                } else {
                    self.allocate_page(crate::util::PageType::Overflow, 0, EMPTY_PAGE)?
                };

                // Write page payload.
                write_page_data(&mut self.mmap, page_id, page_payload)?;

                if page_idx > 0 {
                    // Link previous page to this overflow page.
                    let mut prev_header = {
                        let offset = (prev_page as usize) * PAGE_SIZE;
                        let mut bytes = [0u8; 32];
                        bytes.copy_from_slice(&self.mmap[offset..offset + 32]);
                        crate::file::page::PageHeader::from_bytes(&bytes)?
                    };
                    prev_header.next_page = page_id;
                    crate::file::page::write_page_header(&mut self.mmap, prev_page, &prev_header)?;
                    prev_page = page_id;
                } else {
                    // Ensure primary page header has no stale next_page.
                    let mut header = {
                        let offset = (page_id as usize) * PAGE_SIZE;
                        let mut bytes = [0u8; 32];
                        bytes.copy_from_slice(&self.mmap[offset..offset + 32]);
                        crate::file::page::PageHeader::from_bytes(&bytes)?
                    };
                    header.next_page = EMPTY_PAGE;
                    crate::file::page::write_page_header(&mut self.mmap, page_id, &header)?;
                }
            }
        }

        // Allocate and write directory page.
        let directory_page = self.allocate_page(crate::util::PageType::BTreeLeaf, 0, EMPTY_PAGE)?;
        let mut dir_data = Vec::with_capacity(8 + bucket_count * 4);
        dir_data.extend_from_slice(&page_data.bucket_count.to_le_bytes());
        dir_data.extend_from_slice(&page_data.split_pointer.to_le_bytes());
        for &page_id in &primary_pages {
            dir_data.extend_from_slice(&page_id.to_le_bytes());
        }
        write_page_data(&mut self.mmap, directory_page, &dir_data)?;

        Ok(directory_page)
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
        for (&id_hash, &page_ref) in self.btree.iter() {
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
mod tests {
    use super::*;
    use tempfile::TempDir;

    #[test]
    fn test_file_auto_extension() {
        let dir = TempDir::new().unwrap();
        let path = dir.path().join("extend.meh");
        let mut config = MemHopConfig::new(path, 768);
        config.encoder_grpc_addr = None; // unit test does not need real encoder
        let mut db = MemHop::open(config).unwrap();

        // Initial database has 2000 pages; pages 18..1999 are free (1982 pages).
        assert_eq!(db.header.page_count, 2000);

        // Consume all initially free pages.
        for _ in 0..1982 {
            db.allocate_page(
                crate::util::PageType::Context,
                2,
                crate::file::free_list::EMPTY_FREE_LIST,
            )
            .unwrap();
        }

        // The next allocation must trigger an automatic extension.
        let page_id = db
            .allocate_page(
                crate::util::PageType::Context,
                2,
                crate::file::free_list::EMPTY_FREE_LIST,
            )
            .unwrap();
        assert!(page_id >= 2000);
        assert_eq!(db.header.page_count, 2500);

        // Additional allocations from the extended region should succeed.
        for _ in 0..10 {
            db.allocate_page(
                crate::util::PageType::Context,
                2,
                crate::file::free_list::EMPTY_FREE_LIST,
            )
            .unwrap();
        }
    }

    #[test]
    fn test_extend_file_preserves_old_free_list() {
        let dir = TempDir::new().unwrap();
        let path = dir.path().join("extend_old_free.meh");
        let mut config = MemHopConfig::new(path, 768);
        config.encoder_grpc_addr = None; // unit test does not need real encoder
        let mut db = MemHop::open(config).unwrap();

        let old_page_count = db.header.page_count;
        let old_free_list_head = db.header.free_list_head;
        assert_ne!(old_free_list_head, crate::file::free_list::EMPTY_FREE_LIST);

        // Extend the file by a small number of pages.
        let grow_pages = 50;
        db.extend_file(grow_pages).unwrap();

        assert_eq!(db.header.page_count, old_page_count + grow_pages);

        // The last new page is the tail of the new free chain and should
        // still be marked as Free until the whole new chain is consumed.
        let tail_page = old_page_count + grow_pages - 1;
        let free_header = crate::file::page::read_page_header(&db.mmap, tail_page).unwrap();
        assert_eq!(free_header.page_type, crate::util::PageType::Free as u16);

        // All new pages plus at least one page from the old free list must be
        // reachable without triggering another auto-extension.
        for i in 0..grow_pages + 1 {
            db.allocate_page(
                crate::util::PageType::Context,
                2,
                crate::file::free_list::EMPTY_FREE_LIST,
            )
            .unwrap_or_else(|_| panic!("allocation {} should succeed (old free list lost?)", i));
        }
    }
}
