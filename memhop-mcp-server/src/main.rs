use std::io::{BufRead, BufReader, Write};
use serde_json::{json, Value};
use memhop::{MemHop, StoreOptions};

fn main() {
    let db_path: Option<String> = std::env::var("MEMHOP_DB_PATH").ok()
        .or_else(|| Some("/tmp/memhop-mcp.db".to_string()));
    let mut db: Option<MemHop> = None;
    let stdin = std::io::stdin();
    let reader = BufReader::new(stdin.lock());
    let mut stdout = std::io::stdout();

    for line in reader.lines() {
        let line = match line { Ok(l) => l, Err(_) => break };
        let line = line.trim().to_string();
        if line.is_empty() { continue; }

        let req: Value = match serde_json::from_str(&line) {
            Ok(v) => v,
            Err(e) => { let _ = writeln!(stdout,"{}",json!({"jsonrpc":"2.0","error":{"code":-32700,"message":format!("{}",e)},"id":null})); continue; }
        };

        let id = req.get("id").cloned().or(Some(Value::Null));
        let method = req.get("method").and_then(|m| m.as_str()).unwrap_or("");

        let result: Result<Value, String> = match method {
            "initialize" => Ok(json!({"protocolVersion":"2024-11-05","serverInfo":{"name":"memhop-mcp-server","version":"0.6.0"},"capabilities":{"tools":{}}})),
            "notifications/initialized" => continue,
            "tools/list" => Ok(tools_list()),
            "tools/call" => {
                let p = req.get("params");
                if db.is_none()
                    && let Some(ref path) = db_path {
                        match MemHop::open(path) {
                            Ok(engine) => db = Some(engine),
                            Err(e) => {
                                let resp = json!({"jsonrpc":"2.0","id":id,"error":{"code":-32000,"message":format!("failed to open database: {}", e)}});
                                let _ = writeln!(stdout, "{}", resp);
                                let _ = stdout.flush();
                                continue;
                            }
                        }
                    }
                tool_call(&mut db, p)
            }
            _ => Err(format!("Method not found: {}", method)),
        };

        let mut resp = json!({"jsonrpc":"2.0","id":id});
        match result {
            Ok(val) => { resp["result"] = val; }
            Err(e) => { resp["error"] = json!({"code":-32000,"message":e}); }
        }
        let _ = writeln!(stdout, "{}", resp);
        let _ = stdout.flush();
    }
}

fn tools_list() -> Value { json!({"tools":[
    {"name":"memhop_create_tree","description":"Create a new domain tree","inputSchema":{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}},
    {"name":"memhop_list_trees","description":"List all domain trees","inputSchema":{"type":"object","properties":{}}},
    {"name":"memhop_remove_tree","description":"Remove a domain tree","inputSchema":{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}},
    {"name":"memhop_store","description":"Store a new memory","inputSchema":{"type":"object","properties":{"text":{"type":"string"},"domain":{"type":"string"},"auto_entangle":{"type":"boolean"}},"required":["text","domain"]}},
    {"name":"memhop_recall","description":"O(1) single memory recall","inputSchema":{"type":"object","properties":{"query":{"type":"string"},"tree":{"type":"string"}},"required":["query"]}},
    {"name":"memhop_recall_topk","description":"Top-K memory recall","inputSchema":{"type":"object","properties":{"query":{"type":"string"},"k":{"type":"integer"},"tree":{"type":"string"}},"required":["query","k"]}},
    {"name":"memhop_forget","description":"Delete a memory","inputSchema":{"type":"object","properties":{"memory_id":{"type":"string"}},"required":["memory_id"]}},
    {"name":"memhop_dream","description":"Run background memory consolidation","inputSchema":{"type":"object","properties":{}}},
    {"name":"memhop_search","description":"Search memories by metadata filters","inputSchema":{"type":"object","properties":{"filters":{"type":"object"},"limit":{"type":"integer"}},"required":["filters"]}},
    {"name":"memhop_stats","description":"Get database statistics","inputSchema":{"type":"object","properties":{}}},
    {"name":"memhop_recent","description":"Get recent N memories","inputSchema":{"type":"object","properties":{"limit":{"type":"integer"},"tree":{"type":"string"}},"required":["limit"]}},
    {"name":"memhop_count","description":"Get total memory count","inputSchema":{"type":"object","properties":{}}}
]})}

fn tool_call(db: &mut Option<MemHop>, params: Option<&Value>) -> Result<Value, String> {
    let p = params.ok_or("Missing params")?;
    let name = p.get("name").and_then(|n| n.as_str()).ok_or("Missing tool name")?;
    let args = p.get("arguments").unwrap_or(&Value::Null);
    let db = db.as_mut().ok_or("MemHop not initialized")?;

    match name {
        "memhop_create_tree" => { let n = s(args,"name")?; db.create_tree(&n).map_err(|e|e.to_string())?; Ok(json!({"created":n})) }
        "memhop_list_trees" => Ok(json!({"trees":db.list_trees()})),
        "memhop_remove_tree" => { let n = s(args,"name")?; db.remove_tree(&n).map_err(|e|e.to_string())?; Ok(json!({"removed":n})) }
        "memhop_store" => {
            let t = s(args,"text")?; let d = s(args,"domain")?;
            let ae = args.get("auto_entangle").and_then(|v|v.as_bool()).unwrap_or(true);
            let opts = StoreOptions { auto_entangle: ae, context_snippet:None, manual_links:vec![] };
            let id = db.store(&t, Some(&d), &opts).map_err(|e|e.to_string())?;
            Ok(json!({"memory_id":id}))
        }
        "memhop_recall" => {
            let q = s(args,"query")?; let tr = args.get("tree").and_then(|v|v.as_str());
            match db.recall(&q, tr).map_err(|e|e.to_string())? {
                Some(m) => Ok(json!({"found":true,"id":m.id,"text":m.text,"confidence":m.confidence})),
                None => Ok(json!({"found":false}))
            }
        }
        "memhop_recall_topk" => {
            let q = s(args,"query")?; let k = args.get("k").and_then(|v|v.as_u64()).unwrap_or(5) as usize;
            let tr = args.get("tree").and_then(|v|v.as_str());
            Ok(json!({"results":db.recall_topk(&q,k,tr).iter().map(|m|json!({"id":m.id,"text":m.text,"confidence":m.confidence})).collect::<Vec<_>>()}))
        }
        "memhop_forget" => { let id = s(args,"memory_id")?; let ok = db.forget(&id).map_err(|e|e.to_string())?; Ok(json!({"deleted":ok})) }
        "memhop_dream" => { db.dream(None); Ok(json!({"status":"ok"})) }
        "memhop_search" => {
            let f = args.get("filters").ok_or("Missing filters")?;
            let lim = args.get("limit").and_then(|v|v.as_u64()).unwrap_or(50) as usize;
            Ok(json!({"results":db.search(f,lim).map_err(|e|e.to_string())?.iter().map(|m|json!({"id":m.id,"text":m.text})).collect::<Vec<_>>()}))
        }
        "memhop_stats" => Ok(json!(db.stats())),
        "memhop_recent" => {
            let lim = args.get("limit").and_then(|v|v.as_u64()).unwrap_or(10) as usize;
            let tr = args.get("tree").and_then(|v|v.as_str());
            Ok(json!({"results":db.recent(lim,tr).map_err(|e|e.to_string())?.iter().map(|m|json!({"id":m.id,"text":m.text})).collect::<Vec<_>>()}))
        }
        "memhop_count" => Ok(json!({"count":db.count()})),
        _ => Err(format!("Unknown tool: {}", name)),
    }
}

fn s(val: &Value, key: &str) -> Result<String, String> {
    val.get(key).and_then(|v|v.as_str()).map(|s|s.to_string()).ok_or_else(||format!("Missing: {}",key))
}
