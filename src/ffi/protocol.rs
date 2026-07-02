// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! FFI protocol types — 13 serde-tagged commands mapping to API.md interfaces.

use serde::{Deserialize, Serialize};
use serde_json::Value;

/// Top-level FFI command — serde(tag = "command") dispatches by action
#[derive(Debug, Deserialize)]
#[serde(tag = "command")]
pub enum FfiCommand {
    /// Search memory (Interface 2)
    #[serde(rename = "search")]
    Search {
        #[serde(flatten)]
        params: crate::query::types::SearchQuery,
    },
    /// Update memory (Interface 3)
    #[serde(rename = "update")]
    Update {
        #[serde(flatten)]
        params: crate::query::types::UpdateRequest,
    },
    /// Unified query (Interfaces 5-12 merged)
    #[serde(rename = "query_layer")]
    QueryLayer {
        /// Target: "l0" | "l1" | "l2" | "l3" | "l4" | "l5"
        layer: String,
        /// Action: "get" | "list"
        action: String,
        /// Get-by-ID params
        #[serde(default)]
        get: QueryGetParams,
        /// List params
        #[serde(default)]
        list: QueryListParams,
    },
    /// Unified title update (Interfaces 13-16 merged)
    #[serde(rename = "update_title")]
    UpdateTitle {
        /// Target: "l0" | "l2" | "l3" | "l5"
        layer: String,
        #[serde(default)]
        params: UpdateTitleParams,
    },
    /// Dream consolidation (Interface 4)
    #[serde(rename = "dream")]
    Dream {
        #[serde(flatten)]
        llm: crate::config::LlmConfig,
    },
    /// Merge L2 topics (Interface 18)
    #[serde(rename = "merge_topics")]
    MergeTopics {
        primary_id: String,
        secondary_ids: Vec<String>,
    },
    /// Import memory (Interface 19, with sub-action for build_l3)
    #[serde(rename = "import")]
    Import {
        #[serde(default)]
        params: ImportImportParams,
    },
    /// Session management (Interface 20)
    #[serde(rename = "session")]
    Session {
        #[serde(default)]
        params: SessionParams,
    },
    /// Batch store (Interface 21)
    #[serde(rename = "batch_store")]
    BatchStore {
        #[serde(flatten)]
        batch: crate::query::batch::StoreBatch,
    },
    /// Graph traversal query (L3 hypergraph)
    #[serde(rename = "graph_query")]
    GraphQuery {
        graph_id: String,
        start_node: String,
        max_depth: usize,
        #[serde(default)]
        edge_kinds: Option<Vec<String>>,
    },
    /// Delete a record by layer and id
    #[serde(rename = "delete")]
    Delete { layer: String, id: String },
    /// Sync to disk
    #[serde(rename = "sync")]
    Sync,
    /// Close database
    #[serde(rename = "close")]
    Close,
    /// L3 isolated node detection
    #[serde(rename = "l3_isolated")]
    L3Isolated {
        graph_id: String,
        #[serde(default)]
        threshold: Option<u32>,
    },
    /// L3 community detection
    #[serde(rename = "l3_community")]
    L3Community {
        graph_id: String,
        #[serde(default)]
        max_hyperedge_size: Option<usize>,
    },
    /// L3 DSL graph query
    #[serde(rename = "l3_query")]
    L3Query {
        graph_id: String,
        query: String,
        #[serde(default = "default_page")]
        page: usize,
    },
}

/// Get-by-ID parameters for query_layer
#[derive(Debug, Default, Deserialize)]
pub struct QueryGetParams {
    pub id: Option<String>,
    /// Batch IDs for L3 node retrieval
    #[serde(default)]
    pub ids: Option<Vec<String>>,
    /// Whether to include full text content (default: true)
    #[serde(default = "default_include_text")]
    pub include_text: bool,
}

fn default_include_text() -> bool {
    true
}

/// List parameters for query_layer — all fields optional
#[derive(Debug, Default, Deserialize)]
pub struct QueryListParams {
    pub page: Option<usize>,
    pub page_size: Option<usize>,
    pub keyword: Option<String>,

    // L1
    pub state_filter: Option<String>,
    pub min_importance: Option<f32>,

    // L2
    pub active_only: Option<bool>,

    // L3
    pub domain_filter: Option<String>,
    pub knowledge_type: Option<String>,

    // L4 (archive)
    pub start_time: Option<i64>,
    pub end_time: Option<i64>,
    pub content_type: Option<String>,
    pub topic_id: Option<String>,
    pub node_ids: Option<Vec<String>>,

    // L5
    pub status_filter: Option<String>,
    pub min_trigger_count: Option<u32>,
}

/// Update title parameters — shared for all layers
#[derive(Debug, Default, Deserialize)]
pub struct UpdateTitleParams {
    // For L2/L3/L5
    pub id: Option<String>,
    pub new_title: Option<String>,

    // For L2: L3 knowledge node references to associate with this topic
    #[serde(default)]
    pub l3_refs: Option<Vec<String>>,

    // Override layer for dispatch (e.g. "topic" → "l2")
    #[serde(default)]
    pub layer: Option<String>,

    // For L0 (profile update)
    pub name: Option<String>,
    pub role: Option<String>,
    pub personality: Option<String>,
    pub worldview: Option<String>,
    pub preferences: Option<std::collections::HashMap<String, String>>,

    // For L0 (user language habits)
    pub lexicon: Option<std::collections::HashMap<String, String>>,
    pub style_traits: Option<Vec<String>>,
    pub emotion_patterns: Option<std::collections::HashMap<String, String>>,
}

/// Import sub-actions
#[derive(Debug, Default, Deserialize)]
pub struct ImportImportParams {
    /// "import" or "build_l3"
    #[serde(default = "default_import_action")]
    pub action: String,
    pub target_layer: Option<String>,
    pub mode: Option<String>,
    pub knowledge_title: Option<String>,
    pub data: Option<Value>,
    /// For build_l3
    pub path: Option<String>,
}

fn default_import_action() -> String {
    "import".to_string()
}

fn default_page() -> usize {
    1
}

/// Session management sub-actions
#[derive(Debug, Default, Deserialize)]
pub struct SessionParams {
    /// "activate" | "deactivate" | "list" | "adjust"
    pub action: Option<String>,
    pub topic_id: Option<String>,
    pub ttl_ms: Option<i64>,
    pub delta: Option<f32>,
}

/// Unified FFI response
#[derive(Debug, Serialize)]
pub struct FfiResponse {
    pub success: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub data: Option<Value>,
}

impl FfiResponse {
    pub fn ok(data: Value) -> Self {
        FfiResponse {
            success: true,
            error: None,
            data: Some(data),
        }
    }

    pub fn err(msg: impl Into<String>) -> Self {
        FfiResponse {
            success: false,
            error: Some(msg.into()),
            data: None,
        }
    }
}
