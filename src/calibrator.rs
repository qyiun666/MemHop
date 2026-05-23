//! Calibrator trait — optional second model for memory maintenance.
//!
//! The BrainLoop may optionally be configured with a separate, lighter model
//! that handles memory-calibration tasks — importance scoring, semantic dedup,
//! and link validation — so the main Thinker model stays focused on reasoning.
//!
//! When no calibrator is provided, `ThinkerBackedCalibrator` falls back to
//! using the Thinker (via prompt-based approximation).

use crate::thinker::Thinker;
use crate::types::BrainError;

// ── Result types ──────────────────────────────────────────

/// Result of a semantic dedup check between two memories.
#[derive(Debug, Clone)]
pub struct DedupResult {
    pub is_duplicate: bool,
    pub confidence: f32,
    pub merge_suggestion: Option<String>,
}

/// Result of a link-validity check.
#[derive(Debug, Clone)]
pub struct LinkValidation {
    pub is_valid: bool,
    pub confidence: f32,
}

/// Context provided alongside a memory when calibrating.
#[derive(Debug, Clone)]
pub struct CalibrationContext {
    pub domain: Option<String>,
    pub layer: Option<String>,
    pub recent_count: u32,
}

// ── Calibrator trait ─────────────────────────────────────

/// A model specialised for memory-calibration tasks.
///
/// In a dual-model setup the calibrator is a small, fast model (e.g. Qwen-0.5B)
/// while the Thinker handles deep reasoning. The calibrator is always optional:
/// when absent, `ThinkerBackedCalibrator` falls back to the Thinker.
pub trait Calibrator: Send + Sync {
    /// Score a single memory's importance (0.0 .. 1.0).
    fn cal_importance(
        &self,
        text: &str,
        context: &CalibrationContext,
    ) -> Result<f32, BrainError>;

    /// Check whether two memories are semantically duplicate.
    fn cal_dedup(
        &self,
        text_a: &str,
        text_b: &str,
    ) -> Result<DedupResult, BrainError>;

    /// Validate a semantic link between two memories.
    fn cal_link(
        &self,
        from_text: &str,
        to_text: &str,
        relation: &str,
    ) -> Result<LinkValidation, BrainError>;

    /// Batch-importance scoring — default implementation calls `cal_importance`
    /// individually for each item. Implementations that speak a batch API may
    /// override this to reduce HTTP round-trips.
    fn cal_batch_importance(
        &self,
        items: &[(String, CalibrationContext)],
    ) -> Result<Vec<f32>, BrainError> {
        items
            .iter()
            .map(|(text, ctx)| self.cal_importance(text, ctx))
            .collect()
    }
}

// ── ThinkerBackedCalibrator ──────────────────────────────

/// Fallback calibrator that delegates every call to the main Thinker via
/// prompt-based LLM queries. Used when the user does not supply a dedicated
/// calibrator model.
pub struct ThinkerBackedCalibrator {
    thinker: Box<dyn Thinker>,
}

impl ThinkerBackedCalibrator {
    pub fn new(thinker: Box<dyn Thinker>) -> Self {
        ThinkerBackedCalibrator { thinker }
    }
}

impl Calibrator for ThinkerBackedCalibrator {
    fn cal_importance(
        &self,
        text: &str,
        context: &CalibrationContext,
    ) -> Result<f32, BrainError> {
        let prompt = format!(
            "Evaluate the importance of the following memory. \
             Return ONLY a number between 0.0 and 1.0, nothing else.\n\
             Memory: {text}\n\
             Domain: {domain}\n\
             Layer: {layer}",
            text = text,
            domain = context.domain.as_deref().unwrap_or("unknown"),
            layer = context.layer.as_deref().unwrap_or("unknown"),
        );
        let response = self.thinker.think_fast(&prompt)?;
        response
            .trim()
            .parse::<f32>()
            .map_err(|_| BrainError::ParseError)
    }

    fn cal_dedup(
        &self,
        text_a: &str,
        text_b: &str,
    ) -> Result<DedupResult, BrainError> {
        let prompt = format!(
            "Are the following two memories semantically duplicate? \
             Return ONLY \"true\" or \"false\".\n\
             Memory A: {a}\nMemory B: {b}",
            a = text_a,
            b = text_b,
        );
        let response = self.thinker.think_fast(&prompt)?;
        let is_dup = response.trim().eq_ignore_ascii_case("true");
        Ok(DedupResult {
            is_duplicate: is_dup,
            confidence: if is_dup { 0.8 } else { 0.9 },
            merge_suggestion: None,
        })
    }

    fn cal_link(
        &self,
        from_text: &str,
        to_text: &str,
        relation: &str,
    ) -> Result<LinkValidation, BrainError> {
        let prompt = format!(
            "Is the following link semantically valid? \
             Return ONLY \"true\" or \"false\".\n\
             From: {from}\nTo: {to}\nRelation: {rel}",
            from = from_text,
            to = to_text,
            rel = relation,
        );
        let response = self.thinker.think_fast(&prompt)?;
        let valid = response.trim().eq_ignore_ascii_case("true");
        Ok(LinkValidation {
            is_valid: valid,
            confidence: if valid { 0.8 } else { 0.9 },
        })
    }
}

// ── Tests ─────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;
    use crate::types::BrainError;

    /// A mock Thinker that returns canned responses, simulating the LLM
    /// answers a ThinkerBackedCalibrator would receive.
    struct MockThinkerCal {
        importance_response: String,
        dedup_response: String,
        link_response: String,
        fail_mode: Option<&'static str>,
    }

    impl MockThinkerCal {
        fn with_responses(importance: &str, dedup: &str, link: &str) -> Self {
            MockThinkerCal {
                importance_response: importance.to_string(),
                dedup_response: dedup.to_string(),
                link_response: link.to_string(),
                fail_mode: None,
            }
        }

        fn with_failures() -> Self {
            MockThinkerCal {
                importance_response: String::new(),
                dedup_response: String::new(),
                link_response: String::new(),
                fail_mode: Some("all"),
            }
        }
    }

    impl Thinker for MockThinkerCal {
        fn think_fast(&self, prompt: &str) -> Result<String, BrainError> {
            if self.fail_mode.is_some() {
                return Err(BrainError::ThinkerFailed("mock failure".into()));
            }
            // Route to the appropriate canned response based on prompt content
            if prompt.contains("semantically duplicate") {
                Ok(self.dedup_response.clone())
            } else if prompt.contains("semantically valid") {
                Ok(self.link_response.clone())
            } else {
                Ok(self.importance_response.clone())
            }
        }

        fn think_deep(&self, _prompt: &str) -> Result<String, BrainError> {
            Ok(String::new())
        }

        fn think_stream(
            &self,
            _prompt: &str,
            _on_chunk: &mut dyn FnMut(&str),
        ) -> Result<String, BrainError> {
            Ok(String::new())
        }
    }

    fn make_context(domain: &str, layer: &str) -> CalibrationContext {
        CalibrationContext {
            domain: Some(domain.to_string()),
            layer: Some(layer.to_string()),
            recent_count: 0,
        }
    }

    #[test]
    fn test_cal_importance_parses_float() {
        let mock = MockThinkerCal::with_responses("0.75", "false", "true");
        let calibrator = ThinkerBackedCalibrator::new(Box::new(mock));
        let ctx = make_context("code", "episode");
        let score = calibrator.cal_importance("test memory", &ctx).unwrap();
        assert!((score - 0.75).abs() < 1e-4);
    }

    #[test]
    fn test_cal_importance_parses_float_with_whitespace() {
        let mock = MockThinkerCal::with_responses("  0.5  ", "false", "true");
        let calibrator = ThinkerBackedCalibrator::new(Box::new(mock));
        let ctx = make_context("chat", "episode");
        let score = calibrator.cal_importance("some memory", &ctx).unwrap();
        assert!((score - 0.5).abs() < 1e-4);
    }

    #[test]
    fn test_cal_importance_parse_error() {
        let mock = MockThinkerCal::with_responses("not-a-number", "false", "true");
        let calibrator = ThinkerBackedCalibrator::new(Box::new(mock));
        let ctx = make_context("test", "test");
        let result = calibrator.cal_importance("test", &ctx);
        assert!(matches!(result, Err(BrainError::ParseError)));
    }

    #[test]
    fn test_cal_dedup_returns_true() {
        let mock = MockThinkerCal::with_responses("0.5", "true", "true");
        let calibrator = ThinkerBackedCalibrator::new(Box::new(mock));
        let result = calibrator.cal_dedup("a", "a").unwrap();
        assert!(result.is_duplicate);
    }

    #[test]
    fn test_cal_dedup_returns_false() {
        let mock = MockThinkerCal::with_responses("0.5", "false", "true");
        let calibrator = ThinkerBackedCalibrator::new(Box::new(mock));
        let result = calibrator
            .cal_dedup("apple", "quantum physics")
            .unwrap();
        assert!(!result.is_duplicate);
    }

    #[test]
    fn test_cal_link_valid() {
        let mock = MockThinkerCal::with_responses("0.5", "false", "true");
        let calibrator = ThinkerBackedCalibrator::new(Box::new(mock));
        let result = calibrator
            .cal_link("A causes B", "B effect", "causes")
            .unwrap();
        assert!(result.is_valid);
    }

    #[test]
    fn test_cal_link_invalid() {
        let mock = MockThinkerCal::with_responses("0.5", "false", "false");
        let calibrator = ThinkerBackedCalibrator::new(Box::new(mock));
        let result = calibrator
            .cal_link("cats", "thermodynamics", "related_to")
            .unwrap();
        assert!(!result.is_valid);
    }

    #[test]
    fn test_cal_importance_thinker_failure() {
        let mock = MockThinkerCal::with_failures();
        let calibrator = ThinkerBackedCalibrator::new(Box::new(mock));
        let ctx = make_context("test", "test");
        let result = calibrator.cal_importance("x", &ctx);
        assert!(matches!(result, Err(BrainError::ThinkerFailed(_))));
    }

    #[test]
    fn test_cal_batch_importance_default_impl() {
        let mock = MockThinkerCal::with_responses("0.9", "false", "true");
        let calibrator = ThinkerBackedCalibrator::new(Box::new(mock));
        let items = vec![
            ("first".to_string(), make_context("a", "episode")),
            ("second".to_string(), make_context("b", "knowledge")),
        ];
        let scores = calibrator.cal_batch_importance(&items).unwrap();
        assert_eq!(scores.len(), 2);
        assert!((scores[0] - 0.9).abs() < 1e-4);
        assert!((scores[1] - 0.9).abs() < 1e-4);
    }

    #[test]
    fn test_dedup_result_defaults() {
        let r = DedupResult {
            is_duplicate: true,
            confidence: 0.95,
            merge_suggestion: None,
        };
        assert!(r.is_duplicate);
        assert!((r.confidence - 0.95).abs() < 1e-4);
        assert!(r.merge_suggestion.is_none());
    }

    #[test]
    fn test_link_validation_defaults() {
        let v = LinkValidation {
            is_valid: false,
            confidence: 0.0,
        };
        assert!(!v.is_valid);
        assert!(v.confidence.abs() < 1e-4);
    }

    #[test]
    fn test_calibration_context_defaults() {
        let ctx = CalibrationContext {
            domain: None,
            layer: None,
            recent_count: 0,
        };
        assert!(ctx.domain.is_none());
        assert!(ctx.layer.is_none());
        assert_eq!(ctx.recent_count, 0);
    }
}
