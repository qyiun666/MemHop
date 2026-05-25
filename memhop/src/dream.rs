//! Dream Mode — idle-time memory consolidation.
//! ... (see git history for full docs)
use std::time::{Duration, SystemTime};
use std::sync::{Arc, RwLock};
use crate::engine::EngineInner;
use crate::encoder::Encoder;
use crate::storage::MetaRecord;

#[derive(Debug, Clone)]
pub struct DreamConfig { pub idle_timeout_secs: u64, pub max_dream_cycles: u32, pub enabled: bool, pub replay_sample_size: usize, pub weak_importance_threshold: f32, pub weak_recall_count_min: u64, pub importance_boost: f32, }
impl Default for DreamConfig { fn default() -> Self { Self { idle_timeout_secs:300, max_dream_cycles:3, enabled:true, replay_sample_size:5, weak_importance_threshold:0.4, weak_recall_count_min:2, importance_boost:0.15 } } }

impl From<crate::types::DreamConfig> for DreamConfig {
    fn from(cfg: crate::types::DreamConfig) -> Self {
        DreamConfig {
            max_dream_cycles: 3,
            enabled: true,
            replay_sample_size: 5,
            weak_importance_threshold: cfg.weaken_threshold,
            weak_recall_count_min: 2,
            importance_boost: 0.15,
            ..Default::default()
        }
    }
}

pub struct DreamMode { pub config: DreamConfig, last_active: SystemTime, pub cycle_count: u64, }
#[derive(Debug, Clone, Default)] pub struct DreamReport { pub cycles_completed: u32, pub replayed_count: u32, pub reinforced_count: u32, pub decayed_count: u32, }

impl DreamMode {
    pub fn new(config: DreamConfig) -> Self { Self { config, last_active: SystemTime::UNIX_EPOCH, cycle_count: 0 } }
    #[allow(dead_code)]
    pub fn note_activity(&mut self) { self.last_active = SystemTime::now(); }
    #[allow(dead_code)]
    pub fn should_dream(&self) -> bool { self.config.enabled && SystemTime::now().duration_since(self.last_active).is_ok_and(|d| d >= Duration::from_secs(self.config.idle_timeout_secs)) }
    #[allow(dead_code)]
    pub fn idle_secs(&self) -> u64 { SystemTime::now().duration_since(self.last_active).map(|d| d.as_secs()).unwrap_or(0) }

    pub fn dream(&mut self, inner: Option<&Arc<RwLock<EngineInner>>>) -> DreamReport {
        if !self.config.enabled { return DreamReport::default(); }
        let inner = match inner { Some(i) => i.clone(), None => return DreamReport::default() };
        let mut report = DreamReport::default();
        let cycles = self.config.max_dream_cycles.min(10);
        for _ in 0..cycles {
            report.cycles_completed += 1;
            report.replayed_count += self.replay_random(&inner);
            report.reinforced_count += self.reinforce_weak(&inner);
            report.decayed_count += self.apply_decay(&inner);
        }
        self.cycle_count += 1; self.last_active = SystemTime::now(); report
    }

    fn replay_random(&self, inner: &Arc<RwLock<EngineInner>>) -> u32 {
        let mut replayed = 0u32;

        // Phase 1: read all data needed under read lock
        let mut tasks: Vec<(String, String, Vec<f32>, u64)> = Vec::new();
        {
            let engine = inner.read().unwrap_or_else(|e| e.into_inner());
            let now_ms = SystemTime::now().duration_since(std::time::UNIX_EPOCH).unwrap_or_default().as_millis() as u64;
            for tree in engine.trees.values() {
                let ids = match tree.storage.all_ids() { Ok(ids) => ids, Err(_) => continue };
                let sample_size = self.config.replay_sample_size.min(ids.len());
                for id in ids.iter().take(sample_size) {
                    let blob = match tree.storage.get_blob(id) { Ok(Some(b)) => b, _ => continue };
                    let encoded = engine.encoder.encode(&blob.text);
                    let q: Vec<f32> = encoded.dense.iter().map(|x: &half::f16| x.to_f32()).collect();
                    tasks.push((tree.name.clone(), id.clone(), q, now_ms));
                }
            }
        }

        // Phase 2: replay with plasticity under write lock
        let mut dirty: Vec<usize> = Vec::new();
        for (tree_name, _id, query_f32, now_ms) in &tasks {
            let mut engine = inner.write().unwrap_or_else(|e| e.into_inner());
            let tree = match engine.trees.get_mut(tree_name) { Some(t) => t, None => continue };
            let was_enabled = tree.hopfield.drift_enabled;
            tree.hopfield.enable_plasticity(true);
            let result = tree.hopfield.recall_with_plasticity(query_f32, *now_ms);
            tree.hopfield.enable_plasticity(was_enabled);
            let _ = tree;
            if let Some((_, _, indices)) = result {
                dirty.extend(indices);
                replayed += 1;
            }
        }
        if !dirty.is_empty() {
            let mut engine = inner.write().unwrap_or_else(|e| e.into_inner());
            for idx in dirty { engine.dirty_patterns.insert(idx); }
        }
        replayed
    }

    fn reinforce_weak(&self, inner: &Arc<RwLock<EngineInner>>) -> u32 {
        let mut reinforced = 0u32;
        let now_ms = SystemTime::now().duration_since(std::time::UNIX_EPOCH).unwrap_or_default().as_millis() as i64;

        struct Cand { id: String, cur: f32, score: f32 }
        let mut engine = inner.write().unwrap_or_else(|e| e.into_inner());
        for tree in engine.trees.values_mut() {
            let metas = match tree.storage.all_metas() { Ok(m) => m, Err(_) => continue };
            let mut candidates: Vec<Cand> = Vec::new();
            for (id, meta) in &metas {
                if meta.protection != 0 || meta.importance >= self.config.weak_importance_threshold { continue; }
                let (rc, _) = match tree.hopfield.get_access_stats(id) { Some(s) => s, None => continue };
                if rc < self.config.weak_recall_count_min { continue; }
                let days = ((now_ms - meta.created_at).max(0) as f64) / (24.0 * 3600.0 * 1000.0);
                let score = (rc as f32) * 0.5 + 1.0f32 / (1.0 + days as f32 / 30.0) * 0.3;
                if score > 0.0 { candidates.push(Cand { id: id.clone(), cur: meta.importance, score }); }
            }
            for c in &candidates {
                let boosted = (c.cur + self.config.importance_boost * c.score).min(1.0);
                if boosted > c.cur
                    && let Ok(Some(cur)) = tree.storage.get_meta(&c.id) {
                        let _ = tree.storage.update_meta(&c.id, &MetaRecord { importance: boosted, ..cur });
                        reinforced += 1;
                    }
            }
        }
        reinforced
    }

    fn apply_decay(&self, inner: &Arc<RwLock<EngineInner>>) -> u32 {
        let mut count = 0u32;
        let now_ms = SystemTime::now().duration_since(std::time::UNIX_EPOCH).unwrap_or_default().as_millis() as u64;
        let mut engine = inner.write().unwrap_or_else(|e| e.into_inner());
        for tree in engine.trees.values_mut() {
            let ids = tree.hopfield.apply_decay(now_ms);
            count += ids.len() as u32;
            for id in &ids {
                if let Ok(Some(m)) = tree.storage.get_meta(id) {
                    let _ = tree.storage.update_meta(id, &MetaRecord { is_dormant: true, ..m });
                }
            }
        }
        count
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test] fn test_config() { let c = DreamConfig::default(); assert!(c.enabled); assert_eq!(c.idle_timeout_secs, 300); }
    #[test] fn test_new() { let m = DreamMode::new(DreamConfig::default()); assert_eq!(m.cycle_count, 0); }
    #[test] fn test_activity() { let mut m = DreamMode::new(DreamConfig::default()); m.note_activity(); assert!(m.idle_secs() < 2); }
    #[test] fn test_should_dream_false() { let mut m = DreamMode::new(DreamConfig{idle_timeout_secs:3600,..Default::default()}); m.note_activity(); assert!(!m.should_dream()); }
    #[test] fn test_should_dream_disabled() { let mut m = DreamMode::new(DreamConfig{enabled:false,..Default::default()}); m.note_activity(); assert!(!m.should_dream()); }
    #[test] fn test_noop_without_engine() { let mut m = DreamMode::new(DreamConfig::default()); let r = m.dream(None); assert_eq!(r.cycles_completed, 0); }

    #[test] fn test_cycle_count() { let m = DreamMode::new(DreamConfig{enabled:false,..Default::default()}); assert_eq!(m.cycle_count, 0); }
    #[test] fn test_dream_updates_last_active() { let mut m = DreamMode::new(DreamConfig{idle_timeout_secs:1,..Default::default()}); m.last_active = SystemTime::now()-Duration::from_secs(10); assert!(m.should_dream()); }
    #[test] fn test_idle_secs() { let m = DreamMode::new(DreamConfig::default()); assert!(m.idle_secs() > 100_000); }
    #[test] fn test_reinforce_noop() { let mut m = DreamMode::new(DreamConfig::default()); let r = m.dream(None); assert_eq!(r.reinforced_count, 0); }
    #[test] fn test_decay_noop() { let mut m = DreamMode::new(DreamConfig::default()); let r = m.dream(None); assert_eq!(r.decayed_count, 0); }
}
