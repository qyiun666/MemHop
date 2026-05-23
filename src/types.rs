use half::f16;
use pyo3::prelude::*;
use serde::{Serialize, Deserialize};
use std::collections::HashMap;
use std::fmt;

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
    pub id: String,
    #[pyo3(get)]
    pub text: String,
    #[pyo3(get)]
    pub meta: HashMap<String, PyObject>,
    #[pyo3(get)]
    pub confidence: f64,
    #[pyo3(get)]
    pub created_at: String,
    #[pyo3(get)]
    pub content_type: Option<String>,
    #[pyo3(get)]
    pub blob: Option<Vec<u8>>,
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

// ── v0.5.0 BrainLoop types ────────────────────────────────

/// LLM reasoning strategy / model tier
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub enum Route {
    Fast,
    Deep,
    Reasoning,
}

/// Hint from brain to body about recommended LLM strategy
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub enum StrategyHint {
    SwitchToFastModel,
    SwitchToDeepModel,
    RetryWithRefinement,
}

/// Cognitive health metrics for each turn
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CognitionHealth {
    pub llm_calls: u8,
    pub tokens_used: u32,
    pub total_memories: u64,
    pub avg_confidence: f32,
    pub strategy_hint: Option<StrategyHint>,
}

/// Meta-notifications from brain to body (signal only, no data)
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct BrainNotifications {
    pub new_knowledge_count: u32,
    pub compression_triggered: bool,
    pub cognition_health: CognitionHealth,
}

/// Actions the body (MeowAgent) must perform
#[derive(Debug, Serialize, Deserialize)]
#[serde(tag = "type")]
pub enum BodyAction {
    Tool {
        name: String,
        params: serde_json::Value,
    },
    AskUser {
        question: String,
        options: Vec<String>,
        danger_level: String,
    },
    HearMore {
        prompt: String,
    },
    ReadFile {
        path: String,
    },
}

/// Output from body actions fed back into brain
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct BodyResult {
    pub source: String,
    pub text: String,
    pub meta: HashMap<String, serde_json::Value>,
}

/// The three output variants from BrainLoop
#[derive(Debug, Serialize, Deserialize)]
#[serde(tag = "type")]
pub enum BrainAction {
    /// Streaming token during LLM thinking
    Streaming { chunk: String },
    /// Brain needs body to act — loop pauses
    NeedBody {
        actions: Vec<BodyAction>,
        context: String,
    },
    /// Cognitive loop complete
    Done {
        for_user: String,
        notifications: BrainNotifications,
    },
}

/// BrainLoop configuration
#[pyclass(name = "BrainConfig")]
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct BrainConfig {
    #[pyo3(get, set)]
    pub max_attempts: u8,
    #[pyo3(get, set)]
    pub confidence_threshold: f32,
    #[pyo3(get, set)]
    pub compress_threshold: u64,
    #[pyo3(get, set)]
    pub auto_consolidate: bool,
    #[pyo3(get, set)]
    pub scene_aware: bool,
    #[pyo3(get, set)]
    pub plasticity_enabled: bool,
    #[pyo3(get, set)]
    pub calibrate_threshold: u64,
    #[pyo3(get, set)]
    pub fast_path_threshold: f32,
}

#[pymethods]
impl BrainConfig {
    #[new]
    #[pyo3(signature = (
        max_attempts=3,
        confidence_threshold=0.3,
        compress_threshold=10,
        auto_consolidate=true,
        scene_aware=true,
        plasticity_enabled=true,
        calibrate_threshold=20,
        fast_path_threshold=0.85,
    ))]
    fn py_new(
        max_attempts: u8,
        confidence_threshold: f32,
        compress_threshold: u64,
        auto_consolidate: bool,
        scene_aware: bool,
        plasticity_enabled: bool,
        calibrate_threshold: u64,
        fast_path_threshold: f32,
    ) -> Self {
        BrainConfig {
            max_attempts,
            confidence_threshold,
            compress_threshold,
            auto_consolidate,
            scene_aware,
            plasticity_enabled,
            calibrate_threshold,
            fast_path_threshold,
        }
    }

    fn __repr__(&self) -> String {
        format!(
            "BrainConfig(max_attempts={}, confidence_threshold={}, compress_threshold={})",
            self.max_attempts, self.confidence_threshold, self.compress_threshold
        )
    }
}

impl Default for BrainConfig {
    fn default() -> Self {
        BrainConfig {
            max_attempts: 3,
            confidence_threshold: 0.3,
            compress_threshold: 10,
            auto_consolidate: true,
            scene_aware: true,
            plasticity_enabled: true,
            calibrate_threshold: 20,
            fast_path_threshold: 0.85,
        }
    }
}

// ── ModelSlot ─────────────────────────────────────────────

/// A model configuration slot — position determines role.
///
/// In a dual-model setup:
/// - `models[0]` = thinker (deep reasoning, required)
/// - `models[1]` = calibrator (memory maintenance, optional)
#[pyclass(name = "ModelSlot")]
#[derive(Debug, Clone)]
pub struct ModelSlot {
    #[pyo3(get, set)]
    pub endpoint: String,
    #[pyo3(get, set)]
    pub api_key: Option<String>,
    #[pyo3(get, set)]
    pub model: String,
}

#[pymethods]
impl ModelSlot {
    #[new]
    #[pyo3(signature = (endpoint, api_key=None, model="gpt-4o"))]
    fn new(endpoint: &str, api_key: Option<String>, model: &str) -> Self {
        ModelSlot {
            endpoint: endpoint.to_string(),
            api_key: api_key.filter(|k| !k.is_empty()),
            model: model.to_string(),
        }
    }

    fn __repr__(&self) -> String {
        format!(
            "ModelSlot(endpoint='{}', model='{}')",
            self.endpoint, self.model
        )
    }
}

/// BrainLoop error variants
#[derive(Debug)]
pub enum BrainError {
    ThinkerFailed(String),
    GateRejected(String),
    MaxAttemptsExceeded,
    Internal(String),
    CalibratorFailed(String),
    ParseError,
}

impl fmt::Display for BrainError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            BrainError::ThinkerFailed(msg) => write!(f, "Thinker failed: {}", msg),
            BrainError::GateRejected(msg) => write!(f, "Gate rejected: {}", msg),
            BrainError::MaxAttemptsExceeded => write!(f, "Max attempts exceeded"),
            BrainError::Internal(msg) => write!(f, "Internal error: {}", msg),
            BrainError::CalibratorFailed(msg) => write!(f, "Calibrator failed: {}", msg),
            BrainError::ParseError => write!(f, "Parse error"),
        }
    }
}

impl std::error::Error for BrainError {}

// ── Python-facing wrapper types (pyo3) ───────────────────

/// Python-facing BodyAction — flat representation of the BodyAction enum.
#[pyclass(name = "BodyAction")]
#[derive(Debug, Clone)]
pub struct PyBodyAction {
    #[pyo3(get)]
    pub action_type: String,
    #[pyo3(get)]
    pub name: Option<String>,
    #[pyo3(get)]
    pub params: Option<String>,
    #[pyo3(get)]
    pub question: Option<String>,
    #[pyo3(get)]
    pub options: Option<Vec<String>>,
    #[pyo3(get)]
    pub danger_level: Option<String>,
    #[pyo3(get)]
    pub prompt: Option<String>,
    #[pyo3(get)]
    pub path: Option<String>,
}

impl From<BodyAction> for PyBodyAction {
    fn from(a: BodyAction) -> Self {
        match a {
            BodyAction::Tool { name, params } => PyBodyAction {
                action_type: "Tool".into(),
                name: Some(name),
                params: Some(params.to_string()),
                question: None,
                options: None,
                danger_level: None,
                prompt: None,
                path: None,
            },
            BodyAction::AskUser {
                question,
                options,
                danger_level,
            } => PyBodyAction {
                action_type: "AskUser".into(),
                name: None,
                params: None,
                question: Some(question),
                options: Some(options),
                danger_level: Some(danger_level),
                prompt: None,
                path: None,
            },
            BodyAction::HearMore { prompt } => PyBodyAction {
                action_type: "HearMore".into(),
                name: None,
                params: None,
                question: None,
                options: None,
                danger_level: None,
                prompt: Some(prompt),
                path: None,
            },
            BodyAction::ReadFile { path } => PyBodyAction {
                action_type: "ReadFile".into(),
                name: None,
                params: None,
                question: None,
                options: None,
                danger_level: None,
                prompt: None,
                path: Some(path),
            },
        }
    }
}

/// Python-facing CognitionHealth.
#[pyclass(name = "CognitionHealth")]
#[derive(Debug, Clone)]
pub struct PyCognitionHealth {
    #[pyo3(get)]
    pub llm_calls: u8,
    #[pyo3(get)]
    pub tokens_used: u32,
    #[pyo3(get)]
    pub total_memories: u64,
    #[pyo3(get)]
    pub avg_confidence: f32,
    #[pyo3(get)]
    pub strategy_hint: Option<String>,
}

impl From<CognitionHealth> for PyCognitionHealth {
    fn from(h: CognitionHealth) -> Self {
        PyCognitionHealth {
            llm_calls: h.llm_calls,
            tokens_used: h.tokens_used,
            total_memories: h.total_memories,
            avg_confidence: h.avg_confidence,
            strategy_hint: h.strategy_hint.map(|s| format!("{:?}", s)),
        }
    }
}

/// Python-facing BrainNotifications.
#[pyclass(name = "BrainNotifications")]
#[derive(Debug, Clone)]
pub struct PyBrainNotifications {
    #[pyo3(get)]
    pub new_knowledge_count: u32,
    #[pyo3(get)]
    pub compression_triggered: bool,
    #[pyo3(get)]
    pub cognition_health: PyCognitionHealth,
}

impl From<BrainNotifications> for PyBrainNotifications {
    fn from(n: BrainNotifications) -> Self {
        PyBrainNotifications {
            new_knowledge_count: n.new_knowledge_count,
            compression_triggered: n.compression_triggered,
            cognition_health: n.cognition_health.into(),
        }
    }
}

/// Python-facing BrainAction — flat representation of the BrainAction enum.
#[pyclass(name = "BrainAction")]
#[derive(Debug, Clone)]
pub struct PyBrainAction {
    #[pyo3(get)]
    pub action_type: String,
    #[pyo3(get)]
    pub chunk: Option<String>,
    #[pyo3(get)]
    pub actions: Option<Vec<PyBodyAction>>,
    #[pyo3(get)]
    pub context: Option<String>,
    #[pyo3(get)]
    pub for_user: Option<String>,
    #[pyo3(get)]
    pub notifications: Option<PyBrainNotifications>,
}

impl From<BrainAction> for PyBrainAction {
    fn from(a: BrainAction) -> Self {
        match a {
            BrainAction::Streaming { chunk } => PyBrainAction {
                action_type: "Streaming".into(),
                chunk: Some(chunk),
                actions: None,
                context: None,
                for_user: None,
                notifications: None,
            },
            BrainAction::NeedBody { actions, context } => PyBrainAction {
                action_type: "NeedBody".into(),
                chunk: None,
                actions: Some(actions.into_iter().map(|a| a.into()).collect()),
                context: Some(context),
                for_user: None,
                notifications: None,
            },
            BrainAction::Done {
                for_user,
                notifications,
            } => PyBrainAction {
                action_type: "Done".into(),
                chunk: None,
                actions: None,
                context: None,
                for_user: Some(for_user),
                notifications: Some(notifications.into()),
            },
        }
    }
}

/// Python-facing BodyResult — input for feed_body_result().
#[pyclass(name = "BodyResult")]
#[derive(Debug, Clone)]
pub struct PyBodyResult {
    #[pyo3(get, set)]
    pub source: String,
    #[pyo3(get, set)]
    pub text: String,
    #[pyo3(get, set)]
    pub meta: HashMap<String, String>,
}

#[pymethods]
impl PyBodyResult {
    #[new]
    #[pyo3(signature = (source="", text="", meta=None))]
    fn new(source: &str, text: &str, meta: Option<HashMap<String, String>>) -> Self {
        PyBodyResult {
            source: source.to_string(),
            text: text.to_string(),
            meta: meta.unwrap_or_default(),
        }
    }
}

impl From<PyBodyResult> for BodyResult {
    fn from(p: PyBodyResult) -> Self {
        let mut meta = HashMap::new();
        for (k, v) in p.meta {
            meta.insert(k, serde_json::Value::String(v));
        }
        BodyResult {
            source: p.source,
            text: p.text,
            meta,
        }
    }
}