//! Benchmark: Full Agent Workflow via FFI (JSON protocol)
//!
//! Simulates a real agent using MemHop:
//!   1. Start mock_meowvec vector model server
//!   2. Open/create database via FFI
//!   3. Write memories (search+auto_create, update, import, batch_store)
//!   4. Recall memories (search with vector, query layers)
//!   5. Merge topics
//!   6. Close database
//!
//! Requires mock_meowvec running:
//!   cargo run --example mock_meowvec &
//!   cargo bench --bench agent_workflow

use criterion::{black_box, criterion_group, criterion_main, Criterion};
use memhop::ffi::{memhop_close, memhop_execute, memhop_free_string, memhop_open, MemHopHandle};
use serde_json::Value;
use std::ffi::{CStr, CString};

const DB_PATH: &str = "/tmp/memhop_bench.meh";

// ============================================================================
// FFI helpers
// ============================================================================

unsafe fn exec(handle: *mut MemHopHandle, json: &str) -> Value {
    let cmd = CString::new(json).unwrap();
    let res_ptr = memhop_execute(handle, cmd.as_ptr());
    assert!(!res_ptr.is_null(), "memhop_execute returned null");
    let res_str = CStr::from_ptr(res_ptr).to_str().unwrap().to_string();
    memhop_free_string(res_ptr);
    serde_json::from_str(&res_str).expect("response is not valid JSON")
}

unsafe fn open_db() -> *mut MemHopHandle {
    let _ = std::fs::remove_file(DB_PATH);
    let cfg = CString::new(format!(
        r#"{{"db_path":"{}","encoder_grpc_addr":"unix:///tmp/.meowagent/meowvec.sock","vector_dim":384}}"#,
        DB_PATH
    ))
    .unwrap();
    let handle = memhop_open(cfg.as_ptr());
    assert!(!handle.is_null(), "memhop_open failed — is mock_meowvec running?");
    handle
}

// ============================================================================
// Benchmarks: Write operations
// ============================================================================

fn bench_search_auto_create(c: &mut Criterion) {
    unsafe {
        let handle = open_db();

        c.bench_function("search_auto_create", |b| {
            let mut i = 0;
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

        memhop_close(handle);
    }
}

fn bench_update_memory(c: &mut Criterion) {
    unsafe {
        let handle = open_db();

        // Create a topic first
        let res = exec(
            handle,
            r#"{"command":"search","dialogue":"Update benchmark topic","auto_create":1,"context_limit":5,"min_score":0.0}"#,
        );
        let topic_id = res["data"]["contexts"][0]["id"]
            .as_str()
            .unwrap()
            .to_string();

        c.bench_function("update_memory", |b| {
            let mut i = 0;
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

        memhop_close(handle);
    }
}

fn bench_import_knowledge(c: &mut Criterion) {
    unsafe {
        let handle = open_db();

        c.bench_function("import_knowledge", |b| {
            let mut i = 0;
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

        memhop_close(handle);
    }
}

// ============================================================================
// Benchmarks: Recall operations
// ============================================================================

fn bench_search_recall(c: &mut Criterion) {
    unsafe {
        let handle = open_db();

        // Pre-populate 10 topics
        for i in 0..10 {
            let cmd = format!(
                r#"{{"command":"search","dialogue":"Recall topic {} about neural networks and deep learning architectures","auto_create":1,"context_limit":10,"min_score":0.0}}"#,
                i
            );
            exec(handle, &cmd);
        }

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

        memhop_close(handle);
    }
}

fn bench_query_l2_list(c: &mut Criterion) {
    unsafe {
        let handle = open_db();

        // Pre-populate
        for i in 0..10 {
            let cmd = format!(
                r#"{{"command":"search","dialogue":"List topic {} about various subjects","auto_create":1,"context_limit":10,"min_score":0.0}}"#,
                i
            );
            exec(handle, &cmd);
        }

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

        memhop_close(handle);
    }
}

// ============================================================================
// Benchmarks: Full lifecycle
// ============================================================================

fn bench_full_lifecycle(c: &mut Criterion) {
    c.bench_function("full_agent_lifecycle", |b| {
        b.iter(|| {
            unsafe {
                let _ = std::fs::remove_file(DB_PATH);
                let handle = open_db();

                // 1. Write: create topic via search
                let res = exec(
                    handle,
                    r#"{"command":"search","dialogue":"Lifecycle test about Rust memory safety","auto_create":1,"context_limit":5,"min_score":0.0}"#,
                );
                let topic_id = res["data"]["contexts"][0]["id"]
                    .as_str()
                    .unwrap()
                    .to_string();

                // 2. Write: update with dialogue
                let cmd = format!(
                    r#"{{"command":"update","topic_id":"{}","dialogue_text":"User: How does borrowing work?\nAssistant: Rust uses references with lifetimes","action_chain":[]}}"#,
                    topic_id
                );
                exec(handle, &cmd);

                // 3. Write: import knowledge
                exec(
                    handle,
                    r#"{"command":"import","params":{"action":"import","target_layer":"knowledge","mode":"merge","data":{"Knowledge":[{"title":"Rust Borrowing","domain":"programming","knowledge_type":"Conceptual","text":"Borrowing allows temporary access without ownership transfer","keywords":["rust","borrowing"]}]}}}"#,
                );

                // 4. Recall: search
                let res = exec(
                    handle,
                    r#"{"command":"search","dialogue":"memory safety borrowing","auto_create":0,"context_limit":5,"min_score":0.0}"#,
                );
                black_box(res["data"]["contexts"].as_array().unwrap().len());

                // 5. Query: L2 list
                exec(
                    handle,
                    r#"{"command":"query_layer","layer":"l2","action":"list","list":{"page":1,"page_size":10}}"#,
                );

                // 6. Session: activate
                let cmd = format!(
                    r#"{{"command":"session","params":{{"action":"activate","topic_id":"{}","ttl_ms":300000}}}}"#,
                    topic_id
                );
                exec(handle, &cmd);

                // 7. Sync
                exec(handle, r#"{"command":"sync"}"#);

                // 8. Close & free
                memhop_close(handle);
            }
        })
    });
}

criterion_group!(
    benches,
    bench_search_auto_create,
    bench_update_memory,
    bench_import_knowledge,
    bench_search_recall,
    bench_query_l2_list,
    bench_full_lifecycle,
);
criterion_main!(benches);
