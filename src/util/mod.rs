// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

pub(crate) mod hash;

pub use hash::hash_id;

use serde::{Deserialize, Serialize};
use std::fmt;

pub const PAGE_SIZE: usize = 4096;

pub const MAGIC: [u8; 4] = [0x4D, 0x45, 0x48, 0x21]; // "MEH!"
pub const TAIL_MAGIC: [u8; 4] = [0xDE, 0xAD, 0xBE, 0xEF];
pub const VERSION: u16 = 0x0025;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum Layer {
    Profile,     // L0: Agent identity
    ContextNode, // L1: Hypergraph skeleton node
    Hyperedge,   // L1: Hypergraph skeleton edge
    Context,     // L2: Scene-based conversation context
    Hypergraph,  // L3: Generic hypergraph engine
    Archive,     // L4: Raw text + file paths
    ActionChain, // L5: Ordered action sequences
    Procedural,  // L6: Pathway weight (procedural memory)
}

impl Layer {
    pub fn to_u8(&self) -> u8 {
        match self {
            Layer::Profile => 0,
            Layer::ContextNode => 1,
            Layer::Hyperedge => 2,
            Layer::Context => 3,
            Layer::Hypergraph => 4,
            Layer::Archive => 5,
            Layer::ActionChain => 6,
            Layer::Procedural => 7,
        }
    }

    pub fn from_u8(value: u8) -> Option<Layer> {
        match value {
            0 => Some(Layer::Profile),
            1 => Some(Layer::ContextNode),
            2 => Some(Layer::Hyperedge),
            3 => Some(Layer::Context),
            4 => Some(Layer::Hypergraph),
            5 => Some(Layer::Archive),
            6 => Some(Layer::ActionChain),
            7 => Some(Layer::Procedural),
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
            Layer::Procedural => write!(f, "Procedural"),
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[repr(u8)]
pub enum SourceType {
    UserInput = 0,
    SystemGenerated = 1,
    ExternalAPI = 2,
    FileImport = 3,
}

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

#[inline]
pub fn get_current_timestamp() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as i64
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct SourceRef {
    pub uri: String,
    pub offset: Option<u64>,
    pub length: Option<u64>,
}
