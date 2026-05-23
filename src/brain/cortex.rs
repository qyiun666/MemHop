//! Cortex — worldview storage (layer="cortex" CRUD)
//!
//! The Cortex stores beliefs about the user, the world, and the assistant itself.
//! Evolution (belief conflict detection + reflective updates) is deferred to v0.6.0.
//! For v0.5.0, this is a simple in-memory key-value store of beliefs that gets
//! injected into the prompt assembly pipeline.

use chrono::Utc;

/// A single belief in the worldview.
///
/// Categories: "fact", "preference", "rule", "self_identity", "user_trait"
#[derive(Debug, Clone)]
pub struct Belief {
    pub content: String,
    pub confidence: f32,
    pub category: String,
    pub created_at: String,
}

impl Belief {
    pub fn new(content: &str, confidence: f32, category: &str) -> Self {
        Belief {
            content: content.to_string(),
            confidence,
            category: category.to_string(),
            created_at: Utc::now().to_rfc3339(),
        }
    }
}

/// Storage for worldview beliefs.
///
/// In v0.5.0, beliefs are stored in-memory. In later versions, they will be
/// persisted via the engine with `layer="cortex"` metadata.
pub struct CortexStorage {
    beliefs: Vec<Belief>,
}

impl CortexStorage {
    /// Create an empty CortexStorage.
    pub fn new() -> Self {
        CortexStorage {
            beliefs: Vec::new(),
        }
    }

    /// Return all current beliefs for worldview injection.
    pub fn current_beliefs(&self) -> Vec<Belief> {
        self.beliefs.clone()
    }

    /// Add a new belief to the worldview.
    ///
    /// If a belief with the same content already exists, its confidence is
    /// updated to the max of old and new (reinforcement).
    pub fn add_belief(&mut self, content: &str, confidence: f32, category: &str) {
        if let Some(existing) = self
            .beliefs
            .iter_mut()
            .find(|b| b.content == content)
        {
            existing.confidence = existing.confidence.max(confidence);
            return;
        }
        self.beliefs.push(Belief::new(content, confidence, category));
    }

    /// Remove a belief by exact content match. Returns true if found.
    pub fn remove_belief(&mut self, content: &str) -> bool {
        let len_before = self.beliefs.len();
        self.beliefs.retain(|b| b.content != content);
        self.beliefs.len() < len_before
    }

    /// Clear all beliefs.
    pub fn clear(&mut self) {
        self.beliefs.clear();
    }

    /// Number of stored beliefs.
    pub fn count(&self) -> usize {
        self.beliefs.len()
    }

    /// Return beliefs filtered by category.
    pub fn beliefs_by_category(&self, category: &str) -> Vec<&Belief> {
        self.beliefs
            .iter()
            .filter(|b| b.category == category)
            .collect()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn make_cortex() -> CortexStorage {
        CortexStorage::new()
    }

    #[test]
    fn test_new_cortex_is_empty() {
        let cortex = make_cortex();
        assert_eq!(cortex.count(), 0);
        assert!(cortex.current_beliefs().is_empty());
    }

    #[test]
    fn test_add_and_retrieve_belief() {
        let mut cortex = make_cortex();
        cortex.add_belief("The user prefers concise answers", 0.8, "preference");
        assert_eq!(cortex.count(), 1);

        let beliefs = cortex.current_beliefs();
        assert_eq!(beliefs[0].content, "The user prefers concise answers");
        assert!((beliefs[0].confidence - 0.8).abs() < 0.001);
        assert_eq!(beliefs[0].category, "preference");
    }

    #[test]
    fn test_add_belief_reinforcement() {
        let mut cortex = make_cortex();
        cortex.add_belief("fact one", 0.5, "fact");
        cortex.add_belief("fact one", 0.7, "fact"); // higher confidence
        assert_eq!(cortex.count(), 1);
        assert!((cortex.current_beliefs()[0].confidence - 0.7).abs() < 0.001);
    }

    #[test]
    fn test_add_belief_no_downgrade() {
        let mut cortex = make_cortex();
        cortex.add_belief("fact one", 0.9, "fact");
        cortex.add_belief("fact one", 0.3, "fact"); // lower, should not downgrade
        assert_eq!(cortex.count(), 1);
        assert!((cortex.current_beliefs()[0].confidence - 0.9).abs() < 0.001);
    }

    #[test]
    fn test_remove_belief() {
        let mut cortex = make_cortex();
        cortex.add_belief("keep me", 0.5, "fact");
        cortex.add_belief("remove me", 0.5, "fact");
        assert_eq!(cortex.count(), 2);

        assert!(cortex.remove_belief("remove me"));
        assert_eq!(cortex.count(), 1);
        assert_eq!(cortex.current_beliefs()[0].content, "keep me");

        assert!(!cortex.remove_belief("nonexistent"));
    }

    #[test]
    fn test_clear_beliefs() {
        let mut cortex = make_cortex();
        cortex.add_belief("a", 0.5, "fact");
        cortex.add_belief("b", 0.5, "preference");
        assert_eq!(cortex.count(), 2);

        cortex.clear();
        assert_eq!(cortex.count(), 0);
    }

    #[test]
    fn test_beliefs_by_category() {
        let mut cortex = make_cortex();
        cortex.add_belief("fact one", 0.8, "fact");
        cortex.add_belief("pref one", 0.7, "preference");
        cortex.add_belief("fact two", 0.6, "fact");

        let facts = cortex.beliefs_by_category("fact");
        assert_eq!(facts.len(), 2);

        let prefs = cortex.beliefs_by_category("preference");
        assert_eq!(prefs.len(), 1);

        let rules = cortex.beliefs_by_category("rule");
        assert_eq!(rules.len(), 0);
    }
}
