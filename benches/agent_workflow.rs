//! Benchmark: Full Agent Workflow via FFI (JSON protocol)
//!
//! Single database, all operations via FFI. Phases:
//!   1. Setup: populate database with topics + knowledge
//!   2. Bench: measure search, update, import, query, session
//!
//! Requires mock_meowvec running:
//!   cargo run --example mock_meowvec &
//!   cargo bench --bench agent_workflow

use criterion::{black_box, criterion_group, criterion_main, Criterion};
use memhop::ffi::{memhop_execute, memhop_free_string, memhop_open, MemHopHandle};
use serde_json::Value;
use std::ffi::{CStr, CString};
use std::sync::OnceLock;

const DB_PATH: &str = "/tmp/memhop_bench.meh";
const ENCODER_ADDR: &str = "http://127.0.0.1:27110";

// ============================================================================
// Global shared handle — opened once, used by all benchmarks
// ============================================================================

struct Handle(*mut MemHopHandle);
unsafe impl Send for Handle {}
unsafe impl Sync for Handle {}

static HANDLE: OnceLock<Handle> = OnceLock::new();

unsafe fn get_handle() -> *mut MemHopHandle {
    HANDLE
        .get_or_init(|| {
            let _ = std::fs::remove_file(DB_PATH);
            let cfg = CString::new(format!(
                r#"{{"db_path":"{}","encoder_grpc_addr":"{}","vector_dim":384}}"#,
                DB_PATH, ENCODER_ADDR
            ))
            .unwrap();
            let handle = memhop_open(cfg.as_ptr());
            assert!(!handle.is_null(), "memhop_open failed — is mock_meowvec running?");

            // Pre-populate: create 10 topics
            for i in 0..10 {
                let cmd = CString::new(format!(
                    r#"{{"command":"search","dialogue":"Topic {} about machine learning neural networks deep learning","auto_create":1,"context_limit":10,"min_score":0.0}}"#,
                    i
                ))
                .unwrap();
                let res_ptr = memhop_execute(handle, cmd.as_ptr());
                memhop_free_string(res_ptr);
            }

            // Pre-populate: import 5 knowledge items
            for i in 0..5 {
                let cmd = CString::new(format!(
                    r#"{{"command":"import","params":{{"action":"import","target_layer":"knowledge","mode":"merge","data":{{"Knowledge":[{{"title":"Concept {}","domain":"bench","knowledge_type":"Factual","text":"Knowledge about systems programming and memory safety","keywords":["systems","memory"]}}]}}}}}}"#,
                    i
                ))
                .unwrap();
                let res_ptr = memhop_execute(handle, cmd.as_ptr());
                memhop_free_string(res_ptr);
            }

            // Sync to ensure all data is persisted before benchmarks
            let sync_cmd = CString::new(r#"{"command":"sync"}"#).unwrap();
            let res_ptr = memhop_execute(handle, sync_cmd.as_ptr());
            memhop_free_string(res_ptr);

            Handle(handle)
        })
        .0
}

unsafe fn exec(handle: *mut MemHopHandle, json: &str) -> Value {
    let cmd = CString::new(json).unwrap();
    let res_ptr = memhop_execute(handle, cmd.as_ptr());
    assert!(!res_ptr.is_null(), "memhop_execute returned null");
    let res_str = CStr::from_ptr(res_ptr).to_str().unwrap().to_string();
    memhop_free_string(res_ptr);
    let val: Value = serde_json::from_str(&res_str).expect("response is not valid JSON");
    if !val["success"].as_bool().unwrap_or(false) {
        eprintln!("FFI ERROR: cmd={}\n  resp={}", json, res_str);
    }
    val
}

// ============================================================================
// Benchmarks: Recall operations
// ============================================================================

fn bench_search_recall(c: &mut Criterion) {
    unsafe {
        let handle = get_handle();
        c.bench_function("search_recall", |b| {
            b.iter(|| {
                let res = exec(
                    handle,
                    r#"{"command":"search","dialogue":"neural network deep learning architecture","auto_create":0,"context_limit":5,"min_score":0.0}"#,
                );
                assert!(res["success"].as_bool().unwrap_or(false));
                black_box(res["data"]["contexts"].as_array().unwrap().len())
            })
        });
    }
}

// ============================================================================
// Benchmarks: Write operations
// ============================================================================

fn bench_update_memory(_c: &mut Criterion) {
    unsafe {
        let handle = get_handle();

        // Get first topic from the pre-populated database
        let res = exec(
            handle,
            r#"{"command":"query_layer","layer":"l2","action":"list","list":{"page":1,"page_size":1}}"#,
        );
        let topic_id = res["data"]["items"][0]["id"].as_str().unwrap().to_string();

        // Measure a single update call (each update allocates a page, so we
        // can't iterate without exhausting the fixed-size .meh file)
        let start = std::time::Instant::now();
        let cmd = format!(
            r#"{{"command":"update","topic_id":"{}","dialogue_text":"User: How does Rust work?\nAssistant: Ownership and borrowing","action_chain":[]}}"#,
            topic_id,
        );
        let res = exec(handle, &cmd);
        let elapsed = start.elapsed();
        assert!(
            res["success"].as_bool().unwrap_or(false),
            "update failed: {}",
            res
        );
        println!("update_memory (single): {:?}", elapsed);
    }
}

// ============================================================================
// Benchmarks: Query operations
// ============================================================================

fn bench_query_l2_list(c: &mut Criterion) {
    unsafe {
        let handle = get_handle();
        c.bench_function("query_l2_list", |b| {
            b.iter(|| {
                let res = exec(
                    handle,
                    r#"{"command":"query_layer","layer":"l2","action":"list","list":{"page":1,"page_size":10}}"#,
                );
                assert!(res["success"].as_bool().unwrap_or(false));
                black_box(res["data"]["total"].as_u64().unwrap_or(0))
            })
        });
    }
}

fn bench_session_activate(c: &mut Criterion) {
    unsafe {
        let handle = get_handle();

        let res = exec(
            handle,
            r#"{"command":"query_layer","layer":"l2","action":"list","list":{"page":1,"page_size":1}}"#,
        );
        let topic_id = res["data"]["items"][0]["id"].as_str().unwrap().to_string();

        c.bench_function("session_activate", |b| {
            b.iter(|| {
                let cmd = format!(
                    r#"{{"command":"session","params":{{"action":"activate","topic_id":"{}","ttl_ms":300000}}}}"#,
                    black_box(&topic_id)
                );
                let res = exec(handle, &cmd);
                assert!(res["success"].as_bool().unwrap_or(false));
            })
        });
    }
}

criterion_group!(
    benches,
    bench_search_recall,
    bench_update_memory,
    bench_query_l2_list,
    bench_session_activate,
);
criterion_main!(benches);
