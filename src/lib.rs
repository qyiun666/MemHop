//! MemHop v0.48.0 - Agent-oriented memory database inspired by human brain cognitive architecture
//!
//! MemHop is a specialized memory database designed for AI Agents, implementing
//! a six-layer cognitive architecture (L0-L5) with custom .meh binary file format.
//!
//! # Features
//! - Zero-copy mmap retrieval
//! - Hybrid search (BM25 + Vector similarity + Entity matching)
//! - Hypergraph-based associative memory
//! - Automatic memory consolidation (dream pipeline)
//!
//! # Example
//! ```no_run
//! use memhop::{MemHop, MemHopConfig};
//! use std::path::PathBuf;
//!
//! let config = MemHopConfig::new(PathBuf::from("test.meh"), 768);
//! let db = MemHop::open(config).unwrap();
//! ```

pub mod config;
pub mod dream;
pub mod encoder;
pub mod ffi;
pub mod file;
pub mod index;
pub mod l3;
pub mod migrate;
pub mod organize;
pub mod query;
pub mod session;
pub mod slot;
pub mod util;

pub use config::{LlmConfig, MemHopConfig};
pub use util::{Layer, SourceMeta, SourceRef, SourceType};

// Re-export public types
pub use dream::llm::{CrystalDef, CrystalStep, LlmProvider, MemorySummary, Pattern};
pub use dream::openai_compatible::OpenAICompatibleLlmProvider;
pub use dream::prune::DreamReport;
pub use migrate::{migrate, verify_migration, MigrateError, MigrateReport};
pub use organize::extract_keywords;
pub use query::batch::{BatchReport, EncodedItem, StoreBatch, StoreItem};

// Re-export new API types (API_NEW.md) - These are the recommended public interfaces
pub use query::types::{
    ActionItem,
    ActionType,
    Archive,
    ArchiveListResult,
    ArchivePageQuery,
    ArchiveRef,
    ContextResult,
    CrystalListQuery,
    CrystalListResult,
    CrystalSummary,
    EdgeListQuery,
    EdgeListResult,
    // List Queries (Interfaces 6-12)
    EngramListQuery,
    EngramListResult,
    EngramResult,
    ImportData,
    ImportError,
    ImportMode,
    // Import Memory (Interface 19)
    ImportRequest,
    ImportResult,
    ImportStatus,
    KnowledgeDetail,
    KnowledgeImportItem,
    KnowledgeListQuery,
    KnowledgeListResult,
    KnowledgeSummary,
    NodeListQuery,
    NodeListResult,
    ProfileResult,
    // Search Memory (Interface 2)
    RequestSource,
    SearchQuery,
    SearchResult,
    L3Preview,
    // L3 Hypergraph Engine
    TargetLayer,
    TopicDetail,
    TopicImportItem,
    TopicListQuery,
    TopicListResult,
    TopicSummary,
    // Update Titles (Interfaces 13-16)
    UpdateProfileRequest,
    // Update Memory (Interface 3)
    UpdateRequest,
    UpdateResult,
    UpdateStatus,
};

use memmap2::{Mmap, MmapMut};
use std::collections::HashSet;
use std::fs::{File, OpenOptions};
use std::io;
use thiserror::Error;

use crate::file::free_list::init_free_list;
use crate::file::header::{read_headers, select_valid_header, FileHeader};
use crate::file::journal::replay_journal;
use crate::file::page::{read_page_data, write_page_data};
use crate::index::btree::BTreeIndex as BTree;
use crate::index::sparse::SparseIndex;
use crate::query::search::L1ReverseIndex;
use crate::session::SessionManager;
use crate::util::PAGE_SIZE;

/// MemHop error types
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

    #[error("Vector dimension mismatch: expected {expected}, got {actual}")]
    VectorDimensionMismatch { expected: usize, actual: usize },

    #[error("Configuration error: {0}")]
    ConfigError(String),

    #[error("Encoder error: {0}")]
    EncoderError(String),
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
    mmap: MmapMut,
    #[allow(dead_code)]
    file: File, // Kept for mmap lifecycle (file handle management)
    header: FileHeader,
    config: MemHopConfig,
    btree: BTree,
    sparse_index: SparseIndex,
    session_manager: SessionManager,
    encoder: Option<Box<dyn crate::encoder::Encoder + Send + Sync>>,
    l1_reverse_index: L1ReverseIndex,
    adjacency_cache: crate::l3::AdjacencyCache,
    closed: bool, // Prevent Drop from re-checkpointing after close()
}

impl MemHop {
    /// Open or create a MemHop database
    pub fn open(config: MemHopConfig) -> Result<Self> {
        let db_path = &config.db_path;

        // Check if database exists before creating file
        let db_exists = db_path.exists();

        // 1. Open or create .meh file
        let file = if db_exists {
            let file = OpenOptions::new().read(true).write(true).open(db_path)?;
            // Verify file has minimum size (at least 500 pages)
            let metadata = file.metadata()?;
            let min_size = 500 * PAGE_SIZE as u64;
            if metadata.len() < min_size {
                eprintln!(
                    "Warning: Database file is too small ({} bytes), extending to {} bytes",
                    metadata.len(),
                    min_size
                );
                file.set_len(min_size)?;
            }
            file
        } else {
            // Create new file with initial size (500 pages: 2 headers + 1 free list + 14 layer roots + 483 data pages)
            // This allows storing ~240 documents (each needs engram + vector page)
            let file = OpenOptions::new()
                .read(true)
                .write(true)
                .create(true)
                .truncate(true)
                .open(db_path)?;
            file.set_len(500 * 4096)?; // Initial 500 pages (~2MB)
            file
        };

        // 2. Initialize mmap
        let mut mmap = unsafe { MmapMut::map_mut(&file)? };

        // 3. Read/validate Header (A/B dual header recovery)
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
            // Pages 18-499: available data pages
            for page_id in (18..500).rev() {
                use crate::file::free_list::free_page;
                free_page(&mut mmap, &mut header, page_id)?;
            }

            header.page_count = 500;

            // Write headers directly to mmap
            let header_bytes = header.to_bytes();
            mmap[..PAGE_SIZE].copy_from_slice(&header_bytes); // Header A
            mmap[PAGE_SIZE..PAGE_SIZE * 2].copy_from_slice(&header_bytes); // Header B
            mmap.flush()?;

            header
        };

        // 4. Replay Journal
        let mmap_readonly = unsafe { Mmap::map(&file)? };
        let journal_entries = replay_journal(&mmap_readonly, &header)?;

        if !journal_entries.is_empty() {
            // 按 commit_id 排序,顺序应用
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

        // 5. Load B-tree and Sparse Index from disk
        let btree = Self::load_btree(&mmap_readonly, &header);
        let sparse_index = Self::load_sparse_index(&mmap_readonly, &header);

        // 5a. Load L1 reverse index from persistence if available, otherwise
        // rebuild it from the loaded B-tree.
        let l1_reverse_index = if header.layer_roots[12] != 0 {
            match Self::read_l1_reverse_pages(&mmap_readonly, &header, header.layer_roots[12]) {
                Ok(data) => match L1ReverseIndex::deserialize(&data) {
                    Ok(idx) => idx,
                    Err(e) => {
                        eprintln!(
                            "Warning: Failed to load L1 reverse index from disk: {}. Rebuilding.",
                            e
                        );
                        L1ReverseIndex::build(&mmap_readonly, &btree)?
                    }
                },
                Err(e) => {
                    eprintln!(
                        "Warning: Failed to read L1 reverse index pages: {}. Rebuilding.",
                        e
                    );
                    L1ReverseIndex::build(&mmap_readonly, &btree)?
                }
            }
        } else {
            L1ReverseIndex::build(&mmap_readonly, &btree)?
        };

        // 6. Initialize SessionManager
        let session_manager = SessionManager::new();

        // 8. Initialize encoder from config (gRPC over TCP)
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

        // 9. Return MemHop instance
        Ok(MemHop {
            mmap,
            file,
            header,
            config,
            btree,
            sparse_index,
            session_manager,
            encoder,
            l1_reverse_index,
            adjacency_cache: crate::l3::AdjacencyCache::new(),
            closed: false,
        })
    }

    /// Load the B-tree index from disk.
    ///
    /// Reads the Linear Hash bucket layout starting at the page recorded in
    /// `header.reserved[8..12]`. Falls back to the legacy single-page format
    /// stored at `header.layer_roots[0]` if the new metadata is missing or
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
                            eprintln!(
                                "Warning: Failed to load Linear Hash B-tree from disk: {}. Trying legacy format.",
                                e
                            );
                        }
                    }
                }
            }
        }

        // Fall back to legacy single-page format.
        let btree_page = header.layer_roots[0];
        if btree_page != 0 && btree_page < header.page_count {
            match read_page_data(mmap, btree_page) {
                Ok(data) => match BTree::deserialize(data) {
                    Ok(index) => return index,
                    Err(e) => {
                        eprintln!(
                            "Warning: Failed to load legacy B-tree from disk: {}. Using empty index.",
                            e
                        );
                    }
                },
                Err(e) => {
                    eprintln!(
                        "Warning: Failed to read legacy B-tree page: {}. Using empty index.",
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
            let btree_page = self.header.layer_roots[0];
            if btree_page != 0 && btree_page < self.header.page_count {
                crate::file::free_list::free_page(&mut self.mmap, &mut self.header, btree_page)?;
            }
        }

        // Clear btree metadata in header.
        self.header.reserved[0..12].fill(0);
        self.header.layer_roots[0] = 0;

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
        let directory_page = self.header.layer_roots[1];
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
        self.header.layer_roots[1] = 0;
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
        let directory_page = header.layer_roots[1];
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
                            eprintln!(
                                "Warning: Failed to load multi-page Sparse Index: {}. Trying legacy format.",
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
                eprintln!(
                    "Warning: Failed to load Sparse Index from disk: {}. Using empty index.",
                    e
                );
                SparseIndex::new()
            }
        }
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
        let start_page = self.header.layer_roots[12];
        if start_page == 0 || start_page >= self.header.page_count {
            return Ok(());
        }

        Self::free_page_chain(&mut self.mmap, &mut self.header, start_page)?;
        self.header.layer_roots[12] = 0;
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

    /// Extend the database file by `grow_pages` pages and remap mmap.
    ///
    /// New pages are initialized as free-list pages (`PageType::Free`) and linked
    /// into the existing free list. After extending, A/B dual headers are rewritten
    /// via the checkpoint mechanism for crash safety.
    pub fn extend_file(&mut self, grow_pages: u32) -> Result<()> {
        let old_page_count = self.header.page_count;
        let new_page_count = old_page_count + grow_pages;
        let new_size = (new_page_count as usize) * PAGE_SIZE;
        let old_free_list_head = self.header.free_list_head;

        // 1. Extend the underlying file.
        self.file.set_len(new_size as u64)?;

        // 2. Re-map the file into memory.
        self.mmap = unsafe { MmapMut::map_mut(&self.file)? };

        // 3. Link the new pages into the free list.
        // The free list stores the next free page id in the first 4 bytes of
        // each free page (matching `free_list::free_page`).
        // We link the new chain *in front of* the old free list so previously
        // free pages remain reachable. The last new page points to the old head.
        let mut next_free = old_free_list_head;
        let free_type = crate::util::PageType::Free.to_u16().to_le_bytes();
        for page_id in (old_page_count..new_page_count).rev() {
            let page_offset = (page_id as usize) * PAGE_SIZE;
            self.mmap[page_offset..page_offset + 4].copy_from_slice(&next_free.to_le_bytes());
            self.mmap[page_offset + 4..page_offset + 6].copy_from_slice(&free_type);
            next_free = page_id;
        }

        // 4. Update in-memory header so the checkpoint can allocate from the
        // newly created free pages. Save old values so we can roll back if the
        // checkpoint fails before the headers are persisted.
        self.header.free_list_head = next_free;
        self.header.page_count = new_page_count;

        // 5. Persist the updated headers (A/B dual header checkpoint).
        if let Err(e) = self.checkpoint() {
            self.header.free_list_head = old_free_list_head;
            self.header.page_count = old_page_count;
            return Err(e);
        }

        Ok(())
    }

    /// Allocate a new page, automatically extending the file if the free list is exhausted.
    ///
    /// This is a safe wrapper around `crate::file::page::allocate_page` that catches
    /// `MemHopError::FileFull` and grows the file by 500 pages before retrying.
    pub fn allocate_page(
        &mut self,
        page_type: crate::util::PageType,
        layer_id: u16,
        next_page_id: u32,
    ) -> Result<u32> {
        use crate::file::page::allocate_page as alloc_page;

        match alloc_page(
            &mut self.mmap,
            &mut self.header,
            page_type,
            layer_id,
            next_page_id,
        ) {
            Ok(page_id) => Ok(page_id),
            Err(MemHopError::FileFull) => {
                self.extend_file(500)?;
                alloc_page(
                    &mut self.mmap,
                    &mut self.header,
                    page_type,
                    layer_id,
                    next_page_id,
                )
            }
            Err(e) => Err(e),
        }
    }

    // Note: Old interfaces (store, recall, recall_cascade, recall_more) have been removed.
    // Use the new API interfaces: search_memory(), update_memory(), etc.

    /// Search memory using topic-centric retrieval model
    ///
    /// # Arguments
    /// * `query` - Search query with dialogue, filters, and optional LLM enhancement
    ///
    /// # Returns
    /// SearchResult containing profile, topics, knowledge, archives, etc.
    pub fn search_memory(&mut self, query: SearchQuery) -> Result<SearchResult> {
        use crate::query::search::search_memory as search_impl;

        search_impl(
            &mut self.mmap,
            &mut self.header,
            query,
            &mut self.btree,
            &mut self.sparse_index,
            self.config.vector_dim,
            self.encoder.as_deref(),
            &self.l1_reverse_index,
        )
    }

    /// Update memory with multi-level updates
    ///
    /// # Arguments
    /// * `request` - Update request with dialogue, titles, and action chain
    ///
    /// # Returns
    /// UpdateResult with IDs of created/updated items
    pub fn update_memory(&mut self, request: UpdateRequest) -> Result<UpdateResult> {
        use crate::query::update::update_memory as update_impl;

        update_impl(
            &mut self.mmap,
            &mut self.header,
            request,
            &mut self.btree,
            &mut self.sparse_index,
            self.config.vector_dim,
        )
    }

    // ========================================================================
    // Query Interfaces
    // ========================================================================

    /// Get profile
    pub fn get_profile(&self) -> Result<Option<ProfileResult>> {
        use crate::query::list::get_profile as impl_fn;
        impl_fn(&self.mmap, &self.btree)
    }

    /// Get single engram by ID
    pub fn get_engram(&self, id: &str) -> Result<Option<EngramResult>> {
        use crate::query::list::get_engram as impl_fn;
        impl_fn(&self.mmap, &self.btree, id)
    }

    /// List engrams with pagination and filtering
    pub fn list_engrams(&self, query: EngramListQuery) -> Result<EngramListResult> {
        use crate::query::list::list_engrams as impl_fn;
        impl_fn(&self.mmap, &self.header, &self.btree, query)
    }

    /// Get single topic by ID
    pub fn get_topic(&self, id: &str) -> Result<Option<TopicDetail>> {
        use crate::query::list::get_topic as impl_fn;
        impl_fn(&self.mmap, &self.btree, id)
    }

    /// List topics with pagination and filtering
    pub fn list_topics(&self, query: TopicListQuery) -> Result<TopicListResult> {
        use crate::query::list::list_topics as impl_fn;
        impl_fn(&self.mmap, &self.header, &self.btree, query)
    }

    /// List archives by topic ID
    pub fn list_archives_by_topic(
        &self,
        topic_id: &str,
        query: ArchivePageQuery,
    ) -> Result<ArchiveListResult> {
        use crate::query::list::list_archives_by_topic as impl_fn;
        impl_fn(&self.mmap, &self.header, &self.btree, topic_id, query)
    }

    /// List archives by node IDs
    pub fn list_archives_by_nodes(
        &self,
        node_ids: &[String],
        query: ArchivePageQuery,
    ) -> Result<ArchiveListResult> {
        use crate::query::list::list_archives_by_nodes as impl_fn;
        impl_fn(&self.mmap, &self.header, &self.btree, node_ids, query)
    }

    /// List all archives
    pub fn list_all_archives(&self, query: ArchivePageQuery) -> Result<ArchiveListResult> {
        use crate::query::list::list_all_archives as impl_fn;
        impl_fn(&self.mmap, &self.header, &self.btree, query)
    }

    /// List crystals with pagination and filtering
    pub fn list_crystals(&self, query: CrystalListQuery) -> Result<CrystalListResult> {
        use crate::query::list::list_crystals as impl_fn;
        impl_fn(&self.mmap, &self.header, &self.btree, query)
    }

    /// Get single knowledge (L3 hypergraph) by ID
    ///
    /// Uses l3::store engine to read the hypergraph and aggregate node content
    /// into the KnowledgeDetail structure.
    pub fn get_knowledge(&self, id: &str) -> Result<Option<KnowledgeDetail>> {
        let data: &[u8] = &self.mmap[..];
        let id_hash = crate::query::common::parse_id_to_hash(id);

        // Read HypergraphSlot from BTree
        let slot = match self.btree.search(id_hash) {
            Some(page_ref) => {
                if let Some(slot_data) = crate::query::slot_io::get_slot_data(data, page_ref) {
                    match crate::slot::hypergraph::HypergraphSlot::deserialize_slot(slot_data) {
                        Ok(s) => s,
                        Err(_) => return Ok(None),
                    }
                } else {
                    return Err(MemHopError::PageNotFound(
                        crate::query::slot_io::decode_page_id(page_ref),
                    ));
                }
            }
            None => return Ok(None),
        };

        // Aggregate node content using l3::store
        let source_ref = match &slot.source {
            crate::slot::hypergraph::HypergraphSource::Path(p) => Some(p.clone()),
            crate::slot::hypergraph::HypergraphSource::Url(u) => Some(u.clone()),
            _ => None,
        };

        let mut text = String::new();
        let mut summary: Option<String> = None;
        let mut keywords: Vec<String> = Vec::new();
        let edge_ptrs: Vec<String> = Vec::new();
        let mut avg_importance = 0.5f32;

        let node_query = crate::query::types::NodeListQuery {
            page: 1,
            page_size: 1000,
            node_type: None,
            keyword: None,
            min_importance: None,
        };
        if let Ok(nodes) =
            crate::l3::store::list_nodes_by_graph(&self.mmap, &self.btree, id_hash, &node_query)
        {
            let count = nodes.total as f32;
            if count > 0.0 {
                let imp_sum: f32 = nodes.items.iter().map(|n| n.importance).sum();
                avg_importance = imp_sum / count;
            }
            for node in &nodes.items {
                if !node.content.is_empty() {
                    text.push_str(&node.content);
                    text.push('\n');
                }
                if summary.is_none() && !node.title.is_empty() {
                    summary = Some(node.title.clone());
                }
                keywords.extend(node.keywords.iter().cloned());
            }
        }

        keywords.sort();
        keywords.dedup();

        Ok(Some(crate::query::types::KnowledgeDetail {
            id: crate::query::common::format_hash(slot.id_hash),
            title: slot.name,
            domain: format!("{:?}", slot.source.kind()),
            knowledge_type: "Generic".to_string(),
            text: text.trim_end().to_string(),
            summary,
            keywords,
            edge_ptrs,
            archive_refs: vec![],
            source_ref,
            importance: avg_importance,
            confidence: 1.0,
            created_at: slot.created_at,
            updated_at: slot.updated_at,
        }))
    }

    /// List knowledge (L3 hypergraphs) with pagination and filtering
    pub fn list_knowledge(&self, query: KnowledgeListQuery) -> Result<KnowledgeListResult> {
        use crate::query::list::list_knowledge as impl_fn;
        impl_fn(&self.mmap, &self.header, &self.btree, query)
    }

    // ========================================================================
    // Graph query and deletion interfaces
    // ========================================================================

    /// Parse a string into a `GraphEdgeKind`.
    fn parse_graph_edge_kind(s: &str) -> Option<crate::slot::hypergraph::GraphEdgeKind> {
        use crate::slot::hypergraph::GraphEdgeKind;
        match s {
            "Related" | "related" => Some(GraphEdgeKind::Related),
            "Causal" | "causal" => Some(GraphEdgeKind::Causal),
            "PartOf" | "part_of" => Some(GraphEdgeKind::PartOf),
            "Sequence" | "sequence" => Some(GraphEdgeKind::Sequence),
            "Dependency" | "dependency" => Some(GraphEdgeKind::Dependency),
            "Custom" | "custom" => Some(GraphEdgeKind::Custom),
            _ => None,
        }
    }

    /// Query a subgraph reachable from `start_node` within `max_depth` hops.
    pub fn graph_query(
        &mut self,
        graph_id: &str,
        start_node: &str,
        max_depth: usize,
        edge_kinds: Option<Vec<String>>,
    ) -> Result<crate::query::types::Subgraph> {
        let (subgraph, _hops) = self.graph_query_internal(graph_id, start_node, max_depth, edge_kinds)?;
        Ok(subgraph)
    }

    /// Internal graph query that returns both the subgraph and the traversal hops.
    pub(crate) fn graph_query_internal(
        &mut self,
        graph_id: &str,
        start_node: &str,
        max_depth: usize,
        edge_kinds: Option<Vec<String>>,
    ) -> Result<(crate::query::types::Subgraph, Vec<crate::query::types::TraversalHop>)> {
        use crate::query::types::Subgraph;
        use crate::slot::hypergraph::HypergraphNode;

        let graph_hash = crate::query::common::parse_id_to_hash(graph_id);
        let start_hash = crate::query::common::parse_id_to_hash(start_node);

        let kinds = edge_kinds.and_then(|vec| {
            let parsed: Vec<_> = vec
                .iter()
                .filter_map(|s| Self::parse_graph_edge_kind(s))
                .collect();
            // Treat empty array as None (no filtering)
            if parsed.is_empty() {
                None
            } else {
                Some(parsed)
            }
        });

        let data: &[u8] = &self.mmap[..];
        let hops = crate::l3::store::bfs_traversal_cached(
            data,
            &self.btree,
            graph_hash,
            start_hash,
            max_depth,
            kinds.as_deref(),
            &mut self.adjacency_cache,
        )?;

        let mut node_hashes = HashSet::new();
        let mut edge_ids = HashSet::new();
        let mut edges = Vec::new();

        node_hashes.insert(start_hash);
        for hop in &hops {
            node_hashes.insert(hop.from_node);
            node_hashes.insert(hop.to_node);
            if edge_ids.insert(hop.edge.id_hash) {
                edges.push(hop.edge.clone());
            }
        }

        let mut nodes: Vec<HypergraphNode> = Vec::new();
        for &node_hash in &node_hashes {
            if let Some(page_ref) = self.btree.search(node_hash) {
                if let Some(slot_data) = crate::query::slot_io::get_slot_data(data, page_ref) {
                    if let Ok(node) = HypergraphNode::deserialize(slot_data) {
                        if node.graph_id == graph_hash {
                            nodes.push(node);
                        }
                    }
                }
            }
        }

        Ok((Subgraph { nodes, edges }, hops))
    }

    /// Delete an L2 topic and its associated L1 nodes and L4 archives.
    pub fn delete_topic(&mut self, topic_id: u64) -> Result<()> {
        let page_ref = match self.btree.search(topic_id) {
            Some(pr) => pr,
            None => return Ok(()),
        };

        let ctx = {
            let data: &[u8] = &self.mmap[..];
            let slot_data = crate::query::slot_io::get_slot_data(data, page_ref).ok_or(
                MemHopError::PageNotFound(crate::query::slot_io::decode_page_id(page_ref)),
            )?;
            crate::slot::context::ContextSlot::deserialize_slot(slot_data)?
        };

        // Collect associated L1 ContextNode records using L1ReverseIndex (O(1) lookup).
        let l1_nodes: Vec<(u64, u64)> = {
            let data: &[u8] = &self.mmap[..];
            self.l1_reverse_index
                .find_associated(&std::iter::once(topic_id).collect())
                .into_iter()
                .filter(|(_, page_ref)| {
                    // Verify the page is still a ContextNode (defensive check)
                    let page_id = crate::query::slot_io::decode_page_id(*page_ref);
                    if page_id >= self.header.page_count {
                        return false;
                    }
                    if let Ok(page_hdr) = crate::file::page::read_page_header(data, page_id) {
                        page_hdr.page_type == crate::util::PageType::ContextNode as u16
                    } else {
                        false
                    }
                })
                .collect()
        };

        // Free L1 nodes and update the reverse index.
        for (node_hash, page_ref) in l1_nodes {
            self.btree.delete(node_hash);
            let page_id = crate::query::slot_io::decode_page_id(page_ref);
            crate::file::free_list::free_page(&mut self.mmap, &mut self.header, page_id)?;
            self.l1_reverse_index.remove_node(node_hash);
        }

        // Free associated L4 archives.
        for &arc_hash in &ctx.archive_refs {
            if let Some(page_ref) = self.btree.delete(arc_hash) {
                let page_id = crate::query::slot_io::decode_page_id(page_ref);
                crate::file::free_list::free_page(&mut self.mmap, &mut self.header, page_id)?;
            }
        }

        // Free centroid vector page if present.
        if ctx.centroid_page_ref != 0 {
            let page_id = crate::query::slot_io::decode_page_id(ctx.centroid_page_ref);
            crate::file::free_list::free_page(&mut self.mmap, &mut self.header, page_id)?;
        }

        // Remove the ContextSlot itself.
        self.btree.delete(topic_id);
        let page_id = crate::query::slot_io::decode_page_id(page_ref);
        crate::file::free_list::free_page(&mut self.mmap, &mut self.header, page_id)?;

        self.sparse_index.remove_document(topic_id);
        self.l1_reverse_index.remove_context(topic_id);

        Ok(())
    }

    /// Delete an L3 hypergraph and clean up its references from L2 contexts.
    pub fn delete_graph(&mut self, graph_id: u64) -> Result<()> {
        let l3_id_str = crate::query::common::format_hash(graph_id);

        // Collect L2 ContextSlots that reference this graph before deleting it.
        let l2_refs = crate::l3::store::collect_l2_refs(&self.mmap, &self.btree, graph_id)?;

        crate::l3::store::delete_graph(
            &mut self.mmap,
            &mut self.header,
            &mut self.btree,
            &l3_id_str,
        )?;

        // Remove the graph reference from each L2 context.
        for (page_id, _id_hash) in l2_refs {
            crate::l3::store::remove_l3_ref_from_context(&mut self.mmap, page_id, graph_id)?;
        }

        // Invalidate adjacency cache for this graph
        self.adjacency_cache.invalidate(graph_id);

        Ok(())
    }

    /// Delete an L5 action chain and all associated action steps.
    pub fn delete_action_chain(&mut self, chain_id: u64) -> Result<()> {
        let chain_page_ref = match self.btree.search(chain_id) {
            Some(pr) => pr,
            None => return Ok(()),
        };

        let chain_page_id = crate::query::slot_io::decode_page_id(chain_page_ref);
        crate::file::free_list::free_page(&mut self.mmap, &mut self.header, chain_page_id)?;
        self.btree.delete(chain_id);

        // Collect associated ActionStep records.
        let mut steps: Vec<(u64, u64)> = Vec::new();
        {
            let data: &[u8] = &self.mmap[..];
            for (&id_hash, &page_ref) in self.btree.iter() {
                if crate::l3::store::page_type_of(data, page_ref)
                    != Some(crate::util::PageType::ActionStep as u16)
                {
                    continue;
                }
                if let Some(slot_data) = crate::query::slot_io::get_slot_data(data, page_ref) {
                    if let Ok(step) = crate::slot::action_chain::ActionStep::deserialize(slot_data) {
                        if step.chain_id == chain_id {
                            steps.push((id_hash, page_ref));
                        }
                    }
                }
            }
        }

        // Free each action step.
        for (step_hash, page_ref) in steps {
            self.btree.delete(step_hash);
            let page_id = crate::query::slot_io::decode_page_id(page_ref);
            crate::file::free_list::free_page(&mut self.mmap, &mut self.header, page_id)?;
        }

        Ok(())
    }

    // ========================================================================
    // Update Title/Profile Interfaces
    // ========================================================================

    /// Update profile (merge strategy - only update Some fields)
    pub fn update_profile(&mut self, request: UpdateProfileRequest) -> Result<ProfileResult> {
        use crate::query::update_title::update_profile as impl_fn;
        impl_fn(&mut self.mmap, &mut self.header, &mut self.btree, request)
    }

    /// Update topic title (with sparse index synchronization)
    pub fn update_topic_title(&mut self, id: &str, new_title: String) -> Result<TopicSummary> {
        use crate::query::update_title::update_topic_title as impl_fn;
        impl_fn(
            &mut self.mmap,
            &mut self.header,
            &self.btree,
            &mut self.sparse_index,
            id,
            new_title,
        )
    }

    /// Update crystal title
    pub fn update_crystal_title(&mut self, id: &str, new_title: String) -> Result<CrystalSummary> {
        use crate::query::update_title::update_crystal_title as impl_fn;
        impl_fn(&mut self.mmap, &self.btree, id, new_title)
    }

    /// Update L3 knowledge title (Interface 15)
    pub fn update_knowledge_title(
        &mut self,
        id: &str,
        new_title: String,
    ) -> Result<KnowledgeSummary> {
        use crate::query::update_title::update_knowledge_title as impl_fn;
        impl_fn(&mut self.mmap, &self.btree, id, new_title)
    }

    // ========================================================================
    // Advanced Function Interfaces
    // ========================================================================

    /// Merge multiple topics into a primary topic
    pub fn merge_topics(
        &mut self,
        primary_id: &str,
        secondary_ids: Vec<String>,
    ) -> Result<TopicDetail> {
        use crate::query::merge::merge_topics as impl_fn;
        impl_fn(
            &mut self.mmap,
            &mut self.header,
            &mut self.btree,
            &mut self.sparse_index,
            primary_id,
            secondary_ids,
        )
    }

    /// Import memory into specified layer
    pub fn import_memory(&mut self, request: ImportRequest) -> Result<ImportResult> {
        use crate::query::import::import_memory as impl_fn;
        impl_fn(
            &mut self.mmap,
            &mut self.header,
            &mut self.btree,
            &mut self.sparse_index,
            request,
        )
    }

    /// Build hypergraph edges from file path
    ///
    /// Reads files from the given path, extracts keywords, finds related existing
    /// knowledge nodes via BM25 search, and creates KnowledgeEdge connections between them.
    ///
    /// # Arguments
    /// * `path` - Path to file or directory to analyze
    ///
    /// # Returns
    /// * `Ok(ImportResult)` - Result with created edge IDs
    /// * `Err(MemHopError)` - IO, configuration, or import error
    pub fn build_l3_hypergraph_from_path(
        &mut self,
        path: &std::path::Path,
    ) -> Result<ImportResult> {
        use crate::query::import::build_l3_hypergraph_from_path as impl_fn;
        let result = impl_fn(
            &mut self.mmap,
            &mut self.header,
            &mut self.btree,
            &mut self.sparse_index,
            path,
        )?;
        // Invalidate all adjacency cache since import may modify any graph
        self.adjacency_cache.invalidate_all();
        Ok(result)
    }

    /// Activate a Topic for session management. If capacity is exceeded,
    /// the LRU topic is evicted and optionally processed through a lightweight
    /// dream consolidation before removal from the active set.
    ///
    /// # Arguments
    /// * `topic_id` - Topic ID string (will be converted to hash)
    /// * `ttl_ms` - Optional custom TTL in milliseconds, uses default if None
    pub fn activate_topic(&mut self, topic_id: &str, ttl_ms: Option<i64>) {
        use crate::util::hash::hash_id;
        let id_hash = hash_id(topic_id);
        let evicted = self.session_manager.activate_topic(id_hash, ttl_ms);

        if let Some(evicted_id) = evicted {
            if self.config.auto_dream_on_evict {
                if let Err(e) = self.dream_single_topic(evicted_id) {
                    eprintln!("[memhop] Warning: dream_single_topic failed for evicted topic: {}", e);
                }
            }
        }
    }

    /// Lightweight dream: consolidate a single evicted topic.
    ///
    /// Runs L3 distillation + L2 compression + L1 rebuild for the given topic only.
    /// Skips global stages (L1 decay, L0 profile, habit distillation, L5 crystallization)
    /// to keep latency low. Uses `self.config.llm` for LLM configuration.
    fn dream_single_topic(&mut self, topic_id: u64) -> Result<DreamReport> {
        use crate::dream::dream_pipeline;
        use crate::dream::openai_compatible::OpenAICompatibleLlmProvider;
        use std::collections::HashSet;

        let llm_provider = OpenAICompatibleLlmProvider::new(self.config.llm.clone());
        let session_topics: HashSet<u64> = [topic_id].into_iter().collect();

        let report = dream_pipeline(
            &mut self.mmap,
            &mut self.header,
            &mut self.btree,
            &mut self.sparse_index,
            &llm_provider,
            session_topics,
        )?;
        self.l1_reverse_index = L1ReverseIndex::build(&self.mmap, &self.btree)?;
        self.adjacency_cache.invalidate_all();
        Ok(report)
    }

    /// Deactivate the specified Topic
    ///
    /// # Arguments
    /// * `topic_id` - Topic ID string to deactivate
    pub fn deactivate_topic(&mut self, topic_id: &str) {
        use crate::util::hash::hash_id;
        let id_hash = hash_id(topic_id);
        self.session_manager.deactivate_topic(id_hash);
    }

    /// Get all currently active Topic IDs in hex string format
    ///
    /// # Returns
    /// Vector of active topic IDs as hex strings
    pub fn get_active_topic_ids(&self) -> Vec<String> {
        self.session_manager
            .get_active_topic_ids()
            .iter()
            .map(|id| format!("{:016x}", id))
            .collect()
    }

    /// Adjust the activation TTL of a Topic
    ///
    /// # Arguments
    /// * `topic_id` - Topic ID string
    /// * `delta` - Adjustment factor, TTL change = delta × 600,000 ms
    pub fn adjust_activation(&mut self, topic_id: &str, delta: f32) {
        use crate::util::hash::hash_id;
        let id_hash = hash_id(topic_id);
        self.session_manager.adjust_activation(id_hash, delta);
    }

    /// Run dream consolidation pipeline
    ///
    /// Executes memory consolidation on all currently active contexts:
    /// 1. L2 depth demotion (主→次→次次→remove)
    /// 2. L1 rebuild based on updated L2
    /// 3. L0 profile regeneration from L1
    /// 4. L5 crystallization from all ActionChainSlots
    ///
    /// # Arguments
    /// * `llm` - LLM configuration (api_url, api_key, model, temperature, timeout)
    pub fn dream(&mut self, llm: LlmConfig) -> Result<DreamReport> {
        use crate::dream::dream_pipeline;
        use crate::dream::openai_compatible::OpenAICompatibleLlmProvider;
        use std::collections::HashSet;

        // Create LLM provider from passed configuration
        let llm_provider = OpenAICompatibleLlmProvider::new(llm);

        let session_topics: HashSet<u64> = self
            .session_manager
            .get_active_topic_ids()
            .into_iter()
            .collect();

        let report = dream_pipeline(
            &mut self.mmap,
            &mut self.header,
            &mut self.btree,
            &mut self.sparse_index,
            &llm_provider,
            session_topics,
        )?;
        self.l1_reverse_index = L1ReverseIndex::build(&self.mmap, &self.btree)?;
        // Invalidate all adjacency cache since L3 distillation may modify any graph
        self.adjacency_cache.invalidate_all();
        Ok(report)
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
    pub fn set_encoder<E: crate::encoder::Encoder + Send + Sync + 'static>(&mut self, encoder: E) {
        self.encoder = Some(Box::new(encoder));
    }

    /// Batch store multiple documents using the five-phase pipeline
    ///
    /// This method requires an encoder to be set via `set_encoder()` before calling.
    /// If no encoder is set, it will use a MockEncoder with the configured vector dimension.
    ///
    /// # Arguments
    /// * `batch` - Batch of items to store
    ///
    /// # Returns
    /// BatchReport with statistics about the operation
    ///
    /// # Errors
    /// Returns error if encoder is not available or batch processing fails
    pub fn batch_store(
        &mut self,
        batch: crate::query::batch::StoreBatch,
    ) -> Result<crate::query::batch::BatchReport> {
        use crate::query::batch::batch_store;

        let report = batch_store(
            &mut self.mmap,
            &mut self.header,
            batch,
            &mut self.btree,
            &mut self.sparse_index,
            self.config.vector_dim,
            self.encoder.as_deref().ok_or_else(|| {
                MemHopError::EncoderError("No encoder configured for batch_store".to_string())
            })?,
        )?;
        self.l1_reverse_index = L1ReverseIndex::build(&self.mmap, &self.btree)?;
        Ok(report)
    }

    /// Checkpoint: save indices to disk and update header
    pub fn checkpoint(&mut self) -> Result<()> {
        // Save old page references so we can free them AFTER writing new pages.
        let old_btree_directory = u32::from_le_bytes([
            self.header.reserved[8],
            self.header.reserved[9],
            self.header.reserved[10],
            self.header.reserved[11],
        ]);
        let old_btree_bucket_count = u32::from_le_bytes([
            self.header.reserved[0],
            self.header.reserved[1],
            self.header.reserved[2],
            self.header.reserved[3],
        ]);
        let old_sparse_directory = self.header.layer_roots[1];
        let old_l1_root = self.header.layer_roots[12];
        let old_btree_root = self.header.layer_roots[0];

        // Serialize and save B-tree using multi-page Linear Hash layout.
        let btree_pages = self
            .btree
            .serialize_to_pages()
            .map_err(MemHopError::Serialization)?;
        let directory_page = self.write_btree_pages(&btree_pages)?;
        self.header.reserved[0..4].copy_from_slice(&btree_pages.bucket_count.to_le_bytes());
        self.header.reserved[4..8].copy_from_slice(&btree_pages.split_pointer.to_le_bytes());
        self.header.reserved[8..12].copy_from_slice(&directory_page.to_le_bytes());
        self.header.layer_roots[0] = directory_page;

        // Serialize and save Sparse Index using multi-page bucket chains.
        let sparse_page_data = self
            .sparse_index
            .serialize_to_pages()
            .map_err(MemHopError::Serialization)?;
        let sparse_directory_page = self.write_sparse_pages(&sparse_page_data)?;
        self.header.layer_roots[1] = sparse_directory_page;

        // Serialize and save L1 reverse index using a dedicated page chain.
        let l1_data = self.l1_reverse_index.serialize()?;
        let l1_root_page = self.write_l1_reverse_pages(&l1_data)?;
        self.header.layer_roots[12] = l1_root_page;

        // Update header commit_id
        self.header.commit_id += 1;

        // Write updated headers with A/B alternation for crash safety:
        // Write B first (backup), flush, then write A (primary), flush.
        // If crash occurs between flushes, select_valid_header picks the one
        // with higher commit_id (both will be valid, both have same content).
        let header_bytes = self.header.to_bytes();
        self.mmap[PAGE_SIZE..PAGE_SIZE * 2].copy_from_slice(&header_bytes); // B first
        self.mmap.flush_range(PAGE_SIZE, PAGE_SIZE)?;
        self.mmap[..PAGE_SIZE].copy_from_slice(&header_bytes); // A second
        self.mmap.flush_range(0, PAGE_SIZE)?;

        // NOW free old pages (write-then-free strategy).
        // If a crash happened before this point, the old pages are still valid
        // and the old headers (A or B) still point to them.
        self.free_old_btree_pages(old_btree_directory, old_btree_bucket_count, old_btree_root)?;
        self.free_old_sparse_pages(old_sparse_directory)?;
        self.free_old_l1_reverse_pages(old_l1_root)?;

        Ok(())
    }

    /// Free old B-tree pages after a successful checkpoint.
    fn free_old_btree_pages(
        &mut self,
        old_directory: u32,
        old_bucket_count: u32,
        old_root: u32,
    ) -> Result<()> {
        if old_directory != 0 && old_directory < self.header.page_count && old_bucket_count > 0 {
            // Free new-format bucket chains via directory page.
            let dir_offset = (old_directory as usize) * PAGE_SIZE + 32;
            let dir_data = &self.mmap[dir_offset..dir_offset + PAGE_SIZE - 32];
            let primary_pages = Self::read_directory_page(dir_data, old_bucket_count);
            for primary_page in primary_pages {
                if primary_page != 0 && primary_page < self.header.page_count {
                    Self::free_page_chain(&mut self.mmap, &mut self.header, primary_page)?;
                }
            }
            // Free directory page itself.
            crate::file::free_list::free_page(&mut self.mmap, &mut self.header, old_directory)?;
        } else if old_root != 0 && old_root < self.header.page_count {
            // Free legacy single-page btree.
            crate::file::free_list::free_page(&mut self.mmap, &mut self.header, old_root)?;
        }
        Ok(())
    }

    /// Free old Sparse Index pages after a successful checkpoint.
    fn free_old_sparse_pages(&mut self, old_directory: u32) -> Result<()> {
        if old_directory == 0 || old_directory >= self.header.page_count {
            return Ok(());
        }

        let dir_data = match read_page_data(&self.mmap, old_directory) {
            Ok(d) => d,
            Err(_) => return Ok(()),
        };

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

        crate::file::free_list::free_page(&mut self.mmap, &mut self.header, old_directory)?;
        Ok(())
    }

    /// Free old L1 reverse index pages after a successful checkpoint.
    fn free_old_l1_reverse_pages(&mut self, old_root: u32) -> Result<()> {
        if old_root == 0 || old_root >= self.header.page_count {
            return Ok(());
        }
        Self::free_page_chain(&mut self.mmap, &mut self.header, old_root)?;
        Ok(())
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
                eprintln!("Warning: Failed to checkpoint on drop: {}", e);
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

        // Initial database has 500 pages; pages 18..499 are free (482 pages).
        assert_eq!(db.header.page_count, 500);

        // Consume all initially free pages.
        for _ in 0..482 {
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
        assert!(page_id >= 500);
        assert_eq!(db.header.page_count, 1000);

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
            .unwrap_or_else(|_| {
                panic!("allocation {} should succeed (old free list lost?)", i)
            });
        }
    }

    /// Minimal mock encoder that returns a fixed dense vector.
    struct MockEncoder {
        dim: usize,
    }

    impl crate::encoder::Encoder for MockEncoder {
        fn encode(&self, _text: &str) -> Result<crate::encoder::EncoderOutput> {
            Ok(crate::encoder::EncoderOutput {
                dense: vec![half::f16::from_f32(0.1); self.dim],
                sparse: std::collections::HashMap::new(),
            })
        }

        fn dim(&self) -> usize {
            self.dim
        }

        fn mode(&self) -> &str {
            "mock"
        }
    }

    #[test]
    fn test_l1_reverse_index_persistence() {
        use crate::query::batch::{StoreBatch, StoreItem};
        use crate::{MemHopConfig, SourceMeta, SourceType};

        let dir = TempDir::new().unwrap();
        let path = dir.path().join("l1_reverse_persist.meh");
        let mut config = MemHopConfig::new(path.clone(), 8);
        config.encoder_grpc_addr = None;
        let mut db = MemHop::open(config).unwrap();
        db.set_encoder(MockEncoder { dim: 8 });

        let batch = StoreBatch {
            items: vec![
                StoreItem {
                    text: "hello world one".to_string(),
                    topic_label: Some("greetings".to_string()),
                    domain_id: None,
                    importance: Some(0.5),
                    valence: None,
                    arousal: None,
                    source: SourceMeta::new(SourceType::UserInput, None),
                    is_structural: false,
                    source_ref: None,
                },
                StoreItem {
                    text: "hello world two".to_string(),
                    topic_label: Some("greetings".to_string()),
                    domain_id: None,
                    importance: Some(0.6),
                    valence: None,
                    arousal: None,
                    source: SourceMeta::new(SourceType::UserInput, None),
                    is_structural: false,
                    source_ref: None,
                },
            ],
            session_id: None,
            turn_id: None,
            source: Default::default(),
        };
        db.batch_store(batch).unwrap();

        // The L1 reverse index should have been built from stored data.
        assert!(!db.l1_reverse_index.is_empty());
        let original_index = db.l1_reverse_index.clone();

        // Checkpoint and close to persist everything.
        db.checkpoint().unwrap();
        assert_ne!(db.header.layer_roots[12], 0, "L1 root page should be persisted");
        db.close().unwrap();

        // Reopen and verify the L1 reverse index is loaded from disk.
        let mut config2 = MemHopConfig::new(path, 8);
        config2.encoder_grpc_addr = None;
        let mut db2 = MemHop::open(config2).unwrap();
        assert_ne!(db2.header.layer_roots[12], 0, "L1 root page should survive reopen");
        assert_eq!(
            db2.l1_reverse_index.serialize().unwrap(),
            original_index.serialize().unwrap(),
            "Loaded L1 reverse index should match original"
        );

        // Searching with the same text should still find associated contexts.
        db2.set_encoder(MockEncoder { dim: 8 });
        let result = db2
            .search_memory(crate::query::types::SearchQuery {
                dialogue: "hello world".to_string(),
                context_id: None,
                l3_id: None,
                context_limit: 10,
                llm_enhance: None,
                auto_create: 0,
                min_score: 0.0,
                context_history: None,
                source: Default::default(),
            })
            .unwrap();
        assert!(
            !result.associated_contexts.is_empty() || !result.contexts.is_empty(),
            "Search should still work after reopen"
        );
    }

    #[test]
    fn test_l1_reverse_index_fallback_rebuild() {
        // A fresh database has no persisted L1 reverse index (layer_roots[12] == 0).
        // The first open should succeed and build an empty reverse index.
        let dir = TempDir::new().unwrap();
        let path = dir.path().join("l1_reverse_fallback.meh");
        let mut config = MemHopConfig::new(path, 8);
        config.encoder_grpc_addr = None;
        let db = MemHop::open(config).unwrap();

        assert_eq!(db.header.layer_roots[12], 0);
        assert!(db.l1_reverse_index.is_empty());
    }
}
