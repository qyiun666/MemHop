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
    /// Model name (default: "deepseek-chat")
    #[serde(default = "default_model")]
    pub model: String,
    /// API base URL without the `/chat/completions` suffix (default: "https://api.deepseek.com/v1")
    #[serde(default = "default_api_base")]
    pub api_base: String,
    /// API key. Defaults to the value of the `MEMHOP_DEEPSEEK_KEY` environment variable.
    #[serde(default = "default_api_key")]
    pub api_key: String,
    /// Sampling temperature. Lower values produce more deterministic output
    /// (default: 0.2, suitable for memory consolidation).
    #[serde(default = "default_temperature")]
    pub temperature: f32,
    /// Request timeout in seconds (default: 30)
    #[serde(default = "default_timeout_secs")]
    pub timeout_secs: u64,
    /// Expected response language (default: "zh")
    #[serde(default = "default_language")]
    pub language: String,
}

impl Default for LlmConfig {
    fn default() -> Self {
        Self {
            model: default_model(),
            api_base: default_api_base(),
            api_key: default_api_key(),
            temperature: default_temperature(),
            timeout_secs: default_timeout_secs(),
            language: default_language(),
        }
    }
}

fn default_model() -> String {
    "deepseek-chat".to_string()
}

fn default_api_base() -> String {
    "https://api.deepseek.com/v1".to_string()
}

fn default_api_key() -> String {
    std::env::var("MEMHOP_DEEPSEEK_KEY").unwrap_or_default()
}

fn default_temperature() -> f32 {
    0.2
}

fn default_timeout_secs() -> u64 {
    30
}

fn default_language() -> String {
    "zh".to_string()
}

impl LlmConfig {
    /// Return the full OpenAI-compatible chat completions URL.
    pub fn api_url(&self) -> String {
        let base = self.api_base.trim_end_matches('/');
        format!("{}/chat/completions", base)
    }
}
