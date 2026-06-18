// Dream consolidation pipeline (prune module)
use crate::dream::llm::LlmProvider;
use crate::file::header::FileHeader;
use crate::index::btree::BTreeIndex;
use crate::index::sparse::SparseIndex;
use crate::MemHopError;
use memmap2::MmapMut;
use serde::{Deserialize, Serialize};
use std::collections::HashSet;

/// Report of dream operation
///
/// Captures all changes made during the dream pipeline:
/// - L2 depth demotion (主→次, 次→次次, 次次→移除)
/// - L1 rebuild based on updated L2
/// - L0 profile update based on L1
/// - L3 knowledge distillation from active L2 contexts
/// - L5 crystallization from all ActionChainSlots
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct DreamReport {
    /// Contexts demoted from depth 1 → depth 2 (with compressed summary)
    pub demoted_to_secondary: Vec<DemotionResult>,
    /// Contexts demoted from depth 2 → depth 3
    pub demoted_to_tertiary: Vec<String>,
    /// Contexts removed (depth 3 → gone)
    pub removed_contexts: Vec<String>,
    /// New compressed contexts created from demoted depth-1 nodes
    pub new_compressed: Vec<CompressResult>,
    /// L1 nodes updated based on L2 changes
    pub l1_updated: Vec<String>,
    /// Number of L1 nodes whose importance was decayed/updated
    pub l1_decayed_nodes: usize,
    /// Number of edge pointers pruned from ContextNodes
    pub l1_pruned_edges: usize,
    /// Number of L1 ContextNodes removed due to low importance
    pub l1_removed_nodes: usize,
    /// Number of HyperedgeSlots removed due to low weight or underpopulation
    pub l1_removed_edges: usize,
    /// L0 profile updated: (profile_id, updated_fields)
    pub l0_updated: Option<(String, Vec<String>)>,
    /// User language habits updated from dialogue analysis
    pub habits_updated: Option<crate::dream::habit_distill_stage::HabitUpdate>,
    /// New L3 nodes created via LLM-based knowledge distillation
    pub new_l3_nodes: Vec<String>,
    /// New crystals created from L5 crystallization
    pub new_crystals: Vec<String>,
    /// Low-quality crystals pruned
    pub pruned_crystals: Vec<String>,
    /// Total execution time in milliseconds
    pub duration_ms: u64,
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

/// Run dream consolidation pipeline
/// Scans active L2 contexts and performs depth demotion + compression + crystallization
pub fn dream_consolidation(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    llm: &dyn LlmProvider,
    session_topic_ids: HashSet<u64>,
) -> Result<DreamReport, MemHopError> {
    // Delegate to main orchestration function
    crate::dream::dream_pipeline(mmap, header, btree, sparse_index, llm, session_topic_ids)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_dream_report_structure() {
        let report = DreamReport {
            demoted_to_secondary: vec![DemotionResult {
                context_id: "ctx-1".to_string(),
                original_title: "Rust dev".to_string(),
                compressed_summary: "Rust development summary".to_string(),
                new_depth: 2,
            }],
            demoted_to_tertiary: vec!["ctx-2".to_string()],
            removed_contexts: vec!["ctx-3".to_string()],
            new_compressed: vec![CompressResult {
                new_context_id: "ctx-new-1".to_string(),
                source_context_id: "ctx-1".to_string(),
                new_summary: "compressed".to_string(),
            }],
            l1_updated: vec!["node-1".to_string()],
            l1_decayed_nodes: 0,
            l1_pruned_edges: 0,
            l1_removed_nodes: 0,
            l1_removed_edges: 0,
            l0_updated: Some(("profile-1".to_string(), vec!["personality".to_string()])),
            habits_updated: None,
            new_l3_nodes: vec!["l3-node-1".to_string()],
            new_crystals: vec!["crystal-1".to_string()],
            pruned_crystals: vec!["crystal-old".to_string()],
            duration_ms: 500,
        };

        assert_eq!(report.demoted_to_secondary.len(), 1);
        assert_eq!(report.removed_contexts.len(), 1);
        assert_eq!(report.new_crystals.len(), 1);
        assert_eq!(report.duration_ms, 500);
    }
}
