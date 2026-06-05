//! MemHop v0.18.1 MCP Server — 5层记忆架构的 JSON-RPC 2.0 接口。
//! 双通道检索：BM25（始终可用）+ HNSW 语义向量 + 双编码器路由 (zh/en)。

use memhop::{Brain, BrainConfig, Layer, RecallRequest, ShelfDomain, StoreBatch, StoreItem};
use serde_json::{Value, json};
use std::collections::HashMap;
use std::fs::{self, File};
use std::io::Write;
use std::sync::{Arc, LazyLock, Mutex};
use tokio::io::{AsyncBufReadExt, AsyncWriteExt, BufReader};
use tokio::net::{UnixListener, UnixStream};

const VERSION: &str = "0.18.1";
const LOCK_FILE: &str = "/tmp/memhop-mcp-server.lock";

/// 检查是否已有进程在运行，使用文件锁实现单实例
fn check_single_instance() -> Result<(), String> {
    // 检查锁文件是否存在
    if fs::metadata(LOCK_FILE).is_ok() {
        // 读取锁文件中的 PID
        if let Ok(pid_str) = fs::read_to_string(LOCK_FILE)
            && let Ok(pid) = pid_str.trim().parse::<u32>()
            && is_process_running(pid)
        {
            return Err(format!(
                "memhop-mcp-server is already running (PID: {}). \
                 Remove {} to force start.",
                pid, LOCK_FILE
            ));
        }
        // 进程已不存在，删除旧的锁文件
        let _ = fs::remove_file(LOCK_FILE);
    }
    
    // 写入当前进程的 PID
    let pid = std::process::id();
    let mut file = File::create(LOCK_FILE)
        .map_err(|e| format!("Failed to create lock file: {}", e))?;
    file.write_all(pid.to_string().as_bytes())
        .map_err(|e| format!("Failed to write lock file: {}", e))?;
    
    Ok(())
}

/// 检查进程是否在运行
fn is_process_running(pid: u32) -> bool {
    // 在 macOS/Linux 上，发送信号 0 检查进程是否存在
    // 信号 0 不会实际发送信号，只是检查权限和进程存在性
    unsafe { libc::kill(pid as i32, 0) == 0 }
}

/// agent_id → Arc<Mutex<Brain>> 的全局缓存，避免每 RPC 重复 mmap 4 个 LMDB。
static BRAIN_CACHE: LazyLock<Mutex<HashMap<String, Arc<Mutex<Brain>>>>> =
    LazyLock::new(|| Mutex::new(HashMap::new()));

fn error_response(id: &Value, code: i64, message: &str) -> Value {
    json!({"jsonrpc":"2.0","id":id,"error":{"code":code,"message":message}})
}

fn get_or_open_brain(
    agent_id: &str,
    brains_dir: &str,
    model_path: Option<String>,
) -> Result<Arc<Mutex<Brain>>, String> {
    let cache_key = format!("{}/{}", brains_dir, agent_id);
    let mut cache = BRAIN_CACHE.lock().map_err(|e| e.to_string())?;
    if let Some(brain) = cache.get(&cache_key) {
        return Ok(brain.clone());
    }
    let cfg = BrainConfig {
        brains_dir: cache_key.clone(),
        agent_id: agent_id.to_string(),
        model_path,
    };
    let brain = Brain::open(cfg).map_err(|e| e.to_string())?;
    let brain = Arc::new(Mutex::new(brain));
    cache.insert(cache_key, brain.clone());
    Ok(brain)
}

#[tokio::main]
async fn main() {
    // 检查单实例
    if let Err(e) = check_single_instance() {
        eprintln!("ERROR: {}", e);
        std::process::exit(1);
    }
    
    // 注册退出时清理锁文件
    ctrlc::set_handler(move || {
        let _ = fs::remove_file(LOCK_FILE);
        std::process::exit(0);
    }).expect("Error setting Ctrl-C handler");
    
    let socket_path =
        std::env::var("MEMHOP_SOCKET").unwrap_or_else(|_| "/tmp/memhop.sock".to_string());
    let _ = std::fs::remove_file(&socket_path);
    let brains_dir =
        std::env::var("MEMHOP_BRAINS_DIR").unwrap_or_else(|_| "./memhop_brains".to_string());
    // v0.16.0: 单编码器模型路径
    let model_path = std::env::var("MEMHOP_MODEL_PATH").ok();

    if let Some(ref mp) = model_path {
        eprintln!("memhop-mcp-server: model_path='{}'", mp);
    }

    let listener = UnixListener::bind(&socket_path).unwrap_or_else(|e| {
        eprintln!("memhop-mcp-server: bind error {}: {}", socket_path, e);
        std::process::exit(1);
    });
    eprintln!(
        "memhop-mcp-server v{} listening on {}",
        VERSION, socket_path
    );

    loop {
        tokio::select! {
            result = listener.accept() => {
                if let Ok((stream, _)) = result {
                    let bd = brains_dir.clone();
                    let mp = model_path.clone();
                    tokio::spawn(handle(stream, bd, mp));
                }
            }
        }
    }
}

async fn handle(stream: UnixStream, brains_dir: String, model_path: Option<String>) {
    let (reader, mut writer) = stream.into_split();
    let mut buf = BufReader::new(reader);
    let mut line = String::new();

    loop {
        line.clear();
        match buf.read_line(&mut line).await {
            Ok(0) => break,
            Ok(_) => {}
            Err(_) => break,
        }

        let req: Value = match serde_json::from_str(&line) {
            Ok(v) => v,
            Err(_) => {
                let err = json!({"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"Parse error"}});
                let mut resp = serde_json::to_string(&err).unwrap_or_default();
                resp.push('\n');
                let _ = writer.write_all(resp.as_bytes()).await;
                continue;
            }
        };

        let id = req.get("id").cloned().unwrap_or(Value::Null);
        let method = req.get("method").and_then(|v| v.as_str()).unwrap_or("");
        let params = req
            .get("params")
            .and_then(|v| v.as_object())
            .cloned()
            .unwrap_or_default();
        let agent_id = params
            .get("agent_id")
            .and_then(|v| v.as_str())
            .unwrap_or("default");

        let response = match method {
            "memhop_batch_store" => {
                handle_batch_store(&id, agent_id, &brains_dir, &model_path, &params)
            }
            "memhop_recall" => handle_recall(&id, agent_id, &brains_dir, &model_path, &params),
            "memhop_consolidate" => handle_dream(&id, agent_id, &brains_dir, &model_path),
            "memhop_dream" => handle_dream(&id, agent_id, &brains_dir, &model_path),
            "memhop_organize" => handle_organize(&id, agent_id, &brains_dir, &model_path, &params),
            "memhop_mount_shelf" => {
                handle_mount_shelf(&id, agent_id, &brains_dir, &model_path, &params)
            }
            "memhop_unmount_shelf" => {
                handle_unmount_shelf(&id, agent_id, &brains_dir, &model_path, &params)
            }
            "memhop_health" => {
                json!({"jsonrpc":"2.0","id":id,"result":{"status":"ok","version":VERSION}})
            }
            "memhop_get_profile" => handle_get_profile(&id, agent_id, &brains_dir, &model_path),
            "memhop_set_profile" => {
                handle_set_profile(&id, agent_id, &brains_dir, &model_path, &params)
            }
            "memhop_get_activated" => {
                handle_get_activated(&id, agent_id, &brains_dir, &model_path, &params)
            }
            "memhop_activate" => handle_activate(&id, agent_id, &brains_dir, &model_path, &params),
            "memhop_deactivate" => {
                handle_deactivate(&id, agent_id, &brains_dir, &model_path, &params)
            }
            "memhop_get_l4_raw" => {
                handle_get_l4_raw(&id, agent_id, &brains_dir, &model_path, &params)
            }
            "memhop_list_l3_paths" => handle_list_l3_paths(&id, agent_id, &brains_dir, &model_path),
            "memhop_list_topics" => handle_list_topics(&id, agent_id, &brains_dir, &model_path),
            "memhop_re_search" => {
                handle_re_search(&id, agent_id, &brains_dir, &model_path, &params)
            }
            "memhop_list_shelf" => handle_list_shelf(&id, agent_id, &brains_dir, &model_path),
            "memhop_update_topic" => {
                handle_update_topic(&id, agent_id, &brains_dir, &model_path, &params)
            }
            "memhop_set_l0" => handle_set_l0(&id, agent_id, &brains_dir, &model_path, &params),
            "memhop_feedback" => handle_feedback(&id, agent_id, &brains_dir, &model_path, &params),
            "memhop_stats" => handle_stats(&id, agent_id, &brains_dir, &model_path),
            _ => error_response(&id, -32601, &format!("unknown method: {}", method)),
        };

        let mut resp = serde_json::to_string(&response).unwrap_or_default();
        resp.push('\n');
        let _ = writer.write_all(resp.as_bytes()).await;
    }
}

fn handle_batch_store(
    id: &Value,
    agent_id: &str,
    brains_dir: &str,
    model_path: &Option<String>,
    params: &serde_json::Map<String, Value>,
) -> Value {
    let brain = match get_or_open_brain(agent_id, brains_dir, model_path.clone()) {
        Ok(b) => b,
        Err(e) => return error_response(id, -32000, &e),
    };

    let items = match params.get("items").and_then(|v| v.as_array()) {
        Some(arr) => {
            let mut result = Vec::with_capacity(arr.len());
            for item in arr {
                let text = match item.get("text").and_then(|v| v.as_str()) {
                    Some(t) => t.to_string(),
                    None => return error_response(id, -32602, "items[].text is required"),
                };
                let source = item
                    .get("source")
                    .and_then(|v| v.as_str())
                    .unwrap_or("chat")
                    .to_string();
                result.push(StoreItem {
                    text,
                    source,
                    turn_id: item
                        .get("turn_id")
                        .and_then(|v| v.as_str())
                        .map(|s| s.to_string()),
                    session_id: item
                        .get("session_id")
                        .and_then(|v| v.as_str())
                        .map(|s| s.to_string()),
                    topic_label: item
                        .get("topic_label")
                        .and_then(|v| v.as_str())
                        .map(|s| s.to_string()),
                    llm_keywords: item
                        .get("llm_keywords")
                        .and_then(|v| v.as_array())
                        .map(|a| {
                            a.iter()
                                .filter_map(|v| v.as_str().map(|s| s.to_string()))
                                .collect()
                        }),
                    llm_compressed_summary: item
                        .get("llm_compressed_summary")
                        .and_then(|v| v.as_str())
                        .map(|s| s.to_string()),
                    valence: item.get("valence").and_then(|v| v.as_f64()),
                    arousal: item.get("arousal").and_then(|v| v.as_f64()),
                    chain_parent_id: item
                        .get("chain_parent_id")
                        .and_then(|v| v.as_str())
                        .map(|s| s.to_string()),
                    chain_label: item
                        .get("chain_label")
                        .and_then(|v| v.as_str())
                        .map(|s| s.to_string()),
                    domain_id: item
                        .get("domain_id")
                        .and_then(|v| v.as_str())
                        .map(|s| s.to_string()),
                    importance: item
                        .get("importance")
                        .and_then(|v| v.as_f64())
                        .map(|v| v as f32),
                });
            }
            result
        }
        None => Vec::new(),
    };

    let mut guard = match brain.lock() {
        Ok(g) => g,
        Err(e) => return error_response(id, -32000, &e.to_string()),
    };
    match guard.batch_store(StoreBatch { items }) {
        Ok(report) => json!({"jsonrpc":"2.0","id":id,"result":report}),
        Err(e) => error_response(id, -32000, &e.to_string()),
    }
}

fn handle_recall(
    id: &Value,
    agent_id: &str,
    brains_dir: &str,
    model_path: &Option<String>,
    params: &serde_json::Map<String, Value>,
) -> Value {
    let brain = match get_or_open_brain(agent_id, brains_dir, model_path.clone()) {
        Ok(b) => b,
        Err(e) => return error_response(id, -32000, &e),
    };

    let query = params
        .get("query")
        .and_then(|v| v.as_str())
        .unwrap_or("")
        .to_string();
    let max_results = params
        .get("max_results")
        .and_then(|v| v.as_u64())
        .unwrap_or(10) as usize;

    let target_layers = params
        .get("target_layers")
        .and_then(|v| v.as_array())
        .map(|arr| {
            arr.iter()
                .filter_map(|v| v.as_str())
                .filter_map(|s| match s {
                    "L1" => Some(Layer::L1),
                    "L2" => Some(Layer::L2),
                    "L3" => Some(Layer::L3),
                    "L4" => Some(Layer::L4),
                    _ => None,
                })
                .collect()
        })
        .unwrap_or_else(|| vec![Layer::L1, Layer::L2, Layer::L4]);

    let time_range = params
        .get("time_range")
        .and_then(|v| v.as_array())
        .and_then(|arr| {
            if arr.len() == 2 {
                let start = arr[0].as_i64()?;
                let end = arr[1].as_i64()?;
                Some((start, end))
            } else {
                None
            }
        });

    let spread_depth = params
        .get("spread_depth")
        .and_then(|v| v.as_u64())
        .map(|v| v as usize);
    let topic_filter = params
        .get("topic_filter")
        .and_then(|v| v.as_str())
        .map(|s| s.to_string());

    let exclude_ids = params
        .get("exclude_ids")
        .and_then(|v| v.as_array())
        .map(|arr| {
            arr.iter()
                .filter_map(|v| v.as_str().map(|s| s.to_string()))
                .collect()
        })
        .unwrap_or_default();

    let exclude_topic_ids = params
        .get("exclude_topic_ids")
        .and_then(|v| v.as_array())
        .map(|arr| {
            arr.iter()
                .filter_map(|v| v.as_str().map(|s| s.to_string()))
                .collect()
        })
        .unwrap_or_default();

    let l3_domain_id = params
        .get("l3_domain_id")
        .and_then(|v| v.as_str())
        .map(|s| s.to_string());
    let l2_topic_id = params
        .get("l2_topic_id")
        .and_then(|v| v.as_str())
        .map(|s| s.to_string());
    let session_id = params
        .get("session_id")
        .and_then(|v| v.as_str())
        .map(|s| s.to_string());

    let req = RecallRequest {
        query,
        max_results,
        target_layers,
        time_range,
        spread_depth,
        topic_filter,
        exclude_ids,
        exclude_topic_ids,
        l3_domain_id,
        l2_topic_id,
        session_id,
        time_decay_lambda: params
            .get("time_decay_lambda")
            .and_then(|v| v.as_f64())
            .map(|v| v as f32),
    };

    let mut guard = match brain.lock() {
        Ok(g) => g,
        Err(e) => return error_response(id, -32000, &e.to_string()),
    };
    match guard.recall(&req) {
        Ok(resp) => json!({"jsonrpc":"2.0","id":id,"result":resp}),
        Err(e) => error_response(id, -32000, &e.to_string()),
    }
}

fn handle_dream(
    id: &Value,
    agent_id: &str,
    brains_dir: &str,
    model_path: &Option<String>,
) -> Value {
    let brain = match get_or_open_brain(agent_id, brains_dir, model_path.clone()) {
        Ok(b) => b,
        Err(e) => return error_response(id, -32000, &e),
    };
    let mut guard = match brain.lock() {
        Ok(g) => g,
        Err(e) => return error_response(id, -32000, &e.to_string()),
    };
    match guard.consolidate() {
        Ok(report) => json!({"jsonrpc":"2.0","id":id,"result":report}),
        Err(e) => error_response(id, -32000, &e.to_string()),
    }
}

fn handle_organize(
    id: &Value,
    agent_id: &str,
    brains_dir: &str,
    model_path: &Option<String>,
    params: &serde_json::Map<String, Value>,
) -> Value {
    let brain = match get_or_open_brain(agent_id, brains_dir, model_path.clone()) {
        Ok(b) => b,
        Err(e) => return error_response(id, -32000, &e),
    };
    let node_id = match params.get("node_id").and_then(|v| v.as_str()) {
        Some(s) => s.to_string(),
        None => return error_response(id, -32602, "node_id is required"),
    };

    let mut guard = match brain.lock() {
        Ok(g) => g,
        Err(e) => return error_response(id, -32000, &e.to_string()),
    };
    match guard.organize_node(&node_id) {
        Ok(_) => json!({"jsonrpc":"2.0","id":id,"result":{"status":"ok"}}),
        Err(e) => error_response(id, -32000, &e.to_string()),
    }
}

fn handle_mount_shelf(
    id: &Value,
    agent_id: &str,
    brains_dir: &str,
    model_path: &Option<String>,
    params: &serde_json::Map<String, Value>,
) -> Value {
    let brain = match get_or_open_brain(agent_id, brains_dir, model_path.clone()) {
        Ok(b) => b,
        Err(e) => return error_response(id, -32000, &e),
    };
    let dir_path = match params.get("path").and_then(|v| v.as_str()) {
        Some(s) => s.to_string(),
        None => return error_response(id, -32602, "path is required"),
    };
    let domain_name = match params.get("name").and_then(|v| v.as_str()) {
        Some(s) => s.to_string(),
        None => return error_response(id, -32602, "name is required"),
    };
    let doc_type_str = params
        .get("doc_type")
        .and_then(|v| v.as_str())
        .unwrap_or("generic");
    let domain = match doc_type_str {
        "code" => ShelfDomain::Code,
        "doc" => ShelfDomain::Doc,
        "book" => ShelfDomain::Book,
        "paper" => ShelfDomain::Paper,
        _ => ShelfDomain::Generic,
    };

    let mut guard = match brain.lock() {
        Ok(g) => g,
        Err(e) => return error_response(id, -32000, &e.to_string()),
    };
    match memhop::shelf::mount(&mut guard, &dir_path, domain, &domain_name) {
        Ok(meta) => json!({"jsonrpc":"2.0","id":id,"result":meta}),
        Err(e) => error_response(id, -32000, &e.to_string()),
    }
}

fn handle_unmount_shelf(
    id: &Value,
    agent_id: &str,
    brains_dir: &str,
    model_path: &Option<String>,
    params: &serde_json::Map<String, Value>,
) -> Value {
    let brain = match get_or_open_brain(agent_id, brains_dir, model_path.clone()) {
        Ok(b) => b,
        Err(e) => return error_response(id, -32000, &e),
    };
    let domain_id = match params.get("domain_id").and_then(|v| v.as_str()) {
        Some(s) => s.to_string(),
        None => return error_response(id, -32602, "domain_id is required"),
    };

    let mut guard = match brain.lock() {
        Ok(g) => g,
        Err(e) => return error_response(id, -32000, &e.to_string()),
    };
    match memhop::shelf::unmount(&mut guard, &domain_id) {
        Ok(_) => json!({"jsonrpc":"2.0","id":id,"result":{"status":"ok"}}),
        Err(e) => error_response(id, -32000, &e.to_string()),
    }
}

fn handle_get_profile(
    id: &Value,
    agent_id: &str,
    brains_dir: &str,
    model_path: &Option<String>,
) -> Value {
    let brain = match get_or_open_brain(agent_id, brains_dir, model_path.clone()) {
        Ok(b) => b,
        Err(e) => return error_response(id, -32000, &e),
    };
    let guard = match brain.lock() {
        Ok(g) => g,
        Err(e) => return error_response(id, -32000, &e.to_string()),
    };
    match guard.get_l0_profile() {
        Ok(profile) => json!({"jsonrpc":"2.0","id":id,"result":profile}),
        Err(e) => error_response(id, -32000, &e.to_string()),
    }
}

fn handle_set_profile(
    id: &Value,
    agent_id: &str,
    brains_dir: &str,
    model_path: &Option<String>,
    params: &serde_json::Map<String, Value>,
) -> Value {
    let brain = match get_or_open_brain(agent_id, brains_dir, model_path.clone()) {
        Ok(b) => b,
        Err(e) => return error_response(id, -32000, &e),
    };
    let catid = params
        .get("catid")
        .and_then(|v| v.as_str())
        .map(|s| s.to_string());
    let role_name = params
        .get("role_name")
        .and_then(|v| v.as_str())
        .map(|s| s.to_string());
    let role = params
        .get("role")
        .and_then(|v| v.as_str())
        .map(|s| s.to_string());
    let position = params
        .get("position")
        .and_then(|v| v.as_str())
        .map(|s| s.to_string());
    let traits: HashMap<String, String> = params
        .get("traits")
        .and_then(|v| v.as_object())
        .map(|obj| {
            obj.iter()
                .filter_map(|(k, v)| v.as_str().map(|s| (k.clone(), s.to_string())))
                .collect()
        })
        .unwrap_or_default();

    let mut guard = match brain.lock() {
        Ok(g) => g,
        Err(e) => return error_response(id, -32000, &e.to_string()),
    };
    match guard.set_l0_profile(catid, role_name, role, position, traits) {
        Ok(()) => json!({"jsonrpc":"2.0","id":id,"result":{"status":"ok"}}),
        Err(e) => error_response(id, -32000, &e.to_string()),
    }
}

fn handle_get_activated(
    id: &Value,
    agent_id: &str,
    brains_dir: &str,
    model_path: &Option<String>,
    _params: &serde_json::Map<String, Value>,
) -> Value {
    let brain = match get_or_open_brain(agent_id, brains_dir, model_path.clone()) {
        Ok(b) => b,
        Err(e) => return error_response(id, -32000, &e),
    };
    let mut guard = match brain.lock() {
        Ok(g) => g,
        Err(e) => return error_response(id, -32000, &e.to_string()),
    };
    let list = guard.get_activated_topics();
    json!({"jsonrpc":"2.0","id":id,"result":list})
}

fn handle_activate(
    id: &Value,
    agent_id: &str,
    brains_dir: &str,
    model_path: &Option<String>,
    params: &serde_json::Map<String, Value>,
) -> Value {
    let brain = match get_or_open_brain(agent_id, brains_dir, model_path.clone()) {
        Ok(b) => b,
        Err(e) => return error_response(id, -32000, &e),
    };
    let session_id = params
        .get("session_id")
        .and_then(|v| v.as_str())
        .unwrap_or("default");
    let topic_id = match params.get("topic_id").and_then(|v| v.as_str()) {
        Some(s) => s.to_string(),
        None => return error_response(id, -32602, "topic_id is required"),
    };
    let ttl_ms = params
        .get("ttl_ms")
        .and_then(|v| v.as_i64())
        .unwrap_or(3_600_000);

    let mut guard = match brain.lock() {
        Ok(g) => g,
        Err(e) => return error_response(id, -32000, &e.to_string()),
    };
    guard.session_mgr.activate(session_id, &topic_id, ttl_ms);
    json!({"jsonrpc":"2.0","id":id,"result":{"status":"ok"}})
}

fn handle_deactivate(
    id: &Value,
    agent_id: &str,
    brains_dir: &str,
    model_path: &Option<String>,
    params: &serde_json::Map<String, Value>,
) -> Value {
    let brain = match get_or_open_brain(agent_id, brains_dir, model_path.clone()) {
        Ok(b) => b,
        Err(e) => return error_response(id, -32000, &e),
    };
    let session_id = params
        .get("session_id")
        .and_then(|v| v.as_str())
        .unwrap_or("default");
    let topic_id = match params.get("topic_id").and_then(|v| v.as_str()) {
        Some(s) => s.to_string(),
        None => return error_response(id, -32602, "topic_id is required"),
    };

    let mut guard = match brain.lock() {
        Ok(g) => g,
        Err(e) => return error_response(id, -32000, &e.to_string()),
    };
    guard.session_mgr.deactivate(session_id, &topic_id);
    json!({"jsonrpc":"2.0","id":id,"result":{"status":"ok"}})
}

fn handle_get_l4_raw(
    id: &Value,
    agent_id: &str,
    brains_dir: &str,
    model_path: &Option<String>,
    params: &serde_json::Map<String, Value>,
) -> Value {
    let brain = match get_or_open_brain(agent_id, brains_dir, model_path.clone()) {
        Ok(b) => b,
        Err(e) => return error_response(id, -32000, &e),
    };
    let doc_id = match params.get("doc_id").and_then(|v| v.as_str()) {
        Some(s) => s.to_string(),
        None => return error_response(id, -32602, "doc_id is required"),
    };

    let guard = match brain.lock() {
        Ok(g) => g,
        Err(e) => return error_response(id, -32000, &e.to_string()),
    };
    match guard.get_l4_raw(&doc_id) {
        Ok(doc) => json!({"jsonrpc":"2.0","id":id,"result":doc}),
        Err(e) => error_response(id, -32000, &e.to_string()),
    }
}

fn handle_list_l3_paths(
    id: &Value,
    agent_id: &str,
    brains_dir: &str,
    model_path: &Option<String>,
) -> Value {
    let brain = match get_or_open_brain(agent_id, brains_dir, model_path.clone()) {
        Ok(b) => b,
        Err(e) => return error_response(id, -32000, &e),
    };
    let guard = match brain.lock() {
        Ok(g) => g,
        Err(e) => return error_response(id, -32000, &e.to_string()),
    };
    match guard.list_l3_paths() {
        Ok(paths) => json!({"jsonrpc":"2.0","id":id,"result":paths}),
        Err(e) => error_response(id, -32000, &e.to_string()),
    }
}

fn handle_list_topics(
    id: &Value,
    agent_id: &str,
    brains_dir: &str,
    model_path: &Option<String>,
) -> Value {
    let brain = match get_or_open_brain(agent_id, brains_dir, model_path.clone()) {
        Ok(b) => b,
        Err(e) => return error_response(id, -32000, &e),
    };
    let guard = match brain.lock() {
        Ok(g) => g,
        Err(e) => return error_response(id, -32000, &e.to_string()),
    };
    match guard.list_topics() {
        Ok(topics) => json!({"jsonrpc":"2.0","id":id,"result":topics}),
        Err(e) => error_response(id, -32000, &e.to_string()),
    }
}

fn handle_re_search(
    id: &Value,
    agent_id: &str,
    brains_dir: &str,
    model_path: &Option<String>,
    params: &serde_json::Map<String, Value>,
) -> Value {
    let brain = match get_or_open_brain(agent_id, brains_dir, model_path.clone()) {
        Ok(b) => b,
        Err(e) => return error_response(id, -32000, &e),
    };
    // Parse params same as recall
    let query = params
        .get("query")
        .and_then(|v| v.as_str())
        .unwrap_or("")
        .to_string();
    let max_results = params
        .get("max_results")
        .and_then(|v| v.as_u64())
        .unwrap_or(10) as usize;
    let target_layers = params
        .get("target_layers")
        .and_then(|v| v.as_array())
        .map(|arr| {
            arr.iter()
                .filter_map(|v| v.as_str())
                .filter_map(|s| match s {
                    "L1" => Some(Layer::L1),
                    "L2" => Some(Layer::L2),
                    "L3" => Some(Layer::L3),
                    "L4" => Some(Layer::L4),
                    _ => None,
                })
                .collect()
        })
        .unwrap_or_else(|| vec![Layer::L1, Layer::L2, Layer::L4]);
    let exclude_ids = params
        .get("exclude_ids")
        .and_then(|v| v.as_array())
        .map(|arr| {
            arr.iter()
                .filter_map(|v| v.as_str().map(|s| s.to_string()))
                .collect()
        })
        .unwrap_or_default();
    let exclude_topic_ids = params
        .get("exclude_topic_ids")
        .and_then(|v| v.as_array())
        .map(|arr| {
            arr.iter()
                .filter_map(|v| v.as_str().map(|s| s.to_string()))
                .collect()
        })
        .unwrap_or_default();
    let req = RecallRequest {
        query,
        max_results,
        target_layers,
        time_range: None,
        spread_depth: params
            .get("spread_depth")
            .and_then(|v| v.as_u64())
            .map(|v| v as usize),
        topic_filter: params
            .get("topic_filter")
            .and_then(|v| v.as_str())
            .map(|s| s.to_string()),
        exclude_ids,
        exclude_topic_ids,
        l3_domain_id: params
            .get("l3_domain_id")
            .and_then(|v| v.as_str())
            .map(|s| s.to_string()),
        l2_topic_id: params
            .get("l2_topic_id")
            .and_then(|v| v.as_str())
            .map(|s| s.to_string()),
        session_id: params
            .get("session_id")
            .and_then(|v| v.as_str())
            .map(|s| s.to_string()),
        time_decay_lambda: params
            .get("time_decay_lambda")
            .and_then(|v| v.as_f64())
            .map(|v| v as f32),
    };
    let mut guard = match brain.lock() {
        Ok(g) => g,
        Err(e) => return error_response(id, -32000, &e.to_string()),
    };
    match guard.re_search(&req) {
        Ok(resp) => json!({"jsonrpc":"2.0","id":id,"result":resp}),
        Err(e) => error_response(id, -32000, &e.to_string()),
    }
}

fn handle_list_shelf(
    id: &Value,
    agent_id: &str,
    brains_dir: &str,
    model_path: &Option<String>,
) -> Value {
    let brain = match get_or_open_brain(agent_id, brains_dir, model_path.clone()) {
        Ok(b) => b,
        Err(e) => return error_response(id, -32000, &e),
    };
    let guard = match brain.lock() {
        Ok(g) => g,
        Err(e) => return error_response(id, -32000, &e.to_string()),
    };
    match memhop::shelf::list(&guard) {
        Ok(shelves) => json!({"jsonrpc":"2.0","id":id,"result":shelves}),
        Err(e) => error_response(id, -32000, &e.to_string()),
    }
}

// ── v0.16.0: memhop_feedback ───────────────────────────────────

fn handle_feedback(
    id: &Value,
    agent_id: &str,
    brains_dir: &str,
    model_path: &Option<String>,
    params: &serde_json::Map<String, Value>,
) -> Value {
    let brain = match get_or_open_brain(agent_id, brains_dir, model_path.clone()) {
        Ok(b) => b,
        Err(e) => return error_response(id, -32000, &e),
    };

    let result_ids: Vec<String> = params
        .get("result_ids")
        .and_then(|v| v.as_array())
        .map(|arr| {
            arr.iter()
                .filter_map(|v| v.as_str().map(|s| s.to_string()))
                .collect()
        })
        .unwrap_or_default();

    let relevant = params
        .get("relevant")
        .and_then(|v| v.as_bool())
        .unwrap_or(true);
    let session_id = params
        .get("session_id")
        .and_then(|v| v.as_str())
        .unwrap_or("default");

    let mut guard = match brain.lock() {
        Ok(g) => g,
        Err(e) => return error_response(id, -32000, &e.to_string()),
    };

    // Feedback logic: find each result's topic, adjust only matched topics once
    let active_topic_ids = guard.session_mgr.get_active_topic_ids(session_id);
    let delta = if relevant { 0.1f32 } else { -0.1f32 };

    // Build result_id set for fast lookup
    let rid_set: std::collections::HashSet<&str> = result_ids.iter().map(|s| s.as_str()).collect();

    // Find topics that contain any result_id
    let mut matched_topics: std::collections::HashSet<String> = std::collections::HashSet::new();
    if let Ok(topics) = guard.list_topics() {
        for topic in &topics {
            for nid in &topic.node_ids {
                if rid_set.contains(nid.as_str()) && active_topic_ids.contains(&topic.id) {
                    matched_topics.insert(topic.id.clone());
                    break;
                }
            }
        }
    }

    // If no specific topic matched, fall back to all active topics (adjust once each)
    let topics_to_adjust: Vec<String> = if matched_topics.is_empty() {
        active_topic_ids
    } else {
        matched_topics.into_iter().collect()
    };

    let adjusted = topics_to_adjust.len() as u32;
    for tid in &topics_to_adjust {
        guard.session_mgr.adjust_activation(session_id, tid, delta);
    }

    json!({"jsonrpc":"2.0","id":id,"result":{"adjusted": adjusted, "relevant": relevant}})
}

// ── v0.16.0: memhop_stats ──────────────────────────────────────

fn handle_stats(
    id: &Value,
    agent_id: &str,
    brains_dir: &str,
    model_path: &Option<String>,
) -> Value {
    let brain = match get_or_open_brain(agent_id, brains_dir, model_path.clone()) {
        Ok(b) => b,
        Err(e) => return error_response(id, -32000, &e),
    };
    let guard = match brain.lock() {
        Ok(g) => g,
        Err(e) => return error_response(id, -32000, &e.to_string()),
    };

    // Use BM25 index for L1 (includes all nodes, not just those with vectors)
    let l1_nodes = guard.l1.bm25.len();
    let l2_topics = guard.l2.topic_vectors.len();
    let l3_nodes = guard.l3.vector_index.len();
    let l4_docs = guard.l4.vector_index.len();
    let encoder_mode = guard.encoder.mode().to_string();
    let encoder_dim = guard.encoder.dim();

    json!({"jsonrpc":"2.0","id":id,"result":{
        "version": VERSION,
        "encoder_mode": encoder_mode,
        "encoder_dim": encoder_dim,
        "brain_stats": {
            "l1_nodes": l1_nodes,
            "l2_topics": l2_topics,
            "l3_nodes": l3_nodes,
            "l4_docs": l4_docs,
        },
        "total_engrams": l1_nodes + l2_topics + l3_nodes + l4_docs,
    }})
}

// ── v0.17.0: memhop_update_topic ────────────────────────────────

fn handle_update_topic(
    id: &Value,
    agent_id: &str,
    brains_dir: &str,
    model_path: &Option<String>,
    params: &serde_json::Map<String, Value>,
) -> Value {
    let brain = match get_or_open_brain(agent_id, brains_dir, model_path.clone()) {
        Ok(b) => b,
        Err(e) => return error_response(id, -32000, &e),
    };

    let topic_id = match params.get("topic_id").and_then(|v| v.as_str()) {
        Some(s) => s.to_string(),
        None => return error_response(id, -32602, "topic_id is required"),
    };

    let summary = params
        .get("summary")
        .and_then(|v| v.as_str())
        .map(|s| s.to_string());
    let keywords = params.get("keywords").and_then(|v| v.as_array()).map(|a| {
        a.iter()
            .filter_map(|v| v.as_str().map(|s| s.to_string()))
            .collect()
    });
    let extended_meta = params
        .get("extended_meta")
        .and_then(|v| v.as_object())
        .map(|obj| {
            obj.iter()
                .map(|(k, v)| {
                    let val = match v {
                        serde_json::Value::String(s) => s.clone(),
                        other => other.to_string(),
                    };
                    (k.clone(), val)
                })
                .collect()
        });

    let mut guard = match brain.lock() {
        Ok(g) => g,
        Err(e) => return error_response(id, -32000, &e.to_string()),
    };
    match guard.update_topic(&topic_id, summary, keywords, extended_meta) {
        Ok(()) => json!({"jsonrpc":"2.0","id":id,"result":{"status":"ok"}}),
        Err(e) => error_response(id, -32000, &e.to_string()),
    }
}

// ── v0.17.0: memhop_set_l0 ───────────────────────────────────────

fn handle_set_l0(
    id: &Value,
    agent_id: &str,
    brains_dir: &str,
    model_path: &Option<String>,
    params: &serde_json::Map<String, Value>,
) -> Value {
    let brain = match get_or_open_brain(agent_id, brains_dir, model_path.clone()) {
        Ok(b) => b,
        Err(e) => return error_response(id, -32000, &e),
    };

    let catid = params
        .get("catid")
        .and_then(|v| v.as_str())
        .map(|s| s.to_string());
    let role_name = params
        .get("role_name")
        .and_then(|v| v.as_str())
        .map(|s| s.to_string());
    let personality = params
        .get("personality")
        .and_then(|v| v.as_array())
        .map(|a| {
            a.iter()
                .filter_map(|v| v.as_str().map(|s| s.to_string()))
                .collect()
        })
        .unwrap_or_default();
    let values = params
        .get("values")
        .and_then(|v| v.as_array())
        .map(|a| {
            a.iter()
                .filter_map(|v| v.as_str().map(|s| s.to_string()))
                .collect()
        })
        .unwrap_or_default();
    let worldview = params
        .get("worldview")
        .and_then(|v| v.as_array())
        .map(|a| {
            a.iter()
                .filter_map(|v| v.as_str().map(|s| s.to_string()))
                .collect()
        })
        .unwrap_or_default();
    let traits: std::collections::HashMap<String, String> = params
        .get("traits")
        .and_then(|v| v.as_object())
        .map(|obj| {
            obj.iter()
                .filter_map(|(k, v)| v.as_str().map(|s| (k.clone(), s.to_string())))
                .collect()
        })
        .unwrap_or_default();

    let mut guard = match brain.lock() {
        Ok(g) => g,
        Err(e) => return error_response(id, -32000, &e.to_string()),
    };
    match guard.set_l0(catid, role_name, personality, values, worldview, traits) {
        Ok(()) => json!({"jsonrpc":"2.0","id":id,"result":{"status":"ok"}}),
        Err(e) => error_response(id, -32000, &e.to_string()),
    }
}
