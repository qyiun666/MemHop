// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! MemHop — agent-oriented memory database with L0-L6 cognitive architecture.

mod api;
pub mod config;
pub mod dream;
pub mod encoder;
pub(crate) mod index;
pub mod l3;
pub(crate) mod layers;
pub mod organize;
pub mod query;
pub(crate) mod session;
pub(crate) mod shared;
pub(crate) mod storage;
pub(crate) mod store;
pub(crate) mod util;

#[cfg(test)]
pub(crate) mod test_helpers;

pub mod prelude;

pub use api::MemHop;
pub use api::{MemHopError, Result};

// Backwards-compatible re-exports
pub use config::{LlmConfig, LlmPreprocessConfig, MemHopConfig, SearchWeights};
pub use shared::common::{format_hash, parse_id_to_hash};
pub use util::{hash_id, Layer, SourceMeta, SourceRef, SourceType};

pub use dream::llm::{
    ConsolidationInput, ConsolidationOutput, CrystalDef, CrystalStep, DreamSection, HabitAnalysis,
    L2Group, L3Extraction, LlmProvider, Section,
};
#[cfg(feature = "llm")]
pub use dream::llm_preprocess;
#[cfg(feature = "llm")]
pub use dream::openai_compatible::OpenAICompatibleLlmProvider;
pub use dream::prune::DreamReport;
pub use organize::extract_keywords;

pub use query::types::{
    ActionItem, ActionType, Archive, ArchiveListResult, ArchivePageQuery, ArchiveQuery, ArchiveRef,
    ContextResult, CrystalListQuery, CrystalListResult, CrystalSummary, DreamStage, EdgeKind,
    GraphEdge, GraphEdgeKind, GraphNode, GraphSlot, HealthStatus, ImportData, ImportError,
    ImportMode, ImportRequest, ImportResult, ImportStatus, KnowledgeDetail, KnowledgeImportItem,
    KnowledgeListQuery, KnowledgeListResult, KnowledgeNodeDetail, KnowledgeNodeQuery,
    KnowledgeNodesResult, KnowledgeSummary, L1Edge, L1Graph, L1Node, L3Detail, L3EntityHint,
    L3Preview, MergeResult, ProfileDelta, ProfileResult, RequestSource, SceneTreeResult,
    SearchFilters, SearchQuery, SearchResult, SessionStatus, StoreBatch, StoreItem, StoreResult,
    Subgraph, SubgraphEdge, SubgraphNode, TargetLayer, TopicDetail, TopicImportItem,
    TopicListQuery, TopicListResult, TopicSummary, TraversalHop, UpdateL2Fields, UpdateL3Fields,
    UpdateL5Fields, UpdateRequest, UpdateResult, UpdateStatus,
};

// ---------------------------------------------------------------------------
// Modular re-exports for meowAgent SDK integration
// ---------------------------------------------------------------------------

pub mod search {
    pub use crate::query::types::{ContextResult, SearchFilters, SearchQuery, SearchResult};
}
pub mod profile {
    pub use crate::query::types::ProfileResult;
}
pub mod update {
    pub use crate::query::types::{UpdateRequest, UpdateResult, UpdateStatus};
}
pub mod store_mod {
    pub use crate::query::types::{StoreBatch, StoreItem, StoreResult};
}
pub mod l2 {
    pub use crate::query::types::{
        MergeResult, SceneTreeResult, TopicDetail, TopicListQuery, TopicListResult, TopicSummary,
        UpdateL2Fields,
    };
}
pub mod l4 {
    pub use crate::query::types::{Archive, ArchiveListResult, ArchivePageQuery, ArchiveQuery};
}
pub mod l5 {
    pub use crate::query::types::{
        CrystalListQuery, CrystalListResult, CrystalSummary, UpdateL5Fields,
    };
}
pub mod l1 {
    pub use crate::query::types::{L1Edge, L1Graph, L1Node};
}
pub mod diagnostics {
    pub use crate::query::types::HealthStatus;
}
pub mod session_mod {
    pub use crate::query::types::SessionStatus;
}
pub mod import {
    pub use crate::query::types::{
        ImportData, ImportMode, ImportRequest, ImportResult, KnowledgeImportItem, TargetLayer,
        TopicImportItem,
    };
}
pub mod l3_types {
    pub use crate::query::types::{
        EdgeKind, GraphEdge, GraphEdgeKind, GraphNode, GraphSlot, L3Detail, L3Preview, Subgraph,
        SubgraphEdge, SubgraphNode, TraversalHop, UpdateL3Fields,
    };
}
