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
        }
    }
}

/// LLM configuration for dream stages and other LLM-powered features
#[derive(Debug, Clone, Serialize, Deserialize)]
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

fn default_timeout() -> u64 {
    30
}

fn default_language() -> String {
    "zh".to_string()
}
