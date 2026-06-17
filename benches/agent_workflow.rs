//! Benchmark: Full Agent Workflow via FFI (JSON protocol)
//!
//! Simulates a real agent using MemHop — single database, all operations via FFI.
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
const ENCODER_ADDR: &str = "unix:///tmp/.meowagent/meowvec.sock";

// ============================================================================
// Global shared handle — opened once, used by all benchmarks
// ============================================================================

/// Wrapper to make raw pointer Send+Sync (safe: single-threaded bench usage)
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
    serde_json::from_str(&res_str).expect("response is not valid JSON")
}

// ============================================================================
// Benchmarks: Write operations
// ============================================================================

fn bench_search_auto_create(c: &mut Criterion) {
    unsafe {
        let handle = get_handle();
        let mut i = 0;
        c.bench_function("search_auto_create", |b| {
            b.iter(|| {
                i += 1;
                let cmd = format!(
                    r#"{{"command":"search","dialogue":"Benchmark topic {} about Rust programming and memory systems","auto_create":1,"context_limit":10,"min_score":0.0}}"#,
                    black_box(i)
                );
                let res = exec(handle, &cmd);
                assert!(res["success"].as_bool().unwrap_or(false));
                black_box(res["data"]["contexts"].as_array().unwrap().len())
            })
        });
    }
}

fn bench_update_memory(c: &mut Criterion) {
    unsafe {
        let handle = get_handle();

        // Create a topic first
        let res = exec(
            handle,
            r#"{"command":"search","dialogue":"Update benchmark topic","auto_create":1,"context_limit":5,"min_score":0.0}"#,
        );
        let topic_id = res["data"]["contexts"][0]["id"]
            .as_str()
            .unwrap()
            .to_string();

        let mut i = 0;
        c.bench_function("update_memory", |b| {
            b.iter(|| {
                i += 1;
                let cmd = format!(
                    r#"{{"command":"update","topic_id":"{}","dialogue_text":"User: Question {} about Rust\nAssistant: Answer about ownership and borrowing","action_chain":[]}}"#,
                    topic_id,
                    black_box(i)
                );
                let res = exec(handle, &cmd);
                assert!(res["success"].as_bool().unwrap_or(false));
            })
        });
    }
}

fn bench_import_knowledge(c: &mut Criterion) {
    unsafe {
        let handle = get_handle();
        let mut i = 0;
        c.bench_function("import_knowledge", |b| {
            b.iter(|| {
                i += 1;
                let cmd = format!(
                    r#"{{"command":"import","params":{{"action":"import","target_layer":"knowledge","mode":"merge","data":{{"Knowledge":[{{"title":"Concept {}","domain":"bench","knowledge_type":"Factual","text":"Benchmark knowledge item number {} about systems programming","keywords":["bench","systems"]}}]}}}}}}"#,
                    black_box(i),
                    i
                );
                let res = exec(handle, &cmd);
                assert!(res["success"].as_bool().unwrap_or(false));
            })
        });
    }
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

        // Get an existing topic_id
        let res = exec(
            handle,
            r#"{"command":"query_layer","layer":"l2","action":"list","list":{"page":1,"page_size":1}}"#,
        );
        let topic_id = res["data"]["items"][0]["id"]
            .as_str()
            .unwrap()
            .to_string();

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
    bench_search_auto_create,
    bench_update_memory,
    bench_import_knowledge,
    bench_search_recall,
    bench_query_l2_list,
    bench_session_activate,
);
criterion_main!(benches);
