// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

use serde::{Deserialize, Serialize};
use std::path::PathBuf;

/// Default gRPC encoder address (meowvec VectorModelService TCP endpoint).
pub const DEFAULT_ENCODER_ADDR: &str = "http://127.0.0.1:27110";

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct MemHopConfig {
    pub db_path: PathBuf,
    /// gRPC encoder address (TCP). Env `MEMHOP_ENCODER_GRPC_ADDR` overrides.
    pub encoder_grpc_addr: Option<String>,
    pub vector_dim: usize,
    pub crystal_path: Option<PathBuf>,
    #[serde(default)]
    pub llm: LlmConfig,
    #[serde(default = "default_auto_dream_on_evict")]
    pub auto_dream_on_evict: bool,
    #[serde(default = "default_ivf_initial_k")]
    pub ivf_initial_k: usize,
    /// Auto-trigger dream when L2 context archive count exceeds this (default 20).
    /// 0 means no auto-dream by archive count.
    #[serde(default = "default_auto_dream_archive_threshold")]
    pub auto_dream_archive_threshold: usize,
    /// Auto-trigger dream when L2 context summary bytes exceed this (default 2048).
    /// 0 means no auto-dream by summary bytes.
    #[serde(default = "default_auto_dream_summary_bytes")]
    pub auto_dream_summary_bytes: usize,
    #[serde(default)]
    pub search_weights: Option<SearchWeights>,
    #[serde(default)]
    pub decay_config: Option<DecayConfig>,
    #[serde(default)]
    pub session_config: Option<SessionConfig>,
    /// L2 context idle time (seconds) before automatic dream consolidation.
    /// When a depth-1 context has not been updated for this duration,
    /// the next sync/checkpoint cycle will trigger dream on it.
    /// `None` disables idle-triggered dreaming.
    #[serde(default = "default_dream_idle_threshold_secs")]
    pub dream_idle_threshold_secs: Option<u64>,
    /// Number of uncommitted updates to buffer before auto-checkpoint.
    /// `None` means checkpoint after every update.
    #[serde(default)]
    pub auto_checkpoint_interval: Option<u64>,
    /// Maximum number of entries kept in the L3 adjacency cache.
    #[serde(default = "default_adjacency_cache_max_entries")]
    pub adjacency_cache_max_entries: usize,
    /// LLM preprocessing configuration (search keyword extraction + write encoding).
    #[serde(default)]
    pub llm_preprocess: LlmPreprocessConfig,
}

impl MemHopConfig {
    pub fn new(db_path: PathBuf, vector_dim: usize) -> Self {
        Self {
            db_path,
            #[cfg(feature = "grpc-encoder")]
            encoder_grpc_addr: Some(DEFAULT_ENCODER_ADDR.to_string()),
            #[cfg(not(feature = "grpc-encoder"))]
            encoder_grpc_addr: None,
            vector_dim,
            crystal_path: None,
            llm: LlmConfig::default(),
            auto_dream_on_evict: false,
            auto_dream_archive_threshold: default_auto_dream_archive_threshold(),
            auto_dream_summary_bytes: default_auto_dream_summary_bytes(),
            ivf_initial_k: default_ivf_initial_k(),
            search_weights: None,
            decay_config: None,
            session_config: None,
            dream_idle_threshold_secs: default_dream_idle_threshold_secs(),
            auto_checkpoint_interval: None,
            adjacency_cache_max_entries: default_adjacency_cache_max_entries(),
            llm_preprocess: LlmPreprocessConfig::default(),
        }
    }
}

fn default_auto_dream_on_evict() -> bool {
    false
}

fn default_dream_idle_threshold_secs() -> Option<u64> {
    Some(3600) // 1 hour
}

fn default_auto_dream_archive_threshold() -> usize {
    20
}

fn default_auto_dream_summary_bytes() -> usize {
    2048
}

fn default_ivf_initial_k() -> usize {
    16
}

fn default_adjacency_cache_max_entries() -> usize {
    128
}

fn default_n_probes() -> usize {
    8
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(default)]
pub struct LlmConfig {
    /// Full API URL. Backwards-compatible alias `api_base` also accepted.
    #[serde(alias = "api_base")]
    pub api_url: String,
    /// Falls back to `MEMHOP_LLM_API_KEY` env var when using Default.
    pub api_key: String,
    pub model: String,
    #[serde(default = "default_temperature")]
    pub temperature: f32,
    #[serde(default = "default_top_p")]
    pub top_p: f32,
    #[serde(default = "default_presence_penalty")]
    pub presence_penalty: f32,
    #[serde(default = "default_frequency_penalty")]
    pub frequency_penalty: f32,
    #[serde(default = "default_timeout")]
    pub timeout_secs: u64,
    #[serde(default = "default_language")]
    pub language: String,
}

impl Default for LlmConfig {
    fn default() -> Self {
        Self {
            api_url: String::new(),
            api_key: default_api_key(),
            model: String::new(),
            temperature: default_temperature(),
            top_p: default_top_p(),
            presence_penalty: default_presence_penalty(),
            frequency_penalty: default_frequency_penalty(),
            timeout_secs: default_timeout(),
            language: default_language(),
        }
    }
}

fn default_api_key() -> String {
    std::env::var("MEMHOP_LLM_API_KEY").unwrap_or_default()
}

fn default_temperature() -> f32 {
    0.2
}

fn default_top_p() -> f32 {
    0.9
}

fn default_presence_penalty() -> f32 {
    0.0
}

fn default_frequency_penalty() -> f32 {
    0.0
}

fn default_timeout() -> u64 {
    30
}

fn default_language() -> String {
    "zh".to_string()
}

// ============================================================================
// LLM Preprocessing configuration (v0.61)
// ============================================================================

/// Configuration for LLM-based content preprocessing in search and write paths.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(default)]
pub struct LlmPreprocessConfig {
    /// Enable LLM preprocessing for search queries (keyword extraction + L3 import judgment).
    /// When disabled, falls back to tokenizer-based keyword extraction.
    #[serde(default = "default_true")]
    pub enable_search_preprocess: bool,
    /// Enable LLM preprocessing for write content (keyword extraction + importance scoring).
    /// When disabled, falls back to tokenizer-based keyword extraction.
    #[serde(default = "default_true")]
    pub enable_write_preprocess: bool,
    /// Temperature for preprocess LLM calls (default 0.1 for deterministic extraction).
    #[serde(default = "default_preprocess_temperature")]
    pub preprocess_temperature: f32,
    /// Max tokens for preprocess LLM responses (default 512).
    #[serde(default = "default_preprocess_max_tokens")]
    pub preprocess_max_tokens: u32,
    /// Fall back to tokenizer when LLM is unavailable or fails.
    #[serde(default = "default_true")]
    pub fallback_to_tokenizer: bool,
}

impl Default for LlmPreprocessConfig {
    fn default() -> Self {
        Self {
            enable_search_preprocess: true,
            enable_write_preprocess: true,
            preprocess_temperature: default_preprocess_temperature(),
            preprocess_max_tokens: default_preprocess_max_tokens(),
            fallback_to_tokenizer: true,
        }
    }
}

fn default_true() -> bool {
    true
}

fn default_preprocess_temperature() -> f32 {
    0.1
}

fn default_preprocess_max_tokens() -> u32 {
    512
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct DecayConfig {
    pub lambda_node: f32,
    pub lambda_edge: f32,
    pub node_remove_threshold: f32,
    pub node_prune_edges_threshold: f32,
    pub edge_remove_threshold: f32,
    pub min_edge_nodes: usize,
    /// L6 pathway weight exponential decay lambda (per second).
    #[serde(default = "default_lambda_pathway")]
    pub lambda_pathway: f32,
    /// L6 pathway weight removal threshold after decay.
    #[serde(default = "default_pathway_remove_threshold")]
    pub pathway_remove_threshold: f32,
}

fn default_lambda_pathway() -> f32 {
    0.01
}

fn default_pathway_remove_threshold() -> f32 {
    0.05
}

impl Default for DecayConfig {
    fn default() -> Self {
        Self {
            lambda_node: 0.01,
            lambda_edge: 0.02,
            node_remove_threshold: 0.05,
            node_prune_edges_threshold: 0.15,
            edge_remove_threshold: 0.05,
            min_edge_nodes: 2,
            lambda_pathway: default_lambda_pathway(),
            pathway_remove_threshold: default_pathway_remove_threshold(),
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SessionConfig {
    #[serde(default = "default_ttl_ms")]
    pub default_ttl_ms: i64,
    /// Working memory capacity (Miller's law: 7±2).
    #[serde(default = "default_capacity")]
    pub capacity: usize,
}

impl Default for SessionConfig {
    fn default() -> Self {
        Self {
            default_ttl_ms: 3_600_000,
            capacity: 7,
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SearchWeights {
    #[serde(default = "default_bm25_weight")]
    pub bm25_weight: f32,
    #[serde(default = "default_vector_weight")]
    pub vector_weight: f32,
    #[serde(default = "default_n_probes")]
    pub n_probes: usize,
    #[serde(default)]
    pub enable_reranker: bool,
    #[serde(default = "default_rerank_max_candidates")]
    pub rerank_max_candidates: usize,
    /// Recency boost weight (multiplier applied to time-decay score, default 0.5).
    /// score += recency_weight * exp(-age_days / 7)
    #[serde(default = "default_recency_weight")]
    pub recency_weight: f32,
    /// Activation boost multiplier for active-session topics (default 1.3).
    /// score *= activation_boost when topic is in working memory
    #[serde(default = "default_activation_boost")]
    pub activation_boost: f32,
}

impl Default for SearchWeights {
    fn default() -> Self {
        Self {
            bm25_weight: 0.45,
            vector_weight: 0.55,
            n_probes: default_n_probes(),
            enable_reranker: true,
            rerank_max_candidates: default_rerank_max_candidates(),
            recency_weight: default_recency_weight(),
            activation_boost: default_activation_boost(),
        }
    }
}

fn default_rerank_max_candidates() -> usize {
    20
}

fn default_recency_weight() -> f32 {
    0.5
}

fn default_activation_boost() -> f32 {
    1.3
}

fn default_bm25_weight() -> f32 {
    0.45
}

fn default_vector_weight() -> f32 {
    0.55
}

fn default_ttl_ms() -> i64 {
    3_600_000
}

fn default_capacity() -> usize {
    7
}
