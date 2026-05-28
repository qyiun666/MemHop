use std::io::{BufRead, BufReader, Write};
use std::sync::{Mutex, OnceLock};
use std::time::Instant;
use serde_json::{json, Value};
use memhop::{
    Brain, BrainConfig, PerceptionInput, RecallRequest,
    EmotionalState, Protection, ReflectionInput, ReflectionKind,
    ShelfManager, ShelfDomain,
};

const VERSION: &str = "0.9.1";

static START_TIME: OnceLock<Instant> = OnceLock::new();

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
            "initialize" => Ok(json!({"protocolVersion":"2024-11-05","serverInfo":{"name":"memhop-mcp-server","version":VERSION},"capabilities":{"tools":{}}})),
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
        {"name":"memhop_store","description":"Store a new perception/memory","inputSchema":{"type":"object","properties":{"text":{"type":"string"},"session_id":{"type":"string"},"valence":{"type":"number"},"arousal":{"type":"number"},"turn_id":{"type":"string"},"turn_index":{"type":"integer"},"topic_label":{"type":"string"}},"required":["text"]}},
        {"name":"memhop_recall","description":"Recall memories matching a query. Optionally accepts pre-encoded query_vector for external encoder benchmarks.","inputSchema":{"type":"object","properties":{"query":{"type":"string"},"session_id":{"type":"string"},"limit":{"type":"integer"},"max_tokens":{"type":"integer"},"query_vector":{"type":"array","items":{"type":"number"}}},"required":["query"]}},
        {"name":"memhop_reflect","description":"Create a reflection engram","inputSchema":{"type":"object","properties":{"content":{"type":"string"},"kind":{"type":"string"},"session_id":{"type":"string"}},"required":["content","kind"]}},
        {"name":"memhop_dream","description":"Run Dream consolidation cycle","inputSchema":{"type":"object","properties":{}}},
        {"name":"memhop_stats","description":"Get brain statistics","inputSchema":{"type":"object","properties":{}}},
        {"name":"memhop_count","description":"Get total engram count","inputSchema":{"type":"object","properties":{}}},
        {"name":"memhop_health","description":"Get health metrics (uptime, version, basic stats)","inputSchema":{"type":"object","properties":{}}},
        {"name":"memhop_complete_plan","description":"Complete a plan (mark as Completed, optionally summarize)","inputSchema":{"type":"object","properties":{"plan_id":{"type":"string"}},"required":["plan_id"]}},
        {"name":"memhop_get_plan_tree","description":"Get the plan tree (all root plans or descendants of a given plan)","inputSchema":{"type":"object","properties":{"plan_id":{"type":"string"}}}},
        {"name":"memhop_get_chat_history","description":"Get archived dialogue turns for a plan","inputSchema":{"type":"object","properties":{"plan_id":{"type":"string"}},"required":["plan_id"]}},
        {"name":"memhop_plan_stats","description":"Get aggregated plan statistics (domain distribution + tone trends)","inputSchema":{"type":"object","properties":{"start_time":{"type":"integer"},"end_time":{"type":"integer"}}}},
        {"name":"memhop_mount_shelf","description":"Mount a knowledge shelf from a file or directory path","inputSchema":{"type":"object","properties":{"path":{"type":"string"},"domain":{"type":"string"}},"required":["path"]}},
        {"name":"memhop_knowledge_search","description":"Search within a mounted knowledge shelf","inputSchema":{"type":"object","properties":{"query":{"type":"string"},"shelf_id":{"type":"string"},"limit":{"type":"integer"},"max_tokens":{"type":"integer"}},"required":["query","shelf_id"]}},
        {"name":"memhop_unmount_shelf","description":"Unmount and remove a knowledge shelf","inputSchema":{"type":"object","properties":{"shelf_id":{"type":"string"}},"required":["shelf_id"]}},
        {"name":"memhop_forget","description":"Forget a dialogue turn and its associated engrams","inputSchema":{"type":"object","properties":{"turn_id":{"type":"string"}},"required":["turn_id"]}}
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
        "memhop_health" => tool_health(brain, args),
        "memhop_complete_plan" => tool_complete_plan(brain, args),
        "memhop_get_plan_tree" => tool_get_plan_tree(brain, args),
        "memhop_get_chat_history" => tool_get_chat_history(brain, args),
        "memhop_plan_stats" => tool_plan_stats(brain, args),
        "memhop_mount_shelf" => tool_mount_shelf(brain, args),
        "memhop_knowledge_search" => tool_knowledge_search(brain, args),
        "memhop_unmount_shelf" => tool_unmount_shelf(brain, args),
        "memhop_forget" => tool_forget(brain, args),
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
    let session_id = args.get("session_id")
        .and_then(|v| v.as_str())
        .unwrap_or("default")
        .to_string();
    let valence = args.get("valence").and_then(|v| v.as_f64()).unwrap_or(0.0) as f32;
    let arousal = args.get("arousal").and_then(|v| v.as_f64()).unwrap_or(0.5) as f32;
    // v0.9.1: Optional turn-level fields
    let turn_id = args.get("turn_id")
        .and_then(|v| v.as_str())
        .unwrap_or("")
        .to_string();
    let turn_index = args.get("turn_index")
        .and_then(|v| v.as_u64())
        .unwrap_or(0) as u32;
    let topic_label = args.get("topic_label")
        .and_then(|v| v.as_str())
        .map(|s| s.to_string());

    let vector = brain.encode_text(&text);

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
        agent_response: None,
        dialogue_timestamp: None,
        source: None,
        turn_id,
        turn_index,
        segment_index: 0,
        topic_label,
    };

    let output = brain.perceive(input).map_err(|e| e.to_string())?;
    Ok(json!({
        "memory_id": output.engram_id,
        "plan_id": output.current_plan_id,
        "plan_hint": format!("{:?}", output.plan_hint),
        "plan_name": output.plan_name,
    }))
}

fn tool_recall(brain: &mut Brain, args: &Value) -> Result<Value, String> {
    let query = s(args, "query")?;
    let session_id = args.get("session_id")
        .and_then(|v| v.as_str())
        .unwrap_or("")
        .to_string();
    let limit = args.get("limit").and_then(|v| v.as_u64()).unwrap_or(5) as usize;
    let max_tokens = args.get("max_tokens").and_then(|v| v.as_u64()).map(|v| v as usize);

    // Accept pre-encoded query_vector for external encoder benchmarks
    let query_vector: Option<Vec<half::f16>> = args.get("query_vector")
        .and_then(|v| v.as_array())
        .map(|arr| {
            arr.iter()
                .filter_map(|x| x.as_f64().map(|f| half::f16::from_f32(f as f32)))
                .collect()
        });

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
        mode: memhop::RecallMode::Retrieval,
        use_reranker: false,
    };

    let resp = brain.recall(&req).map_err(|e| e.to_string())?;

    const MAX_PER_SESSION: usize = 3;

    let mut results: Vec<Value> = Vec::new();
    let mut session_count: usize = 0;
    for e in &resp.working_memory {
        if session_count >= MAX_PER_SESSION {
            break;
        }
        let text = match max_tokens {
            Some(n) => truncate_to_tokens(&e.text, n),
            None => e.text.clone(),
        };
        results.push(json!({"id": e.id, "text": text, "kind": format!("{}", e.kind), "source": "working_memory"}));
        session_count += 1;
    }
    for e in &resp.associations {
        let text = match max_tokens {
            Some(n) => truncate_to_tokens(&e.text, n),
            None => e.text.clone(),
        };
        results.push(json!({"id": e.id, "text": text, "kind": format!("{}", e.kind), "source": "association"}));
    }
    results.truncate(limit);

    Ok(json!({
        "results": results,
        "schemas": resp.schemas.iter().map(|e| json!({"id": e.id, "text": e.text})).collect::<Vec<_>>(),
        "hit_turns": resp.hit_turns,
        "aggregated_sessions": resp.aggregated_sessions,
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

// ── v0.9.1: Forget tool ────────────────────────────────────────

fn tool_forget(brain: &mut Brain, args: &Value) -> Result<Value, String> {
    let turn_id = s(args, "turn_id")?;
    brain.forget(&turn_id).map_err(|e| e.to_string())?;
    Ok(json!({"status": "ok"}))
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

fn tool_health(brain: &mut Brain, _args: &Value) -> Result<Value, String> {
    let g = brain.growth_state();
    let uptime_secs = START_TIME.get_or_init(|| Instant::now()).elapsed().as_secs();
    Ok(json!({
        "status": "ok",
        "version": VERSION,
        "uptime_secs": uptime_secs,
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

// ── v0.9.0: Knowledge Shelf tools ──────────────────────────

fn get_shelf_manager() -> &'static Mutex<ShelfManager> {
    static MANAGER: OnceLock<Mutex<ShelfManager>> = OnceLock::new();
    MANAGER.get_or_init(|| Mutex::new(ShelfManager::new()))
}

fn truncate_to_tokens(text: &str, max_tokens: usize) -> String {
    let words: Vec<&str> = text.split_whitespace().collect();
    if words.len() <= max_tokens {
        text.to_string()
    } else {
        words[..max_tokens].join(" ")
    }
}

fn tool_mount_shelf(brain: &mut Brain, args: &Value) -> Result<Value, String> {
    let path = s(args, "path")?;
    let domain_str = args
        .get("domain")
        .and_then(|v| v.as_str())
        .unwrap_or("doc");
    let domain = match domain_str {
        "code" => ShelfDomain::Code,
        "book" => ShelfDomain::Book,
        "paper" => ShelfDomain::Paper,
        _ => ShelfDomain::Doc,
    };

    let mut manager = get_shelf_manager().lock().map_err(|e| e.to_string())?;
    let shelf_id = manager.mount(&path, domain)?;
    manager.encode_shelf(&shelf_id, |text| brain.encode_text(text))?;

    Ok(json!({"shelf_id": shelf_id}))
}

fn tool_knowledge_search(brain: &mut Brain, args: &Value) -> Result<Value, String> {
    let query = s(args, "query")?;
    let shelf_id = s(args, "shelf_id")?;
    let limit = args
        .get("limit")
        .and_then(|v| v.as_u64())
        .unwrap_or(5) as usize;
    let max_tokens = args
        .get("max_tokens")
        .and_then(|v| v.as_u64())
        .map(|v| v as usize);

    let query_vector = brain.encode_text(&query);
    let manager = get_shelf_manager().lock().map_err(|e| e.to_string())?;
    let results = manager.search(&shelf_id, &query_vector, limit)?;

    let out: Vec<Value> = results
        .into_iter()
        .map(|r| {
            let text = match max_tokens {
                Some(n) => truncate_to_tokens(&r.text, n),
                None => r.text,
            };
            json!({
                "text": text,
                "location": r.location,
                "score": r.score,
                "source": r.source,
            })
        })
        .collect();

    Ok(json!({"status": "ok", "results": out}))
}

fn tool_unmount_shelf(_brain: &mut Brain, args: &Value) -> Result<Value, String> {
    let shelf_id = s(args, "shelf_id")?;
    let mut manager = get_shelf_manager().lock().map_err(|e| e.to_string())?;
    manager.unmount(&shelf_id)?;
    Ok(json!({"status": "ok"}))
}

