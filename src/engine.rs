use half::f16;
use pyo3::prelude::*;
use pyo3::types::PyDict;
use pyo3::IntoPyObjectExt;
use std::collections::{HashMap, HashSet};
use std::sync::{Arc, Mutex};

use crate::types::{PyMemory, MemHopClosedError, MemHopError, Protection, VECTOR_DIM};
use crate::encoder::{NgramEncoder, Encoder};
use crate::storage::{LmdbStorage, BlobRecord, MetaRecord, StorageError};
use crate::hopfield::ModernHopfield;
use crate::index::SparseIndex;

// ── MetaIndex for O(1) equality-filter acceleration ────────

struct MetaIndex {
    /// field_name → (field_value → set of memory_ids)
    by_layer: HashMap<String, HashSet<String>>,
    by_type: HashMap<String, HashSet<String>>,
    by_domain: HashMap<String, HashSet<String>>,
    by_session_id: HashMap<String, HashSet<String>>,
    by_path: HashMap<String, HashSet<String>>,
    by_parent: HashMap<String, HashSet<String>>,
}

impl MetaIndex {
    fn new() -> Self {
        MetaIndex {
            by_layer: HashMap::new(),
            by_type: HashMap::new(),
            by_domain: HashMap::new(),
            by_session_id: HashMap::new(),
            by_path: HashMap::new(),
            by_parent: HashMap::new(),
        }
    }

    fn add(&mut self, id: &str, meta: &HashMap<String, serde_json::Value>) {
        MetaIndex::insert_to(&mut self.by_layer, "layer", id, meta);
        MetaIndex::insert_to(&mut self.by_type, "type", id, meta);
        MetaIndex::insert_to(&mut self.by_domain, "domain", id, meta);
        MetaIndex::insert_to(&mut self.by_session_id, "session_id", id, meta);
        MetaIndex::insert_to(&mut self.by_path, "path", id, meta);
        MetaIndex::insert_to(&mut self.by_parent, "parent", id, meta);
    }

    fn remove(&mut self, id: &str, meta: &HashMap<String, serde_json::Value>) {
        MetaIndex::remove_from(&mut self.by_layer, "layer", id, meta);
        MetaIndex::remove_from(&mut self.by_type, "type", id, meta);
        MetaIndex::remove_from(&mut self.by_domain, "domain", id, meta);
        MetaIndex::remove_from(&mut self.by_session_id, "session_id", id, meta);
        MetaIndex::remove_from(&mut self.by_path, "path", id, meta);
        MetaIndex::remove_from(&mut self.by_parent, "parent", id, meta);
    }

    fn update(&mut self, id: &str, old_meta: &HashMap<String, serde_json::Value>, new_meta: &HashMap<String, serde_json::Value>) {
        self.remove(id, old_meta);
        self.add(id, new_meta);
    }

    /// Get candidate IDs matching an equality filter. Returns None if field is not indexed
    /// or value not found (caller should fall back to full scan).
    fn get_candidates(
        &self,
        layer: Option<&str>,
        r#type: Option<&str>,
        domain: Option<&str>,
        session_id: Option<&str>,
        path: Option<&str>,
        parent: Option<&str>,
    ) -> Option<HashSet<String>> {
        if layer.is_none() && r#type.is_none() && domain.is_none()
            && session_id.is_none() && path.is_none() && parent.is_none() {
            return None;
        }

        let mut result: Option<HashSet<String>> = None;
        for (map, val) in [
            (&self.by_layer, layer),
            (&self.by_type, r#type),
            (&self.by_domain, domain),
            (&self.by_session_id, session_id),
            (&self.by_path, path),
            (&self.by_parent, parent),
        ] {
            if let Some(v) = val {
                let set = map.get(v).cloned().unwrap_or_default();
                result = match result {
                    None => Some(set),
                    Some(r) => Some(r.intersection(&set).cloned().collect()),
                };
            }
        }

        if let Some(ref r) = result {
            if r.is_empty() {
                return Some(HashSet::new());
            }
        }
        result
    }

    fn insert_to(
        map: &mut HashMap<String, HashSet<String>>,
        field: &str, id: &str, meta: &HashMap<String, serde_json::Value>,
    ) {
        if let Some(serde_json::Value::String(v)) = meta.get(field) {
            map.entry(v.clone()).or_default().insert(id.to_string());
        }
    }

    fn remove_from(
        map: &mut HashMap<String, HashSet<String>>,
        field: &str, id: &str, meta: &HashMap<String, serde_json::Value>,
    ) {
        if let Some(serde_json::Value::String(v)) = meta.get(field) {
            if let Some(set) = map.get_mut(v) {
                set.remove(id);
                if set.is_empty() {
                    map.remove(v);
                }
            }
        }
    }
}

// ── EngineInner ──────────────────────────────────────────

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
    inner: Arc<Mutex<EngineInner>>,
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

fn storage_to_py(err: StorageError) -> PyErr {
    MemHopError::new_err(err.to_string())
}

/// Convert f16 dense vector to f32 for Hopfield query.
fn f16_to_f32(v: &[f16]) -> Vec<f32> {
    v.iter().map(|x| x.to_f32()).collect()
}

/// Convert a Python value (Bound<'_, PyAny>) to serde_json::Value.
fn bound_to_json_value(val: &Bound<'_, PyAny>) -> serde_json::Value {
    if val.is_none() {
        return serde_json::Value::Null;
    }
    if let Ok(b) = val.extract::<bool>() {
        return serde_json::Value::Bool(b);
    }
    if let Ok(i) = val.extract::<i64>() {
        return serde_json::Value::Number(i.into());
    }
    if let Ok(f) = val.extract::<f64>() {
        return serde_json::Number::from_f64(f)
            .map(serde_json::Value::Number)
            .unwrap_or(serde_json::Value::Null);
    }
    if let Ok(s) = val.extract::<String>() {
        return serde_json::Value::String(s);
    }
    let py = val.py();
    if let Ok(list) = val.extract::<Vec<PyObject>>() {
        let arr: Vec<serde_json::Value> = list
            .iter()
            .map(|item| bound_to_json_value(item.bind(py)))
            .collect();
        return serde_json::Value::Array(arr);
    }
    if let Ok(dict_map) = val.extract::<HashMap<String, PyObject>>() {
        let mut map = serde_json::Map::new();
        for (k, v) in &dict_map {
            map.insert(k.clone(), bound_to_json_value(v.bind(py)));
        }
        return serde_json::Value::Object(map);
    }
    serde_json::Value::Null
}

fn pydict_to_json_map(
    meta: &HashMap<String, PyObject>,
    py: Python<'_>,
) -> HashMap<String, serde_json::Value> {
    meta.iter()
        .map(|(k, v)| (k.clone(), bound_to_json_value(v.bind(py))))
        .collect()
}

fn json_value_to_pyobj(py: Python<'_>, val: &serde_json::Value) -> PyObject {
    match val {
        serde_json::Value::Null => py.None(),
        serde_json::Value::Bool(b) => b.into_py_any(py).unwrap(),
        serde_json::Value::Number(n) => {
            if let Some(i) = n.as_i64() {
                i.into_py_any(py).unwrap()
            } else if let Some(f) = n.as_f64() {
                f.into_py_any(py).unwrap()
            } else {
                py.None()
            }
        }
        serde_json::Value::String(s) => s.into_py_any(py).unwrap(),
        serde_json::Value::Array(arr) => {
            let items: Vec<PyObject> = arr.iter().map(|v| json_value_to_pyobj(py, v)).collect();
            items.into_py_any(py).unwrap()
        }
        serde_json::Value::Object(map) => {
            let dict = PyDict::new(py);
            for (k, v) in map {
                dict.set_item(k, json_value_to_pyobj(py, v)).unwrap();
            }
            dict.into()
        }
    }
}

fn json_map_to_pydict(
    py: Python<'_>,
    map: &HashMap<String, serde_json::Value>,
) -> HashMap<String, PyObject> {
    map.iter()
        .map(|(k, v)| (k.clone(), json_value_to_pyobj(py, v)))
        .collect()
}

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

    if let Some(dormant) = criteria.is_dormant {
        if meta.is_dormant != dormant {
            return false;
        }
    }

    if let Some(ref prot_str) = criteria.protection {
        if meta.protection != protection_str_to_u8(prot_str) {
            return false;
        }
    }

    if let Some(gt) = criteria.importance_gt {
        if meta.importance <= gt {
            return false;
        }
    }
    if let Some(lt) = criteria.importance_lt {
        if meta.importance >= lt {
            return false;
        }
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
        let mut sparse_index = SparseIndex::new();
        let mut meta_index = MetaIndex::new();

        // Startup recovery: load all existing patterns into hopfield + sparse index
        let all_patterns = storage.all_patterns().map_err(storage_to_py)?;
        let all_blobs = storage.all_blobs().map_err(storage_to_py)?;

        for (id, pattern) in &all_patterns {
            hopfield.add_pattern(id, pattern);
        }

        // Rebuild sparse index + meta index by re-encoding stored texts
        for (id, blob) in &all_blobs {
            let output = ngram_encoder.encode(&blob.text);
            sparse_index.add(id, &output.sparse);
            meta_index.add(id, &blob.meta);
        }

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
            inner: Arc::new(Mutex::new(inner)),
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
        let mut inner = self.inner.lock().unwrap();
        check_closed(&inner)?;

        let output = inner.encoder.encode(text);

        let json_meta = if let Some(ref m) = meta {
            pydict_to_json_map(m, py)
        } else {
            HashMap::new()
        };

        let importance = extract_importance(&json_meta);
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

    fn recall(&self, cue: &str, py: Python<'_>) -> PyResult<Option<PyMemory>> {
        let inner = self.inner.lock().unwrap();
        check_closed(&inner)?;

        let output = inner.encoder.encode(cue);
        let query_f32 = f16_to_f32(&output.dense);

        let n = inner.hopfield.len();
        if n == 0 {
            return Ok(None);
        }

        let recall_result = if n <= 500 {
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
        };

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
                                blob.blob_data.clone(),
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

    #[pyo3(signature = (cue, k=5))]
    fn recall_topk(&self, cue: &str, k: usize, py: Python<'_>) -> PyResult<Vec<PyMemory>> {
        let inner = self.inner.lock().unwrap();
        check_closed(&inner)?;

        let output = inner.encoder.encode(cue);
        let query_f32 = f16_to_f32(&output.dense);
        let lookup_k = (k * 3).max(20);
        let results = inner.hopfield.recall_topk(&query_f32, lookup_k);

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
                            blob.blob_data.clone(),
                        ));
                    }
                }
                _ => continue,
            }
        }

        Ok(memories)
    }

    fn forget(&self, memory_id: &str) -> PyResult<bool> {
        let mut inner = self.inner.lock().unwrap();
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

    #[pyo3(signature = (before_datetime))]
    fn purge_before(&self, before_datetime: &str) -> PyResult<u64> {
        let mut inner = self.inner.lock().unwrap();
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
        let inner = self.inner.lock().unwrap();
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

    #[pyo3(signature = (items))]
    fn remember_batch(
        &self,
        py: Python<'_>,
        items: Vec<Bound<'_, PyDict>>,
    ) -> PyResult<Vec<String>> {
        let mut inner = self.inner.lock().unwrap();
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
            let protection = extract_protection(&json_meta);
            let is_dormant = extract_is_dormant(&json_meta);

            let meta_record = MetaRecord {
                created_at: now_millis(),
                importance,
                protection: protection_to_u8(&protection),
                is_dormant,
                key: None,
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
        let mut inner = self.inner.lock().unwrap();
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
        } else if content_type.is_some() || blob.is_some() {
            if let Some(mut blob_rec) = old_blob {
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
        let inner = self.inner.lock().unwrap();
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
        let mut inner = self.inner.lock().unwrap();
        if inner.closed {
            return Ok(());
        }
        inner.closed = true;
        inner.storage.close().map_err(storage_to_py)?;
        Ok(())
    }

    #[getter]
    fn count(&self) -> PyResult<u64> {
        let inner = self.inner.lock().unwrap();
        check_closed(&inner)?;
        inner.storage.count().map_err(storage_to_py)
    }

    #[getter]
    fn stats(&self, py: Python<'_>) -> PyResult<PyObject> {
        let inner = self.inner.lock().unwrap();
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
        let inner = self.inner.lock().unwrap();
        format!(
            "MemHopEngine(closed={}, count={})",
            inner.closed,
            inner.storage.count().unwrap_or(0),
        )
    }
}

// ── Eviction helper ────────────────────────────────────────

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
