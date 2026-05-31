#![cfg(feature = "onnx")]

use std::collections::HashMap;
use std::path::Path;
use std::sync::{Mutex, OnceLock};

use half::f16;
use ort::session::builder::GraphOptimizationLevel;
use ort::session::Session;
use ort::value::Tensor;
use tokenizers::Tokenizer;

use crate::encoder::{Encoder, EncoderOutput};
use crate::engram::VECTOR_DIM;

/// Serialise ORT session creation to avoid potential global init races
/// when `from_path` is called concurrently from multiple threads
/// (e.g. during `Brain::open`).
static ORT_BUILD_LOCK: OnceLock<Mutex<()>> = OnceLock::new();

pub struct OnnxEncoder {
    session: Mutex<Session>,
    tokenizer: Tokenizer,
    model_dim: usize,
    needs_token_type_ids: bool,
}

impl OnnxEncoder {
    pub fn from_path(model_dir: &str) -> Result<Self, Box<dyn std::error::Error>> {
        let dir = Path::new(model_dir);
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

        // Serialise ORT session creation across threads to avoid
        // potential global-init races (e.g. during Brain::open).
        let _guard = ORT_BUILD_LOCK.get_or_init(|| Mutex::new(())).lock().unwrap();

        let mut builder = Session::builder()?;
        // Level1 (basic) — Level3 (full) can take 10+ minutes for 500+ MB
        // models like BGE-M3 on macOS. Level1 is a good balance of startup
        // time and inference speed. First startup takes 30-120s.
        builder = builder.with_optimization_level(GraphOptimizationLevel::Level1)
            .map_err(|e| format!("optimization level: {e}"))?;
        // Single intra-op thread avoids potential deadlocks on some ort builds.
        builder = builder.with_intra_threads(1)
            .map_err(|e| format!("intra threads: {e}"))?;
        let session = builder.commit_from_file(&model_path)
            .map_err(|e| format!("load model: {e}"))?;

        // Determine token_type_ids need from session input count
        // (no inference needed — just inspect the graph signature).
        let needs_token_type_ids = session.inputs().len() > 2;

        // BGE-M3 hidden dim is 1024; we detect from model config if available,
        // otherwise fall back to the standard dim.
        let config_path = dir.join("config.json");
        let model_dim = if config_path.exists() {
            let cfg: serde_json::Value = serde_json::from_reader(
                std::fs::File::open(&config_path)
                    .map_err(|e| format!("open config.json: {e}"))?,
            )
            .map_err(|e| format!("parse config.json: {e}"))?;
            cfg.get("hidden_size")
                .and_then(|v| v.as_u64())
                .unwrap_or(1024) as usize
        } else {
            1024
        };

        eprintln!("memhop-onnx: session created, dim={model_dim} (warm-up deferred to first encode)");

        Ok(OnnxEncoder {
            session: Mutex::new(session),
            tokenizer,
            model_dim,
            needs_token_type_ids,
        })
    }

    fn project_to_dim(v: &[f32]) -> Vec<f16> {
        let mut p = Vec::with_capacity(VECTOR_DIM);
        for (i, x) in v.iter().enumerate() { if i < VECTOR_DIM { p.push(*x); } else { break; } }
        p.resize(VECTOR_DIM, 0.0f32);
        let norm: f32 = p.iter().map(|x| x * x).sum::<f32>().sqrt();
        if norm > 1e-8 { p.iter().map(|x| f16::from_f32(x / norm)).collect() }
        else { vec![f16::ZERO; VECTOR_DIM] }
    }
}

impl Encoder for OnnxEncoder {
    fn encode(&self, text: &str) -> EncoderOutput {
        let encoded = self.tokenizer.encode(text, false).expect("tokenizer encode");
        let ids: Vec<i64> = encoded.get_ids().iter().map(|&x| x as i64).collect();
        let mask: Vec<i64> = encoded.get_attention_mask().iter().map(|&x| x as i64).collect();
        let n = ids.len();

        let input_tensor = Tensor::from_array((vec![1i64, n as i64], ids))
            .expect("input tensor");
        let mask_tensor = Tensor::from_array((vec![1i64, n as i64], mask))
            .expect("mask tensor");

        let mut lock = self.session.lock().unwrap();
        let outputs = if self.needs_token_type_ids {
            let tt: Vec<i64> = vec![0i64; n];
            let tt_tensor = Tensor::from_array((vec![1i64, n as i64], tt))
                .expect("tt tensor");
            lock.run(ort::inputs!["input_ids" => input_tensor, "attention_mask" => mask_tensor, "token_type_ids" => tt_tensor])
                .expect("onnx run")
        } else {
            lock.run(ort::inputs!["input_ids" => input_tensor, "attention_mask" => mask_tensor])
                .expect("onnx run")
        };
        let (_, last_hidden) = outputs[0].try_extract_tensor::<f32>()
            .expect("extract output");
        let cls: Vec<f32> = last_hidden.iter().take(self.model_dim).copied().collect();
        let dense = Self::project_to_dim(&cls);

        EncoderOutput { dense, sparse: HashMap::new() }
    }

    fn dim(&self) -> usize { VECTOR_DIM }
}
