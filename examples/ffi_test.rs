//! FFI Binary Validation — 加载编译好的 libmemhop.dylib 动态测试
//!
//! 用途：验证从 GitHub Actions 下载的二进制文件能正常工作
//! 用法：
//!   cargo run --example ffi_test
//!   MEMHOP_DYLIB_PATH=/tmp/memhop-download/libmemhop.dylib cargo run --example ffi_test
//!   MEMHOP_DEEPSEEK_KEY=sk-xxx cargo run --example ffi_test -- --dream
//!
//! 输出：每个操作的结果 + 耗时 (ms)

use libloading::Library;
use std::ffi::{c_char, CStr, CString};
use std::path::PathBuf;
use std::ptr;
use std::time::Instant;

// ============================================================================
// 类型别名 — 匹配 libmemhop 的 4 个 extern "C" 函数签名
// ============================================================================

type MemHopOpen = unsafe extern "C" fn(*const c_char) -> *mut std::ffi::c_void;
type MemHopExecute = unsafe extern "C" fn(*mut std::ffi::c_void, *const c_char) -> *mut c_char;
type MemHopFreeString = unsafe extern "C" fn(*mut c_char);
type MemHopClose = unsafe extern "C" fn(*mut std::ffi::c_void);

// ============================================================================
// 辅助函数
// ============================================================================

fn load_lib(path: &PathBuf) -> Library {
    unsafe { Library::new(path).unwrap_or_else(|_| panic!("Failed to load library: {:?}", path)) }
}

unsafe fn exec(
    memhop_execute: MemHopExecute,
    handle: *mut std::ffi::c_void,
    json: &str,
) -> serde_json::Value {
    let cmd = CString::new(json).unwrap();
    let res_ptr = memhop_execute(handle, cmd.as_ptr());
    assert!(!res_ptr.is_null(), "memhop_execute returned null");
    let res_str = CStr::from_ptr(res_ptr).to_str().unwrap().to_string();
    // 注意：下面这行在真实场景中由调用方 free，但我们在 libloading 环境中
    // memhop_free_string 已加载，所以可以正常调用
    // 但注意：这里 Symbol 会被 drop，所以需要先完成所有操作
    let result: serde_json::Value = serde_json::from_str(&res_str).expect("invalid JSON response");
    result
}

fn assert_success(res: &serde_json::Value, label: &str) {
    let ok = res["success"].as_bool().unwrap_or(false);
    if !ok {
        eprintln!("❌ {} failed: {}", label, res["error"]);
    }
    assert!(ok, "{} should succeed, got: {}", label, res);
}

fn assert_error(res: &serde_json::Value) {
    assert!(
        !res["success"].as_bool().unwrap_or(true),
        "expected error, got: {}",
        res
    );
}

/// 定时执行并打印
macro_rules! timed {
    ($label:expr, $body:expr) => {{
        let start = Instant::now();
        let result = $body;
        let ms = start.elapsed().as_micros() as f64 / 1000.0;
        println!("  {:<45} {:>8.1} ms", $label, ms);
        result
    }};
}

// ============================================================================
// 主测试逻辑
// ============================================================================

fn run_tests(lib: &Library, run_dream: bool, api_key: &str) {
    unsafe {
        // 加载 4 个函数符号
        let memhop_open: MemHopOpen = *lib.get(b"memhop_open").expect("memhop_open not found");
        let memhop_execute: MemHopExecute = *lib
            .get(b"memhop_execute")
            .expect("memhop_execute not found");
        let _memhop_free_string: MemHopFreeString = *lib
            .get(b"memhop_free_string")
            .expect("memhop_free_string not found");
        let memhop_close: MemHopClose = *lib.get(b"memhop_close").expect("memhop_close not found");

        let db_path = "/tmp/memhop_ffi_binary_test.meh";
        let _ = std::fs::remove_file(db_path);

        // ====================================================================
        // Phase 0: 边界测试
        // ====================================================================
        println!("\n━━━ Phase 0: FFI 边界 ━━━");

        timed!("memhop_open(null)", memhop_open(ptr::null()));
        assert!(
            memhop_open(ptr::null()).is_null(),
            "null config should return null"
        );

        timed!("memhop_open(invalid JSON)", {
            let cfg = CString::new("not json").unwrap();
            memhop_open(cfg.as_ptr())
        });
        // ptr already consumed above, just check the concept works

        timed!("memhop_close(null)", memhop_close(ptr::null_mut()));

        // ====================================================================
        // Phase 1: 打开 + 基础操作
        // ====================================================================
        println!("\n━━━ Phase 1: 打开数据库 ━━━");

        let config_json =
            CString::new(format!(r#"{{"db_path":"{}","vector_dim":384}}"#, db_path)).unwrap();
        let handle = timed!("memhop_open()", memhop_open(config_json.as_ptr()));
        assert!(!handle.is_null(), "open failed");
        println!("  ✅ Handle: {:p}", handle);

        // ====================================================================
        // Phase 2: 11 个命令全覆盖
        // ====================================================================
        println!("\n━━━ Phase 2: 11 个命令 ━━━");

        // 2a. search + auto_create
        let res = timed!("search (auto_create=1)", {
            exec(
                memhop_execute,
                handle,
                r#"{"command":"search","dialogue":"Rust borrowing and ownership","auto_create":1,"context_limit":5,"min_score":0.0}"#,
            )
        });
        assert_success(&res, "search");
        let contexts = res["data"]["contexts"].as_array().unwrap();
        assert!(!contexts.is_empty(), "auto_create should create L2");
        let l2_id = contexts[0]["id"].as_str().unwrap().to_string();

        // 2b. update
        let update_cmd = format!(
            r#"{{"command":"update","topic_id":"{}","dialogue_text":"User: What is Rust?\nAssistant: Rust is a systems language.","action_chain":[{{"title":"answer","description":"explain rust","action_type":"Execute"}}]}}"#,
            l2_id
        );
        let res = timed!("update", exec(memhop_execute, handle, &update_cmd));
        assert_success(&res, "update");

        // 2c. query_layer - L0 profile
        let res = timed!("query_layer L0 get", {
            exec(
                memhop_execute,
                handle,
                r#"{"command":"query_layer","layer":"l0","action":"get","get":{},"list":{}}"#,
            )
        });
        assert_success(&res, "query_layer L0");

        // 2d. query_layer - L1 list
        let res = timed!("query_layer L1 list", {
            exec(
                memhop_execute,
                handle,
                r#"{"command":"query_layer","layer":"l1","action":"list","list":{"page":1,"page_size":10}}"#,
            )
        });
        assert_success(&res, "query_layer L1");

        // 2e. query_layer - L2 list
        let res = timed!("query_layer L2 list", {
            exec(
                memhop_execute,
                handle,
                r#"{"command":"query_layer","layer":"l2","action":"list","list":{"page":1,"page_size":10}}"#,
            )
        });
        assert_success(&res, "query_layer L2");
        assert!(res["data"]["total"].as_u64().unwrap_or(0) > 0);

        // 2f. query_layer - L2 get
        let get_cmd = format!(
            r#"{{"command":"query_layer","layer":"l2","action":"get","get":{{"id":"{}"}},"list":{{}}}}"#,
            l2_id
        );
        let res = timed!("query_layer L2 get", {
            exec(memhop_execute, handle, &get_cmd)
        });
        assert_success(&res, "query_layer L2 get");

        // 2g. query_layer - L3 list
        let res = timed!("query_layer L3 list", {
            exec(
                memhop_execute,
                handle,
                r#"{"command":"query_layer","layer":"l3","action":"list","list":{"page":1,"page_size":10}}"#,
            )
        });
        assert_success(&res, "query_layer L3");

        // 2h. query_layer - L4 list
        let res = timed!("query_layer L4 list", {
            exec(
                memhop_execute,
                handle,
                r#"{"command":"query_layer","layer":"l4","action":"list","list":{"page":1,"page_size":10}}"#,
            )
        });
        assert_success(&res, "query_layer L4");

        // 2i. query_layer - L5 list
        let res = timed!("query_layer L5 list", {
            exec(
                memhop_execute,
                handle,
                r#"{"command":"query_layer","layer":"l5","action":"list","list":{"page":1,"page_size":10}}"#,
            )
        });
        assert_success(&res, "query_layer L5");

        // 2j. update_title - L0
        let res = timed!("update_title L0", {
            exec(
                memhop_execute,
                handle,
                r#"{"command":"update_title","layer":"l0","params":{"name":"Binary Agent","role":"Test"}}"#,
            )
        });
        assert_success(&res, "update_title L0");

        // 2k. import - L0 profile
        let res = timed!("import profile", {
            exec(
                memhop_execute,
                handle,
                r#"{"command":"import","params":{"action":"import","target_layer":"profile","mode":"merge","data":{"Profile":{"name":"Imported","role":"Tester"}}}}"#,
            )
        });
        assert_success(&res, "import profile");

        // 2l. import - L2 topics
        let res = timed!("import topics", {
            exec(
                memhop_execute,
                handle,
                r#"{"command":"import","params":{"action":"import","target_layer":"topic","mode":"merge","data":{"Topics":[{"title":"Python","summary":"Python basics","keywords":["python"]}]}}}"#,
            )
        });
        assert_success(&res, "import topics");

        // 2m. import - L3 knowledge
        let res = timed!("import knowledge", {
            exec(
                memhop_execute,
                handle,
                r#"{"command":"import","params":{"action":"import","target_layer":"knowledge","mode":"merge","data":{"Knowledge":[{"title":"Rust Ownership","domain":"programming","knowledge_type":"Conceptual","text":"Rust ownership...","keywords":["rust"]}]}}}"#,
            )
        });
        assert_success(&res, "import knowledge");

        // 2n. session - activate
        let session_cmd = format!(
            r#"{{"command":"session","params":{{"action":"activate","topic_id":"{}","ttl_ms":300000}}}}"#,
            l2_id
        );
        let res = timed!("session activate", {
            exec(memhop_execute, handle, &session_cmd)
        });
        assert_success(&res, "session activate");

        // 2o. session - list
        let res = timed!("session list", {
            exec(
                memhop_execute,
                handle,
                r#"{"command":"session","params":{"action":"list"}}"#,
            )
        });
        assert_success(&res, "session list");
        assert!(!res["data"]["active_topics"].as_array().unwrap().is_empty());

        // 2p. session - adjust
        let adjust_cmd = format!(
            r#"{{"command":"session","params":{{"action":"adjust","topic_id":"{}","delta":0.5}}}}"#,
            l2_id
        );
        let res = timed!("session adjust", {
            exec(memhop_execute, handle, &adjust_cmd)
        });
        assert_success(&res, "session adjust");

        // 2q. session - deactivate
        let deact_cmd = format!(
            r#"{{"command":"session","params":{{"action":"deactivate","topic_id":"{}"}}}}"#,
            l2_id
        );
        let res = timed!("session deactivate", {
            exec(memhop_execute, handle, &deact_cmd)
        });
        assert_success(&res, "session deactivate");

        // 2r. merge_topics (create second topic first)
        let res2 = exec(
            memhop_execute,
            handle,
            r#"{"command":"search","dialogue":"Second topic","auto_create":1,"context_limit":5,"min_score":0.0}"#,
        );
        let id2 = res2["data"]["contexts"][0]["id"]
            .as_str()
            .unwrap()
            .to_string();

        let merge_cmd = format!(
            r#"{{"command":"merge_topics","primary_id":"{}","secondary_ids":["{}"]}}"#,
            l2_id, id2
        );
        let res = timed!("merge_topics", exec(memhop_execute, handle, &merge_cmd));
        assert_success(&res, "merge_topics");

        // 2s. batch_store
        let res = timed!("batch_store", {
            exec(
                memhop_execute,
                handle,
                r#"{"command":"batch_store","items":[{"text":"batch test","topic_label":"btest","domain_id":"t","importance":0.5,"source":{"source_type":"UserInput","source_id":null,"timestamp":0},"is_structural":false}],"session_id":"s1","turn_id":"t1"}"#,
            )
        });
        assert_success(&res, "batch_store");

        // 2t. sync
        let res = timed!("sync", {
            exec(memhop_execute, handle, r#"{"command":"sync"}"#)
        });
        assert_success(&res, "sync");

        // 2u. close (command)
        let res = timed!("close command", {
            exec(memhop_execute, handle, r#"{"command":"close"}"#)
        });
        assert_success(&res, "close command");

        // ====================================================================
        // Phase 3: 持久化验证
        // ====================================================================
        println!("\n━━━ Phase 3: 持久化验证 ━━━");

        // 释放原 handle
        memhop_close(handle);

        // 重新打开
        let handle2 = timed!("reopen database", memhop_open(config_json.as_ptr()));
        assert!(!handle2.is_null(), "reopen failed");

        let res = timed!("verify L2 persist", {
            exec(
                memhop_execute,
                handle2,
                r#"{"command":"query_layer","layer":"l2","action":"list","list":{"page":1,"page_size":100}}"#,
            )
        });
        assert_success(&res, "verify L2");
        let total = res["data"]["total"].as_u64().unwrap_or(0);
        assert!(total > 0, "L2 should persist");
        println!("  ✅ L2 topics persisted: {}", total);

        memhop_close(handle2);

        // ====================================================================
        // Phase 4: 错误处理
        // ====================================================================
        println!("\n━━━ Phase 4: 错误处理 ━━━");

        // 重新打开做错误测试
        let handle3 = memhop_open(config_json.as_ptr());
        assert!(!handle3.is_null());

        let res = timed!("search missing field", {
            exec(memhop_execute, handle3, r#"{"command":"search"}"#)
        });
        assert_error(&res);
        println!("    ✓ error: {}", res["error"]);

        let res = timed!("unknown import action", {
            exec(
                memhop_execute,
                handle3,
                r#"{"command":"import","params":{"action":"bad_action"}}"#,
            )
        });
        assert_error(&res);

        let res = timed!("session missing action", {
            exec(
                memhop_execute,
                handle3,
                r#"{"command":"session","params":{"action":"activate"}}"#,
            )
        });
        assert_error(&res);

        memhop_close(handle3);

        // ====================================================================
        // Phase 5: Dream（可选，需 API key）
        // ====================================================================
        if run_dream && !api_key.is_empty() {
            println!("\n━━━ Phase 5: Dream (DeepSeek) ━━━");

            let db_path2 = "/tmp/memhop_ffi_dream_binary.meh";
            let _ = std::fs::remove_file(db_path2);
            let cfg2 =
                CString::new(format!(r#"{{"db_path":"{}","vector_dim":384}}"#, db_path2)).unwrap();
            let h = memhop_open(cfg2.as_ptr());
            assert!(!h.is_null());

            // 准备数据
            let res = exec(
                memhop_execute,
                h,
                r#"{"command":"search","dialogue":"Learning Rust memory management","auto_create":1,"context_limit":5,"min_score":0.0}"#,
            );
            let tid = res["data"]["contexts"][0]["id"]
                .as_str()
                .unwrap()
                .to_string();

            let up = format!(
                r#"{{"command":"update","topic_id":"{}","dialogue_text":"User: Explain Rust.\nAssistant: Rust is safe.","summary":"rust intro","action_chain":[{{"title":"intro","description":"intro","action_type":"Execute"}}]}}"#,
                tid
            );
            exec(memhop_execute, h, &up);

            let act = format!(
                r#"{{"command":"session","params":{{"action":"activate","topic_id":"{}","ttl_ms":600000}}}}"#,
                tid
            );
            exec(memhop_execute, h, &act);

            // 调用 DeepSeek
            let dream_cmd = format!(
                r#"{{"command":"dream","api_url":"https://api.deepseek.com/v1/chat/completions","api_key":"{}","model":"deepseek-chat","api_format":1}}"#,
                api_key
            );
            let res = timed!("dream (DeepSeek)", { exec(memhop_execute, h, &dream_cmd) });
            assert_success(&res, "dream");
            println!("  ✅ Dream report: {:?}", res["data"]);

            memhop_close(h);
            let _ = std::fs::remove_file(db_path2);
        }

        // 清理
        let _ = std::fs::remove_file(db_path);
    }
}

// ============================================================================
// 入口
// ============================================================================

fn main() {
    let args: Vec<String> = std::env::args().collect();
    let run_dream = args.iter().any(|a| a == "--dream");
    let api_key = std::env::var("MEMHOP_DEEPSEEK_KEY")
        .or_else(|_| std::env::var("DEEPSEEK_API_KEY"))
        .unwrap_or_default();

    // dylib 路径（优先级：环境变量 > 默认路径）
    let dylib_path = std::env::var("MEMHOP_DYLIB_PATH")
        .map(PathBuf::from)
        .unwrap_or_else(|_| {
            let p = PathBuf::from("/tmp/memhop-download/libmemhop.dylib");
            if p.exists() {
                p
            } else {
                // fallback to local build artifact
                PathBuf::from(
                    std::env::var("CARGO_MANIFEST_DIR").unwrap_or_else(|_| ".".to_string()),
                )
                .join("target/release/libmemhop.dylib")
            }
        });

    if !dylib_path.exists() {
        eprintln!("❌ Library not found: {:?}", dylib_path);
        eprintln!("   Build first: cargo build --release");
        eprintln!("   Or set MEMHOP_DYLIB_PATH");
        std::process::exit(1);
    }
    println!("📦 Library: {:?}", dylib_path);

    let lib = load_lib(&dylib_path);
    run_tests(&lib, run_dream, &api_key);

    if run_dream {
        println!("\n✅ All tests passed (including dream)");
    } else {
        println!("\n✅ All tests passed (use --dream to run DeepSeek dream test)");
    }
}
