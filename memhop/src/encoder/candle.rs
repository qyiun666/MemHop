#![cfg(feature = "candle")]

use std::collections::HashMap;
use std::path::Path;

use candle_core::{DType, Device, Tensor, D};
use candle_nn::VarBuilder;
use candle_transformers::models::bert::{BertModel, Config as BertConfig};
use half::f16;
use tokenizers::Tokenizer;

use crate::encoder::{Encoder, EncoderOutput};
use crate::encoder::ngram::NgramEncoder;

/// Candle BERT 编码器 — feature-gated，纯 Rust ML 推理。
///
/// 加载本地 BERT safetensors 模型进行语义编码，同时产生 ngram sparse 输出
/// 供 BM25 索引使用。默认模型: BGE-small-zh-v1.5（92MB, 512维）。
pub struct CandleEncoder {
    model: BertModel,
    tokenizer: Tokenizer,
    device: Device,
    hidden_size: usize,
    max_position_embeddings: usize,
}

impl CandleEncoder {
    /// 从本地模型目录加载 BERT 模型。
    ///
    /// 目录需包含: config.json, model.safetensors, tokenizer.json
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
        let config: BertConfig = {
            let f = std::fs::File::open(&config_path)
                .map_err(|e| format!("open config: {e}"))?;
            serde_json::from_reader(f)
                .map_err(|e| format!("parse config: {e}"))?
        };
        let hidden_size = config.hidden_size;
        let max_position_embeddings = config.max_position_embeddings;

        // Load tokenizer
        let tokenizer_path = dir.join("tokenizer.json");
        let tokenizer = if tokenizer_path.exists() {
            Tokenizer::from_file(&tokenizer_path)
                .map_err(|e| format!("tokenizer: {e}"))?
        } else {
            return Err("tokenizer.json not found".into());
        };

        // Load safetensors model on CPU
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
            "memhop-candle: loaded BERT from '{}', hidden_size={}, max_pos={}",
            model_dir, hidden_size, max_position_embeddings
        );

        Ok(CandleEncoder {
            model,
            tokenizer,
            device,
            hidden_size,
            max_position_embeddings,
        })
    }

    /// Mean pooling over token embeddings (masked average).
    fn mean_pool(
        &self,
        last_hidden: &Tensor,
        attention_mask: &Tensor,
    ) -> Result<Vec<f32>, candle_core::Error> {
        let mask = attention_mask.to_dtype(DType::F32)?;
        let mask = mask.unsqueeze(D::Minus1)?;
        let masked = (last_hidden * &mask)?;
        let summed: Tensor = masked.sum(D::Minus2)?;
        let counts: Tensor = mask.sum(D::Minus2)?;
        let counts = counts.clamp(1e-9f32, f32::MAX)?;
        let mean = (&summed / &counts)?;
        let flat = mean.squeeze(0)?;
        flat.to_vec1::<f32>()
    }

    /// Convert f32 vector to L2-normalized f16 vector.
    fn normalize_to_f16(v: &[f32], dim: usize) -> Vec<f16> {
        let norm: f32 = v.iter().map(|x| x * x).sum::<f32>().sqrt();
        if norm > 1e-8 {
            let scale = 1.0 / norm;
            v.iter().map(|&x| f16::from_f32(x * scale)).collect()
        } else {
            vec![f16::ZERO; dim]
        }
    }
}

impl Encoder for CandleEncoder {
    fn encode(&self, text: &str) -> EncoderOutput {
        let trimmed = text.trim();
        if trimmed.is_empty() {
            return EncoderOutput {
                dense: vec![f16::ZERO; self.hidden_size],
                sparse: HashMap::new(),
            };
        }

        // 1. Produce ngram sparse output (for BM25)
        let sparse = NgramEncoder::extract_ngrams(trimmed);

        // 2. Tokenize
        let encoding = match self.tokenizer.encode(trimmed, false) {
            Ok(e) => e,
            Err(_) => {
                return EncoderOutput {
                    dense: vec![f16::ZERO; self.hidden_size],
                    sparse,
                };
            }
        };

        let mut token_ids: Vec<u32> = encoding.get_ids().to_vec();
        let mut attention_mask: Vec<u32> = encoding.get_attention_mask().to_vec();

        // Truncate to max_position_embeddings
        if token_ids.len() > self.max_position_embeddings {
            token_ids.truncate(self.max_position_embeddings);
            attention_mask.truncate(self.max_position_embeddings);
        }
        let seq_len = token_ids.len();

        // 3. BERT forward
        let input_ids = match Tensor::new(&token_ids[..], &self.device) {
            Ok(t) => match t.unsqueeze(0) {
                Ok(t) => t,
                Err(_) => return EncoderOutput { dense: vec![f16::ZERO; self.hidden_size], sparse },
            },
            Err(_) => return EncoderOutput { dense: vec![f16::ZERO; self.hidden_size], sparse },
        };

        let mask = match Tensor::new(&attention_mask[..], &self.device) {
            Ok(t) => match t.unsqueeze(0) {
                Ok(t) => t,
                Err(_) => return EncoderOutput { dense: vec![f16::ZERO; self.hidden_size], sparse },
            },
            Err(_) => return EncoderOutput { dense: vec![f16::ZERO; self.hidden_size], sparse },
        };

        let token_type_ids = match Tensor::zeros((1, seq_len), DType::U32, &self.device) {
            Ok(t) => t,
            Err(_) => return EncoderOutput { dense: vec![f16::ZERO; self.hidden_size], sparse },
        };

        let output = match self.model.forward(&input_ids, &token_type_ids, Some(&mask)) {
            Ok(o) => o,
            Err(_) => return EncoderOutput { dense: vec![f16::ZERO; self.hidden_size], sparse },
        };

        // 4. Mean pooling
        let dense_f32: Vec<f32> = match self.mean_pool(&output, &mask) {
            Ok(v) => v,
            Err(_) => {
                // CLS fallback: output[0, 0, :] — first batch, first token
                output.get(0)
                    .ok()
                    .and_then(|t| t.get(0).ok())
                    .and_then(|t| t.to_vec1::<f32>().ok())
                    .unwrap_or_else(|| vec![0.0f32; self.hidden_size])
            }
        };

        // 5. L2 normalize → f16
        let dense = Self::normalize_to_f16(&dense_f32, self.hidden_size);

        EncoderOutput { dense, sparse }
    }

    fn dim(&self) -> usize {
        self.hidden_size
    }

    fn mode(&self) -> &str {
        "candle"
    }
}
