#[cfg(feature = "candle")]
use candle_core::{Device, Tensor};
#[cfg(feature = "candle")]
use candle_nn::VarBuilder;
#[cfg(feature = "candle")]
use candle_transformers::models::bert::{BertModel, Config as BertConfig};
#[cfg(feature = "candle")]
use tokenizers::Tokenizer;
#[cfg(feature = "candle")]
use std::path::Path;

use super::{Encoder, EncoderOutput};
use half::f16;
use std::collections::HashMap;

/// CandleEncoder - 基于 candle 的语义向量编码器
/// 
/// 使用 multilingual-e5-small 模型 (384维)
/// 仅在 `feature = "candle"` 下可用
#[cfg(feature = "candle")]
pub struct CandleEncoder {
    model: BertModel,
    tokenizer: Tokenizer,
    device: Device,
}

#[cfg(feature = "candle")]
impl CandleEncoder {
    /// 从本地模型目录加载 CandleEncoder
    /// 
    /// # Arguments
    /// * `model_path` - 模型目录路径，需包含 model.safetensors, config.json, tokenizer.json
    /// 
    /// # Returns
    /// * `Result<CandleEncoder, Box<dyn std::error::Error>>` - 成功返回编码器，失败返回错误
    pub fn new(model_path: &str) -> std::result::Result<Self, Box<dyn std::error::Error>> {
        let path = Path::new(model_path);
        
        // 加载配置
        let config_path = path.join("config.json");
        let config_str = std::fs::read_to_string(&config_path)?;
        let config: BertConfig = serde_json::from_str(&config_str)?;
        
        // 加载 tokenizer
        let tokenizer_path = path.join("tokenizer.json");
        let tokenizer = Tokenizer::from_file(&tokenizer_path)
            .map_err(|e| format!("Failed to load tokenizer: {}", e))?;
        
        // 加载模型权重
        let model_file = path.join("model.safetensors");
        let device = Device::Cpu;
        let vb = unsafe {
            VarBuilder::from_mmaped_safetensors(&[model_file], candle_core::DType::F32, &device)?
        };
        
        // 构建 BertModel
        let model = BertModel::load(vb, &config)?;
        
        Ok(Self {
            model,
            tokenizer,
            device,
        })
    }
    
    /// 从 HuggingFace 缓存加载模型
    /// 
    /// # Arguments
    /// * `model_name` - 模型名称（如 "intfloat/multilingual-e5-small"）
    pub fn from_pretrained(model_name: &str) -> std::result::Result<Self, Box<dyn std::error::Error>> {
        let cache_dir = dirs::cache_dir()
            .ok_or("Cannot find cache directory")?
            .join("huggingface")
            .join("hub")
            .join(format!("models--{}", model_name.replace("/", "--")));
        
        // 找到最新的 snapshot
        let snapshots_dir = cache_dir.join("snapshots");
        let mut snapshots: Vec<_> = std::fs::read_dir(&snapshots_dir)?
            .filter_map(|e| e.ok())
            .filter(|e| e.path().is_dir())
            .collect();
        snapshots.sort_by_key(|e| e.metadata().and_then(|m| m.modified()).ok());
        
        let snapshot_path = snapshots.last()
            .ok_or("No snapshots found")?
            .path();
        
        Self::new(snapshot_path.to_str().ok_or("Invalid path")?)
    }
    
    /// 内部编码方法
    fn encode_inner(&self, text: &str) -> std::result::Result<Vec<f32>, Box<dyn std::error::Error>> {
        // Tokenize
        let encoding = self.tokenizer.encode(text, true)
            .map_err(|e| format!("Tokenization failed: {}", e))?;
        
        let input_ids = encoding.get_ids();
        let attention_mask = encoding.get_attention_mask();
        
        // 转换为 Tensor
        let input_ids_tensor = Tensor::new(input_ids, &self.device)?
            .unsqueeze(0)?;  // [1, seq_len]
        let attention_mask_tensor = Tensor::new(attention_mask, &self.device)?
            .unsqueeze(0)?;  // [1, seq_len]
        
        // Forward pass
        let outputs = self.model.forward(&input_ids_tensor, &attention_mask_tensor, None)?;
        // outputs shape: [batch_size, seq_len, hidden_size] = [1, seq_len, 384]
        
        // Mean pooling (考虑 attention mask)
        let mask_expanded = attention_mask_tensor
            .unsqueeze(2)?  // [1, seq_len, 1]
            .to_dtype(candle_core::DType::F32)?;
        
        let masked_outputs = outputs.broadcast_mul(&mask_expanded)?;
        let sum = masked_outputs.sum(1)?;  // [1, hidden_size]
        let mask_sum = mask_expanded.sum(1)?.clamp(1e-9, f64::MAX)?;  // [1, 1]
        let mean_pooled = sum.broadcast_div(&mask_sum)?;  // [1, hidden_size]
        
        // L2 normalize
        let norm = mean_pooled.sqr()?.sum_all()?.sqrt()?;
        let normalized = mean_pooled.broadcast_div(&norm.unsqueeze(0)?)?;
        
        // 转换为 Vec<f32>
        let embeddings = normalized.squeeze(0)?.to_vec1::<f32>()?;
        
        Ok(embeddings)
    }
}

#[cfg(feature = "candle")]
impl Encoder for CandleEncoder {
    fn encode(&self, text: &str) -> EncoderOutput {
        match self.encode_inner(text) {
            Ok(embeddings_f32) => {
                // 转换为 f16
                let dense: Vec<f16> = embeddings_f32.iter().map(|&x| f16::from_f32(x)).collect();
                
                EncoderOutput {
                    dense,
                    sparse: HashMap::new(), // CandleEncoder 仅输出 dense，sparse 由 EncoderRouter 补充
                }
            }
            Err(e) => {
                eprintln!("CandleEncoder error: {}", e);
                EncoderOutput {
                    dense: vec![f16::from_f32(0.0); self.dim()],
                    sparse: HashMap::new(),
                }
            }
        }
    }
    
    fn dim(&self) -> usize {
        384 // multilingual-e5-small 输出维度
    }
    
    fn mode(&self) -> &str {
        "candle"
    }
}

#[cfg(all(test, feature = "candle"))]
mod tests {
    use super::*;
    
    #[test]
    fn test_candle_encoder_load() {
        let model_path = "/Volumes/zt_hd/projects/meow/memhop/models/multilingual-e5-small";
        
        // 仅在模型文件存在时测试
        if Path::new(model_path).join("model.safetensors").exists() {
            let encoder = CandleEncoder::new(model_path).expect("Failed to load model");
            assert_eq!(encoder.dim(), 384);
            assert_eq!(encoder.mode(), "candle");
        }
    }
    
    #[test]
    fn test_candle_encoder_encode() {
        let model_path = "/Volumes/zt_hd/projects/meow/memhop/models/multilingual-e5-small";
        
        if Path::new(model_path).join("model.safetensors").exists() {
            let encoder = CandleEncoder::new(model_path).expect("Failed to load model");
            let output = encoder.encode("Hello, world!");
            
            assert_eq!(output.dense.len(), 384);
            assert!(output.sparse.is_empty());
            
            // 验证 L2 normalize（向量长度应接近 1.0）
            let norm: f32 = output.dense.iter().map(|x| x.to_f32().powi(2)).sum::<f32>().sqrt();
            assert!((norm - 1.0).abs() < 0.01, "L2 norm should be close to 1.0, got {}", norm);
        }
    }
}
