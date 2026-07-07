//! Shared test helpers for managing the Python onnxruntime meowvec gRPC encoder process.
//!
//! Integration tests that need a real vector encoder can call
//! `ensure_python_meowvec(port)` at the start.
//! The first call spawns the Python server; subsequent calls reuse the same process.
//! The process is killed when the test binary exits.

use std::process::{Child, Command, Stdio};
use std::sync::OnceLock;
use std::time::Duration;

#[cfg(feature = "grpc-encoder")]
pub mod vector_model {
    tonic::include_proto!("vector_model");
}

/// Poll the Python meowvec gRPC health check until it reports healthy.
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
        "python meowvec on port {} did not become ready within 60s",
        port
    ))
}

#[cfg(not(feature = "grpc-encoder"))]
pub fn wait_for_meowvec_ready(_port: u16) -> Result<(), String> {
    Err("grpc-encoder feature is disabled; cannot wait for meowvec".to_string())
}

// ---------------------------------------------------------------------------
// Python onnxruntime meowvec server — cross-platform, GPU-capable
// ---------------------------------------------------------------------------

#[cfg(feature = "grpc-encoder")]
pub struct PythonMeowvecGuard(std::sync::Mutex<Child>);

#[cfg(feature = "grpc-encoder")]
impl Drop for PythonMeowvecGuard {
    fn drop(&mut self) {
        if let Ok(mut child) = self.0.lock() {
            let _ = child.kill();
            let _ = child.wait();
        }
    }
}

#[cfg(feature = "grpc-encoder")]
static PYTHON_GUARD: OnceLock<PythonMeowvecGuard> = OnceLock::new();

/// Spawn `examples/meowvec_server.py` (onnxruntime) on the requested port.
#[cfg(feature = "grpc-encoder")]
pub fn spawn_python_meowvec(port: u16) -> Child {
    let root = std::env::var("CARGO_MANIFEST_DIR")
        .map(std::path::PathBuf::from)
        .unwrap_or_else(|_| std::path::PathBuf::from("."));
    let script = root.join("examples").join("meowvec_server.py");
    let model_path = root.join("models").join("bge-m3-onnx-int8");

    let mut cmd = Command::new("python3");
    cmd.arg(script.as_os_str())
        .arg("--port")
        .arg(format!("{port}"))
        .arg("--model-path")
        .arg(model_path.as_os_str())
        .stdout(Stdio::null())
        .stderr(Stdio::null());
    cmd.spawn()
        .unwrap_or_else(|e| panic!("failed to spawn python meowvec_server.py: {}", e))
}

/// Ensure the Python meowvec server is running on the given port.
#[cfg(feature = "grpc-encoder")]
pub fn ensure_python_meowvec(port: u16) -> &'static PythonMeowvecGuard {
    PYTHON_GUARD.get_or_init(|| {
        let child = spawn_python_meowvec(port);
        wait_for_meowvec_ready(port).expect("python meowvec failed to become ready");
        PythonMeowvecGuard(std::sync::Mutex::new(child))
    })
}
