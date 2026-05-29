//! MemHop unified error type.

use crate::storage::StorageError;

#[derive(Debug)]
pub enum MemHopError {
    Storage(String),
    InvalidArgument(String),
    NotFound(String),
    Internal(String),
    /// v0.11.0: LMDB schema version mismatch.
    IncompatibleSchema {
        found: String,
        expected: &'static str,
        hint: &'static str,
    },
}

impl std::fmt::Display for MemHopError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            MemHopError::Storage(msg) => write!(f, "storage: {}", msg),
            MemHopError::InvalidArgument(msg) => write!(f, "invalid argument: {}", msg),
            MemHopError::NotFound(msg) => write!(f, "not found: {}", msg),
            MemHopError::Internal(msg) => write!(f, "internal: {}", msg),
            MemHopError::IncompatibleSchema { found, expected, hint } => {
                write!(f, "incompatible schema: found '{}', expected '{}'. {}", found, expected, hint)
            }
        }
    }
}

impl std::error::Error for MemHopError {}

impl From<StorageError> for MemHopError {
    fn from(err: StorageError) -> Self {
        MemHopError::Storage(err.to_string())
    }
}

/// Convenience alias used throughout the crate.
pub type Result<T> = std::result::Result<T, MemHopError>;
