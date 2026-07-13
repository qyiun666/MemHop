//! Minimal E2E smoke tests for the v0.57+ API.
//!
//! Full integration tests (with gRPC encoder) were removed due to API restructuring.
//! Run with:
//!     cargo test -- --ignored --test e2e_test

mod common;

use memhop::{MemHop, MemHopConfig};
use tempfile::TempDir;

#[test]
fn test_open_close_db() {
    let port = common::ensure_encoder_running();
    let dir = TempDir::new().unwrap();
    let path = dir.path().join("e2e.meh");
    let mut config = MemHopConfig::new(path, 768);
    config.encoder_grpc_addr = format!("http://127.0.0.1:{}", port);
    let db = MemHop::open(config).unwrap();
    db.close().unwrap();
}
