#![cfg(feature = "candle")]

use std::collections::HashMap;
use std::path::Path;

use candle_core::{DType, Device, Tensor, D};
use candle_nn::VarBuilder;
use candle_transformers::models::bert::{BertModel, Config};
use half::f16;
use tokenizers::Tokenizer;

use crate::encoder::{Encoder, EncoderOutput};
use crate::engram::VECTOR_DIM;

pub struct CandleEncoder {
    model: BertModel,
    tokenizer: Tokenizer,
    device: Device,
    hidden_size: usize,
    max_position_embeddings: usize,
}

impl CandleEncoder {
    pub fn from_path(model_dir: &str) -> Result<Self, Box<dyn std::error::Error>> {
        let dir = Path::new(model_dir);
        let config_path = dir.join("config.json");
        let model_path = dir.join("model.safetensors");

        if !config_path.exists() {
            return Err(format!("config.json not found in {model_dir}").into());
        }
        if !model_path.exists() {
            return Err(format!("model.safetensors not found in {model_dir}").into());
        }

        // Load BERT config
        let config: Config = {
            let f = std::fs::File::open(&config_path)
                .map_err(|e| format!("open config: {e}"))?;
            serde_json::from_reader(f)
                .map_err(|e| format!("parse config: {e}"))?
        };
        let hidden_size = config.hidden_size;
        // v0.11.0: Detect max_position_embeddings for input truncation
        let max_position_embeddings = config.max_position_embeddings;

        // Load tokenizer
        let tokenizer_path = dir.join("tokenizer.json");
        let tokenizer = if tokenizer_path.exists() {
            Tokenizer::from_file(&tokenizer_path)
                .map_err(|e| format!("tokenizer: {e}"))?
        } else {
            return Err("tokenizer.json not found".into());
        };

        // Load safetensors model
        let device = Device::Cpu;
        let vb = unsafe {
            VarBuilder::from_mmaped_safetensors(
                &[model_path.to_str().unwrap()],
                DType::F32,
                &device,
            )
        }
        .map_err(|e| format!("load safetensors: {e}"))?;
        let model = BertModel::load(vb, &config)
            .map_err(|e| format!("load BERT: {e}"))?;

        eprintln!(
            "memhop-candle: loaded BERT from '{}', hidden_size={hidden_size}",
            model_dir
        );

        Ok(CandleEncoder {
            model,
            tokenizer,
            device,
            hidden_size,
            max_position_embeddings,
        })
    }

    fn mean_pool(
        &self,
        last_hidden: &Tensor,
        attention_mask: &Tensor,
    ) -> Result<Vec<f32>, candle_core::Error> {
        // last_hidden: [1, seq_len, hidden_size], attention_mask: [1, seq_len]
        let mask = attention_mask.to_dtype(DType::F32)?; // [1, seq_len]
        let mask = mask.unsqueeze(D::Minus1)?;            // [1, seq_len, 1]
        let masked = (last_hidden * &mask)?;               // auto-broadcast
        let summed: Tensor = masked.sum(D::Minus2)?;       // [1, hidden_size]
        let counts: Tensor = mask.sum(D::Minus2)?;         // [1, 1]
        let counts = counts.clamp(1e-9f32, f32::MAX)?;    // avoid div by 0
        let mean = (&summed / &counts)?;                   // auto-broadcast
        let flat = mean.squeeze(0)?;                       // [hidden_size]
        flat.to_vec1::<f32>()
    }
}

impl Encoder for CandleEncoder {
    fn encode(&self, text: &str) -> EncoderOutput {
        let encoded = self
            .tokenizer
            .encode(text, false)
            .expect("tokenizer encode");

        let mut token_ids: Vec<u32> = encoded.get_ids().to_vec();
        let mut attention_mask: Vec<u32> = encoded.get_attention_mask().to_vec();

        // Truncate to max_position_embeddings to avoid OOB in position embedding
        if token_ids.len() > self.max_position_embeddings {
            token_ids.truncate(self.max_position_embeddings);
            attention_mask.truncate(self.max_position_embeddings);
        }
        let seq_len = token_ids.len();

        let input_ids = Tensor::new(&token_ids[..], &self.device)
            .expect("input_ids tensor")
            .unsqueeze(0)
            .expect("batch dim");

        let mask = Tensor::new(&attention_mask[..], &self.device)
            .expect("mask tensor")
            .unsqueeze(0)
            .expect("batch dim");

        // BGE models don't use token_type_ids — pass all-zero tensor
        let token_type_ids = Tensor::zeros((1, seq_len), DType::U32, &self.device)
            .expect("token_type_ids");

        let output = self
            .model
            .forward(&input_ids, &token_type_ids, Some(&mask))
            .expect("BERT forward");

        // Mean pooling or CLS fallback
        let dense_vec: Vec<f32> = match self.mean_pool(&output, &mask) {
            Ok(v) => v,
            Err(_) => {
                // Fallback: first token (CLS)
                output.get(0).ok().and_then(|t| t.to_vec1::<f32>().ok()).unwrap_or_default()
            }
        };

        let dense = vec_to_f16(&dense_vec, self.hidden_size);

        EncoderOutput {
            dense,
            sparse: HashMap::new(),
        }
    }

    fn dim(&self) -> usize {
        VECTOR_DIM
    }
}

fn vec_to_f16(v: &[f32], hidden_size: usize) -> Vec<f16> {
    let norm: f32 = v.iter().take(hidden_size).map(|x| x * x).sum::<f32>().sqrt();
    let scale = if norm > 1e-8 { 1.0 / norm } else { 1.0 };
    v.iter()
        .take(hidden_size)
        .map(|&x| f16::from_f32(x * scale))
        .chain(std::iter::repeat(f16::ZERO))
        .take(VECTOR_DIM)
        .collect()
}
