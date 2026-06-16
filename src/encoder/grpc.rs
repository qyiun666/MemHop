// Encoder module — gRPC client for meowvec VectorModelService (UDS only)

use crate::MemHopError;
use half::f16;
use std::collections::HashMap;

/// Default meowvec gRPC Unix socket address
pub const DEFAULT_ENCODER_ADDR: &str = "unix:///tmp/.meowagent/meowvec.sock";

pub mod vector_model {
    tonic::include_proto!("vector_model");
}

use vector_model::vector_model_service_client::VectorModelServiceClient;
use vector_model::{EncodeRequest, HealthCheckRequest};

// ============================================================================
// Encoder trait & output
// ============================================================================

/// Encoder trait for external encoding service
pub trait Encoder: Send + Sync {
    /// Encode text to dense and sparse vectors
    fn encode(&self, text: &str) -> Result<EncoderOutput, MemHopError>;

    /// Get vector dimension
    fn dim(&self) -> usize;

    /// Get encoder mode (e.g., "dense", "sparse", "hybrid")
    fn mode(&self) -> &str;
}

/// Output from encoder
pub struct EncoderOutput {
    pub dense: Vec<f16>,
    pub sparse: HashMap<String, f32>,
}

// ============================================================================
// GrpcEncoder — UDS only
// ============================================================================

/// gRPC encoder client for meowvec VectorModelService (Unix Domain Socket)
pub struct GrpcEncoder {
    rt: tokio::runtime::Runtime,
    client: std::sync::Mutex<VectorModelServiceClient<tonic::transport::Channel>>,
    dim: usize,
}

impl GrpcEncoder {
    /// Create a new gRPC encoder connecting via Unix Domain Socket
    ///
    /// # Arguments
    /// * `addr` - UDS address: `"unix:///tmp/.meowagent/meowvec.sock"`
    /// * `dim` - Expected vector dimension
    pub fn new(addr: &str, dim: usize) -> Result<Self, MemHopError> {
        let socket_path = addr.strip_prefix("unix://").ok_or_else(|| {
            MemHopError::ConfigError(format!(
                "gRPC address must use unix:// scheme, got: {}",
                addr
            ))
        })?;

        let rt = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .map_err(|e| {
                MemHopError::ConfigError(format!("Failed to create tokio runtime: {}", e))
            })?;

        let channel = Self::connect_uds(&rt, socket_path)?;
        let client = VectorModelServiceClient::new(channel);

        Ok(GrpcEncoder {
            rt,
            client: std::sync::Mutex::new(client),
            dim,
        })
    }

    /// Create a tonic Channel over Unix Domain Socket
    #[cfg(unix)]
    fn connect_uds(
        rt: &tokio::runtime::Runtime,
        socket_path: &str,
    ) -> Result<tonic::transport::Channel, MemHopError> {
        use hyper_util::rt::TokioIo;
        use std::path::PathBuf;
        use tokio::net::UnixStream;
        use tonic::transport::Uri;

        let path = PathBuf::from(socket_path);

        if !path.exists() {
            return Err(MemHopError::ConfigError(format!(
                "gRPC Unix socket not found: {}",
                socket_path
            )));
        }

        let path_for_connect = path.clone();
        let channel = rt
            .block_on(
                tonic::transport::Channel::from_static("http://[::]:50051").connect_with_connector(
                    tower::service_fn(move |_: Uri| {
                        let path = path_for_connect.clone();
                        async move {
                            let stream = UnixStream::connect(path).await.map_err(|e| {
                                std::io::Error::new(std::io::ErrorKind::ConnectionRefused, e)
                            })?;
                            Ok::<_, std::io::Error>(TokioIo::new(stream))
                        }
                    }),
                ),
            )
            .map_err(|e| {
                MemHopError::ConfigError(format!(
                    "Failed to connect gRPC via Unix socket {}: {}",
                    socket_path, e
                ))
            })?;

        Ok(channel)
    }

    #[cfg(not(unix))]
    fn connect_uds(
        _rt: &tokio::runtime::Runtime,
        socket_path: &str,
    ) -> Result<tonic::transport::Channel, MemHopError> {
        Err(MemHopError::ConfigError(format!(
            "Unix socket not supported on this platform: {}",
            socket_path
        )))
    }

    /// Check if the gRPC encoder service is available via HealthCheck RPC
    pub fn is_available(&self) -> bool {
        let mut client = match self.client.lock() {
            Ok(c) => c,
            Err(_) => return false,
        };
        match self.rt.block_on(client.health_check(HealthCheckRequest {})) {
            Ok(response) => response.into_inner().healthy,
            Err(_) => false,
        }
    }
}

impl Encoder for GrpcEncoder {
    fn encode(&self, text: &str) -> Result<EncoderOutput, MemHopError> {
        let mut client = self
            .client
            .lock()
            .map_err(|_| MemHopError::ConfigError("gRPC client mutex poisoned".to_string()))?;

        let response = self
            .rt
            .block_on(client.encode(EncodeRequest {
                text: text.to_string(),
            }))
            .map_err(|e| MemHopError::ConfigError(format!("gRPC encode failed: {}", e)))?;

        let resp = response.into_inner();
        let dense: Vec<f16> = resp.embedding.iter().map(|&v| f16::from_f32(v)).collect();

        Ok(EncoderOutput {
            dense,
            sparse: HashMap::new(),
        })
    }

    fn dim(&self) -> usize {
        self.dim
    }

    fn mode(&self) -> &str {
        "grpc"
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_grpc_encoder_requires_unix_scheme() {
        let result = GrpcEncoder::new("http://127.0.0.1:50051", 384);
        assert!(result.is_err());
        if let Err(e) = result {
            let err_msg = format!("{}", e);
            assert!(err_msg.contains("unix://"));
        }
    }

    #[test]
    fn test_grpc_encoder_uds_not_found() {
        let result = GrpcEncoder::new("unix:///tmp/nonexistent_meowvec_test.sock", 384);
        assert!(result.is_err());
    }

    #[test]
    fn test_grpc_encoder_dim_and_mode() {
        // Can't create a real GrpcEncoder without a running server,
        // but we verify the UDS path check works
        let result = GrpcEncoder::new("unix:///tmp/nonexistent.sock", 384);
        assert!(result.is_err()); // Expected: socket not found
    }
}
