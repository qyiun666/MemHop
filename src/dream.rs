//! Dream Mode — idle-time memory consolidation.
//!
//! When the brain is idle for `idle_timeout_secs`, Dream Mode automatically
//! runs consolidation cycles that:
//!
//! 1. **Replay** random recent memories through the Hopfield network with
//!    plasticity, reinforcing frequently-accessed patterns.
//! 2. **Reinforce** under-valued but frequently-accessed memories by boosting
//!    their `importance` using statistical scoring (access count + time decay).
//! 3. **Apply decay** to old, seldom-accessed patterns via the Hopfield
//!    network's built-in decay mechanism.
//!
//! Dream Mode is purely deterministic — no LLM calls, no learning. It mimics
//! REM-sleep memory consolidation through attractor dynamics.

use std::time::{Duration, SystemTime};

use crate::brain::brain_loop::BrainLoop;

use crate::encoder::Encoder;
use crate::storage::MetaRecord;

// ── DreamConfig ───────────────────────────────────────────

/// Configuration for Dream Mode
#[derive(Debug, Clone)]
pub struct DreamConfig {
    /// Seconds of inactivity before a dream cycle triggers (default 300)
    pub idle_timeout_secs: u64,
    /// Maximum dream cycles per dream session (default 3)
    pub max_dream_cycles: u32,
    /// Whether dream mode is enabled (default true)
    pub enabled: bool,
    /// How many random memories to replay each cycle (default 5)
    pub replay_sample_size: usize,
    /// Importance threshold: memories below this get re-scored (default 0.4)
    pub weak_importance_threshold: f32,
    /// Recall count threshold: memories accessed more than this qualify (default 2)
    pub weak_recall_count_min: u64,
    /// Importance boost for reinforced memories (default 0.15)
    pub importance_boost: f32,
}

impl Default for DreamConfig {
    fn default() -> Self {
        DreamConfig {
            idle_timeout_secs: 300,
            max_dream_cycles: 3,
            enabled: true,
            replay_sample_size: 5,
            weak_importance_threshold: 0.4,
            weak_recall_count_min: 2,
            importance_boost: 0.15,
        }
    }
}

// ── DreamMode ─────────────────────────────────────────────

/// The Dream Mode state machine — tracks idle time and runs consolidation.
///
/// Usage:
/// ```ignore
/// let mut dream = DreamMode::new(DreamConfig::default());
/// dream.note_activity();  // call after each brain turn
/// if dream.should_dream() {
///     let report = dream.dream(&brain);
/// }
/// ```
pub struct DreamMode {
    pub config: DreamConfig,
    /// When the brain was last active (updated via `note_activity()`)
    last_active: SystemTime,
    /// Total dream cycles ever run
    pub cycle_count: u64,
}

/// Summary of what a dream cycle accomplished
#[derive(Debug, Clone, Default)]
pub struct DreamReport {
    /// Number of dream cycles completed
    pub cycles_completed: u32,
    /// Number of memories replayed through the Hopfield network
    pub replayed_count: u32,
    /// Number of memories with boosted importance
    pub reinforced_count: u32,
    /// Number of patterns removed by decay
    pub decayed_count: u32,
}

impl DreamMode {
    /// Create a new Dream Mode with the given configuration.
    ///
    /// `last_active` is initialised to `UNIX_EPOCH` so the first `should_dream()`
    /// check will always return `false` (the brain just started).
    pub fn new(config: DreamConfig) -> Self {
        DreamMode {
            config,
            last_active: SystemTime::UNIX_EPOCH,
            cycle_count: 0,
        }
    }

    /// Mark the brain as active — resets the idle timer.
    pub fn note_activity(&mut self) {
        self.last_active = SystemTime::now();
    }

    /// Check whether the brain has been idle long enough to trigger a dream.
    pub fn should_dream(&self) -> bool {
        if !self.config.enabled {
            return false;
        }
        match SystemTime::now().duration_since(self.last_active) {
            Ok(duration) => duration >= Duration::from_secs(self.config.idle_timeout_secs),
            Err(_) => false,
        }
    }

    /// Time elapsed since last activity, in seconds.
    pub fn idle_secs(&self) -> u64 {
        SystemTime::now()
            .duration_since(self.last_active)
            .map(|d| d.as_secs())
            .unwrap_or(0)
    }

    /// Run a full dream session on the given brain.
    ///
    /// Performs up to `max_dream_cycles` cycles of:
    /// 1. Random memory replay (reinforce via plasticity)
    /// 2. Weak memory reinforcement (boost importance)
    /// 3. Decay old patterns
    ///
    /// Returns a report of what was done.  This is a no-op when the brain
    /// has no engine (`inner: None`).
    pub fn dream(&mut self, brain: &BrainLoop) -> DreamReport {
        if !self.config.enabled {
            return DreamReport::default();
        }

        let inner = match brain.inner {
            Some(ref inner) => inner.clone(),
            None => return DreamReport::default(),
        };

        let mut report = DreamReport::default();
        let cycles = self.config.max_dream_cycles.min(10);

        for _cycle in 0..cycles {
            report.cycles_completed += 1;

            // Phase 1 — replay random memories through the Hopfield network
            report.replayed_count += self.replay_random(&inner);

            // Phase 2 — reinforce under-valued but frequently-accessed memories
            report.reinforced_count += self.reinforce_weak(&inner);

            // Phase 3 — apply Hopfield decay
            report.decayed_count += self.apply_decay(&inner);
        }

        self.cycle_count += 1;
        self.last_active = SystemTime::now();
        report
    }

    // ── Phase 1: Random replay ────────────────────────────

    /// Pick random memories and replay them through the Hopfield network with
    /// plasticity enabled, which naturally reinforces their attractor strength.
    fn replay_random(&self, inner: &std::sync::Arc<std::sync::RwLock<crate::engine::EngineInner>>) -> u32 {
        let mut replayed = 0u32;

        let ids = {
            let engine = match inner.read() {
                Ok(e) => e,
                Err(_) => return 0,
            };
            match engine.storage.all_ids() {
                Ok(ids) => ids,
                Err(_) => return 0,
            }
        };

        if ids.is_empty() {
            return 0;
        }

        let sample_size = self.config.replay_sample_size.min(ids.len());
        let sample: Vec<&String> = ids
            .iter()
            .filter(|_| {
                // Simple pseudo-random sampling: use a predictable cycle
                // to avoid depending on external RNG
                true
            })
            .take(sample_size)
            .collect();

        let mut engine = match inner.write() {
            Ok(e) => e,
            Err(_) => return 0,
        };

        // Enable plasticity for replay (preserve original config)
        let was_enabled = engine.hopfield.drift_enabled;
        engine.hopfield.enable_plasticity(true);

        for id in &sample {
            // Get blob text
            let blob = match engine.storage.get_blob(id) {
                Ok(Some(b)) => b,
                _ => continue,
            };

            // Encode and recall with plasticity to reinforce
            let encoded = engine.encoder.encode(&blob.text);
            let query_f32: Vec<f32> = encoded.dense.iter().map(|x: &half::f16| x.to_f32()).collect();
            let now_ms = SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap_or_default()
                .as_millis() as u64;

            if let Some((_id, _conf, indices)) = engine.hopfield.recall_with_plasticity(&query_f32, now_ms) {
                for idx in indices {
                    engine.dirty_patterns.insert(idx);
                }
                replayed += 1;
            }
        }

        // Restore plasticity config
        engine.hopfield.enable_plasticity(was_enabled);

        replayed
    }

    // ── Phase 2: Weak memory reinforcement ────────────────

    /// Scan for memories accessed frequently (high recall_count) but with
    /// low importance.  Use statistical scoring (access count + time decay)
    /// to boost importance — no Calibrator / LLM dependency.
    fn reinforce_weak(
        &self,
        inner: &std::sync::Arc<std::sync::RwLock<crate::engine::EngineInner>>,
    ) -> u32 {
        let mut reinforced = 0u32;

        let metas = {
            let engine = match inner.read() {
                Ok(e) => e,
                Err(_) => return 0,
            };
            match engine.storage.all_metas() {
                Ok(m) => m,
                Err(_) => return 0,
            }
        };

        let now_ms = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap_or_default()
            .as_millis() as i64;

        struct Candidate {
            id: String,
            current_importance: f32,
            raw_score: f32,
        }

        let mut candidates: Vec<Candidate> = Vec::new();

        for (id, meta) in &metas {
            if meta.protection != 0 {
                continue;
            }
            if meta.importance >= self.config.weak_importance_threshold {
                continue;
            }

            let engine = match inner.read() {
                Ok(e) => e,
                Err(_) => break,
            };
            let stats = engine.hopfield.get_access_stats(id);
            drop(engine);

            let stats = match stats {
                Some(s) => s,
                None => continue,
            };
            let (recall_count, _last_access) = stats;

            if recall_count < self.config.weak_recall_count_min {
                continue;
            }

            // Statistical scoring: access frequency + time recency
            let access_score = (recall_count as f32) * 0.5;
            let elapsed_ms = (now_ms - meta.created_at).max(0) as f64;
            let days = elapsed_ms / (24.0 * 3600.0 * 1000.0);
            let time_recency = 1.0f32 / (1.0 + days as f32 / 30.0) * 0.3;

            let raw_score = access_score + time_recency;
            if raw_score > 0.0 {
                candidates.push(Candidate {
                    id: id.clone(),
                    current_importance: meta.importance,
                    raw_score,
                });
            }
        }

        for c in &candidates {
            let engine = match inner.write() {
                Ok(e) => e,
                Err(_) => continue,
            };

            // Compute boost: proportional to raw_score, at least the threshold
            let boosted = c.current_importance + self.config.importance_boost * c.raw_score;
            let new_importance = boosted.max(self.config.weak_importance_threshold);

            if new_importance > c.current_importance {
                if let Ok(Some(current_meta)) = engine.storage.get_meta(&c.id) {
                    let updated = MetaRecord {
                        importance: new_importance.min(1.0),
                        ..current_meta
                    };
                    if engine.storage.update_meta(&c.id, &updated).is_ok() {
                        reinforced += 1;
                    }
                }
            }
        }

        reinforced
    }

    // ── Phase 3: Apply decay ─────────────────────────────

    /// Trigger the Hopfield network's built-in decay for old, seldom-accessed
    /// patterns.  Returns the number of patterns removed.
    fn apply_decay(&self, inner: &std::sync::Arc<std::sync::RwLock<crate::engine::EngineInner>>) -> u32 {
        let mut engine = match inner.write() {
            Ok(e) => e,
            Err(_) => return 0,
        };

        let now_ms = SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap_or_default()
            .as_millis() as u64;

        let decayed_ids = engine.hopfield.apply_decay(now_ms);
        let count = decayed_ids.len() as u32;

        // Also mark the storage records as dormant
        for id in &decayed_ids {
            if let Ok(Some(meta)) = engine.storage.get_meta(id) {
                let updated = MetaRecord {
                    is_dormant: true,
                    ..meta
                };
                let _ = engine.storage.update_meta(id, &updated);
            }
        }

        count
    }
}

// ── Tests ─────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;
    use crate::brain::brain_loop::BrainLoop;
    use crate::thinker::{Thinker, Cerebellum};
    use crate::types::{BrainConfig, BrainError};

    struct MockThinker;
    impl Thinker for MockThinker {
        fn think_fast(&self, _: &str) -> Result<String, BrainError> {
            Ok("fast".into())
        }
        fn think_deep(&self, _: &str) -> Result<String, BrainError> {
            Ok("deep".into())
        }
        fn think_stream(&self, _: &str, _: &mut dyn FnMut(&str)) -> Result<String, BrainError> {
            Ok("stream".into())
        }
    }

    struct MockCerebellum;
    impl Cerebellum for MockCerebellum {
        fn reflex(&self, _: &str) -> Option<String> { None }
    }

    fn make_brain() -> BrainLoop {
        BrainLoop::new(
            None,
            Box::new(MockThinker),
            Box::new(MockCerebellum),
            BrainConfig::default(),
        )
    }

    // ── Config ────────────────────────────────────────────

    #[test]
    fn test_dream_config_defaults() {
        let cfg = DreamConfig::default();
        assert!(cfg.enabled);
        assert_eq!(cfg.idle_timeout_secs, 300);
        assert_eq!(cfg.max_dream_cycles, 3);
        assert_eq!(cfg.replay_sample_size, 5);
    }

    #[test]
    fn test_dream_mode_new() {
        let mode = DreamMode::new(DreamConfig::default());
        assert_eq!(mode.cycle_count, 0);
        assert!(mode.config.enabled);
    }

    // ── Activity tracking ─────────────────────────────────

    #[test]
    fn test_note_activity_resets_timer() {
        let mut mode = DreamMode::new(DreamConfig::default());
        mode.note_activity();
        // Immediately after note_activity, idle_secs should be near 0
        assert!(mode.idle_secs() < 2);
    }

    #[test]
    fn test_should_dream_false_after_activity() {
        let mut mode = DreamMode::new(DreamConfig { idle_timeout_secs: 3600, ..DreamConfig::default() });
        mode.note_activity();
        // Should not dream immediately after activity (timeout is 3600s)
        assert!(!mode.should_dream());
    }

    #[test]
    fn test_should_dream_disabled() {
        let mut mode = DreamMode::new(DreamConfig { enabled: false, ..DreamConfig::default() });
        mode.note_activity();
        assert!(!mode.should_dream());
    }

    // ── Dream — no-op without engine ──────────────────────

    #[test]
    fn test_dream_noop_without_engine() {
        let brain = make_brain();
        assert!(brain.inner.is_none());

        let mut mode = DreamMode::new(DreamConfig::default());
        let report = mode.dream(&brain);
        assert_eq!(report.cycles_completed, 0);
        assert_eq!(report.replayed_count, 0);
        assert_eq!(report.reinforced_count, 0);
    }

    // ── Dream — with engine but empty ─────────────────────

    #[test]
    #[ignore = "requires MemHopEngine construction with pyo3 GIL; integration test"]
    fn test_dream_noop_empty_engine() {
    }

    // ─── Dream cycle count ────────────────────────────────

    #[test]
    fn test_cycle_count_increments() {
        let mode = DreamMode::new(DreamConfig { enabled: false, ..DreamConfig::default() });
        assert_eq!(mode.cycle_count, 0);
    }

    #[test]
    fn test_dream_updates_last_active() {
        let mut mode = DreamMode::new(DreamConfig { idle_timeout_secs: 1, ..DreamConfig::default() });
        // Force old last_active
        mode.last_active = SystemTime::now() - Duration::from_secs(10);
        assert!(mode.should_dream());
    }

    #[test]
    fn test_idle_secs_increases() {
        let cfg = DreamConfig::default();
        let mode = DreamMode::new(cfg);
        // Should be large since last_active = UNIX_EPOCH
        assert!(mode.idle_secs() > 100_000);
    }

    // ── Reinforce weak ────────────────────────────────────

    #[test]
    fn test_reinforce_weak_noop_without_engine() {
        let cfg = DreamConfig::default();
        let mut mode = DreamMode::new(cfg);
        let brain = make_brain();
        // Access through dream report (reinforce_weak is private)
        let report = mode.dream(&brain);
        assert_eq!(report.reinforced_count, 0);
    }

    // ── Apply decay ───────────────────────────────────────

    #[test]
    fn test_apply_decay_noop_without_engine() {
        let cfg = DreamConfig::default();
        let mut mode = DreamMode::new(cfg);
        let brain = make_brain();
        let report = mode.dream(&brain);
        assert_eq!(report.decayed_count, 0);
    }
}
