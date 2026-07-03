// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Diagnostics and health check types for MemHop.

use serde::{Deserialize, Serialize};

/// Status of a single dream pipeline stage
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum StageStatus {
    /// Stage completed successfully
    Success,
    /// Stage failed with an error
    Failed,
    /// Stage was skipped (e.g., no data to process)
    Skipped,
}

/// Report for a single stage in the dream pipeline
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct StageReport {
    /// Stage name (e.g., "l3_distill", "l2_compress", "l1_rebuild", "l1_decay", "l0_profile", "habit_distill", "l5_crystallize")
    pub name: String,
    /// Stage execution status
    pub status: StageStatus,
    /// Human-readable description of what the stage did
    pub description: String,
    /// Number of items processed (contexts, nodes, crystals, etc.)
    pub processed_count: usize,
    /// Stage execution time in milliseconds
    pub duration_ms: u64,
    /// Error message if status is Failed
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error: Option<String>,
}

/// Health check result for a MemHop database instance
// Reserved for upcoming diagnostics/health-check API.
#[allow(dead_code)]
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct HealthCheckResult {
    /// Overall health status
    pub healthy: bool,
    /// Database file path
    pub db_path: String,
    /// File size in bytes
    pub file_size: u64,
    /// Number of pages allocated
    pub page_count: u32,
    /// Number of free pages available
    pub free_pages: usize,
    /// Vector dimension configured
    pub vector_dim: usize,
    /// B-tree entry count
    pub btree_entries: usize,
    /// Sparse index document count
    pub sparse_doc_count: usize,
    /// Whether an encoder is configured
    pub encoder_configured: bool,
    /// Whether IVF index is built
    pub ivf_index_built: bool,
    /// Number of active session topics
    pub active_topics: usize,
    /// Any health issues found
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub issues: Vec<String>,
}

/// Statistics result for a MemHop database instance
// Reserved for upcoming diagnostics/health-check API.
#[allow(dead_code)]
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct StatsResult {
    /// Total number of contexts stored
    pub context_count: usize,
    /// Number of active contexts (depth 1, is_active)
    pub active_context_count: usize,
    /// Number of L3 hypergraphs
    pub l3_graph_count: usize,
    /// Number of L4 archives
    pub archive_count: usize,
    /// Number of L5 crystals
    pub crystal_count: usize,
    /// Number of L1 ContextNodes
    pub l1_node_count: usize,
    /// Number of L1 HyperedgeSlots
    pub l1_edge_count: usize,
    /// Average context depth
    pub avg_depth: f32,
    /// Average context importance
    pub avg_importance: f32,
    /// Total number of dialogue turns across all contexts
    pub total_turns: u64,
}
