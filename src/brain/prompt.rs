//! Prompt engine — template assembly + output formatting + refine
//!
//! Combines BrainStem (prompt assembly) and Mouth (output formatting) into
//! one stateless module. The PromptEngine takes raw cognitive data and builds
//! the prompt string sent to the LLM, then formats the LLM's response for the user.

use crate::types::{PyMemory, Route};
use crate::brain::cortex::Belief;

/// Prompt engine — stateless prompt assembly and formatting.
pub struct PromptEngine;

impl PromptEngine {
    /// Create a new PromptEngine.
    pub fn new() -> Self {
        PromptEngine
    }

    /// Assemble the full prompt for LLM reasoning.
    ///
    /// Builds a structured prompt from:
    /// 1. System instructions (role, capabilities)
    /// 2. Relevant worldview beliefs (from cortex)
    /// 3. Recalled memories (from hippocampus, already filtered by gate)
    /// 4. User's current input
    /// 5. Route-specific instructions (Fast vs Deep vs Reasoning)
    pub fn assemble(
        &self,
        user_input: &str,
        route: &Route,
        memories: &[&PyMemory],
        worldview: &[Belief],
    ) -> String {
        let mut parts: Vec<String> = Vec::new();

        // ── System header ──────────────────────────────────
        parts.push(
            "You are MeowHop, an associative memory assistant. \
             You recall relevant information and reason step by step."
                .to_string(),
        );

        // ── Route instruction ──────────────────────────────
        let route_instr = match route {
            Route::Fast => "Provide a brief, direct answer. No explanation needed."
                .to_string(),
            Route::Deep => {
                "Provide a thorough, well-reasoned answer. \
                 Reference relevant context where helpful."
                    .to_string()
            }
            Route::Reasoning => {
                "Think step by step. Show your reasoning clearly. \
                 Consider multiple perspectives if applicable."
                    .to_string()
            }
        };
        parts.push(route_instr);

        // ── Worldview / beliefs ────────────────────────────
        if !worldview.is_empty() {
            let mut belief_lines: Vec<String> = Vec::new();
            for b in worldview {
                belief_lines.push(format!(
                    "- [{}] (confidence: {:.1}) {}",
                    b.category, b.confidence, b.content
                ));
            }
            parts.push(format!(
                "## Known context\n{}",
                belief_lines.join("\n")
            ));
        }

        // ── Recalled memories ──────────────────────────────
        if !memories.is_empty() {
            let mut mem_lines: Vec<String> = Vec::new();
            for (i, m) in memories.iter().enumerate() {
                let preview = if m.text.len() > 200 {
                    format!("{}...", &m.text[..m.text.floor_char_boundary(200)])
                } else {
                    m.text.clone()
                };
                mem_lines.push(format!(
                    "[{}] (confidence: {:.2}) {}",
                    i + 1,
                    m.confidence,
                    preview
                ));
            }
            parts.push(format!(
                "## Relevant memories\n{}",
                mem_lines.join("\n")
            ));
        }

        // ── User input ─────────────────────────────────────
        parts.push(format!("## User\n{}", user_input));

        // ── Response instruction ───────────────────────────
        parts.push("## Response\nPlease respond helpfully based on the above context.".to_string());

        parts.join("\n\n")
    }

    /// Format the raw LLM output for user display.
    ///
    /// Strips common artifacts and trims whitespace.
    pub fn format_output(&self, result: &str) -> String {
        let trimmed = result.trim();

        // Strip markdown code fences if the entire output is wrapped
        let stripped = if trimmed.starts_with("```") && trimmed.ends_with("```") {
            let inner = &trimmed[3..trimmed.len() - 3].trim();
            // Remove the language tag if present (first line of code block)
            if let Some(first_newline) = inner.find('\n') {
                inner[first_newline + 1..].trim()
            } else {
                inner
            }
        } else {
            trimmed
        };

        // Remove common LLM filler prefixes
        let cleaned = {
            let after_strip = stripped
                .strip_prefix("Sure, ")
                .or_else(|| stripped.strip_prefix("Of course, "))
                .or_else(|| stripped.strip_prefix("Certainly, "))
                .or_else(|| stripped.strip_prefix("Absolutely, "));

            match after_strip {
                Some(remaining) if remaining.len() > 5 => remaining,
                _ => stripped,
            }
        };

        cleaned.to_string()
    }

    /// Refine the prompt after validation failure.
    ///
    /// Appends the rejection reason as guidance for the LLM's next attempt,
    /// instructing it to produce a better response.
    pub fn refine(&self, current_prompt: &str, reason: &str) -> String {
        format!(
            "{}\n\n## Refinement feedback\nYour previous response was \
             rejected because: {}.\nPlease provide a better response that \
             addresses this concern. Do not repeat the previous mistake.",
            current_prompt, reason
        )
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::types::PyMemory;
    use crate::brain::cortex::Belief;
    use std::collections::HashMap;

    fn make_prompt() -> PromptEngine {
        PromptEngine::new()
    }

    fn make_memory(text: &str, confidence: f64) -> PyMemory {
        PyMemory {
            id: String::new(),
            text: text.to_string(),
            meta: HashMap::new(),
            confidence,
            created_at: String::new(),
            content_type: None,
            blob: None,
        }
    }

    #[test]
    fn test_assemble_basic() {
        let prompt = make_prompt();
        let result = prompt.assemble("hello", &Route::Fast, &[], &[]);

        assert!(result.contains("MeowHop"));
        assert!(result.contains("brief, direct answer"));
        assert!(result.contains("User"));
        assert!(result.contains("hello"));
    }

    #[test]
    fn test_assemble_with_memories() {
        let prompt = make_prompt();
        let mem = make_memory("Paris is the capital of France", 0.95);
        let result = prompt.assemble(
            "What is the capital of France?",
            &Route::Deep,
            &[&mem],
            &[],
        );

        assert!(result.contains("Paris is the capital of France"));
        assert!(result.contains("confidence: 0.95"));
        assert!(result.contains("thorough, well-reasoned"));
    }

    #[test]
    fn test_assemble_with_worldview() {
        let prompt = make_prompt();
        let beliefs = vec![Belief::new(
            "User speaks Chinese",
            0.9,
            "user_trait",
        )];
        let result = prompt.assemble("hello", &Route::Fast, &[], &beliefs);

        assert!(result.contains("Known context"));
        assert!(result.contains("User speaks Chinese"));
        assert!(result.contains("user_trait"));
    }

    #[test]
    fn test_assemble_reasoning_route() {
        let prompt = make_prompt();
        let result = prompt.assemble("solve 2+2", &Route::Reasoning, &[], &[]);

        assert!(result.contains("step by step"));
        assert!(result.contains("Show your reasoning"));
    }

    #[test]
    fn test_format_output_trimmed() {
        let prompt = make_prompt();
        let result = prompt.format_output("  Hello world  ");
        assert_eq!(result, "Hello world");
    }

    #[test]
    fn test_format_output_strip_filler() {
        let prompt = make_prompt();
        let result = prompt.format_output("Sure, here is the answer.");
        assert_eq!(result, "here is the answer.");
    }

    #[test]
    fn test_format_output_filler_too_short() {
        let prompt = make_prompt();
        // If stripping leaves very little, keep original
        let result = prompt.format_output("Sure, hi");
        assert_eq!(result, "Sure, hi");
    }

    #[test]
    fn test_format_output_code_fence() {
        let prompt = make_prompt();
        let input = "```rust\nfn main() {\n    println!(\"hello\");\n}\n```";
        let result = prompt.format_output(input);
        assert_eq!(result, "fn main() {\n    println!(\"hello\");\n}");
    }

    #[test]
    fn test_refine_appends_feedback() {
        let prompt = make_prompt();
        let original = "You are an assistant. User: hi";
        let result = prompt.refine(original, "Result too short");

        assert!(result.contains(original));
        assert!(result.contains("Refinement feedback"));
        assert!(result.contains("Result too short"));
        assert!(result.contains("better response"));
    }
}
