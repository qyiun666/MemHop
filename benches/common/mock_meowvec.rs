//! Automatic mock meowvec gRPC encoder lifecycle helper for benchmarks.
//!
//! Spawns the `mock_meowvec` example as a child process on a requested TCP port
//! and waits until its `health_check` RPC responds successfully before returning.

use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::sync::OnceLock;
use std::thread;
use std::time::{Duration, Instant};

static MOCK_BINARY: OnceLock<Result<PathBuf, String>> = OnceLock::new();

/// Return the path to the compiled `mock_meowvec` example binary, building it
/// once per process if necessary.
fn binary_path() -> Result<&'static Path, &'static str> {
    let result = MOCK_BINARY.get_or_init(|| {
        let status = Command::new("cargo")
            .args(["build", "--example", "mock_meowvec"])
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .status()
            .map_err(|e| format!("failed to invoke cargo build: {e}"))?;
        if !status.success() {
            return Err(format!(
                "cargo build --example mock_meowvec failed: {status}"
            ));
        }

        let manifest_dir = std::env::var("CARGO_MANIFEST_DIR").unwrap_or_else(|_| ".".to_string());
        let exe = PathBuf::from(manifest_dir)
            .join("target")
            .join("debug")
            .join("examples")
            .join(format!("mock_meowvec{}", std::env::consts::EXE_SUFFIX));
        if !exe.exists() {
            return Err(format!(
                "mock_meowvec binary not found at {}",
                exe.display()
            ));
        }
        Ok(exe)
    });

    result.as_ref().map(|p| p.as_path()).map_err(|s| s.as_str())
}

/// Spawn `mock_meowvec` on `127.0.0.1:{port}` and block until it is ready.
///
/// Panics if the binary cannot be built or the server does not become healthy
/// within a timeout.
pub fn spawn_mock_meowvec(port: u16) -> Child {
    let exe = binary_path().expect("failed to locate mock_meowvec binary");
    let addr = format!("127.0.0.1:{port}");

    let mut child = Command::new(exe)
        .arg("--addr")
        .arg(&addr)
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .expect("failed to spawn mock_meowvec");

    if let Err(e) = wait_for_meowvec_ready(port) {
        let _ = child.kill();
        panic!("mock_meowvec on port {port} did not become ready: {e}");
    }

    child
}

/// Wait up to 30 seconds for the mock encoder's gRPC health check to succeed.
pub fn wait_for_meowvec_ready(port: u16) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    let addr = format!("http://127.0.0.1:{port}");
    let deadline = Instant::now() + Duration::from_secs(30);

    while Instant::now() < deadline {
        match memhop::encoder::GrpcEncoder::new(&addr, 384) {
            Ok(_) => return Ok(()),
            Err(_) => thread::sleep(Duration::from_millis(100)),
        }
    }

    Err(format!("mock_meowvec on port {port} not ready within 30s").into())
}

/// Kill the mock meowvec child process and reap its exit status.
pub fn kill_mock_meowvec(child: &mut Child) {
    let _ = child.kill();
    let _ = child.wait();
}
