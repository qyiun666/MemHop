// Utility module
pub mod hash;
pub mod io_helpers;

// Re-export hash function
pub use hash::hash_id;

use serde::{Deserialize, Serialize};
use std::fmt;

/// Page size constant (4KB)
pub const PAGE_SIZE: usize = 4096;

/// Magic bytes for .meh file header
pub const MAGIC: [u8; 4] = [0x4D, 0x45, 0x48, 0x21]; // "MEH!"

/// Tail magic bytes
pub const TAIL_MAGIC: [u8; 4] = [0xDE, 0xAD, 0xBE, 0xEF];

/// Version constant (v0.35 = 0x0023)
pub const VERSION: u16 = 0x0023;

/// Cognitive architecture layers (L0-L5)
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum Layer {
    Profile,     // L0: Agent identity
    ContextNode, // L1: Hypergraph skeleton node
    Hyperedge,   // L1: Hypergraph skeleton edge
    Context,     // L2: Scene-based conversation context
    Hypergraph,  // L3: Generic hypergraph engine
    Archive,     // L4: Raw text + file paths
    ActionChain, // L5: Ordered action sequences
}

impl Layer {
    /// Convert layer to u8 for storage
    pub fn to_u8(&self) -> u8 {
        match self {
            Layer::Profile => 0,
            Layer::ContextNode => 1,
            Layer::Hyperedge => 2,
            Layer::Context => 3,
            Layer::Hypergraph => 4,
            Layer::Archive => 5,
            Layer::ActionChain => 6,
        }
    }

    /// Convert u8 to Layer
    pub fn from_u8(value: u8) -> Option<Layer> {
        match value {
            0 => Some(Layer::Profile),
            1 => Some(Layer::ContextNode),
            2 => Some(Layer::Hyperedge),
            3 => Some(Layer::Context),
            4 => Some(Layer::Hypergraph),
            5 => Some(Layer::Archive),
            6 => Some(Layer::ActionChain),
            _ => None,
        }
    }
}

impl fmt::Display for Layer {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Layer::Profile => write!(f, "Profile"),
            Layer::ContextNode => write!(f, "ContextNode"),
            Layer::Hyperedge => write!(f, "Hyperedge"),
            Layer::Context => write!(f, "Context"),
            Layer::Hypergraph => write!(f, "Hypergraph"),
            Layer::Archive => write!(f, "Archive"),
            Layer::ActionChain => write!(f, "ActionChain"),
        }
    }
}

/// Source type for memory origin
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[repr(u8)]
pub enum SourceType {
    UserInput = 0,
    SystemGenerated = 1,
    ExternalAPI = 2,
    FileImport = 3,
}

/// Metadata about the source of a memory
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct SourceMeta {
    pub source_type: SourceType,
    pub source_id: Option<String>,
    pub timestamp: i64,
}

impl Default for SourceMeta {
    fn default() -> Self {
        Self {
            source_type: SourceType::UserInput,
            source_id: None,
            timestamp: 0,
        }
    }
}

impl SourceMeta {
    /// Create a new SourceMeta with current timestamp
    pub fn new(source_type: SourceType, source_id: Option<String>) -> Self {
        let timestamp = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap_or_default()
            .as_millis() as i64;

        Self {
            source_type,
            source_id,
            timestamp,
        }
    }
}

/// Get current timestamp in milliseconds since UNIX epoch
#[inline]
pub fn get_current_timestamp() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as i64
}

/// Reference to a source location (for external references)
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct SourceRef {
    pub uri: String,
    pub offset: Option<u64>,
    pub length: Option<u64>,
}

/// Page type encoding
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[repr(u16)]
pub enum PageType {
    ContextNode = 0x01,    // L1 graph node
    Hyperedge = 0x02,      // L1 hyperedge
    VectorMatrix = 0x03,   // Vector storage page
    SparseIndex = 0x04,    // BM25/ngram index
    Context = 0x05,        // L2 scene context
    HypergraphSlot = 0x06, // L3 hypergraph container
    Archive = 0x07,        // L4 raw archive
    ActionChain = 0x08,    // L5 action chain
    ActionStep = 0x09,     // L5 action step
    Profile = 0x0A,        // L0 agent profile
    HypergraphNode = 0x0B, // L3 hypergraph node
    HypergraphEdge = 0x0C, // L3 hypergraph edge
    L3IndexPage = 0x0D,    // L3 engine index page
    IVFCluster = 0x0E,     // IVF centroid storage page
    IVFBucket = 0x0F,      // IVF bucket (vector id list) page
    BTreeNode = 0x10,      // B-tree internal node
    BTreeLeaf = 0x11,      // B-tree leaf node
    L1ReverseIndex = 0x12, // L1 reverse index page
    Free = 0x20,           // Free page
    Overflow = 0xFF,       // Overflow page
}

impl PageType {
    /// Convert to u16 for storage
    pub fn to_u16(&self) -> u16 {
        *self as u16
    }

    /// Convert from u16
    pub fn from_u16(value: u16) -> Option<PageType> {
        match value {
            0x01 => Some(PageType::ContextNode),
            0x02 => Some(PageType::Hyperedge),
            0x03 => Some(PageType::VectorMatrix),
            0x04 => Some(PageType::SparseIndex),
            0x05 => Some(PageType::Context),
            0x06 => Some(PageType::HypergraphSlot),
            0x07 => Some(PageType::Archive),
            0x08 => Some(PageType::ActionChain),
            0x09 => Some(PageType::ActionStep),
            0x0A => Some(PageType::Profile),
            0x0B => Some(PageType::HypergraphNode),
            0x0C => Some(PageType::HypergraphEdge),
            0x0D => Some(PageType::L3IndexPage),
            0x0E => Some(PageType::IVFCluster),
            0x0F => Some(PageType::IVFBucket),
            0x10 => Some(PageType::BTreeNode),
            0x11 => Some(PageType::BTreeLeaf),
            0x12 => Some(PageType::L1ReverseIndex),
            0x20 => Some(PageType::Free),
            0xFF => Some(PageType::Overflow),
            _ => None,
        }
    }
}
