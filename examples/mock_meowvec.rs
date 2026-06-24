// Mock meowvec gRPC server for benchmarking and integration testing
//
// Usage:
//   cargo run --example mock_meowvec                                  # default TCP: 127.0.0.1:27110
//   cargo run --example mock_meowvec -- --addr 127.0.0.1:28080        # custom TCP address
//
// Returns deterministic 384-dim vectors based on text content hash.
// Does NOT load the real BERT model (use real meowvec for production).

use tonic::{transport::Server, Request, Response, Status};

pub mod vector_model {
    tonic::include_proto!("vector_model");
}

use vector_model::vector_model_service_server::{VectorModelService, VectorModelServiceServer};
use vector_model::{
    BatchEncodeRequest, BatchEncodeResponse, Embedding, EncodeRequest, EncodeResponse,
    HealthCheckRequest, HealthCheckResponse,
};

const DIM: usize = 384; // multilingual-e5-small dimension
const DEFAULT_ADDR: &str = "127.0.0.1:27110";

/// Deterministic pseudo-encoder: FNV hash -> 384-dim normalized vector
fn fake_encode(text: &str) -> Vec<f32> {
    let mut hash: u64 = 0xcbf29ce484222325;
    for byte in text.bytes() {
        hash ^= byte as u64;
        hash = hash.wrapping_mul(0x100000001b3);
    }

    let mut vec = Vec::with_capacity(DIM);
    let mut state = hash;
    for i in 0..DIM {
        state = state.wrapping_mul(6364136223846793005).wrapping_add(1);
        let val = ((state >> 33) as f32) / (u32::MAX as f32) - 0.5;
        vec.push(val * (1.0 + i as f32 * 0.001));
    }

    // L2 normalize
    let norm: f32 = vec.iter().map(|x| x * x).sum::<f32>().sqrt();
    if norm > 0.0 {
        for v in &mut vec {
            *v /= norm;
        }
    }
    vec
}

struct MockVectorModelService;

#[tonic::async_trait]
impl VectorModelService for MockVectorModelService {
    async fn encode(
        &self,
        request: Request<EncodeRequest>,
    ) -> Result<Response<EncodeResponse>, Status> {
        let text = request.into_inner().text;
        let embedding = fake_encode(&text);
        Ok(Response::new(EncodeResponse {
            embedding,
            dimension: DIM as i32,
        }))
    }

    async fn batch_encode(
        &self,
        request: Request<BatchEncodeRequest>,
    ) -> Result<Response<BatchEncodeResponse>, Status> {
        let texts = request.into_inner().texts;
        let embeddings = texts
            .iter()
            .map(|t| Embedding {
                values: fake_encode(t),
            })
            .collect();
        Ok(Response::new(BatchEncodeResponse { embeddings }))
    }

    async fn health_check(
        &self,
        _request: Request<HealthCheckRequest>,
    ) -> Result<Response<HealthCheckResponse>, Status> {
        Ok(Response::new(HealthCheckResponse {
            healthy: true,
            socket: String::new(),
            model_name: "mock-multilingual-e5-small".to_string(),
            dimension: DIM as i32,
        }))
    }
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let args: Vec<String> = std::env::args().collect();

    let addr = args
        .iter()
        .position(|a| a == "--addr")
        .and_then(|i| args.get(i + 1))
        .map(|s| s.as_str())
        .unwrap_or(DEFAULT_ADDR);

    let socket: std::net::SocketAddr = addr.parse()?;

    eprintln!("mock_meowvec listening on http://{}", addr);
    eprintln!(
        "  model: mock-multilingual-e5-small ({}d, deterministic hash)",
        DIM
    );
    eprintln!("  press Ctrl+C to stop");

    Server::builder()
        .add_service(VectorModelServiceServer::new(MockVectorModelService))
        .serve(socket)
        .await?;

    Ok(())
}
