use serde::{Deserialize, Serialize};
use std::path::PathBuf;

/// Configuration for MemHop database
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct MemHopConfig {
    /// Path to the .meh database file
    pub db_path: PathBuf,
    /// gRPC encoder address (Unix socket). Defaults to meowvec UDS path.
    /// Environment variable MEMHOP_ENCODER_GRPC_ADDR overrides this.
    pub encoder_grpc_addr: Option<String>,
    /// Vector dimension (specified at creation time)
    pub vector_dim: usize,
    /// Crystal knowledge storage path (optional, default: same directory as db_path)
    pub crystal_path: Option<PathBuf>,
}

impl MemHopConfig {
    /// Create a new configuration with default gRPC address
    pub fn new(db_path: PathBuf, vector_dim: usize) -> Self {
        Self {
            db_path,
            encoder_grpc_addr: Some(crate::encoder::DEFAULT_ENCODER_ADDR.to_string()),
            vector_dim,
            crystal_path: None,
        }
    }
}
