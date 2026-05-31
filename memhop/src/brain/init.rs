//! Brain initialization — open, warmup, rebuild.
//!
//! Extracted from brain.rs in v0.12.2.

use std::cell::RefCell;
use std::collections::HashMap;
use std::path::Path;
use std::sync::Arc;

use crate::engram::{EngramKind, VECTOR_DIM};
use crate::error::{MemHopError, Result};
use crate::hippocampus::Hippocampus;
use crate::hnsw::HnswIndex;
use crate::hopfield::ModernHopfield;
use crate::index::SparseIndex;
use crate::personality::GrowthState;
use crate::plan_gate::{PlanGate, PlanIndex};
use crate::storage::LmdbStorage;
use crate::storage::CURRENT_SCHEMA;
use crate::unified_graph::UnifiedGraph;

use crate::brain::{now_millis, Brain};
use crate::context::{ActiveContextSet, Phase};
use crate::cortex::Cortex;
use crate::encoder::{Encoder, NgramEncoder};
use crate::engram::EmotionalContext;
use crate::llm_provider::LlmProvider;
use crate::types::{BrainConfig, InnateSchema};

// ── VECTOR_DIM 来源于 engram ───────────────────────────────────

/// Open or create a Brain.
pub(crate) fn open(
    path: &str,
    config: BrainConfig,
    llm: Option<Box<dyn LlmProvider>>,
) -> Result<Brain> {
    let storage = Arc::new(
        LmdbStorage::open(path)
            .map_err(|e| MemHopError::Storage(e.to_string()))?,
    );

    // v0.11.0: Schema version check — must happen before any data deserialization.
    {
        let rtxn = storage.begin_read()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let ver: Option<String> = storage.get_config(&rtxn, "schema_version")
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        drop(rtxn);

        match ver {
            Some(ref v) if v == CURRENT_SCHEMA => {
                // Schema matches — proceed
            }
            Some(ref v) => {
                // Incompatible old version
                return Err(MemHopError::IncompatibleSchema {
                    found: v.clone(),
                    expected: CURRENT_SCHEMA,
                    hint: "This version is not backward compatible. Delete the old database or continue using v0.10.x.",
                });
            }
            None => {
                // New/empty database — write current version
                let mut wtxn = storage.begin_write()
                    .map_err(|e| MemHopError::Storage(e.to_string()))?;
                storage.put_config(&mut wtxn, "schema_version", &CURRENT_SCHEMA.to_string())
                    .map_err(|e| MemHopError::Storage(e.to_string()))?;
                wtxn.commit()
                    .map_err(|e| MemHopError::Storage(e.to_string()))?;
            }
        }
    }

    let personality = config.personality.clone();
    let hippocampus = Hippocampus::rebuild(&storage, config.hippocampus_capacity)
        .map_err(|e| MemHopError::Storage(e.to_string()))?;

    let graph = UnifiedGraph::rebuild(&storage)
        .map_err(|e| MemHopError::Storage(e.to_string()))?;

    let hopfield = rebuild_hopfield(&storage, config.hopfield.knowledge_pattern_weight)?;

    // v0.9.0: Engram cache for recall speed — bounded FIFO
    let engram_cache = RefCell::new(super::EngramCache::new(1000));

    // v0.9.0: Load or rebuild HNSW index from storage
    let mut counter: u64 = 0;
    let (mut hnsw, hnsw_id_map) = match HnswIndex::load_from_storage(&storage) {
        Ok(Some(idx)) => {
            let mut id_map = HashMap::new();
            if let Ok(txn) = storage.begin_read()
                && let Ok(entries) = storage.all_hippocampus_entries(&txn) {
                    for (id_str, _) in &entries {
                        let node_id = counter;
                        counter += 1;
                        id_map.insert(node_id, id_str.clone());
                    }
                }
            (idx, id_map)
        }
        _ => {
            let mut idx = HnswIndex::new(VECTOR_DIM);
            let mut id_map = HashMap::new();
            if let Ok(txn) = storage.begin_read()
                && let Ok(entries) = storage.all_hippocampus_entries(&txn) {
                    // v0.9.0: Pre-warm engram cache during HNSW rebuild
                    for (id_str, engram) in &entries {
                        let node_id = counter;
                        counter += 1;
                        id_map.insert(node_id, id_str.clone());
                        idx.insert(node_id, &engram.vector);
                        engram_cache.borrow_mut().insert(id_str.clone(), engram.clone());
                    }
                }
            (idx, id_map)
        }
    };

    // v0.11.0: Restore HNSW tombstones from LMDB config
    {
        if let Ok(rtxn) = storage.begin_read()
            && let Ok(Some(ids)) = storage.get_config::<Vec<u64>>(&rtxn, "hnsw_tombstones")
        {
            for id in ids {
                hnsw.tombstones.insert(id);
            }
        }
    }

    // v0.9.0: Rebuild sparse index from stored engrams
    let sparse_index = {
        let mut si = SparseIndex::new();
        if let Ok(txn) = storage.begin_read()
            && let Ok(entries) = storage.all_hippocampus_entries(&txn) {
                let tmp_enc = NgramEncoder::new(VECTOR_DIM);
                for (id_str, engram) in &entries {
                    let sparse = tmp_enc.encode(&engram.text).sparse;
                    si.add(id_str, &sparse, engram.text.chars().count());
                }
            }
        si
    };

    if hopfield.is_empty() && !config.innate_schemas.is_empty() {
        let now = now_millis();
        for innate in &config.innate_schemas {
            bootstrap_innate_schema(&storage, &graph, innate, now)?;
        }
    }

    // v0.8.0: Rebuild PlanIndex from stored PlanNodes
    let plan_index = {
        let mut pi = PlanIndex::new();
        let ro_txn = storage
            .begin_read()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        if let Ok(plans) = storage.get_all_plans(&ro_txn) {
            pi.rebuild(&plans);
        }
        pi
    };

    let hnsw_id_map_len = hnsw_id_map.len() as u64;

    // ── v0.12.1: Load Candle encoder — no ONNX, no Ngram fallback ──
    let encoder_loaded: bool;
    #[cfg(feature = "candle")]
    let candle_encoder: Option<crate::encoder::CandleEncoder>;
    #[cfg(not(feature = "candle"))]
    let candle_encoder: Option<crate::encoder::CandleEncoder> = None;

    if config.allow_fallback_encoder {
        #[allow(unused_assignments)]
        { candle_encoder = None; encoder_loaded = false; }
        eprintln!("memhop: no external model configured, using built-in NgramEncoder (allow_fallback_encoder=true).");
    } else {
        let model_path = config.onnx_model_path.as_ref().ok_or_else(|| {
            MemHopError::Internal(
                "Candle encoder required: set MEMHOP_ONNX_MODEL or BrainConfig.onnx_model_path".to_string()
            )
        })?;
        let safetensors_path = Path::new(model_path).join("model.safetensors");

        if !safetensors_path.exists() {
            return Err(MemHopError::Internal(format!(
                "Candle encoder requires model.safetensors in '{}'. File not found.",
                model_path
            )));
        }

        // Try loading with Candle
        #[cfg(feature = "candle")]
        {
            eprintln!("memhop: loading Candle encoder from '{}'...", model_path);
            let start = std::time::Instant::now();
            match crate::encoder::CandleEncoder::from_path(model_path) {
                Ok(enc) => {
                    let elapsed = start.elapsed();
                    eprintln!(
                        "memhop: Candle encoder ready (dim={}, {:.1}s)",
                        enc.dim(),
                        elapsed.as_secs_f64()
                    );
                    candle_encoder = Some(enc);
                    encoder_loaded = true;
                }
                Err(e) => {
                    #[allow(unused_assignments)]
                    { candle_encoder = None; encoder_loaded = false; }
                    return Err(MemHopError::Internal(format!(
                        "Candle encoder failed to load from '{}': {}. \
                         Check model files are valid. MemHop does NOT fall back to ONNX or Ngram.",
                        model_path, e
                    )));
                }
            }
        }
        #[cfg(not(feature = "candle"))]
        {
            return Err(MemHopError::Internal(format!(
                "Candle encoder feature is not enabled. Build with --features candle. \
                 MemHop does NOT support ONNX or Ngram fallback."
            )));
        }
    }

    if encoder_loaded {
        eprintln!("memhop: semantic encoder active — NgramEncoder disabled.");
    }

    // v0.10.0: Initialize reranker from config
    #[cfg(feature = "onnx")]
    let reranker = {
        if let Some(model_path) = &config.reranker_model_path {
            match crate::encoder::reranker::Reranker::from_path(model_path) {
                Ok(r) => Some(r),
                Err(e) => {
                    eprintln!(
                        "memhop: failed to load reranker from '{}': {}, reranker disabled",
                        model_path, e
                    );
                    None
                }
            }
        } else {
            None
        }
    };

    // v0.12.0: Initialize active context set
    let active_contexts = ActiveContextSet::new(
        config.max_active_contexts,
        config.context_match_threshold,
        config.context_half_life_hours,
    );

    let brain = Brain {
        cortex: Cortex::new(config.cortex_capacity),
        hippocampus,
        graph,
        hopfield,
        hnsw,
        sparse_index,
        hnsw_id_map,
        next_node_id: hnsw_id_map_len,
        storage,
        emotional_ctx: EmotionalContext::new(),
        growth: GrowthState::new(),
        personality,
        llm,
        ngram_encoder: NgramEncoder::new(VECTOR_DIM),
        #[cfg(feature = "candle")]
        candle_encoder,
        #[cfg(feature = "onnx")]
        reranker,
        plan_gate: PlanGate::new(
            config.plan_boundary_threshold.unwrap_or(0.55),
            3,
            24,
        ),
        plan_index: RefCell::new(plan_index),
        config,
        recalled_buffer: RefCell::new(Vec::new()),
        engram_cache,
        last_chunk_per_tree: HashMap::new(),
        last_perceive_at: 0,
        phase: Phase::Warmup,
        active_contexts,
    };

    // Warm up the encoder
    warmup_encoder(&brain);

    Ok(brain)
}

/// Warm up the ONNX/Candle encoder by encoding a short text.
pub(crate) fn warmup_encoder(brain: &Brain) {
    #[cfg(any(feature = "onnx", feature = "candle"))]
    {
        let warmup_start = std::time::Instant::now();
        let _ = brain.encode_text("warmup");
        let elapsed = warmup_start.elapsed();
        if elapsed.as_secs_f32() > 0.1 {
            eprintln!("memhop: encoder warmup completed in {:.1}s", elapsed.as_secs_f32());
        }
    }
}

/// Rebuild the Modern Hopfield network from stored engrams.
fn rebuild_hopfield(storage: &LmdbStorage, knowledge_weight: f32) -> Result<ModernHopfield> {
    let txn = storage
        .begin_read()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    let entries = storage
        .all_hippocampus_entries(&txn)
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    drop(txn);

    let mut hopfield = ModernHopfield::new(VECTOR_DIM, crate::brain::HOPFIELD_BETA);
    for (id, engram) in &entries {
        let weight = match engram.kind {
            EngramKind::Knowledge => knowledge_weight,
            _ => 1.0,
        };
        hopfield.add_pattern_weighted(id, &engram.vector, weight);
    }
    Ok(hopfield)
}

/// Bootstrap an innate schema from configuration.
fn bootstrap_innate_schema(
    _storage: &LmdbStorage,
    _graph: &UnifiedGraph,
    _innate: &InnateSchema,
    _now: i64,
) -> Result<()> {
    Ok(())
}
