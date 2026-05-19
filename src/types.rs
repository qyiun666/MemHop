use half::f16;
use pyo3::prelude::*;
use serde::{Serialize, Deserialize};
use std::collections::HashMap;

// ── Vector dimension ──────────────────────────────────────
pub const VECTOR_DIM: usize = 1024;

// ── Protection levels ─────────────────────────────────────

#[derive(Debug, Clone, Serialize, Deserialize)]
pub enum Protection {
    Normal,
    Protected,
    Permanent,
}

// ── Exceptions ─────────────────────────────────────────────

pyo3::create_exception!(memhop, MemHopError, pyo3::exceptions::PyException);
pyo3::create_exception!(memhop, MemHopClosedError, MemHopError);

// ── PyMemory ──────────────────────────────────────────────

#[pyclass(name = "Memory")]
#[derive(Debug)]
pub struct PyMemory {
    #[pyo3(get)]
    id: String,
    #[pyo3(get)]
    text: String,
    #[pyo3(get)]
    meta: HashMap<String, PyObject>,
    #[pyo3(get)]
    confidence: f64,
    #[pyo3(get)]
    created_at: String,
    #[pyo3(get)]
    content_type: Option<String>,
    #[pyo3(get)]
    blob: Option<Vec<u8>>,
}

#[pymethods]
impl PyMemory {
    #[new]
    #[pyo3(signature = (id, text, meta=None, confidence=0.0, created_at="", content_type=None, blob=None))]
    fn new(
        id: String,
        text: String,
        meta: Option<HashMap<String, PyObject>>,
        confidence: f64,
        created_at: &str,
        content_type: Option<String>,
        blob: Option<Vec<u8>>,
    ) -> Self {
        PyMemory {
            id,
            text,
            meta: meta.unwrap_or_default(),
            confidence,
            created_at: created_at.to_string(),
            content_type,
            blob,
        }
    }

    fn __repr__(&self) -> String {
        let text_preview = if self.text.len() > 40 {
            let end = self.text.floor_char_boundary(40);
            format!("{}...", &self.text[..end])
        } else {
            self.text.clone()
        };
        let mut extra = String::new();
        if let Some(ref ct) = self.content_type {
            extra.push_str(&format!(", content_type='{}'", ct));
        }
        if let Some(ref b) = self.blob {
            extra.push_str(&format!(", blob_size={}", b.len()));
        }
        format!(
            "Memory(id='{}', text='{}', confidence={:.2}{})",
            self.id, text_preview, self.confidence, extra
        )
    }
}

// ── PyMemory Rust-side constructor ────────────────────────

impl PyMemory {
    pub fn create(
        id: String,
        text: String,
        meta: HashMap<String, PyObject>,
        confidence: f64,
        created_at: String,
        content_type: Option<String>,
        blob: Option<Vec<u8>>,
    ) -> Self {
        PyMemory {
            id,
            text,
            meta,
            confidence,
            created_at,
            content_type,
            blob,
        }
    }
}

// ── Internal MemoryRecord (Rust-side storage) ─────────────

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct MemoryRecord {
    pub id: String,
    pub text: String,
    pub meta: HashMap<String, serde_json::Value>,
    pub confidence: f64,
    pub created_at: String,
    pub protection: Protection,
    pub dense_vector: Vec<f16>,
    pub sparse_vector: HashMap<String, f32>,
}