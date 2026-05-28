//! LLM Provider abstraction for optional Dream-layer enhancement.
//!
//! MemHop's core `store` / `recall` paths are LLM-free by design (zero-model,
//! deterministic, n-gram based). The `LlmProvider` trait below allows agent
//! integrators to inject an external LLM so the Dream consolidation layer can
//! optionally perform higher-order reasoning: memory merging, contradiction
//! detection, importance scoring, summarisation.
//!
//! All trait methods are object-safe so a `Box<dyn LlmProvider>` can be held
//! by the engine. Implementations must be `Send + Sync` to remain compatible
//! with the cross-thread Dream scheduler.

#![allow(dead_code)]

use crate::error::Result;

/// Pluggable Large Language Model provider used exclusively by Dream-layer
/// enhancements.
///
/// The trait provides a single synchronous method `generate` for prompt
/// completions. The method returns [`MemHopError`] so backend failures (HTTP, quota, parse,
/// etc.) propagate through the engine's normal error pipeline.
///
/// ## Object-safety
/// All methods take `&self` and use only sized parameters / owned returns,
/// keeping the trait object-safe (`Box<dyn LlmProvider>` works).
///
/// ## Example
/// ```ignore
/// struct EchoLlm;
/// impl memhop::LlmProvider for EchoLlm {
///     fn generate(&self, prompt: &str, _max_tokens: usize)
///         -> Result<String, memhop::MemHopError> { Ok(prompt.to_string()) }
/// }
/// ```
pub trait LlmProvider: Send + Sync {
    /// Generate a text completion for `prompt`, capped at `max_tokens` output
    /// tokens. Implementations should treat `max_tokens` as an upper bound and
    /// may return shorter strings on early stop.
    fn generate(&self, prompt: &str, max_tokens: usize) -> Result<String>;
}

/// Built-in Dream-layer prompt templates.
///
/// Templates are written in English for cross-language portability and to
/// match the lingua franca of most modern LLMs. Each template returns a
/// fully-formed prompt string ready to be passed to
/// [`LlmProvider::generate`].
///
/// The templates intentionally request structured, machine-friendly output
/// (numeric scores, single-line verdicts, plain summaries) so callers can
/// parse the LLM response without additional NLP.
pub struct PromptTemplates;

impl PromptTemplates {
    /// Build a prompt that asks the LLM to merge several similar memories
    /// into a single, non-redundant statement preserving every concrete fact.
    ///
    /// `memories` is the slice of memory texts to merge. Empty slices yield a
    /// prompt that still produces an empty/no-op response (caller should
    /// short-circuit before calling this when `memories.is_empty()`).
    pub fn memory_merge(memories: &[&str]) -> String {
        let mut buf = String::with_capacity(256 + memories.iter().map(|m| m.len() + 8).sum::<usize>());
        buf.push_str(
            "You are a memory consolidation assistant. Merge the following \
             related memories into ONE concise statement that preserves every \
             concrete fact and removes redundancy. Do not invent details. \
             Reply with the merged memory only, no preamble.\n\nMemories:\n",
        );
        for (i, m) in memories.iter().enumerate() {
            buf.push_str(&format!("{}. {}\n", i + 1, m));
        }
        buf.push_str("\nMerged memory:");
        buf
    }

    /// Build a prompt that asks the LLM whether two memories are
    /// contradictory.
    ///
    /// The expected response format is a single token: `YES` or `NO`,
    /// optionally followed by a one-line justification. Callers should parse
    /// the first token case-insensitively.
    pub fn conflict_detect(memory_a: &str, memory_b: &str) -> String {
        format!(
            "You are a memory consistency checker. Decide whether the two \
             memories below contradict each other on any factual point. \
             Reply with exactly one of: YES or NO on the first line, then an \
             optional one-line reason.

Memory A: {}
Memory B: {}

Verdict:",
            memory_a, memory_b
        )
    }

    /// Build a prompt that asks the LLM to score the long-term importance of
    /// `memory` given surrounding `context`, returning a float in `[0.0, 1.0]`.
    ///
    /// The expected response is a bare decimal number on the first line. The
    /// caller is responsible for parsing and clamping.
    pub fn importance_score(memory: &str, context: &str) -> String {
        format!(
            "You are a memory importance estimator. Given the memory and its \
             surrounding context, output a single decimal number in the range \
             [0.0, 1.0] estimating long-term importance: 0.0 = trivial / \
             ephemeral, 1.0 = critical / must-retain. Reply with the number \
             only on the first line.

Context: {}
Memory: {}

Score:",
            context, memory
        )
    }

    /// Build a prompt that asks the LLM to compress `memory` into a short,
    /// faithful summary (one or two sentences). Used by Dream when long
    /// memories should be archived in a denser form.
    pub fn summarize(memory: &str) -> String {
        format!(
            "Summarize the following memory in one or two sentences. Keep all \
             concrete facts, drop filler. Reply with the summary only.\n\n\
             Memory: {}\n\nSummary:",
            memory
        )
    }
}

/// Ask an LLM to suggest 3-5 keywords / topics for a memory text.
pub fn llm_suggest_keywords(llm: &dyn LlmProvider, text: &str) -> Result<Vec<String>> {
    let prompt = format!(
        "Extract 3-5 key topics or keywords from the following text. \
         Return them as a comma-separated list, nothing else.\n\nText: {}",
        text
    );
    let response = llm.generate(&prompt, 100)?;
    let keywords: Vec<String> = response
        .split(',')
        .map(|s| s.trim().trim_matches('"').trim_matches('\'').to_string())
        .filter(|s| !s.is_empty())
        .collect();
    Ok(keywords)
}

/// Ask an LLM whether two texts contradict each other on any factual point.
pub fn llm_detect_contradiction(
    llm: &dyn LlmProvider,
    text_a: &str,
    text_b: &str,
) -> Result<bool> {
    let prompt = PromptTemplates::conflict_detect(text_a, text_b);
    let response = llm.generate(&prompt, 50)?;
    Ok(response.trim().to_uppercase().contains("YES"))
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Compile-time check that `LlmProvider` is object-safe.
    #[allow(dead_code)]
    fn assert_object_safe(_p: Box<dyn LlmProvider>) {}

    #[test]
    fn test_memory_merge_contains_all() {
        let p = PromptTemplates::memory_merge(&["alpha", "beta"]);
        assert!(p.contains("alpha"));
        assert!(p.contains("beta"));
        assert!(p.contains("Merged memory"));
    }

    #[test]
    fn test_memory_merge_empty() {
        let p = PromptTemplates::memory_merge(&[]);
        assert!(p.contains("Merged memory"));
    }

    #[test]
    fn test_conflict_detect_contains_both() {
        let p = PromptTemplates::conflict_detect("sky is blue", "sky is green");
        assert!(p.contains("sky is blue"));
        assert!(p.contains("sky is green"));
        assert!(p.contains("YES") && p.contains("NO"));
    }

    #[test]
    fn test_importance_score_contains_inputs() {
        let p = PromptTemplates::importance_score("memo", "ctx");
        assert!(p.contains("memo"));
        assert!(p.contains("ctx"));
        assert!(p.contains("0.0") && p.contains("1.0"));
    }

    #[test]
    fn test_summarize_contains_input() {
        let p = PromptTemplates::summarize("a long memory text");
        assert!(p.contains("a long memory text"));
        assert!(p.contains("Summary"));
    }
}
