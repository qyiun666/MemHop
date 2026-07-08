//! Candle gRPC encoder lifecycle helper for benchmarks.
//!
//! The Rust `candle-encoder` server must be started manually **before** running
//! benchmarks (see `tools/candle-encoder/`). This module only performs a
//! health‑check wait so benchmarks block until the server is ready.
//!
//! Usage:
//!   # Terminal 1 — start the encoder
//!   cargo run --release --manifest-path tools/candle-encoder/Cargo.toml
//!
//!   # Terminal 2 — run benchmarks
//!   cargo bench

use std::sync::Mutex;
use std::thread;
use std::time::{Duration, Instant};

/// Global spinner state — ensures at most one wait loop is started.
static GLOBAL_READY: std::sync::OnceLock<Mutex<bool>> = std::sync::OnceLock::new();

/// Wait for a gRPC encoder to be reachable on `port`.
///
/// A global flag prevents repeated wait attempts once the server has been
/// confirmed ready.
pub fn ensure_meowvec_running(port: u16) {
    let flag = GLOBAL_READY.get_or_init(|| Mutex::new(false));
    let mut ready = flag.lock().unwrap();
    if *ready {
        return;
    }

    let addr = format!("http://127.0.0.1:{port}");
    let deadline = Instant::now() + Duration::from_secs(60);

    while Instant::now() < deadline {
        match memhop::encoder::GrpcEncoder::new(&addr, 1024) {
            Ok(_) => {
                eprintln!("[benches] gRPC encoder ready on {addr}");
                *ready = true;
                return;
            }
            Err(_) => thread::sleep(Duration::from_millis(500)),
        }
    }

    panic!(
        "gRPC encoder not ready on {addr} within 60s.\n\
         Make sure the candle-encoder server is running:\n\
         cargo run --release --manifest-path tools/candle-encoder/Cargo.toml"
    );
}

/// No-op — the Rust server manages its own lifecycle.
pub fn cleanup_global_meowvec() {}

/// Legacy — kept for backward compatibility.
pub fn kill_python_meowvec(_child: &mut std::process::Child) {}

/// Panics — Python meowvec has been removed.
pub fn spawn_python_meowvec(_port: u16) -> std::process::Child {
    panic!("Python meowvec removed; use the Rust candle-encoder instead");
}
