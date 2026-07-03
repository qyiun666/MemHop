// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// gRPC client for meowvec VectorModelService (TCP).

use crate::MemHopError;
use half::f16;
use std::collections::HashMap;
use std::time::Duration;

pub const DEFAULT_ENCODER_ADDR: &str = "http://127.0.0.1:27110";

pub mod vector_model {
    tonic::include_proto!("vector_model");
}

use vector_model::vector_model_service_client::VectorModelServiceClient;
use vector_model::{EncodeRequest, HealthCheckRequest};

/// Health-check timeout shared between eager check and availability probe.
const HEALTH_CHECK_TIMEOUT: Duration = Duration::from_secs(5);

// ============================================================================
// Encoder trait & output
// ============================================================================

pub trait Encoder: Send + Sync {
    fn encode(&self, text: &str) -> Result<EncoderOutput, MemHopError>;
    fn dim(&self) -> usize;
    fn mode(&self) -> &str;
}

pub struct EncoderOutput {
    pub dense: Vec<f16>,
    pub sparse: HashMap<String, f32>,
}

// ============================================================================
// GrpcEncoder — TCP only
// ============================================================================

/// TCP-only gRPC client; performs eager health check on construction.
pub struct GrpcEncoder {
    rt: tokio::runtime::Runtime,
    client: std::sync::Mutex<VectorModelServiceClient<tonic::transport::Channel>>,
    dim: usize,
}

impl GrpcEncoder {
    /// Connects via TCP and performs an eager health check so connection
    /// failures surface immediately instead of being silently degraded.
    pub fn new(addr: &str, dim: usize) -> Result<Self, MemHopError> {
        let rt = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .map_err(|e| {
                MemHopError::ConfigError(format!("Failed to create tokio runtime: {}", e))
            })?;

        let channel = Self::connect_tcp(&rt, addr)?;
        let mut client = VectorModelServiceClient::new(channel);

        // Eager health check: fail fast if unreachable/unhealthy.
        let health = rt
            .block_on(async {
                tokio::time::timeout(
                    HEALTH_CHECK_TIMEOUT,
                    client.health_check(HealthCheckRequest {}),
                )
                .await
            })
            .map_err(|_| {
                MemHopError::EncoderError(format!(
                    "gRPC encoder health check timed out after {:?} at {}",
                    HEALTH_CHECK_TIMEOUT, addr
                ))
            })?
            .map_err(|e| {
                MemHopError::EncoderError(format!(
                    "gRPC encoder health check failed at {}: {}",
                    addr, e
                ))
            })?
            .into_inner();

        if !health.healthy {
            return Err(MemHopError::EncoderError(format!(
                "gRPC encoder reports unhealthy at {}",
                addr
            )));
        }

        Ok(GrpcEncoder {
            rt,
            client: std::sync::Mutex::new(client),
            dim,
        })
    }

    fn connect_tcp(
        rt: &tokio::runtime::Runtime,
        addr: &str,
    ) -> Result<tonic::transport::Channel, MemHopError> {
        if !addr.starts_with("http://") && !addr.starts_with("https://") {
            return Err(MemHopError::ConfigError(format!(
                "gRPC encoder address must use http:// or https:// scheme, got: {}",
                addr
            )));
        }

        let endpoint = tonic::transport::Channel::from_shared(addr.to_string())
            .map_err(|e| {
                MemHopError::ConfigError(format!("Invalid gRPC encoder address '{}': {}", addr, e))
            })?
            .connect_timeout(Duration::from_secs(5));

        let channel = rt.block_on(endpoint.connect()).map_err(|e| {
            MemHopError::EncoderError(format!("Failed to connect gRPC encoder at {}: {}", addr, e))
        })?;

        Ok(channel)
    }

    pub fn is_available(&self) -> bool {
        let mut client = match self.client.lock() {
            Ok(c) => c,
            Err(_) => return false,
        };
        match self.rt.block_on(async {
            tokio::time::timeout(
                HEALTH_CHECK_TIMEOUT,
                client.health_check(HealthCheckRequest {}),
            )
            .await
        }) {
            Ok(Ok(response)) => response.into_inner().healthy,
            _ => false,
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
            .block_on(async {
                tokio::time::timeout(
                    Duration::from_secs(10),
                    client.encode(EncodeRequest {
                        text: text.to_string(),
                    }),
                )
                .await
            })
            .map_err(|_| MemHopError::EncoderError("encode timeout after 10s".into()))?
            .map_err(|e| MemHopError::EncoderError(format!("gRPC encode failed: {}", e)))?;

        let resp = response.into_inner();
        let dense: Vec<f16> = resp.embedding.iter().map(|&v| f16::from_f32(v)).collect();
        let sparse: HashMap<String, f32> = resp.sparse.into_iter().collect();

        Ok(EncoderOutput { dense, sparse })
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
    fn test_default_encoder_addr_is_tcp() {
        assert_eq!(DEFAULT_ENCODER_ADDR, "http://127.0.0.1:27110");
    }

    #[test]
    fn test_grpc_encoder_rejects_unix_scheme() {
        let result = GrpcEncoder::new("unix:///tmp/.meowagent/meowvec.sock", 384);
        assert!(result.is_err());
        if let Err(e) = result {
            let err_msg = format!("{}", e);
            assert!(
                err_msg.contains("http://") || err_msg.contains("https://"),
                "expected scheme error, got: {}",
                err_msg
            );
        }
    }

    #[test]
    fn test_grpc_encoder_rejects_bare_tcp_address() {
        // Bare host:port rejected; tonic requires explicit scheme.
        let result = GrpcEncoder::new("127.0.0.1:27110", 384);
        assert!(result.is_err());
        if let Err(e) = result {
            let err_msg = format!("{}", e);
            assert!(
                err_msg.contains("http://") || err_msg.contains("https://"),
                "expected scheme error, got: {}",
                err_msg
            );
        }
    }

    #[test]
    fn test_grpc_encoder_tcp_unavailable() {
        let result = GrpcEncoder::new("http://127.0.0.1:1", 384);
        assert!(result.is_err(), "expected connection failure");
    }

    #[test]
    fn test_grpc_encoder_rejects_invalid_scheme() {
        let result = GrpcEncoder::new("ftp://127.0.0.1:27110", 384);
        assert!(result.is_err());
    }
}
