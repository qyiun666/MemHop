// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

use serde::{Deserialize, Serialize};
use std::path::PathBuf;

/// 打开or创建数据库参数
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct MemHopConfig {
    /// meh数据库路径
    pub db_path: PathBuf,
    /// gRPC 编码器地址，格式如 "http://127.0.0.1:27110"（默认 http://127.0.0.1:27110）
    #[serde(
        default = "default_encoder_addr",
        deserialize_with = "deserialize_option_or_default"
    )]
    pub encoder_grpc_addr: String,
    /// 向量模型的维度
    pub vector_dim: usize,
    pub crystal_path: Option<PathBuf>,
    pub llm: LlmConfig,
    pub auto_dream_on_evict: bool,
    pub ivf_initial_k: usize,
    pub auto_dream_archive_threshold: usize,
    pub auto_dream_summary_bytes: usize,
    pub search_weights: Option<SearchWeights>,
    pub decay_config: Option<DecayConfig>,
    pub session_config: Option<SessionConfig>,
    pub dream_idle_threshold_secs: Option<u64>,
    pub auto_checkpoint_interval: Option<u64>,
    pub adjacency_cache_max_entries: usize,
    pub llm_preprocess: LlmPreprocessConfig,
}

impl MemHopConfig {
    pub fn new(db_path: PathBuf, vector_dim: usize) -> Self {
        Self {
            db_path,
            vector_dim,
            encoder_grpc_addr: default_encoder_addr(),
            crystal_path: None,
            llm: LlmConfig::default(),
            auto_dream_on_evict: false,
            ivf_initial_k: 16,
            auto_dream_archive_threshold: 20,
            auto_dream_summary_bytes: 2048,
            search_weights: Some(SearchWeights::default()),
            decay_config: Some(DecayConfig::default()),
            session_config: Some(SessionConfig::default()),
            dream_idle_threshold_secs: Some(3600),
            auto_checkpoint_interval: None,
            adjacency_cache_max_entries: 128,
            llm_preprocess: LlmPreprocessConfig::default(),
        }
    }
}

/// 打开or创建 llm参数
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct LlmConfig {
    pub api_url: String,
    pub api_key: String,
    pub model: String,
    pub temperature: f32,
    pub top_p: f32,
    pub presence_penalty: f32,
    pub frequency_penalty: f32,
    pub timeout_secs: u64,
    pub language: String,
}

impl Default for LlmConfig {
    fn default() -> Self {
        Self {
            api_url: String::new(),
            api_key: String::new(),
            model: String::new(),
            temperature: 0.2,
            top_p: 0.9,
            presence_penalty: 0.0,
            frequency_penalty: 0.0,
            timeout_secs: 30,
            language: "zh".to_string(),
        }
    }
}

impl LlmConfig {
    pub fn new(api_url: String, api_key: String, model: String, language: String) -> Self {
        Self {
            api_url,
            api_key,
            model,
            temperature: 0.2,
            top_p: 0.9,
            presence_penalty: 0.0,
            frequency_penalty: 0.0,
            timeout_secs: 30,
            language,
        }
    }
}

// ============================================================================
// LLM Preprocessing configuration (v0.61)
// ============================================================================

/// Configuration for LLM-based content preprocessing in search and write paths.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct LlmPreprocessConfig {
    /// Temperature for preprocess LLM calls (default 0.1 for deterministic extraction).
    pub preprocess_temperature: f32,
    /// Max tokens for preprocess LLM responses (default 512).
    pub preprocess_max_tokens: u32,
    // (removed: fallback_to_tokenizer — LLM failures now return errors)
}

impl Default for LlmPreprocessConfig {
    fn default() -> Self {
        Self {
            preprocess_temperature: 0.1,
            preprocess_max_tokens: 512,
        }
    }
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
    pub n_probes: usize,
    pub enable_reranker: bool,
    pub rerank_max_candidates: usize,
    /// Activation boost multiplier for active-session topics (default 1.3).
    /// score *= activation_boost when topic is in working memory
    pub activation_boost: f32,
}

impl Default for SearchWeights {
    fn default() -> Self {
        Self {
            bm25_weight: 0.45,
            vector_weight: 0.55,
            n_probes: 8,
            enable_reranker: true,
            rerank_max_candidates: 20,
            activation_boost: 1.3,
        }
    }
}

fn default_encoder_addr() -> String {
    "http://127.0.0.1:27110".to_string()
}

fn deserialize_option_or_default<'de, D>(deserializer: D) -> Result<String, D::Error>
where
    D: serde::Deserializer<'de>,
{
    let opt = Option::<String>::deserialize(deserializer)?;
    Ok(opt.unwrap_or_else(default_encoder_addr))
}
