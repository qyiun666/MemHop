// Shared test helpers for managing the mock meowvec gRPC encoder process.
//
// Integration tests that need a real-ish vector encoder can call
// `ensure_mock_meowvec(port)` at the start of each test. The first call builds
// the example binary and spawns the server; subsequent calls reuse the same
// process. The process is killed when the test binary exits.

use std::process::{Child, Command, Stdio};
use std::sync::OnceLock;
use std::time::Duration;

#[cfg(feature = "grpc-encoder")]
pub mod vector_model {
    tonic::include_proto!("vector_model");
}

static BUILD_ONCE: OnceLock<()> = OnceLock::new();

/// Build the `mock_meowvec` example once per test binary invocation.
///
/// This must run after `cargo test` has finished compiling the test binary,
/// because cargo releases the build lock before executing tests.
fn build_mock_meowvec() {
    BUILD_ONCE.get_or_init(|| {
        let status = Command::new("cargo")
            .args(["build", "--example", "mock_meowvec"])
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .status()
            .expect("failed to execute `cargo build --example mock_meowvec`");
        assert!(
            status.success(),
            "`cargo build --example mock_meowvec` failed with status {}",
            status
        );
    });
}

/// Return the target directory used by cargo.
fn target_dir() -> std::path::PathBuf {
    std::env::var("CARGO_TARGET_DIR")
        .map(std::path::PathBuf::from)
        .unwrap_or_else(|_| std::path::PathBuf::from("target"))
}

/// Return the path to the built `mock_meowvec` example binary.
fn example_binary_path() -> std::path::PathBuf {
    let mut path = target_dir();
    path.push(std::env::var("PROFILE").unwrap_or_else(|_| "debug".to_string()));
    path.push("examples");
    #[cfg(target_os = "windows")]
    path.push("mock_meowvec.exe");
    #[cfg(not(target_os = "windows"))]
    path.push("mock_meowvec");
    path
}

/// Spawn `examples/mock_meowvec.rs` on the requested port.
///
/// The example is built first via `cargo build --example mock_meowvec` if it
/// has not already been built in this test run.
pub fn spawn_mock_meowvec(port: u16) -> Child {
    build_mock_meowvec();
    let binary = example_binary_path();
    let addr = format!("127.0.0.1:{}", port);
    let mut cmd = Command::new(&binary);
    cmd.args(["--addr", &addr])
        .stdout(Stdio::null())
        .stderr(Stdio::null());
    cmd.spawn().unwrap_or_else(|e| {
        panic!(
            "failed to spawn mock_meowvec at {}: {}",
            binary.display(),
            e
        )
    })
}

/// Poll the mock meowvec gRPC health check until it reports healthy.
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

    for attempt in 0..60 {
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
            Ok(false) => {}
            Err(_) if attempt == 59 => {}
            Err(_) => {}
        }
        std::thread::sleep(Duration::from_millis(500));
    }
    Err(format!(
        "mock_meowvec on port {} did not become ready within 30s",
        port
    ))
}

#[cfg(not(feature = "grpc-encoder"))]
pub fn wait_for_meowvec_ready(_port: u16) -> Result<(), String> {
    Err("grpc-encoder feature is disabled; cannot wait for meowvec".to_string())
}

/// Kill a mock meowvec child process and reap it.
pub fn kill_mock_meowvec(child: &mut Child) {
    let _ = child.kill();
    let _ = child.wait();
}

/// Guard that keeps a mock meowvec process alive and kills it on drop.
#[cfg(feature = "grpc-encoder")]
pub struct MockMeowvecGuard(std::sync::Mutex<Child>);

#[cfg(feature = "grpc-encoder")]
impl Drop for MockMeowvecGuard {
    fn drop(&mut self) {
        if let Ok(mut child) = self.0.lock() {
            kill_mock_meowvec(&mut child);
        }
    }
}

#[cfg(feature = "grpc-encoder")]
static MOCK_GUARD: OnceLock<MockMeowvecGuard> = OnceLock::new();

/// Ensure the mock meowvec server is running on the given port.
///
/// The first call in a test binary builds the example, spawns the process, and
/// waits for the health check to pass. Subsequent calls return the same guard.
/// The process is killed automatically when the test binary exits.
#[cfg(feature = "grpc-encoder")]
pub fn ensure_mock_meowvec(port: u16) -> &'static MockMeowvecGuard {
    MOCK_GUARD.get_or_init(|| {
        let child = spawn_mock_meowvec(port);
        wait_for_meowvec_ready(port).expect("mock_meowvec failed to become ready");
        MockMeowvecGuard(std::sync::Mutex::new(child))
    })
}
