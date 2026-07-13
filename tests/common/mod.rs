//! Shared test helpers for managing the Rust candle-encoder gRPC server.
//!
//! Integration tests that need a real vector encoder should call
//! `ensure_encoder_running()` at the start. The encoder will be
//! automatically started if not already running.
//!
//! Environment variable `MEMHOP_ENCODER_GRPC_ADDR` can override the
//! default address (e.g. `http://127.0.0.1:27110`).

use std::process::{Child, Command};
use std::sync::Mutex;
use std::time::Duration;

// ---------------------------------------------------------------------------
// Encoder lifecycle management (per-process singleton)
// ---------------------------------------------------------------------------

struct EncoderState {
    child: Option<Child>,
    port: u16,
}

impl Drop for EncoderState {
    fn drop(&mut self) {
        if let Some(mut child) = self.child.take() {
            let _ = child.kill();
            let _ = child.wait();
        }
    }
}

static ENCODER_STATE: Mutex<Option<EncoderState>> = Mutex::new(None);

/// Return the encoder port from the environment, or a default.
fn get_encoder_port() -> u16 {
    std::env::var("MEMHOP_ENCODER_GRPC_ADDR")
        .ok()
        .and_then(|addr| addr.split(':').last().and_then(|p| p.parse().ok()))
        .unwrap_or(27110)
}

/// Ensure the candle-encoder is running and ready.
///
/// - If `MEMHOP_ENCODER_GRPC_ADDR` is set, uses that address.
/// - If an encoder is already running on the port (health check succeeds),
///   uses it directly.
/// - Otherwise, automatically starts candle-encoder and waits for readiness.
///
/// Returns the port number.
pub fn ensure_encoder_running() -> u16 {
    let port = get_encoder_port();
    let mut state = ENCODER_STATE.lock().unwrap();

    if let Some(ref s) = *state {
        return s.port;
    }

    // Fast check: encoder already running (e.g. from another test binary)?
    if wait_for_meowvec_ready(port, 2).is_ok() {
        eprintln!("[memhop-test] Using existing encoder on port {}", port);
        *state = Some(EncoderState { child: None, port });
        return port;
    }

    // Start the encoder
    eprintln!("[memhop-test] Starting candle-encoder on port {} ...", port);
    let project_root = std::path::Path::new(env!("CARGO_MANIFEST_DIR"));

    // On macOS, set CXXFLAGS to ensure C++ headers are found
    let mut cmd = Command::new("cargo");
    cmd.args([
        "run",
        "--release",
        "--manifest-path",
        project_root
            .join("tools/candle-encoder/Cargo.toml")
            .to_str()
            .unwrap(),
        "--",
        "--model-path",
        project_root
            .join("models/granite-embedding-278m-multilingual")
            .to_str()
            .unwrap(),
        "--addr",
        &format!("127.0.0.1:{}", port),
    ])
    .stdout(std::process::Stdio::null())
    .stderr(std::process::Stdio::null());

    // On macOS, set CXXFLAGS to find C++ headers (cstdint etc.)
    if cfg!(target_os = "macos") {
        if std::env::var("CXXFLAGS").is_err() {
            if let Ok(sdk_path) = std::process::Command::new("xcrun")
                .arg("--show-sdk-path")
                .output()
                .map(|o| String::from_utf8_lossy(&o.stdout).trim().to_string())
            {
                if !sdk_path.is_empty() {
                    let cxxflags = format!("-I{0}/usr/include/c++/v1 -I{0}/usr/include", sdk_path);
                    cmd.env("CXXFLAGS", cxxflags);
                }
            }
        }
    }

    let mut child = cmd.spawn().expect("Failed to start candle-encoder");

    eprintln!("[memhop-test] Waiting for candle-encoder to become ready (up to 180s) ...");

    // 360 attempts times 500 ms = 180 seconds (first compile may take long)
    if let Err(e) = wait_for_meowvec_ready(port, 360) {
        let _ = child.kill();
        let _ = child.wait();
        panic!("candle-encoder failed to start within 180 seconds: {}", e);
    }

    eprintln!("[memhop-test] candle-encoder is ready on port {}", port);
    *state = Some(EncoderState {
        child: Some(child),
        port,
    });
    port
}

// ---------------------------------------------------------------------------
// gRPC health check helpers
// ---------------------------------------------------------------------------

#[cfg(feature = "grpc-encoder")]
pub mod vector_model {
    tonic::include_proto!("vector_model");
}

/// Poll the candle-encoder gRPC health check for up to `max_attempts`
/// attempts (500 ms each).
#[cfg(feature = "grpc-encoder")]
pub fn wait_for_meowvec_ready(port: u16, max_attempts: u64) -> Result<(), String> {
    use tonic::transport::Channel;
    use vector_model::vector_model_service_client::VectorModelServiceClient;
    use vector_model::HealthCheckRequest;

    let addr = format!("http://127.0.0.1:{}", port);
    let rt = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()
        .map_err(|e| format!("failed to create tokio runtime: {}", e))?;

    for attempt in 0..max_attempts {
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
            Ok(false) => {
                if attempt < 3 || (attempt + 1) % 30 == 0 {
                    eprintln!(
                        "[memhop-test] healthcheck attempt {}/{}: unhealthy, retrying...",
                        attempt + 1,
                        max_attempts
                    );
                }
                std::thread::sleep(Duration::from_millis(500));
            }
            Err(e) => {
                if attempt < 3 || (attempt + 1) % 30 == 0 {
                    eprintln!(
                        "[memhop-test] healthcheck attempt {}/{}: {}, retrying...",
                        attempt + 1,
                        max_attempts,
                        e
                    );
                }
                std::thread::sleep(Duration::from_millis(500));
            }
        }
    }
    Err(format!(
        "candle encoder on port {} did not become ready within {}ms",
        port,
        max_attempts * 500
    ))
}

#[cfg(not(feature = "grpc-encoder"))]
pub fn wait_for_meowvec_ready(_port: u16, _max_attempts: u64) -> Result<(), String> {
    Err("grpc-encoder feature is disabled; cannot wait for candle encoder".to_string())
}

/// Legacy helper — ensure the candle-encoder is ready on a given port.
/// The encoder must be started **manually**.
#[cfg(feature = "grpc-encoder")]
pub fn ensure_candle_encoder(port: u16) {
    wait_for_meowvec_ready(port, 120).expect("candle encoder failed to become ready");
}

#[cfg(not(feature = "grpc-encoder"))]
pub fn ensure_candle_encoder(_port: u16) {
    panic!("grpc-encoder feature is disabled; cannot ensure candle encoder");
}
