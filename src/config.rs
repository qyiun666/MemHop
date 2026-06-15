use std::path::PathBuf;

/// Configuration for MemHop database
#[derive(Debug, Clone)]
pub struct MemHopConfig {
    /// Path to the .meh database file
    pub db_path: PathBuf,
    /// Path to encoder socket (reserved for v0.31+)
    pub encoder_socket: PathBuf,
    /// Vector dimension (specified at creation time)
    pub vector_dim: usize,
    /// Crystal knowledge storage path (optional, default: same directory as db_path)
    pub crystal_path: Option<PathBuf>,
}

impl MemHopConfig {
    /// Create a new configuration with default encoder socket path
    pub fn new(db_path: PathBuf, vector_dim: usize) -> Self {
        Self {
            db_path,
            encoder_socket: std::env::temp_dir().join("memhop_encoder.sock"),
            vector_dim,
            crystal_path: None,
        }
    }
}
