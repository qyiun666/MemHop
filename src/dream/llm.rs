// LLM Provider trait for dream consolidation
use crate::MemHopError;
use serde::{Deserialize, Serialize};

/// Trait for LLM providers used in dream consolidation
pub trait LlmProvider: Send + Sync {
    /// Summarize a collection of texts into a concise summary
    ///
    /// # Arguments
    /// * `texts` - Collection of text strings to summarize
    ///
    /// # Returns
    /// A summarized text string
    fn summarize(&self, texts: &[String]) -> Result<String, MemHopError>;

    /// Extract patterns from memory summaries
    ///
    /// # Arguments
    /// * `memories` - Collection of memory summaries with keywords and timestamps
    ///
    /// # Returns
    /// Vector of extracted patterns with frequency and confidence scores
    fn extract_patterns(&self, memories: &[MemorySummary]) -> Result<Vec<Pattern>, MemHopError>;

    /// Generate a Crystal definition from a pattern
    ///
    /// # Arguments
    /// * `pattern` - The pattern to crystallize into programmatic knowledge
    ///
    /// # Returns
    /// CrystalDef with condition (DSL format), action, and confidence
    fn generate_crystal(&self, pattern: &Pattern) -> Result<CrystalDef, MemHopError>;

    /// Fallback summarization using keyword frequency when LLM is unavailable
    ///
    /// # Arguments
    /// * `texts` - Collection of text strings
    ///
    /// # Returns
    /// Comma-separated top keywords
    fn fallback_summarize(&self, texts: &[String]) -> String;

    /// Fallback pattern extraction using keyword intersection when LLM is unavailable
    ///
    /// # Arguments
    /// * `memories` - Collection of memory summaries
    ///
    /// # Returns
    /// Vector of patterns based on common keywords
    fn fallback_extract_patterns(&self, memories: &[MemorySummary]) -> Vec<Pattern>;

    /// Fallback crystal generation using regex pattern matching when LLM is unavailable
    ///
    /// # Arguments
    /// * `pattern` - The pattern to convert to crystal
    ///
    /// # Returns
    /// CrystalDef with reduced confidence
    fn fallback_generate_crystal(&self, pattern: &Pattern) -> CrystalDef;

    /// Analyze user language habits from dialogue history
    ///
    /// Extracts: personal lexicon (unique word meanings), communication style traits,
    /// and emotional expression patterns.
    ///
    /// # Arguments
    /// * `dialogues` - Recent dialogue texts from L4 archives
    ///
    /// # Returns
    /// HabitAnalysis with lexicon, style traits, and emotion patterns
    fn analyze_user_habits(&self, dialogues: &[String]) -> Result<HabitAnalysis, MemHopError>;

    /// Fallback habit analysis using word frequency when LLM is unavailable
    fn fallback_analyze_user_habits(&self, dialogues: &[String]) -> HabitAnalysis;

    /// Distill structured concepts and relations from a summary.
    ///
    /// # Arguments
    /// * `summary` - A compressed memory summary to analyze
    ///
    /// # Returns
    /// `LlmDistillResult` containing extracted concepts and relations
    fn distill_concepts(&self, summary: &str) -> Result<LlmDistillResult, MemHopError>;

    /// Fallback concept distillation returning an empty result.
    ///
    /// # Arguments
    /// * `summary` - A compressed memory summary to analyze
    ///
    /// # Returns
    /// Empty `LlmDistillResult`
    fn fallback_distill_concepts(&self, summary: &str) -> LlmDistillResult;
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
///
/// Produced by Dream Stage 3.5 from dialogue history analysis.
#[derive(Debug, Clone, Default)]
pub struct HabitAnalysis {
    /// User-specific vocabulary: word/expression → meaning
    pub lexicon: std::collections::HashMap<String, String>,
    /// Communication style trait tags
    pub style_traits: Vec<String>,
    /// Emotional expression patterns: expression → true meaning
    pub emotion_patterns: std::collections::HashMap<String, String>,
}
