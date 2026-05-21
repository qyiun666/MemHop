use half::f16;
use pyo3::prelude::*;
use pyo3::types::PyDict;
use pyo3::IntoPyObjectExt;
use std::collections::{HashMap, HashSet};
use std::sync::{Arc, RwLock};

use crate::types::{PyMemory, MemHopClosedError, MemHopError, Protection, VECTOR_DIM};
use crate::encoder::{NgramEncoder, Encoder};
use crate::storage::{LmdbStorage, BlobRecord, MetaRecord, StorageError};
use crate::hopfield::ModernHopfield;
use crate::index::SparseIndex;
use crate::meta_index::MetaIndex;
use crate::recall_strategies::{RecallScope, TimeRange, scope_to_candidates};

const INDEX_VERSION: u32 = 1;

fn rebuild_indices(
    encoder: &NgramEncoder,
    storage: &LmdbStorage,
) -> (SparseIndex, MetaIndex) {
    let mut sparse_index = SparseIndex::new();
    let mut meta_index = MetaIndex::new();
    let all_blobs = match storage.all_blobs() {
        Ok(blobs) => blobs,
        Err(_) => return (sparse_index, meta_index),
    };
    for (id, blob) in &all_blobs {
        let output = encoder.encode(&blob.text);
        sparse_index.add(id, &output.sparse);
        meta_index.add(id, &blob.meta);
    }
    (sparse_index, meta_index)
}



struct EngineInner {
    storage: LmdbStorage,
    encoder: NgramEncoder,
    encoder_mode: String,
    storage_path: String,
    hopfield: ModernHopfield,
    sparse_index: SparseIndex,
    meta_index: MetaIndex,
    confidence_threshold: f32,
    beta: f32,
    max_memories: u64,
    closed: bool,
}

// ── MemHopEngine ─────────────────────────────────────────

#[pyclass]
pub struct MemHopEngine {
    inner: Arc<RwLock<EngineInner>>,
}

// ── Helpers ──────────────────────────────────────────────

fn generate_memory_id() -> String {
    use rand::Rng;
    let mut rng = rand::thread_rng();
    let bytes: [u8; 6] = rng.r#gen();
    format!(
        "m_{:02x}{:02x}{:02x}{:02x}{:02x}{:02x}",
        bytes[0], bytes[1], bytes[2], bytes[3], bytes[4], bytes[5]
    )
}

fn now_millis() -> i64 {
    chrono::Utc::now().timestamp_millis()
}

fn millis_to_iso(millis: i64) -> String {
    chrono::DateTime::from_timestamp_millis(millis)
        .map(|dt| dt.to_rfc3339())
        .unwrap_or_default()
}

fn protection_to_u8(p: &Protection) -> u8 {
    match p {
        Protection::Normal => 0,
        Protection::Protected => 1,
        Protection::Permanent => 2,
    }
}

fn u8_to_protection(v: u8) -> Protection {
    match v {
        0 => Protection::Normal,
        1 => Protection::Protected,
        2 => Protection::Permanent,
        _ => Protection::Normal,
    }
}

fn parse_datetime_to_millis(s: &str) -> Result<i64, PyErr> {
    if let Ok(dt) = chrono::DateTime::parse_from_rfc3339(s) {
        return Ok(dt.timestamp_millis());
    }
    if let Ok(naive) = chrono::NaiveDateTime::parse_from_str(s, "%Y-%m-%dT%H:%M:%S") {
        return Ok(naive.and_utc().timestamp_millis());
    }
    if let Ok(naive) = chrono::NaiveDateTime::parse_from_str(s, "%Y-%m-%dT%H:%M:%S%.f") {
        return Ok(naive.and_utc().timestamp_millis());
    }
    if let Ok(naive) = chrono::NaiveDate::parse_from_str(s, "%Y-%m-%d") {
        let dt = naive.and_hms_opt(0, 0, 0).unwrap().and_utc();
        return Ok(dt.timestamp_millis());
    }
    Err(MemHopError::new_err(format!(
        "Invalid datetime format: '{}'. Expected ISO 8601 (e.g. 2024-01-01T00:00:00Z)",
        s
    )))
}

fn extract_protection(json_meta: &HashMap<String, serde_json::Value>) -> Protection {
    if let Some(serde_json::Value::String(s)) = json_meta.get("protection") {
        match s.as_str() {
            "protected" => Protection::Protected,
            "permanent" => Protection::Permanent,
            _ => Protection::Normal,
        }
    } else {
        Protection::Normal
    }
}

fn extract_is_dormant(json_meta: &HashMap<String, serde_json::Value>) -> bool {
    json_meta
        .get("is_dormant")
        .and_then(|v| v.as_bool())
        .unwrap_or(false)
}

fn extract_importance(json_meta: &HashMap<String, serde_json::Value>) -> f32 {
    json_meta
        .get("importance")
        .and_then(|v| v.as_f64())
        .unwrap_or(0.5) as f32
}

fn extract_importance_decay_rate(json_meta: &HashMap<String, serde_json::Value>) -> Option<f32> {
    json_meta
        .get("importance_decay_rate")
        .and_then(|v| v.as_f64())
        .map(|v| v as f32)
}

/// Compute effective importance considering time decay.
/// effective = importance × decay_rate^(days_elapsed)
fn effective_importance(meta: &MetaRecord, now_ms: i64) -> f32 {
    match meta.importance_decay_rate {
        Some(rate) => {
            let elapsed_ms = (now_ms - meta.created_at).max(0);
            let days = elapsed_ms as f64 / (24.0 * 3600.0 * 1000.0);
            meta.importance * rate.powf(days as f32)
        }
        None => meta.importance,
    }
}

fn storage_to_py(err: StorageError) -> PyErr {
    MemHopError::new_err(err.to_string())
}

/// Convert f16 dense vector to f32 for Hopfield query.
fn f16_to_f32(v: &[f16]) -> Vec<f32> {
    v.iter().map(|x| x.to_f32()).collect()
}
use crate::python_conv::*;

fn check_closed(inner: &EngineInner) -> PyResult<()> {
    if inner.closed {
        return Err(MemHopClosedError::new_err("MemHop engine is closed"));
    }
    Ok(())
}

// ── Search filter types ────────────────────────────────────

struct FilterCriteria {
    layer: Option<String>,
    r#type: Option<String>,
    domain: Option<String>,
    is_dormant: Option<bool>,
    protection: Option<String>,
    session_id: Option<String>,
    path: Option<String>,
    parent: Option<String>,
    importance_gt: Option<f32>,
    importance_lt: Option<f32>,
    tags_contains: Option<String>,
    connections_to: Option<String>,
}

fn parse_filters(filters: &Bound<'_, PyDict>) -> PyResult<FilterCriteria> {
    let mut c = FilterCriteria {
        layer: None,
        r#type: None,
        domain: None,
        is_dormant: None,
        protection: None,
        session_id: None,
        path: None,
        parent: None,
        importance_gt: None,
        importance_lt: None,
        tags_contains: None,
        connections_to: None,
    };

    for (key, val) in filters.iter() {
        let key_str: String = key.extract()?;
        match key_str.as_str() {
            "layer" => c.layer = Some(val.extract()?),
            "type" => c.r#type = Some(val.extract()?),
            "domain" => c.domain = Some(val.extract()?),
            "is_dormant" => c.is_dormant = Some(val.extract()?),
            "protection" => c.protection = Some(val.extract()?),
            "session_id" => c.session_id = Some(val.extract()?),
            "path" => c.path = Some(val.extract()?),
            "parent" => c.parent = Some(val.extract()?),
            "importance_gt" => c.importance_gt = Some(val.extract::<f64>()? as f32),
            "importance_lt" => c.importance_lt = Some(val.extract::<f64>()? as f32),
            "tags_contains" => c.tags_contains = Some(val.extract()?),
            "connections_to" => c.connections_to = Some(val.extract()?),
            other => {
                return Err(MemHopError::new_err(format!(
                    "Unknown filter key: '{}'",
                    other
                )));
            }
        }
    }
    Ok(c)
}

fn protection_str_to_u8(s: &str) -> u8 {
    match s {
        "protected" => 1,
        "permanent" => 2,
        _ => 0,
    }
}

fn matches_filters(blob: &BlobRecord, meta: &MetaRecord, criteria: &FilterCriteria) -> bool {
    if let Some(ref layer) = criteria.layer {
        match blob.meta.get("layer") {
            Some(serde_json::Value::String(v)) if v == layer => {}
            _ => return false,
        }
    }
    if let Some(ref t) = criteria.r#type {
        match blob.meta.get("type") {
            Some(serde_json::Value::String(v)) if v == t => {}
            _ => return false,
        }
    }
    if let Some(ref domain) = criteria.domain {
        match blob.meta.get("domain") {
            Some(serde_json::Value::String(v)) if v == domain => {}
            _ => return false,
        }
    }
    if let Some(ref session_id) = criteria.session_id {
        match blob.meta.get("session_id") {
            Some(serde_json::Value::String(v)) if v == session_id => {}
            _ => return false,
        }
    }
    if let Some(ref path) = criteria.path {
        match blob.meta.get("path") {
            Some(serde_json::Value::String(v)) if v == path => {}
            _ => return false,
        }
    }
    if let Some(ref parent) = criteria.parent {
        match blob.meta.get("parent") {
            Some(serde_json::Value::String(v)) if v == parent => {}
            _ => return false,
        }
    }

    if let Some(dormant) = criteria.is_dormant
        && meta.is_dormant != dormant {
            return false;
        }

    if let Some(ref prot_str) = criteria.protection
        && meta.protection != protection_str_to_u8(prot_str) {
            return false;
        }

    if let Some(gt) = criteria.importance_gt
        && meta.importance <= gt {
            return false;
        }
    if let Some(lt) = criteria.importance_lt
        && meta.importance >= lt {
            return false;
        }

    if let Some(ref tag) = criteria.tags_contains {
        match blob.meta.get("tags") {
            Some(serde_json::Value::Array(tags)) => {
                if !tags.iter().any(|t| t.as_str() == Some(tag.as_str())) {
                    return false;
                }
            }
            _ => return false,
        }
    }

    if let Some(ref target) = criteria.connections_to {
        match blob.meta.get("connections") {
            Some(serde_json::Value::Array(conns)) => {
                if !conns.iter().any(|c| {
                    c.get("to").and_then(|t| t.as_str()) == Some(target.as_str())
                }) {
                    return false;
                }
            }
            _ => return false,
        }
    }

    true
}

/// Parse a Python scope dict into RecallScope.
fn parse_recall_scope(scope_dict: &HashMap<String, PyObject>, py: Python<'_>) -> RecallScope {
    let mut scope = RecallScope::default();

    for (k, v) in scope_dict {
        match k.as_str() {
            "domain" | "layer" | "knowledge_tree" | "session_id" => {
                if let Ok(s) = v.bind(py).extract::<String>() {
                    match k.as_str() {
                        "domain" => scope.domain = Some(s),
                        "layer" => scope.layer = Some(s),
                        "knowledge_tree" => scope.knowledge_tree = Some(s),
                        "session_id" => scope.session_id = Some(s),
                        _ => {}
                    }
                }
            }
            "time_range" => {
                if let Ok(tr_dict) = v.bind(py).downcast::<PyDict>() {
                    let mut after_ms: Option<i64> = None;
                    let mut before_ms: Option<i64> = None;

                    if let Ok(Some(val)) = tr_dict.get_item("hours")
                        && let Ok(hours) = val.extract::<f64>() {
                            after_ms = Some(now_millis() - (hours * 3600.0 * 1000.0) as i64);
                        }
                    if let Ok(Some(val)) = tr_dict.get_item("days")
                        && let Ok(days) = val.extract::<f64>() {
                            let threshold = now_millis() - (days * 86400.0 * 1000.0) as i64;
                            if after_ms.is_none_or(|a| threshold > a) {
                                after_ms = Some(threshold);
                            }
                        }
                    if let Ok(Some(val)) = tr_dict.get_item("after")
                        && let Ok(s) = val.extract::<String>()
                            && let Ok(ms) = parse_datetime_to_millis(&s) {
                                after_ms = Some(ms);
                            }
                    if let Ok(Some(val)) = tr_dict.get_item("before")
                        && let Ok(s) = val.extract::<String>()
                            && let Ok(ms) = parse_datetime_to_millis(&s) {
                                before_ms = Some(ms);
                            }

                    if after_ms.is_some() || before_ms.is_some() {
                        scope.time_range = Some(TimeRange { after_ms, before_ms });
                    }
                }
            }
            _ => {}
        }
    }

    scope
}


// ── pymethods ────────────────────────────────────────────

#[pymethods]
impl MemHopEngine {
    #[new]
    #[pyo3(signature = (path="memhop.db", encoder="ngram", confidence_threshold=0.7, beta=8.0, max_memories=1_000_000, timezone="UTC"))]
    fn new(
        path: &str,
        encoder: &str,
        confidence_threshold: f64,
        beta: f64,
        max_memories: u64,
        timezone: &str,
    ) -> PyResult<Self> {
        let encoder_mode = encoder.to_string();
        let _tz = timezone.to_string();

        if encoder != "ngram" {
            return Err(MemHopError::new_err(format!(
                "Unsupported encoder: '{}'. Currently only 'ngram' is supported.",
                encoder
            )));
        }

        let storage = LmdbStorage::open(path).map_err(storage_to_py)?;
        let ngram_encoder = NgramEncoder::default_encoder();
        let mut hopfield = ModernHopfield::new(VECTOR_DIM, beta as f32);

        // Startup recovery: load all existing patterns into hopfield
        let all_patterns = storage.all_patterns().map_err(storage_to_py)?;
        for (id, pattern) in &all_patterns {
            hopfield.add_pattern(id, pattern);
        }

        // Try to load cached indices (sparse + meta + version)
        let keepsake_sparse = storage.load_index("sparse").unwrap_or(None);
        let cached_meta = storage.load_index("meta").unwrap_or(None);
        let cached_version = storage.load_index("version").unwrap_or(None);

        let (sparse_index, meta_index) = if let (
            Some(sparse_data),
            Some(meta_data),
            Some(version_data),
        ) = (&keepsake_sparse, &cached_meta, &cached_version)
        {
            let stored_version: u32 = bincode::deserialize(version_data).unwrap_or(0);
            if stored_version == INDEX_VERSION {
                if let Some(si) = SparseIndex::from_bytes(sparse_data) {
                    if let Some(mi) = MetaIndex::from_bytes(meta_data) {
                        (si, mi)
                    } else {
                        rebuild_indices(&ngram_encoder, &storage)
                    }
                } else {
                    rebuild_indices(&ngram_encoder, &storage)
                }
            } else {
                rebuild_indices(&ngram_encoder, &storage)
            }
        } else {
            rebuild_indices(&ngram_encoder, &storage)
        };

        let inner = EngineInner {
            storage,
            encoder: ngram_encoder,
            encoder_mode,
            storage_path: path.to_string(),
            hopfield,
            sparse_index,
            meta_index,
            confidence_threshold: confidence_threshold as f32,
            beta: beta as f32,
            max_memories,
            closed: false,
        };

        Ok(MemHopEngine {
            inner: Arc::new(RwLock::new(inner)),
        })
    }

    #[pyo3(signature = (text, meta=None, memory_id=None, content_type=None, blob=None))]
    fn remember(
        &self,
        py: Python<'_>,
        text: &str,
        meta: Option<HashMap<String, PyObject>>,
        memory_id: Option<String>,
        content_type: Option<String>,
        blob: Option<Vec<u8>>,
    ) -> PyResult<String> {
        let mut inner = self.inner.write().unwrap();
        check_closed(&inner)?;

        let output = inner.encoder.encode(text);

        let json_meta = if let Some(ref m) = meta {
            pydict_to_json_map(m, py)
        } else {
            HashMap::new()
        };

        let importance = extract_importance(&json_meta);
        let importance_decay_rate = extract_importance_decay_rate(&json_meta);
        let protection = extract_protection(&json_meta);
        let is_dormant = extract_is_dormant(&json_meta);

        let dedup_key: Option<String> = json_meta
            .get("key")
            .and_then(|v| v.as_str())
            .map(|s| s.to_string());

        let id = if let Some(ref key_val) = dedup_key {
            if let Some(existing_id) = inner.storage.find_by_key(key_val).map_err(storage_to_py)? {
                // Remove old meta index entries before overwriting
                if let Some(old_blob) = inner.storage.get_blob(&existing_id).map_err(storage_to_py)? {
                    inner.meta_index.remove(&existing_id, &old_blob.meta);
                }
                inner.hopfield.remove_pattern(&existing_id);
                inner.sparse_index.remove(&existing_id);
                inner.storage.delete(&existing_id).map_err(storage_to_py)?;
                existing_id
            } else {
                memory_id.unwrap_or_else(generate_memory_id)
            }
        } else {
            memory_id.unwrap_or_else(generate_memory_id)
        };

        let meta_record = MetaRecord {
            created_at: now_millis(),
            importance,
            protection: protection_to_u8(&protection),
            is_dormant,
            key: dedup_key,
            importance_decay_rate,
        };

        let blob_record = BlobRecord {
            text: text.to_string(),
            meta: json_meta.clone(),
            content_type: content_type.clone(),
            blob_data: blob.clone(),
        };

        inner
            .storage
            .put(&id, &output.dense, &blob_record, &meta_record)
            .map_err(storage_to_py)?;
        inner.hopfield.add_pattern(&id, &output.dense);
        inner.sparse_index.add(&id, &output.sparse);
        inner.meta_index.add(&id, &json_meta);

        let count = inner.storage.count().map_err(storage_to_py)?;
        if count > inner.max_memories {
            evict_oldest(&mut inner)?;
        }

        Ok(id)
    }

    #[pyo3(signature = (cue, *, include_blob = true, scope = None, time_alpha = 0.0, importance_alpha = 0.0))]
    fn recall(&self, cue: &str, py: Python<'_>, include_blob: bool, scope: Option<HashMap<String, PyObject>>, time_alpha: f64, importance_alpha: f64) -> PyResult<Option<PyMemory>> {
        let inner = self.inner.read().unwrap();
        check_closed(&inner)?;

        let output = inner.encoder.encode(cue);
        let query_f32 = f16_to_f32(&output.dense);

        let n = inner.hopfield.len();
        if n == 0 {
            return Ok(None);
        }

        let comprehensive = time_alpha > 0.0 || importance_alpha > 0.0;
        let ta = time_alpha as f32;
        let ia = importance_alpha as f32;

        // Resolve scope to candidate set, if any
        let scope_candidates = if let Some(ref scope_dict) = scope {
            let rc = parse_recall_scope(scope_dict, py);
            scope_to_candidates(&rc, &inner.meta_index, &inner.storage, now_millis())
                .map_err(storage_to_py)?
        } else {
            None
        };

        // Early exit for empty scope
        if let Some(ref scoped_ids) = scope_candidates
            && scoped_ids.is_empty() {
                return Ok(None);
            }

        // Pre-build candidate list for comprehensive recall
        let comprehensive_ids: Option<Vec<String>> = if comprehensive {
            let ids = if let Some(ref scoped_ids) = scope_candidates {
                scoped_ids.iter().cloned().collect()
            } else if n <= 500 {
                inner.storage.all_ids().map_err(storage_to_py)?
            } else {
                let max_candidates = 500.min(n);
                inner.sparse_index.search(&output.sparse, max_candidates)
            };
            if ids.is_empty() {
                return Ok(None);
            }
            Some(ids)
        } else {
            None
        };

        let recall_result = py.allow_threads(|| {
            if let Some(ref cand_ids) = comprehensive_ids {
                let refs: Vec<&str> = cand_ids.iter().map(|s| s.as_str()).collect();
                let mut raw = inner.hopfield.recall_among_raw(&query_f32, &refs);
                let now = now_millis();

                for (id, score) in &mut raw {
                    if let Ok(Some(meta)) = inner.storage.get_meta(id) {
                        if ta != 0.0 {
                            let days = ((now - meta.created_at).max(0) as f64) / (24.0 * 3600.0 * 1000.0);
                            let recency = 1.0f32 / (1.0 + days as f32 / 30.0);
                            *score += ta * recency;
                        }
                        if ia != 0.0 {
                            *score += ia * effective_importance(&meta, now);
                        }
                    }
                }

                if raw.is_empty() {
                    return None;
                }

                let sims: Vec<f32> = raw.iter().map(|(_, s)| *s).collect();
                let max_s = sims.iter().cloned().fold(f32::NEG_INFINITY, f32::max);
                let exps: Vec<f32> = sims.iter().map(|&s| (s - max_s).exp()).collect();
                let sum: f32 = exps.iter().sum();
                if sum == 0.0 {
                    return None;
                }
                let weights: Vec<f32> = exps.iter().map(|&e| e / sum).collect();
                let (best_idx, _) = weights.iter().enumerate()
                    .max_by(|(_, a), (_, b)| a.partial_cmp(b).unwrap())?;
                Some((raw[best_idx].0.clone(), weights[best_idx]))
            } else if let Some(ref scoped_ids) = scope_candidates {
                let refs: Vec<&str> = scoped_ids.iter().map(|s| s.as_str()).collect();
                inner.hopfield.recall_among(&query_f32, &refs)
            } else {
                if n <= 500 {
                    inner.hopfield.recall(&query_f32)
                } else {
                    let max_candidates = 500.min(n);
                    let candidates = inner.sparse_index.search(&output.sparse, max_candidates);

                    if candidates.is_empty() {
                        inner.hopfield.recall(&query_f32)
                    } else {
                        let candidate_refs: Vec<&str> = candidates.iter().map(|s| s.as_str()).collect();
                        inner.hopfield.recall_among(&query_f32, &candidate_refs)
                    }
                }
            }
        });

        match recall_result {
            Some((id, confidence)) => {
                if confidence < inner.confidence_threshold {
                    return Ok(None);
                }
                let meta_rec = inner.storage.get_meta(&id).map_err(storage_to_py)?;
                match meta_rec {
                    Some(meta_rec) if !meta_rec.is_dormant => {
                        let blob = inner.storage.get_blob(&id).map_err(storage_to_py)?;
                        if let Some(blob) = blob {
                            let py_meta = json_map_to_pydict(py, &blob.meta);
                            return Ok(Some(PyMemory::create(
                                id,
                                blob.text,
                                py_meta,
                                confidence as f64,
                                millis_to_iso(meta_rec.created_at),
                                blob.content_type.clone(),
                                if include_blob { blob.blob_data.clone() } else { None },
                            )));
                        }
                    }
                    _ => {}
                }
                Ok(None)
            }
            None => Ok(None),
        }
    }

    #[pyo3(signature = (cue, k=5, *, include_blob = true, scope = None, time_alpha = 0.0, importance_alpha = 0.0))]
    fn recall_topk(&self, cue: &str, k: usize, py: Python<'_>, include_blob: bool, scope: Option<HashMap<String, PyObject>>, time_alpha: f64, importance_alpha: f64) -> PyResult<Vec<PyMemory>> {
        let inner = self.inner.read().unwrap();
        check_closed(&inner)?;

        let output = inner.encoder.encode(cue);
        let query_f32 = f16_to_f32(&output.dense);

        let comprehensive = time_alpha > 0.0 || importance_alpha > 0.0;
        let ta = time_alpha as f32;
        let ia = importance_alpha as f32;

        // Resolve scope to candidate set, if any
        let scope_candidates = if let Some(ref scope_dict) = scope {
            let rc = parse_recall_scope(scope_dict, py);
            scope_to_candidates(&rc, &inner.meta_index, &inner.storage, now_millis())
                .map_err(storage_to_py)?
        } else {
            None
        };

        // Early exit for empty scope
        if let Some(ref scoped_ids) = scope_candidates
            && scoped_ids.is_empty() {
                return Ok(Vec::new());
            }

        let lookup_k = (k * 3).max(20);

        // Pre-build candidate list for comprehensive recall
        let comprehensive_ids: Option<Vec<String>> = if comprehensive {
            let ids = if let Some(ref scoped_ids) = scope_candidates {
                scoped_ids.iter().cloned().collect()
            } else {
                let n = inner.hopfield.len();
                let max_candidates = 2000.min(n);
                inner.sparse_index.search(&output.sparse, max_candidates)
            };
            if ids.is_empty() {
                return Ok(Vec::new());
            }
            Some(ids)
        } else {
            None
        };

        let results = py.allow_threads(|| {
            if let Some(ref cand_ids) = comprehensive_ids {
                let refs: Vec<&str> = cand_ids.iter().map(|s| s.as_str()).collect();
                let mut raw = inner.hopfield.recall_among_raw(&query_f32, &refs);
                let now = now_millis();

                for (id, score) in &mut raw {
                    if let Ok(Some(meta)) = inner.storage.get_meta(id) {
                        if ta != 0.0 {
                            let days = ((now - meta.created_at).max(0) as f64) / (24.0 * 3600.0 * 1000.0);
                            let recency = 1.0f32 / (1.0 + days as f32 / 30.0);
                            *score += ta * recency;
                        }
                        if ia != 0.0 {
                            *score += ia * effective_importance(&meta, now);
                        }
                    }
                }

                raw.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap());
                raw.truncate(lookup_k);
                // Softmax
                if !raw.is_empty() {
                    let sims: Vec<f32> = raw.iter().map(|(_, s)| *s).collect();
                    let max_s = sims.iter().cloned().fold(f32::NEG_INFINITY, f32::max);
                    let exps: Vec<f32> = sims.iter().map(|&s| (s - max_s).exp()).collect();
                    let sum: f32 = exps.iter().sum();
                    if sum > 0.0 {
                        for (i, (_, conf)) in raw.iter_mut().enumerate() {
                            *conf = exps[i] / sum;
                        }
                    }
                }
                raw
            } else if let Some(ref scoped_ids) = scope_candidates {
                let refs: Vec<&str> = scoped_ids.iter().map(|s| s.as_str()).collect();
                let mut raw = inner.hopfield.recall_among_raw(&query_f32, &refs);
                raw.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap());
                raw.truncate(lookup_k);
                if !raw.is_empty() {
                    let max_s = raw.iter().map(|(_, s)| *s).fold(f32::NEG_INFINITY, f32::max);
                    let exps: Vec<f32> = raw.iter().map(|(_, s)| (s - max_s).exp()).collect();
                    let sum: f32 = exps.iter().sum();
                    if sum > 0.0 {
                        for (i, (_, conf)) in raw.iter_mut().enumerate() {
                            *conf = exps[i] / sum;
                        }
                    }
                }
                raw
            } else {
                inner.hopfield.recall_topk(&query_f32, lookup_k)
            }
        });

        let mut memories = Vec::with_capacity(k);
        for (id, confidence) in results {
            if memories.len() >= k {
                break;
            }
            if confidence < inner.confidence_threshold {
                break;
            }
            let meta_rec = inner.storage.get_meta(&id).map_err(storage_to_py)?;
            match meta_rec {
                Some(meta_rec) if !meta_rec.is_dormant => {
                    let blob = inner.storage.get_blob(&id).map_err(storage_to_py)?;
                    if let Some(blob) = blob {
                        let py_meta = json_map_to_pydict(py, &blob.meta);
                        memories.push(PyMemory::create(
                            id,
                            blob.text,
                            py_meta,
                            confidence as f64,
                            millis_to_iso(meta_rec.created_at),
                            blob.content_type.clone(),
                            if include_blob { blob.blob_data.clone() } else { None },
                        ));
                    }
                }
                _ => continue,
            }
        }

        Ok(memories)
    }

    /// Fuse multiple cues via weighted averaging for multi-aspect recall.
    #[pyo3(signature = (cues, *, weights = None, include_blob = true, scope = None, time_alpha = 0.0, importance_alpha = 0.0))]
    fn fuse_recall(&self, cues: Vec<String>, py: Python<'_>, weights: Option<Vec<f64>>, include_blob: bool, scope: Option<HashMap<String, PyObject>>, time_alpha: f64, importance_alpha: f64) -> PyResult<Option<PyMemory>> {
        let inner = self.inner.read().unwrap();
        check_closed(&inner)?;

        if cues.is_empty() {
            return Err(MemHopError::new_err("fuse_recall: at least one cue required"));
        }

        // Normalize weights
        let w: Vec<f32> = match weights {
            Some(ref wv) if wv.len() == cues.len() => {
                let sum: f64 = wv.iter().sum();
                if sum <= 0.0 {
                    return Err(MemHopError::new_err("fuse_recall: weights sum must be positive"));
                }
                wv.iter().map(|x| (*x / sum) as f32).collect()
            }
            _ => vec![1.0f32 / cues.len() as f32; cues.len()],
        };

        // Encode each cue and fuse via weighted average
        let vd = VECTOR_DIM;
        let mut fused = vec![0.0f32; vd];
        for (i, cue) in cues.iter().enumerate() {
            let output = inner.encoder.encode(cue);
            for j in 0..vd {
                fused[j] += w[i] * output.dense[j].to_f32();
            }
        }

        // L2-normalize the fused query
        let norm: f32 = fused.iter().map(|x| x * x).sum::<f32>().sqrt();
        if norm > 0.0 {
            for x in &mut fused {
                *x /= norm;
            }
        }

        let n = inner.hopfield.len();
        if n == 0 {
            return Ok(None);
        }

        let comprehensive = time_alpha > 0.0 || importance_alpha > 0.0;
        let ta = time_alpha as f32;
        let ia = importance_alpha as f32;

        // Resolve scope
        let scope_candidates = if let Some(ref scope_dict) = scope {
            let rc = parse_recall_scope(scope_dict, py);
            scope_to_candidates(&rc, &inner.meta_index, &inner.storage, now_millis())
                .map_err(storage_to_py)?
        } else {
            None
        };

        if let Some(ref scoped_ids) = scope_candidates
            && scoped_ids.is_empty() {
                return Ok(None);
            }

        // Pre-build candidate list for comprehensive mode
        let comprehensive_ids: Option<Vec<String>> = if comprehensive {
            let ids = if let Some(ref scoped_ids) = scope_candidates {
                scoped_ids.iter().cloned().collect()
            } else if n <= 500 {
                inner.storage.all_ids().map_err(storage_to_py)?
            } else {
                let encoded = inner.encoder.encode(&cues[0]);
                let max_c = 500.min(n);
                inner.sparse_index.search(&encoded.sparse, max_c)
            };
            if ids.is_empty() {
                return Ok(None);
            }
            Some(ids)
        } else {
            None
        };

        let recall_result = py.allow_threads(|| {
            if let Some(ref cand_ids) = comprehensive_ids {
                let refs: Vec<&str> = cand_ids.iter().map(|s| s.as_str()).collect();
                let mut raw = inner.hopfield.recall_among_raw(&fused, &refs);
                let now = now_millis();

                for (id, score) in &mut raw {
                    if let Ok(Some(meta)) = inner.storage.get_meta(id) {
                        if ta != 0.0 {
                            let days = ((now - meta.created_at).max(0) as f64) / (24.0 * 3600.0 * 1000.0);
                            let recency = 1.0f32 / (1.0 + days as f32 / 30.0);
                            *score += ta * recency;
                        }
                        if ia != 0.0 {
                            *score += ia * effective_importance(&meta, now);
                        }
                    }
                }

                if raw.is_empty() { return None; }

                let sims: Vec<f32> = raw.iter().map(|(_, s)| *s).collect();
                let max_s = sims.iter().cloned().fold(f32::NEG_INFINITY, f32::max);
                let exps: Vec<f32> = sims.iter().map(|&s| (s - max_s).exp()).collect();
                let sum: f32 = exps.iter().sum();
                if sum == 0.0 { return None; }
                let weights: Vec<f32> = exps.iter().map(|&e| e / sum).collect();
                let (best_idx, _) = weights.iter().enumerate()
                    .max_by(|(_, a), (_, b)| a.partial_cmp(b).unwrap())?;
                Some((raw[best_idx].0.clone(), weights[best_idx]))
            } else if let Some(ref scoped_ids) = scope_candidates {
                let refs: Vec<&str> = scoped_ids.iter().map(|s| s.as_str()).collect();
                inner.hopfield.recall_among(&fused, &refs)
            } else {
                if n <= 500 {
                    inner.hopfield.recall(&fused)
                } else {
                    let encoded = inner.encoder.encode(&cues[0]);
                    let max_c = 500.min(n);
                    let candidates = inner.sparse_index.search(&encoded.sparse, max_c);
                    if candidates.is_empty() {
                        inner.hopfield.recall(&fused)
                    } else {
                        let candidate_refs: Vec<&str> = candidates.iter().map(|s| s.as_str()).collect();
                        inner.hopfield.recall_among(&fused, &candidate_refs)
                    }
                }
            }
        });

        match recall_result {
            Some((id, confidence)) => {
                if confidence < inner.confidence_threshold {
                    return Ok(None);
                }
                let meta_rec = inner.storage.get_meta(&id).map_err(storage_to_py)?;
                match meta_rec {
                    Some(meta_rec) if !meta_rec.is_dormant => {
                        let blob = inner.storage.get_blob(&id).map_err(storage_to_py)?;
                        if let Some(blob) = blob {
                            let py_meta = json_map_to_pydict(py, &blob.meta);
                            return Ok(Some(PyMemory::create(
                                id,
                                blob.text,
                                py_meta,
                                confidence as f64,
                                millis_to_iso(meta_rec.created_at),
                                blob.content_type.clone(),
                                if include_blob { blob.blob_data.clone() } else { None },
                            )));
                        }
                    }
                    _ => {}
                }
                Ok(None)
            }
            None => Ok(None),
        }
    }

    /// Fuse multiple cues for top-K recall.
    #[pyo3(signature = (cues, k=5, *, weights = None, include_blob = true, scope = None, time_alpha = 0.0, importance_alpha = 0.0))]
    fn fuse_recall_topk(&self, cues: Vec<String>, k: usize, py: Python<'_>, weights: Option<Vec<f64>>, include_blob: bool, scope: Option<HashMap<String, PyObject>>, time_alpha: f64, importance_alpha: f64) -> PyResult<Vec<PyMemory>> {
        let inner = self.inner.read().unwrap();
        check_closed(&inner)?;

        if cues.is_empty() {
            return Err(MemHopError::new_err("fuse_recall_topk: at least one cue required"));
        }

        // Normalize weights
        let w: Vec<f32> = match weights {
            Some(ref wv) if wv.len() == cues.len() => {
                let sum: f64 = wv.iter().sum();
                if sum <= 0.0 {
                    return Err(MemHopError::new_err("fuse_recall_topk: weights sum must be positive"));
                }
                wv.iter().map(|x| (*x / sum) as f32).collect()
            }
            _ => vec![1.0f32 / cues.len() as f32; cues.len()],
        };

        // Encode and fuse
        let vd = VECTOR_DIM;
        let mut fused = vec![0.0f32; vd];
        for (i, cue) in cues.iter().enumerate() {
            let output = inner.encoder.encode(cue);
            for j in 0..vd {
                fused[j] += w[i] * output.dense[j].to_f32();
            }
        }

        let norm: f32 = fused.iter().map(|x| x * x).sum::<f32>().sqrt();
        if norm > 0.0 {
            for x in &mut fused { *x /= norm; }
        }

        let n = inner.hopfield.len();
        let comprehensive = time_alpha > 0.0 || importance_alpha > 0.0;
        let ta = time_alpha as f32;
        let ia = importance_alpha as f32;

        let scope_candidates = if let Some(ref scope_dict) = scope {
            let rc = parse_recall_scope(scope_dict, py);
            scope_to_candidates(&rc, &inner.meta_index, &inner.storage, now_millis())
                .map_err(storage_to_py)?
        } else {
            None
        };

        if let Some(ref scoped_ids) = scope_candidates
            && scoped_ids.is_empty() { return Ok(Vec::new()); }

        let lookup_k = (k * 3).max(20);

        let comprehensive_ids: Option<Vec<String>> = if comprehensive {
            let ids = if let Some(ref scoped_ids) = scope_candidates {
                scoped_ids.iter().cloned().collect()
            } else {
                let encoded = inner.encoder.encode(&cues[0]);
                let max_c = 2000.min(n);
                inner.sparse_index.search(&encoded.sparse, max_c)
            };
            if ids.is_empty() { return Ok(Vec::new()); }
            Some(ids)
        } else {
            None
        };

        let results = py.allow_threads(|| {
            if let Some(ref cand_ids) = comprehensive_ids {
                let refs: Vec<&str> = cand_ids.iter().map(|s| s.as_str()).collect();
                let mut raw = inner.hopfield.recall_among_raw(&fused, &refs);
                let now = now_millis();

                for (id, score) in &mut raw {
                    if let Ok(Some(meta)) = inner.storage.get_meta(id) {
                        if ta != 0.0 {
                            let days = ((now - meta.created_at).max(0) as f64) / (24.0 * 3600.0 * 1000.0);
                            let recency = 1.0f32 / (1.0 + days as f32 / 30.0);
                            *score += ta * recency;
                        }
                        if ia != 0.0 {
                            *score += ia * effective_importance(&meta, now);
                        }
                    }
                }

                raw.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap());
                raw.truncate(lookup_k);
                if !raw.is_empty() {
                    let sims: Vec<f32> = raw.iter().map(|(_, s)| *s).collect();
                    let max_s = sims.iter().cloned().fold(f32::NEG_INFINITY, f32::max);
                    let exps: Vec<f32> = sims.iter().map(|&s| (s - max_s).exp()).collect();
                    let sum: f32 = exps.iter().sum();
                    if sum > 0.0 {
                        for (i, (_, conf)) in raw.iter_mut().enumerate() {
                            *conf = exps[i] / sum;
                        }
                    }
                }
                raw
            } else if let Some(ref scoped_ids) = scope_candidates {
                let refs: Vec<&str> = scoped_ids.iter().map(|s| s.as_str()).collect();
                let mut raw = inner.hopfield.recall_among_raw(&fused, &refs);
                raw.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap());
                raw.truncate(lookup_k);
                if !raw.is_empty() {
                    let max_s = raw.iter().map(|(_, s)| *s).fold(f32::NEG_INFINITY, f32::max);
                    let exps: Vec<f32> = raw.iter().map(|(_, s)| (s - max_s).exp()).collect();
                    let sum: f32 = exps.iter().sum();
                    if sum > 0.0 {
                        for (i, (_, conf)) in raw.iter_mut().enumerate() {
                            *conf = exps[i] / sum;
                        }
                    }
                }
                raw
            } else {
                inner.hopfield.recall_topk(&fused, lookup_k)
            }
        });

        let mut memories = Vec::with_capacity(k);
        for (id, confidence) in results {
            if memories.len() >= k { break; }
            if confidence < inner.confidence_threshold { break; }
            let meta_rec = inner.storage.get_meta(&id).map_err(storage_to_py)?;
            match meta_rec {
                Some(meta_rec) if !meta_rec.is_dormant => {
                    let blob = inner.storage.get_blob(&id).map_err(storage_to_py)?;
                    if let Some(blob) = blob {
                        let py_meta = json_map_to_pydict(py, &blob.meta);
                        memories.push(PyMemory::create(
                            id,
                            blob.text,
                            py_meta,
                            confidence as f64,
                            millis_to_iso(meta_rec.created_at),
                            blob.content_type.clone(),
                            if include_blob { blob.blob_data.clone() } else { None },
                        ));
                    }
                }
                _ => continue,
            }
        }

        Ok(memories)
    }

    fn forget(&self, memory_id: &str) -> PyResult<bool> {
        let mut inner = self.inner.write().unwrap();
        check_closed(&inner)?;

        let meta = inner.storage.get_meta(memory_id).map_err(storage_to_py)?;
        match meta {
            Some(meta_rec) => {
                let protection = u8_to_protection(meta_rec.protection);
                if matches!(protection, Protection::Permanent) {
                    return Ok(false);
                }
            }
            None => return Ok(false),
        }

        // Remove from meta index before deleting
        if let Some(blob) = inner.storage.get_blob(memory_id).map_err(storage_to_py)? {
            inner.meta_index.remove(memory_id, &blob.meta);
        }
        inner.storage.delete(memory_id).map_err(storage_to_py)?;
        inner.hopfield.remove_pattern(memory_id);
        inner.sparse_index.remove(memory_id);

        Ok(true)
    }

    /// Create a directed link between two memories.
    /// Stores the link in the from-memory's connections array.
    #[pyo3(signature = (from_id, to_id, link_type = "related"))]
    fn link_to(&self, from_id: &str, to_id: &str, link_type: &str) -> PyResult<bool> {
        let mut inner = self.inner.write().unwrap();
        check_closed(&inner)?;

        if inner.storage.get_pattern(from_id).map_err(storage_to_py)?.is_none() {
            return Ok(false);
        }
        if inner.storage.get_pattern(to_id).map_err(storage_to_py)?.is_none() {
            return Ok(false);
        }

        let blob = inner.storage.get_blob(from_id).map_err(storage_to_py)?;
        if let Some(mut blob) = blob {
            let old_json = blob.meta.clone();
            let mut connections: Vec<serde_json::Value> = match blob.meta.get("connections") {
                Some(serde_json::Value::Array(arr)) => arr.clone(),
                _ => Vec::new(),
            };

            // Avoid duplicate links
            let already_linked = connections.iter().any(|c| {
                c.get("to").and_then(|v| v.as_str()) == Some(to_id)
                    && c.get("type").and_then(|v| v.as_str()) == Some(link_type)
            });

            if !already_linked {
                let mut link = serde_json::Map::new();
                link.insert("to".to_string(), serde_json::Value::String(to_id.to_string()));
                link.insert("type".to_string(), serde_json::Value::String(link_type.to_string()));
                connections.push(serde_json::Value::Object(link));
                blob.meta.insert("connections".to_string(), serde_json::Value::Array(connections));
                inner.meta_index.update(from_id, &old_json, &blob.meta);
                inner.storage.update_blob(from_id, &blob).map_err(storage_to_py)?;
                return Ok(true);
            }

            Ok(true)
        } else {
            Ok(false)
        }
    }

    /// Get all outgoing links from a memory.
    fn links_of(&self, memory_id: &str, py: Python<'_>) -> PyResult<Vec<PyObject>> {
        let inner = self.inner.read().unwrap();
        check_closed(&inner)?;

        let blob = inner.storage.get_blob(memory_id).map_err(storage_to_py)?;
        match blob {
            Some(b) => match b.meta.get("connections") {
                Some(serde_json::Value::Array(arr)) => {
                    let result: Vec<PyObject> = arr.iter()
                        .map(|v| json_value_to_pyobj(py, v))
                        .collect();
                    Ok(result)
                }
                _ => Ok(Vec::new()),
            },
            None => Ok(Vec::new()),
        }
    }

    /// Find all memories that link TO the given memory (incoming links).
    /// NOTE: This scans all blobs since we don't maintain a reverse index.
    #[pyo3(signature = (memory_id))]
    fn links_to(&self, memory_id: &str, py: Python<'_>) -> PyResult<Vec<PyObject>> {
        let inner = self.inner.read().unwrap();
        check_closed(&inner)?;

        let all_blobs = inner.storage.all_blobs().map_err(storage_to_py)?;
        let mut result = Vec::new();

        for (from_id, blob) in &all_blobs {
            if let Some(serde_json::Value::Array(conns)) = blob.meta.get("connections") {
                for link in conns {
                    if link.get("to").and_then(|v| v.as_str()) == Some(memory_id) {
                        let mut entry = serde_json::Map::new();
                        entry.insert("from".to_string(), serde_json::Value::String(from_id.clone()));
                        if let Some(lt) = link.get("type") {
                            entry.insert("type".to_string(), lt.clone());
                        }
                        result.push(json_value_to_pyobj(py, &serde_json::Value::Object(entry)));
                    }
                }
            }
        }

        Ok(result)
    }


    #[pyo3(signature = (before_datetime))]
    fn purge_before(&self, before_datetime: &str) -> PyResult<u64> {
        let mut inner = self.inner.write().unwrap();
        check_closed(&inner)?;

        let cutoff = parse_datetime_to_millis(before_datetime)?;
        let all_metas = inner.storage.all_metas().map_err(storage_to_py)?;

        let mut deleted = 0u64;
        for (id, meta) in all_metas {
            if meta.protection == 0 && meta.created_at < cutoff {
                if let Some(blob) = inner.storage.get_blob(&id).map_err(storage_to_py)? {
                    inner.meta_index.remove(&id, &blob.meta);
                }
                inner.storage.delete(&id).map_err(storage_to_py)?;
                inner.hopfield.remove_pattern(&id);
                inner.sparse_index.remove(&id);
                deleted += 1;
            }
        }

        Ok(deleted)
    }

    #[pyo3(signature = (limit=5))]
    fn recent(&self, py: Python<'_>, limit: usize) -> PyResult<Vec<PyMemory>> {
        let inner = self.inner.read().unwrap();
        check_closed(&inner)?;

        let mut all_metas = inner.storage.all_metas().map_err(storage_to_py)?;
        all_metas.sort_by(|a, b| b.1.created_at.cmp(&a.1.created_at));
        all_metas.truncate(limit);

        let mut results = Vec::with_capacity(all_metas.len());
        for (id, meta_rec) in all_metas {
            if let Some(blob) = inner.storage.get_blob(&id).map_err(storage_to_py)? {
                let py_meta = json_map_to_pydict(py, &blob.meta);
                results.push(PyMemory::create(
                    id,
                    blob.text,
                    py_meta,
                    0.0,
                    millis_to_iso(meta_rec.created_at),
                    blob.content_type.clone(),
                    blob.blob_data.clone(),
                ));
            }
        }

        Ok(results)
    }

    /// Build an entity graph showing domain-level connections.
    /// Each node is a domain; edges represent linked memories across domains.
    fn entity_graph(&self, py: Python<'_>) -> PyResult<PyObject> {
        let inner = self.inner.read().unwrap();
        check_closed(&inner)?;

        let all_blobs = inner.storage.all_blobs().map_err(storage_to_py)?;
        let mut domains = HashMap::new();
        let mut edges: Vec<(String, String, String)> = Vec::new();

        // Collect domain info
        for (id, blob) in &all_blobs {
            if let Some(serde_json::Value::String(domain)) = blob.meta.get("domain") {
                let entry = domains.entry(domain.clone()).or_insert_with(|| (0usize, Vec::new()));
                entry.0 += 1;
                entry.1.push(id.clone());
            }
            // Extract connections for edges
            if let Some(serde_json::Value::Array(conns)) = blob.meta.get("connections") {
                let from_domain = blob.meta.get("domain")
                    .and_then(|v| v.as_str())
                    .unwrap_or("unknown");
                for conn in conns {
                    if let (Some(to), Some(link_type)) = (
                        conn.get("to").and_then(|v| v.as_str()),
                        conn.get("type").and_then(|v| v.as_str()),
                    ) {
                        // Find target domain
                        let to_domain = inner.storage.get_blob(to)
                            .map_err(storage_to_py)?
                            .and_then(|b| b.meta.get("domain").and_then(|v| v.as_str().map(|s| s.to_string())))
                            .unwrap_or_else(|| "unknown".to_string());
                        edges.push((from_domain.to_string(), to_domain, link_type.to_string()));
                    }
                }
            }
        }

        let dict = PyDict::new(py);
        // Nodes
        let nodes: Vec<PyObject> = domains.iter().map(|(name, (count, _ids))| {
            let nd = PyDict::new(py);
            let _ = nd.set_item("domain", name);
            let _ = nd.set_item("count", *count as u64);
            nd.into()
        }).collect();
        dict.set_item("nodes", nodes)?;

        // Edges (deduplicated)
        edges.sort();
        edges.dedup();
        let edge_objs: Vec<PyObject> = edges.iter().map(|(from, to, link_type)| {
            let ed = PyDict::new(py);
            let _ = ed.set_item("from", from);
            let _ = ed.set_item("to", to);
            let _ = ed.set_item("type", link_type);
            ed.into()
        }).collect();
        dict.set_item("edges", edge_objs)?;

        Ok(dict.into())
    }

    /// Build a knowledge tree from parent/child relationships.
    fn knowledge_tree(&self, py: Python<'_>) -> PyResult<PyObject> {
        let inner = self.inner.read().unwrap();
        check_closed(&inner)?;

        let all_blobs = inner.storage.all_blobs().map_err(storage_to_py)?;

        // Build tree: id → (children, text snippet)
        let mut children_map: HashMap<String, Vec<String>> = HashMap::new();
        let mut node_info: HashMap<String, (String, String)> = HashMap::new(); // (text[:50], layer/domain)

        // First pass: find all nodes and their parent relationships
        for (id, blob) in &all_blobs {
            let text_snippet = if blob.text.len() > 50 {
                format!("{}...", &blob.text[..50])
            } else {
                blob.text.clone()
            };
            let layer = blob.meta.get("layer").and_then(|v| v.as_str()).unwrap_or("").to_string();
            node_info.insert(id.clone(), (text_snippet, layer));

            if let Some(serde_json::Value::String(parent)) = blob.meta.get("parent") {
                children_map.entry(parent.clone()).or_default().push(id.clone());
            }
        }

        // Find roots (nodes with no parent)
        let roots: Vec<String> = all_blobs.iter()
            .filter(|(id, _)| !node_info.contains_key(id) || {
                all_blobs.iter().all(|(_, b)| {
                    b.meta.get("parent").and_then(|v| v.as_str()) != Some(id.as_str())
                })
            })
            .map(|(id, _)| id.clone())
            .filter(|id| children_map.contains_key(id)) // only nodes that have children
            .collect();

        fn build_subtree(
            root: &str,
            children_map: &HashMap<String, Vec<String>>,
            node_info: &HashMap<String, (String, String)>,
            py: Python<'_>,
        ) -> PyObject {
            let node = PyDict::new(py);
            let _ = node.set_item("id", root);
            if let Some((text, layer)) = node_info.get(root) {
                let _ = node.set_item("text", text);
                let _ = node.set_item("layer", layer);
            }
            if let Some(children) = children_map.get(root) {
                let child_objs: Vec<PyObject> = children.iter()
                    .map(|c| build_subtree(c, children_map, node_info, py))
                    .collect();
                let _ = node.set_item("children", child_objs);
            }
            node.into()
        }

        let tree: Vec<PyObject> = roots.into_iter()
            .map(|r| build_subtree(&r, &children_map, &node_info, py))
            .collect();

        Ok(tree.into_py_any(py).unwrap())
    }

    /// Get memories ordered by time, optionally filtered by session_id or layer.
    #[pyo3(signature = (session_id=None, layer=None, limit=50))]
    fn episode_thread(&self, py: Python<'_>, session_id: Option<String>, layer: Option<String>, limit: usize) -> PyResult<Vec<PyMemory>> {
        let inner = self.inner.read().unwrap();
        check_closed(&inner)?;

        let all_blobs = inner.storage.all_blobs().map_err(storage_to_py)?;
        let all_metas = inner.storage.all_metas().map_err(storage_to_py)?;
        let meta_map: HashMap<&str, &MetaRecord> = all_metas.iter()
            .map(|(id, m)| (id.as_str(), m))
            .collect();

        let mut filtered: Vec<(i64, String)> = all_blobs.iter()
            .filter(|(_id, blob)| {
                if let Some(ref sid) = session_id {
                    blob.meta.get("session_id").and_then(|v| v.as_str()) == Some(sid.as_str())
                } else { true }
            })
            .filter(|(_, blob)| {
                if let Some(ref l) = layer {
                    blob.meta.get("layer").and_then(|v| v.as_str()) == Some(l.as_str())
                } else { true }
            })
            .filter_map(|(id, _)| {
                meta_map.get(id.as_str()).map(|m| (m.created_at, id.clone()))
            })
            .collect();

        filtered.sort_by_key(|(ts, _)| *ts);
        filtered.truncate(limit);

        let mut results = Vec::with_capacity(filtered.len());
        for (_, id) in &filtered {
            if let Some(meta_rec) = meta_map.get(id.as_str())
                && let Some(blob) = inner.storage.get_blob(id).map_err(storage_to_py)? {
                    let py_meta = json_map_to_pydict(py, &blob.meta);
                    results.push(PyMemory::create(
                        id.clone(),
                        blob.text,
                        py_meta,
                        0.0,
                        millis_to_iso(meta_rec.created_at),
                        blob.content_type.clone(),
                        blob.blob_data.clone(),
                    ));
                }
        }

        Ok(results)
    }

    /// Group memories by their layer metadata.
    #[pyo3(signature = (layer=None))]
    fn memories_by_layer(&self, py: Python<'_>, layer: Option<String>) -> PyResult<PyObject> {
        let inner = self.inner.read().unwrap();
        check_closed(&inner)?;

        let all_blobs = inner.storage.all_blobs().map_err(storage_to_py)?;
        let mut layers: HashMap<String, Vec<PyObject>> = HashMap::new();

        for (id, blob) in &all_blobs {
            let layer_val = blob.meta.get("layer")
                .and_then(|v| v.as_str())
                .unwrap_or("_ungrouped");
            if let Some(ref l) = layer
                && layer_val != l { continue; }
            let item = PyDict::new(py);
            let _ = item.set_item("id", id);
            let text_snippet = if blob.text.len() > 80 {
                format!("{}...", &blob.text[..80])
            } else {
                blob.text.clone()
            };
            let _ = item.set_item("text", text_snippet);
            if let Some(serde_json::Value::String(domain)) = blob.meta.get("domain") {
                let _ = item.set_item("domain", domain);
            }
            layers.entry(layer_val.to_string()).or_default().push(item.into());
        }

        let dict = PyDict::new(py);
        for (name, memories) in &layers {
            dict.set_item(name, memories)?;
        }
        Ok(dict.into())
    }


    #[pyo3(signature = (items))]
    fn remember_batch(
        &self,
        py: Python<'_>,
        items: Vec<Bound<'_, PyDict>>,
    ) -> PyResult<Vec<String>> {
        let mut inner = self.inner.write().unwrap();
        check_closed(&inner)?;

        let mut ids = Vec::with_capacity(items.len());
        let mut batch_data = Vec::with_capacity(items.len());

        for item in &items {
            let text_bound = item
                .get_item("text")
                .map_err(|_| MemHopError::new_err("remember_batch: 'text' key required"))?
                .ok_or_else(|| MemHopError::new_err("remember_batch: 'text' key required"))?;
            let text: String = text_bound
                .extract()
                .map_err(|_| MemHopError::new_err("remember_batch: 'text' must be a string"))?;

            let meta_py: Option<HashMap<String, PyObject>> = match item.get_item("meta") {
                Ok(Some(v)) => v.extract().ok(),
                _ => None,
            };

            let json_meta = if let Some(ref m) = meta_py {
                pydict_to_json_map(m, py)
            } else {
                HashMap::new()
            };

            let content_type: Option<String> = match item.get_item("content_type") {
                Ok(Some(v)) => v.extract().ok(),
                _ => None,
            };
            let blob_data: Option<Vec<u8>> = match item.get_item("blob") {
                Ok(Some(v)) => v.extract().ok(),
                _ => None,
            };

            let id = generate_memory_id();
            let output = inner.encoder.encode(&text);

            let importance = extract_importance(&json_meta);
            let importance_decay_rate = extract_importance_decay_rate(&json_meta);
            let protection = extract_protection(&json_meta);
            let is_dormant = extract_is_dormant(&json_meta);

            let meta_record = MetaRecord {
                created_at: now_millis(),
                importance,
                protection: protection_to_u8(&protection),
                is_dormant,
                key: None,
                importance_decay_rate,
            };

            let blob_record = BlobRecord {
                text: text.clone(),
                meta: json_meta.clone(),
                content_type: content_type.clone(),
                blob_data: blob_data.clone(),
            };

            batch_data.push((id.clone(), output.dense.clone(), output.sparse.clone(), blob_record, meta_record, json_meta));
            ids.push(id);
        }

        let storage_batch: Vec<(String, Vec<f16>, BlobRecord, MetaRecord)> = batch_data
            .iter()
            .map(|(id, dense, _sparse, blob, meta, _json)| (id.clone(), dense.clone(), blob.clone(), meta.clone()))
            .collect();

        inner
            .storage
            .put_batch(&storage_batch)
            .map_err(storage_to_py)?;

        for (id, dense, sparse, _blob, _meta, json_meta) in &batch_data {
            inner.hopfield.add_pattern(id, dense);
            inner.sparse_index.add(id, sparse);
            inner.meta_index.add(id, json_meta);
        }

        let count = inner.storage.count().map_err(storage_to_py)?;
        if count > inner.max_memories {
            evict_oldest(&mut inner)?;
        }

        Ok(ids)
    }

    #[pyo3(signature = (memory_id, text=None, meta=None, content_type=None, blob=None))]
    fn update(
        &self,
        memory_id: &str,
        text: Option<&str>,
        meta: Option<HashMap<String, PyObject>>,
        content_type: Option<String>,
        blob: Option<Vec<u8>>,
        py: Python<'_>,
    ) -> PyResult<bool> {
        let mut inner = self.inner.write().unwrap();
        check_closed(&inner)?;

        if inner
            .storage
            .get_pattern(memory_id)
            .map_err(storage_to_py)?
            .is_none()
        {
            return Ok(false);
        }

        let new_json = meta.as_ref().map(|m| pydict_to_json_map(m, py));

        // Track old meta for index update
        let old_blob = inner.storage.get_blob(memory_id).map_err(storage_to_py)?;

        if let Some(new_text) = text {
            let output = inner.encoder.encode(new_text);
            inner
                .storage
                .update_pattern(memory_id, &output.dense)
                .map_err(storage_to_py)?;
            inner.hopfield.add_pattern(memory_id, &output.dense);
            inner.sparse_index.update(memory_id, &output.sparse);

            if let Some(mut blob_rec) = old_blob {
                let old_json = blob_rec.meta.clone();
                blob_rec.text = new_text.to_string();
                if let Some(ref ct) = content_type {
                    blob_rec.content_type = Some(ct.clone());
                }
                if let Some(ref b) = blob {
                    blob_rec.blob_data = Some(b.clone());
                }
                if let Some(ref nj) = new_json {
                    for (k, v) in nj {
                        blob_rec.meta.insert(k.clone(), v.clone());
                    }
                }
                inner.meta_index.update(memory_id, &old_json, &blob_rec.meta);
                inner
                    .storage
                    .update_blob(memory_id, &blob_rec)
                    .map_err(storage_to_py)?;
            }
        } else if let Some(ref nj) = new_json {
            if let Some(mut blob_rec) = old_blob {
                let old_json = blob_rec.meta.clone();
                for (k, v) in nj {
                    blob_rec.meta.insert(k.clone(), v.clone());
                }
                if let Some(ref ct) = content_type {
                    blob_rec.content_type = Some(ct.clone());
                }
                if let Some(ref b) = blob {
                    blob_rec.blob_data = Some(b.clone());
                }
                inner.meta_index.update(memory_id, &old_json, &blob_rec.meta);
                inner
                    .storage
                    .update_blob(memory_id, &blob_rec)
                    .map_err(storage_to_py)?;
            }
        } else if (content_type.is_some() || blob.is_some())
            && let Some(mut blob_rec) = old_blob {
                if let Some(ref ct) = content_type {
                    blob_rec.content_type = Some(ct.clone());
                }
                if let Some(ref b) = blob {
                    blob_rec.blob_data = Some(b.clone());
                }
                inner
                    .storage
                    .update_blob(memory_id, &blob_rec)
                    .map_err(storage_to_py)?;
            }

        if let Some(ref nj) = new_json {
            let existing_meta = inner.storage.get_meta(memory_id).map_err(storage_to_py)?;
            if let Some(mut meta_rec) = existing_meta {
                let mut changed = false;
                if let Some(serde_json::Value::Number(n)) = nj.get("importance") {
                    meta_rec.importance = n.as_f64().unwrap_or(meta_rec.importance as f64) as f32;
                    changed = true;
                }
                if let Some(serde_json::Value::String(s)) = nj.get("protection") {
                    meta_rec.protection = protection_to_u8(&match s.as_str() {
                        "protected" => Protection::Protected,
                        "permanent" => Protection::Permanent,
                        _ => Protection::Normal,
                    });
                    changed = true;
                }
                if let Some(serde_json::Value::Bool(b)) = nj.get("is_dormant") {
                    meta_rec.is_dormant = *b;
                    changed = true;
                }
                if changed {
                    inner
                        .storage
                        .update_meta(memory_id, &meta_rec)
                        .map_err(storage_to_py)?;
                }
            }
        }

        Ok(true)
    }

    /// Search memories by metadata filters.
    /// Uses MetaIndex for O(1) equality-filter acceleration when available.
    #[pyo3(signature = (filters, limit=None))]
    fn search(
        &self,
        py: Python<'_>,
        filters: &Bound<'_, PyDict>,
        limit: Option<usize>,
    ) -> PyResult<Vec<PyMemory>> {
        let inner = self.inner.read().unwrap();
        check_closed(&inner)?;

        let limit = limit.unwrap_or(20);
        let filter_criteria = parse_filters(filters)?;

        // Try index-accelerated candidate lookup for equality filters
        let candidate_ids: Option<HashSet<String>> = inner.meta_index.get_candidates(
            filter_criteria.layer.as_deref(),
            filter_criteria.r#type.as_deref(),
            filter_criteria.domain.as_deref(),
            filter_criteria.session_id.as_deref(),
            filter_criteria.path.as_deref(),
            filter_criteria.parent.as_deref(),
        );

        let mut results = Vec::new();

        let has_complex_filters = filter_criteria.is_dormant.is_some()
            || filter_criteria.protection.is_some()
            || filter_criteria.importance_gt.is_some()
            || filter_criteria.importance_lt.is_some()
            || filter_criteria.tags_contains.is_some()
            || filter_criteria.connections_to.is_some();

        if let Some(ref cands) = candidate_ids {
            if cands.is_empty() {
                return Ok(Vec::new());
            }
            // Index-assisted scan: only iterate over candidates
            for id in cands {
                if results.len() >= limit {
                    break;
                }
                let blob = match inner.storage.get_blob(id).map_err(storage_to_py)? {
                    Some(b) => b,
                    None => continue,
                };
                let meta_rec = match inner.storage.get_meta(id).map_err(storage_to_py)? {
                    Some(m) => m,
                    None => continue,
                };
                if matches_filters(&blob, &meta_rec, &filter_criteria) {
                    let py_meta = json_map_to_pydict(py, &blob.meta);
                    results.push(PyMemory::create(
                        id.clone(),
                        blob.text,
                        py_meta,
                        0.0,
                        millis_to_iso(meta_rec.created_at),
                        blob.content_type.clone(),
                        blob.blob_data.clone(),
                    ));
                }
            }
        } else if has_complex_filters {
            // Full scan for complex filters that aren't covered by MetaIndex
            let all_ids = inner.storage.all_ids().map_err(storage_to_py)?;
            for id in all_ids {
                if results.len() >= limit {
                    break;
                }
                let blob = match inner.storage.get_blob(&id).map_err(storage_to_py)? {
                    Some(b) => b,
                    None => continue,
                };
                let meta_rec = match inner.storage.get_meta(&id).map_err(storage_to_py)? {
                    Some(m) => m,
                    None => continue,
                };
                if matches_filters(&blob, &meta_rec, &filter_criteria) {
                    let py_meta = json_map_to_pydict(py, &blob.meta);
                    results.push(PyMemory::create(
                        id,
                        blob.text,
                        py_meta,
                        0.0,
                        millis_to_iso(meta_rec.created_at),
                        blob.content_type.clone(),
                        blob.blob_data.clone(),
                    ));
                }
            }
        } else {
            // No filters at all: return first N (same as recent)
            let all_ids = inner.storage.all_ids().map_err(storage_to_py)?;
            for id in all_ids.iter().take(limit) {
                let blob = match inner.storage.get_blob(id).map_err(storage_to_py)? {
                    Some(b) => b,
                    None => continue,
                };
                let meta_rec = match inner.storage.get_meta(id).map_err(storage_to_py)? {
                    Some(m) => m,
                    None => continue,
                };
                let py_meta = json_map_to_pydict(py, &blob.meta);
                results.push(PyMemory::create(
                    id.clone(),
                    blob.text,
                    py_meta,
                    0.0,
                    millis_to_iso(meta_rec.created_at),
                    blob.content_type.clone(),
                    blob.blob_data.clone(),
                ));
            }
        }

        Ok(results)
    }

    fn close(&self) -> PyResult<()> {
        let mut inner = self.inner.write().unwrap();
        if inner.closed {
            return Ok(());
        }
        // Persist indices for fast startup recovery
        persist_indices(&inner)?;
        inner.closed = true;
        inner.storage.close().map_err(storage_to_py)?;
        Ok(())
    }

    #[getter]
    fn count(&self) -> PyResult<u64> {
        let inner = self.inner.read().unwrap();
        check_closed(&inner)?;
        inner.storage.count().map_err(storage_to_py)
    }

    #[getter]
    fn stats(&self, py: Python<'_>) -> PyResult<PyObject> {
        let inner = self.inner.read().unwrap();
        let dict = PyDict::new(py);
        let count = inner.storage.count().map_err(storage_to_py)?;
        let index_size = inner.storage.index_size_bytes().map_err(storage_to_py)?;
        dict.set_item("total_memories", count)?;
        dict.set_item("storage_path", &inner.storage_path)?;
        dict.set_item("encoder_mode", &inner.encoder_mode)?;
        dict.set_item("beta", inner.beta as f64)?;
        dict.set_item("threshold", inner.confidence_threshold as f64)?;
        dict.set_item("max_memories", inner.max_memories)?;
        dict.set_item("index_size_bytes", index_size)?;

        // Enhanced stats from MetaIndex + storage scan
        let all_metas = inner.storage.all_metas().map_err(storage_to_py)?;
        let all_blobs = inner.storage.all_blobs().map_err(storage_to_py)?;

        let active = all_metas.iter().filter(|(_, m)| !m.is_dormant).count() as u64;
        dict.set_item("active_memories", active)?;

        let avg_importance = if all_metas.is_empty() { 0.0 }
            else { all_metas.iter().map(|(_, m)| m.importance as f64).sum::<f64>() / all_metas.len() as f64 };
        dict.set_item("avg_importance", avg_importance)?;

        // Counts by layer and domain
        let mut layer_counts = HashMap::new();
        let mut domain_counts = HashMap::new();
        for (_, blob) in &all_blobs {
            if let Some(serde_json::Value::String(l)) = blob.meta.get("layer") {
                *layer_counts.entry(l.clone()).or_insert(0u64) += 1;
            }
            if let Some(serde_json::Value::String(d)) = blob.meta.get("domain") {
                *domain_counts.entry(d.clone()).or_insert(0u64) += 1;
            }
        }
        let py_layer_counts: HashMap<String, u64> = layer_counts;
        let py_domain_counts: HashMap<String, u64> = domain_counts;
        dict.set_item("layer_counts", py_layer_counts)?;
        dict.set_item("domain_counts", py_domain_counts)?;

        dict.set_item("hopfield_patterns", inner.hopfield.len() as u64)?;

        Ok(dict.into())
    }

    // ── Context manager ───────────────────────────────────

    fn __enter__(slf: PyRef<Self>) -> PyRef<Self> {
        slf
    }

    #[pyo3(signature = (exc_type=None, exc_val=None, exc_tb=None))]
    fn __exit__(
        &self,
        exc_type: Option<&Bound<'_, PyAny>>,
        exc_val: Option<&Bound<'_, PyAny>>,
        exc_tb: Option<&Bound<'_, PyAny>>,
    ) -> PyResult<()> {
        let _ = (exc_type, exc_val, exc_tb);
        self.close()
    }

    fn __repr__(&self) -> String {
        let inner = self.inner.read().unwrap();
        format!(
            "MemHopEngine(closed={}, count={})",
            inner.closed,
            inner.storage.count().unwrap_or(0),
        )
    }
}

// ── Eviction helper ────────────────────────────────────────

fn persist_indices(inner: &EngineInner) -> PyResult<()> {
    let sparse_bytes = inner.sparse_index.to_bytes();
    inner.storage.save_index("sparse", &sparse_bytes).map_err(storage_to_py)?;
    let meta_bytes = inner.meta_index.to_bytes();
    inner.storage.save_index("meta", &meta_bytes).map_err(storage_to_py)?;
    let version_bytes = bincode::serialize(&INDEX_VERSION)
        .map_err(|e| MemHopError::new_err(format!("version serialize: {}", e)))?;
    inner.storage.save_index("version", &version_bytes).map_err(storage_to_py)?;
    Ok(())
}

fn evict_oldest(inner: &mut EngineInner) -> PyResult<()> {
    let count = inner.storage.count().map_err(storage_to_py)?;
    if count <= inner.max_memories {
        return Ok(());
    }
    let excess = (count - inner.max_memories) as usize;
    let mut evicted = 0usize;

    let all_metas = inner.storage.all_metas().map_err(storage_to_py)?;
    let mut sorted: Vec<_> = all_metas;
    sorted.sort_by_key(|(_, m)| m.created_at);

    for (id, meta) in &sorted {
        if evicted >= excess {
            break;
        }
        if meta.protection != 0 {
            continue;
        }
        if let Some(blob) = inner.storage.get_blob(id).map_err(storage_to_py)? {
            inner.meta_index.remove(id, &blob.meta);
        }
        inner.storage.delete(id).map_err(storage_to_py)?;
        inner.hopfield.remove_pattern(id);
        inner.sparse_index.remove(id);
        evicted += 1;
    }

    if evicted < excess {
        let all_metas = inner.storage.all_metas().map_err(storage_to_py)?;
        let mut sorted: Vec<_> = all_metas;
        sorted.sort_by_key(|(_, m)| m.created_at);

        for (id, meta) in &sorted {
            if evicted >= excess {
                break;
            }
            if meta.protection == 1 {
                if let Some(blob) = inner.storage.get_blob(id).map_err(storage_to_py)? {
                    inner.meta_index.remove(id, &blob.meta);
                }
                inner.storage.delete(id).map_err(storage_to_py)?;
                inner.hopfield.remove_pattern(id);
                inner.sparse_index.remove(id);
                evicted += 1;
            }
        }
    }

    Ok(())
}
