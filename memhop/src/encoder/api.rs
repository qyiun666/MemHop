//! Generic OpenAI-compatible HTTP embedding encoder — optional,
//! feature-gated drop-in for [`crate::Encoder`].
//!
//! # What this is
//!
//! [`ApiEncoder`] talks to any service that exposes the OpenAI-style
//! `POST {base_url}/embeddings` contract:
//!
//! ```text
//! POST {base_url}/embeddings
//! Authorization: Bearer {api_key}
//! Content-Type: application/json
//!
//! { "model": "{model}", "input": "{text}" }
//!
//! → { "data": [ { "embedding": [0.1, 0.2, ...] } ] }
//! ```
//!
//! That covers SiliconFlow, OpenAI, Jina, DeepInfra, Together, vLLM
//! `--api-key`-style servers, and any self-hosted gateway that
//! mimics the format. Nothing is hardcoded — the user passes the
//! `base_url`, `api_key` and `model` at construction time.
//!
//! # Activation
//!
//! Enable the `api-encoder` cargo feature to pull in
//! `reqwest` (blocking + JSON). The default build does **not** depend
//! on `reqwest` so the base crate stays free of HTTP / TLS code.
//!
//! ```toml
//! [dependencies]
//! memhop = { version = "*", features = ["api-encoder"] }
//! ```
//!
//! # Wiring an agent
//!
//! ```no_run
//! # #[cfg(feature = "api-encoder")]
//! # fn demo() -> memhop::Result<()> {
//! use memhop::{ApiEncoder, MemHop};
//!
//! // SiliconFlow BGE-M3
//! let encoder = ApiEncoder::new(
//!     "https://api.siliconflow.cn/v1",
//!     "sk-xxx",
//!     "BAAI/bge-m3",
//! ).map_err(|e| memhop::MemHopError::Internal(e.to_string()))?;
//!
//! let engine = MemHop::open_with_encoder("./data", Box::new(encoder))?;
//! # let _ = engine;
//! # Ok(())
//! # }
//! ```
//!
//! Other examples:
//! - OpenAI:   `("https://api.openai.com/v1", "sk-...", "text-embedding-3-large")`
//! - Jina:     `("https://api.jina.ai/v1",    "jina_...", "jina-embeddings-v3")`
//! - Self-host: `("http://127.0.0.1:8080/v1", "any",    "your-model")`
//!
//! # Dimension contract
//!
//! Hopfield recall expects exactly [`crate::types::VECTOR_DIM`] (1024)
//! dimensions. When the upstream model returns a different hidden size
//! we project transparently:
//! - smaller (768, 512, 384, …) → right-pad with zeros, norm preserved.
//! - larger  (1536, 3072, …)    → truncate, then re-L2-normalize.
//!
//! The model's *native* dimension is discovered on the first successful
//! call (a probe issued by [`ApiEncoder::new`]) and cached on `self`.
//!
//! # Error handling
//!
//! [`Encoder::encode`] returns `Vec<f16>` with no `Result`, so transient
//! API failures (network error, timeout, 5xx, malformed JSON, …) cannot
//! propagate. They are logged to `stderr` (with the API key redacted)
//! and the call returns a zero vector. Fusion in
//! [`crate::HybridEncoder`] gracefully degrades to the primary ngram
//! side when one input has zero norm.
//!
//! Authentication failures are detected at construction time by the
//! probe call and returned as `Err` so misconfiguration surfaces
//! immediately instead of being silently masked by zero vectors at
//! recall time.

#![cfg(feature = "api-encoder")]

use std::sync::RwLock;
use std::time::Duration;

use half::f16;
use reqwest::blocking::Client;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;

use crate::encoder::{Encoder, EncoderOutput};
use crate::engram::VECTOR_DIM;

/// Default request timeout. Embedding endpoints are normally fast
/// (<1 s) but cold-start on shared inference services can spike to a
/// few seconds; 10 s leaves enough headroom without hanging the
/// recall path indefinitely.
const DEFAULT_TIMEOUT_SECS: u64 = 10;

/// Generic OpenAI-compatible HTTP embedding encoder.
///
/// Construction performs a single probe request to validate the
/// endpoint and discover the model's native output dimension. After
/// that the encoder is purely functional — `encode()` is a single
/// blocking POST per call.
///
/// `Send + Sync` is satisfied because:
/// - `reqwest::blocking::Client` is `Send + Sync`.
/// - `String` fields are immutable after construction.
/// - The discovered `model_dim` lives behind an `RwLock` to allow a
///   one-time refresh if the first probe failed (degraded mode).
pub struct ApiEncoder {
    base_url: String,
    api_key: String,
    model: String,
    client: Client,
    /// Native dimension reported by the model. Cached from the
    /// construction probe; falls back to [`VECTOR_DIM`] when the
    /// probe failed so subsequent calls still attempt projection.
    model_dim: RwLock<usize>,
}

/// JSON body for `POST /embeddings`. Single-string `input` keeps the
/// surface minimal and matches every OpenAI-compatible provider; we
/// don't expose batch encoding because the [`Encoder`] trait
/// processes one text at a time anyway.
#[derive(Serialize)]
struct EmbeddingRequest<'a> {
    model: &'a str,
    input: &'a str,
}

#[derive(Deserialize)]
struct EmbeddingResponse {
    data: Vec<EmbeddingItem>,
}

#[derive(Deserialize)]
struct EmbeddingItem {
    embedding: Vec<f32>,
}

impl ApiEncoder {
    /// Construct a new encoder and probe the endpoint.
    ///
    /// Trims a trailing slash from `base_url` so callers can pass
    /// either form (`".../v1"` or `".../v1/"`).
    ///
    /// Returns `Err` if the probe call fails — this surfaces wrong
    /// `base_url` / `api_key` / `model` early instead of silently
    /// returning zero vectors at recall time.
    pub fn new(
        base_url: &str,
        api_key: &str,
        model: &str,
    ) -> Result<Self, Box<dyn std::error::Error>> {
        Self::with_timeout(base_url, api_key, model, Duration::from_secs(DEFAULT_TIMEOUT_SECS))
    }

    /// Same as [`ApiEncoder::new`] but lets callers override the per-
    /// request timeout. Useful for slow / cold inference endpoints
    /// or for tightening the budget on hot recall paths.
    pub fn with_timeout(
        base_url: &str,
        api_key: &str,
        model: &str,
        timeout: Duration,
    ) -> Result<Self, Box<dyn std::error::Error>> {
        if base_url.is_empty() {
            return Err("base_url must not be empty".into());
        }
        if model.is_empty() {
            return Err("model must not be empty".into());
        }

        let client = Client::builder()
            .timeout(timeout)
            .build()
            .map_err(|e| format!("failed to build reqwest client: {e}"))?;

        let me = ApiEncoder {
            base_url: base_url.trim_end_matches('/').to_string(),
            api_key: api_key.to_string(),
            model: model.to_string(),
            client,
            model_dim: RwLock::new(VECTOR_DIM),
        };

        // Probe with a short, side-effect-free input. Any non-empty
        // ASCII string works — we only need a successful response to
        // learn the native dimension. Failure is fatal so the user
        // notices misconfiguration immediately.
        let probe = me
            .raw_embedding("a")
            .map_err(|e| format!("api probe failed: {}", redact(&me.api_key, &e)))?;

        if let Ok(mut guard) = me.model_dim.write() {
            *guard = probe.len();
        }

        Ok(me)
    }

    /// Native hidden size discovered during construction. Returns
    /// [`VECTOR_DIM`] if the lock is poisoned (never expected in
    /// practice — `model_dim` is only written once during `new()`).
    pub fn model_dim(&self) -> usize {
        match self.model_dim.read() {
            Ok(g) => *g,
            Err(_) => VECTOR_DIM,
        }
    }

    /// Issue a single embedding request and return the raw `f32`
    /// vector in the model's native dimension. No projection or
    /// re-normalization is applied here; callers are responsible for
    /// shaping the result.
    fn raw_embedding(&self, text: &str) -> Result<Vec<f32>, String> {
        let url = format!("{}/embeddings", self.base_url);
        let body = EmbeddingRequest { model: &self.model, input: text };

        let resp = self
            .client
            .post(&url)
            .bearer_auth(&self.api_key)
            .json(&body)
            .send()
            .map_err(|e| format!("request error: {e}"))?;

        if !resp.status().is_success() {
            let status = resp.status();
            // Best-effort body read, capped to avoid logging huge HTML
            // error pages from gateways.
            let snippet = resp
                .text()
                .unwrap_or_default()
                .chars()
                .take(256)
                .collect::<String>();
            return Err(format!("http {status}: {snippet}"));
        }

        let parsed: EmbeddingResponse = resp
            .json()
            .map_err(|e| format!("malformed embedding response: {e}"))?;

        let first = parsed
            .data
            .into_iter()
            .next()
            .ok_or_else(|| "embedding response had empty `data`".to_string())?;

        if first.embedding.is_empty() {
            return Err("embedding response had empty vector".to_string());
        }

        Ok(first.embedding)
    }
}

impl Encoder for ApiEncoder {
    fn encode(&self, text: &str) -> EncoderOutput {
        // Empty / whitespace-only inputs map to a zero vector. Mirrors
        // [`OnnxEncoder`] so HybridEncoder can degrade to the primary
        // ngram side without an extra branch.
        if text.trim().is_empty() {
            return EncoderOutput { dense: vec![f16::ZERO; VECTOR_DIM], sparse: HashMap::new() };
        }

        let raw = match self.raw_embedding(text) {
            Ok(v) => v,
            Err(e) => {
                // Log + degrade. The trait can't return Result so a
                // crash here would take down the whole recall path.
                eprintln!("memhop::ApiEncoder: encode failed ({}); returning zero vector", redact(&self.api_key, &e));
                return EncoderOutput { dense: vec![f16::ZERO; VECTOR_DIM], sparse: HashMap::new() };
            }
        };

        let normalized = l2_normalize(raw);
        let projected = project_to_dim(normalized, VECTOR_DIM);
        let dense = projected.into_iter().map(f16::from_f32).collect();
        EncoderOutput { dense, sparse: HashMap::new() }
    }

    fn dim(&self) -> usize {
        VECTOR_DIM
    }
}

/// Defensive scrub: ensure the API key never leaks into log lines.
/// Even though we don't deliberately format the key into errors, a
/// future change in `reqwest` (or a misbehaving server echoing the
/// header) could expose it; redacting here is cheap insurance.
fn redact(api_key: &str, msg: &str) -> String {
    if api_key.is_empty() {
        return msg.to_string();
    }
    msg.replace(api_key, "***")
}

/// L2-normalise `v` in place. Zero vectors are returned unchanged so
/// callers can detect / skip them.
fn l2_normalize(mut v: Vec<f32>) -> Vec<f32> {
    let norm: f32 = v.iter().map(|x| x * x).sum::<f32>().sqrt();
    if norm > 1e-8 {
        for x in v.iter_mut() {
            *x /= norm;
        }
    }
    v
}

/// Project `src` to exactly `target` dimensions:
/// - smaller → zero-pad the tail (norm preserved, no re-norm needed).
/// - larger  → truncate + re-L2-normalize (otherwise norm drifts <1).
/// - equal   → pass-through.
fn project_to_dim(src: Vec<f32>, target: usize) -> Vec<f32> {
    if src.len() == target {
        return src;
    }
    let mut out = vec![0.0f32; target];
    let copy = src.len().min(target);
    out[..copy].copy_from_slice(&src[..copy]);
    if src.len() > target {
        return l2_normalize(out);
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn redact_replaces_api_key() {
        let out = redact("sk-secret", "request error sk-secret in url");
        assert!(!out.contains("sk-secret"));
        assert!(out.contains("***"));
    }

    #[test]
    fn redact_noop_on_empty_key() {
        let out = redact("", "anything goes");
        assert_eq!(out, "anything goes");
    }

    #[test]
    fn l2_normalize_handles_zero() {
        let v = vec![0.0f32; 4];
        let out = l2_normalize(v);
        assert!(out.iter().all(|x| *x == 0.0));
    }

    #[test]
    fn l2_normalize_unitises() {
        let out = l2_normalize(vec![3.0f32, 4.0]);
        assert!((out[0] - 0.6).abs() < 1e-6);
        assert!((out[1] - 0.8).abs() < 1e-6);
    }

    #[test]
    fn project_pads_smaller() {
        let v = vec![0.6f32, 0.8];
        let out = project_to_dim(v, 4);
        assert_eq!(out.len(), 4);
        assert_eq!(out[2], 0.0);
        assert_eq!(out[3], 0.0);
    }

    #[test]
    fn project_truncates_larger_and_renormalises() {
        let v = vec![0.5f32, 0.5, 0.5, 0.5]; // norm 1.0
        let out = project_to_dim(v, 2);
        assert_eq!(out.len(), 2);
        let norm: f32 = out.iter().map(|x| x * x).sum::<f32>().sqrt();
        assert!((norm - 1.0).abs() < 1e-6);
    }

    #[test]
    fn project_passthrough() {
        let v = vec![0.6f32, 0.8];
        let out = project_to_dim(v.clone(), 2);
        assert_eq!(out, v);
    }

    #[test]
    fn empty_base_url_rejected() {
        let err = ApiEncoder::new("", "k", "m").err().expect("should fail");
        assert!(err.to_string().contains("base_url"));
    }

    #[test]
    fn empty_model_rejected() {
        let err = ApiEncoder::new("http://localhost", "k", "").err().expect("should fail");
        assert!(err.to_string().contains("model"));
    }
}
