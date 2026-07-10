// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Public MemHop API surface.

mod action_ops;
mod archive_ops;
mod checkpoint;
mod crud_ops;
mod diagnostic_ops;
mod dream_ops;
mod graph_ops;
mod import_ops;
mod l2_ops;
mod pathway_ops;
mod profile_ops;
mod search_ops;
mod session_ops;
mod update_ops;

use std::collections::HashMap;
use std::io;
use thiserror::Error;

use crate::config::{self, MemHopConfig};
use crate::index::l2_meta::L2MetaIndex;
use crate::index::sparse::SparseIndex;
use crate::index::vector::read_vector_from_engine;
use crate::query::search::L1ReverseIndex;
use crate::session::SessionManager;
use crate::storage::StorageEngine;

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

    #[error("Corruption: {0}")]
    Corruption(String),

    #[error("Not found: {0}")]
    NotFound(String),

    #[error("Missing field: {0}")]
    MissingField(String),
}

pub type Result<T> = std::result::Result<T, MemHopError>;

/// Main MemHop database instance
pub struct MemHop {
    pub(crate) engine: StorageEngine,
    pub(crate) config: MemHopConfig,
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
    /// In-memory L2 context metadata index (rebuilt on open, updated on write).
    pub(crate) l2_meta: L2MetaIndex,
    pub(crate) closed: bool, // Prevent Drop from re-checkpointing after close()
}

impl MemHop {
    /// Open or create a MemHop database
    pub fn open(config: MemHopConfig) -> Result<Self> {
        let db_path = &config.db_path;

        // Initialize v2 storage engine
        let engine_path = db_path.with_extension("meh");
        let engine = if engine_path.exists() {
            StorageEngine::open(&engine_path)?
        } else {
            StorageEngine::create(&engine_path, config.vector_dim as u16)?
        };

        // Validate vector dimension matches config
        if engine.vector_dim() != config.vector_dim as u16 {
            return Err(MemHopError::VectorDimensionMismatch {
                expected: config.vector_dim,
                actual: engine.vector_dim() as usize,
            });
        }

        let mut sparse_index = SparseIndex::new();
        let mut l3_index_map: HashMap<u64, crate::l3::L3Index> = HashMap::new();
        let mut pathways = Vec::new();

        // Load indices from v2 snapshot
        if let Some(snapshot) = engine.snapshot_data() {
            if !snapshot.sparse_data.is_empty() {
                match bincode::deserialize(&snapshot.sparse_data) {
                    Ok(idx) => sparse_index = idx,
                    Err(e) => tracing::warn!(
                        "Failed to deserialize sparse index from snapshot: {}. Using empty index.",
                        e
                    ),
                }
            }
            if !snapshot.l3_index_data.is_empty() {
                match bincode::deserialize(&snapshot.l3_index_data) {
                    Ok(map) => l3_index_map = map,
                    Err(e) => tracing::warn!(
                        "Failed to deserialize L3 index map from snapshot: {}. Starting empty.",
                        e
                    ),
                }
            }
            if !snapshot.l6_pathway_data.is_empty() {
                match bincode::deserialize(&snapshot.l6_pathway_data) {
                    Ok(pw) => pathways = pw,
                    Err(e) => tracing::warn!(
                        "Failed to deserialize L6 pathway data from snapshot: {}. Starting empty.",
                        e
                    ),
                }
            }
        }

        // Build L1 reverse index from engine
        let l1_reverse_index = L1ReverseIndex::build(&engine)?;

        // Prefer snapshot L1 reverse data if available
        let l1_reverse_index = if let Some(snapshot) = engine.snapshot_data() {
            if !snapshot.l1_reverse_data.is_empty() {
                match L1ReverseIndex::deserialize(&snapshot.l1_reverse_data) {
                    Ok(idx) => idx,
                    Err(e) => {
                        tracing::warn!(
                            "Failed to deserialize L1 reverse index from snapshot: {}. Using rebuilt.",
                            e
                        );
                        l1_reverse_index
                    }
                }
            } else {
                l1_reverse_index
            }
        } else {
            l1_reverse_index
        };

        let ivf_index = match crate::index::vector::read_ivf_index(&engine) {
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
                Some(addr) => {
                    match GrpcEncoder::new(&addr, config.vector_dim) {
                        Ok(enc) => Some(Box::new(enc)),
                        Err(e) => {
                            tracing::warn!(
                                "Failed to initialize gRPC encoder at {}: {} — vector search will be unavailable.",
                                addr, e
                            );
                            None
                        }
                    }
                }
                None => {
                    tracing::warn!(
                        "No gRPC encoder address configured — vector search will be unavailable. \
                         Set encoder_grpc_addr in MemHopConfig or MEMHOP_ENCODER_GRPC_ADDR env var."
                    );
                    None
                }
            }
        };

        let l2_meta = L2MetaIndex::build_from_engine(&engine);
        let adjacency_cache_max_entries = config.adjacency_cache_max_entries;

        Ok(MemHop {
            engine,
            config,
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
            l2_meta,
            closed: false,
        })
    }

    /// Rebuild IVF index from engine records
    fn rebuild_ivf_index(&mut self) {
        let Some(ref mut ivf) = self.ivf_index else {
            return;
        };

        let mut new_ivf =
            crate::index::vector::IVFIndex::new(self.config.vector_dim, self.config.ivf_initial_k);
        let dim = self.config.vector_dim;

        for (&id_hash, &_offset) in self.engine.iter_index() {
            let Ok(Some((record_type, data))) = self.engine.read_record(id_hash) else {
                continue;
            };
            // Only process REC_L2_TOPIC records
            if record_type != crate::storage::record::REC_L2_TOPIC {
                continue;
            }
            if let Ok(ctx) = bincode::deserialize::<crate::layers::context::ContextSlot>(data) {
                if ctx.centroid_page_ref != 0 {
                    // Read actual centroid vector from engine storage
                    if let Ok(centroid) =
                        read_vector_from_engine(&self.engine, ctx.centroid_page_ref, dim)
                    {
                        new_ivf.add_vector(id_hash, &centroid, 0, 0);
                    }
                }
            }
        }

        new_ivf.rebuild_if_needed(self.engine.record_count() as usize);
        *ivf = new_ivf;
    }

    /// Sync all changes to disk
    pub fn sync(&self) -> Result<()> {
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

        // 2. Mark as closed to prevent Drop from re-checkpointing
        self.closed = true;

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
