//! Shared test helpers for managing the Rust candle-encoder gRPC server.
//!
//! Integration tests that need a real vector encoder should call
//! `ensure_candle_encoder(port)` at the start.
//! The candle-encoder must be started **manually** before running tests.
//!
//! Usage:
//!   # Terminal 1 — start the encoder
//!   cargo run --release --manifest-path tools/candle-encoder/Cargo.toml
//!
//!   # Terminal 2 — run tests
//!   cargo test -- --ignored

use std::time::Duration;

#[cfg(feature = "grpc-encoder")]
pub mod vector_model {
    tonic::include_proto!("vector_model");
}

/// Poll the candle-encoder gRPC health check until it reports healthy.
#[cfg(feature = "grpc-encoder")]
pub fn wait_for_meowvec_ready(port: u16) -> Result<(), String> {
    use tonic::transport::Channel;
    use vector_model::vector_model_service_client::VectorModelServiceClient;
    use vector_model::HealthCheckRequest;

    let addr = format!("http://127.0.0.1:{}", port);
    let rt = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()
        .map_err(|e| format!("failed to create tokio runtime: {}", e))?;

    for _ in 0..120 {
        let result: Result<bool, Box<dyn std::error::Error>> = rt.block_on(async {
            let channel = Channel::from_shared(addr.clone())
                .map_err(|e| format!("invalid address: {}", e))?
                .connect()
                .await
                .map_err(|e| format!("connection failed: {}", e))?;
            let mut client = VectorModelServiceClient::new(channel);
            let response = client
                .health_check(HealthCheckRequest {})
                .await
                .map_err(|e| format!("health check failed: {}", e))?;
            Ok(response.into_inner().healthy)
        });
        match result {
            Ok(true) => return Ok(()),
            _ => std::thread::sleep(Duration::from_millis(500)),
        }
    }
    Err(format!(
        "candle encoder on port {} did not become ready within 60s",
        port
    ))
}

#[cfg(not(feature = "grpc-encoder"))]
pub fn wait_for_meowvec_ready(_port: u16) -> Result<(), String> {
    Err("grpc-encoder feature is disabled; cannot wait for candle encoder".to_string())
}

/// Ensure the candle-encoder gRPC server is ready on the given port.
///
/// The candle-encoder must be started **manually** before running tests:
/// ```ignore
/// cargo run --release --manifest-path tools/candle-encoder/Cargo.toml
/// ```
///
/// This function panics if the server is not ready within 60 seconds.
#[cfg(feature = "grpc-encoder")]
pub fn ensure_candle_encoder(port: u16) {
    wait_for_meowvec_ready(port).expect("candle encoder failed to become ready");
}

#[cfg(not(feature = "grpc-encoder"))]
pub fn ensure_candle_encoder(_port: u16) {
    panic!("grpc-encoder feature is disabled; cannot ensure candle encoder");
}
