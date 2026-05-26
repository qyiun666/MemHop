use std::io::{BufRead, BufReader, Write};
use serde_json::{json, Value};
use memhop::{
    Brain, BrainConfig, PerceptionInput, RecallRequest,
    EmotionalState, Protection, ReflectionInput, ReflectionKind,
};

fn main() {
    let db_path = std::env::var("MEMHOP_DB_PATH")
        .unwrap_or_else(|_| "/tmp/memhop-mcp.db".to_string());
    let mut brain: Option<Brain> = None;
    let stdin = std::io::stdin();
    let reader = BufReader::new(stdin.lock());
    let mut stdout = std::io::stdout();

    for line in reader.lines() {
        let line = match line { Ok(l) => l, Err(_) => break };
        let line = line.trim().to_string();
        if line.is_empty() { continue; }

        let req: Value = match serde_json::from_str(&line) {
            Ok(v) => v,
            Err(e) => {
                let _ = writeln!(stdout, "{}", json!({"jsonrpc":"2.0","error":{"code":-32700,"message":format!("parse error: {}", e)},"id":null}));
                continue;
            }
        };

        let id = req.get("id").cloned().or(Some(Value::Null));
        let method = req.get("method").and_then(|m| m.as_str()).unwrap_or("");

        let result: Result<Value, String> = match method {
            "initialize" => Ok(json!({"protocolVersion":"2024-11-05","serverInfo":{"name":"memhop-mcp-server","version":"0.7.3"},"capabilities":{"tools":{}}})),
            "notifications/initialized" => continue,
            "tools/list" => Ok(tools_list()),
            "tools/call" => {
                if brain.is_none() {
                    match Brain::open(&db_path, BrainConfig::default(), None) {
                        Ok(b) => brain = Some(b),
                        Err(e) => {
                            let resp = json!({"jsonrpc":"2.0","id":id,"error":{"code":-32000,"message":format!("failed to open brain: {}", e)}});
                            let _ = writeln!(stdout, "{}", resp);
                            let _ = stdout.flush();
                            continue;
                        }
                    }
                }
                tool_call(brain.as_mut().unwrap(), req.get("params"))
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

fn tools_list() -> Value {
    json!({"tools":[
        {"name":"memhop_store","description":"Store a new perception/memory","inputSchema":{"type":"object","properties":{"text":{"type":"string"},"session_id":{"type":"string"},"valence":{"type":"number"},"arousal":{"type":"number"}},"required":["text"]}},
        {"name":"memhop_recall","description":"Recall memories matching a query","inputSchema":{"type":"object","properties":{"query":{"type":"string"},"session_id":{"type":"string"},"limit":{"type":"integer"}},"required":["query"]}},
        {"name":"memhop_reflect","description":"Create a reflection engram","inputSchema":{"type":"object","properties":{"content":{"type":"string"},"kind":{"type":"string"},"session_id":{"type":"string"}},"required":["content","kind"]}},
        {"name":"memhop_dream","description":"Run Dream consolidation cycle","inputSchema":{"type":"object","properties":{}}},
        {"name":"memhop_stats","description":"Get brain statistics","inputSchema":{"type":"object","properties":{}}},
        {"name":"memhop_count","description":"Get total engram count","inputSchema":{"type":"object","properties":{}}}
    ]})
}

fn tool_call(brain: &mut Brain, params: Option<&Value>) -> Result<Value, String> {
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
        _ => Err(format!("Unknown tool: {}", name)),
    }
}

fn s(val: &Value, key: &str) -> Result<String, String> {
    val.get(key)
        .and_then(|v| v.as_str())
        .map(|s| s.to_string())
        .ok_or_else(|| format!("Missing required parameter: {}", key))
}

fn tool_store(brain: &mut Brain, args: &Value) -> Result<Value, String> {
    let text = s(args, "text")?;
    let session_id = args.get("session_id")
        .and_then(|v| v.as_str())
        .unwrap_or("default")
        .to_string();
    let valence = args.get("valence").and_then(|v| v.as_f64()).unwrap_or(0.0) as f32;
    let arousal = args.get("arousal").and_then(|v| v.as_f64()).unwrap_or(0.5) as f32;

    let vector = vec![half::f16::from_f32(0.0); memhop::VECTOR_DIM];

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
    };

    let id = brain.perceive(input).map_err(|e| e.to_string())?;
    Ok(json!({"memory_id": id}))
}

fn tool_recall(brain: &mut Brain, args: &Value) -> Result<Value, String> {
    let query = s(args, "query")?;
    let session_id = args.get("session_id")
        .and_then(|v| v.as_str())
        .unwrap_or("")
        .to_string();
    let limit = args.get("limit").and_then(|v| v.as_u64()).unwrap_or(5) as usize;

    let req = RecallRequest {
        query,
        query_vector: None,
        session_id,
        emotional_state: EmotionalState::default(),
        attention_anchors: vec![],
        current_goal: None,
        recent_limit: limit,
        spread_depth: 3,
        spread_top_k: limit,
    };

    let resp = brain.recall(&req).map_err(|e| e.to_string())?;

    let mut results: Vec<Value> = Vec::new();
    for e in &resp.working_memory {
        results.push(json!({"id": e.id, "text": e.text, "kind": format!("{}", e.kind), "source": "working_memory"}));
    }
    for e in &resp.associations {
        results.push(json!({"id": e.id, "text": e.text, "kind": format!("{}", e.kind), "source": "association"}));
    }
    results.truncate(limit);

    Ok(json!({
        "results": results,
        "schemas": resp.schemas.iter().map(|e| json!({"id": e.id, "text": e.text})).collect::<Vec<_>>(),
        "trace": {
            "latency_us": resp.trace.latency_us,
            "hopfield_candidates": resp.trace.hopfield_candidates,
            "spread_steps": resp.trace.spread_steps,
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
    }))
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
    }))
}

fn tool_count(brain: &mut Brain, _args: &Value) -> Result<Value, String> {
    Ok(json!({"count": brain.memory_count() + brain.hippocampus_len()}))
}
