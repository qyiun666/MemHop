#![cfg(feature = "onnx")]

//! Cross-Encoder reranker for precision re-ranking.
//!
//! Uses ONNX BGE-Reranker-v2-m3 to score query-candidate pairs.
//! Applied on top-k candidates from RRF fusion to achieve >0.95 NDCG.

use std::sync::{Mutex, OnceLock};

use ort::session::builder::GraphOptimizationLevel;
use ort::session::Session;
use ort::value::Tensor;
use tokenizers::Tokenizer;

/// Serialise ORT session creation to avoid potential global-init races
/// when `from_path` is called concurrently from multiple threads.
static RERANKER_BUILD_LOCK: OnceLock<Mutex<()>> = OnceLock::new();

/// Cross-encoder reranker using BGE-Reranker-v2-m3 ONNX model.
///
/// Scores query-candidate pairs and re-ranks top-k candidates
/// for improved retrieval precision.
pub struct Reranker {
    session: Mutex<Session>,
    tokenizer: Tokenizer,
    max_length: usize,
}

impl Reranker {
    /// Load reranker model from a directory containing `model.onnx` and `tokenizer.json`.
    ///
    /// # Errors
    /// - Missing `model.onnx` or `tokenizer.json` in the directory.
    /// - ONNX session build fails (e.g. incompatible model format).
    /// - Tokenizer loading fails.
    pub fn from_path(model_dir: &str) -> Result<Self, Box<dyn std::error::Error>> {
        let dir = std::path::Path::new(model_dir);
        let model_path = dir.join("model.onnx");
        let tokenizer_path = dir.join("tokenizer.json");

        if !model_path.exists() {
            return Err(format!("model.onnx not found in {model_dir}").into());
        }
        if !tokenizer_path.exists() {
            return Err(format!("tokenizer.json not found in {model_dir}").into());
        }

        let tokenizer = Tokenizer::from_file(&tokenizer_path)
            .map_err(|e| format!("failed to load tokenizer: {e}"))?;

        // Serialise ORT session creation across threads
        let _guard = RERANKER_BUILD_LOCK
            .get_or_init(|| Mutex::new(())).lock().unwrap();

        let mut builder = Session::builder()?;
        builder = builder
            .with_optimization_level(GraphOptimizationLevel::Level3)
            .map_err(|e| format!("optimization level: {e}"))?;
        builder = builder
            .with_intra_threads(4)
            .map_err(|e| format!("intra threads: {e}"))?;
        let session = builder
            .commit_from_file(&model_path)
            .map_err(|e| format!("load model: {e}"))?;

        drop(_guard);

        Ok(Reranker {
            session: Mutex::new(session),
            tokenizer,
            max_length: 512,
        })
    }

    /// Score a single query-candidate pair.
    ///
    /// Returns a relevance score in [0.0, 1.0] (higher = more relevant).
    ///
    /// The input is formatted as `query [SEP] candidate` and tokenized.
    /// The ONNX model returns a single logit which is sigmoid-normalised.
    pub fn score(&self, query: &str, candidate: &str) -> Result<f32, String> {
        // Cross-encoder input format: [CLS] query [SEP] candidate [SEP]
        let text = format!("{} [SEP] {}", query, candidate);

        let encoded = self
            .tokenizer
            .encode(text, false)
            .map_err(|e| format!("tokenizer encode: {e}"))?;

        let ids: Vec<i64> = encoded
            .get_ids()
            .iter()
            .take(self.max_length)
            .map(|&x| x as i64)
            .collect();
        let mask: Vec<i64> = encoded
            .get_attention_mask()
            .iter()
            .take(self.max_length)
            .map(|&x| x as i64)
            .collect();

        let n = ids.len();
        let input_tensor = Tensor::from_array((vec![1i64, n as i64], ids))
            .map_err(|e| format!("input tensor: {e}"))?;
        let mask_tensor = Tensor::from_array((vec![1i64, n as i64], mask))
            .map_err(|e| format!("mask tensor: {e}"))?;

        let mut lock = self
            .session
            .lock()
            .map_err(|e| format!("session lock: {e}"))?;
        let outputs = lock
            .run(ort::inputs!["input_ids" => input_tensor, "attention_mask" => mask_tensor])
            .map_err(|e| format!("onnx run: {e}"))?;

        // BGE-Reranker returns a single logit as output
        let (_shape, logits) = outputs[0]
            .try_extract_tensor::<f32>()
            .map_err(|e| format!("extract output: {e}"))?;

        let logit: f32 = logits.iter().copied().next().unwrap_or(0.0);

        // Apply sigmoid to normalise to [0.0, 1.0]
        Ok(1.0 / (1.0 + (-logit).exp()))
    }

    /// Rerank a list of candidates by relevance to the query.
    ///
    /// Returns `Vec<(index, score)>` sorted by descending score.
    /// Each entry gives the original index and the relevance score.
    ///
    /// # Errors
    /// Propagates errors from `score()` for any individual candidate.
    pub fn rerank(&self, query: &str, candidates: &[&str]) -> Result<Vec<(usize, f32)>, String> {
        let mut scores: Vec<(usize, f32)> = Vec::with_capacity(candidates.len());
        for (i, cand) in candidates.iter().enumerate() {
            let s = self.score(query, cand)?;
            scores.push((i, s));
        }
        scores.sort_by(|a, b| {
            b.1.partial_cmp(&a.1)
                .unwrap_or(std::cmp::Ordering::Equal)
        });
        Ok(scores)
    }
}
