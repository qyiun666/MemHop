//! Candle-based gRPC encoder server for MemHop.
//!
//! Replaces the Python meowvec service with a pure Rust implementation
//! using `candle-transformers` + `granite-embedding-278m-multilingual`.
//!
//! Usage:
//!   cargo run --bin candle-encoder --release
//!   cargo run --bin candle-encoder --release -- --model-path models/granite-embedding-278m-multilingual --addr 127.0.0.1:27110

use std::collections::HashMap;
use std::path::PathBuf;
use std::sync::Arc;

use anyhow::{Context, Result};
use candle_core::Device;
use candle_nn::VarBuilder;
use candle_transformers::models::bert::{BertModel, Config as BertConfig};
use clap::Parser;
use tokio::sync::Mutex;
use tonic::{transport::Server, Request, Response, Status};

// Generated gRPC code from proto/vector_model.proto
pub mod vector_model {
    tonic::include_proto!("vector_model");
}

use vector_model::vector_model_service_server::{VectorModelService, VectorModelServiceServer};
use vector_model::{
    BatchEncodeRequest, BatchEncodeResponse, Embedding, EncodeRequest, EncodeResponse,
    HealthCheckRequest, HealthCheckResponse, RerankRequest, RerankResponse,
};

// ============================================================================
// CLI arguments
// ============================================================================

#[derive(Parser, Debug)]
#[command(name = "candle-encoder", about = "Rust gRPC encoder server for MemHop")]
struct Args {
    /// Path to the model directory containing config.json, model.safetensors, tokenizer.json
    #[arg(long, default_value = "models/granite-embedding-278m-multilingual")]
    model_path: String,

    /// Listen address for the gRPC server
    #[arg(long, default_value = "127.0.0.1:27110")]
    addr: String,

    /// Use CPU instead of auto-detecting Metal/GPU
    #[arg(long)]
    cpu: bool,
}

// ============================================================================
// Candle model wrapper
// ============================================================================

struct EncoderModel {
    model: BertModel,
    tokenizer: tokenizers::Tokenizer,
    device: Device,
    hidden_size: usize,
}

impl EncoderModel {
    fn load(model_path: &str, device: Device) -> Result<Self> {
        let path = PathBuf::from(model_path);
        let config_path = path.join("config.json");
        let weights_path = path.join("model.safetensors");
        let tokenizer_path = path.join("tokenizer.json");

        // Validate all files exist
        for f in [&config_path, &weights_path, &tokenizer_path] {
            if !f.exists() {
                anyhow::bail!("Required model file not found: {:?}", f);
            }
        }

        // Load BERT config from config.json via serde (candle's Config derives Deserialize)
        tracing::info!("Loading config from {:?}", config_path);
        let config_file = std::fs::File::open(&config_path)
            .with_context(|| format!("Failed to open {:?}", config_path))?;
        let config: BertConfig = serde_json::from_reader(config_file)
            .with_context(|| format!("Failed to parse BERT config from {:?}", config_path))?;
        let hidden_size = config.hidden_size;
        tracing::info!(
            "Model config: hidden_size={}, layers={}, heads={}, model_type={:?}",
            hidden_size,
            config.num_hidden_layers,
            config.num_attention_heads,
            config.model_type
        );

        // Load safetensors weights
        tracing::info!(
            "Loading weights from {:?} (this may take a few seconds)...",
            weights_path
        );
        let tensors = candle_core::safetensors::load(&weights_path, &device)
            .with_context(|| format!("Failed to load safetensors from {:?}", weights_path))?;
        let vb = VarBuilder::from_tensors(tensors, candle_core::DType::F32, &device);
        let model = BertModel::load(vb, &config)
            .context("Failed to load BertModel from weights")?;

        // Load tokenizer
        tracing::info!("Loading tokenizer from {:?}", tokenizer_path);
        let tokenizer = tokenizers::Tokenizer::from_file(
            tokenizer_path.to_string_lossy().as_ref(),
        )
        .map_err(|e| anyhow::anyhow!("Failed to load tokenizer from {:?}: {}", tokenizer_path, e))?;

        tracing::info!("Model loaded successfully on {:?} — hidden_size={}", device, hidden_size);

        Ok(Self {
            model,
            tokenizer,
            device,
            hidden_size,
        })
    }

    /// Encode a single text into a dense vector.
    fn encode(&self, text: &str) -> Result<Vec<f32>> {
        // Tokenize
        let encoding = self
            .tokenizer
            .encode(text, true)
            .map_err(|e| anyhow::anyhow!("Tokenization failed: {}", e))?;
        let input_ids = encoding.get_ids();
        let attention_mask = encoding.get_attention_mask();

        if input_ids.is_empty() {
            anyhow::bail!("Empty input after tokenization");
        }

        let seq_len = input_ids.len();

        // Build tensors
        let input_ids_tensor =
            candle_core::Tensor::from_slice(input_ids, seq_len, &self.device)?.unsqueeze(0)?;
        let attention_mask_tensor =
            candle_core::Tensor::from_slice(attention_mask, seq_len, &self.device)?
                .unsqueeze(0)?;

        // XLM-RoBERTa: create an all-zero token_type_ids tensor (type_vocab_size=1)
        let token_type_ids = input_ids_tensor.zeros_like()?;

        // Forward pass through BertModel
        let hidden = self.model.forward(
            &input_ids_tensor,
            &token_type_ids,
            Some(&attention_mask_tensor),
        )?;
        // hidden: (1, seq_len, hidden_size)

        // Mean pooling: average over non-padding tokens
        let mask_f32 = attention_mask_tensor.to_dtype(candle_core::DType::F32)?;
        let mask_expanded = mask_f32.unsqueeze(1)?; // (1, 1, seq_len)
        let masked_sum = (hidden * mask_expanded)?.sum(1)?; // (1, hidden_size)

        let valid_tokens: f32 = attention_mask.iter().map(|&m| m as f32).sum();
        if valid_tokens <= 0.0 {
            anyhow::bail!("No valid tokens in input");
        }
        let pooled = masked_sum.broadcast_div(&candle_core::Tensor::new(
            &[valid_tokens],
            &self.device,
        )?)?;

        // L2 normalize
        let norm = pooled.sqr()?.sum(1)?.sqrt()?;
        let normalized = pooled.broadcast_div(&norm)?;

        // Extract as Vec<f32>
        let result: Vec<f32> = normalized.squeeze(0)?.to_vec1()?;
        debug_assert_eq!(result.len(), self.hidden_size);
        Ok(result)
    }
}

// ============================================================================
// gRPC service implementation
// ============================================================================

struct CandleEncoderService {
    model: Arc<Mutex<EncoderModel>>,
    addr: String,
}

#[tonic::async_trait]
impl VectorModelService for CandleEncoderService {
    async fn health_check(
        &self,
        _request: Request<HealthCheckRequest>,
    ) -> Result<Response<HealthCheckResponse>, Status> {
        Ok(Response::new(HealthCheckResponse {
            healthy: true,
            socket: self.addr.clone(),
            model_name: "granite-embedding-278m-multilingual (candle)".to_string(),
            dimension: self.model.try_lock().map(|m| m.hidden_size as i32).unwrap_or(768),
        }))
    }

    async fn encode(
        &self,
        request: Request<EncodeRequest>,
    ) -> Result<Response<EncodeResponse>, Status> {
        let text = request.into_inner().text;
        let model = self.model.lock().await;
        let embedding = model.encode(&text).map_err(|e| {
            Status::internal(format!("Encode failed: {}", e))
        })?;

        Ok(Response::new(EncodeResponse {
            embedding,
            dimension: model.hidden_size as i32,
            sparse: HashMap::new(), // BM25 sparse handled by MemHop itself
        }))
    }

    async fn batch_encode(
        &self,
        request: Request<BatchEncodeRequest>,
    ) -> Result<Response<BatchEncodeResponse>, Status> {
        let texts = request.into_inner().texts;
        let model = self.model.lock().await;
        let mut embeddings = Vec::with_capacity(texts.len());

        for text in &texts {
            let values = model.encode(text).map_err(|e| {
                Status::internal(format!("Batch encode failed for '{}': {}", text, e))
            })?;
            embeddings.push(Embedding { values });
        }

        Ok(Response::new(BatchEncodeResponse { embeddings }))
    }

    async fn rerank(
        &self,
        request: Request<RerankRequest>,
    ) -> Result<Response<RerankResponse>, Status> {
        let req = request.into_inner();
        let query = req.query;
        let documents = req.documents;
        let model = self.model.lock().await;

        if documents.is_empty() {
            return Ok(Response::new(RerankResponse { scores: vec![] }));
        }

        // Encode query once
        let query_vec = model.encode(&query).map_err(|e| {
            Status::internal(format!("Rerank query encode failed: {}", e))
        })?;

        // Encode each document and compute cosine similarity
        let mut scores = Vec::with_capacity(documents.len());
        for doc in &documents {
            let doc_vec = match model.encode(doc) {
                Ok(v) => v,
                Err(e) => {
                    tracing::warn!("Rerank doc encode failed: {}", e);
                    scores.push(0.0_f32);
                    continue;
                }
            };

            let dot: f32 = query_vec.iter().zip(doc_vec.iter()).map(|(a, b)| a * b).sum();
            scores.push(dot.clamp(0.0, 1.0)); // L2 normalized → dot = cosine
        }

        Ok(Response::new(RerankResponse { scores }))
    }
}

// ============================================================================
// Entry point
// ============================================================================

#[tokio::main]
async fn main() -> Result<()> {
    tracing_subscriber::fmt()
        .with_env_filter(
            tracing_subscriber::EnvFilter::from_default_env()
                .add_directive("candle_encoder=info".parse().unwrap())
                .add_directive("tonic=warn".parse().unwrap()),
        )
        .init();

    let args = Args::parse();

    // Auto-detect device: prefer Metal on macOS, CUDA on Linux, fallback CPU
    let device = if args.cpu {
        Device::Cpu
    } else {
        Device::new_metal(0).unwrap_or_else(|e| {
            tracing::info!("Metal not available ({}), falling back to CPU", e);
            Device::Cpu
        })
    };
    tracing::info!("Using device: {:?}", device);

    // Load model (blocking, done before server starts)
    let model_path = args.model_path;
    tracing::info!("Loading encoder model from '{}'...", model_path);
    let model = EncoderModel::load(&model_path, device)
        .context("Failed to load encoder model")?;
    let model = Arc::new(Mutex::new(model));

    let addr_str = args.addr.clone();
    let addr = addr_str
        .parse::<std::net::SocketAddr>()
        .with_context(|| format!("Invalid address: {}", addr_str))?;

    tracing::info!("Starting gRPC encoder server on {}", addr);
    tracing::info!("MemHop clients should connect to grpc://{}", addr);

    let service = CandleEncoderService {
        model,
        addr: addr_str,
    };

    Server::builder()
        .add_service(VectorModelServiceServer::new(service))
        .serve(addr)
        .await
        .context("gRPC server failed")?;

    Ok(())
}
