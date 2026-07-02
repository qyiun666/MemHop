// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

use serde::{Deserialize, Serialize};
use std::path::PathBuf;

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
    #[serde(default)]
    pub search_weights: Option<SearchWeights>,
    #[serde(default)]
    pub decay_config: Option<DecayConfig>,
    #[serde(default)]
    pub session_config: Option<SessionConfig>,
    #[serde(default)]
    pub auto_dream_archive_threshold: Option<usize>,
    #[serde(default)]
    pub auto_dream_summary_bytes: Option<usize>,
}

impl MemHopConfig {
    pub fn new(db_path: PathBuf, vector_dim: usize) -> Self {
        Self {
            db_path,
            encoder_grpc_addr: Some(crate::encoder::DEFAULT_ENCODER_ADDR.to_string()),
            vector_dim,
            crystal_path: None,
            llm: LlmConfig::default(),
            auto_dream_on_evict: false,
            ivf_initial_k: default_ivf_initial_k(),
            search_weights: None,
            decay_config: None,
            session_config: None,
            auto_dream_archive_threshold: None,
            auto_dream_summary_bytes: None,
        }
    }
}

fn default_auto_dream_on_evict() -> bool {
    false
}

fn default_ivf_initial_k() -> usize {
    16
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

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct DecayConfig {
    pub lambda_node: f32,
    pub lambda_edge: f32,
    pub node_remove_threshold: f32,
    pub node_prune_edges_threshold: f32,
    pub edge_remove_threshold: f32,
    pub min_edge_nodes: usize,
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
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SessionConfig {
    pub default_ttl_ms: i64,
    /// Working memory capacity (Miller's law: 7±2).
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
    pub bm25_weight: f32,
    pub vector_weight: f32,
    pub entity_weight: f32,
    #[serde(default = "default_n_probes")]
    pub n_probes: usize,
}

impl Default for SearchWeights {
    fn default() -> Self {
        Self {
            bm25_weight: 0.4,
            vector_weight: 0.4,
            entity_weight: 0.2,
            n_probes: default_n_probes(),
        }
    }
}
