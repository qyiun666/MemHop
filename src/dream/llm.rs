// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! LLM Provider trait for dream consolidation.

use crate::MemHopError;
use serde::{Deserialize, Serialize};

/// Structured compression result with all LLM-extracted fields.
#[derive(Debug, Clone)]
pub struct CompressedSummary {
    /// Core topic keywords (space-separated, 2-5 keywords)
    pub theme: String,
    /// Compressed short title (≤20 chars)
    pub title: String,
    /// Key information points (3-8 items)
    pub key_points: Vec<String>,
    /// Keyword-dense summary paragraph (100-200 chars)
    pub summary: String,
}

/// Trait for LLM providers used in dream consolidation
pub trait LlmProvider: Send + Sync {
    /// Summarize a collection of texts into a structured compressed summary.
    fn summarize(&self, texts: &[String]) -> Result<CompressedSummary, MemHopError>;

    /// Extract patterns from memory summaries
    fn extract_patterns(&self, memories: &[MemorySummary]) -> Result<Vec<Pattern>, MemHopError>;

    /// Generate a Crystal definition from a pattern
    fn generate_crystal(&self, pattern: &Pattern) -> Result<CrystalDef, MemHopError>;

    /// Fallback summarization using keyword frequency when LLM is unavailable
    fn fallback_summarize(&self, texts: &[String]) -> CompressedSummary;

    /// Fallback pattern extraction using keyword intersection when LLM is unavailable
    fn fallback_extract_patterns(&self, memories: &[MemorySummary]) -> Vec<Pattern>;

    /// Fallback crystal generation using regex pattern matching when LLM is unavailable
    fn fallback_generate_crystal(&self, pattern: &Pattern) -> CrystalDef;

    /// Analyze user language habits from dialogue history
    fn analyze_user_habits(&self, dialogues: &[String]) -> Result<HabitAnalysis, MemHopError>;

    /// Fallback habit analysis using word frequency when LLM is unavailable
    fn fallback_analyze_user_habits(&self, dialogues: &[String]) -> HabitAnalysis;

    /// Distill structured concepts and relations from a summary.
    fn distill_concepts(&self, summary: &str) -> Result<LlmDistillResult, MemHopError>;

    /// Fallback concept distillation returning an empty result.
    fn fallback_distill_concepts(&self, summary: &str) -> LlmDistillResult;

    /// Check whether two adjacent conversation summaries describe the same topic.
    /// Returns `true` if they should be merged into one parent node.
    fn check_same_topic(&self, summary_a: &str, summary_b: &str) -> Result<bool, MemHopError>;

    /// Merge multiple adjacent-conversation texts into a single (title, summary) pair.
    fn merge_summarize(&self, texts: &[String]) -> Result<(String, String), MemHopError>;

    /// Compress dialogue text for retrieval: extract keywords + short summary.
    /// `role` distinguishes "用户提问" vs "助手回复" for prompt tuning.
    fn compress_for_retrieval(&self, text: &str, role: &str) -> Result<String, MemHopError> {
        // Default: return original text unchanged
        let _ = role;
        Ok(text.to_string())
    }
}

/// Summary of a memory for pattern extraction
#[derive(Debug, Clone)]
pub struct MemorySummary {
    /// Original text content
    pub text: String,
    /// Extracted keywords
    pub keywords: Vec<String>,
    /// Timestamp when the memory was created (milliseconds since epoch)
    pub timestamp: i64,
}

/// Pattern extracted from multiple memories
#[derive(Debug, Clone)]
pub struct Pattern {
    /// Human-readable description of the pattern
    pub description: String,
    /// Number of times this pattern appeared
    pub frequency: u32,
    /// Confidence score (0.0 - 1.0)
    pub confidence: f32,
}

/// A single executable step inside a crystal (L5)
#[derive(Debug, Clone)]
pub struct CrystalStep {
    /// Action description for this step
    pub action: String,
    /// Optional JSON parameters for this step
    pub parameters: Option<String>,
}

/// Crystal definition for programmatic knowledge (L5)
#[derive(Debug, Clone)]
pub struct CrystalDef {
    /// Condition in DSL format that triggers the crystal
    pub condition: String,
    /// Overall action to execute when condition is met
    pub action: String,
    /// Ordered list of concrete steps to execute
    pub steps: Vec<CrystalStep>,
    /// Confidence score (0.0 - 1.0)
    pub confidence: f32,
}

/// Concept extracted from a summary during L3 knowledge distillation
#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct LlmConcept {
    /// Concept name
    pub name: String,
    /// Concept node type (e.g. "concept", "entity", "skill")
    #[serde(rename = "type")]
    pub node_type: String,
    /// Human-readable description
    pub description: String,
    /// Associated keywords
    #[serde(default)]
    pub keywords: Vec<String>,
}

/// Relation between two concepts extracted during L3 knowledge distillation
#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct LlmRelation {
    /// Source concept name
    pub from: String,
    /// Target concept name
    pub to: String,
    /// Relation kind (e.g. "Related", "Causal", "PartOf", "Sequence", "Dependency")
    #[serde(default = "default_relation_kind")]
    pub kind: String,
}

fn default_relation_kind() -> String {
    "Dependency".to_string()
}

/// Result of LLM-based L3 knowledge distillation
#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct LlmDistillResult {
    /// Extracted concepts
    #[serde(default)]
    pub concepts: Vec<LlmConcept>,
    /// Extracted relations between concepts
    #[serde(default)]
    pub relations: Vec<LlmRelation>,
}

/// User language habit analysis result
#[derive(Debug, Clone, Default)]
pub struct HabitAnalysis {
    /// User-specific vocabulary: word/expression → meaning
    pub lexicon: std::collections::HashMap<String, String>,
    /// Communication style trait tags
    pub style_traits: Vec<String>,
    /// Emotional expression patterns: expression → true meaning
    pub emotion_patterns: std::collections::HashMap<String, String>,
}
