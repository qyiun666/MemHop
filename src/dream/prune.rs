// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

use crate::query::diagnostics::StageReport;
use serde::{Deserialize, Serialize};

/// Report of dream operation
///
/// Simplified public report for agent consumption.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct DreamReport {
    /// Total number of memory items consolidated
    pub consolidated_count: u32,
    /// IDs of any new skills/crystals created
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub new_skills: Option<Vec<String>>,
    /// Which layers were compressed (e.g., [2, 3, 5])
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub compressed_layers: Option<Vec<u8>>,
    /// Number of new L3 knowledge nodes created
    pub new_l3_nodes: u32,
    /// Number of new L5 crystals created
    pub new_crystals: u32,
    /// Number of L5 crystals pruned
    pub pruned_crystals: u32,
    /// L1 nodes whose importance was decayed/updated
    pub l1_decayed_nodes: u32,
    /// Edge pointers pruned from ContextNodes
    pub l1_pruned_edges: u32,
    /// L1 ContextNodes removed due to low importance
    pub l1_removed_nodes: u32,
    /// HyperedgeSlots removed due to low weight or underpopulation
    pub l1_removed_edges: u32,
    /// Per-stage execution reports
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub stages: Vec<StageReport>,
}

/// Result of demoting a depth-1 context to depth-2
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct DemotionResult {
    /// Original context ID (hex)
    pub context_id: String,
    /// Original title
    pub original_title: String,
    /// Compressed summary generated
    pub compressed_summary: String,
    /// New depth after demotion
    pub new_depth: u8,
}

/// Result of compressing a depth-1 context into a new context
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CompressResult {
    /// New compressed context ID (hex)
    pub new_context_id: String,
    /// Source context ID that was compressed
    pub source_context_id: String,
    /// Compressed summary
    pub new_summary: String,
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_dream_report_structure() {
        let report = DreamReport {
            consolidated_count: 10,
            new_skills: None,
            compressed_layers: Some(vec![2, 3, 5]),
            new_l3_nodes: 1,
            new_crystals: 1,
            pruned_crystals: 1,
            l1_decayed_nodes: 5,
            l1_pruned_edges: 3,
            l1_removed_nodes: 2,
            l1_removed_edges: 1,
            stages: vec![],
        };

        assert_eq!(report.consolidated_count, 10);
        assert_eq!(report.pruned_crystals, 1);
        assert!(report.stages.is_empty());
    }
}
