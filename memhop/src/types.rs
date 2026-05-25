//! Core data types for MemHop — pure Rust.

use serde::{Deserialize, Serialize};
use std::collections::HashMap;

use crate::hopfield::ModernHopfield;
use crate::index::SparseIndex;
use crate::meta_index::MetaIndex;
use crate::storage::LmdbStorage;

// ── Vector dimension ──────────────────────────────────────
pub const VECTOR_DIM: usize = 1024;

// ── Protection levels ─────────────────────────────────────

#[derive(Debug, Clone, Serialize, Deserialize)]
pub enum Protection {
    Normal,
    Protected,
    Permanent,
}

// ── Public Memory type ────────────────────────────────────

#[derive(Debug, Clone)]
pub struct Memory {
    pub id: String,
    pub text: String,
    pub meta: HashMap<String, serde_json::Value>,
    pub confidence: f32,
    pub created_at: String,
    pub content_type: Option<String>,
    pub blob: Option<Vec<u8>>,
}

// ── Domain Tree ───────────────────────────────────────────

pub struct DomainTree {
    pub name: String,
    pub hopfield: ModernHopfield,
    pub sparse_index: SparseIndex,
    pub meta_index: MetaIndex,
    pub storage: LmdbStorage,
}

// ── StoreOptions ──────────────────────────────────────────

pub struct StoreOptions {
    /// Automatically discover associations and create entangle edges (default true).
    pub auto_entangle: bool,
    /// Context snippet at storage time (1-2 sentences).
    pub context_snippet: Option<String>,
    /// Manually specify memory IDs to link.
    pub manual_links: Vec<String>,
}

impl Default for StoreOptions {
    fn default() -> Self {
        StoreOptions {
            auto_entangle: true,
            context_snippet: None,
            manual_links: Vec::new(),
        }
    }
}

// ── DreamConfig ───────────────────────────────────────────

#[derive(Debug, Clone)]
pub struct DreamConfig {
    /// Trigger dream after every N store() calls (default 100).
    pub auto_trigger_interval: usize,
    /// Cosine similarity > this value triggers pattern merge (default 0.95).
    pub merge_threshold: f32,
    /// Confidence < this value triggers pattern weakening (default 0.3).
    pub weaken_threshold: f32,
    /// Maximum dream duration in milliseconds (default 500).
    pub max_duration_ms: u64,
}

impl Default for DreamConfig {
    fn default() -> Self {
        DreamConfig {
            auto_trigger_interval: 100,
            merge_threshold: 0.95,
            weaken_threshold: 0.3,
            max_duration_ms: 500,
        }
    }
}
