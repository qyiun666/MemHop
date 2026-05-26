//! ONNX semantic encoder — optional, feature-gated drop-in for
//! [`crate::Encoder`].
//!
//! # Activation
//!
//! Enable the `onnx` cargo feature to pull in `ort` and `tokenizers`.
//! The default build does **not** depend on any of these crates, so
//! platforms without an onnxruntime toolchain stay buildable.
//!
//! ```toml
//! [dependencies]
//! memhop = { version = "*", features = ["onnx"] }
//! ```
//!
//! Note that we link `ort` in `load-dynamic` mode: at startup the
//! process needs to be able to locate a `libonnxruntime` shared
//! library, either via `ORT_DYLIB_PATH` or a system-wide install.
//! See <https://ort.pyke.io/setup/linking#dynamic-linking> for
//! details.
//!
//! # Wiring an agent
//!
//! ```no_run
//! # #[cfg(feature = "onnx")]
//! # fn demo() -> memhop::Result<()> {
//! use memhop::{MemHop, OnnxEncoder};
//!
//! // Provide a local model directory containing `model.onnx` +
//! // `tokenizer.json`. Any HF-exported encoder (BGE-M3, BGE-small,
//! // E5, multilingual-e5-large …) works as long as it emits a
//! // single floating-point tensor.
//! let onnx = OnnxEncoder::from_path("./models/bge-m3")
//!     .map_err(|e| memhop::MemHopError::Internal(e.to_string()))?;
//! let engine = MemHop::open_with_encoder("./data", Box::new(onnx))?;
//! # let _ = engine;
//! # Ok(())
//! # }
//! ```
//!
//! Agents that do not provide a model fall back to the built-in
//! [`crate::NgramEncoder`] automatically (see
//! [`crate::MemHop::open`]).
//!
//! # Dimension contract
//!
//! Fusion in [`crate::HybridEncoder`] requires `secondary.dim() ==
//! primary.dim() == VECTOR_DIM` (1024). When the underlying model
//! emits a different hidden size we transparently project to
//! `VECTOR_DIM`:
//! - smaller (e.g. 384/768 from BGE-small / E5-base): right-pad with
//!   zeros and re-normalize.
//! - larger: truncate and re-normalize.
//!
//! This keeps the engine surface unchanged while accepting a broad
//! set of off-the-shelf models. For best recall quality pick a model
//! whose hidden size already matches `VECTOR_DIM` (BGE-M3 = 1024).

#![cfg(feature = "onnx")]

use std::path::Path;
use std::sync::Mutex;

use half::f16;
use ort::session::{builder::GraphOptimizationLevel, Session};
use ort::value::Value;
use tokenizers::Tokenizer;

use crate::encoder::Encoder;
use crate::types::VECTOR_DIM;

/// Maximum token length fed to the model. Longer inputs are truncated
/// by the tokenizer. 512 matches the context window of the BGE / E5
/// family; encoders trained with longer windows (BGE-M3 supports up
/// to 8192) silently accept this cap and behave like the short-context
/// variant, trading recall on very long passages for predictable
/// latency.
const MAX_TOKENS: usize = 512;

/// Local-ONNX semantic encoder.
///
/// Holds a loaded `ort::Session` plus a Hugging Face `tokenizers`
/// tokenizer. `Session::run` requires `&mut self` in ort 2.x, so the
/// session lives behind a `Mutex` — the engine treats encoders as
/// throughput-bound singletons and embedding work happens on the
/// recall path or background Dream thread, never concurrently for the
/// same query. Cloning is intentionally not provided.
pub struct OnnxEncoder {
    session: Mutex<Session>,
    tokenizer: Tokenizer,
    /// Hidden size emitted by the model, discovered during
    /// construction by running a single token through the graph.
    model_dim: usize,
    /// Whether the loaded model requires a `token_type_ids` input
    /// (BERT-family) in addition to `input_ids` / `attention_mask`.
    needs_token_type_ids: bool,
}

impl OnnxEncoder {
    /// Load a model from a directory containing `model.onnx` and
    /// `tokenizer.json`.
    ///
    /// Returns an error if either file is missing or the model fails
    /// to load. No network access is performed — the model must
    /// already be on disk.
    pub fn from_path(model_dir: &str) -> Result<Self, Box<dyn std::error::Error>> {
        let dir = Path::new(model_dir);
        let model_path = dir.join("model.onnx");
        let tokenizer_path = dir.join("tokenizer.json");

        if !model_path.exists() {
            return Err(format!(
                "model.onnx not found in {} (expected {})",
                model_dir,
                model_path.display()
            )
            .into());
        }
        if !tokenizer_path.exists() {
            return Err(format!(
                "tokenizer.json not found in {} (expected {})",
                model_dir,
                tokenizer_path.display()
            )
            .into());
        }

        let session = Session::builder()?
            .with_optimization_level(GraphOptimizationLevel::Level3)?
            .commit_from_file(&model_path)?;

        // Detect whether the graph wants `token_type_ids` (BERT,
        // distilbert, ...) — XLM-RoBERTa / BGE-M3 ship without it.
        let needs_token_type_ids = session
            .inputs()
            .iter()
            .any(|i| i.name() == "token_type_ids");

        let tokenizer = Tokenizer::from_file(&tokenizer_path)
            .map_err(|e| format!("failed to load tokenizer.json: {e}"))?;

        let me = Self {
            session: Mutex::new(session),
            tokenizer,
            model_dim: VECTOR_DIM, // overwritten by the probe below
            needs_token_type_ids,
        };

        // Probe the model with a single token to discover the actual
        // hidden size. This is a one-time cost at construction; later
        // inferences reuse `model_dim` for the projection step.
        let probe = me
            .raw_pooled_embedding("a")
            .map_err(|e| format!("model probe failed: {e}"))?;

        Ok(Self {
            model_dim: probe.len(),
            ..me
        })
    }

    /// Run the model on `text` and return the L2-normalised pooled
    /// embedding in the **native** hidden size (no projection).
    fn raw_pooled_embedding(
        &self,
        text: &str,
    ) -> Result<Vec<f32>, Box<dyn std::error::Error>> {
        let encoding = self
            .tokenizer
            .encode(text, true)
            .map_err(|e| format!("tokenize failed: {e}"))?;

        let raw_ids = encoding.get_ids();
        let raw_mask = encoding.get_attention_mask();
        let seq_len = raw_ids.len().min(MAX_TOKENS).max(1);

        let ids: Vec<i64> = raw_ids
            .iter()
            .take(seq_len)
            .map(|&x| x as i64)
            .collect();
        let mask: Vec<i64> = raw_mask
            .iter()
            .take(seq_len)
            .map(|&x| x as i64)
            .collect();

        // Use the `(shape, Vec<T>)` tuple form so we don't depend on a
        // specific `ndarray` version — ort copies the data internally.
        let shape: [usize; 2] = [1, seq_len];
        let input_ids_val = Value::from_array((shape, ids))?;
        let attention_mask_val = Value::from_array((shape, mask.clone()))?;

        let mut session = self
            .session
            .lock()
            .map_err(|_| "ONNX session mutex poisoned")?;

        let outputs = if self.needs_token_type_ids {
            let token_type_ids = vec![0i64; seq_len];
            let token_type_ids_val = Value::from_array((shape, token_type_ids))?;
            session.run(ort::inputs![
                "input_ids" => input_ids_val,
                "attention_mask" => attention_mask_val,
                "token_type_ids" => token_type_ids_val,
            ])?
        } else {
            session.run(ort::inputs![
                "input_ids" => input_ids_val,
                "attention_mask" => attention_mask_val,
            ])?
        };

        // Pick the first floating-point output. BGE/E5 ship a single
        // `last_hidden_state` (rank-3) or `sentence_embedding`
        // (rank-2). Either is handled below.
        let (_name, value) = outputs
            .iter()
            .next()
            .ok_or("model produced no outputs")?;

        let (out_shape, data) = value
            .try_extract_tensor::<f32>()
            .map_err(|e| format!("output is not f32 tensor: {e}"))?;
        let dims: Vec<usize> = out_shape.iter().map(|d| *d as usize).collect();

        let pooled: Vec<f32> = match dims.len() {
            // [batch, hidden] — pre-pooled sentence embedding.
            2 => data[..dims[1]].to_vec(),
            // [batch, seq_len, hidden] — apply attention-mask mean pool.
            3 => {
                let seq = dims[1];
                let hidden = dims[2];
                let mut out = vec![0.0f32; hidden];
                let mut count = 0.0f32;
                for t in 0..seq {
                    if mask.get(t).copied().unwrap_or(0) == 0 {
                        continue;
                    }
                    count += 1.0;
                    let base = t * hidden;
                    for h in 0..hidden {
                        out[h] += data[base + h];
                    }
                }
                if count > 0.0 {
                    for v in out.iter_mut() {
                        *v /= count;
                    }
                }
                out
            }
            other => {
                return Err(format!(
                    "unsupported output rank {} (expected 2 or 3)",
                    other
                )
                .into())
            }
        };

        Ok(l2_normalize(pooled))
    }

    /// Native hidden size reported by the loaded model.
    pub fn model_dim(&self) -> usize {
        self.model_dim
    }
}

impl Encoder for OnnxEncoder {
    fn encode(&self, text: &str) -> Vec<f16> {
        // Empty / whitespace-only inputs map to a zero vector so that
        // fusion in `HybridEncoder` gracefully degrades to the
        // primary ngram side (it skips zero-norm inputs).
        if text.trim().is_empty() {
            return vec![f16::ZERO; VECTOR_DIM];
        }

        let raw = match self.raw_pooled_embedding(text) {
            Ok(v) => v,
            Err(_) => return vec![f16::ZERO; VECTOR_DIM],
        };

        let projected = project_to_dim(raw, VECTOR_DIM);
        projected.into_iter().map(f16::from_f32).collect()
    }

    fn sparse(&self, _text: &str) -> Vec<(u64, f32)> {
        // Semantic encoders do not own the sparse hash space used by
        // `SparseIndex` (it must stay aligned with ngram FNV-1a keys
        // so seed lookup keeps working). HybridEncoder ignores this
        // and always routes `sparse()` to the primary ngram encoder,
        // so returning an empty list here is the correct contract.
        Vec::new()
    }

    fn dim(&self) -> usize {
        VECTOR_DIM
    }
}

/// L2-normalise `v` in place (allocation-free except for the input
/// vector itself). Zero vectors are returned unchanged so callers
/// can detect / skip them.
fn l2_normalize(mut v: Vec<f32>) -> Vec<f32> {
    let norm: f32 = v.iter().map(|x| x * x).sum::<f32>().sqrt();
    if norm > 1e-8 {
        for x in v.iter_mut() {
            *x /= norm;
        }
    }
    v
}

/// Project `src` to exactly `target` dimensions by zero-padding the
/// tail (when smaller) or truncating (when larger), then re-L2-
/// normalising. Keeps the returned vector usable by Hopfield without
/// distorting cosine similarity beyond the unavoidable information
/// loss of truncation.
fn project_to_dim(src: Vec<f32>, target: usize) -> Vec<f32> {
    if src.len() == target {
        return src;
    }
    let mut out = vec![0.0f32; target];
    let copy = src.len().min(target);
    out[..copy].copy_from_slice(&src[..copy]);
    if src.len() > target {
        // Truncated → norm drift, restore unit length.
        return l2_normalize(out);
    }
    // Zero-padded → norm preserved, no need to re-normalize.
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn project_pads_smaller_vector() {
        let v = vec![0.6f32, 0.8]; // norm = 1.0
        let out = project_to_dim(v, 4);
        assert_eq!(out.len(), 4);
        assert!((out[0] - 0.6).abs() < 1e-6);
        assert!((out[1] - 0.8).abs() < 1e-6);
        assert_eq!(out[2], 0.0);
        assert_eq!(out[3], 0.0);
        let norm: f32 = out.iter().map(|x| x * x).sum::<f32>().sqrt();
        assert!((norm - 1.0).abs() < 1e-6);
    }

    #[test]
    fn project_truncates_larger_vector() {
        let v = vec![0.5f32, 0.5, 0.5, 0.5]; // norm = 1.0
        let out = project_to_dim(v, 2);
        assert_eq!(out.len(), 2);
        let norm: f32 = out.iter().map(|x| x * x).sum::<f32>().sqrt();
        assert!(
            (norm - 1.0).abs() < 1e-6,
            "truncated projection must be re-normalised, got {norm}"
        );
    }

    #[test]
    fn project_passthrough_when_dims_match() {
        let v = vec![0.6f32, 0.8];
        let out = project_to_dim(v.clone(), 2);
        assert_eq!(out, v);
    }

    #[test]
    fn l2_normalize_handles_zero_vector() {
        let v = vec![0.0f32; 8];
        let out = l2_normalize(v);
        assert!(out.iter().all(|x| *x == 0.0));
    }
}
