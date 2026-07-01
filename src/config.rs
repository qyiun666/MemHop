use serde::{Deserialize, Serialize};
use std::path::PathBuf;

/// Configuration for MemHop database
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct MemHopConfig {
    /// Path to the .meh database file
    pub db_path: PathBuf,
    /// gRPC encoder address (TCP). Defaults to meowvec TCP endpoint.
    /// Environment variable MEMHOP_ENCODER_GRPC_ADDR overrides this.
    pub encoder_grpc_addr: Option<String>,
    /// Vector dimension (specified at creation time)
    pub vector_dim: usize,
    /// Crystal knowledge storage path (optional, default: same directory as db_path)
    pub crystal_path: Option<PathBuf>,
    /// LLM configuration for dream stages and other LLM-powered features
    #[serde(default)]
    pub llm: LlmConfig,
    /// Automatically run lightweight dream consolidation when a topic is evicted
    /// from working memory due to capacity limits (default: true)
    #[serde(default = "default_auto_dream_on_evict")]
    pub auto_dream_on_evict: bool,
    /// IVF index initial number of clusters (default: 16)
    #[serde(default = "default_ivf_initial_k")]
    pub ivf_initial_k: usize,
    /// Custom search weights (optional, uses default if None)
    #[serde(default)]
    pub search_weights: Option<SearchWeights>,
    /// Custom decay configuration (optional, uses default if None)
    #[serde(default)]
    pub decay_config: Option<DecayConfig>,
    /// Custom session configuration (optional, uses default if None)
    #[serde(default)]
    pub session_config: Option<SessionConfig>,
    /// Auto-dream archive threshold (optional)
    #[serde(default)]
    pub auto_dream_archive_threshold: Option<usize>,
    /// Auto-dream summary bytes limit (optional)
    #[serde(default)]
    pub auto_dream_summary_bytes: Option<usize>,
}

impl MemHopConfig {
    /// Create a new configuration with default gRPC address
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

/// LLM configuration for dream stages and other LLM-powered features
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(default)]
pub struct LlmConfig {
    /// Full API URL (including `/chat/completions` suffix).
    /// Backwards-compatible alias `api_base` is also accepted during deserialization.
    #[serde(alias = "api_base")]
    pub api_url: String,
    /// API key. When using `Default`, falls back to the `MEMHOP_LLM_API_KEY`
    /// environment variable if present.
    pub api_key: String,
    /// Model name
    pub model: String,
    /// Sampling temperature. Lower values produce more deterministic output
    /// (default: 0.2, suitable for memory consolidation).
    #[serde(default = "default_temperature")]
    pub temperature: f32,
    /// Nucleus sampling parameter (default: 0.9)
    #[serde(default = "default_top_p")]
    pub top_p: f32,
    /// Presence penalty (default: 0.0)
    #[serde(default = "default_presence_penalty")]
    pub presence_penalty: f32,
    /// Frequency penalty (default: 0.0)
    #[serde(default = "default_frequency_penalty")]
    pub frequency_penalty: f32,
    /// Request timeout in seconds (default: 30)
    #[serde(default = "default_timeout")]
    pub timeout_secs: u64,
    /// Expected response language (default: "zh")
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

/// Configuration for L1 decay parameters
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct DecayConfig {
    /// Decay rate for L1 nodes
    pub lambda_node: f32,
    /// Decay rate for L1 edges
    pub lambda_edge: f32,
    /// Threshold below which a node is removed
    pub node_remove_threshold: f32,
    /// Threshold below which a node's edges are pruned
    pub node_prune_edges_threshold: f32,
    /// Threshold below which an edge is removed
    pub edge_remove_threshold: f32,
    /// Minimum number of nodes an edge must connect to be retained
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

/// Configuration for session management
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SessionConfig {
    /// Default time-to-live in milliseconds (default: 1 hour)
    pub default_ttl_ms: i64,
    /// Working memory capacity (default: 7, Miller's law)
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

/// Configuration for search weights
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SearchWeights {
    /// Weight for BM25 text relevance
    pub bm25_weight: f32,
    /// Weight for vector similarity
    pub vector_weight: f32,
    /// Weight for entity matching
    pub entity_weight: f32,
    /// IVF n_probes: number of centroids to probe during search (default: 8)
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
