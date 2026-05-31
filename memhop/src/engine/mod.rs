//! MemHop engine — lightweight wrapper around LmdbStorage for v0.7.3+.

pub(crate) mod helpers;

use half::f16;

use crate::engram::Engram;
use crate::encoder::{Encoder, NgramEncoder};
use crate::error::{MemHopError, Result};
use crate::storage::LmdbStorage;
use crate::types::{PerceptionInput, RecallRequest, RecallResponse, RecallTrace};
use std::sync::{Arc, Mutex};
use std::collections::HashMap;
use std::time::Instant;

/// Growth statistics.
pub struct GrowthState {
    pub total_engrams_created: u64,
    #[allow(dead_code)]
    pub total_forgotten: u64,
    pub total_recalls: u64,
    pub dream_cycles: u64,
}

impl GrowthState {
    pub fn new() -> Self {
        GrowthState {
            total_engrams_created: 0,
            total_forgotten: 0,
            total_recalls: 0,
            dream_cycles: 0,
        }
    }
}

impl Default for GrowthState {
    fn default() -> Self { Self::new() }
}

/// Brain — the v0.7.3 public API for associative memory.
pub struct MemHop {
    pub(crate) storage: Arc<LmdbStorage>,
    pub(crate) growth: Arc<Mutex<GrowthState>>,
    pub(crate) closed: Arc<Mutex<bool>>,
}

impl MemHop {
    /// Open a Brain database at the given path. Creates it if it does not exist.
    pub fn open(path: &str) -> Result<Self> {
        let storage = LmdbStorage::open(path)
            .map_err(|e| MemHopError::Internal(format!("storage open: {}", e)))?;
        Ok(MemHop {
            storage: Arc::new(storage),
            growth: Arc::new(Mutex::new(GrowthState::new())),
            closed: Arc::new(Mutex::new(false)),
        })
    }

    /// Close the engine, flushing all pending writes.
    pub fn close(&self) -> Result<()> {
        let mut closed = self.closed.lock().map_err(|e| MemHopError::Internal(e.to_string()))?;
        *closed = true;
        self.storage.close()
            .map_err(|e| MemHopError::Internal(format!("close: {}", e)))
    }

    /// Store a perception (episode) into hippocampus.
    pub fn store(&self, input: &PerceptionInput) -> Result<String> {
        let mut wtxn = self.storage.begin_write()
            .map_err(|e| MemHopError::Internal(format!("write txn: {}", e)))?;
        let now = helpers::now_millis();
        let id = format!("e_{:020}", now);

        let engram = Engram::new_episode(
            id.clone(),
            input.content.clone(),
            input.vector.clone(),
            Vec::new(),
            input.emotional_state.valence,
            input.emotional_state.arousal,
            now,
        );

        self.storage.put_hippocampus(&mut wtxn, &id, &engram)
            .map_err(|e| MemHopError::Internal(format!("put: {}", e)))?;
        wtxn.commit()
            .map_err(|e| MemHopError::Internal(format!("commit: {}", e)))?;

        let mut growth = self.growth.lock().map_err(|e| MemHopError::Internal(e.to_string()))?;
        growth.total_engrams_created += 1;

        // Push to cortex (if configured)
        crate::cortex::push_to_cortex(&engram);

        Ok(id)
    }

    /// Recall by text or query vector — returns closest matches from hippocampus + neocortex.
    pub fn recall(&self, req: &RecallRequest) -> Result<RecallResponse> {
        let start = Instant::now();

        // 1. Query vector
        let query: Vec<f32> = match &req.query_vector {
            Some(v) => v.iter().map(|x| x.to_f32()).collect(),
            None => {
                let encoder = NgramEncoder::new(crate::engram::VECTOR_DIM);
                let encoded = encoder.encode(&req.query);
                encoded.dense.iter().map(|&x| x.to_f32()).collect()
            }
        };
        let query_f16: Vec<f16> = query.iter().map(|&x| f16::from_f32(x)).collect();

        // 2. Scan hippocampus + neocortex
        let rtxn = match self.storage.begin_read() {
            Ok(t) => t,
            Err(e) => return Err(MemHopError::Internal(format!("read txn: {}", e))),
        };

        let all = match self.storage.all_hippocampus_entries(&rtxn) {
            Ok(a) => a,
            Err(e) => return Err(MemHopError::Internal(format!("hippocampus entries: {}", e))),
        };

        let mut scored: Vec<(String, f32)> = all.iter()
            .filter(|(_, e)| e.kind == crate::engram::EngramKind::Episode)
            .filter(|(_, e)| !e.is_archived)
            .map(|(id, e)| {
                let sim = crate::hopfield::cosine_similarity_f16(&query_f16, &e.vector);
                (id.clone(), sim)
            })
            .collect();

        scored.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
        let top_k = if req.spread_top_k > 0 { req.spread_top_k } else { 5 };
        scored.truncate(top_k);

        let latency_us = start.elapsed().as_micros() as u64;
        let trace = RecallTrace {
            latency_us,
            gated_anchors: req.attention_anchors.clone(),
            hopfield_candidates: all.len(),
            spread_steps: 1,
            post_inhibition_count: scored.len(),
            pgt_layer: None,
        };

        let results: Vec<Engram> = scored.into_iter()
            .filter_map(|(id, _)| all.iter().find(|(i, _)| *i == id))
            .map(|(_, e)| e.clone())
            .collect();

        drop(rtxn);

        let mut growth = self.growth.lock().map_err(|e| MemHopError::Internal(e.to_string()))?;
        growth.total_recalls += 1;

        Ok(RecallResponse {
            working_memory: Vec::new(),
            associations: results,
            schemas: Vec::new(),
            emotional_echoes: Vec::new(),
            conflicts: Vec::new(),
            archive_results: None,
            hit_turns: Vec::new(),
            aggregated_sessions: Vec::new(),
            knowledge_memories: vec![],
            tree_contexts: vec![],
            graph_associations: vec![],
            worldview_context: vec![],
            cognitive_conflicts: vec![],
            trace,
        })
    }

    /// Count total engrams.
    pub fn count(&self) -> usize {
        let rtxn = match self.storage.begin_read() {
            Ok(t) => t,
            Err(_) => return 0,
        };
        self.storage.all_engram_ids(&rtxn).map(|v| v.len()).unwrap_or(0)
    }

    /// Statistics.
    pub fn stats(&self) -> HashMap<String, serde_json::Value> {
        let mut s = HashMap::new();
        let count = self.count();
        let growth = self.growth.lock().ok();
        s.insert("total_memories".to_string(), serde_json::Value::Number(count.into()));
        s.insert("total_recalls".to_string(), serde_json::Value::Number(
            growth.as_ref().map(|g| g.total_recalls).unwrap_or(0).into()
        ));
        s.insert("dream_cycles".to_string(), serde_json::Value::Number(
            growth.as_ref().map(|g| g.dream_cycles).unwrap_or(0).into()
        ));
        s
    }
}
