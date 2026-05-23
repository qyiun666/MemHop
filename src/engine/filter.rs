//! Search filter types and scope parsing for the engine.
//! Extracted from the original monolithic engine.rs to reduce file size.

use std::collections::{HashMap, HashSet};
use pyo3::prelude::*;
use pyo3::types::PyDict;
use pyo3::IntoPyObjectExt;
use half::f16;

use crate::storage::{BlobRecord, MetaRecord, StorageError};
use crate::types::{MemHopError};
use crate::recall_strategies::{RecallScope, TimeRange, scope_to_candidates};
use crate::scene_gating::SceneState;
use crate::meta_index::MetaIndex;
use crate::storage::LmdbStorage;

use super::helpers::{now_millis, parse_datetime_to_millis};

// ── Search filter types ────────────────────────────────────

pub(crate) struct FilterCriteria {
    pub(crate) layer: Option<String>,
    pub(crate) r#type: Option<String>,
    pub(crate) domain: Option<String>,
    pub(crate) is_dormant: Option<bool>,
    pub(crate) protection: Option<String>,
    pub(crate) session_id: Option<String>,
    pub(crate) path: Option<String>,
    pub(crate) parent: Option<String>,
    pub(crate) importance_gt: Option<f32>,
    pub(crate) importance_lt: Option<f32>,
    pub(crate) tags_contains: Option<String>,
    pub(crate) connections_to: Option<String>,
}

pub(crate) fn parse_filters(filters: &Bound<'_, PyDict>) -> PyResult<FilterCriteria> {
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

pub(crate) fn matches_filters(blob: &BlobRecord, meta: &MetaRecord, criteria: &FilterCriteria) -> bool {
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
        && meta.protection != super::helpers::protection_str_to_u8(prot_str) {
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
pub(crate) fn parse_recall_scope(scope_dict: &HashMap<String, PyObject>, py: Python<'_>) -> RecallScope {
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

/// Attempt to build an auto-scope from scene gating.
/// Returns Ok(None) when no gate matches (same as no scope).
pub(crate) fn scene_gating_to_auto_scope(
    scene_state: &SceneState,
    query_f32: &[f32],
    meta_index: &MetaIndex,
    storage: &LmdbStorage,
    now_ms: i64,
) -> Result<Option<HashSet<String>>, StorageError> {
    if !scene_state.gating_enabled {
        return Ok(None);
    }

    // Layer 1: session fingerprint match
    if let Some(sid) = scene_state.match_session_fingerprint(query_f32) {
        let mut rc = RecallScope::default();
        rc.session_id = Some(sid);
        return scope_to_candidates(&rc, meta_index, storage, now_ms);
    }

    // Layer 2: knowledge tree path prediction
    if let Some(tree_root) = scene_state.predict_tree_path(query_f32) {
        let mut rc = RecallScope::default();
        rc.knowledge_tree = Some(tree_root);
        return scope_to_candidates(&rc, meta_index, storage, now_ms);
    }

    // Layer 3: active scene anchoring
    if let Some(ref active) = scene_state.active_scene {
        if active.miss_count < 3 {
            if let Some(ref sid) = active.session_id {
                let mut rc = RecallScope::default();
                rc.session_id = Some(sid.clone());
                return scope_to_candidates(&rc, meta_index, storage, now_ms);
            }
        }
    }

    Ok(None)
}
