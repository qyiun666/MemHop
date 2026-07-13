// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! LLM Provider trait for consolidated dream consolidation.
//!
//! Single-phase design: one consolidate call processes all dream stages.

use crate::MemHopError;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;

// ============================================================================
// Input structures — all data needed for a dream cycle
// ============================================================================

#[derive(Debug, Clone)]
pub struct ConsolidationInput {
    /// L2 contexts grouped by scene, depth-1 nodes sorted by created_at
    pub scenes: Vec<SceneData>,
    /// Recent L4 archive dialogue texts for habit analysis (up to 30)
    pub recent_dialogues: Vec<String>,
    /// Existing L5 action chains for crystal generation (up to 20)
    pub existing_chains: Vec<ChainData>,
}

#[derive(Debug, Clone)]
pub struct SceneData {
    pub scene_id: u64,
    pub nodes: Vec<L2NodeData>,
}

/// Per-node data sent to the LLM for consolidation (depth-1 only).
#[derive(Debug, Clone)]
pub struct L2NodeData {
    pub id_hash: u64,
    pub created_at: i64,
    pub depth: u8,
    /// User-turn keywords
    pub user_keywords: Vec<String>,
    /// Agent-turn keywords
    pub agent_keywords: Vec<String>,
    /// Fused keywords (from prior compression, if depth > 1)
    pub fused_keywords: Vec<String>,
    /// Compressed summary (if available)
    pub fused_summary: Option<String>,
    /// Existing children ids (for understanding topic hierarchy)
    pub children_ids: Vec<u64>,
}

#[derive(Debug, Clone)]
pub struct ChainData {
    pub title: String,
    pub trigger: String,
    pub trigger_count: u32,
    pub confidence: f32,
}

// ============================================================================
// Output structures — returned by the LLM
// ============================================================================

/// Consolidated output; each field is independently valid or failed.
#[derive(Debug, Clone)]
pub struct ConsolidationOutput {
    pub l2_groups: Section<Vec<L2Group>>,
    pub l3_extractions: Section<Vec<L3Extraction>>,
    pub habits: Section<HabitAnalysis>,
    pub crystals: Section<Vec<CrystalDef>>,
}

/// Tagged section result.
#[derive(Debug, Clone)]
pub enum Section<T> {
    Valid(T),
    Empty,               // no data to process
    ParseFailed(String), // LLM returned unparseable content
}

impl<T> Section<T> {
    /// Returns true for Valid or Empty — only ParseFailed is considered a failure.
    pub fn is_ok(&self) -> bool {
        matches!(self, Section::Valid(_) | Section::Empty)
    }

    /// Returns true only if this section needs a retry (LLM returned unparseable content).
    pub fn needs_retry(&self) -> bool {
        matches!(self, Section::ParseFailed(_))
    }
}

/// Which sections to reprocess in the second call.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum DreamSection {
    L2Groups,
    L3Distill,
    Habits,
    Crystals,
}

// ----- L2 merge output -----

#[derive(Debug, Clone)]
pub struct L2Group {
    pub scene_id: u64,
    /// id_hashes of nodes to merge (must be depth-1, same scene, consecutive)
    pub node_hashes: Vec<u64>,
    /// LLM-generated title for the new merged parent
    pub merged_title: String,
    /// LLM-generated merged summary
    pub merged_summary: String,
}

// ----- L3 knowledge extraction output -----

#[derive(Debug, Clone)]
pub struct L3Extraction {
    /// Which L2 context this extraction came from
    pub context_id: u64,
    pub concepts: Vec<LlmConcept>,
    pub relations: Vec<LlmRelation>,
}

#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct LlmConcept {
    pub name: String,
    #[serde(rename = "type")]
    pub node_type: String,
    pub description: String,
    #[serde(default)]
    pub keywords: Vec<String>,
}

#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct LlmRelation {
    pub from: String,
    pub to: String,
    #[serde(default = "default_relation_kind")]
    pub kind: String,
}

fn default_relation_kind() -> String {
    "Related".to_string()
}

// ----- Habit analysis -----

#[derive(Debug, Clone, Default)]
pub struct HabitAnalysis {
    pub lexicon: HashMap<String, String>,
    pub style_traits: Vec<String>,
    pub emotion_patterns: HashMap<String, String>,
}

// ----- Crystal definitions -----

#[derive(Debug, Clone)]
pub struct CrystalDef {
    pub condition: String,
    pub action: String,
    pub steps: Vec<CrystalStep>,
    pub confidence: f32,
}

#[derive(Debug, Clone)]
pub struct CrystalStep {
    pub action: String,
    pub parameters: Option<String>,
}

// ============================================================================
// Trait
// ============================================================================

pub trait LlmProvider: Send + Sync {
    /// Monolithic consolidation of all dream stages into a single LLM call.
    fn consolidate(&self, input: &ConsolidationInput) -> Result<ConsolidationOutput, MemHopError>;

    /// Generic chat completion for lightweight LLM tasks (preprocess, keyword extraction, etc.).
    /// Default implementation returns an error — providers must override to enable this feature.
    #[allow(clippy::too_many_arguments)]
    fn chat(
        &self,
        _system: &str,
        _user: &str,
        _max_tokens: u32,
        _temperature: f32,
        _top_p: f32,
        _presence_penalty: f32,
        _frequency_penalty: f32,
    ) -> Result<String, MemHopError> {
        Err(MemHopError::ConfigError(
            "chat() not implemented for this LlmProvider".into(),
        ))
    }
}

// ============================================================================
// Legacy type — kept for internal stage helpers
// ============================================================================

/// Result of LLM-based L3 knowledge distillation (used internally by stage helpers).
#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct LlmDistillResult {
    #[serde(default)]
    pub concepts: Vec<LlmConcept>,
    #[serde(default)]
    pub relations: Vec<LlmRelation>,
}
