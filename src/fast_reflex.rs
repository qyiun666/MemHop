//! FastReflex — concrete Cerebellum implementation.
//!
//! A rule-based reflex layer that matches input against known patterns.
//! If a pattern matches, returns a canned response and the BrainLoop
//! short-circuits, skipping all higher-order reasoning.

use pyo3::prelude::*;

use crate::thinker::Cerebellum;

// ── ReflexRule ───────────────────────────────────────────

/// A single reflex rule: pattern → response.
#[derive(Debug, Clone)]
struct ReflexRule {
    /// Substring to match (lowercased)
    pattern: String,
    /// Response to return on match
    response: String,
}

// ── Built-in patterns ────────────────────────────────────

/// Default greeting patterns.
fn default_rules() -> Vec<ReflexRule> {
    vec![
        // ── Greetings ────────────────────────────────────
        ReflexRule {
            pattern: "hello".into(),
            response: "Hello! How can I help you today?".into(),
        },
        ReflexRule {
            pattern: "hi".into(),
            response: "Hi there! What's on your mind?".into(),
        },
        ReflexRule {
            pattern: "hey ".into(),
            response: "Hey! How can I assist you?".into(),
        },
        ReflexRule {
            pattern: "good morning".into(),
            response: "Good morning! How can I help you today?".into(),
        },
        ReflexRule {
            pattern: "good afternoon".into(),
            response: "Good afternoon! What can I do for you?".into(),
        },
        ReflexRule {
            pattern: "good evening".into(),
            response: "Good evening! How can I assist you?".into(),
        },
        // ── Acknowledgments ──────────────────────────────
        ReflexRule {
            pattern: "thanks".into(),
            response: "You're welcome! Let me know if you need anything else.".into(),
        },
        ReflexRule {
            pattern: "thank you".into(),
            response: "You're very welcome! Happy to help.".into(),
        },
        // ── Common questions ─────────────────────────────
        ReflexRule {
            pattern: "who are you".into(),
            response: "I'm MeowHop — your associative memory assistant. I can remember, recall, and reason about information to help you solve problems.".into(),
        },
        ReflexRule {
            pattern: "what can you do".into(),
            response: "I can remember information, recall relevant memories, reason about complex topics, and help you organize your thoughts. I also have a self-growth ability to compress and consolidate knowledge over time.".into(),
        },
        ReflexRule {
            pattern: "how are you".into(),
            response: "I'm functioning optimally! Ready to help you with whatever you need.".into(),
        },
        // ── Status ───────────────────────────────────────
        ReflexRule {
            pattern: "status".into(),
            response: "All systems operational. Memory engine active, cognitive loop ready.".into(),
        },
        ReflexRule {
            pattern: "are you there".into(),
            response: "Yes, I'm here and ready to help!".into(),
        },
    ]
}

// ── FastReflex ───────────────────────────────────────────

/// A rule-based reflex system implementing [`Cerebellum`].
///
/// Matches user input against predefined patterns. On match,
/// returns a canned response, allowing the BrainLoop to short-circuit.
#[pyclass(name = "FastReflex")]
#[derive(Clone)]
pub struct FastReflex {
    /// Ordered list of reflex rules (first match wins)
    rules: Vec<ReflexRule>,
}

#[pymethods]
impl FastReflex {
    #[new]
    pub fn new() -> Self {
        FastReflex {
            rules: default_rules(),
        }
    }

    fn __repr__(&self) -> String {
        format!("FastReflex(rules={})", self.rules.len())
    }

    /// Add a custom reflex rule (Python API for extensibility).
    fn add_rule(&mut self, pattern: String, response: String) {
        self.rules.push(ReflexRule { pattern, response });
    }
}

impl Default for FastReflex {
    fn default() -> Self {
        FastReflex::new()
    }
}

impl Cerebellum for FastReflex {
    fn reflex(&self, input: &str) -> Option<String> {
        let lower = input.to_lowercase();
        for rule in &self.rules {
            if lower.contains(&rule.pattern) {
                return Some(rule.response.clone());
            }
        }
        None
    }
}
