//! Search filter types and scope parsing for the engine.

use std::collections::HashMap;

use crate::error::MemHopError;
use crate::storage::{BlobRecord, MetaRecord};

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

pub(crate) fn parse_filters(
    filters: &HashMap<String, serde_json::Value>,
) -> Result<FilterCriteria, MemHopError> {
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

    for (key, val) in filters {
        match key.as_str() {
            "layer" => c.layer = val.as_str().map(|s| s.to_string()),
            "type" => c.r#type = val.as_str().map(|s| s.to_string()),
            "domain" => c.domain = val.as_str().map(|s| s.to_string()),
            "is_dormant" => c.is_dormant = val.as_bool(),
            "protection" => c.protection = val.as_str().map(|s| s.to_string()),
            "session_id" => c.session_id = val.as_str().map(|s| s.to_string()),
            "path" => c.path = val.as_str().map(|s| s.to_string()),
            "parent" => c.parent = val.as_str().map(|s| s.to_string()),
            "importance_gt" => c.importance_gt = val.as_f64().map(|v| v as f32),
            "importance_lt" => c.importance_lt = val.as_f64().map(|v| v as f32),
            "tags_contains" => c.tags_contains = val.as_str().map(|s| s.to_string()),
            "connections_to" => c.connections_to = val.as_str().map(|s| s.to_string()),
            other => {
                return Err(MemHopError::InvalidArgument(format!(
                    "Unknown filter key: '{}'",
                    other
                )));
            }
        }
    }
    Ok(c)
}

pub(crate) fn matches_filters(
    blob: &BlobRecord,
    _meta: &MetaRecord,
    criteria: &FilterCriteria,
) -> bool {
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
    if let Some(is_dormant) = criteria.is_dormant {
        match blob.meta.get("is_dormant") {
            Some(serde_json::Value::Bool(v)) if *v == is_dormant => {}
            _ => return false,
        }
    }
    if let Some(ref protection) = criteria.protection {
        match blob.meta.get("protection") {
            Some(serde_json::Value::String(v)) if v == protection => {}
            _ => return false,
        }
    }
    if let Some(importance_gt) = criteria.importance_gt {
        match blob.meta.get("importance") {
            Some(serde_json::Value::Number(n)) if n.as_f64().unwrap_or(0.0) > importance_gt as f64 => {
            }
            _ => return false,
        }
    }
    if let Some(importance_lt) = criteria.importance_lt {
        match blob.meta.get("importance") {
            Some(serde_json::Value::Number(n))
                if n.as_f64().unwrap_or(f64::MAX) < importance_lt as f64 => {}
            _ => return false,
        }
    }
    if let Some(ref tag) = criteria.tags_contains {
        match blob.meta.get("tags") {
            Some(serde_json::Value::Array(arr)) => {
                if !arr.iter().any(|t| t.as_str() == Some(tag.as_str())) {
                    return false;
                }
            }
            _ => return false,
        }
    }
    if let Some(ref conn_to) = criteria.connections_to {
        match blob.meta.get("connections") {
            Some(serde_json::Value::Array(arr)) => {
                if !arr.iter().any(|c| {
                    c.get("id")
                        .and_then(|v| v.as_str())
                        .is_some_and(|s| s == conn_to)
                }) {
                    return false;
                }
            }
            _ => return false,
        }
    }

    true
}
