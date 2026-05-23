use pyo3::prelude::*;

mod types;
mod engine;
mod encoder;
mod hopfield;
mod storage;
mod index;
mod python_conv;
mod meta_index;
mod recall_strategies;
mod scene_gating;
mod brain;
mod thinker;
mod http_thinker;
mod fast_reflex;

use brain::brain_loop::BrainLoop;
use types::*;

// ── PyBrainLoop — Python wrapper for BrainLoop ───────────

/// Python-facing BrainLoop — the complete cognitive loop.
///
/// Wraps the Rust BrainLoop state machine and exposes it to Python
/// via pyo3. Accepts HttpThinker, FastReflex, and BrainConfig.
#[pyclass(name = "BrainLoop")]
struct PyBrainLoop {
    inner: BrainLoop,
}

#[pymethods]
impl PyBrainLoop {
    #[new]
    #[pyo3(signature = (
        db_path=None,
        thinker=None,
        cerebellum=None,
        config=None,
    ))]
    fn new(
        db_path: Option<String>,
        thinker: Option<http_thinker::HttpThinker>,
        cerebellum: Option<fast_reflex::FastReflex>,
        config: Option<BrainConfig>,
    ) -> Self {
        let _ = db_path; // engine integration deferred to v0.5.0+ Phase 3
        let thinker: Box<dyn crate::thinker::Thinker> = thinker
            .map(|t| Box::new(t) as Box<dyn crate::thinker::Thinker>)
            .unwrap_or_else(|| Box::new(http_thinker::HttpThinker::new(
                "https://api.openai.com/v1/chat/completions".into(),
                "".into(),
                "gpt-4o".into(),
                "gpt-4o-mini".into(),
            )));
        let cerebellum: Box<dyn crate::thinker::Cerebellum> = cerebellum
            .map(|c| Box::new(c) as Box<dyn crate::thinker::Cerebellum>)
            .unwrap_or_else(|| Box::new(fast_reflex::FastReflex::new()));
        let cfg = config.unwrap_or_default();

        let brain = BrainLoop::new(None, thinker, cerebellum, cfg);
        PyBrainLoop { inner: brain }
    }

    /// Run one full cognitive cycle (non-streaming).
    fn process(&mut self, user_input: &str) -> PyBrainAction {
        let action = self.inner.process(user_input);
        action.into()
    }

    /// Run one cognitive cycle with streaming LLM output.
    ///
    /// `on_chunk` is a Python callable that receives each token as a string.
    fn process_streaming(
        &mut self,
        user_input: &str,
        on_chunk: PyObject,
    ) -> PyBrainAction {
        let callback = on_chunk;
        let action = self.inner.process_streaming(user_input, &mut |chunk: &str| {
            Python::with_gil(|py| {
                let _ = callback.call1(py, (chunk,));
            });
        });
        action.into()
    }

    /// Feed body action results back into the brain.
    fn feed_body_result(&mut self, results: Vec<PyBodyResult>) -> PyBrainAction {
        let rust_results: Vec<BodyResult> = results.into_iter().map(|r| r.into()).collect();
        let action = self.inner.feed_body_result(rust_results);
        action.into()
    }

    fn __repr__(&self) -> String {
        format!(
            "BrainLoop(turns={}, route={:?})",
            self.inner.turn_counter, self.inner.current_route
        )
    }
}

// ── PyO3 module export ───────────────────────────────────

#[pymodule]
fn _core(m: &Bound<'_, PyModule>) -> PyResult<()> {
    m.add_class::<types::PyMemory>()?;
    m.add_class::<engine::MemHopEngine>()?;
    m.add("MemHopError", m.py().get_type::<types::MemHopError>())?;
    m.add("MemHopClosedError", m.py().get_type::<types::MemHopClosedError>())?;

    // v0.5.0 BrainLoop exports
    m.add_class::<PyBrainLoop>()?;
    m.add_class::<BrainConfig>()?;
    m.add_class::<PyBrainAction>()?;
    m.add_class::<PyBodyAction>()?;
    m.add_class::<PyBrainNotifications>()?;
    m.add_class::<PyCognitionHealth>()?;
    m.add_class::<PyBodyResult>()?;
    m.add_class::<http_thinker::HttpThinker>()?;
    m.add_class::<fast_reflex::FastReflex>()?;

    Ok(())
}
