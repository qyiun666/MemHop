//! FFI Integration Tests — 通过 4 个 extern "C" 函数测试完整 API
//!
//! 测试策略：
//! - 所有调用走 FFI 层（extern "C"），验证 JSON-in JSON-out 协议
//! - 覆盖 11 个命令 + 4 个 C 函数 + 边界条件
//! - 模拟 Agent 接入流程（open → search → update → query → close）
//! - Dream 命令需设置 MEMHOP_LLM_API_KEY 环境变量（可选）
//! - 向量编码需要配置 gRPC 或 IPC 编码器（测试中使用 auto_create 跳过向量检索）

use std::ffi::{CStr, CString};
use std::ptr;

use memhop::ffi::{memhop_close, memhop_execute, memhop_free_string, memhop_open, MemHopHandle};

// ============================================================================
// 辅助函数
// ============================================================================

/// 调用 memhop_execute 并返回解析后的 serde_json::Value
unsafe fn exec(handle: *mut MemHopHandle, json: &str) -> serde_json::Value {
    let cmd = CString::new(json).unwrap();
    let res_ptr = memhop_execute(handle, cmd.as_ptr());
    assert!(!res_ptr.is_null(), "memhop_execute returned null");
    let res_str = CStr::from_ptr(res_ptr).to_str().unwrap().to_string();
    memhop_free_string(res_ptr);
    serde_json::from_str(&res_str).expect("response is not valid JSON")
}

/// 创建 CString 配置 JSON
fn config_json(db_path: &str) -> CString {
    CString::new(format!(r#"{{"db_path":"{}","vector_dim":384}}"#, db_path)).unwrap()
}

/// 断言响应 success=true
fn assert_success(res: &serde_json::Value) {
    assert!(
        res["success"].as_bool().unwrap_or(false),
        "expected success, got: {}",
        res
    );
}

/// 断言响应 success=false，并返回 error 消息
fn assert_error(res: &serde_json::Value) -> String {
    assert!(
        !res["success"].as_bool().unwrap_or(true),
        "expected error, got: {}",
        res
    );
    res["error"].as_str().unwrap_or("").to_string()
}

// ============================================================================
// 测试：4 个 C 函数边界条件
// ============================================================================

#[test]
fn test_ffi_open_null_config() {
    unsafe {
        let handle = memhop_open(ptr::null());
        assert!(handle.is_null(), "null config should return null handle");
    }
}

#[test]
fn test_ffi_open_invalid_json() {
    unsafe {
        let cfg = CString::new("not json").unwrap();
        let handle = memhop_open(cfg.as_ptr());
        assert!(handle.is_null(), "invalid JSON should return null handle");
    }
}

#[test]
fn test_ffi_open_invalid_config() {
    unsafe {
        let cfg = CString::new(r#"{"db_path":"","vector_dim":0}"#).unwrap();
        let handle = memhop_open(cfg.as_ptr());
        assert!(handle.is_null(), "invalid config should return null handle");
    }
}

#[test]
fn test_ffi_execute_null_handle() {
    unsafe {
        let res = exec(ptr::null_mut(), r#"{"command":"sync"}"#);
        assert_error(&res);
    }
}

#[test]
fn test_ffi_execute_null_command() {
    let _ = std::fs::remove_file("/tmp/memhop_ffi_null_cmd.meh");
    unsafe {
        let cfg = config_json("/tmp/memhop_ffi_null_cmd.meh");
        let handle = memhop_open(cfg.as_ptr());
        assert!(!handle.is_null(), "open failed");

        let res_ptr = memhop_execute(handle, ptr::null());
        assert!(!res_ptr.is_null(), "expected error response, not null");
        let res_str = CStr::from_ptr(res_ptr).to_str().unwrap().to_string();
        memhop_free_string(res_ptr);
        let res: serde_json::Value =
            serde_json::from_str(&res_str).expect("response is not valid JSON");
        assert_error(&res);

        memhop_close(handle);
    }
}

#[test]
fn test_ffi_execute_invalid_json() {
    let _ = std::fs::remove_file("/tmp/memhop_ffi_invalid_cmd.meh");
    unsafe {
        let cfg = config_json("/tmp/memhop_ffi_invalid_cmd.meh");
        let handle = memhop_open(cfg.as_ptr());
        assert!(!handle.is_null());
        let res = exec(handle, "not json");
        assert_error(&res);
        memhop_close(handle);
    }
}

#[test]
fn test_ffi_execute_invalid_command() {
    let _ = std::fs::remove_file("/tmp/memhop_ffi_unknown_cmd.meh");
    unsafe {
        let cfg = config_json("/tmp/memhop_ffi_unknown_cmd.meh");
        let handle = memhop_open(cfg.as_ptr());
        assert!(!handle.is_null());
        let res = exec(handle, r#"{"command":"nonexistent"}"#);
        assert_error(&res);
        memhop_close(handle);
    }
}

#[test]
fn test_ffi_free_string_null() {
    unsafe {
        // Calling memhop_free_string(null) should be a safe no-op
        memhop_free_string(ptr::null_mut());
    }
}

#[test]
fn test_ffi_close_null() {
    unsafe {
        // Calling memhop_close(null) should be a safe no-op
        memhop_close(ptr::null_mut());
    }
}

// ============================================================================
// 测试：完整生命周期（open → commands → close）
// ============================================================================

#[test]
fn test_ffi_full_lifecycle() {
    let db_path = "/tmp/memhop_ffi_lifecycle.meh";
    let _ = std::fs::remove_file(db_path);

    unsafe {
        // ---- 1. Open ----
        let cfg = config_json(db_path);
        let handle = memhop_open(cfg.as_ptr());
        assert!(!handle.is_null(), "memhop_open failed");

        // ---- 2. Search with auto_create ----
        let res = exec(
            handle,
            r#"{"command":"search","dialogue":"Rust programming","auto_create":1,"context_limit":5,"min_score":0.0}"#,
        );
        assert_success(&res);
        let contexts = res["data"]["contexts"].as_array().unwrap();
        assert!(!contexts.is_empty(), "auto_create should create L2");
        let l2_id = contexts[0]["id"].as_str().unwrap().to_string();

        // ---- 3. Update L2 with dialogue ----
        let update_cmd = format!(
            r#"{{"command":"update","topic_id":"{}","dialogue_text":"User: What is Rust?\nAssistant: Rust is a systems language.","action_chain":[{{"title":"answer","description":"explain rust","action_type":"Execute"}}]}}"#,
            l2_id
        );
        let res = exec(handle, &update_cmd);
        assert_success(&res);

        // ---- 4. Query L0 profile ----
        let res = exec(
            handle,
            r#"{"command":"query_layer","layer":"l0","action":"get","get":{},"list":{}}"#,
        );
        assert_success(&res);
        println!("  L0 profile: {:?}", res["data"]);

        // ---- 5. Query L2 topics ----
        let res = exec(
            handle,
            r#"{"command":"query_layer","layer":"l2","action":"list","list":{"page":1,"page_size":10}}"#,
        );
        assert_success(&res);
        let total_l2 = res["data"]["total"].as_u64().unwrap_or(0);
        assert!(total_l2 > 0, "should have L2 topics");

        // ---- 6. Query L1 engrams ----
        let res = exec(
            handle,
            r#"{"command":"query_layer","layer":"l1","action":"list","list":{"page":1,"page_size":10}}"#,
        );
        assert_success(&res);

        // ---- 6a. Query L1 get (single engram by ID) ----
        let engrams = res["data"]["items"].as_array().unwrap();
        if !engrams.is_empty() {
            let engram_id = engrams[0]["id"].as_str().unwrap();
            let get_cmd = format!(
                r#"{{"command":"query_layer","layer":"l1","action":"get","get":{{"id":"{}"}}, "list":{{}}}}"#,
                engram_id
            );
            let res = exec(handle, &get_cmd);
            assert_success(&res);
        }

        // ---- 7. Query L3 knowledge ----
        let res = exec(
            handle,
            r#"{"command":"query_layer","layer":"l3","action":"list","list":{"page":1,"page_size":10}}"#,
        );
        assert_success(&res);

        // ---- 7a. Query L3 get (single knowledge by ID) ----
        let knowledge_items = res["data"]["items"].as_array().unwrap();
        let mut knowledge_id: Option<String> = None;
        if !knowledge_items.is_empty() {
            let kid = knowledge_items[0]["id"].as_str().unwrap();
            knowledge_id = Some(kid.to_string());
            let get_cmd = format!(
                r#"{{"command":"query_layer","layer":"l3","action":"get","get":{{"id":"{}"}}, "list":{{}}}}"#,
                kid
            );
            let res = exec(handle, &get_cmd);
            assert_success(&res);
        }

        // ---- 8. Query L4 archives (generic) ----
        let res = exec(
            handle,
            r#"{"command":"query_layer","layer":"l4","action":"list","list":{"page":1,"page_size":10}}"#,
        );
        assert_success(&res);

        // ---- 8a. Query L4 archives by topic_id ----
        let list_by_topic = format!(
            r#"{{"command":"query_layer","layer":"l4","action":"list","list":{{"page":1,"page_size":10,"topic_id":"{}"}}}}"#,
            l2_id
        );
        let res = exec(handle, &list_by_topic);
        assert_success(&res);

        // ---- 9. Query L5 crystals ----
        let res = exec(
            handle,
            r#"{"command":"query_layer","layer":"l5","action":"list","list":{"page":1,"page_size":10}}"#,
        );
        assert_success(&res);

        // ---- 10. Update L0 profile ----
        let res = exec(
            handle,
            r#"{"command":"update_title","layer":"l0","params":{"name":"FFI Agent","role":"Test Assistant"}}"#,
        );
        assert_success(&res);

        // ---- 11. Update L2 title ----
        let update_title_cmd = format!(
            r#"{{"command":"update_title","layer":"l2","params":{{"id":"{}","new_title":"Updated Rust Topic"}}}}"#,
            l2_id
        );
        let res = exec(handle, &update_title_cmd);
        assert_success(&res);

        // ---- 12. Verify updated title ----
        let get_topic_cmd = format!(
            r#"{{"command":"query_layer","layer":"l2","action":"get","get":{{"id":"{}"}}, "list":{{}}}}"#,
            l2_id
        );
        let res = exec(handle, &get_topic_cmd);
        assert_success(&res);

        // ---- 12a. Update L3 title ----
        if let Some(kid) = &knowledge_id {
            let update_l3_cmd = format!(
                r#"{{"command":"update_title","layer":"l3","params":{{"id":"{}","new_title":"Updated Knowledge"}}}}"#,
                kid
            );
            let res = exec(handle, &update_l3_cmd);
            assert_success(&res);
        }

        // ---- 12b. Update L5 title (test error path: no crystals yet) ----
        let res = exec(
            handle,
            r#"{"command":"update_title","layer":"l5","params":{"id":"nonexistent","new_title":"test"}}"#,
        );
        // L5 update with nonexistent ID returns error - that's correct
        assert_error(&res);

        // ---- 13. Session management ----
        // activate
        let session_activate = format!(
            r#"{{"command":"session","params":{{"action":"activate","topic_id":"{}","ttl_ms":300000}}}}"#,
            l2_id
        );
        let res = exec(handle, &session_activate);
        assert_success(&res);

        // list active
        let res = exec(
            handle,
            r#"{"command":"session","params":{"action":"list"}}"#,
        );
        assert_success(&res);
        let active = res["data"]["active_topics"].as_array().unwrap();
        assert!(!active.is_empty(), "should have active topics");

        // adjust activation
        let adjust_cmd = format!(
            r#"{{"command":"session","params":{{"action":"adjust","topic_id":"{}","delta":0.5}}}}"#,
            l2_id
        );
        let res = exec(handle, &adjust_cmd);
        assert_success(&res);

        // deactivate
        let deactivate_cmd = format!(
            r#"{{"command":"session","params":{{"action":"deactivate","topic_id":"{}"}}}}"#,
            l2_id
        );
        let res = exec(handle, &deactivate_cmd);
        assert_success(&res);

        // ---- 14. Import L0 profile ----
        let res = exec(
            handle,
            r#"{"command":"import","params":{"action":"import","target_layer":"profile","mode":"merge","data":{"Profile":{"name":"Imported Agent","role":"Tester"}}}}"#,
        );
        assert_success(&res);

        // ---- 15. Import L2 topics ----
        let res = exec(
            handle,
            r#"{"command":"import","params":{"action":"import","target_layer":"topic","mode":"merge","data":{"Topics":[{"title":"Python Basics","summary":"Learning Python","keywords":["python"]}]}}}"#,
        );
        assert_success(&res);

        // ---- 16. Import L3 knowledge ----
        let res = exec(
            handle,
            r#"{"command":"import","params":{"action":"import","target_layer":"knowledge","mode":"merge","data":{"Knowledge":[{"title":"Rust Ownership","domain":"programming","knowledge_type":"Conceptual","text":"Rust ownership system...","keywords":["rust","ownership"]}]}}}"#,
        );
        assert_success(&res);

        // ---- 16a. Import build_l3 from path ----
        let res = exec(
            handle,
            r#"{"command":"import","params":{"action":"build_l3","path":"/tmp"}}"#,
        );
        // build_l3 may succeed or fail depending on files - just check it runs
        println!(
            "  build_l3 result: success={}",
            res["success"].as_bool().unwrap_or(false)
        );

        // ---- 17. Batch store (fails without encoder, expected behavior) ----
        let res = exec(
            handle,
            r#"{"command":"batch_store","items":[{"text":"test memory","topic_label":"test","domain_id":"test","importance":0.5,"source":{"source_type":"UserInput","source_id":null,"timestamp":0},"is_structural":false}],"session_id":"s1","turn_id":"t1"}"#,
        );
        // Without a real gRPC encoder, batch_store returns an error (no degradation)
        let _ = res; // Accept both success (if encoder available) and failure

        // ---- 18. Sync ----
        let res = exec(handle, r#"{"command":"sync"}"#);
        assert_success(&res);

        // ---- 19. Close ----
        let res = exec(handle, r#"{"command":"close"}"#);
        assert_success(&res);

        // ---- 20. Proper close (free handle) ----
        memhop_close(handle);

        // ---- 21. Verify data persists by reopening ----
        let handle2 = memhop_open(cfg.as_ptr());
        assert!(!handle2.is_null(), "reopen failed");

        let res = exec(
            handle2,
            r#"{"command":"query_layer","layer":"l2","action":"list","list":{"page":1,"page_size":100}}"#,
        );
        assert_success(&res);
        let total = res["data"]["total"].as_u64().unwrap_or(0);
        assert!(total > 0, "L2 topics should persist after close/reopen");

        let res = exec(
            handle2,
            r#"{"command":"query_layer","layer":"l0","action":"get","get":{},"list":{}}"#,
        );
        assert_success(&res);
        assert!(
            res["data"]["name"].as_str().is_some(),
            "profile should persist"
        );

        memhop_close(handle2);
        let _ = std::fs::remove_file(db_path);
    }
}

// ============================================================================
// 测试：Graph query 与 Delete 命令（L2/L3/L5）
// ============================================================================

#[test]
fn test_ffi_graph_query_and_delete() {
    let db_path = "/tmp/memhop_ffi_graph_delete.meh";
    let source_path = "/tmp/memhop_test_graph";
    let _ = std::fs::remove_file(db_path);
    let _ = std::fs::remove_dir_all(source_path);

    // Prepare a minimal Rust codebase so build_l3 creates nodes and edges.
    std::fs::create_dir_all(format!("{}/src", source_path)).unwrap();
    std::fs::write(
        format!("{}/src/a.rs", source_path),
        "use crate::b;\npub fn foo() {}\n",
    )
    .unwrap();
    std::fs::write(
        format!("{}/src/b.rs", source_path),
        "pub fn bar() {}\n",
    )
    .unwrap();

    unsafe {
        let cfg = config_json(db_path);
        let handle = memhop_open(cfg.as_ptr());
        assert!(!handle.is_null(), "memhop_open failed");

        // ---- 1. Build L3 hypergraph ----
        let build_cmd = format!(
            r#"{{"command":"import","params":{{"action":"build_l3","path":"{}"}}}}"#,
            source_path
        );
        let res = exec(handle, &build_cmd);
        assert_success(&res);
        let created_ids = res["data"]["created_ids"].as_array().unwrap();
        assert!(
            created_ids.len() >= 2,
            "build_l3 should create at least two nodes"
        );
        let start_node = created_ids[0].as_str().unwrap().to_string();

        // ---- 2. Query L3 list to obtain graph_id ----
        let res = exec(
            handle,
            r#"{"command":"query_layer","layer":"l3","action":"list","list":{"page":1,"page_size":10}}"#,
        );
        assert_success(&res);
        let l3_items = res["data"]["items"].as_array().unwrap();
        assert!(!l3_items.is_empty(), "L3 should contain the built graph");
        let graph_id = l3_items[0]["id"].as_str().unwrap().to_string();

        // ---- 3. Graph query with Dependency edges ----
        let graph_query_cmd = format!(
            r#"{{"command":"graph_query","graph_id":"{}","start_node":"{}","max_depth":2,"edge_kinds":["Dependency"]}}"#,
            graph_id, start_node
        );
        let res = exec(handle, &graph_query_cmd);
        assert_success(&res);
        let nodes = res["data"]["nodes"].as_array().unwrap();
        let edges = res["data"]["edges"].as_array().unwrap();
        let hops = res["data"]["hops"].as_array().unwrap();
        assert!(
            nodes.len() >= 2,
            "graph_query should return at least 2 nodes, got {}",
            nodes.len()
        );
        assert!(
            !edges.is_empty() || hops.is_empty(),
            "either edges exist or no hops were made"
        );

        // ---- 4. Delete L3 graph ----
        let delete_l3_cmd = format!(
            r#"{{"command":"delete","layer":"l3","id":"{}"}}"#,
            graph_id
        );
        let res = exec(handle, &delete_l3_cmd);
        assert_success(&res);
        assert!(res["data"]["deleted"].as_bool().unwrap_or(false));

        // Verify the graph is gone.
        let res = exec(
            handle,
            r#"{"command":"query_layer","layer":"l3","action":"list","list":{"page":1,"page_size":10}}"#,
        );
        assert_success(&res);
        let l3_total = res["data"]["total"].as_u64().unwrap_or(0);
        assert_eq!(l3_total, 0, "L3 graph should be deleted");

        // ---- 5. Create an L2 topic and L5 action chain ----
        let res = exec(
            handle,
            r#"{"command":"search","dialogue":"Action chain test topic","auto_create":1,"context_limit":5,"min_score":0.0}"#,
        );
        assert_success(&res);
        let l2_id = res["data"]["contexts"][0]["id"]
            .as_str()
            .unwrap()
            .to_string();

        let update_cmd = format!(
            r#"{{"command":"update","topic_id":"{}","dialogue_text":"User: Do something.\nAssistant: Done.","action_chain":[{{"title":"do_something","description":"perform an action","action_type":"Execute"}}]}}"#,
            l2_id
        );
        let res = exec(handle, &update_cmd);
        assert_success(&res);

        // ---- 6. List L5 crystals and delete the action chain ----
        let res = exec(
            handle,
            r#"{"command":"query_layer","layer":"l5","action":"list","list":{"page":1,"page_size":10}}"#,
        );
        assert_success(&res);
        let l5_items = res["data"]["items"].as_array().unwrap();
        assert!(!l5_items.is_empty(), "L5 should contain the action chain");
        let chain_id = l5_items[0]["id"].as_str().unwrap().to_string();

        let delete_l5_cmd = format!(
            r#"{{"command":"delete","layer":"l5","id":"{}"}}"#,
            chain_id
        );
        let res = exec(handle, &delete_l5_cmd);
        assert_success(&res);

        let res = exec(
            handle,
            r#"{"command":"query_layer","layer":"l5","action":"list","list":{"page":1,"page_size":10}}"#,
        );
        assert_success(&res);
        let l5_total = res["data"]["total"].as_u64().unwrap_or(0);
        assert_eq!(l5_total, 0, "L5 action chain should be deleted");

        // ---- 7. Delete L2 topic ----
        let delete_l2_cmd = format!(
            r#"{{"command":"delete","layer":"l2","id":"{}"}}"#,
            l2_id
        );
        let res = exec(handle, &delete_l2_cmd);
        assert_success(&res);

        let get_l2_cmd = format!(
            r#"{{"command":"query_layer","layer":"l2","action":"get","get":{{"id":"{}"}},"list":{{}}}}"#,
            l2_id
        );
        let res = exec(handle, &get_l2_cmd);
        assert!(
            !res["success"].as_bool().unwrap_or(true) || res["data"].is_null(),
            "deleted L2 topic should not be retrievable"
        );

        // ---- 8. Unsupported delete layer returns error ----
        let res = exec(
            handle,
            r#"{"command":"delete","layer":"l1","id":"0000000000000001"}"#,
        );
        assert_error(&res);

        memhop_close(handle);
        let _ = std::fs::remove_file(db_path);
    }
    let _ = std::fs::remove_dir_all(source_path);
}

// ============================================================================
// 测试：查询 L3 详情
// ============================================================================

#[test]
fn test_ffi_query_l3_detail() {
    let db_path = "/tmp/memhop_ffi_l3_detail.meh";
    let source_path = "/Volumes/zt_hd/projects/meow/meowagent/src";
    let _ = std::fs::remove_file(db_path);

    if !std::path::Path::new(source_path).exists() {
        eprintln!("[SKIP] meowagent source not found");
        return;
    }

    unsafe {
        let cfg = config_json(db_path);
        let handle = memhop_open(cfg.as_ptr());
        assert!(!handle.is_null());

        // 1. Build L3
        let build_cmd = format!(
            r#"{{"command":"import","params":{{"action":"build_l3","path":"{}"}}}}"#,
            source_path
        );
        let res = exec(handle, &build_cmd);
        assert_success(&res);

        // 2. Query L3 list
        println!("\n===== L3 LIST =====");
        let res = exec(
            handle,
            r#"{"command":"query_layer","layer":"l3","action":"list","list":{"page":1,"page_size":20}}"#,
        );
        assert_success(&res);
        println!("{}", serde_json::to_string_pretty(&res["data"]).unwrap());

        // 3. Get L3 detail (with all nodes)
        let l3_items = res["data"]["items"].as_array().unwrap();
        if !l3_items.is_empty() {
            let l3_id = l3_items[0]["id"].as_str().unwrap();
            println!("\n===== L3 DETAIL (id={}) =====", l3_id);
            let get_cmd = format!(
                r#"{{"command":"query_layer","layer":"l3","action":"get","get":{{"id":"{}"}},"list":{{}}}}"#,
                l3_id
            );
            let res = exec(handle, &get_cmd);
            assert_success(&res);

            // Print detail fields (truncate long text)
            let data = &res["data"];
            println!("title: {}", data["title"].as_str().unwrap_or("?"));
            println!("domain: {}", data["domain"].as_str().unwrap_or("?"));
            println!(
                "knowledge_type: {}",
                data["knowledge_type"].as_str().unwrap_or("?")
            );
            println!("importance: {}", data["importance"].as_f64().unwrap_or(0.0));
            println!("source_ref: {}", data["source_ref"].as_str().unwrap_or("?"));

            let keywords = data["keywords"].as_array().unwrap();
            println!("keywords ({}):", keywords.len());
            for kw in keywords.iter().take(30) {
                println!("  - {}", kw.as_str().unwrap_or("?"));
            }

            let text = data["text"].as_str().unwrap_or("");
            let preview: String = text.chars().take(500).collect();
            println!("\ntext preview ({} chars total):", text.len());
            println!("{}", preview);
        }

        // 4. Query L2 detail to see l3_refs
        println!("\n===== L2 DETAIL =====");
        let res = exec(
            handle,
            r#"{"command":"query_layer","layer":"l2","action":"list","list":{"page":1,"page_size":5}}"#,
        );
        assert_success(&res);
        let l2_items = res["data"]["items"].as_array().unwrap();
        if !l2_items.is_empty() {
            let l2_id = l2_items[0]["id"].as_str().unwrap();
            let get_cmd = format!(
                r#"{{"command":"query_layer","layer":"l2","action":"get","get":{{"id":"{}"}},"list":{{}}}}"#,
                l2_id
            );
            let res = exec(handle, &get_cmd);
            assert_success(&res);
            println!("{}", serde_json::to_string_pretty(&res["data"]).unwrap());
        }

        memhop_close(handle);
        let _ = std::fs::remove_file(db_path);
    }
}

// ============================================================================
// 测试：Merge Topics
// ============================================================================

#[test]
fn test_ffi_merge_topics() {
    let db_path = "/tmp/memhop_ffi_merge.meh";
    let _ = std::fs::remove_file(db_path);

    unsafe {
        let cfg = config_json(db_path);
        let handle = memhop_open(cfg.as_ptr());
        assert!(!handle.is_null());

        // Create two L2s via auto_create
        let res = exec(
            handle,
            r#"{"command":"search","dialogue":"Topic Alpha","auto_create":1,"context_limit":5,"min_score":0.0}"#,
        );
        assert_success(&res);
        let id1 = res["data"]["contexts"][0]["id"]
            .as_str()
            .unwrap()
            .to_string();

        let res = exec(
            handle,
            r#"{"command":"search","dialogue":"Topic Beta","auto_create":1,"context_limit":5,"min_score":0.0}"#,
        );
        assert_success(&res);
        let id2 = res["data"]["contexts"][0]["id"]
            .as_str()
            .unwrap()
            .to_string();

        // Merge them
        let merge_cmd = format!(
            r#"{{"command":"merge_topics","primary_id":"{}","secondary_ids":["{}"]}}"#,
            id1, id2
        );
        let res = exec(handle, &merge_cmd);
        assert_success(&res);

        // Verify secondary is gone
        let get_cmd = format!(
            r#"{{"command":"query_layer","layer":"l2","action":"get","get":{{"id":"{}"}},"list":{{}}}}"#,
            id2
        );
        let res = exec(handle, &get_cmd);
        // After merge, secondary topic detail should return null/error
        assert!(
            res["data"].is_null() || !res["success"].as_bool().unwrap_or(false),
            "secondary topic should be deleted after merge"
        );

        memhop_close(handle);
        let _ = std::fs::remove_file(db_path);
    }
}

// ============================================================================
// 测试：错误处理全面覆盖
// ============================================================================

#[test]
fn test_ffi_error_handling() {
    let db_path = "/tmp/memhop_ffi_errors.meh";
    let _ = std::fs::remove_file(db_path);

    unsafe {
        let cfg = config_json(db_path);
        let handle = memhop_open(cfg.as_ptr());
        assert!(!handle.is_null());

        // missing field
        let res = exec(handle, r#"{"command":"search"}"#);
        assert_error(&res);

        // unknown import action
        let res = exec(
            handle,
            r#"{"command":"import","params":{"action":"unknown_action"}}"#,
        );
        let msg = assert_error(&res);
        assert!(
            msg.contains("unknown import action"),
            "unexpected msg: {}",
            msg
        );

        // query_layer with unsupported combination
        let res = exec(
            handle,
            r#"{"command":"query_layer","layer":"l4","action":"get","get":{},"list":{}}"#,
        );
        assert_error(&res);

        // update_title with unknown layer
        let res = exec(
            handle,
            r#"{"command":"update_title","layer":"l1","params":{}}"#,
        );
        assert_error(&res);

        // session with unknown action
        let res = exec(
            handle,
            r#"{"command":"session","params":{"action":"unknown"}}"#,
        );
        assert_error(&res);

        // session activate without topic_id
        let res = exec(
            handle,
            r#"{"command":"session","params":{"action":"activate"}}"#,
        );
        assert_error(&res);

        memhop_close(handle);
        let _ = std::fs::remove_file(db_path);
    }
}

// ============================================================================
// 测试：模拟 Agent 接入流程
// ============================================================================

#[test]
fn test_ffi_agent_workflow() {
    let db_path = "/tmp/memhop_ffi_agent.meh";
    let _ = std::fs::remove_file(db_path);

    unsafe {
        // Agent 1: 打开数据库
        let cfg = config_json(db_path);
        let handle = memhop_open(cfg.as_ptr());
        assert!(!handle.is_null(), "Agent: failed to open database");
        println!("[Agent] Database opened");

        // Agent 2: 设置自己的画像
        let res = exec(
            handle,
            r#"{"command":"update_title","layer":"l0","params":{"name":"Coding Agent","role":"Rust Programming Assistant","personality":"Helpful and precise"}}"#,
        );
        assert_success(&res);
        println!("[Agent] Profile set");

        // Agent 3: 用户提问，检索记忆
        let res = exec(
            handle,
            r#"{"command":"search","dialogue":"How do I fix a borrow checker error in Rust?","auto_create":1,"context_limit":5,"min_score":0.0}"#,
        );
        assert_success(&res);
        let contexts = res["data"]["contexts"].as_array().unwrap();
        assert!(!contexts.is_empty());
        let topic_id = contexts[0]["id"].as_str().unwrap().to_string();
        println!("[Agent] Search complete, active topic: {}", topic_id);

        // Agent 4: 激活会话
        let activate_cmd = format!(
            r#"{{"command":"session","params":{{"action":"activate","topic_id":"{}","ttl_ms":600000}}}}"#,
            topic_id
        );
        let res = exec(handle, &activate_cmd);
        assert_success(&res);
        println!("[Agent] Session activated");

        // Agent 5: 写入对话
        let update_cmd = format!(
            r#"{{"command":"update","topic_id":"{}","dialogue_text":"User: How do I fix borrow checker error?\nAssistant: The borrow checker ensures memory safety. Use & instead of &mut when you don't need mutation.","summary":"borrow checker explanation","action_chain":[{{"title":"explain_borrow_checker","description":"explain how to fix borrow checker error","action_type":"Execute"}},{{"title":"provide_example","description":"show code example","action_type":"Create"}}]}}"#,
            topic_id
        );
        let res = exec(handle, &update_cmd);
        assert_success(&res);
        println!("[Agent] Memory updated");

        // Agent 6: 验证写入的对话
        let res = exec(
            handle,
            r#"{"command":"query_layer","layer":"l4","action":"list","list":{"page":1,"page_size":10}}"#,
        );
        assert_success(&res);
        println!("[Agent] Archives verified");

        // Agent 7: 同步到磁盘
        let res = exec(handle, r#"{"command":"sync"}"#);
        assert_success(&res);
        println!("[Agent] Synced to disk");

        // Agent 8: 关闭
        let res = exec(handle, r#"{"command":"close"}"#);
        assert_success(&res);
        memhop_close(handle);
        println!("[Agent] Database closed");

        let _ = std::fs::remove_file(db_path);
    }
}

// ============================================================================
// 测试：Dream（记忆整合）— 需要 LLM API 环境变量
// ============================================================================

#[test]
#[ignore = "requires MEMHOP_LLM_API_KEY env var and network access"]
fn test_ffi_dream_with_llm() {
    let api_key = std::env::var("MEMHOP_LLM_API_KEY")
        .expect("MEMHOP_LLM_API_KEY must be set");

    let db_path = "/tmp/memhop_ffi_dream.meh";
    let _ = std::fs::remove_file(db_path);

    unsafe {
        let cfg = config_json(db_path);
        let handle = memhop_open(cfg.as_ptr());
        assert!(!handle.is_null());

        // 1. Create some memory first
        let res = exec(
            handle,
            r#"{"command":"search","dialogue":"Learning about Rust memory management","auto_create":1,"context_limit":5,"min_score":0.0}"#,
        );
        assert_success(&res);
        let contexts = res["data"]["contexts"].as_array().unwrap();
        let topic_id = contexts[0]["id"].as_str().unwrap().to_string();

        // 2. Add some content
        let update_cmd = format!(
            r#"{{"command":"update","topic_id":"{}","dialogue_text":"User: Explain Rust ownership.\nAssistant: Ownership is Rust's core memory management system.","summary":"ownership explanation","action_chain":[{{"title":"explain_ownership","description":"explain Rust ownership","action_type":"Execute"}}]}}"#,
            topic_id
        );
        let res = exec(handle, &update_cmd);
        assert_success(&res);

        // 3. Activate the topic
        let activate_cmd = format!(
            r#"{{"command":"session","params":{{"action":"activate","topic_id":"{}","ttl_ms":600000}}}}"#,
            topic_id
        );
        let res = exec(handle, &activate_cmd);
        assert_success(&res);

        // 4. Run dream with configured LLM
        let api_url = std::env::var("MEMHOP_LLM_API_URL")
            .unwrap_or_else(|_| "https://api.openai.com/v1/chat/completions".to_string());
        let model = std::env::var("MEMHOP_LLM_MODEL")
            .unwrap_or_else(|_| "gpt-4o-mini".to_string());
        let dream_cmd = format!(
            r#"{{"command":"dream","api_url":"{}","api_key":"{}","model":"{}"}}"#,
            api_url, api_key, model
        );
        println!("[Dream] Calling LLM API...");
        let res = exec(handle, &dream_cmd);
        assert_success(&res);
        println!("[Dream] Complete: {:?}", res["data"]);

        memhop_close(handle);
        let _ = std::fs::remove_file(db_path);
    }
}

// ============================================================================
// 测试：从文件路径导入 L3 超图并通过 L2 检索
// ============================================================================

#[test]
fn test_ffi_build_l3_from_meowagent() {
    let db_path = "/tmp/memhop_ffi_l3_meowagent.meh";
    let source_path = "/Volumes/zt_hd/projects/meow/meowagent/src";
    let _ = std::fs::remove_file(db_path);

    // Skip if meowagent source not available
    if !std::path::Path::new(source_path).exists() {
        eprintln!("[SKIP] meowagent source not found at {}", source_path);
        return;
    }

    unsafe {
        // 1. Open database
        let cfg = config_json(db_path);
        let handle = memhop_open(cfg.as_ptr());
        assert!(!handle.is_null(), "memhop_open failed");
        println!("[L3 Import] Database opened");

        // 2. Build L3 from meowagent/src
        let build_cmd = format!(
            r#"{{"command":"import","params":{{"action":"build_l3","path":"{}"}}}}"#,
            source_path
        );
        let res = exec(handle, &build_cmd);
        println!(
            "[L3 Import] build_l3 result: {}",
            serde_json::to_string_pretty(&res).unwrap()
        );
        assert_success(&res);

        let created_ids = res["data"]["created_ids"].as_array().unwrap();
        let updated_ids = res["data"]["updated_ids"].as_array().unwrap();
        println!(
            "[L3 Import] Created {} nodes, {} edges",
            created_ids.len(),
            updated_ids.len()
        );
        assert!(
            !created_ids.is_empty(),
            "build_l3 should create at least some nodes"
        );

        // 3. Query L3 list to verify nodes
        let res = exec(
            handle,
            r#"{"command":"query_layer","layer":"l3","action":"list","list":{"page":1,"page_size":20}}"#,
        );
        assert_success(&res);
        let l3_total = res["data"]["total"].as_u64().unwrap_or(0);
        println!("[L3 Query] Total L3 items: {}", l3_total);
        assert!(l3_total > 0, "L3 should have nodes after build_l3");

        // Print first few L3 node titles
        let l3_items = res["data"]["items"].as_array().unwrap();
        for item in l3_items.iter().take(5) {
            println!(
                "  L3: {} (type={}, importance={})",
                item["title"].as_str().unwrap_or("?"),
                item["node_type"].as_str().unwrap_or("?"),
                item["importance"].as_f64().unwrap_or(0.0)
            );
        }

        // 4. Query L2 list to find the auto-created topic
        let res = exec(
            handle,
            r#"{"command":"query_layer","layer":"l2","action":"list","list":{"page":1,"page_size":10}}"#,
        );
        assert_success(&res);
        let l2_total = res["data"]["total"].as_u64().unwrap_or(0);
        println!("[L2 Query] Total L2 topics: {}", l2_total);
        assert!(l2_total > 0, "build_l3 should create an L2 topic");

        let l2_items = res["data"]["items"].as_array().unwrap();
        let l2_id = l2_items[0]["id"].as_str().unwrap().to_string();
        let l2_title = l2_items[0]["title"].as_str().unwrap_or("?").to_string();
        println!("  L2: '{}' (id={})", l2_title, l2_id);

        // 5. Get L2 topic detail to verify L3 linkage (TopicDetail has l3_refs)
        let get_cmd = format!(
            r#"{{"command":"query_layer","layer":"l2","action":"get","get":{{"id":"{}"}}, "list":{{}}}}"#,
            l2_id
        );
        let res = exec(handle, &get_cmd);
        assert_success(&res);
        let detail_l3_refs = res["data"]["l3_refs"].as_array().unwrap();
        println!(
            "[L2 Detail] title='{}', l3_refs={:?}",
            res["data"]["title"].as_str().unwrap_or("?"),
            detail_l3_refs
        );
        assert!(
            !detail_l3_refs.is_empty(),
            "L2 detail should include l3_refs"
        );

        // 6. Search via context_id (doesn't need encoder) to verify L3 discovery
        let search_cmd = format!(
            r#"{{"command":"search","dialogue":"meowagent code","context_id":"{}","context_limit":5,"min_score":0.0,"auto_create":0}}"#,
            l2_id
        );
        let res = exec(handle, &search_cmd);
        println!(
            "[Search context_id] result: {}",
            serde_json::to_string_pretty(&res).unwrap()
        );
        assert_success(&res);

        let contexts = res["data"]["contexts"].as_array().unwrap();
        assert!(!contexts.is_empty(), "search should return the L2 context");

        let l3_ids = res["data"]["l3_ids"].as_array().unwrap();
        println!("[Search] Discovered L3 IDs: {:?}", l3_ids);
        assert!(
            !l3_ids.is_empty(),
            "Search via L2 should discover L3 IDs from l3_refs"
        );

        // 7. Sync and close
        let res = exec(handle, r#"{"command":"sync"}"#);
        assert_success(&res);

        let res = exec(handle, r#"{"command":"close"}"#);
        assert_success(&res);
        memhop_close(handle);

        // 8. Reopen and verify persistence
        let handle2 = memhop_open(cfg.as_ptr());
        assert!(!handle2.is_null(), "reopen failed");

        let res = exec(
            handle2,
            r#"{"command":"query_layer","layer":"l3","action":"list","list":{"page":1,"page_size":5}}"#,
        );
        assert_success(&res);
        let persisted_l3 = res["data"]["total"].as_u64().unwrap_or(0);
        println!("[Persistence] L3 nodes after reopen: {}", persisted_l3);
        assert!(
            persisted_l3 > 0,
            "L3 nodes should persist after close/reopen"
        );

        let res = exec(
            handle2,
            r#"{"command":"query_layer","layer":"l2","action":"list","list":{"page":1,"page_size":5}}"#,
        );
        assert_success(&res);
        let persisted_l2 = res["data"]["total"].as_u64().unwrap_or(0);
        println!("[Persistence] L2 topics after reopen: {}", persisted_l2);
        assert!(persisted_l2 > 0, "L2 topics should persist");

        memhop_close(handle2);
        let _ = std::fs::remove_file(db_path);
    }
}
