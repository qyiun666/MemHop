use std::fmt;

#[derive(Debug)]
pub enum MemHopError {
    Storage(String),
    StorageFull(String),
    Encode(String),
    NotFound(String),
    InvalidArgument(String),
    Internal(String),
    Batch(String),
}

impl fmt::Display for MemHopError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            MemHopError::Storage(msg) => write!(f, "storage error: {}", msg),
            MemHopError::StorageFull(msg) => write!(f, "storage full: {}", msg),
            MemHopError::Encode(msg) => write!(f, "encode error: {}", msg),
            MemHopError::NotFound(msg) => write!(f, "not found: {}", msg),
            MemHopError::InvalidArgument(msg) => write!(f, "invalid argument: {}", msg),
            MemHopError::Internal(msg) => write!(f, "internal error: {}", msg),
            MemHopError::Batch(msg) => write!(f, "batch error: {}", msg),
        }
    }
}

impl std::error::Error for MemHopError {}

pub type Result<T> = std::result::Result<T, MemHopError>;

impl From<heed::Error> for MemHopError {
    fn from(e: heed::Error) -> Self {
        match &e {
            heed::Error::Mdb(heed::MdbError::MapFull) => {
                MemHopError::StorageFull(e.to_string())
            }
            _ => MemHopError::Storage(e.to_string()),
        }
    }
}

impl From<serde_json::Error> for MemHopError {
    fn from(e: serde_json::Error) -> Self {
        MemHopError::Internal(e.to_string())
    }
}

impl From<bincode::Error> for MemHopError {
    fn from(e: bincode::Error) -> Self {
        MemHopError::Storage(e.to_string())
    }
}
