//! Standalone helper functions for the engine module.
//! Free of any EngineInner or pyo3 dependency — pure utility logic.

use std::collections::HashMap;

use half::f16;

use crate::storage::{MetaRecord, StorageError};
use crate::types::{MemHopError, MemHopClosedError, Protection};

// ── ID generation ─────────────────────────────────────────

pub(crate) fn generate_memory_id() -> String {
    use rand::Rng;
    let mut rng = rand::thread_rng();
    let bytes: [u8; 6] = rng.r#gen();
    format!(
        "m_{:02x}{:02x}{:02x}{:02x}{:02x}{:02x}",
        bytes[0], bytes[1], bytes[2], bytes[3], bytes[4], bytes[5]
    )
}

// ── Time helpers ──────────────────────────────────────────

pub(crate) fn now_millis() -> i64 {
    chrono::Utc::now().timestamp_millis()
}

pub(crate) fn millis_to_iso(millis: i64) -> String {
    chrono::DateTime::from_timestamp_millis(millis)
        .map(|dt| dt.to_rfc3339())
        .unwrap_or_default()
}

// ── Protection conversion ─────────────────────────────────

pub(crate) fn protection_to_u8(p: &Protection) -> u8 {
    match p {
        Protection::Normal => 0,
        Protection::Protected => 1,
        Protection::Permanent => 2,
    }
}

pub(crate) fn u8_to_protection(v: u8) -> Protection {
    match v {
        0 => Protection::Normal,
        1 => Protection::Protected,
        2 => Protection::Permanent,
        _ => Protection::Normal,
    }
}

pub(crate) fn protection_str_to_u8(s: &str) -> u8 {
    match s {
        "protected" => 1,
        "permanent" => 2,
        _ => 0,
    }
}

// ── Datetime parsing ──────────────────────────────────────

pub(crate) fn parse_datetime_to_millis(s: &str) -> Result<i64, pyo3::PyErr> {
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

// ── JSON meta extraction ──────────────────────────────────

pub(crate) fn extract_protection(json_meta: &HashMap<String, serde_json::Value>) -> Protection {
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

pub(crate) fn extract_is_dormant(json_meta: &HashMap<String, serde_json::Value>) -> bool {
    json_meta
        .get("is_dormant")
        .and_then(|v| v.as_bool())
        .unwrap_or(false)
}

pub(crate) fn extract_importance(json_meta: &HashMap<String, serde_json::Value>) -> f32 {
    json_meta
        .get("importance")
        .and_then(|v| v.as_f64())
        .unwrap_or(0.5) as f32
}

pub(crate) fn extract_importance_decay_rate(
    json_meta: &HashMap<String, serde_json::Value>,
) -> Option<f32> {
    json_meta
        .get("importance_decay_rate")
        .and_then(|v| v.as_f64())
        .map(|v| v as f32)
}

// ── Importance scoring ────────────────────────────────────

/// Compute effective importance considering time decay.
/// effective = importance × decay_rate^(days_elapsed)
pub(crate) fn effective_importance(meta: &MetaRecord, now_ms: i64) -> f32 {
    match meta.importance_decay_rate {
        Some(rate) => {
            let elapsed_ms = (now_ms - meta.created_at).max(0);
            let days = elapsed_ms as f64 / (24.0 * 3600.0 * 1000.0);
            meta.importance * rate.powf(days as f32)
        }
        None => meta.importance,
    }
}

// ── Error conversion ──────────────────────────────────────

pub(crate) fn storage_to_py(err: StorageError) -> pyo3::PyErr {
    MemHopError::new_err(err.to_string())
}

// ── Vector helpers ────────────────────────────────────────

/// Convert f16 dense vector to f32 for Hopfield query.
pub(crate) fn f16_to_f32(v: &[f16]) -> Vec<f32> {
    v.iter().map(|x| x.to_f32()).collect()
}
