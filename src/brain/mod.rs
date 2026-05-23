//! BrainLoop — MeowHop cognitive loop state machine
//!
//! The BrainLoop orchestrates the full cognitive pipeline:
//! frontal → gate → hippocampus → cortex → prompt → cerebrum → growth
//!
//! Sub-modules:
//! - gate: routing verdict + confidence filter + safety review + result validation
//! - cortex: worldview CRUD (layer="cortex"), evolution deferred to v0.6.0
//! - prompt: template assembly + output formatting + refine
//! - brain_loop: state machine + finalize + streaming
//! - growth: self-growth (compress + consolidate)

pub mod gate;
pub mod cortex;
pub mod prompt;
pub mod brain_loop;
pub mod growth;
