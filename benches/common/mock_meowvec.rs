//! Python onnxruntime meowvec gRPC encoder lifecycle helper for benchmarks.
//!
//! Spawns `examples/meowvec_server.py` (BGE-M3 ONNX via onnxruntime) on a
//! requested TCP port and waits until its `health_check` RPC responds
//! successfully before returning.

use std::path::PathBuf;
use std::process::{Child, Command, Stdio};
use std::thread;
use std::time::{Duration, Instant};

/// Wait up to 60 seconds for the meowvec gRPC health check to succeed.
pub fn wait_for_meowvec_ready(port: u16) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    let addr = format!("http://127.0.0.1:{port}");
    let deadline = Instant::now() + Duration::from_secs(60);

    while Instant::now() < deadline {
        match memhop::encoder::GrpcEncoder::new(&addr, 1024) {
            Ok(_) => return Ok(()),
            Err(_) => thread::sleep(Duration::from_millis(500)),
        }
    }

    Err(format!("meowvec on port {port} not ready within 60s").into())
}

// ---------------------------------------------------------------------------
// Python onnxruntime meowvec server — cross-platform, GPU-capable
// ---------------------------------------------------------------------------

/// Spawn `examples/meowvec_server.py` (onnxruntime Python server) on `127.0.0.1:{port}`.
pub fn spawn_python_meowvec(port: u16) -> Child {
    let manifest_dir = std::env::var("CARGO_MANIFEST_DIR").unwrap_or_else(|_| ".".to_string());
    let script = PathBuf::from(&manifest_dir)
        .join("examples")
        .join("meowvec_server.py");
    let model_path = PathBuf::from(&manifest_dir)
        .join("models")
        .join("bge-m3-onnx-int8");

    if !script.exists() {
        panic!("Python server not found at {}", script.display());
    }

    let mut child = Command::new("python3")
        .arg(script.as_os_str())
        .arg("--port")
        .arg(format!("{port}"))
        .arg("--model-path")
        .arg(model_path.as_os_str())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .expect("failed to spawn python meowvec_server.py");

    if let Err(e) = wait_for_meowvec_ready(port) {
        let _ = child.kill();
        panic!("python meowvec on port {port} did not become ready: {e}");
    }

    child
}

/// Kill the Python meowvec child process and reap its exit status.
pub fn kill_python_meowvec(child: &mut Child) {
    let _ = child.kill();
    let _ = child.wait();
}
