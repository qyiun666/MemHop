//! Thinker + Cerebellum trait definitions for the BrainLoop cognitive architecture.
//!
//! These traits are injected into BrainLoop at construction time, allowing the
//! cognitive loop to invoke LLM reasoning and reflex behaviors without depending
//! on any concrete provider implementation.

use crate::types::BrainError;

/// The thinking core — injected LLM that the BrainLoop calls for reasoning.
///
/// Three tiers:
/// - `think_fast`: cheap, low-latency model (e.g. Qwen-Turbo)
/// - `think_deep`: full-capability model (e.g. DeepSeek-V3)
/// - `think_stream`: streaming variant of deep reasoning,
///   each token pushed through `on_chunk` as it arrives
pub trait Thinker: Send + Sync {
    /// Fast, cheap reasoning — for simple lookups and confirmations
    fn think_fast(&self, prompt: &str) -> Result<String, BrainError>;

    /// Deep, full-capability reasoning
    fn think_deep(&self, prompt: &str) -> Result<String, BrainError>;

    /// Streaming deep reasoning — tokens arrive one by one
    fn think_stream(
        &self,
        prompt: &str,
        on_chunk: &mut dyn FnMut(&str),
    ) -> Result<String, BrainError>;
}

/// Reflex layer — purely rule-based, no LLM needed.
///
/// If `reflex()` returns `Some(response)` the BrainLoop short-circuits
/// and returns the response immediately, skipping all higher-order processing.
pub trait Cerebellum: Send + Sync {
    /// Check if input triggers a reflex response
    fn reflex(&self, input: &str) -> Option<String>;
}
