//! MemHop v0.14 MCP Server — 4层记忆架构的 JSON-RPC 2.0 接口。

use std::collections::HashMap;
use std::sync::{Arc, Mutex, LazyLock};
use serde_json::{json, Value};
use tokio::io::{AsyncBufReadExt, AsyncWriteExt, BufReader};
use tokio::net::{UnixListener, UnixStream};
use memhop::{Brain, BrainConfig, StoreBatch, StoreItem, RecallRequest, Layer};

const VERSION: &str = "0.14.0";

/// agent_id → Arc<Mutex<Brain>> 的全局缓存，避免每 RPC 重复 mmap 4 个 LMDB。
static BRAIN_CACHE: LazyLock<Mutex<HashMap<String, Arc<Mutex<Brain>>>>> =
    LazyLock::new(|| Mutex::new(HashMap::new()));

fn error_response(id: &Value, code: i64, message: &str) -> Value {
    json!({"jsonrpc":"2.0","id":id,"error":{"code":code,"message":message}})
}

fn get_or_open_brain(agent_id: &str, brains_dir: &str) -> Result<Arc<Mutex<Brain>>, String> {
    let cache_key = format!("{}/{}", brains_dir, agent_id);
    let mut cache = BRAIN_CACHE.lock().map_err(|e| e.to_string())?;
    if let Some(brain) = cache.get(&cache_key) {
        return Ok(brain.clone());
    }
    let cfg = BrainConfig {
        brains_dir: cache_key.clone(),
        agent_id: agent_id.to_string(),
    };
    let brain = Brain::open(cfg).map_err(|e| e.to_string())?;
    let brain = Arc::new(Mutex::new(brain));
    cache.insert(cache_key, brain.clone());
    Ok(brain)
}

#[tokio::main]
async fn main() {
    let socket_path = std::env::var("MEMHOP_SOCKET")
        .unwrap_or_else(|_| "/tmp/memhop.sock".to_string());
    let _ = std::fs::remove_file(&socket_path);
    let brains_dir = std::env::var("MEMHOP_BRAINS_DIR")
        .unwrap_or_else(|_| "./memhop_brains".to_string());

    let listener = UnixListener::bind(&socket_path).unwrap_or_else(|e| {
        eprintln!("memhop-mcp-server: bind error {}: {}", socket_path, e);
        std::process::exit(1);
    });
    eprintln!("memhop-mcp-server v{} listening on {}", VERSION, socket_path);

    loop {
        tokio::select! {
            result = listener.accept() => {
                if let Ok((stream, _)) = result {
                    let bd = brains_dir.clone();
                    tokio::spawn(handle(stream, bd));
                }
            }
        }
    }
}

async fn handle(stream: UnixStream, brains_dir: String) {
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
        let params = req.get("params").and_then(|v| v.as_object()).cloned().unwrap_or_default();
        let agent_id = params.get("agent_id").and_then(|v| v.as_str()).unwrap_or("default");

        let response = match method {
            "memhop_batch_store" => handle_batch_store(&id, agent_id, &brains_dir, &params),
            "memhop_recall"      => handle_recall(&id, agent_id, &brains_dir, &params),
            "memhop_consolidate" => handle_consolidate(&id, agent_id, &brains_dir),
            "memhop_health"      => json!({"jsonrpc":"2.0","id":id,"result":{"status":"ok","version":VERSION}}),
            _ => error_response(&id, -32601, &format!("unknown method: {}", method)),
        };

        let mut resp = serde_json::to_string(&response).unwrap_or_default();
        resp.push('\n');
        let _ = writer.write_all(resp.as_bytes()).await;
    }
}

fn handle_batch_store(id: &Value, agent_id: &str, brains_dir: &str, params: &serde_json::Map<String, Value>) -> Value {
    let brain = match get_or_open_brain(agent_id, brains_dir) {
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
                let source = item.get("source").and_then(|v| v.as_str()).unwrap_or("chat").to_string();
                result.push(StoreItem {
                    text,
                    source,
                    turn_id: item.get("turn_id").and_then(|v| v.as_str()).map(|s| s.to_string()),
                    session_id: item.get("session_id").and_then(|v| v.as_str()).map(|s| s.to_string()),
                    topic_label: item.get("topic_label").and_then(|v| v.as_str()).map(|s| s.to_string()),
                    llm_keywords: item.get("llm_keywords").and_then(|v| v.as_array()).map(|a| {
                        a.iter().filter_map(|v| v.as_str().map(|s| s.to_string())).collect()
                    }),
                    llm_compressed_summary: item.get("llm_compressed_summary").and_then(|v| v.as_str()).map(|s| s.to_string()),
                    valence: item.get("valence").and_then(|v| v.as_f64()),
                    arousal: item.get("arousal").and_then(|v| v.as_f64()),
                    chain_parent_id: item.get("chain_parent_id").and_then(|v| v.as_str()).map(|s| s.to_string()),
                    chain_label: item.get("chain_label").and_then(|v| v.as_str()).map(|s| s.to_string()),
                    domain_id: item.get("domain_id").and_then(|v| v.as_str()).map(|s| s.to_string()),
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

fn handle_recall(id: &Value, agent_id: &str, brains_dir: &str, params: &serde_json::Map<String, Value>) -> Value {
    let brain = match get_or_open_brain(agent_id, brains_dir) {
        Ok(b) => b,
        Err(e) => return error_response(id, -32000, &e),
    };

    let query = params.get("query").and_then(|v| v.as_str()).unwrap_or("").to_string();
    let max_results = params.get("max_results").and_then(|v| v.as_u64()).unwrap_or(10) as usize;

    let target_layers = params.get("target_layers").and_then(|v| v.as_array()).map(|arr| {
        arr.iter().filter_map(|v| v.as_str()).filter_map(|s| match s {
            "L1" => Some(Layer::L1),
            "L2" => Some(Layer::L2),
            "L3" => Some(Layer::L3),
            "L4" => Some(Layer::L4),
            _ => None,
        }).collect()
    }).unwrap_or_else(|| vec![Layer::L1, Layer::L2, Layer::L4]);

    let time_range = params.get("time_range").and_then(|v| v.as_array()).and_then(|arr| {
        if arr.len() == 2 {
            let start = arr[0].as_i64()?;
            let end = arr[1].as_i64()?;
            Some((start, end))
        } else {
            None
        }
    });

    let req = RecallRequest {
        query,
        max_results,
        target_layers,
        time_range,
    };

    let guard = match brain.lock() {
        Ok(g) => g,
        Err(e) => return error_response(id, -32000, &e.to_string()),
    };
    match guard.recall(&req) {
        Ok(resp) => json!({"jsonrpc":"2.0","id":id,"result":resp}),
        Err(e) => error_response(id, -32000, &e.to_string()),
    }
}

fn handle_consolidate(id: &Value, agent_id: &str, brains_dir: &str) -> Value {
    let brain = match get_or_open_brain(agent_id, brains_dir) {
        Ok(b) => b,
        Err(e) => return error_response(id, -32000, &e),
    };
    let guard = match brain.lock() {
        Ok(g) => g,
        Err(e) => return error_response(id, -32000, &e.to_string()),
    };
    match guard.consolidate() {
        Ok(report) => json!({"jsonrpc":"2.0","id":id,"result":report}),
        Err(e) => error_response(id, -32000, &e.to_string()),
    }
}
