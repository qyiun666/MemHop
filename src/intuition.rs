//! Collective Intuition — multi-cat agreement detection.
//!
//! When multiple CloneCats independently arrive at the same conclusion,
//! Collective Intuition detects the consensus and reports it as an
//! **IntuitionInsight**.  The caller may then boost memory confidence
//! for those topics in the shared brain.
//!
//! This is NOT a voting mechanism — it mirrors Hopfield resonance, where
//! multiple attractors converging to the same energy valley signal shared
//! salience.

use std::collections::HashMap;

// ── CollectiveIntuition ───────────────────────────────────

/// Detects when multiple brains agree on the same topics.
pub struct CollectiveIntuition {
    /// Minimum number of brains that must mention a topic for agreement
    /// (default 3)
    pub agreement_threshold: usize,
    /// Confidence boost per agreeing brain (default 0.1)
    pub confidence_boost: f32,
}

impl Default for CollectiveIntuition {
    fn default() -> Self {
        CollectiveIntuition {
            agreement_threshold: 3,
            confidence_boost: 0.1,
        }
    }
}

impl CollectiveIntuition {
    /// Create a new CollectiveIntuition detector.
    pub fn new(agreement_threshold: usize, confidence_boost: f32) -> Self {
        CollectiveIntuition {
            agreement_threshold,
            confidence_boost,
        }
    }

    /// Analyse the conclusions from multiple brains and detect consensus.
    ///
    /// `brain_conclusions` is a list of `(brain_id, keywords_extracted)` pairs,
    /// where `keywords_extracted` is the set of topics each brain mentioned.
    ///
    /// Returns a list of `IntuitionInsight` for topics that crossed the
    /// `agreement_threshold`.
    pub fn check(&self, brain_conclusions: &[(&str, Vec<String>)]) -> Vec<IntuitionInsight> {
        if brain_conclusions.is_empty() {
            return Vec::new();
        }

        // Count how many brains mention each topic
        let mut topic_counts: HashMap<String, usize> = HashMap::new();
        let mut topic_brain_map: HashMap<String, Vec<&str>> = HashMap::new();

        for (brain_id, keywords) in brain_conclusions {
            // Use a local set to avoid double-counting the same brain for
            // the same keyword
            let mut seen = std::collections::HashSet::new();
            for kw in keywords {
                if seen.insert(kw.clone()) {
                    *topic_counts.entry(kw.clone()).or_insert(0) += 1;
                    topic_brain_map.entry(kw.clone()).or_default().push(brain_id);
                }
            }
        }

        // Build insights for topics above threshold
        let mut insights: Vec<IntuitionInsight> = topic_counts
            .into_iter()
            .filter(|(_, count)| *count >= self.agreement_threshold)
            .map(|(topic, count)| {
                let boost = self.confidence_boost * count as f32;
                let agreeing_brains: Vec<String> = topic_brain_map
                    .remove(&topic)
                    .unwrap_or_default()
                    .into_iter()
                    .map(String::from)
                    .collect();
                IntuitionInsight {
                    topic,
                    agreement_count: count,
                    confidence_boost: boost,
                    agreeing_brains,
                }
            })
            .collect();

        // Sort by agreement count descending (strongest consensus first)
        insights.sort_by(|a, b| b.agreement_count.cmp(&a.agreement_count));
        insights
    }

    /// Convenience: extract significant keywords from a result text.
    ///
    /// Similar to the keyword extraction in `SnapshotStrategy::Anchor`:
    /// returns words with length >= 3, lowercased, deduplicated.
    pub fn extract_keywords(text: &str) -> Vec<String> {
        let mut keywords: Vec<String> = text
            .split(|c: char| !c.is_alphanumeric() && c != '\'')
            .filter(|w| w.len() >= 3 && !w.is_empty())
            .map(|w| w.to_lowercase())
            .collect();
        keywords.sort();
        keywords.dedup();
        keywords
    }
}

// ── IntuitionInsight ──────────────────────────────────────

/// A detected collective intuition — one topic that multiple brains agree on.
#[derive(Debug, Clone)]
pub struct IntuitionInsight {
    /// The topic / keyword that multiple brains mentioned
    pub topic: String,
    /// How many brains independently mentioned this topic
    pub agreement_count: usize,
    /// Suggested confidence boost for this topic (caller applies it)
    pub confidence_boost: f32,
    /// Which brains agreed on this topic (brain IDs)
    pub agreeing_brains: Vec<String>,
}

// ── Tests ─────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;

    // ── Config ────────────────────────────────────────────

    #[test]
    fn test_default_config() {
        let ci = CollectiveIntuition::default();
        assert_eq!(ci.agreement_threshold, 3);
        assert!((ci.confidence_boost - 0.1).abs() < 1e-6);
    }

    #[test]
    fn test_custom_config() {
        let ci = CollectiveIntuition::new(2, 0.2);
        assert_eq!(ci.agreement_threshold, 2);
        assert!((ci.confidence_boost - 0.2).abs() < 1e-6);
    }

    // ── Empty / edge cases ───────────────────────────────

    #[test]
    fn test_empty_input() {
        let ci = CollectiveIntuition::default();
        let insights = ci.check(&[]);
        assert!(insights.is_empty());
    }

    #[test]
    fn test_no_agreement() {
        let ci = CollectiveIntuition::new(2, 0.1);
        let inputs = vec![
            ("cat_a", vec!["alpha".into(), "beta".into()]),
            ("cat_b", vec!["gamma".into(), "delta".into()]),
        ];
        let insights = ci.check(&inputs);
        assert!(insights.is_empty(), "No topic has 2+ mentions");
    }

    // ── Agreement detection ───────────────────────────────

    #[test]
    fn test_single_topic_agreement() {
        let ci = CollectiveIntuition::new(2, 0.1);
        let inputs = vec![
            ("cat_a", vec!["memory".into(), "hopfield".into()]),
            ("cat_b", vec!["memory".into(), "network".into()]),
        ];
        let insights = ci.check(&inputs);
        assert_eq!(insights.len(), 1, "Only 'memory' is agreed upon");
        assert_eq!(insights[0].topic, "memory");
        assert_eq!(insights[0].agreement_count, 2);
        assert!((insights[0].confidence_boost - 0.2).abs() < 1e-6);
    }

    #[test]
    fn test_multiple_topic_agreement() {
        let ci = CollectiveIntuition::new(2, 0.1);
        let inputs = vec![
            ("cat_a", vec!["rust".into(), "python".into()]),
            ("cat_b", vec!["rust".into(), "python".into()]),
            ("cat_c", vec!["rust".into()]),
        ];
        let insights = ci.check(&inputs);
        // rust: 3 votes (>=2), python: 2 votes (>=2)
        assert_eq!(insights.len(), 2);
        // Sorted by count: rust first (3 > 2)
        assert_eq!(insights[0].topic, "rust");
        assert_eq!(insights[0].agreement_count, 3);
        assert_eq!(insights[1].topic, "python");
        assert_eq!(insights[1].agreement_count, 2);
    }

    #[test]
    fn test_agreement_boost_scales_with_count() {
        let ci = CollectiveIntuition::new(2, 0.15);
        let inputs = vec![
            ("a", vec!["vector".into()]),
            ("b", vec!["vector".into()]),
            ("c", vec!["vector".into()]),
            ("d", vec!["vector".into()]),
        ];
        let insights = ci.check(&inputs);
        assert_eq!(insights.len(), 1);
        // 4 brains × 0.15 = 0.6
        assert!((insights[0].confidence_boost - 0.6).abs() < 1e-6);
        assert_eq!(insights[0].agreeing_brains.len(), 4);
    }

    // ── Extract keywords ──────────────────────────────────

    #[test]
    fn test_extract_keywords_basic() {
        let keywords = CollectiveIntuition::extract_keywords(
            "the quick brown fox jumps over the lazy dog",
        );
        assert!(keywords.contains(&"brown".to_string()));
        assert!(keywords.contains(&"fox".to_string()));
        assert!(keywords.contains(&"jumps".to_string()));
        assert!(keywords.contains(&"quick".to_string()));
        assert!(keywords.contains(&"the".to_string()));
        assert!(keywords.contains(&"lazy".to_string()));
        assert!(keywords.contains(&"dog".to_string()));
        assert!(keywords.contains(&"over".to_string()));
        assert!(!keywords.contains(&"to".to_string()));  // 2 chars → excluded
    }

    #[test]
    fn test_extract_keywords_short_words_excluded() {
        let keywords = CollectiveIntuition::extract_keywords("a an at by to");
        assert!(keywords.is_empty());
    }

    #[test]
    fn test_extract_keywords_deduplicates() {
        let keywords = CollectiveIntuition::extract_keywords("alpha beta alpha gamma beta");
        assert_eq!(keywords.len(), 3);
    }

    #[test]
    fn test_extract_keywords_empty() {
        let keywords = CollectiveIntuition::extract_keywords("");
        assert!(keywords.is_empty());
    }

    #[test]
    fn test_extract_keywords_case_normalized() {
        let keywords = CollectiveIntuition::extract_keywords("Hello HELLO hello");
        assert_eq!(keywords.len(), 1);
        assert_eq!(keywords[0], "hello");
    }

    // ── Edge: one brain, no possible agreement ────────────

    #[test]
    fn test_single_brain_no_insight() {
        let ci = CollectiveIntuition::new(2, 0.1);
        let inputs = vec![("only_cat", vec!["memory".into()])];
        let insights = ci.check(&inputs);
        assert!(insights.is_empty(), "Need 2+ brains to agree");
    }

    // ── Duplicate keywords from same brain ────────────────

    #[test]
    fn test_same_brain_duplicate_not_double_counted() {
        let ci = CollectiveIntuition::new(2, 0.1);
        let inputs = vec![
            ("cat_a", vec!["topic".into(), "topic".into(), "topic".into()]),
            ("cat_b", vec!["topic".into()]),
        ];
        let insights = ci.check(&inputs);
        // Both brains mention "topic" once each → 2 agreements
        assert_eq!(insights.len(), 1);
        assert_eq!(insights[0].agreement_count, 2);
        assert_eq!(insights[0].agreeing_brains.len(), 2);
    }
}
