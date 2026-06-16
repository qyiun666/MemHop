//! Benchmark: GrpcEncoder over Unix Domain Socket
//!
//! Requires mock_meowvec server running:
//!   cargo run --example mock_meowvec -- --uds /tmp/meowvec_bench.sock &
//!   cargo bench --bench grpc_bench

use criterion::{black_box, criterion_group, criterion_main, Criterion};
use memhop::encoder::{Encoder, GrpcEncoder};

const UDS_ADDR: &str = "unix:///tmp/meowvec_bench.sock";

/// Benchmark single gRPC encode call over UDS
fn bench_grpc_encode(c: &mut Criterion) {
    let encoder = GrpcEncoder::new(UDS_ADDR, 384)
        .expect("mock_meowvec must be running on unix:///tmp/meowvec_bench.sock");

    let _ = encoder.encode("warm-up text").unwrap();

    c.bench_function("grpc_encode_uds", |b| {
        b.iter(|| {
            let output = encoder
                .encode(black_box("How does Rust ownership work?"))
                .unwrap();
            black_box(output.dense.len())
        })
    });
}

/// Benchmark 10 sequential gRPC encode calls over UDS
fn bench_grpc_encode_batch(c: &mut Criterion) {
    let encoder = GrpcEncoder::new(UDS_ADDR, 384)
        .expect("mock_meowvec must be running on unix:///tmp/meowvec_bench.sock");

    let texts: Vec<String> = (0..10)
        .map(|i| format!("Sample text number {} for encoding benchmark", i))
        .collect();

    c.bench_function("grpc_encode_uds_10_sequential", |b| {
        b.iter(|| {
            for text in &texts {
                let output = encoder.encode(black_box(text)).unwrap();
                black_box(output.dense.len());
            }
        })
    });
}

/// Benchmark: full search_memory pipeline with gRPC encoder over UDS
fn bench_search_with_grpc(c: &mut Criterion) {
    use memhop::query::types::SearchQuery;
    use memhop::{MemHop, MemHopConfig};
    use tempfile::TempDir;

    let temp_dir = TempDir::new().unwrap();
    let db_path = temp_dir.path().join("bench_grpc.meh");

    let config = MemHopConfig {
        db_path,
        encoder_grpc_addr: Some(UDS_ADDR.to_string()),
        vector_dim: 384,
        crystal_path: None,
    };

    let mut db = MemHop::open(config).expect("Failed to open MemHop");

    // Pre-populate with some data via auto_create
    for i in 0..5 {
        let q = SearchQuery {
            dialogue: format!("Topic {} about machine learning and neural networks", i),
            context_id: None,
            l3_id: None,
            context_limit: 10,
            llm_enhance: None,
            auto_create: 1,
            min_score: 0.0,
            context_history: None,
        };
        let _ = db.search_memory(q);
    }

    c.bench_function("search_memory_grpc_uds", |b| {
        b.iter(|| {
            let q = SearchQuery {
                dialogue: black_box("neural network deep learning".to_string()),
                context_id: None,
                l3_id: None,
                context_limit: 5,
                llm_enhance: None,
                auto_create: 0,
                min_score: 0.0,
                context_history: None,
            };
            let result = db.search_memory(q).unwrap();
            black_box(result.contexts.len())
        })
    });
}

criterion_group!(
    benches,
    bench_grpc_encode,
    bench_grpc_encode_batch,
    bench_search_with_grpc,
);
criterion_main!(benches);
