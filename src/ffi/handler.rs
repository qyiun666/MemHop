//! FFI command dispatcher — routes 11 commands to MemHop public API
//!
//! Key responsibilities:
//! - `dispatch_query_layer()` — merged Interface 5-12 routing
//! - `dispatch_update_title()` — merged Interface 13-16 routing
//! - All other commands directly expose MemHop methods

use crate::ffi::protocol::*;
use crate::query::types::*;
use crate::MemHop;
use serde_json::Value;

/// Dispatch a single FFI command against the MemHop instance
pub fn dispatch(db: &mut MemHop, cmd: FfiCommand) -> Result<Value, String> {
    match cmd {
        FfiCommand::Search { params } => {
            let r = db.search_memory(params).map_err(|e| e.to_string())?;
            serde_json::to_value(r).map_err(|e| e.to_string())
        }
        FfiCommand::Update { params } => {
            let r = db.update_memory(params).map_err(|e| e.to_string())?;
            serde_json::to_value(r).map_err(|e| e.to_string())
        }
        FfiCommand::QueryLayer {
            layer,
            action,
            get,
            list,
        } => dispatch_query_layer(db, &layer, &action, &get, &list),
        FfiCommand::UpdateTitle { layer, params } => dispatch_update_title(db, &layer, &params),
        FfiCommand::Dream { llm } => {
            let r = db.dream(llm).map_err(|e| e.to_string())?;
            serde_json::to_value(r).map_err(|e| e.to_string())
        }
        FfiCommand::MergeTopics {
            primary_id,
            secondary_ids,
        } => {
            let r = db
                .merge_topics(&primary_id, secondary_ids)
                .map_err(|e| e.to_string())?;
            serde_json::to_value(r).map_err(|e| e.to_string())
        }
        FfiCommand::Import { params } => dispatch_import(db, &params),
        FfiCommand::Session { params } => dispatch_session(db, &params),
        FfiCommand::BatchStore { batch } => {
            let r = db.batch_store(batch).map_err(|e| e.to_string())?;
            serde_json::to_value(r).map_err(|e| e.to_string())
        }
        FfiCommand::GraphQuery {
            graph_id,
            start_node,
            max_depth,
            edge_kinds,
        } => {
            let (subgraph, hops) = db
                .graph_query_internal(&graph_id, &start_node, max_depth, edge_kinds)
                .map_err(|e| e.to_string())?;
            Ok(serde_json::json!({
                "nodes": subgraph.nodes,
                "edges": subgraph.edges,
                "hops": hops,
            }))
        }
        FfiCommand::Delete { layer, id } => {
            let id_hash = crate::query::common::parse_id_to_hash(&id);
            match layer.as_str() {
                "l2" | "L2" | "topic" => db.delete_topic(id_hash).map_err(|e| e.to_string())?,
                "l3" | "L3" | "knowledge" | "graph" => {
                    db.delete_graph(id_hash).map_err(|e| e.to_string())?
                }
                "l5" | "L5" | "crystal" | "action_chain" => {
                    db.delete_action_chain(id_hash).map_err(|e| e.to_string())?
                }
                _ => return Err(format!("unsupported delete layer: {}", layer)),
            }
            Ok(serde_json::json!({"deleted": true}))
        }
        FfiCommand::Sync => {
            db.sync().map_err(|e| e.to_string())?;
            Ok(serde_json::json!({"synced": true}))
        }
        FfiCommand::Close => {
            db.checkpoint().map_err(|e| e.to_string())?;
            db.sync().map_err(|e| e.to_string())?;
            // Prevent Drop from double-checkpointing
            db.closed = true;
            Ok(serde_json::json!({"closed": true}))
        }
    }
}

// ============================================================================
// Unified Query Layer (合并 Interface 5-12)
// ============================================================================

fn dispatch_query_layer(
    db: &mut MemHop,
    layer: &str,
    action: &str,
    get: &QueryGetParams,
    list: &QueryListParams,
) -> Result<Value, String> {
    match (layer, action) {
        // --- L0 Profile ---
        ("l0", "get") => {
            let r = db.get_profile().map_err(|e| e.to_string())?;
            serde_json::to_value(r).map_err(|e| e.to_string())
        }
        // --- L1 Engram ---
        ("l1", "get") => {
            let id = get.id.as_deref().ok_or("missing 'id' for L1 get")?;
            let r = db.get_engram(id).map_err(|e| e.to_string())?;
            serde_json::to_value(r).map_err(|e| e.to_string())
        }
        ("l1", "list") => {
            let q = EngramListQuery {
                page: list.page.unwrap_or(1),
                page_size: list.page_size.unwrap_or(20),
                state_filter: list.state_filter.clone(),
                min_importance: list.min_importance,
                keyword: list.keyword.clone(),
            };
            let r = db.list_engrams(q).map_err(|e| e.to_string())?;
            serde_json::to_value(r).map_err(|e| e.to_string())
        }
        // --- L2 Topic ---
        ("l2", "get") => {
            let id = get.id.as_deref().ok_or("missing 'id' for L2 get")?;
            let r = db.get_topic(id).map_err(|e| e.to_string())?;
            serde_json::to_value(r).map_err(|e| e.to_string())
        }
        ("l2", "list") => {
            let q = TopicListQuery {
                page: list.page.unwrap_or(1),
                page_size: list.page_size.unwrap_or(20),
                active_only: list.active_only.unwrap_or(false),
                keyword: list.keyword.clone(),
            };
            let r = db.list_topics(q).map_err(|e| e.to_string())?;
            serde_json::to_value(r).map_err(|e| e.to_string())
        }
        // --- L3 Knowledge ---
        ("l3", "get") => {
            let id = get.id.as_deref().ok_or("missing 'id' for L3 get")?;
            let r = db.get_knowledge(id).map_err(|e| e.to_string())?;
            serde_json::to_value(r).map_err(|e| e.to_string())
        }
        ("l3", "list") => {
            let q = KnowledgeListQuery {
                page: list.page.unwrap_or(1),
                page_size: list.page_size.unwrap_or(20),
                domain_filter: list.domain_filter.clone(),
                knowledge_type: list.knowledge_type.clone(),
                keyword: list.keyword.clone(),
            };
            let r = db.list_knowledge(q).map_err(|e| e.to_string())?;
            serde_json::to_value(r).map_err(|e| e.to_string())
        }
        // --- L4 Archive (3 variants) ---
        ("l4", "list") => {
            let query = ArchivePageQuery {
                page: list.page.unwrap_or(1),
                page_size: list.page_size.unwrap_or(20),
                start_time: list.start_time,
                end_time: list.end_time,
                content_type: list.content_type.clone(),
            };
            let r = if let Some(tid) = &list.topic_id {
                db.list_archives_by_topic(tid, query)
                    .map_err(|e| e.to_string())?
            } else if let Some(nids) = &list.node_ids {
                db.list_archives_by_nodes(nids, query)
                    .map_err(|e| e.to_string())?
            } else {
                db.list_all_archives(query).map_err(|e| e.to_string())?
            };
            serde_json::to_value(r).map_err(|e| e.to_string())
        }
        // --- L5 Crystal ---
        ("l5", "list") => {
            let q = CrystalListQuery {
                page: list.page.unwrap_or(1),
                page_size: list.page_size.unwrap_or(20),
                status_filter: list.status_filter.clone(),
                min_trigger_count: list.min_trigger_count,
                keyword: list.keyword.clone(),
            };
            let r = db.list_crystals(q).map_err(|e| e.to_string())?;
            serde_json::to_value(r).map_err(|e| e.to_string())
        }
        _ => Err(format!(
            "unsupported query_layer: layer={}, action={}",
            layer, action
        )),
    }
}

// ============================================================================
// Unified Update Title (合并 Interface 13-16)
// ============================================================================

fn dispatch_update_title(
    db: &mut MemHop,
    layer: &str,
    params: &UpdateTitleParams,
) -> Result<Value, String> {
    match layer {
        "l0" => {
            let req = UpdateProfileRequest {
                name: params.name.clone(),
                role: params.role.clone(),
                personality: params.personality.clone(),
                worldview: params.worldview.clone(),
                preferences: params.preferences.clone(),
                lexicon: params.lexicon.clone(),
                style_traits: params.style_traits.clone(),
                emotion_patterns: params.emotion_patterns.clone(),
            };
            let r = db.update_profile(req).map_err(|e| e.to_string())?;
            serde_json::to_value(r).map_err(|e| e.to_string())
        }
        "l2" => {
            let id = params.id.as_deref().ok_or("missing 'id' for L2")?;
            let title = params
                .new_title
                .as_deref()
                .ok_or("missing 'new_title' for L2")?;
            let r = db
                .update_topic_title(id, title.to_string())
                .map_err(|e| e.to_string())?;
            serde_json::to_value(r).map_err(|e| e.to_string())
        }
        "l3" => {
            let id = params.id.as_deref().ok_or("missing 'id' for L3")?;
            let title = params
                .new_title
                .as_deref()
                .ok_or("missing 'new_title' for L3")?;
            let r = db
                .update_knowledge_title(id, title.to_string())
                .map_err(|e| e.to_string())?;
            serde_json::to_value(r).map_err(|e| e.to_string())
        }
        "l5" => {
            let id = params.id.as_deref().ok_or("missing 'id' for L5")?;
            let title = params
                .new_title
                .as_deref()
                .ok_or("missing 'new_title' for L5")?;
            let r = db
                .update_crystal_title(id, title.to_string())
                .map_err(|e| e.to_string())?;
            serde_json::to_value(r).map_err(|e| e.to_string())
        }
        _ => Err(format!("unsupported update_title layer: {}", layer)),
    }
}

// ============================================================================
// Import (Interface 19 — import_memory + build_l3_hypergraph_from_path)
// ============================================================================

fn dispatch_import(db: &mut MemHop, params: &ImportImportParams) -> Result<Value, String> {
    match params.action.as_str() {
        "build_l3" => {
            let path_str = params
                .path
                .as_deref()
                .ok_or("missing 'path' for build_l3")?;
            let path = std::path::Path::new(path_str);
            let r = db
                .build_l3_hypergraph_from_path(path)
                .map_err(|e| e.to_string())?;
            serde_json::to_value(r).map_err(|e| e.to_string())
        }
        "import" => {
            let target_layer = params
                .target_layer
                .as_deref()
                .ok_or("missing 'target_layer' for import")?;
            let mode = params.mode.as_deref().unwrap_or("merge");
            let import_mode = match mode {
                "overwrite" => ImportMode::Overwrite,
                "skip" => ImportMode::Skip,
                _ => ImportMode::Merge,
            };

            let target = match target_layer {
                "profile" => TargetLayer::Profile,
                "topic" => TargetLayer::Topic,
                "knowledge" => TargetLayer::Knowledge,
                _ => return Err(format!("unknown target_layer: {}", target_layer)),
            };

            let data_val = params.data.as_ref().ok_or("missing 'data' for import")?;
            let data: ImportData = serde_json::from_value(data_val.clone())
                .map_err(|e| format!("data parse: {}", e))?;

            let req = ImportRequest {
                target_layer: target,
                data,
                mode: import_mode,
                knowledge_title: params.knowledge_title.clone(),
            };

            let r = db.import_memory(req).map_err(|e| e.to_string())?;
            serde_json::to_value(r).map_err(|e| e.to_string())
        }
        _ => Err(format!(
            "unknown import action: '{}' (expected 'import' or 'build_l3')",
            params.action
        )),
    }
}

// ============================================================================
// Session (Interface 20 — activate/deactivate/list/adjust)
// ============================================================================

fn dispatch_session(db: &mut MemHop, params: &SessionParams) -> Result<Value, String> {
    let action = params.action.as_deref().ok_or("missing 'action'")?;
    match action {
        "activate" => {
            let id = params.topic_id.as_deref().ok_or("missing 'topic_id'")?;
            db.activate_topic(id, params.ttl_ms);
            Ok(serde_json::json!({"activated": id}))
        }
        "deactivate" => {
            let id = params.topic_id.as_deref().ok_or("missing 'topic_id'")?;
            db.deactivate_topic(id);
            Ok(serde_json::json!({"deactivated": id}))
        }
        "list" => {
            let ids = db.get_active_topic_ids();
            Ok(serde_json::json!({"active_topics": ids}))
        }
        "adjust" => {
            let id = params.topic_id.as_deref().ok_or("missing 'topic_id'")?;
            let delta = params
                .delta
                .ok_or("missing 'delta' (f32 adjustment factor, e.g. 0.5)")?;
            db.adjust_activation(id, delta);
            Ok(serde_json::json!({"adjusted": id, "delta": delta}))
        }
        _ => Err(format!("unknown session action: {}", action)),
    }
}
