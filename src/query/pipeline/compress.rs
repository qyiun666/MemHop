// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Dialogue compression: LLM-based content summarization for reduced noise.
//!
//! Uses the LlmProvider's compress_for_retrieval method when available,
//! falling back to the original text unchanged.

use crate::dream::llm::LlmProvider;
use crate::MemHopError;

/// Compress/summarize dialogue text to its core meaning before retrieval.
///
/// When an LLM provider is available:
/// - Remove conversational filler and repetitions
/// - Extract the core question or intent
/// - Summarize long context into search-optimized form
///
/// When no provider is given, returns the original text unchanged.
pub fn compress_dialogue(
    dialogue: &str,
    llm: Option<&dyn LlmProvider>,
) -> Result<String, MemHopError> {
    match llm {
        Some(provider) => provider.compress_for_retrieval(dialogue, "用户提问"),
        None => Ok(dialogue.to_string()),
    }
}
