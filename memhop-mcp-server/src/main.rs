#![recursion_limit = "256"]

use std::collections::HashMap;
use std::sync::{Mutex, OnceLock};
use std::time::{Duration, Instant};
use serde_json::{json, Value};
use tokio::io::{AsyncBufReadExt, AsyncWriteExt, BufReader};
use tokio::net::{UnixListener, UnixStream};
use memhop::{
    Brain, BrainConfig, EngramKind, PerceptionInput, RecallRequest,
    EmotionalState, Protection, ReflectionInput, ReflectionKind,
    ShelfManager, ShelfDomain,
    StoreResult,
};

const VERSION: &str = "0.13.1";

/// A cached Brain instance with idle-time tracking for lazy eviction.
struct BrainState {
    brain: Brain,
    last_used: Instant,
}

/// Idle timeout for Brain instances (30 minutes).
const BRAIN_IDLE_TIMEOUT: Duration = Duration::from_secs(30 * 60);

static START_TIME: OnceLock<Instant> = OnceLock::new();

struct AppState {
    brains: Mutex<HashMap<String, BrainState>>,
    onnx_model_path: Option<String>,
    reranker_model_path: Option<String>,
}

static STATE: OnceLock<AppState> = OnceLock::new();

struct SocketGuard {
    path: String,
}

impl Drop for SocketGuard {
    fn drop(&mut self) {
        let _ = std::fs::remove_file(&self.path);
        eprintln!("memhop-mcp-server: cleaned up socket {}", self.path);
    }
}

#[tokio::main]
async fn main() {
    let socket_path = parse_socket_path();

    let _ = std::fs::remove_file(&socket_path);

    let onnx_model_path = std::env::var("MEMHOP_ONNX_MODEL").ok();
    let reranker_model_path = std::env::var("MEMHOP_RERANKER_MODEL").ok();

    STATE.set(AppState {
        brains: Mutex::new(HashMap::new()),
        onnx_model_path,
        reranker_model_path,
    }).ok();

    START_TIME.get_or_init(Instant::now);

    let listener = UnixListener::bind(&socket_path).unwrap_or_else(|e| {
        eprintln!("memhop-mcp-server: failed to bind to {}: {}", socket_path, e);
        std::process::exit(1);
    });

    let _guard = SocketGuard { path: socket_path.clone() };

    eprintln!("memhop-mcp-server v{} listening on {}", VERSION, socket_path);

    loop {
        tokio::select! {
            result = listener.accept() => {
                match result {
                    Ok((stream, _)) => {
                        tokio::spawn(handle_connection(stream));
                    }
                    Err(e) => {
                        eprintln!("memhop-mcp-server: accept error: {}", e);
                    }
                }
            }
            _ = tokio::signal::ctrl_c() => {
                eprintln!("memhop-mcp-server: received SIGINT, shutting down");
                break;
            }
        }
    }
}

fn parse_socket_path() -> String {
    let args: Vec<String> = std::env::args().collect();
    for arg in &args[1..] {
        if let Some(val) = arg.strip_prefix("--socket-path=") && !val.is_empty() {
            return val.to_string();
        }
        if arg == "--help" || arg == "-h" {
            eprintln!("Usage: memhop-mcp-server --socket-path=<PATH>");
            std::process::exit(0);
        }
    }
    eprintln!("Error: --socket-path is required");
    eprintln!("Usage: memhop-mcp-server --socket-path=<PATH>");
    std::process::exit(1);
}

async fn handle_connection(stream: UnixStream) {
    let (reader, mut writer) = tokio::io::split(stream);
    let mut reader = BufReader::new(reader);
    let mut line = String::new();

    loop {
        line.clear();
        match reader.read_line(&mut line).await {
            Ok(0) => break,
            Ok(_) => {}
            Err(e) => {
                eprintln!("memhop-mcp-server: read error: {}", e);
                break;
            }
        }

        let trimmed = line.trim();
        if trimmed.is_empty() {
            continue;
        }

        let req: Value = match serde_json::from_str(trimmed) {
            Ok(v) => v,
            Err(e) => {
                let err_resp = json!({
                    "jsonrpc": "2.0",
                    "error": {"code": -32700, "message": format!("parse error: {}", e)},
                    "id": null
                });
                let json_str = serde_json::to_string(&err_resp).unwrap_or_default();
                let _ = writer.write_all(format!("{}\n", json_str).as_bytes()).await;
                continue;
            }
        };

        let result = tokio::task::spawn_blocking(move || {
            process_request(&req)
        }).await;

        match result {
            Ok(Some(resp)) => {
                let json_str = serde_json::to_string(&resp).unwrap_or_else(|_| "{}".to_string());
                if let Err(e) = writer.write_all(format!("{}\n", json_str).as_bytes()).await {
                    eprintln!("memhop-mcp-server: write error: {}", e);
                    break;
                }
                if let Err(e) = writer.flush().await {
                    eprintln!("memhop-mcp-server: flush error: {}", e);
                    break;
                }
            }
            Ok(None) => {}
            Err(e) => {
                eprintln!("memhop-mcp-server: task error: {}", e);
                break;
            }
        }
    }
}

fn process_request(req: &Value) -> Option<Value> {
    let id = req.get("id").cloned();
    let method = req.get("method").and_then(|m| m.as_str()).unwrap_or("");

    match method {
        "initialize" => {
            Some(json!({
                "jsonrpc": "2.0",
                "result": {
                    "protocolVersion": "2024-11-05",
                    "serverInfo": {"name": "memhop-mcp-server", "version": VERSION},
                    "capabilities": {"tools": {}}
                },
                "id": id
            }))
        }
        "notifications/initialized" => None,
        "tools/list" => {
            Some(json!({
                "jsonrpc": "2.0",
                "result": tools_list(),
                "id": id
            }))
        }
        "tools/call" => {
            let result = dispatch_tool_call(req);
            match result {
                Ok(val) => Some(json!({"jsonrpc":"2.0","result":val,"id":id})),
                Err(e) => Some(json!({"jsonrpc":"2.0","error":{"code":-32000,"message":e},"id":id})),
            }
        }
        _ => {
            Some(json!({
                "jsonrpc": "2.0",
                "error": {"code": -32601, "message": format!("Method not found: {}", method)},
                "id": id
            }))
        }
    }
}

fn dispatch_tool_call(req: &Value) -> Result<Value, String> {
    let state = STATE.get().ok_or("Server not initialized")?;
    let mut brains = state.brains.lock().map_err(|e| e.to_string())?;
    let params = req.get("params");

    let agent_id = params
        .and_then(|p| p.get("arguments"))
        .and_then(|a| a.get("agent_id"))
        .and_then(|v| v.as_str())
        .ok_or_else(|| "Missing required parameter: agent_id".to_string())
        .and_then(|aid| {
            if aid.is_empty() || aid.len() > 64
                || !aid.chars().all(|c| c.is_ascii_alphanumeric() || c == '-' || c == '_')
            {
                Err("Invalid agent_id: must match [a-zA-Z0-9_-]{1,64}".to_string())
            } else {
                Ok(aid)
            }
        })?;

    let brains_count = brains.len();
    let brain = get_or_open_brain(
        &mut brains,
        agent_id,
        &state.onnx_model_path,
        &state.reranker_model_path,
    )?;

    tool_call(brain, params, brains_count)
}

fn tools_list() -> Value {
    json!({"tools":[
        {"name":"memhop_store","description":"Store a new memory/episode or knowledge chunk (ADD-only, auto-dedup).","inputSchema":{"type":"object","properties":{
            "text":{"type":"string"},
            "agent_response":{"type":"string","description":"AI's response to this turn (optional, creates DialogueTurn)"},
            "kind":{"type":"string","description":"'episode' (default) or 'knowledge'"},
            "session_id":{"type":"string"},
            "tree_path":{"type":"string","description":"Knowledge tree path (required for kind=knowledge)"},
            "source_path":{"type":"string","description":"Original file path (for knowledge)"},
            "source_textunit":{"type":"string","description":"Text unit reference (e.g., '§3.2')"},
            "valence":{"type":"number"},
            "arousal":{"type":"number"},
            "agent_id":{"type":"string","description":"Agent identifier for multi-agent isolation"},
            "auto_create_tree":{"type":"boolean","description":"Auto-create knowledge tree from accumulated context (default: true)"},
            "auto_compress":{"type":"boolean","description":"Auto-compress context to knowledge when enough turns (default: true)"},
            "match_threshold":{"type":"number","description":"Context match cosine threshold (default: 0.75)"},
            "context_half_life":{"type":"number","description":"Context time decay half-life in hours (default: 12.0)"},
            "llm_compressed_summary":{"type":"string","description":"LLM-generated compressed summary for context"},
            "llm_keywords":{"type":"array","items":{"type":"string"},"description":"LLM-extracted keywords"}},
            "required":["text","agent_id"]}},
        {"name":"memhop_recall","description":"Recall memories matching a query. Returns unified results across all types.","inputSchema":{"type":"object","properties":{
            "query":{"type":"string"},
            "session_id":{"type":"string"},
            "limit":{"type":"integer"},
            "mode":{"type":"string","description":"'retrieval' (HNSW+CrossEncoder, default) or 'associative' (Hopfield+spread)"},
            "use_reranker":{"type":"boolean","description":"Enable CrossEncoder reranking (Retrieval mode only, default: true)"},
            "kind_filter":{"type":"array","items":{"type":"string"},"description":"Filter by kind: 'episode', 'knowledge'. Empty = all."},
            "tree":{"type":"string","description":"Filter by knowledge tree path."},
            "query_vector":{"type":"array","items":{"type":"number"}},
            "agent_id":{"type":"string","description":"Agent identifier for multi-agent isolation"},
            "context_id":{"type":"string","description":"Scope recall to a specific context (reduces forgetfulness)"},
            "use_worldview_filter":{"type":"boolean","description":"Filter results conflicting with worldview (reduces hallucination, default: true)"},
            "llm_conflict_check":{"type":"string","description":"LLM-provided conflict detection result"}},
            "required":["query","agent_id"]}},
        {"name":"memhop_reflect","description":"Create a reflection engram","inputSchema":{"type":"object","properties":{
            "content":{"type":"string"},
            "kind":{"type":"string"},
            "session_id":{"type":"string"},
            "agent_id":{"type":"string","description":"Agent identifier for multi-agent isolation"}},
            "required":["content","kind","agent_id"]}},
        {"name":"memhop_dream","description":"Run Dream consolidation cycle (includes Knowledge engrams).","inputSchema":{"type":"object","properties":{
            "agent_id":{"type":"string","description":"Agent identifier for multi-agent isolation"},
            "context_compress":{"type":"boolean","description":"Compress all pending contexts (default: true)"},
            "llm_patterns":{"type":"array","items":{"type":"object"},"description":"LLM-discovered patterns"},
            "llm_contradictions":{"type":"array","items":{"type":"object"},"description":"LLM-discovered contradictions"}},
            "required":["agent_id"]}},
        {"name":"memhop_stats","description":"Get brain statistics","inputSchema":{"type":"object","properties":{
            "agent_id":{"type":"string","description":"Agent identifier for multi-agent isolation"}},
            "required":["agent_id"]}},
        {"name":"memhop_count","description":"Get total engram count","inputSchema":{"type":"object","properties":{
            "agent_id":{"type":"string","description":"Agent identifier for multi-agent isolation"}},
            "required":["agent_id"]}},
        {"name":"memhop_health","description":"Get health metrics (uptime, version, basic stats)","inputSchema":{"type":"object","properties":{
            "agent_id":{"type":"string","description":"Agent identifier for multi-agent isolation"}},
            "required":["agent_id"]}},
        {"name":"memhop_complete_plan","description":"Complete a plan (mark as Completed, optionally summarize)","inputSchema":{"type":"object","properties":{
            "plan_id":{"type":"string"},
            "agent_id":{"type":"string","description":"Agent identifier for multi-agent isolation"}},
            "required":["plan_id","agent_id"]}},
        {"name":"memhop_get_plan_tree","description":"Get the plan tree (all root plans or descendants of a given plan)","inputSchema":{"type":"object","properties":{
            "plan_id":{"type":"string"},
            "agent_id":{"type":"string","description":"Agent identifier for multi-agent isolation"}},
            "required":["agent_id"]}},
        {"name":"memhop_get_chat_history","description":"Get archived dialogue turns for a plan","inputSchema":{"type":"object","properties":{
            "plan_id":{"type":"string"},
            "agent_id":{"type":"string","description":"Agent identifier for multi-agent isolation"}},
            "required":["plan_id","agent_id"]}},
        {"name":"memhop_plan_stats","description":"Get aggregated plan statistics (domain distribution + tone trends)","inputSchema":{"type":"object","properties":{
            "start_time":{"type":"integer"},
            "end_time":{"type":"integer"},
            "agent_id":{"type":"string","description":"Agent identifier for multi-agent isolation"}},
            "required":["agent_id"]}},
        {"name":"memhop_mount_tree","description":"Mount a knowledge tree from a file or directory path. Path is the identity.","inputSchema":{"type":"object","properties":{
            "path":{"type":"string"},
            "domain":{"type":"string","description":"'code', 'book', 'paper', 'doc', or 'generic'"},
            "agent_id":{"type":"string","description":"Agent identifier for multi-agent isolation"}},
            "required":["path","agent_id"]}},
        {"name":"memhop_unmount_tree","description":"Unmount a knowledge tree by tree path.","inputSchema":{"type":"object","properties":{
            "tree_path":{"type":"string"},
            "agent_id":{"type":"string","description":"Agent identifier for multi-agent isolation"}},
            "required":["tree_path","agent_id"]}},
        {"name":"memhop_tree_status","description":"List all mounted knowledge trees with metadata.","inputSchema":{"type":"object","properties":{
            "tree_path":{"type":"string","description":"Optional: get status for a specific tree. Returns all trees if omitted."},
            "agent_id":{"type":"string","description":"Agent identifier for multi-agent isolation"}},
            "required":["agent_id"]}},
        {"name":"memhop_knowledge_search","description":"[DEPRECATED] Use memhop_recall with tree and kind_filter instead.","inputSchema":{"type":"object","properties":{
            "query":{"type":"string"},
            "shelf_id":{"type":"string"},
            "limit":{"type":"integer"},
            "agent_id":{"type":"string","description":"Agent identifier for multi-agent isolation"}},
            "required":["query","shelf_id","agent_id"]}},
        {"name":"memhop_forget","description":"Forget a dialogue turn and its associated engrams","inputSchema":{"type":"object","properties":{
            "turn_id":{"type":"string"},
            "agent_id":{"type":"string","description":"Agent identifier for multi-agent isolation"}},
            "required":["turn_id","agent_id"]}},
        {"name":"memhop_list_schemas","description":"List all emerged schema engrams","inputSchema":{"type":"object","properties":{
            "agent_id":{"type":"string","description":"Agent identifier for multi-agent isolation"}},
            "required":["agent_id"]}},
        {"name":"memhop_create_tree","description":"Create a knowledge tree for organizing memories by domain.","inputSchema":{"type":"object","properties":{
            "name":{"type":"string"},
            "domain":{"type":"string","description":"'work', 'travel', 'parenting', 'generic', etc."},
            "agent_id":{"type":"string","description":"Agent identifier for multi-agent isolation"}},
            "required":["name","agent_id"]}},
        {"name":"memhop_list_trees","description":"List all knowledge trees.","inputSchema":{"type":"object","properties":{
            "agent_id":{"type":"string","description":"Agent identifier for multi-agent isolation"}},
            "required":["agent_id"]}},
        {"name":"memhop_get_tree","description":"Get details of a specific knowledge tree.","inputSchema":{"type":"object","properties":{
            "tree_id":{"type":"string"},
            "agent_id":{"type":"string","description":"Agent identifier for multi-agent isolation"}},
            "required":["tree_id","agent_id"]}},
        {"name":"memhop_move_to_tree","description":"Move an engram to a specific knowledge tree.","inputSchema":{"type":"object","properties":{
            "engram_id":{"type":"string"},
            "tree_id":{"type":"string"},
            "agent_id":{"type":"string","description":"Agent identifier for multi-agent isolation"}},
            "required":["engram_id","tree_id","agent_id"]}},
        {"name":"memhop_delete_tree","description":"Delete a knowledge tree (does not delete associated engrams).","inputSchema":{"type":"object","properties":{
            "tree_id":{"type":"string"},
            "agent_id":{"type":"string","description":"Agent identifier for multi-agent isolation"}},
            "required":["tree_id","agent_id"]}},
        {"name":"memhop_list_entanglements","description":"List all cross-tree entanglement events, sorted by strength.","inputSchema":{"type":"object","properties":{
            "agent_id":{"type":"string","description":"Agent identifier for multi-agent isolation"}},
            "required":["agent_id"]}},
        {"name":"memhop_entanglement_detail","description":"Get details of a specific entanglement event.","inputSchema":{"type":"object","properties":{
            "event_id":{"type":"string"},
            "agent_id":{"type":"string","description":"Agent identifier for multi-agent isolation"}},
            "required":["event_id","agent_id"]}},
        {"name":"memhop_list_worldviews","description":"List all worldview patterns emerged from memory entanglements.","inputSchema":{"type":"object","properties":{
            "agent_id":{"type":"string","description":"Agent identifier for multi-agent isolation"}},
            "required":["agent_id"]}},
        {"name":"memhop_worldview_detail","description":"Get details of a specific worldview pattern.","inputSchema":{"type":"object","properties":{
            "wv_id":{"type":"string"},
            "agent_id":{"type":"string","description":"Agent identifier for multi-agent isolation"}},
            "required":["wv_id","agent_id"]}},
        {"name":"memhop_my_worldview","description":"Get a natural language summary of stable worldview patterns.","inputSchema":{"type":"object","properties":{
            "agent_id":{"type":"string","description":"Agent identifier for multi-agent isolation"}},
            "required":["agent_id"]}}
    ]})
}

/// Get or open a Brain for the given `agent_id`.
///
/// Resolves the database path from `MEMHOP_BRAINS_DIR` (defaults to
/// `~/.memhop/brains/{agent_id}/memhop.db`), opens a new Brain if one
/// is not already cached, then returns a mutable reference to it.
/// Also rebuilds the shelf registry after opening a new Brain.
fn get_or_open_brain<'a>(
    brains: &'a mut HashMap<String, BrainState>,
    agent_id: &'a str,
    onnx_model_path: &'a Option<String>,
    reranker_model_path: &'a Option<String>,
) -> Result<&'a mut Brain, String> {
    // Evict idle brains before potentially creating a new one
    let now = Instant::now();
    brains.retain(|_, state| now.duration_since(state.last_used) < BRAIN_IDLE_TIMEOUT);

    if !brains.contains_key(agent_id) {
        let brains_dir = std::env::var("MEMHOP_BRAINS_DIR")
            .unwrap_or_else(|_| {
                let home = std::env::var("HOME")
                    .or_else(|_| std::env::var("USERPROFILE"))
                    .unwrap_or_else(|_| ".".to_string());
                format!("{}/{}", home, memhop::DEFAULT_BRAINS_DIR)
            });
        let db_path = format!("{}/{}/memhop.db", brains_dir, agent_id);

        let mut brain_config = BrainConfig::default();
        if let Some(path) = onnx_model_path {
            brain_config.onnx_model_path = Some(path.clone());
            eprintln!("memhop-mcp-server: using ONNX model from {}", path);
        }
        if let Some(path) = reranker_model_path {
            brain_config.reranker_model_path = Some(path.clone());
            eprintln!("memhop-mcp-server: using reranker model from {}", path);
        }
        let brain = Brain::open(&db_path, brain_config, None)
            .map_err(|e| format!("failed to open brain: {}", e))?;
        if let Ok(mut manager) = get_shelf_manager().lock() {
            let _ = manager.rebuild_registry(&brain);
        }
        brains.insert(agent_id.to_string(), BrainState { brain, last_used: now });
    }
    let state = brains
        .get_mut(agent_id)
        .ok_or_else(|| "Brain not found after open".to_string())?;
    state.last_used = now;
    Ok(&mut state.brain)
}

fn tool_call(brain: &mut Brain, params: Option<&Value>, brains_count: usize) -> Result<Value, String> {
    let p = params.ok_or("Missing params")?;
    let name = p.get("name").and_then(|n| n.as_str()).ok_or("Missing tool name")?;
    let args = p.get("arguments").unwrap_or(&Value::Null);

    match name {
        "memhop_store" => tool_store(brain, args),
        "memhop_recall" => tool_recall(brain, args),
        "memhop_reflect" => tool_reflect(brain, args),
        "memhop_dream" => tool_dream(brain, args),
        "memhop_stats" => tool_stats(brain, args),
        "memhop_count" => tool_count(brain, args),
        "memhop_health" => tool_health(brain, args, brains_count),
        "memhop_complete_plan" => tool_complete_plan(brain, args),
        "memhop_get_plan_tree" => tool_get_plan_tree(brain, args),
        "memhop_get_chat_history" => tool_get_chat_history(brain, args),
        "memhop_plan_stats" => tool_plan_stats(brain, args),
        "memhop_mount_tree" => tool_mount_tree(brain, args),
        "memhop_unmount_tree" => tool_unmount_tree(brain, args),
        "memhop_tree_status" => tool_tree_status(brain, args),
        "memhop_knowledge_search" => tool_knowledge_search_deprecated(brain, args),
        // ── v0.11.0: Deprecated aliases (backward compat) ──
        "memhop_mount_shelf" => {
            eprintln!("[memhop] DEPRECATION: memhop_mount_shelf is deprecated, use memhop_mount_tree");
            tool_mount_tree(brain, args)
        },
        "memhop_unmount_shelf" => {
            eprintln!("[memhop] DEPRECATION: memhop_unmount_shelf is deprecated, use memhop_unmount_tree");
            tool_unmount_tree(brain, args)
        },
        // ── Old tools ──
        "memhop_forget" => tool_forget(brain, args),
        "memhop_list_schemas" => tool_list_schemas(brain, args),
        // ── v0.12.1: Tree tools ──
        "memhop_create_tree" => tool_create_tree(brain, args),
        "memhop_list_trees" => tool_list_trees(brain, args),
        "memhop_get_tree" => tool_get_tree(brain, args),
        "memhop_move_to_tree" => tool_move_to_tree(brain, args),
        "memhop_delete_tree" => tool_delete_tree(brain, args),
        // ── v0.12.1: Entanglement tools ──
        "memhop_list_entanglements" => tool_list_entanglements(brain, args),
        "memhop_entanglement_detail" => tool_entanglement_detail(brain, args),
        // ── v0.12.1: Worldview tools ──
        "memhop_list_worldviews" => tool_list_worldviews(brain, args),
        "memhop_worldview_detail" => tool_worldview_detail(brain, args),
        "memhop_my_worldview" => tool_my_worldview(brain, args),
        _ => Err(format!("Unknown tool: {}", name)),
    }
}

fn s(val: &Value, key: &str) -> Result<String, String> {
    val.get(key)
        .and_then(|v| v.as_str())
        .map(|s| s.to_string())
        .ok_or_else(|| format!("Missing required parameter: {}", key))
}

/// v0.9.0: Simple privacy filter — strip suspected secrets from text before storage.
fn privacy_filter(text: &str) -> String {
    let mut result = String::new();
    for line in text.lines() {
        let trimmed = line.trim();
        // Skip lines that look like standalone API keys
        if trimmed.starts_with("sk-") && trimmed.len() > 20 {
            continue;
        }
        // Mask inline secret keywords
        let masked = line
            .replace("api_key", "***")
            .replace("API_KEY", "***")
            .replace("api-key", "***")
            .replace("secret", "***")
            .replace("password", "***")
            .replace("token", "***");
        result.push_str(&masked);
        result.push('\n');
    }
    result.trim_end().to_string()
}

fn tool_store(brain: &mut Brain, args: &Value) -> Result<Value, String> {
    let raw_text = s(args, "text")?;
    let text = privacy_filter(&raw_text);
    let kind_str = args.get("kind").and_then(|v| v.as_str()).unwrap_or("episode");

    // Accept pre-computed vector from external encoder (e.g. Python sentence-transformers).
    // Falls back to brain.encode_text() when not provided.
    // Pads to VECTOR_DIM (1024) if the external model produces fewer dims (e.g. all-MiniLM=384).
    let external_vector: Option<Vec<half::f16>> = args.get("vector")
        .and_then(|v| v.as_array())
        .map(|arr| {
            let raw: Vec<half::f16> = arr.iter()
                .filter_map(|x| x.as_f64().map(|f| half::f16::from_f32(f as f32)))
                .collect();
            // Pad to VECTOR_DIM (1024) and re-normalize
            let mut padded = raw;
            padded.resize(memhop::VECTOR_DIM, half::f16::ZERO);
            let norm: f32 = padded.iter()
                .map(|x| f32::from(*x).powi(2))
                .sum::<f32>()
                .sqrt();
            if norm > 1e-8 {
                let scale = half::f16::from_f32(1.0 / norm);
                for x in &mut padded {
                    let v: f32 = f32::from(*x) * f32::from(scale);
                    *x = half::f16::from_f32(v);
                }
            }
            padded
        });

    match kind_str {
        "episode" => {
            let session_id = args.get("session_id")
                .and_then(|v| v.as_str())
                .unwrap_or("default")
                .to_string();
            let valence = args.get("valence").and_then(|v| v.as_f64()).unwrap_or(0.0) as f32;
            let arousal = args.get("arousal").and_then(|v| v.as_f64()).unwrap_or(0.5) as f32;

            let vector = external_vector.unwrap_or_else(|| brain.encode_text(&text));

            let turn_id = args.get("turn_id")
                .and_then(|v| v.as_str())
                .unwrap_or("")
                .to_string();
            let turn_index = args.get("turn_index")
                .and_then(|v| v.as_i64())
                .unwrap_or(0) as u32;
            let topic_label = args.get("topic_label")
                .and_then(|v| v.as_str())
                .map(|s| s.to_string());

            let input = PerceptionInput {
                content: text,
                vector,
                emotional_state: EmotionalState::new(valence, arousal),
                attention_anchors: vec![],
                perceived_importance: 0.5,
                session_id,
                protection: Protection::Normal,
                manual_links: vec![],
                meta: std::collections::HashMap::new(),
                plan_id: None,
                agent_response: args.get("agent_response").and_then(|v| v.as_str()).map(|s| s.to_string()),
                dialogue_timestamp: None,
                source: None,
                turn_id,
                turn_index,
                segment_index: 0,
                topic_label,
                tree_id: args.get("tree_id").and_then(|v| v.as_str()).map(|s| s.to_string()),
            };

            let output = brain.perceive(input).map_err(|e| e.to_string())?;
            Ok(json!({
                "status": "stored",
                "engram_id": output.engram_id,
                "plan_id": output.current_plan_id,
                "plan_hint": format!("{:?}", output.plan_hint),
                "plan_name": output.plan_name,
                "context_id": output.context_id,
                "context_summary": null,
                "phase": output.phase,
            }))
        }
        "knowledge" => {
            let tree_path = s(args, "tree_path")?;
            let source_path = args.get("source_path")
                .and_then(|v| v.as_str())
                .unwrap_or("")
                .to_string();
            let source_textunit = args.get("source_textunit")
                .and_then(|v| v.as_str())
                .unwrap_or("")
                .to_string();
            let vector = external_vector.unwrap_or_else(|| brain.encode_text(&text));
            let result: StoreResult = brain.store(
                &text,
                &vector,
                EngramKind::Knowledge,
                Some(tree_path),
                Some(source_path),
                Some(source_textunit),
            ).map_err(|e| e.to_string())?;
            Ok(json!({
                "status": format!("{:?}", result.status),
                "engram_id": result.engram_id,
                "duplicate_of": result.duplicate_of,
            }))
        }
        _ => Err(format!("Invalid kind '{}'. Must be 'episode' or 'knowledge'.", kind_str)),
    }
}

fn tool_recall(brain: &mut Brain, args: &Value) -> Result<Value, String> {
    let query = s(args, "query")?;
    let session_id = args.get("session_id")
        .and_then(|v| v.as_str())
        .unwrap_or("")
        .to_string();
    let limit = args.get("limit").and_then(|v| v.as_u64()).unwrap_or(5) as usize;

    // v0.11.0: Parse kind_filter and tree
    let kind_filter: Vec<EngramKind> = args.get("kind_filter")
        .and_then(|v| v.as_array())
        .map(|arr| {
            arr.iter()
                .filter_map(|x| x.as_str())
                .filter_map(|s| match s {
                    "episode" => Some(EngramKind::Episode),
                    "knowledge" => Some(EngramKind::Knowledge),
                    _ => None,
                })
                .collect()
        })
        .unwrap_or_default();
    let tree = args.get("tree").and_then(|v| v.as_str()).map(|s| s.to_string());

    let query_vector: Option<Vec<half::f16>> = args.get("query_vector")
        .and_then(|v| v.as_array())
        .map(|arr| {
            let mut v: Vec<half::f16> = arr.iter()
                .filter_map(|x| x.as_f64().map(|f| half::f16::from_f32(f as f32)))
                .collect();
            v.resize(memhop::VECTOR_DIM, half::f16::ZERO);
            // Re-normalize in f16 space to match stored vector pipeline
            let norm: f32 = v.iter().map(|x| f32::from(*x).powi(2)).sum::<f32>().sqrt();
            if norm > 1e-8 {
                let scale = half::f16::from_f32(1.0 / norm);
                for x in &mut v {
                    let val: f32 = f32::from(*x) * f32::from(scale);
                    *x = half::f16::from_f32(val);
                }
            }
            v
        });

    // v0.13.0: Parse context_id for scoped recall
    let context_id = args.get("context_id").and_then(|v| v.as_str()).map(|s| s.to_string());

    // v0.11.1: Read mode from args, default to Retrieval (HNSW + CrossEncoder)
    let mode = match args.get("mode").and_then(|v| v.as_str()).unwrap_or("retrieval") {
        "associative" => memhop::RecallMode::Associative,
        _ => memhop::RecallMode::Retrieval,
    };
    // v0.11.1: Read use_reranker from args, default to true in Retrieval mode
    let use_reranker = args.get("use_reranker")
        .and_then(|v| v.as_bool())
        .unwrap_or(mode == memhop::RecallMode::Retrieval);

    let req = RecallRequest {
        query,
        query_vector,
        session_id,
        emotional_state: EmotionalState::default(),
        attention_anchors: vec![],
        current_goal: None,
        recent_limit: limit,
        spread_depth: 3,
        spread_top_k: limit,
        active_plan_id: None,
        deep_search: false,
        deep_search_plan_id: None,
        domain_filter: vec![],
        limit: 10,
        mode,
        use_reranker,
        tree,
        tree_id: None,
        kind_filter,
        time_from: None,
        time_to: None,
        attach_knowledge: true,
        context_id,
    };

    eprintln!("memhop-recall: hopfield_empty={} hnsw_empty={} memory_count={}",
        brain.hopfield_is_empty(), brain.hnsw_is_empty(), brain.memory_count());
    let resp = brain.recall(&req).map_err(|e| e.to_string())?;
    eprintln!("memhop-recall: got {} wm + {} km + {} assoc",
        resp.working_memory.len(), resp.knowledge_memories.len(), resp.associations.len());
    if !resp.associations.is_empty() {
        let sample: Vec<&str> = resp.associations.iter().take(3).map(|e| e.id.as_str()).collect();
        eprintln!("memhop-recall: sample_ids={:?}", sample);
    }

    let mut results: Vec<Value> = Vec::new();
    for e in &resp.working_memory {
        results.push(json!({
            "id": e.id, "text": e.text,
            "kind": format!("{}", e.kind), "source": "episode",
            "tree_path": e.tree_path,
        }));
    }
    for e in &resp.knowledge_memories {
        results.push(json!({
            "id": e.id, "text": e.text,
            "kind": "knowledge", "source": "knowledge",
            "tree_path": e.tree_path,
            "source_path": e.source_path,
            "source_textunit": e.source_textunit,
        }));
    }
    for e in &resp.associations {
        // Only add associations that are not already in working_memory or knowledge_memories
        if !results.iter().any(|r| r["id"] == e.id) {
            results.push(json!({
                "id": e.id, "text": e.text,
                "kind": format!("{}", e.kind), "source": "episode",
                "tree_path": e.tree_path,
            }));
        }
    }
    results.truncate(limit);

    Ok(json!({
        "results": results,
        "knowledge_memories": resp.knowledge_memories.iter().map(|e| json!({
            "id": e.id, "text": &e.text,
            "tree_path": e.tree_path,
            "source_path": e.source_path,
            "source_textunit": e.source_textunit,
        })).collect::<Vec<_>>(),
        "schemas": resp.schemas.iter().map(|e| json!({"id": e.id, "text": e.text})).collect::<Vec<_>>(),
        "hit_turns": resp.hit_turns,
        "aggregated_sessions": resp.aggregated_sessions,
        "tree_contexts": resp.tree_contexts,
        "graph_associations": resp.graph_associations,
        "trace": {
            "latency_us": resp.trace.latency_us,
            "hopfield_candidates": resp.trace.hopfield_candidates,
            "spread_steps": resp.trace.spread_steps,
        },
        "contexts_summary": [],
        "worldview_context": resp.worldview_context,
        "cognitive_conflicts": resp.cognitive_conflicts,
        "recall_quality": {
            "scope": "global",
            "context_hit_count": 0,
            "total_candidates": 0
        }
    }))
}

fn tool_reflect(brain: &mut Brain, args: &Value) -> Result<Value, String> {
    let content = s(args, "content")?;
    let kind_str = s(args, "kind")?;
    let session_id = args.get("session_id")
        .and_then(|v| v.as_str())
        .unwrap_or("default")
        .to_string();

    let kind = match kind_str.to_lowercase().as_str() {
        "pattern" => ReflectionKind::Pattern,
        "evaluation" => ReflectionKind::Evaluation,
        "intention" => ReflectionKind::Intention,
        "confusion" => ReflectionKind::Confusion,
        _ => return Err(format!("Invalid kind '{}'. Must be one of: pattern, evaluation, intention, confusion", kind_str)),
    };

    let input = ReflectionInput {
        content,
        kind,
        anchored_to: vec![],
        emotional_state: EmotionalState::default(),
        session_id,
    };

    let id = brain.reflect(input).map_err(|e| e.to_string())?;
    Ok(json!({"reflection_id": id}))
}

fn tool_dream(brain: &mut Brain, _args: &Value) -> Result<Value, String> {
    let report = brain.dream().map_err(|e| e.to_string())?;
    Ok(json!({
        "status": "ok",
        "consolidated_count": report.consolidated_count,
        "pruned_edges": report.pruned_edges,
        "duration_ms": report.duration_ms,
        "knowledge_processed": report.knowledge_processed,
        "cross_kind_new_associations": report.cross_kind_new_associations,
        "hnsw_compacted": report.hnsw_compacted,
        "contexts_compressed": 0,
        "dormant_moved": 0,
        "archived": 0,
    }))
}

// ── v0.9.1: Forget tool ────────────────────────────────────────

fn tool_forget(brain: &mut Brain, args: &Value) -> Result<Value, String> {
    let turn_id = s(args, "turn_id")?;
    brain.forget_batch(&memhop::ForgetFilter::ByTurnId(turn_id.to_string()))
        .map_err(|e| e.to_string())?;
    Ok(json!({"status": "ok"}))
}

// ── v0.9.1: List schemas tool ──────────────────────────────

fn tool_list_schemas(brain: &mut Brain, _args: &Value) -> Result<Value, String> {
    let schemas = brain.list_schemas().map_err(|e| e.to_string())?;
    let result: Vec<Value> = schemas.iter().map(|(engram, extra)| {
        json!({
            "id": engram.id,
            "text": engram.text,
            "summary": engram.summary,
            "keywords": engram.keywords,
            "stability": extra.stability,
            "internal_consistency": extra.internal_consistency,
            "match_count": extra.match_count,
            "contradiction_count": extra.contradiction_count,
            "activation_count": engram.activation_count,
        })
    }).collect();
    Ok(json!({"schemas": result}))
}

fn tool_stats(brain: &mut Brain, _args: &Value) -> Result<Value, String> {
    let g = brain.growth_state();
    Ok(json!({
        "total_memories": brain.memory_count() + brain.hippocampus_len(),
        "cortex_len": brain.cortex_len(),
        "hippocampus_len": brain.hippocampus_len(),
        "total_perceptions": g.total_perceptions,
        "total_reflections": g.total_reflections,
        "total_engrams_created": g.total_engrams_created,
        "total_consolidated": g.total_consolidated,
        "dream_cycles": g.dream_cycles,
        "total_schemas_emerged": g.total_schemas_emerged,
        "total_contradictions": g.total_contradictions,
        "version": VERSION,
    }))
}

fn tool_count(brain: &mut Brain, _args: &Value) -> Result<Value, String> {
    Ok(json!({"count": brain.memory_count() + brain.hippocampus_len()}))
}

// ── v0.9.0: Health tool ────────────────────────────────────────

fn tool_health(brain: &mut Brain, _args: &Value, active_brains: usize) -> Result<Value, String> {
    let g = brain.growth_state();
    let uptime_secs = START_TIME.get_or_init(Instant::now).elapsed().as_secs();
    Ok(json!({
        "status": "ok",
        "version": VERSION,
        "uptime_secs": uptime_secs,
        "active_brains": active_brains,
        "total_memories": brain.memory_count() + brain.hippocampus_len(),
        "cortex_len": brain.cortex_len(),
        "hippocampus_len": brain.hippocampus_len(),
        "total_engrams_created": g.total_engrams_created,
        "total_consolidated": g.total_consolidated,
        "dream_cycles": g.dream_cycles,
    }))
}

// ── v0.8.0: New Plan tools ─────────────────────────────────────

fn tool_complete_plan(brain: &mut Brain, args: &Value) -> Result<Value, String> {
    let plan_id = s(args, "plan_id")?;
    brain.complete_plan(&plan_id).map_err(|e| e.to_string())?;
    Ok(json!({"status": "completed"}))
}

fn tool_get_plan_tree(brain: &mut Brain, args: &Value) -> Result<Value, String> {
    let plan_id = args.get("plan_id").and_then(|v| v.as_str());
    let tree = brain.get_plan_tree(plan_id).map_err(|e| e.to_string())?;
    let nodes: Vec<Value> = tree.iter().map(|p| {
        json!({
            "id": p.id,
            "parent_id": p.parent_id,
            "name": p.name,
            "level": format!("{:?}", p.level),
            "state": format!("{:?}", p.state),
            "dialogue_count": p.dialogue_count,
            "compressed_summary": p.compressed_summary,
            "created_at": p.created_at,
            "completed_at": p.completed_at,
        })
    }).collect();
    Ok(json!({"tree": nodes}))
}

fn tool_get_chat_history(brain: &mut Brain, args: &Value) -> Result<Value, String> {
    let plan_id = s(args, "plan_id")?;
    let turns = brain.archived_dialogue(&plan_id, 0, 1000).map_err(|e| e.to_string())?;
    let result: Vec<Value> = turns.iter().map(|t| {
        json!({
            "id": t.id,
            "plan_id": t.plan_id,
            "user_input": t.user_input,
            "agent_response": t.agent_response,
            "user_tone": {"valence": t.user_tone.valence, "arousal": t.user_tone.arousal, "tags": t.user_tone.tone_tags},
            "timestamp": t.timestamp,
        })
    }).collect();
    Ok(json!({"turns": result, "total": result.len()}))
}

fn tool_plan_stats(brain: &mut Brain, args: &Value) -> Result<Value, String> {
    let _plan_id = args.get("plan_id").and_then(|v| v.as_str());
    let start_time = args.get("start_time")
        .and_then(|v| v.as_i64())
        .unwrap_or(0);
    let end_time = args.get("end_time")
        .and_then(|v| v.as_i64())
        .unwrap_or(i64::MAX);

    let domain_distribution = brain.get_topic_distribution()
        .map_err(|e| e.to_string())?;

    let tone_trend = brain.get_tone_aggregates(start_time, end_time)
        .map_err(|e| e.to_string())?;

    let mut domains: Vec<Value> = Vec::new();
    let mut plan_count = 0u32;
    for (name, stats) in &domain_distribution.domains {
        plan_count += stats.plan_count;
        domains.push(json!({
            "domain": name,
            "plan_count": stats.plan_count,
            "dialogue_count": stats.dialogue_count,
            "avg_valence": stats.avg_valence,
        }));
    }

    Ok(json!({
        "plan_count": plan_count,
        "domain_distribution": domains,
        "tone_trend": {
            "avg_valence": tone_trend.avg_valence,
            "avg_arousal": tone_trend.avg_arousal,
            "valence_trend": tone_trend.valence_trend,
            "top_tone_tags": tone_trend.top_tone_tags,
        }
    }))
}

// ── v0.11.0: Knowledge Tree tools ─────────────────────────

fn get_shelf_manager() -> &'static Mutex<ShelfManager> {
    static MANAGER: OnceLock<Mutex<ShelfManager>> = OnceLock::new();
    MANAGER.get_or_init(|| Mutex::new(ShelfManager::new()))
}


fn tool_mount_tree(brain: &mut Brain, args: &Value) -> Result<Value, String> {
    let path = s(args, "path")?;
    let domain_str = args
        .get("domain")
        .and_then(|v| v.as_str())
        .unwrap_or("generic");
    let domain = match domain_str {
        "code" => ShelfDomain::Code,
        "book" => ShelfDomain::Book,
        "paper" => ShelfDomain::Paper,
        "doc" => ShelfDomain::Doc,
        _ => ShelfDomain::Generic,
    };

    let mut manager = get_shelf_manager().lock().map_err(|e| e.to_string())?;
    let result = manager.mount(brain, &path, domain)?;

    Ok(json!({
        "tree_path": result.tree_path,
        "chunk_count": result.chunk_count,
        "domain": result.domain,
        "warnings": result.warnings,
    }))
}

fn tool_knowledge_search_deprecated(brain: &mut Brain, args: &Value) -> Result<Value, String> {
    eprintln!("[memhop] DEPRECATION: memhop_knowledge_search is deprecated. Use memhop_recall with tree=...&kind_filter=[\"knowledge\"] instead.");
    let query = s(args, "query")?;
    let shelf_id = s(args, "shelf_id")?;
    let limit = args
        .get("limit")
        .and_then(|v| v.as_u64())
        .unwrap_or(5) as usize;

    let query_vector = brain.encode_text(&query);

    let req = RecallRequest {
        query: query.clone(),
        query_vector: Some(query_vector),
        session_id: String::new(),
        emotional_state: EmotionalState::default(),
        attention_anchors: vec![],
        current_goal: None,
        recent_limit: limit,
        spread_depth: 1,
        spread_top_k: limit,
        active_plan_id: None,
        deep_search: false,
        deep_search_plan_id: None,
        domain_filter: vec![],
        limit,
        mode: memhop::RecallMode::Retrieval,
        use_reranker: true,
        tree: Some(shelf_id),
        tree_id: None,
        kind_filter: vec![EngramKind::Knowledge],
        time_from: None,
        time_to: None,
        attach_knowledge: true,
        context_id: None,
    };

    let resp = brain.recall(&req).map_err(|e| e.to_string())?;

    let out: Vec<Value> = resp
        .knowledge_memories
        .iter()
        .take(limit)
        .map(|e| {
            json!({
                "text": &e.text,
                "score": 0.0,
                "source": e.source_path,
                "location": e.source_textunit,
            })
        })
        .collect();

    Ok(json!({
        "status": "ok",
        "results": out,
        "deprecation_warning": "Use memhop_recall with tree and kind_filter instead.",
    }))
}

fn tool_unmount_tree(brain: &mut Brain, args: &Value) -> Result<Value, String> {
    let tree_path = s(args, "tree_path")?;
    let mut manager = get_shelf_manager().lock().map_err(|e| e.to_string())?;
    let result = manager.unmount(brain, &tree_path)?;
    Ok(json!({
        "tree_path": result.tree_path,
        "deleted_count": result.deleted_count,
    }))
}

fn tool_tree_status(_brain: &mut Brain, args: &Value) -> Result<Value, String> {
    let manager = get_shelf_manager().lock().map_err(|e| e.to_string())?;
    if let Some(tree_path) = args.get("tree_path").and_then(|v| v.as_str()) {
        match manager.get_tree(tree_path) {
            Some(meta) => Ok(json!({
                "tree_path": meta.tree_path,
                "domain": format!("{:?}", meta.domain),
                "chunk_count": meta.chunk_count,
                "file_count": meta.file_count,
                "mounted_at": meta.mounted_at,
            })),
            None => Err(format!("Tree not found: {}", tree_path)),
        }
    } else {
        let trees: Vec<Value> = manager.get_trees().iter().map(|meta| {
            json!({
                "tree_path": meta.tree_path,
                "domain": format!("{:?}", meta.domain),
                "chunk_count": meta.chunk_count,
                "file_count": meta.file_count,
            })
        }).collect();
        Ok(json!({"trees": trees, "count": trees.len()}))
    }
}

// ── v0.12.1: Tree tools ────────────────────────────────────────────

fn tool_create_tree(brain: &mut Brain, args: &Value) -> Result<Value, String> {
    let name = s(args, "name")?;
    let domain = args
        .get("domain")
        .and_then(|v| v.as_str())
        .unwrap_or("generic");
    let tree = brain.create_tree(&name, domain, false).map_err(|e| e.to_string())?;
    Ok(json!({"tree_id": tree.id, "name": tree.name, "domain": tree.domain}))
}

fn tool_list_trees(brain: &mut Brain, _args: &Value) -> Result<Value, String> {
    let trees = brain.list_trees().map_err(|e| e.to_string())?;
    Ok(json!(trees))
}

fn tool_get_tree(brain: &mut Brain, args: &Value) -> Result<Value, String> {
    let tree_id = s(args, "tree_id")?;
    let tree = brain.get_tree(&tree_id).map_err(|e| e.to_string())?;
    Ok(json!(tree))
}

fn tool_move_to_tree(brain: &mut Brain, args: &Value) -> Result<Value, String> {
    let engram_id = s(args, "engram_id")?;
    let tree_id = s(args, "tree_id")?;
    brain
        .move_to_tree(&engram_id, &tree_id)
        .map_err(|e| e.to_string())?;
    Ok(json!({"status": "ok"}))
}

fn tool_delete_tree(brain: &mut Brain, args: &Value) -> Result<Value, String> {
    let tree_id = s(args, "tree_id")?;
    brain.delete_tree(&tree_id).map_err(|e| e.to_string())?;
    Ok(json!({"status": "ok"}))
}

// ── v0.12.1: Entanglement tools ────────────────────────────────────

fn tool_list_entanglements(brain: &mut Brain, _args: &Value) -> Result<Value, String> {
    let mut events = brain.get_all_entanglements().map_err(|e| e.to_string())?;
    events.sort_by(|a, b| {
        b.strength
            .partial_cmp(&a.strength)
            .unwrap_or(std::cmp::Ordering::Equal)
    });
    Ok(json!(events))
}

fn tool_entanglement_detail(brain: &mut Brain, args: &Value) -> Result<Value, String> {
    let event_id = s(args, "event_id")?;
    let event = brain
        .get_entanglement(&event_id)
        .map_err(|e| e.to_string())?;
    Ok(json!(event))
}

// ── v0.12.1: Worldview tools ───────────────────────────────────────

fn tool_list_worldviews(brain: &mut Brain, _args: &Value) -> Result<Value, String> {
    let worldviews = brain.get_all_worldviews().map_err(|e| e.to_string())?;
    Ok(json!(worldviews))
}

fn tool_worldview_detail(brain: &mut Brain, args: &Value) -> Result<Value, String> {
    let wv_id = s(args, "wv_id")?;
    let wv = brain.get_worldview(&wv_id).map_err(|e| e.to_string())?;
    Ok(json!(wv))
}

fn tool_my_worldview(brain: &mut Brain, _args: &Value) -> Result<Value, String> {
    let worldviews = brain.get_all_worldviews().map_err(|e| e.to_string())?;
    let mut summary = String::new();
    for wv in &worldviews {
        if wv.stability > 0.5 {
            summary.push_str(&format!(
                "[{:?}] {} (稳定度: {:.1})\n",
                wv.category, wv.pattern, wv.stability
            ));
        }
    }
    if summary.is_empty() {
        summary = "暂未涌现出稳定的三观模式，继续积累对话。".to_string();
    }
    Ok(json!({"summary": summary, "patterns": worldviews}))
}