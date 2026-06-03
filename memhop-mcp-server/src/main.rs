//! MemHop v0.14 MCP Server — 4层记忆架构的 JSON-RPC 2.0 接口。

use std::collections::HashMap;
use serde_json::{json, Value};
use tokio::io::{AsyncBufReadExt, AsyncWriteExt, BufReader};
use tokio::net::{UnixListener, UnixStream};
use memhop::{Brain, BrainConfig, StoreBatch, StoreItem, RecallRequest};

const VERSION: &str = "0.14.0";

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
            Err(_) => continue,
        };

        let id = req.get("id").and_then(|v| v.as_u64()).unwrap_or(0);
        let method = req.get("method").and_then(|v| v.as_str()).unwrap_or("");
        let params = req.get("params").and_then(|v| v.as_object()).cloned().unwrap_or_default();
        let agent_id = params.get("agent_id").and_then(|v| v.as_str()).unwrap_or("default");

        let response = match method {
            "memhop_batch_store" => handle_batch_store(id, agent_id, &brains_dir, &params),
            "memhop_store"       => handle_batch_store(id, agent_id, &brains_dir, &params),
            "memhop_recall"      => handle_recall(id, agent_id, &brains_dir, &params),
            "memhop_consolidate" => handle_consolidate(id, agent_id, &brains_dir),
            "memhop_health"      => json!({"jsonrpc":"2.0","id":id,"result":{"status":"ok","version":VERSION}}),
            _ => json!({"jsonrpc":"2.0","id":id,"error":{"code":-32601,"message":format!("unknown: {}", method)}}),
        };

        let mut resp = serde_json::to_string(&response).unwrap_or_default();
        resp.push('\n');
        let _ = writer.write_all(resp.as_bytes()).await;
    }
}

fn handle_batch_store(id: u64, agent_id: &str, brains_dir: &str, params: &serde_json::Map<String, Value>) -> Value {
    let cfg = BrainConfig {
        brains_dir: format!("{}/{}", brains_dir, agent_id),
        agent_id: agent_id.to_string(),
    };

    let mut brain = match Brain::open(cfg) {
        Ok(b) => b,
        Err(e) => return json!({"jsonrpc":"2.0","id":id,"error":{"code":-1,"message":e.to_string()}}),
    };

    let items = params.get("items").and_then(|v| v.as_array()).map(|arr| {
        arr.iter().filter_map(|item| {
            let text = item.get("text")?.as_str()?.to_string();
            let source = item.get("source").and_then(|v| v.as_str()).unwrap_or("chat").to_string();
            Some(StoreItem {
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
            })
        }).collect::<Vec<StoreItem>>()
    }).unwrap_or_default();

    let batch = StoreBatch { items, agent_meta: HashMap::new() };
    match brain.batch_store(batch) {
        Ok(report) => json!({"jsonrpc":"2.0","id":id,"result":report}),
        Err(e) => json!({"jsonrpc":"2.0","id":id,"error":{"code":-1,"message":e.to_string()}}),
    }
}

fn handle_recall(id: u64, agent_id: &str, brains_dir: &str, params: &serde_json::Map<String, Value>) -> Value {
    let cfg = BrainConfig {
        brains_dir: format!("{}/{}", brains_dir, agent_id),
        agent_id: agent_id.to_string(),
    };
    let brain = match Brain::open(cfg) {
        Ok(b) => b,
        Err(e) => return json!({"jsonrpc":"2.0","id":id,"error":{"code":-1,"message":e.to_string()}}),
    };

    let query = params.get("query").and_then(|v| v.as_str()).unwrap_or("").to_string();
    let max_results = params.get("max_results").and_then(|v| v.as_u64()).unwrap_or(10) as usize;

    let req = RecallRequest {
        query,
        max_results,
        ..Default::default()
    };

    match brain.recall(&req) {
        Ok(resp) => json!({"jsonrpc":"2.0","id":id,"result":resp}),
        Err(e) => json!({"jsonrpc":"2.0","id":id,"error":{"code":-1,"message":e.to_string()}}),
    }
}

fn handle_consolidate(id: u64, agent_id: &str, brains_dir: &str) -> Value {
    let cfg = BrainConfig {
        brains_dir: format!("{}/{}", brains_dir, agent_id),
        agent_id: agent_id.to_string(),
    };
    let brain = match Brain::open(cfg) {
        Ok(b) => b,
        Err(e) => return json!({"jsonrpc":"2.0","id":id,"error":{"code":-1,"message":e.to_string()}}),
    };
    match brain.consolidate() {
        Ok(report) => json!({"jsonrpc":"2.0","id":id,"result":report}),
        Err(e) => json!({"jsonrpc":"2.0","id":id,"error":{"code":-1,"message":e.to_string()}}),
    }
}
