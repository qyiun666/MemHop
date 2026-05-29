use std::io::{BufRead, BufReader, Write};
use std::sync::{Mutex, OnceLock};
use std::time::Instant;
use serde_json::{json, Value};
use memhop::{
    Brain, BrainConfig, EngramKind, PerceptionInput, RecallRequest,
    EmotionalState, Protection, ReflectionInput, ReflectionKind,
    ShelfManager, ShelfDomain,
    StoreResult,
};

const VERSION: &str = "0.11.0";

static START_TIME: OnceLock<Instant> = OnceLock::new();

fn main() {
    let db_path = std::env::var("MEMHOP_DB_PATH")
        .unwrap_or_else(|_| "/tmp/memhop-mcp.db".to_string());
    let onnx_model_path = std::env::var("MEMHOP_ONNX_MODEL").ok();
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
                    let mut brain_config = BrainConfig::default();
                    if let Some(ref path) = onnx_model_path {
                        brain_config.onnx_model_path = Some(path.clone());
                        eprintln!("memhop-mcp-server: using ONNX model from {}", path);
                    }
                    match Brain::open(&db_path, brain_config, None) {
                        Ok(b) => {
                            // v0.11.0: Rebuild shelf registry from LMDB
                            if let Ok(mut manager) = get_shelf_manager().lock() {
                                let _ = manager.rebuild_registry(&b);
                            }
                            brain = Some(b);
                        }
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
        {"name":"memhop_store","description":"Store a new memory/episode or knowledge chunk (ADD-only, auto-dedup).","inputSchema":{"type":"object","properties":{"text":{"type":"string"},"kind":{"type":"string","description":"'episode' (default) or 'knowledge'"},"session_id":{"type":"string"},"tree_path":{"type":"string","description":"Knowledge tree path (required for kind=knowledge)"},"source_path":{"type":"string","description":"Original file path (for knowledge)"},"source_textunit":{"type":"string","description":"Text unit reference (e.g., '§3.2')"},"valence":{"type":"number"},"arousal":{"type":"number"}},"required":["text"]}},
        {"name":"memhop_recall","description":"Recall memories matching a query. Returns unified results across all types.","inputSchema":{"type":"object","properties":{"query":{"type":"string"},"session_id":{"type":"string"},"limit":{"type":"integer"},"kind_filter":{"type":"array","items":{"type":"string"},"description":"Filter by kind: 'episode', 'knowledge'. Empty = all."},"tree":{"type":"string","description":"Filter by knowledge tree path."},"query_vector":{"type":"array","items":{"type":"number"}}},"required":["query"]}},
        {"name":"memhop_reflect","description":"Create a reflection engram","inputSchema":{"type":"object","properties":{"content":{"type":"string"},"kind":{"type":"string"},"session_id":{"type":"string"}},"required":["content","kind"]}},
        {"name":"memhop_dream","description":"Run Dream consolidation cycle (includes Knowledge engrams).","inputSchema":{"type":"object","properties":{}}},
        {"name":"memhop_stats","description":"Get brain statistics","inputSchema":{"type":"object","properties":{}}},
        {"name":"memhop_count","description":"Get total engram count","inputSchema":{"type":"object","properties":{}}},
        {"name":"memhop_health","description":"Get health metrics (uptime, version, basic stats)","inputSchema":{"type":"object","properties":{}}},
        {"name":"memhop_complete_plan","description":"Complete a plan (mark as Completed, optionally summarize)","inputSchema":{"type":"object","properties":{"plan_id":{"type":"string"}},"required":["plan_id"]}},
        {"name":"memhop_get_plan_tree","description":"Get the plan tree (all root plans or descendants of a given plan)","inputSchema":{"type":"object","properties":{"plan_id":{"type":"string"}}}},
        {"name":"memhop_get_chat_history","description":"Get archived dialogue turns for a plan","inputSchema":{"type":"object","properties":{"plan_id":{"type":"string"}},"required":["plan_id"]}},
        {"name":"memhop_plan_stats","description":"Get aggregated plan statistics (domain distribution + tone trends)","inputSchema":{"type":"object","properties":{"start_time":{"type":"integer"},"end_time":{"type":"integer"}}}},
        {"name":"memhop_mount_tree","description":"Mount a knowledge tree from a file or directory path. Path is the identity.","inputSchema":{"type":"object","properties":{"path":{"type":"string"},"domain":{"type":"string","description":"'code', 'book', 'paper', 'doc', or 'generic'"}},"required":["path"]}},
        {"name":"memhop_unmount_tree","description":"Unmount a knowledge tree by tree path.","inputSchema":{"type":"object","properties":{"tree_path":{"type":"string"}},"required":["tree_path"]}},
        {"name":"memhop_tree_status","description":"List all mounted knowledge trees with metadata.","inputSchema":{"type":"object","properties":{"tree_path":{"type":"string","description":"Optional: get status for a specific tree. Returns all trees if omitted."}}}},
        {"name":"memhop_knowledge_search","description":"[DEPRECATED] Use memhop_recall with tree and kind_filter instead.","inputSchema":{"type":"object","properties":{"query":{"type":"string"},"shelf_id":{"type":"string"},"limit":{"type":"integer"}},"required":["query","shelf_id"]}},
        {"name":"memhop_forget","description":"Forget a dialogue turn and its associated engrams","inputSchema":{"type":"object","properties":{"turn_id":{"type":"string"}},"required":["turn_id"]}},
        {"name":"memhop_list_schemas","description":"List all emerged schema engrams","inputSchema":{"type":"object","properties":{}}}
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

    match kind_str {
        "episode" => {
            let session_id = args.get("session_id")
                .and_then(|v| v.as_str())
                .unwrap_or("default")
                .to_string();
            let valence = args.get("valence").and_then(|v| v.as_f64()).unwrap_or(0.0) as f32;
            let arousal = args.get("arousal").and_then(|v| v.as_f64()).unwrap_or(0.5) as f32;

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
                turn_id: String::new(),
                turn_index: 0,
                segment_index: 0,
                topic_label: None,
            };

            let output = brain.perceive(input).map_err(|e| e.to_string())?;
            Ok(json!({
                "status": "stored",
                "engram_id": output.engram_id,
                "plan_id": output.current_plan_id,
                "plan_hint": format!("{:?}", output.plan_hint),
                "plan_name": output.plan_name,
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
            let vector = brain.encode_text(&text);
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
        tree,
        kind_filter,
    };

    let resp = brain.recall(&req).map_err(|e| e.to_string())?;

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

fn tool_health(brain: &mut Brain, _args: &Value) -> Result<Value, String> {
    let g = brain.growth_state();
    let uptime_secs = START_TIME.get_or_init(Instant::now).elapsed().as_secs();
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
        use_reranker: false,
        tree: Some(shelf_id),
        kind_filter: vec![EngramKind::Knowledge],
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

