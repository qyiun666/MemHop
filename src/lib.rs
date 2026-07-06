// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! MemHop — agent-oriented memory database with L0-L6 cognitive architecture.

mod api;
pub mod config;
pub mod dream;
pub mod encoder;
pub(crate) mod file;
pub(crate) mod index;
pub mod l3;
pub(crate) mod layers;
pub mod organize;
pub mod query;
pub(crate) mod session;
pub(crate) mod shared;
pub(crate) mod util;

#[cfg(test)]
pub(crate) mod test_helpers;

pub mod prelude;

pub use api::MemHop;
pub use api::{MemHopError, Result};

// Backwards-compatible re-exports
pub use config::{LlmConfig, MemHopConfig, SearchWeights};
pub use layers::pathway::PathwayWeightSlot;
pub use shared::common::{format_hash, parse_id_to_hash};
pub use util::{hash_id, Layer, SourceMeta, SourceRef, SourceType};

pub use dream::llm::{CrystalDef, CrystalStep, LlmProvider, MemorySummary, Pattern};
#[cfg(feature = "llm")]
pub use dream::openai_compatible::OpenAICompatibleLlmProvider;
pub use dream::prune::DreamReport;
pub use organize::extract_keywords;
pub use query::batch::{BatchReport, EncodedItem, StoreBatch, StoreItem};

pub use query::types::{
    ActionItem, ActionType, Archive, ArchiveListResult, ArchivePageQuery, ArchiveRef,
    ContextResult, CrystalListQuery, CrystalListResult, CrystalSummary, EdgeListQuery,
    EdgeListResult, EngramListQuery, EngramListResult, EngramResult, ImportData, ImportError,
    ImportMode, ImportRequest, ImportResult, ImportStatus, KnowledgeDetail, KnowledgeImportItem,
    KnowledgeListQuery, KnowledgeListResult, KnowledgeNodeDetail, KnowledgeNodesResult,
    KnowledgeSummary, L3Detail, L3Preview, L4SearchQuery, L6Filter, MergeResult, NodeListQuery,
    NodeListResult, ProfileResult, RequestSource, SearchQuery, SearchResult, TargetLayer,
    TopicDetail, TopicImportItem, TopicListQuery, TopicListResult, TopicSummary, UpdateL2Fields,
    UpdateL3Fields, UpdateL5Fields, UpdateL6Fields, UpdateProfileRequest, UpdateRequest,
    UpdateResult, UpdateStatus,
};
