// Utility module
pub mod f16;
pub mod hash;
pub mod io_helpers;

// Re-export hash function
pub use hash::hash_id;

use std::fmt;

/// Page size constant (4KB)
pub const PAGE_SIZE: usize = 4096;

/// Magic bytes for .meh file header
pub const MAGIC: [u8; 4] = [0x4D, 0x45, 0x48, 0x21]; // "MEH!"

/// Tail magic bytes
pub const TAIL_MAGIC: [u8; 4] = [0xDE, 0xAD, 0xBE, 0xEF];

/// Version constant (v0.34 = 0x0022)
pub const VERSION: u16 = 0x0022;

/// Cognitive architecture layers (L0-L5)
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Layer {
    L0, // Profile
    L1, // Engram - Episodic hypergraph
    L2, // Topic - Semantic compression
    L3, // Knowledge - Domain knowledge graph
    L4, // Archive - Raw text + file paths
    L5, // Crystal - Programmatic knowledge
}

impl Layer {
    /// Convert layer to u8 for storage
    pub fn to_u8(&self) -> u8 {
        match self {
            Layer::L0 => 0,
            Layer::L1 => 1,
            Layer::L2 => 2,
            Layer::L3 => 3,
            Layer::L4 => 4,
            Layer::L5 => 5,
        }
    }

    /// Convert u8 to Layer
    pub fn from_u8(value: u8) -> Option<Layer> {
        match value {
            0 => Some(Layer::L0),
            1 => Some(Layer::L1),
            2 => Some(Layer::L2),
            3 => Some(Layer::L3),
            4 => Some(Layer::L4),
            5 => Some(Layer::L5),
            _ => None,
        }
    }
}

impl fmt::Display for Layer {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Layer::L0 => write!(f, "L0"),
            Layer::L1 => write!(f, "L1"),
            Layer::L2 => write!(f, "L2"),
            Layer::L3 => write!(f, "L3"),
            Layer::L4 => write!(f, "L4"),
            Layer::L5 => write!(f, "L5"),
        }
    }
}

/// Memory state (reserved for v0.31+)
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[repr(u8)]
pub enum MemoryState {
    Active = 0,
    Latent = 1,
    Dormant = 2,
}

/// Emotion type (reserved for v0.31+)
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[repr(u8)]
pub enum EmotionType {
    Neutral = 0,
    Joy = 1,
    Sadness = 2,
    Anger = 3,
    Fear = 4,
    Surprise = 5,
    Disgust = 6,
}

/// Source type for memory origin
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[repr(u8)]
pub enum SourceType {
    UserInput = 0,
    SystemGenerated = 1,
    ExternalAPI = 2,
    FileImport = 3,
}

/// Metadata about the source of a memory
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SourceMeta {
    pub source_type: SourceType,
    pub source_id: Option<String>,
    pub timestamp: i64,
}

impl SourceMeta {
    /// Create a new SourceMeta with current timestamp
    pub fn new(source_type: SourceType, source_id: Option<String>) -> Self {
        let timestamp = get_current_timestamp();

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
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SourceRef {
    pub uri: String,
    pub offset: Option<u64>,
    pub length: Option<u64>,
}

/// Page type encoding
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[repr(u16)]
pub enum PageType {
    Engram = 0x01,
    Hyperedge = 0x02,
    VectorMatrix = 0x03,
    SparseIndex = 0x04,
    Topic = 0x05,
    TopicEdge = 0x06,
    Archive = 0x07,
    Crystal = 0x08,
    Knowledge = 0x09,
    KnowledgeEdge = 0x0B,
    Profile = 0x0A,
    BTreeNode = 0x10,
    BTreeLeaf = 0x11,
    Free = 0x20,
    Overflow = 0xFF,
}

impl PageType {
    /// Convert to u16 for storage
    pub fn to_u16(&self) -> u16 {
        *self as u16
    }

    /// Convert from u16
    pub fn from_u16(value: u16) -> Option<PageType> {
        match value {
            0x01 => Some(PageType::Engram),
            0x02 => Some(PageType::Hyperedge),
            0x03 => Some(PageType::VectorMatrix),
            0x04 => Some(PageType::SparseIndex),
            0x05 => Some(PageType::Topic),
            0x06 => Some(PageType::TopicEdge),
            0x07 => Some(PageType::Archive),
            0x08 => Some(PageType::Crystal),
            0x09 => Some(PageType::Knowledge),
            0x0A => Some(PageType::Profile),
            0x0B => Some(PageType::KnowledgeEdge),
            0x10 => Some(PageType::BTreeNode),
            0x11 => Some(PageType::BTreeLeaf),
            0x20 => Some(PageType::Free),
            0xFF => Some(PageType::Overflow),
            _ => None,
        }
    }
}
