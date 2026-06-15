//! MemHop - Agent-oriented memory database inspired by human brain cognitive architecture
//!
//! MemHop is a specialized memory database designed for AI Agents, implementing
//! a six-layer cognitive architecture (L0-L5) with custom .meh binary file format.
//!
//! # Features
//! - Zero-copy mmap retrieval
//! - Hybrid search (BM25 + Vector similarity)
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

pub mod activation;
pub mod config;
pub mod dream;
pub mod encoder;
pub mod file;
pub mod index;
pub mod migrate;
pub mod organize;
pub mod query;
pub mod session;
pub mod slot;
pub mod util;

pub use config::MemHopConfig;
pub use util::{Layer, SourceMeta, SourceRef, SourceType};

// Re-export public types
pub use dream::openai_compatible::OpenAICompatibleLlmProvider;
pub use dream::llm::{CrystalDef, LlmProvider, MemorySummary, Pattern};
pub use dream::prune::DreamReport;
pub use migrate::{migrate, verify_migration, MigrateError, MigrateReport};
pub use organize::extract_keywords;
pub use query::batch::{BatchReport, EncodedItem, StoreBatch, StoreItem};

// Re-export new API types (API_NEW.md) - These are the recommended public interfaces
pub use query::types::{
    // LLM Configuration
    LlmConfig,
    // Search Memory (Interface 2)
    SearchQuery, SearchResult, ContextResult, ArchiveRef, ProfileResult,
    // Update Memory (Interface 3)
    UpdateRequest, ActionItem, ActionType, UpdateResult, UpdateStatus,
    // List Queries (Interfaces 6-12)
    EngramListQuery, EngramListResult, EngramResult,
    TopicListQuery, TopicListResult, TopicSummary, TopicDetail,
    KnowledgeListQuery, KnowledgeListResult, KnowledgeSummary, KnowledgeDetail,
    ArchivePageQuery, ArchiveListResult, Archive,
    CrystalListQuery, CrystalListResult, CrystalSummary,
    // Update Titles (Interfaces 13-16)
    UpdateProfileRequest,
    // Import Memory (Interface 19)
    ImportRequest, TargetLayer, ImportMode, ImportData,
    TopicImportItem, KnowledgeImportItem,
    ImportResult, ImportStatus, ImportError,
};

use memmap2::{Mmap, MmapMut};
use std::fs::{File, OpenOptions};
use std::io;
use thiserror::Error;

use crate::activation::{ActivationConfig, ActivationManager};
use crate::file::free_list::init_free_list;
use crate::file::header::{read_headers, select_valid_header, FileHeader};
use crate::file::journal::replay_journal;
use crate::file::page::{read_page_data, write_page_data};
use crate::index::btree::BTreeIndex as BTree;
use crate::index::sparse::SparseIndex;
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

    #[error("Invalid page type")]
    InvalidPageType,

    #[error("Serialization error: {0}")]
    Serialization(String),

    #[error("Vector dimension mismatch: expected {expected}, got {actual}")]
    VectorDimensionMismatch { expected: usize, actual: usize },

    #[error("Configuration error: {0}")]
    ConfigError(String),
}

pub type Result<T> = std::result::Result<T, MemHopError>;

/// Main MemHop database instance
#[allow(dead_code)]
pub struct MemHop {
    mmap: MmapMut,
    file: File, // Kept for potential future use (file handle management)
    header: FileHeader,
    config: MemHopConfig,
    btree: BTree,
    sparse_index: SparseIndex,
    activation_manager: ActivationManager,
    session_manager: SessionManager,
    encoder: Option<Box<dyn crate::encoder::ipc::Encoder + Send + Sync>>, // Optional encoder for batch operations
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
        let btree_page = header.layer_roots[0];
        let btree = if btree_page != 0 && btree_page < header.page_count {
            match read_page_data(&mmap_readonly, btree_page) {
                Ok(data) => match BTree::deserialize(data) {
                    Ok(index) => index,
                    Err(e) => {
                        eprintln!(
                            "Warning: Failed to load B-tree from disk: {}. Using empty index.",
                            e
                        );
                        BTree::new()
                    }
                },
                Err(_) => BTree::new(),
            }
        } else {
            BTree::new()
        };

        let sparse_index_page = header.layer_roots[1];
        let sparse_index = if sparse_index_page != 0 && sparse_index_page < header.page_count {
            match read_page_data(&mmap_readonly, sparse_index_page) {
                Ok(data) => match SparseIndex::deserialize(data) {
                    Ok(index) => index,
                    Err(e) => {
                        eprintln!(
                            "Warning: Failed to load Sparse Index from disk: {}. Using empty index.",
                            e
                        );
                        SparseIndex::new()
                    }
                },
                Err(_) => SparseIndex::new(),
            }
        } else {
            SparseIndex::new()
        };

        // 6. Initialize ActivationManager
        let activation_manager = ActivationManager::new(ActivationConfig::default());

        // 7. Initialize SessionManager
        let session_manager = SessionManager::new();

        // 8. Initialize encoder automatically from config
        let encoder: Option<Box<dyn crate::encoder::ipc::Encoder + Send + Sync>> = {
            use crate::encoder::ipc::{IpcEncoder, MockEncoder};

            let ipc = IpcEncoder::new(
                config.encoder_socket.clone(),
                config.vector_dim,
                "dense".to_string(),
            );

            if ipc.is_available() {
                Some(Box::new(ipc))
            } else {
                // Fallback to MockEncoder if socket is not available
                eprintln!("Warning: Encoder socket {:?} not available, using MockEncoder fallback", config.encoder_socket);
                Some(Box::new(MockEncoder::new(config.vector_dim)))
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
            activation_manager,
            session_manager,
            encoder,
            closed: false,
        })
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

        // Get encoder reference if available
        let encoder_ref: Option<&(dyn crate::encoder::ipc::Encoder + Send + Sync)> = self.encoder.as_deref();

        search_impl(
            &mut self.mmap,
            &mut self.header,
            query,
            &mut self.btree,
            &mut self.sparse_index,
            self.config.vector_dim,
            encoder_ref,
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
    pub fn list_archives_by_topic(&self, topic_id: &str, query: ArchivePageQuery) -> Result<ArchiveListResult> {
        use crate::query::list::list_archives_by_topic as impl_fn;
        impl_fn(&self.mmap, &self.header, &self.btree, topic_id, query)
    }

    /// List archives by node IDs
    pub fn list_archives_by_nodes(&self, node_ids: &[String], query: ArchivePageQuery) -> Result<ArchiveListResult> {
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
    pub fn get_knowledge(&self, id: &str) -> Result<Option<KnowledgeDetail>> {
        use crate::query::list::get_knowledge as impl_fn;
        impl_fn(&self.mmap, &self.btree, id)
    }

    /// List knowledge (L3 hypergraphs) with pagination and filtering
    pub fn list_knowledge(&self, query: KnowledgeListQuery) -> Result<KnowledgeListResult> {
        use crate::query::list::list_knowledge as impl_fn;
        impl_fn(&self.mmap, &self.header, &self.btree, query)
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
        impl_fn(&mut self.mmap, &mut self.header, &self.btree, &mut self.sparse_index, id, new_title)
    }

    /// Update crystal title
    pub fn update_crystal_title(&mut self, id: &str, new_title: String) -> Result<CrystalSummary> {
        use crate::query::update_title::update_crystal_title as impl_fn;
        impl_fn(&mut self.mmap, &self.btree, id, new_title)
    }

    /// Update knowledge (L3 hypergraph) title
    pub fn update_knowledge_title(&mut self, id: &str, new_title: String) -> Result<KnowledgeSummary> {
        use crate::query::update_title::update_knowledge_title as impl_fn;
        impl_fn(&mut self.mmap, &self.btree, id, new_title)
    }

    // ========================================================================
    // Advanced Function Interfaces
    // ========================================================================

    /// Merge multiple topics into a primary topic
    pub fn merge_topics(&mut self, primary_id: &str, secondary_ids: Vec<String>) -> Result<TopicDetail> {
        use crate::query::merge::merge_topics as impl_fn;
        impl_fn(&mut self.mmap, &mut self.header, &mut self.btree, &mut self.sparse_index, primary_id, secondary_ids)
    }

    /// Import memory into specified layer
    pub fn import_memory(&mut self, request: ImportRequest) -> Result<ImportResult> {
        use crate::query::import::import_memory as impl_fn;
        impl_fn(&mut self.mmap, &mut self.header, &mut self.btree, &mut self.sparse_index, request)
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
    pub fn build_l3_hypergraph_from_path(&mut self, path: &std::path::Path) -> Result<ImportResult> {
        use crate::query::import::build_l3_hypergraph_from_path as impl_fn;
        impl_fn(&mut self.mmap, &mut self.header, &mut self.btree, &mut self.sparse_index, path)
    }

    /// Activate a Topic for session management
    ///
    /// # Arguments
    /// * `topic_id` - Topic ID string (will be converted to hash)
    /// * `ttl_ms` - Optional custom TTL in milliseconds, uses default if None
    pub fn activate_topic(&mut self, topic_id: &str, ttl_ms: Option<i64>) {
        use crate::util::hash::hash_id;
        let id_hash = hash_id(topic_id);
        self.session_manager.activate_topic(id_hash, ttl_ms);
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
    /// * `llm` - LLM configuration (api_key, api_url, model)
    pub fn dream(&mut self, llm: LlmConfig) -> Result<DreamReport> {
        use crate::dream::openai_compatible::OpenAICompatibleLlmProvider;
        use crate::dream::dream_pipeline;
        use std::collections::HashSet;

        // Create LLM provider from passed configuration
        let api_key = llm.api_key;
        let api_url = llm.api_url;
        let model = llm.model;

        let llm_provider = OpenAICompatibleLlmProvider::new(api_key, api_url, model);

        let session_topics: HashSet<u64> = self.session_manager
            .get_active_topic_ids()
            .into_iter()
            .collect();

        dream_pipeline(
            &mut self.mmap,
            &mut self.header,
            &mut self.btree,
            &mut self.sparse_index,
            &llm_provider,
            session_topics,
        )
    }


    /// Sync all changes to disk
    pub fn sync(&self) -> Result<()> {
        self.mmap.flush()?;
        Ok(())
    }

    /// Set a custom encoder for batch operations
    ///
    /// # Arguments
    /// * `encoder` - Encoder implementation (e.g., MockEncoder, IpcEncoder)
    pub fn set_encoder<E: crate::encoder::ipc::Encoder + Send + Sync + 'static>(
        &mut self,
        encoder: E,
    ) {
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
        use crate::encoder::ipc::MockEncoder;
        use crate::query::batch::batch_store;

        // Use provided encoder or create a mock one
        let result = if let Some(ref enc) = self.encoder {
            // Use the configured encoder
            batch_store(
                &mut self.mmap,
                &mut self.header,
                batch,
                &mut self.btree,
                &mut self.sparse_index,
                self.config.vector_dim,
                enc.as_ref(),
            )
        } else {
            // Fallback to MockEncoder (created on-the-fly)
            let mock = MockEncoder::new(self.config.vector_dim);
            batch_store(
                &mut self.mmap,
                &mut self.header,
                batch,
                &mut self.btree,
                &mut self.sparse_index,
                self.config.vector_dim,
                &mock,
            )
        };

        result
    }

    /// Checkpoint: save indices to disk and update header
    pub fn checkpoint(&mut self) -> Result<()> {
        // Allocate pages for indices if not already allocated
        if self.header.layer_roots[0] == 0 {
            // Allocate B-tree page (use page 3 as first available after reserved)
            self.header.layer_roots[0] = 3;
        }
        if self.header.layer_roots[1] == 0 {
            // Allocate Sparse Index page (use page 4)
            self.header.layer_roots[1] = 4;
        }

        // Serialize and save B-tree
        let btree_data = self
            .btree
            .serialize()
            .map_err(MemHopError::Serialization)?;
        if btree_data.len() > PAGE_SIZE - 32 {
            return Err(MemHopError::Serialization(
                "B-tree too large for single page".to_string(),
            ));
        }
        write_page_data(&mut self.mmap, self.header.layer_roots[0], &btree_data)?;

        // Serialize and save Sparse Index
        let sparse_data = self
            .sparse_index
            .serialize()
            .map_err(MemHopError::Serialization)?;
        if sparse_data.len() > PAGE_SIZE - 32 {
            return Err(MemHopError::Serialization(
                "Sparse Index too large for single page".to_string(),
            ));
        }
        write_page_data(&mut self.mmap, self.header.layer_roots[1], &sparse_data)?;

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
