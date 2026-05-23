//! ModelRouter — task-appropriate dispatch between Thinker and Calibrator.
//!
//! The BrainLoop uses a ModelRouter to dispatch reasoning tasks to the
//! Thinker (the big, expensive model) and calibration tasks to the
//! Calibrator (the small, cheap model). This eliminates the need for
//! BrainLoop to know which model handles what.

use crate::calibrator::{Calibrator, CalibrationContext, DedupResult, LinkValidation};
use crate::thinker::Thinker;
use crate::types::{BrainError, Route};

/// Routes cognitive tasks to the appropriate model.
///
/// - Reasoning (Route::Fast/Deep) → Thinker
/// - Calibration (importance/dedup/link) → Calibrator
///
/// ModelRouter takes ownership of both the Thinker and the Calibrator.
/// When no Calibrator is configured, a ThinkerBackedCalibrator is used
/// internally (see `Calibrator` trait documentation).
pub struct ModelRouter {
    /// The main reasoning model.
    pub thinker: Box<dyn Thinker>,
    /// The calibration model (may be ThinkerBackedCalibrator).
    pub calibrator: Box<dyn Calibrator>,
}

impl ModelRouter {
    /// Construct a new router.
    ///
    /// `calibrator` should already be the correct implementation
    /// (either user-provided or ThinkerBackedCalibrator fallback).
    pub fn new(thinker: Box<dyn Thinker>, calibrator: Box<dyn Calibrator>) -> Self {
        ModelRouter { thinker, calibrator }
    }

    /// Route a reasoning task to the Thinker.
    ///
    /// - `Route::Fast` → `think_fast`
    /// - `Route::Deep | Reasoning` → `think_deep`
    pub fn route_reasoning(
        &self,
        prompt: &str,
        route: &Route,
    ) -> Result<String, BrainError> {
        match route {
            Route::Fast => self.thinker.think_fast(prompt),
            Route::Deep | Route::Reasoning => self.thinker.think_deep(prompt),
        }
    }

    /// Route a streaming reasoning task to the Thinker.
    pub fn route_stream(
        &self,
        prompt: &str,
        on_chunk: &mut dyn FnMut(&str),
    ) -> Result<String, BrainError> {
        self.thinker.think_stream(prompt, on_chunk)
    }

    // ── Calibrator dispatch ────────────────────────────

    /// Score a single memory's importance via the calibrator.
    pub fn route_calibrate_importance(
        &self,
        text: &str,
        context: &CalibrationContext,
    ) -> Result<f32, BrainError> {
        self.calibrator.cal_importance(text, context)
    }

    /// Batch-importance scoring via the calibrator.
    pub fn route_calibrate_batch(
        &self,
        items: &[(String, CalibrationContext)],
    ) -> Result<Vec<f32>, BrainError> {
        self.calibrator.cal_batch_importance(items)
    }

    /// Semantic dedup check via the calibrator.
    pub fn route_calibrate_dedup(
        &self,
        text_a: &str,
        text_b: &str,
    ) -> Result<DedupResult, BrainError> {
        self.calibrator.cal_dedup(text_a, text_b)
    }

    /// Link validation via the calibrator.
    pub fn route_calibrate_link(
        &self,
        from_text: &str,
        to_text: &str,
        relation: &str,
    ) -> Result<LinkValidation, BrainError> {
        self.calibrator.cal_link(from_text, to_text, relation)
    }
}

// ── Tests ─────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;

    /// A mock Thinker that returns predetermined responses.
    struct MockRouterThinker {
        response: String,
    }

    impl Thinker for MockRouterThinker {
        fn think_fast(&self, _p: &str) -> Result<String, BrainError> {
            Ok(format!("fast: {}", self.response))
        }
        fn think_deep(&self, _p: &str) -> Result<String, BrainError> {
            Ok(format!("deep: {}", self.response))
        }
        fn think_stream(
            &self,
            _p: &str,
            _cb: &mut dyn FnMut(&str),
        ) -> Result<String, BrainError> {
            Ok(format!("stream: {}", self.response))
        }
    }

    /// A mock Calibrator that returns simple canned values.
    struct MockRouterCalibrator;

    impl Calibrator for MockRouterCalibrator {
        fn cal_importance(
            &self,
            _text: &str,
            _ctx: &CalibrationContext,
        ) -> Result<f32, BrainError> {
            Ok(0.42)
        }
        fn cal_dedup(
            &self,
            _a: &str,
            _b: &str,
        ) -> Result<DedupResult, BrainError> {
            Ok(DedupResult {
                is_duplicate: true,
                confidence: 0.9,
                merge_suggestion: None,
            })
        }
        fn cal_link(
            &self,
            _from: &str,
            _to: &str,
            _rel: &str,
        ) -> Result<LinkValidation, BrainError> {
            Ok(LinkValidation {
                is_valid: true,
                confidence: 0.85,
            })
        }
    }

    fn make_router() -> ModelRouter {
        ModelRouter::new(
            Box::new(MockRouterThinker {
                response: "test".into(),
            }),
            Box::new(MockRouterCalibrator),
        )
    }

    #[test]
    fn test_route_reasoning_fast() {
        let router = make_router();
        let result = router.route_reasoning("hello", &Route::Fast).unwrap();
        assert_eq!(result, "fast: test");
    }

    #[test]
    fn test_route_reasoning_deep() {
        let router = make_router();
        let result = router.route_reasoning("hello", &Route::Deep).unwrap();
        assert_eq!(result, "deep: test");
    }

    #[test]
    fn test_route_reasoning_reasoning() {
        let router = make_router();
        let result = router.route_reasoning("hello", &Route::Reasoning).unwrap();
        assert_eq!(result, "deep: test");
    }

    #[test]
    fn test_route_stream() {
        let router = make_router();
        let result = router.route_stream("hello", &mut |_| {}).unwrap();
        assert_eq!(result, "stream: test");
    }

    #[test]
    fn test_route_calibrate_importance() {
        let router = make_router();
        let ctx = CalibrationContext {
            domain: None,
            layer: None,
            recent_count: 0,
        };
        let score = router.route_calibrate_importance("mem", &ctx).unwrap();
        assert!((score - 0.42).abs() < 1e-4);
    }

    #[test]
    fn test_route_calibrate_dedup() {
        let router = make_router();
        let result = router.route_calibrate_dedup("a", "b").unwrap();
        assert!(result.is_duplicate);
    }

    #[test]
    fn test_route_calibrate_link() {
        let router = make_router();
        let result = router.route_calibrate_link("from", "to", "rel").unwrap();
        assert!(result.is_valid);
    }

    #[test]
    fn test_route_calibrate_batch() {
        let router = make_router();
        let items = vec![
            ("a".to_string(), CalibrationContext {
                domain: None, layer: None, recent_count: 0,
            }),
            ("b".to_string(), CalibrationContext {
                domain: None, layer: None, recent_count: 0,
            }),
        ];
        let scores = router.route_calibrate_batch(&items).unwrap();
        assert_eq!(scores.len(), 2);
        assert!((scores[0] - 0.42).abs() < 1e-4);
    }
}
