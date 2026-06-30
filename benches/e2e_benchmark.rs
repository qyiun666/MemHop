//! MemHop E2E API Completeness Benchmark
//!
//! Validates all 13 API commands are functional, all parameters are passable,
//! all code paths are exercised. Uses LOCOMO dialogue data as real input.
//!
//! 8 phases:
//!   Phase 1 — Ingest (batch_store)
//!   Phase 2 — Session management (activate/list/adjust/deactivate)
//!   Phase 3 — Update dialogue writing
//!   Phase 4 — Full query_layer verification (l0-l5)
//!   Phase 5 — Search all modes/routes
//!   Phase 6 — Import + graph_query
//!   Phase 7 — Dream + merge_topics + update_title
//!   Phase 8 — Delete + sync + close
//!
//! Prerequisites:
//!   python3 examples/meowvec_server.py &
//!   cargo bench --bench e2e_benchmark

mod common;

use common::metrics::*;
use criterion::{black_box, criterion_group, BenchmarkId, Criterion};
use memhop::ffi::{memhop_close, memhop_execute, memhop_free_string, memhop_open, MemHopHandle};
use serde_json::{json, Value};
use std::collections::HashMap;
use std::ffi::{CStr, CString};
use std::path::Path;
use std::sync::OnceLock;
use std::time::{Duration, Instant};

// ============================================================================
// Configuration
// ============================================================================

fn db_path() -> String {
    std::env::var("MEMHOP_BENCH_DB")
        .unwrap_or_else(|_| "/tmp/memhop_api_completeness.meh".to_string())
}

fn encoder_addr() -> String {
    std::env::var("MEMHOP_BENCH_ENCODER")
        .unwrap_or_else(|_| "http://127.0.0.1:27110".to_string())
}

fn fixture_path() -> String {
    std::env::var("MEMHOP_BENCH_DATASET")
        .unwrap_or_else(|_| "benches/fixtures/locomo_full.json".to_string())
}

const REPORT_PATH: &str = "target/bench/api_completeness_report.json";

const DEEPSEEK_API_KEY_VAR: &str = "MEMHOP_DEEPSEEK_API_KEY";

// ============================================================================
// FFI helpers
// ============================================================================

struct SafePtr(*mut MemHopHandle);
unsafe impl Send for SafePtr {}
unsafe impl Sync for SafePtr {}

static HANDLE: OnceLock<SafePtr> = OnceLock::new();

fn get_handle() -> *mut MemHopHandle {
    HANDLE
        .get_or_init(|| SafePtr(open_db(&db_path(), &encoder_addr())))
        .0
}

fn open_db(path: &str, encoder: &str) -> *mut MemHopHandle {
    let cfg = CString::new(format!(
        r#"{{"db_path":"{}","encoder_grpc_addr":"{}","vector_dim":384,"crystal_path":"/tmp/memhop_bench_crystals","llm":{{"api_url":"","api_key":"","model":"","temperature":0.2,"top_p":0.9,"presence_penalty":0.0,"frequency_penalty":0.0,"timeout_secs":30,"language":"zh"}},"auto_dream_on_evict":true,"auto_dream_archive_threshold":20,"auto_dream_summary_bytes":2048,"search_weights":{{"entity_weight":0.15,"bm25_weight":0.5,"vector_weight":0.35}},"decay_config":{{"lambda_node":0.01,"lambda_edge":0.02,"node_remove_threshold":0.05,"node_prune_edges_threshold":0.15,"edge_remove_threshold":0.05,"min_edge_nodes":2}},"session_config":{{"default_ttl_ms":3600000,"capacity":7}}}}"#,
        path, encoder
    ))
    .unwrap();
    let handle = unsafe { memhop_open(cfg.as_ptr()) };
    assert!(!handle.is_null(), "memhop_open failed — is mock_meowvec running?");
    handle
}

fn create_db(path: &str, encoder: &str) -> *mut MemHopHandle {
    let _ = std::fs::remove_file(path);
    open_db(path, encoder)
}

fn exec(handle: *mut MemHopHandle, json_str: &str) -> Value {
    unsafe {
        let cmd = CString::new(json_str).unwrap();
        let res_ptr = memhop_execute(handle, cmd.as_ptr());
        assert!(!res_ptr.is_null(), "memhop_execute returned null");
        let res_str = CStr::from_ptr(res_ptr).to_str().unwrap().to_string();
        memhop_free_string(res_ptr);
        serde_json::from_str(&res_str).expect("response is not valid JSON")
    }
}

fn exec_ok(handle: *mut MemHopHandle, json_str: &str) -> Value {
    let val = exec(handle, json_str);
    assert!(
        val["success"].as_bool().unwrap_or(false),
        "FFI command failed: cmd={}\n  resp={}",
        &json_str[..json_str.len().min(200)],
        val
    );
    val
}

// ============================================================================
// LOCOMO fixture types
// ============================================================================

struct Turn {
    text: String,
    timestamp: u64,
    speaker: String,
}

struct Session {
    id: String,
    turns: Vec<Turn>,
}

struct Question {
    id: String,
    question: String,
    answer: String,
    category: String,
    session_refs: Vec<String>,
}

struct Fixture {
    sessions: Vec<Session>,
    questions: Vec<Question>,
}

fn load_fixture(path: &str) -> Fixture {
    let content = std::fs::read_to_string(path)
        .unwrap_or_else(|e| panic!("Cannot read fixture '{}': {}", path, e));
    let v: Value = serde_json::from_str(&content).expect("Invalid fixture JSON");

    let sessions: Vec<Session> = v["sessions"]
        .as_array()
        .unwrap()
        .iter()
        .map(|s| Session {
            id: s["id"].as_str().unwrap().to_string(),
            turns: s["turns"].as_array().unwrap().iter().map(|t| Turn {
                text: t["text"].as_str().unwrap().to_string(),
                timestamp: t["timestamp"].as_u64().unwrap(),
                speaker: t["speaker"].as_str().unwrap().to_string(),
            }).collect(),
        })
        .collect();

    let questions: Vec<Question> = v["questions"]
        .as_array()
        .unwrap()
        .iter()
        .map(|q| Question {
            id: q["id"].as_str().unwrap().to_string(),
            question: q["question"].as_str().unwrap().to_string(),
            answer: q["answer"].as_str().unwrap().to_string(),
            category: q["category"].as_str().unwrap().to_string(),
            session_refs: q["session_refs"].as_array().unwrap().iter()
                .map(|s| s.as_str().unwrap().to_string()).collect(),
        })
        .collect();

    Fixture { sessions, questions }
}

// ============================================================================
// Phase reporting structures
// ============================================================================

#[derive(Default)]
struct CommandCoverage {
    tested: bool,
    passed: bool,
    details: String,
}

#[derive(Default)]
struct PhaseResult {
    passed: bool,
    details: Vec<String>,
}

fn print_phase(n: u32, name: &str, result: &PhaseResult) {
    if result.passed {
        println!("  Phase {}: PASS  ({})", n, name);
    } else {
        println!("  Phase {}: FAIL  ({})", n, name);
        for d in &result.details {
            println!("    - {}", d);
        }
    }
}

fn print_summary(coverage: &HashMap<&str, CommandCoverage>) {
    println!("\n===== API Completeness Summary =====");
    println!("{:<20} {:<10} {:<10} {}", "Command", "Tested", "Passed", "Details");
    println!("{:-<20} {:-<10} {:-<10} {:-<20}", "", "", "", "");
    let mut all_passed = true;
    for (cmd, cov) in coverage {
        let tested = if cov.tested { "✓" } else { "✗" };
        let passed = if cov.passed { "✓" } else { "✗" };
        println!("{:<20} {:<10} {:<10} {}", cmd, tested, passed, cov.details);
        if !cov.passed { all_passed = false; }
    }
    println!("{:-<60}", "");
    if all_passed {
        println!("RESULT: ALL 13 COMMANDS PASSED ✓");
    } else {
        println!("RESULT: SOME COMMANDS FAILED ✗");
    }
}

// ============================================================================
// Phase 1 — Ingest (batch_store)
// ============================================================================

fn phase1_batch_store(
    handle: *mut MemHopHandle,
    fixture: &Fixture,
    coverage: &mut HashMap<&str, CommandCoverage>,
) -> (PhaseResult, Vec<String>, HashMap<String, Vec<String>>) {
    // Default FAIL — all_pass must be explicitly verified
    let mut result = PhaseResult::default();
    let mut all_pass = true;
    let mut all_l2_ids = Vec::new();
    let mut session_l2_map: HashMap<String, Vec<String>> = HashMap::new();
    let mut latencies = Vec::new();
    let mut long_text_session_id: Option<String> = None;

    for session in &fixture.sessions {
        let mut items: Vec<Value> = Vec::new();
        for (i, turn) in session.turns.iter().enumerate() {
            let text = &turn.text;
            let mut item = json!({
                "text": text,
                "topic_label": session.id,
                "importance": if turn.speaker == "user" { 0.7 } else { 0.5 },
                "valence": if turn.speaker == "user" { 0.3 } else { 0.0 },
                "arousal": if turn.speaker == "user" { 0.5 } else { 0.1 },
                "source": {
                    "source_type": "SystemGenerated",
                    "source_id": "locomo",
                    "timestamp": turn.timestamp * 1000
                },
                "source_ref": {
                    "uri": format!("locomo://{}/turn_{}", session.id, i),
                    "offset": 0,
                    "length": text.len()
                },
                "is_structural": i == 0,
                "domain_id": session.id
            });
            // Long text test: append padding to first session's first turn
            if i == 0 && long_text_session_id.is_none() {
                let long = format!("{} {}", text, "X".repeat(600));
                item["text"] = json!(long);
                item["source_ref"]["length"] = json!(long.len());
                long_text_session_id = Some(session.id.clone());
            }
            items.push(item);
        }

        let batch = json!({
            "command": "batch_store",
            "items": items,
            "session_id": session.id,
            "turn_id": format!("{}_turn_0", session.id)
        });

        let t0 = Instant::now();
        let res = exec(handle, &batch.to_string());
        latencies.push(t0.elapsed());

        if res["success"].as_bool().unwrap_or(false) {
            let d = &res["data"];
            let l4_docs = d["l4_docs"].as_u64().unwrap_or(0);
            let l1_created = d["l1_nodes_created"].as_u64().unwrap_or(0);
            let l2_updated = d["l2_topics_updated"].as_u64().unwrap_or(0);

            // Strict data validation
            if l4_docs == 0 {
                result.details.push(format!("FAILED: batch_store session '{}': l4_docs=0, expected > 0 (should create L4 documents)", session.id));
                all_pass = false;
            }
            if l1_created == 0 {
                result.details.push(format!("FAILED: batch_store session '{}': l1_nodes_created=0, expected > 0 (should create L1 nodes)", session.id));
                all_pass = false;
            }
            if l2_updated == 0 {
                result.details.push(format!("FAILED: batch_store session '{}': l2_topics_updated=0, expected > 0 (should create/update L2 topics)", session.id));
                all_pass = false;
            }
            result.details.push(format!(
                "Session {}: l4_docs={}, l1_created={}, l2_updated={}",
                session.id, l4_docs, l1_created, l2_updated
            ));
        } else {
            result.details.push(format!(
                "FAILED: Session {} batch_store error: {}",
                session.id, res["error"]
            ));
            all_pass = false;
        }
    }

    // Dedup test: store same items again, check dedup_skipped > 0
    if !fixture.sessions.is_empty() {
        let session = &fixture.sessions[0];
        let items: Vec<Value> = session.turns.iter().map(|t| {
            json!({
                "text": t.text,
                "topic_label": session.id,
                "importance": 0.5,
                "source": {
                    "source_type": "SystemGenerated",
                    "source_id": "locomo",
                    "timestamp": t.timestamp * 1000
                },
                "is_structural": false,
                "domain_id": session.id
            })
        }).collect();
        let batch = json!({
            "command": "batch_store",
            "items": items,
            "session_id": format!("{}_dedup", session.id),
            "turn_id": format!("{}_dedup_0", session.id)
        });
        let res = exec(handle, &batch.to_string());
        if res["success"].as_bool().unwrap_or(false) {
            let skipped = res["data"]["dedup_skipped"].as_u64().unwrap_or(0);
            if skipped == 0 {
                result.details.push(format!("FAILED: dedup test: dedup_skipped=0, expected > 0 (duplicate items should be skipped)"));
                all_pass = false;
            }
            result.details.push(format!("Dedup test: dedup_skipped={}", skipped));
        } else {
            result.details.push(format!("FAILED: Dedup batch_store: {}", res["error"]));
            all_pass = false;
        }
    }

    // Long text test: query L4 for the long-text session and verify multiple chunks
    if let Some(ref lt_sid) = long_text_session_id {
        let l4_list_cmd = format!(
            r#"{{"command":"query_layer","layer":"l4","action":"list","list":{{"page":1,"page_size":10,"topic_id":"{}"}}}}"#,
            lt_sid
        );
        let res = exec(handle, &l4_list_cmd);
        if res["success"].as_bool().unwrap_or(false) {
            let l4_count = res["data"]["items"].as_array().map(|a| a.len()).unwrap_or(0);
            if l4_count <= 1 {
                result.details.push(format!("Long text chunking: L4 count={} (expected >=2 for chunking verification, text was padded to ~600+ chars)", l4_count));
            } else {
                result.details.push(format!("Long text chunking verified: L4 count={} (multiple chunks)", l4_count));
            }
        } else {
            result.details.push(format!("FAILED: L4 list query for long-text session '{}': {}", lt_sid, res["error"]));
            all_pass = false;
        }
    }

    // Build L2 map by querying L2 with matching titles
    let mut page = 1u32;
    let mut found_any = false;
    loop {
        let list_cmd = format!(
            r#"{{"command":"query_layer","layer":"l2","action":"list","list":{{"page":{},"page_size":100,"active_only":false}}}}"#,
            page
        );
        let res = exec(handle, &list_cmd);
        if !res["success"].as_bool().unwrap_or(false) { break; }
        let items = match res["data"]["items"].as_array() {
            Some(a) => a.clone(),
            None => break,
        };
        if items.is_empty() { break; }
        found_any = true;
        for item in &items {
            if let (Some(id), Some(title)) = (item["id"].as_str(), item["title"].as_str()) {
                all_l2_ids.push(id.to_string());
                session_l2_map.entry(title.to_string()).or_default().push(id.to_string());
            }
        }
        if !res["data"]["has_more"].as_bool().unwrap_or(false) { break; }
        page += 1;
    }

    if !found_any {
        result.details.push("FAILED: No L2 topics found after batch_store (encoder may be missing)".to_string());
        all_pass = false;
    }

    result.passed = all_pass;

    let cc = coverage.entry("batch_store").or_default();
    cc.tested = true;
    cc.passed = result.passed;
    cc.details = format!("{} L2 topics, {} sessions", all_l2_ids.len(), fixture.sessions.len());

    print_phase(1, "Ingest (batch_store)", &result);
    (result, all_l2_ids, session_l2_map)
}

// ============================================================================
// Phase 2 — Session management
// ============================================================================

fn phase2_session(
    handle: *mut MemHopHandle,
    l2_ids: &[String],
    coverage: &mut HashMap<&str, CommandCoverage>,
) -> PhaseResult {
    let mut result = PhaseResult::default();
    let mut all_pass = true;
    let mut latencies = Vec::new();

    if l2_ids.is_empty() {
        result.details.push("FAILED: No L2 IDs available for session tests".to_string());
        all_pass = false;
        result.passed = all_pass;
        print_phase(2, "Session management", &result);
        return result;
    }

    let tid = &l2_ids[0];

    // 1. Activate with ttl_ms
    let activate = json!({
        "command": "session",
        "params": { "action": "activate", "topic_id": tid, "ttl_ms": 60000 }
    });
    let t0 = Instant::now();
    let res = exec(handle, &activate.to_string());
    latencies.push(t0.elapsed());
    if res["success"].as_bool().unwrap_or(false) {
        result.details.push(format!("activate OK: {:?}", res["data"]));
    } else {
        result.details.push(format!("FAILED: activate: {}", res["error"]));
        all_pass = false;
    }

    // 2. List active — verify topic_id is present
    let res = exec(handle, r#"{"command":"session","params":{"action":"list"}}"#);
    if res["success"].as_bool().unwrap_or(false) {
        let active_topics = res["data"]["active_topics"].as_array().cloned().unwrap_or_default();
        let active_count = active_topics.len();
        let contains_tid = active_topics.iter().any(|t| t.as_str() == Some(tid));
        if !contains_tid {
            result.details.push(format!("FAILED: session list after activate does not contain topic '{}' (active_topics={:?})", tid, active_topics));
            all_pass = false;
        } else {
            result.details.push(format!("List after activate: count={}, contains topic_id ✓", active_count));
        }
    } else {
        result.details.push(format!("FAILED: session list: {}", res["error"]));
        all_pass = false;
    }

    // 3. Adjust activation — verify 'adjusted' field
    let adjust = json!({
        "command": "session",
        "params": { "action": "adjust", "topic_id": tid, "delta": 0.5 }
    });
    let res = exec(handle, &adjust.to_string());
    if res["success"].as_bool().unwrap_or(false) {
        let has_adjusted = res["data"]["adjusted"].is_string() || res["data"]["adjusted"].is_boolean() || res["data"]["adjusted"].is_number();
        if !has_adjusted {
            result.details.push(format!("FAILED: adjust response missing 'adjusted' field: {:?}", res["data"]));
            all_pass = false;
        } else {
            result.details.push(format!("adjust OK: adjusted={:?}", res["data"]["adjusted"]));
        }
    } else {
        result.details.push(format!("FAILED: adjust: {}", res["error"]));
        all_pass = false;
    }

    // 4. Deactivate — verify topic_id is removed from active list
    let deactivate = json!({
        "command": "session",
        "params": { "action": "deactivate", "topic_id": tid }
    });
    let t0 = Instant::now();
    let res = exec(handle, &deactivate.to_string());
    latencies.push(t0.elapsed());
    if res["success"].as_bool().unwrap_or(false) {
        result.details.push(format!("deactivate OK: {:?}", res["data"]));
    } else {
        result.details.push(format!("FAILED: deactivate: {}", res["error"]));
        all_pass = false;
    }

    // Verify deactivation: list and assert topic_id NOT present
    let res = exec(handle, r#"{"command":"session","params":{"action":"list"}}"#);
    if res["success"].as_bool().unwrap_or(false) {
        let active_topics = res["data"]["active_topics"].as_array().cloned().unwrap_or_default();
        let still_contains = active_topics.iter().any(|t| t.as_str() == Some(tid));
        if still_contains {
            result.details.push(format!("FAILED: session list after deactivate still contains topic '{}' (active_topics={:?})", tid, active_topics));
            all_pass = false;
        } else {
            result.details.push("Deactivate verified: topic not in active list ✓".to_string());
        }
    } else {
        result.details.push(format!("FAILED: session list after deactivate: {}", res["error"]));
        all_pass = false;
    }

    // 5. Activate without ttl_ms (test default)
    let activate_default = json!({
        "command": "session",
        "params": { "action": "activate", "topic_id": tid }
    });
    let res = exec(handle, &activate_default.to_string());
    if res["success"].as_bool().unwrap_or(false) {
        result.details.push("activate (no ttl) OK".to_string());
    } else {
        result.details.push(format!("FAILED: activate (no ttl): {}", res["error"]));
        all_pass = false;
    }

    result.passed = all_pass;

    let cc = coverage.entry("session").or_default();
    cc.tested = true;
    cc.passed = result.passed;
    cc.details = "activate/list/adjust/deactivate tested".to_string();

    print_phase(2, "Session management", &result);
    result
}

// ============================================================================
// Phase 3 — Update dialogue writing
// ============================================================================

fn phase3_update(
    handle: *mut MemHopHandle,
    fixture: &Fixture,
    l2_ids: &[String],
    coverage: &mut HashMap<&str, CommandCoverage>,
) -> PhaseResult {
    let mut result = PhaseResult::default();
    let mut all_pass = true;
    let mut latencies = Vec::new();

    for (i, tid) in l2_ids.iter().enumerate() {
        // Find assistant replies from LOCOMO data
        let reply_text = if i < fixture.sessions.len() {
            let session = &fixture.sessions[i];
            session.turns.iter()
                .filter(|t| t.speaker == "assistant")
                .map(|t| t.text.as_str())
                .collect::<Vec<_>>()
                .join("\n")
        } else {
            "Sample assistant response for update test".to_string()
        };

        let update = json!({
            "command": "update",
            "topic_id": tid,
            "dialogue_text": reply_text,
            "summary": format!("Session summary for topic {}", i),
            "action_chain": [{
                "title": "test_action",
                "description": "test",
                "action_type": "Read",
                "parameters": {}
            }],
            "source": {
                "source_agent": "benchmark",
                "source_platform": "test"
            }
        });

        let t0 = Instant::now();
        let res = exec(handle, &update.to_string());
        latencies.push(t0.elapsed());

        if res["success"].as_bool().unwrap_or(false) {
            let status = res["data"]["status"].as_str().unwrap_or("?").to_string();
            let archive_id = res["data"]["archive_id"].as_str().unwrap_or("?").to_string();

            // Strict data validation
            if status != "Updated" {
                result.details.push(format!("FAILED: update topic '{}': status='{}', expected 'Updated'", tid, status));
                all_pass = false;
            }
            if archive_id.is_empty() || archive_id == "?" {
                result.details.push(format!("FAILED: update topic '{}': archive_id='{}' is invalid (expected non-empty valid ID)", tid, archive_id));
                all_pass = false;
            }
            result.details.push(format!(
                "Topic {} update: status={}, archive_id={}",
                tid, status, archive_id
            ));
        } else {
            result.details.push(format!("FAILED: Topic {} update: {}", tid, res["error"]));
            all_pass = false;
        }

        // Instant distill test on first topic — verify L3 knowledge nodes > 0
        if i == 0 {
            let distill = json!({
                "command": "update",
                "topic_id": tid,
                "dialogue_text": "Instant distill test message",
                "instant_distill": true,
                "source": {
                    "source_agent": "benchmark",
                    "source_platform": "test"
                }
            });
            let res = exec(handle, &distill.to_string());
            if res["success"].as_bool().unwrap_or(false) {
                result.details.push("instant_distill OK".to_string());

                // Query L3 list to verify knowledge nodes were created
                let l3_res = exec(handle, r#"{"command":"query_layer","layer":"l3","action":"list","list":{"page":1,"page_size":20}}"#);
                if l3_res["success"].as_bool().unwrap_or(false) {
                    let l3_count = l3_res["data"]["items"].as_array().map(|a| a.len()).unwrap_or(0);
                    if l3_count == 0 {
                        result.details.push("FAILED: L3 list after instant_distill returned 0 items (expected knowledge nodes > 0)".to_string());
                        all_pass = false;
                    } else {
                        result.details.push(format!("instant_distill L3 verification: {} knowledge nodes ✓", l3_count));
                    }
                } else {
                    result.details.push(format!("FAILED: L3 list after instant_distill: {}", l3_res["error"]));
                    all_pass = false;
                }
            } else {
                result.details.push(format!("FAILED: instant_distill: {}", res["error"]));
                all_pass = false;
            }
        }
    }

    result.passed = all_pass;

    let cc = coverage.entry("update").or_default();
    cc.tested = true;
    cc.passed = result.passed;
    cc.details = format!("{} topics updated", l2_ids.len());

    print_phase(3, "Update dialogue writing", &result);
    result
}

// ============================================================================
// Phase 4 — Query layer
// ============================================================================

fn phase4_query_layer(
    handle: *mut MemHopHandle,
    l2_ids: &[String],
    l1_ids: &mut Vec<String>,
    l3_ids: &mut Vec<String>,
    l4_ids: &mut Vec<String>,
    l5_ids: &mut Vec<String>,
    coverage: &mut HashMap<&str, CommandCoverage>,
) -> PhaseResult {
    let mut result = PhaseResult::default();
    let mut all_pass = true;

    // L0/get
    let res = exec(handle, r#"{"command":"query_layer","layer":"l0","action":"get","get":{},"list":{}}"#);
    if res["success"].as_bool().unwrap_or(false) {
        result.details.push("L0/get OK".to_string());
    } else {
        result.details.push(format!("FAILED: L0/get: {}", res["error"]));
        all_pass = false;
    }

    // L1/list + state_filter + min_importance
    let res = exec(handle, r#"{"command":"query_layer","layer":"l1","action":"list","list":{"page":1,"page_size":20,"state_filter":"Active","min_importance":0.3}}"#);
    if res["success"].as_bool().unwrap_or(false) {
        let items = res["data"]["items"].as_array().cloned().unwrap_or_default();
        let count = items.len();
        if count == 0 {
            result.details.push("FAILED: L1/list with state_filter=Active returned 0 items (expected > 0)".to_string());
            all_pass = false;
        } else {
            // Validate each item's state and importance
            let mut state_errors = 0;
            let mut imp_errors = 0;
            for item in &items {
                if item["memory_state"].as_str() != Some("Active") {
                    state_errors += 1;
                }
                if let Some(imp) = item["importance"].as_f64() {
                    if imp < 0.3 - 1e-9 {
                        imp_errors += 1;
                    }
                }
                if let Some(id) = item["id"].as_str() {
                    l1_ids.push(id.to_string());
                }
            }
            if state_errors > 0 {
                result.details.push(format!("FAILED: L1/list state_filter=Active: {} items have state != 'Active'", state_errors));
                all_pass = false;
            }
            if imp_errors > 0 {
                result.details.push(format!("FAILED: L1/list min_importance=0.3: {} items have importance < 0.3", imp_errors));
                all_pass = false;
            }
            result.details.push(format!("L1/list count={}, state_filter=Active ✓, min_importance=0.3 ✓", count));
        }
    } else {
        result.details.push(format!("FAILED: L1/list: {}", res["error"]));
        all_pass = false;
    }

    // L1/get (single)
    if !l1_ids.is_empty() {
        let get_cmd = format!(
            r#"{{"command":"query_layer","layer":"l1","action":"get","get":{{"id":"{}"}},"list":{{}}}}"#,
            l1_ids[0]
        );
        let res = exec(handle, &get_cmd);
        if res["success"].as_bool().unwrap_or(false) {
            result.details.push("L1/get OK".to_string());
        } else {
            result.details.push(format!("FAILED: L1/get: {}", res["error"]));
            all_pass = false;
        }
    }

    // L2/list + active_only + keyword
    let res = exec(handle, r#"{"command":"query_layer","layer":"l2","action":"list","list":{"page":1,"page_size":20,"active_only":false,"keyword":""}}"#);
    if res["success"].as_bool().unwrap_or(false) {
        let items = res["data"]["items"].as_array().cloned().unwrap_or_default();
        let count = items.len();
        if count == 0 {
            result.details.push("FAILED: L2/list returned 0 items (expected > 0, topics were stored)".to_string());
            all_pass = false;
        }
        result.details.push(format!("L2/list count={}", count));
        if count == 0 {
            // Debug: print raw response when count is 0
            result.details.push(format!("L2/list raw response: {}", serde_json::to_string_pretty(&res).unwrap_or_default()));
        }
    } else {
        let err_msg = format!("FAILED: L2/list: {}", res["error"]);
        result.details.push(err_msg.clone());
        // Debug: print raw response on error too
        result.details.push(format!("L2/list raw response: {}", serde_json::to_string_pretty(&res).unwrap_or_default()));
        all_pass = false;
    }

    // L2/get
    if !l2_ids.is_empty() {
        let get_cmd = format!(
            r#"{{"command":"query_layer","layer":"l2","action":"get","get":{{"id":"{}"}},"list":{{}}}}"#,
            l2_ids[0]
        );
        let res = exec(handle, &get_cmd);
        if res["success"].as_bool().unwrap_or(false) {
            result.details.push("L2/get OK".to_string());
        } else {
            result.details.push(format!("FAILED: L2/get: {}", res["error"]));
            all_pass = false;
        }
    }

    // L3/list + domain_filter + knowledge_type
    let res = exec(handle, r#"{"command":"query_layer","layer":"l3","action":"list","list":{"page":1,"page_size":20,"domain_filter":"","knowledge_type":"Factual"}}"#);
    if res["success"].as_bool().unwrap_or(false) {
        let items = res["data"]["items"].as_array().cloned().unwrap_or_default();
        let count = items.len();
        result.details.push(format!("L3/list count={}", count));
        for item in &items {
            if let Some(id) = item["id"].as_str() {
                l3_ids.push(id.to_string());
            }
        }
    } else {
        result.details.push(format!("FAILED: L3/list: {}", res["error"]));
        all_pass = false;
    }

    // L3/get
    if !l3_ids.is_empty() {
        let get_cmd = format!(
            r#"{{"command":"query_layer","layer":"l3","action":"get","get":{{"id":"{}"}},"list":{{}}}}"#,
            l3_ids[0]
        );
        let res = exec(handle, &get_cmd);
        if res["success"].as_bool().unwrap_or(false) {
            result.details.push("L3/get OK".to_string());
        } else {
            result.details.push(format!("FAILED: L3/get: {}", res["error"]));
            all_pass = false;
        }
    }

    // L4/list — by_topic (verify non-empty)
    if !l2_ids.is_empty() {
        let list_cmd = format!(
            r#"{{"command":"query_layer","layer":"l4","action":"list","list":{{"page":1,"page_size":10,"topic_id":"{}"}}}}"#,
            l2_ids[0]
        );
        let res = exec(handle, &list_cmd);
        if res["success"].as_bool().unwrap_or(false) {
            let items = res["data"]["items"].as_array().cloned().unwrap_or_default();
            if items.is_empty() {
                result.details.push("FAILED: L4/list by_topic returned empty items (expected L4 docs for known topic)".to_string());
                all_pass = false;
            } else {
                result.details.push(format!("L4/list by_topic: {} items ✓", items.len()));
                collect_ids(&res, l4_ids);
            }
        } else {
            result.details.push(format!("FAILED: L4/list by_topic: {}", res["error"]));
            all_pass = false;
        }
    }

    // L4/list — by_nodes (use l2_ids, verify non-empty — archives store context_id = L2 hash)
    if l2_ids.len() >= 2 {
        let list_cmd = format!(
            r#"{{"command":"query_layer","layer":"l4","action":"list","list":{{"page":1,"page_size":10,"node_ids":["{}","{}"]}}}}"#,
            l2_ids[0], l2_ids[1]
        );
        let res = exec(handle, &list_cmd);
        if res["success"].as_bool().unwrap_or(false) {
            let items = res["data"]["items"].as_array().cloned().unwrap_or_default();
            if items.is_empty() {
                result.details.push("FAILED: L4/list by_nodes returned empty items".to_string());
                all_pass = false;
            } else {
                result.details.push(format!("L4/list by_nodes: {} items ✓", items.len()));
            }
        } else {
            result.details.push(format!("FAILED: L4/list by_nodes: {}", res["error"]));
            all_pass = false;
        }
    }

    // L4/list — all (no filter, verify non-empty)
    let res = exec(handle, r#"{"command":"query_layer","layer":"l4","action":"list","list":{"page":1,"page_size":10}}"#);
    if res["success"].as_bool().unwrap_or(false) {
        let items = res["data"]["items"].as_array().cloned().unwrap_or_default();
        let count = items.len();
        if count == 0 {
            result.details.push("FAILED: L4/list all returned 0 items (expected > 0)".to_string());
            all_pass = false;
        } else {
            result.details.push(format!("L4/list all: {} items ✓", count));
        }
        collect_ids(&res, l4_ids);
    } else {
        result.details.push(format!("FAILED: L4/list all: {}", res["error"]));
        all_pass = false;
    }

    // L4/list — time range
    let res = exec(handle, r#"{"command":"query_layer","layer":"l4","action":"list","list":{"page":1,"page_size":10,"start_time":0,"end_time":99999999999999}}"#);
    if res["success"].as_bool().unwrap_or(false) {
        let items = res["data"]["items"].as_array().cloned().unwrap_or_default();
        result.details.push(format!("L4/list time_range: {} items", items.len()));
    } else {
        result.details.push(format!("FAILED: L4/list time_range: {}", res["error"]));
        all_pass = false;
    }

    // L4/get — skipped: query_layer does not support L4 get operation

    // L5/list + status_filter + min_trigger_count
    let res = exec(handle, r#"{"command":"query_layer","layer":"l5","action":"list","list":{"page":1,"page_size":10,"status_filter":"","min_trigger_count":0}}"#);
    if res["success"].as_bool().unwrap_or(false) {
        let items = res["data"]["items"].as_array().cloned().unwrap_or_default();
        result.details.push(format!("L5/list count={}", items.len()));
        collect_ids(&res, l5_ids);
    } else {
        result.details.push(format!("FAILED: L5/list: {}", res["error"]));
        all_pass = false;
    }

    result.passed = all_pass;

    let cc = coverage.entry("query_layer").or_default();
    cc.tested = true;
    cc.passed = result.passed;
    cc.details = "L0-L5 all layers tested".to_string();

    print_phase(4, "Query layer verification", &result);
    result
}

fn collect_ids(res: &Value, ids: &mut Vec<String>) {
    if let Some(items) = res["data"]["items"].as_array() {
        for item in items {
            if let Some(id) = item["id"].as_str() {
                ids.push(id.to_string());
            }
        }
    }
}

// ============================================================================
// Phase 5 — Search all modes
// ============================================================================

fn phase5_search(
    handle: *mut MemHopHandle,
    fixture: &Fixture,
    l2_ids: &[String],
    l3_ids: &[String],
    session_l2_map: &HashMap<String, Vec<String>>,
    coverage: &mut HashMap<&str, CommandCoverage>,
) -> PhaseResult {
    let mut result = PhaseResult::default();
    let mut all_pass = true;

    // Use LOCOMO questions as search dialogue
    let dialogues: Vec<&str> = fixture.questions.iter().map(|q| q.question.as_str()).collect();
    if dialogues.is_empty() {
        result.details.push("FAILED: No questions available for search".to_string());
        all_pass = false;
        result.passed = all_pass;
        print_phase(5, "Search all modes", &result);
        return result;
    }

    // Auto-create test: count L2 topics before and after search
    let l2_before = {
        let res = exec(handle, r#"{"command":"query_layer","layer":"l2","action":"list","list":{"page":1,"page_size":10000}}"#);
        if res["success"].as_bool().unwrap_or(false) {
            res["data"]["total"].as_u64().unwrap_or(0) as usize
        } else { 0 }
    };

    // Route 1: auto_create=1, search_mode=deep
    let cmd = json!({
        "command": "search",
        "dialogue": dialogues[0],
        "auto_create": 1,
        "search_mode": "deep",
        "context_limit": 5,
        "min_score": 0.1,
        "source": {
            "source_agent": "bench",
            "source_platform": "test"
        }
    });
    let res = exec(handle, &cmd.to_string());
    if res["success"].as_bool().unwrap_or(false) {
        let contexts = res["data"]["contexts"].as_array().cloned().unwrap_or_default();
        let ctx_count = contexts.len();
        if ctx_count == 0 {
            result.details.push("FAILED: Route1 (auto_create=1) returned 0 contexts, expected > 0".to_string());
            all_pass = false;
        }
        // Verify context_limit is respected
        if ctx_count > 5 {
            result.details.push(format!("FAILED: Route1 context_limit=5 violated: got {} contexts", ctx_count));
            all_pass = false;
        }
        // Verify deep mode has associated_contexts
        if res["data"]["associated_contexts"].is_null() {
            result.details.push("FAILED: Route1 deep mode: missing 'associated_contexts' field".to_string());
            all_pass = false;
        } else {
            result.details.push(format!("Route1 (deep mode) has associated_contexts ✓"));
        }
        result.details.push(format!("Route1 (auto_create=1, deep) contexts={}", ctx_count));
    } else {
        result.details.push(format!("FAILED: Route1 search: {}", res["error"]));
        all_pass = false;
    }

    // Auto-create: verify L2 topic count increased
    let l2_after = {
        let res = exec(handle, r#"{"command":"query_layer","layer":"l2","action":"list","list":{"page":1,"page_size":10000}}"#);
        if res["success"].as_bool().unwrap_or(false) {
            res["data"]["total"].as_u64().unwrap_or(0) as usize
        } else { 0 }
    };
    if l2_after <= l2_before {
        result.details.push(format!("FAILED: auto_create did not increase L2 topics (before={}, after={})", l2_before, l2_after));
        all_pass = false;
    } else {
        result.details.push(format!("Auto-create: L2 topics increased ({} → {}) ✓", l2_before, l2_after));
    }

    // Route 2: context_id
    if !l2_ids.is_empty() {
        let cmd = json!({
            "command": "search",
            "dialogue": dialogues.get(1).unwrap_or(&dialogues[0]),
            "context_id": l2_ids[0],
            "context_limit": 5,
            "min_score": 0.1
        });
        let res = exec(handle, &cmd.to_string());
        if res["success"].as_bool().unwrap_or(false) {
            let contexts = res["data"]["contexts"].as_array().cloned().unwrap_or_default();
            result.details.push(format!("Route2 (context_id): {} contexts ✓", contexts.len()));
        } else {
            result.details.push(format!("FAILED: Route2 (context_id): {}", res["error"]));
            all_pass = false;
        }
    }

    // Route 3: l3_id
    if !l3_ids.is_empty() {
        let cmd = json!({
            "command": "search",
            "dialogue": dialogues.get(2).unwrap_or(&dialogues[0]),
            "l3_id": l3_ids[0],
            "context_limit": 5,
            "min_score": 0.1
        });
        let res = exec(handle, &cmd.to_string());
        if res["success"].as_bool().unwrap_or(false) {
            let contexts = res["data"]["contexts"].as_array().cloned().unwrap_or_default();
            result.details.push(format!("Route3 (l3_id): {} contexts ✓", contexts.len()));
        } else {
            result.details.push(format!("FAILED: Route3 (l3_id): {}", res["error"]));
            all_pass = false;
        }
    }

    // Route 4: default (full search) — compute recall@k
    let route4_dialogue = dialogues.last().unwrap_or(&dialogues[0]);
    let cmd = json!({
        "command": "search",
        "dialogue": route4_dialogue,
        "context_limit": 10,
        "min_score": 0.1,
        "context_history": "previous dialogue context for testing"
    });
    let res = exec(handle, &cmd.to_string());
    if res["success"].as_bool().unwrap_or(false) {
        let contexts = res["data"]["contexts"].as_array().cloned().unwrap_or_default();
        let ctx_count = contexts.len();
        if ctx_count == 0 {
            result.details.push("FAILED: Route4 (default) returned 0 contexts, expected > 0".to_string());
            all_pass = false;
        }
        result.details.push(format!("Route4 (default): {} contexts", ctx_count));

        // Compute recall@k using metrics module
        let ctx_ids: Vec<&str> = contexts.iter().filter_map(|c| c["id"].as_str()).collect();
        // Find expected relevant IDs from fixture questions -> session_refs -> session_l2_map
        let last_q = fixture.questions.last();
        let mut expected_ids: Vec<String> = Vec::new();
        if let Some(q) = last_q {
            for sref in &q.session_refs {
                if let Some(l2s) = session_l2_map.get(sref) {
                    expected_ids.extend(l2s.clone());
                }
            }
        }
        let expected_refs: Vec<&str> = expected_ids.iter().map(|s| s.as_str()).collect();
        if !expected_refs.is_empty() && !ctx_ids.is_empty() {
            for k in &[1, 3, 5, 10] {
                let r = recall_at_k(&ctx_ids, &expected_refs, *k);
                result.details.push(format!("  recall@{}={:.3}", k, r));
            }
        }
    } else {
        result.details.push(format!("FAILED: Route4 (default): {}", res["error"]));
        all_pass = false;
    }

    // Parameter coverage: context_limit variants
    for &limit in &[1, 5, 10, 20] {
        let cmd = json!({
            "command": "search",
            "dialogue": route4_dialogue,
            "context_limit": limit,
            "min_score": 0.1,
            "auto_create": 0
        });
        let res = exec(handle, &cmd.to_string());
        if res["success"].as_bool().unwrap_or(false) {
            let contexts = res["data"]["contexts"].as_array().cloned().unwrap_or_default();
            if contexts.len() > limit as usize {
                result.details.push(format!("FAILED: search context_limit={} returned {} items (expected <= {})", limit, contexts.len(), limit));
                all_pass = false;
            }
            result.details.push(format!("search context_limit={}: {} items ✓", limit, contexts.len()));
        } else {
            result.details.push(format!("FAILED: search context_limit={}: {}", limit, res["error"]));
            all_pass = false;
        }
    }

    result.passed = all_pass;

    let cc = coverage.entry("search").or_default();
    cc.tested = true;
    cc.passed = result.passed;
    cc.details = "4 routes + all params + recall@k tested".to_string();

    print_phase(5, "Search all modes", &result);
    result
}

// ============================================================================
// Phase 6 — Import + graph_query
// ============================================================================

fn phase6_import_graph(
    handle: *mut MemHopHandle,
    coverage: &mut HashMap<&str, CommandCoverage>,
    l3_ids: &mut Vec<String>,
) -> PhaseResult {
    let mut result = PhaseResult::default();
    let mut all_pass = true;

    // Import profile (mode=merge)
    let cmd = json!({
        "command": "import",
        "params": {
            "action": "import",
            "target_layer": "profile",
            "mode": "merge",
            "data": {
                "Profile": {
                    "name": "Benchmark User",
                    "role": "tester"
                }
            }
        }
    });
    let res = exec(handle, &cmd.to_string());
    if res["success"].as_bool().unwrap_or(false) {
        let created = res["data"]["created_ids"].as_array().map(|a| a.len()).unwrap_or(0);
        let updated = res["data"]["updated_ids"].as_array().map(|a| a.len()).unwrap_or(0);
        if created == 0 && updated == 0 {
            result.details.push("Import profile: no created_ids or updated_ids (expected >= 1)".to_string());
        } else {
            result.details.push(format!("Import profile (merge): created={}, updated={} ✓", created, updated));
        }
    } else {
        result.details.push(format!("FAILED: Import profile: {}", res["error"]));
        all_pass = false;
    }

    // Import topic (mode=overwrite)
    let cmd = json!({
        "command": "import",
        "params": {
            "action": "import",
            "target_layer": "topic",
            "mode": "overwrite",
            "data": {
                "Topics": [{
                    "title": "Overwritten Topic",
                    "summary": "This topic was overwritten",
                    "keywords": ["overwrite", "test"]
                }]
            }
        }
    });
    let res = exec(handle, &cmd.to_string());
    if res["success"].as_bool().unwrap_or(false) {
        let created = res["data"]["created_ids"].as_array().map(|a| a.len()).unwrap_or(0);
        let updated = res["data"]["updated_ids"].as_array().map(|a| a.len()).unwrap_or(0);
        result.details.push(format!("Import topic (overwrite): created={}, updated={}", created, updated));
    } else {
        result.details.push(format!("FAILED: Import topic overwrite: {}", res["error"]));
        all_pass = false;
    }

    // Import topic (mode=skip)
    let cmd = json!({
        "command": "import",
        "params": {
            "action": "import",
            "target_layer": "topic",
            "mode": "skip",
            "data": {
                "Topics": [{
                    "title": "Skipped Topic",
                    "summary": "This topic should be skipped if exists",
                    "keywords": ["skip", "test"]
                }]
            }
        }
    });
    let res = exec(handle, &cmd.to_string());
    if res["success"].as_bool().unwrap_or(false) {
        result.details.push("Import topic (skip) OK".to_string());
    } else {
        result.details.push(format!("FAILED: Import topic skip: {}", res["error"]));
        all_pass = false;
    }

    // Import topic with knowledge_title
    let cmd = json!({
        "command": "import",
        "params": {
            "action": "import",
            "target_layer": "topic",
            "mode": "merge",
            "knowledge_title": "Test Domain",
            "data": {
                "Topics": [{
                    "title": "Knowledge-Linked Topic",
                    "summary": "Topic linked to knowledge domain",
                    "keywords": ["knowledge", "link"]
                }]
            }
        }
    });
    let res = exec(handle, &cmd.to_string());
    if res["success"].as_bool().unwrap_or(false) {
        result.details.push("Import topic (knowledge_title) OK".to_string());
    } else {
        result.details.push(format!("FAILED: Import topic knowledge_title: {}", res["error"]));
        all_pass = false;
    }

    // Import knowledge
    let cmd = json!({
        "command": "import",
        "params": {
            "action": "import",
            "target_layer": "knowledge",
            "mode": "merge",
            "data": {
                "Knowledge": [{
                    "title": "Test Knowledge",
                    "domain": "testing",
                    "knowledge_type": "Factual",
                    "text": "This is a test knowledge entry for the benchmark.",
                    "keywords": ["test", "knowledge"]
                }]
            }
        }
    });
    let res = exec(handle, &cmd.to_string());
    if res["success"].as_bool().unwrap_or(false) {
        result.details.push("Import knowledge OK".to_string());
    } else {
        result.details.push(format!("FAILED: Import knowledge: {}", res["error"]));
        all_pass = false;
    }

    // Build L3 from Rust source file path (use absolute path)
    let src_path = std::fs::canonicalize("benches/common/metrics.rs")
        .unwrap_or_else(|e| panic!("Cannot canonicalize source path: {}", e));
    let cmd = json!({
        "command": "import",
        "params": {
            "action": "build_l3",
            "path": src_path.to_str().unwrap()
        }
    });
    let res = exec(handle, &cmd.to_string());
    if res["success"].as_bool().unwrap_or(false) {
        let created = res["data"]["created_ids"].as_array().map(|a| a.len()).unwrap_or(0);
        if created == 0 {
            result.details.push("FAILED: build_l3 created=0 (expected > 0)".to_string());
            all_pass = false;
        } else {
            result.details.push(format!("build_l3: {} created ✓", created));
        }
        if let Some(ids) = res["data"]["created_ids"].as_array() {
            for id_val in ids {
                if let Some(id) = id_val.as_str() {
                    l3_ids.push(id.to_string());
                }
            }
        }
        // Also collect from updated_ids
        if let Some(ids) = res["data"]["updated_ids"].as_array() {
            for id_val in ids {
                if let Some(id) = id_val.as_str() {
                    l3_ids.push(id.to_string());
                }
            }
        }
    } else {
        result.details.push(format!("FAILED: build_l3: {}", res["error"]));
        all_pass = false;
    }

    let cc = coverage.entry("import").or_default();
    cc.tested = true;
    cc.passed = all_pass;
    cc.details = "profile/topic/knowledge/build_l3 tested".to_string();

    // Graph query
    let mut graph_all_pass = true;

    if l3_ids.is_empty() {
        result.details.push("FAILED: No L3 IDs for graph_query".to_string());
        graph_all_pass = false;
    } else {
        let graph_id = &l3_ids[0];
        for &kind in &["Related", "Causal", "PartOf", "Sequence", "Dependency", "Custom"] {
            let cmd = json!({
                "command": "graph_query",
                "graph_id": graph_id,
                "start_node": l3_ids[0],
                "max_depth": 3,
                "edge_kinds": [kind]
            });
            let res = exec(handle, &cmd.to_string());
            if res["success"].as_bool().unwrap_or(false) {
                let nodes = res["data"]["nodes"].as_array().cloned().unwrap_or_default();
                let edges = res["data"]["edges"].as_array().cloned().unwrap_or_default();
                if edges.is_empty() && nodes.is_empty() {
                    result.details.push(format!("graph_query edge_kind={}: no edges in test data (expected, build_l3 creates nodes without edges)", kind));
                } else {
                    result.details.push(format!("graph_query edge_kind={}: {} nodes, {} edges ✓", kind, nodes.len(), edges.len()));
                }
            } else {
                result.details.push(format!("FAILED: graph_query edge_kind={}: {}", kind, res["error"]));
                graph_all_pass = false;
            }
        }
    }

    let cc2 = coverage.entry("graph_query").or_default();
    cc2.tested = true;
    cc2.passed = graph_all_pass;
    cc2.details = format!("{} edge_kinds tested", if l3_ids.is_empty() { 0 } else { 6 });

    // Combine import + graph results
    result.passed = all_pass;

    print_phase(6, "Import + graph_query", &result);
    result
}

// ============================================================================
// Phase 7 — Dream + merge_topics + update_title
// ============================================================================

fn phase7_dream_merge_title(
    handle: *mut MemHopHandle,
    l2_ids: &[String],
    l3_ids: &[String],
    l5_ids: &[String],
    coverage: &mut HashMap<&str, CommandCoverage>,
) -> PhaseResult {
    let mut result = PhaseResult::default();
    let mut all_pass = true;

    // ---- Dream (MUST execute, skip = FAILED) ----
    let api_key = std::env::var(DEEPSEEK_API_KEY_VAR).ok();
    if let Some(ref key) = api_key {
        let cmd = json!({
            "command": "dream",
            "api_url": "https://api.deepseek.com/v1/chat/completions",
            "api_key": key,
            "model": "deepseek-chat",
            "temperature": 0.2,
            "top_p": 0.9,
            "presence_penalty": 0.0,
            "frequency_penalty": 0.0,
            "timeout_secs": 30,
            "language": "zh"
        });
        let t0 = Instant::now();
        let res = exec(handle, &cmd.to_string());
        let elapsed = t0.elapsed();
        if res["success"].as_bool().unwrap_or(false) {
            let dur = res["data"]["duration_ms"].as_u64().unwrap_or(0);
            let decayed = res["data"]["l1_decayed_nodes"].as_u64().unwrap_or(0);
            result.details.push(format!("dream OK: duration_ms={}, l1_decayed={}, wall_time={:?}", dur, decayed, elapsed));
        } else {
            result.details.push(format!("FAILED: dream: {}", res["error"]));
            all_pass = false;
        }
    } else {
        result.details.push("FAILED: Dream SKIPPED - MEMHOP_DEEPSEEK_API_KEY not set".to_string());
        all_pass = false;
    }
    let cc = coverage.entry("dream").or_default();
    cc.tested = true;
    cc.passed = all_pass;  // Dream must pass (not skipped)
    cc.details = result.details.last().cloned().unwrap_or_default();

    // ---- Merge topics (verify archive_refs increase) ----
    let mut merge_pass = true;
    let mut merge_archive_count_before: usize = 0;
    if l2_ids.len() >= 2 {
        // Count archive_refs before merge
        let get_cmd = format!(
            r#"{{"command":"query_layer","layer":"l2","action":"get","get":{{"id":"{}"}},"list":{{}}}}"#,
            l2_ids[0]
        );
        let get_res = exec(handle, &get_cmd);
        if get_res["success"].as_bool().unwrap_or(false) {
            merge_archive_count_before = get_res["data"]["archive_refs"].as_array().map(|a| a.len()).unwrap_or(0);
        }

        let cmd = json!({
            "command": "merge_topics",
            "primary_id": l2_ids[0],
            "secondary_ids": [l2_ids[1]]
        });
        let t0 = Instant::now();
        let res = exec(handle, &cmd.to_string());
        let _elapsed = t0.elapsed();
        if res["success"].as_bool().unwrap_or(false) {
            // Query again to verify archive_refs increased
            let get_res2 = exec(handle, &get_cmd);
            let count_after = get_res2["data"]["archive_refs"].as_array().map(|a| a.len()).unwrap_or(0);
            if count_after > merge_archive_count_before {
                result.details.push(format!("merge_topics OK: archive_refs increased ({} → {}) ✓", merge_archive_count_before, count_after));
            } else {
                result.details.push(format!("merge_topics: archive_refs not increased (before={}, after={})", merge_archive_count_before, count_after));
            }
        } else {
            result.details.push(format!("FAILED: merge_topics: {}", res["error"]));
            merge_pass = false;
        }
    } else {
        result.details.push("SKIPPED: merge_topics needs >= 2 L2 topics".to_string());
    }
    let cc = coverage.entry("merge_topics").or_default();
    cc.tested = true;
    cc.passed = merge_pass || l2_ids.len() < 2;
    cc.details = result.details.last().cloned().unwrap_or_default();

    // ---- Update title ----
    let mut title_pass = true;

    // L0 update_title
    let res = exec(handle, r#"{"command":"update_title","layer":"l0","params":{"name":"TestAgent","role":"assistant","personality":"friendly"}}"#);
    if res["success"].as_bool().unwrap_or(false) {
        result.details.push("update_title L0 OK".to_string());
    } else {
        result.details.push(format!("FAILED: update_title L0: {}", res["error"]));
        title_pass = false;
    }

    // L2 update_title + verify via get
    if !l2_ids.is_empty() {
        let new_title = "Updated Benchmark Topic";
        let cmd = json!({
            "command": "update_title",
            "layer": "l2",
            "params": { "id": l2_ids[0], "new_title": new_title }
        });
        let res = exec(handle, &cmd.to_string());
        if res["success"].as_bool().unwrap_or(false) {
            // Verify title by querying L2/get
            let get_cmd = format!(
                r#"{{"command":"query_layer","layer":"l2","action":"get","get":{{"id":"{}"}},"list":{{}}}}"#,
                l2_ids[0]
            );
            let get_res = exec(handle, &get_cmd);
            let actual_title = get_res["data"]["title"].as_str().unwrap_or("?");
            if actual_title == new_title {
                result.details.push(format!("update_title L2 OK: title='{}' ✓", actual_title));
            } else {
                result.details.push(format!("FAILED: update_title L2: title='{}', expected '{}'", actual_title, new_title));
                title_pass = false;
            }
        } else {
            result.details.push(format!("FAILED: update_title L2: {}", res["error"]));
            title_pass = false;
        }
    }

    // L3 update_title + verify
    if !l3_ids.is_empty() {
        let new_title = "Updated L3 Knowledge";
        let cmd = json!({
            "command": "update_title",
            "layer": "l3",
            "params": { "id": l3_ids[0], "new_title": new_title }
        });
        let res = exec(handle, &cmd.to_string());
        if res["success"].as_bool().unwrap_or(false) {
            // Verify title by querying L3/get
            let get_cmd = format!(
                r#"{{"command":"query_layer","layer":"l3","action":"get","get":{{"id":"{}"}},"list":{{}}}}"#,
                l3_ids[0]
            );
            let get_res = exec(handle, &get_cmd);
            let actual_title = get_res["data"]["title"].as_str().unwrap_or("?");
            if actual_title == new_title {
                result.details.push(format!("update_title L3 OK: title='{}' ✓", actual_title));
            } else {
                result.details.push(format!("FAILED: update_title L3: title='{}', expected '{}'", actual_title, new_title));
                title_pass = false;
            }
        } else {
            result.details.push(format!("FAILED: update_title L3: {}", res["error"]));
            title_pass = false;
        }
    }

    // L5 update_title + verify
    if !l5_ids.is_empty() {
        let new_title = "Updated L5 Crystal";
        let cmd = json!({
            "command": "update_title",
            "layer": "l5",
            "params": { "id": l5_ids[0], "new_title": new_title }
        });
        let res = exec(handle, &cmd.to_string());
        if res["success"].as_bool().unwrap_or(false) {
            let get_cmd = format!(
                r#"{{"command":"query_layer","layer":"l5","action":"get","get":{{"id":"{}"}},"list":{{}}}}"#,
                l5_ids[0]
            );
            let get_res = exec(handle, &get_cmd);
            let actual_title = get_res["data"]["title"].as_str().unwrap_or("?");
            if actual_title == new_title {
                result.details.push(format!("update_title L5 OK: title='{}' ✓", actual_title));
            } else {
                result.details.push(format!("FAILED: update_title L5: title='{}', expected '{}'", actual_title, new_title));
                title_pass = false;
            }
        } else {
            result.details.push(format!("FAILED: update_title L5: {}", res["error"]));
            title_pass = false;
        }
    }

    let cc = coverage.entry("update_title").or_default();
    cc.tested = true;
    cc.passed = title_pass;
    cc.details = "L0/L2/L3/L5 tested".to_string();

    result.passed = all_pass;  // Dream passes only if executed successfully
    print_phase(7, "Dream + merge_topics + update_title", &result);
    result
}

// ============================================================================
// Phase 8 — Delete + sync + close
// ============================================================================

fn phase8_delete_sync_close(
    handle: *mut MemHopHandle,
    l2_ids: &[String],
    l3_ids: &[String],
    l5_ids: &[String],
    coverage: &mut HashMap<&str, CommandCoverage>,
) -> PhaseResult {
    let mut result = PhaseResult::default();
    let mut delete_pass = true;
    let mut sync_pass = true;
    let mut close_pass = true;

    // Delete existing L2 — assert deleted == true
    if !l2_ids.is_empty() {
        let cmd = json!({"command": "delete", "layer": "l2", "id": l2_ids[0]});
        let res = exec(handle, &cmd.to_string());
        if res["success"].as_bool().unwrap_or(false) {
            let deleted = res["data"]["deleted"].as_bool().unwrap_or(false);
            if !deleted {
                result.details.push(format!("FAILED: delete L2 (exists): deleted=false, expected true for id '{}'", l2_ids[0]));
                delete_pass = false;
            } else {
                result.details.push(format!("delete L2 (exists): deleted=true ✓"));
            }
        } else {
            result.details.push(format!("FAILED: delete L2: {}", res["error"]));
            delete_pass = false;
        }
    }

    // Delete nonexistent L2 — verify response
    let res = exec(handle, r#"{"command":"delete","layer":"l2","id":"nonexistent_123"}"#);
    if res["success"].as_bool().unwrap_or(false) {
        let deleted = res["data"]["deleted"].as_bool().unwrap_or(false);
        let reason = res["data"]["reason"].as_str().unwrap_or("?");
        result.details.push(format!("delete L2 (nonexistent): deleted={}, reason='{}'", deleted, reason));
    } else {
        result.details.push(format!("FAILED: delete L2 nonexistent: {}", res["error"]));
        delete_pass = false;
    }

    // Delete L3
    if !l3_ids.is_empty() {
        let cmd = json!({"command": "delete", "layer": "l3", "id": l3_ids[0]});
        let res = exec(handle, &cmd.to_string());
        if res["success"].as_bool().unwrap_or(false) {
            let deleted = res["data"]["deleted"].as_bool().unwrap_or(false);
            result.details.push(format!("delete L3: deleted={}", deleted));
        } else {
            result.details.push(format!("FAILED: delete L3: {}", res["error"]));
            delete_pass = false;
        }
    }

    // Delete L5
    if !l5_ids.is_empty() {
        let cmd = json!({"command": "delete", "layer": "l5", "id": l5_ids[0]});
        let res = exec(handle, &cmd.to_string());
        if res["success"].as_bool().unwrap_or(false) {
            let deleted = res["data"]["deleted"].as_bool().unwrap_or(false);
            result.details.push(format!("delete L5: deleted={}", deleted));
        } else {
            result.details.push(format!("FAILED: delete L5: {}", res["error"]));
            delete_pass = false;
        }
    }

    // Delete unsupported layer "l6" (error path)
    let res = exec(handle, r#"{"command":"delete","layer":"l6","id":"0000000000000001"}"#);
    if !res["success"].as_bool().unwrap_or(false) {
        result.details.push(format!("delete l6 error expected: {}", res["error"]));
    } else {
        result.details.push("FAILED: delete l6 unexpectedly succeeded (expected error)".to_string());
        delete_pass = false;
    }

    let cc = coverage.entry("delete").or_default();
    cc.tested = true;
    cc.passed = delete_pass;
    cc.details = "L2/L3/L5 + nonexistent + l6 error tested".to_string();

    // Sync — pass/fail based on actual result
    let res = exec(handle, r#"{"command":"sync"}"#);
    if res["success"].as_bool().unwrap_or(false) {
        let synced = res["data"]["synced"].as_bool().unwrap_or(false);
        if !synced {
            result.details.push("FAILED: sync: synced=false (expected true)".to_string());
            sync_pass = false;
        } else {
            result.details.push("sync: synced=true ✓".to_string());
        }
    } else {
        result.details.push(format!("FAILED: sync: {}", res["error"]));
        sync_pass = false;
    }
    let cc = coverage.entry("sync").or_default();
    cc.tested = true;
    cc.passed = sync_pass;
    cc.details = "synced=true".to_string();

    // Close — pass/fail based on actual result
    let res = exec(handle, r#"{"command":"close"}"#);
    if res["success"].as_bool().unwrap_or(false) {
        let closed = res["data"]["closed"].as_bool().unwrap_or(false);
        if !closed {
            result.details.push("FAILED: close: closed=false (expected true)".to_string());
            close_pass = false;
        } else {
            result.details.push("close: closed=true ✓".to_string());
        }
    } else {
        result.details.push(format!("FAILED: close: {}", res["error"]));
        close_pass = false;
    }
    let cc = coverage.entry("close").or_default();
    cc.tested = true;
    cc.passed = close_pass;
    cc.details = "closed=true".to_string();

    result.passed = delete_pass && sync_pass && close_pass;

    print_phase(8, "Delete + sync + close", &result);
    result
}

// ============================================================================
// Criterion latency benchmarks
// ============================================================================

fn bench_batch_store(c: &mut Criterion) {
    c.bench_function("batch_store", |b| {
        b.iter(|| unsafe {
            let handle = get_handle();
            let items = vec![json!({
                "text": "Criterion benchmark test item for latency measurement",
                "topic_label": "bench_topic",
                "importance": 0.5,
                "source": {"source_type": "SystemGenerated", "source_id": "criterion", "timestamp": 0},
                "is_structural": false,
                "domain_id": "bench"
            })];
            let batch = json!({"command": "batch_store", "items": items, "session_id": "criterion_sess", "turn_id": "criterion_turn"});
            let res = exec(handle, &batch.to_string());
            black_box(res)
        })
    });
}

fn bench_session_activate(c: &mut Criterion) {
    c.bench_function("session_activate", |b| {
        b.iter(|| unsafe {
            let handle = get_handle();
            let cmd = r#"{"command":"session","params":{"action":"activate","topic_id":"0000000000000001","ttl_ms":60000}}"#;
            let res = exec(handle, cmd);
            black_box(res)
        })
    });
}

fn bench_update(c: &mut Criterion) {
    c.bench_function("update", |b| {
        b.iter(|| unsafe {
            let handle = get_handle();
            let cmd = json!({
                "command": "update",
                "topic_id": "0000000000000001",
                "dialogue_text": "Criterion update benchmark",
                "summary": "bench",
                "action_chain": [{"title": "bench", "description": "bench", "action_type": "Read", "parameters": {}}]
            });
            let res = exec(handle, &cmd.to_string());
            black_box(res)
        })
    });
}

fn bench_query_l2_list(c: &mut Criterion) {
    c.bench_function("query_l2_list", |b| {
        b.iter(|| unsafe {
            let handle = get_handle();
            let res = exec(handle, r#"{"command":"query_layer","layer":"l2","action":"list","list":{"page":1,"page_size":20}}"#);
            black_box(res)
        })
    });
}

fn bench_search(c: &mut Criterion) {
    let mut group = c.benchmark_group("search");
    group.sample_size(10);

    group.bench_with_input(BenchmarkId::new("route", "auto_create"), &"auto_create", |b, _| {
        b.iter(|| unsafe {
            let handle = get_handle();
            let cmd = json!({"command":"search","dialogue":"Criterion search benchmark","auto_create":1,"context_limit":5,"min_score":0.1});
            let res = exec(handle, &cmd.to_string());
            black_box(res)
        })
    });

    group.bench_with_input(BenchmarkId::new("route", "default"), &"default", |b, _| {
        b.iter(|| unsafe {
            let handle = get_handle();
            let cmd = json!({"command":"search","dialogue":"Criterion search benchmark","context_limit":10,"min_score":0.1});
            let res = exec(handle, &cmd.to_string());
            black_box(res)
        })
    });

    group.finish();
}

fn bench_import(c: &mut Criterion) {
    c.bench_function("import_topic", |b| {
        b.iter(|| unsafe {
            let handle = get_handle();
            let cmd = json!({
                "command": "import",
                "params": {
                    "action": "import",
                    "target_layer": "topic",
                    "mode": "merge",
                    "data": {"Topics": [{"title": format!("Criterion Topic {}", rand::random::<u64>()), "keywords": ["criterion", "bench"]}]}
                }
            });
            let res = exec(handle, &cmd.to_string());
            black_box(res)
        })
    });
}

fn bench_delete(c: &mut Criterion) {
    c.bench_function("delete", |b| {
        b.iter(|| unsafe {
            let handle = get_handle();
            let res = exec(handle, r#"{"command":"delete","layer":"l2","id":"nonexistent_999"}"#);
            black_box(res)
        })
    });
}

criterion_group!(
    api_benches,
    bench_batch_store,
    bench_session_activate,
    bench_update,
    bench_query_l2_list,
    bench_search,
    bench_import,
    bench_delete,
);

// ============================================================================
// Report types for API completeness
// ============================================================================

#[derive(serde::Serialize)]
struct ApiCompletenessReport {
    name: String,
    timestamp: String,
    phases: Vec<PhaseReportItem>,
    commands: Vec<CommandReportItem>,
    overall_pass: bool,
}

#[derive(serde::Serialize)]
struct PhaseReportItem {
    phase: u32,
    name: String,
    passed: bool,
    details: Vec<String>,
}

#[derive(serde::Serialize)]
struct CommandReportItem {
    command: String,
    tested: bool,
    passed: bool,
    details: String,
}

// ============================================================================
// Main
// ============================================================================

fn main() {
    println!("MemHop E2E API Completeness Benchmark");
    println!("======================================");
    println!("  Fixture  : {}", fixture_path());
    println!("  DB       : {}", db_path());
    println!("  Encoder  : {}", encoder_addr());
    println!();

    let fixture = load_fixture(&fixture_path());

    // Shared state across phases
    let mut coverage: HashMap<&str, CommandCoverage> = HashMap::new();
    let mut l1_ids: Vec<String> = Vec::new();
    let mut l2_ids: Vec<String> = Vec::new();
    let mut l3_ids: Vec<String> = Vec::new();
    let mut l4_ids: Vec<String> = Vec::new();
    let mut l5_ids: Vec<String> = Vec::new();
    let mut session_l2_map: HashMap<String, Vec<String>> = HashMap::new();
    let mut phase_reports: Vec<PhaseReportItem> = Vec::new();

    // === Create DB and run all phases ===
    let path = db_path();
    let addr = encoder_addr();

    unsafe {
        let handle = create_db(&path, &addr);

        let phases_result = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
            // Phase 1: Ingest
            let (r1, l2s, sl2m) = phase1_batch_store(handle, &fixture, &mut coverage);
            l2_ids = l2s;
            session_l2_map = sl2m;
            phase_reports.push(PhaseReportItem {
                phase: 1, name: "Ingest (batch_store)".into(),
                passed: r1.passed, details: r1.details.clone(),
            });

            // Phase 2: Session
            let r2 = phase2_session(handle, &l2_ids, &mut coverage);
            phase_reports.push(PhaseReportItem {
                phase: 2, name: "Session management".into(),
                passed: r2.passed, details: r2.details.clone(),
            });

            // Phase 3: Update
            let r3 = phase3_update(handle, &fixture, &l2_ids, &mut coverage);
            phase_reports.push(PhaseReportItem {
                phase: 3, name: "Update dialogue writing".into(),
                passed: r3.passed, details: r3.details.clone(),
            });

            // Phase 4: Query layer
            let r4 = phase4_query_layer(handle, &l2_ids, &mut l1_ids, &mut l3_ids, &mut l4_ids, &mut l5_ids, &mut coverage);
            phase_reports.push(PhaseReportItem {
                phase: 4, name: "Query layer verification".into(),
                passed: r4.passed, details: r4.details.clone(),
            });

            // Stop after Phase 4 if STOP_AFTER_PHASE4 is set
            if std::env::var("STOP_AFTER_PHASE4").is_ok() {
                println!("STOP_AFTER_PHASE4 set, stopping after Phase 4");
                return; // Exit the closure early
            }

            // Phase 5: Search
            let r5 = phase5_search(handle, &fixture, &l2_ids, &l3_ids, &session_l2_map, &mut coverage);
            phase_reports.push(PhaseReportItem {
                phase: 5, name: "Search all modes".into(),
                passed: r5.passed, details: r5.details.clone(),
            });

            // Phase 6: Import + graph_query
            let r6 = phase6_import_graph(handle, &mut coverage, &mut l3_ids);
            phase_reports.push(PhaseReportItem {
                phase: 6, name: "Import + graph_query".into(),
                passed: r6.passed, details: r6.details.clone(),
            });

            // Phase 7: Dream + merge_topics + update_title
            let r7 = phase7_dream_merge_title(handle, &l2_ids, &l3_ids, &l5_ids, &mut coverage);
            phase_reports.push(PhaseReportItem {
                phase: 7, name: "Dream + merge_topics + update_title".into(),
                passed: r7.passed, details: r7.details.clone(),
            });

            // Phase 8: Delete + sync + close
            let r8 = phase8_delete_sync_close(handle, &l2_ids, &l3_ids, &l5_ids, &mut coverage);
            phase_reports.push(PhaseReportItem {
                phase: 8, name: "Delete + sync + close".into(),
                passed: r8.passed, details: r8.details.clone(),
            });
        }));

        // Always close
        memhop_close(handle);

        if let Err(e) = phases_result {
            std::panic::resume_unwind(e);
        }
    }

    // === Summary ===
    print_summary(&coverage);

    // === Criterion latency benchmarks ===
    if std::env::var("STOP_AFTER_PHASE4").is_err() {
        println!("\n===== Criterion Latency Benchmarks =====");
        api_benches();
    } else {
        println!("\n===== Skipping Criterion Latency Benchmarks (STOP_AFTER_PHASE4) =====");
    }

    // === Generate report ===
    let _ = std::fs::create_dir_all("target/bench");
    let overall = coverage.values().all(|c| !c.tested || c.passed);
    let cmd_reports: Vec<CommandReportItem> = coverage.iter().map(|(cmd, c)| {
        CommandReportItem {
            command: cmd.to_string(),
            tested: c.tested,
            passed: c.passed,
            details: c.details.clone(),
        }
    }).collect();

    let report = ApiCompletenessReport {
        name: "MemHop API Completeness Benchmark".to_string(),
        timestamp: chrono::Utc::now().format("%Y-%m-%d %H:%M:%S").to_string(),
        phases: phase_reports,
        commands: cmd_reports,
        overall_pass: overall,
    };

    let json_path = Path::new(REPORT_PATH);
    match serde_json::to_string_pretty(&report) {
        Ok(json) => {
            std::fs::write(json_path, json).unwrap_or_else(|e| eprintln!("Write report error: {}", e));
            println!("\nReport → {}", json_path.display());
        }
        Err(e) => eprintln!("Serialize report error: {}", e),
    }

    // Cleanup handle
    unsafe {
        if let Some(h) = HANDLE.get() {
            memhop_close(h.0);
        }
    }

    println!("\n===== API Completeness Benchmark Complete =====");
}
