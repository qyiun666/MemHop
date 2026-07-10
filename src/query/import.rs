// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Import memory: import_memory() for L0/L2/L3 layers.
//! Also provides import_l3_from_path() for file-based L3 import.

use crate::index::sparse::SparseIndex;
use crate::layers::context::ContextSlot;
use crate::layers::profile::ProfileSlot;
use crate::query::types::*;
use crate::shared::common;
use crate::shared::common::format_hash;
use crate::storage::record::*;
use crate::storage::StorageEngine;
use crate::store::write_slot;
use crate::util::hash_id;
use crate::MemHopError;
use std::collections::HashMap;
use std::path::Path;

fn calculate_l2_sparse_index_data(ctx: &ContextSlot, engine: &StorageEngine) -> (Vec<String>, u32) {
    let title = if ctx.fused_keywords.is_empty() {
        ctx.user_keywords.join(", ")
    } else {
        ctx.fused_keywords.join(", ")
    };
    let (mut terms, base_doc_len) = common::build_l2_sparse_terms(&title, &ctx.fused_summary);

    let l3_refs: Vec<u64> = ctx
        .user_l3_refs
        .iter()
        .chain(ctx.agent_l3_refs.iter())
        .copied()
        .collect();
    let mut l3_doc_len: usize = 0;
    for &l3_id_hash in &l3_refs {
        if let Some((_, data)) = engine.read_record(l3_id_hash).ok().flatten() {
            if let Ok(slot) =
                bincode::deserialize::<crate::layers::hypergraph::HypergraphSlot>(data)
            {
                terms.extend(crate::index::sparse::tokenize(&slot.name));
                l3_doc_len += slot.name.len();
            }
        }
    }

    let doc_len = base_doc_len as usize + l3_doc_len;

    (terms, doc_len as u32)
}

#[allow(clippy::too_many_arguments)]
pub fn import_memory(
    engine: &mut StorageEngine,
    sparse_index: &mut SparseIndex,
    request: ImportRequest,
    tracker: Option<&mut crate::l3::DegreeTracker>,
    index_map: Option<&mut std::collections::HashMap<u64, crate::l3::L3Index>>,
) -> Result<ImportResult, MemHopError> {
    match request.target_layer {
        TargetLayer::Profile => import_l0_profile(engine, request.data, request.mode),
        TargetLayer::Topic => import_l2_topics(
            engine,
            sparse_index,
            request.data,
            request.mode,
            request.knowledge_title,
        ),
        TargetLayer::Knowledge => import_l3_knowledge(
            engine,
            sparse_index,
            request.data,
            request.mode,
            request.knowledge_title,
            tracker,
            index_map,
        ),
    }
}

// ============================================================================
// L0 Profile Import
// ============================================================================

fn import_l0_profile(
    engine: &mut StorageEngine,
    data: ImportData,
    mode: ImportMode,
) -> Result<ImportResult, MemHopError> {
    let now_ms = common::now_ms();

    if let ImportData::Profile {
        name,
        role,
        personality,
        worldview,
        preferences,
    } = data
    {
        let profile_id_hash = hash_id("profile");

        if engine.contains(profile_id_hash) {
            match mode {
                ImportMode::Merge | ImportMode::Overwrite => {
                    let (_, data_bytes) = engine
                        .read_record(profile_id_hash)?
                        .ok_or(MemHopError::PageNotFound(0))?;

                    let mut profile = bincode::deserialize::<ProfileSlot>(data_bytes)
                        .map_err(|e| MemHopError::Serialization(e.to_string()))?;

                    if let Some(n) = name {
                        profile.name = n;
                    }
                    if let Some(r) = role {
                        profile.role = r;
                    }
                    if let Some(p) = personality {
                        profile.personality = p;
                    }
                    if let Some(w) = worldview {
                        profile.worldview = w;
                    }
                    if let Some(pref) = preferences {
                        profile.preferences = pref;
                    }

                    profile.updated_at = now_ms;
                    profile.version += 1;

                    write_slot(engine, REC_L0_PROFILE, profile_id_hash, &profile)?;

                    Ok(ImportResult {
                        status: ImportStatus::Success,
                        id: None,
                        ids: None,
                        created_ids: vec![],
                        updated_ids: vec![format_hash(profile_id_hash)],
                        skipped_count: 0,
                        errors: vec![],
                        knowledge_title: None,
                        node_count: 0,
                    })
                }
                ImportMode::Skip => Ok(ImportResult {
                    status: ImportStatus::Success,
                    id: None,
                    ids: None,
                    created_ids: vec![],
                    updated_ids: vec![],
                    skipped_count: 1,
                    errors: vec![],
                    knowledge_title: None,
                    node_count: 0,
                }),
            }
        } else {
            let profile = ProfileSlot {
                id_hash: profile_id_hash,
                name: name.unwrap_or_else(|| "Agent".to_string()),
                role: role.unwrap_or_else(|| "Assistant".to_string()),
                personality: personality.unwrap_or_default(),
                worldview: worldview.unwrap_or_default(),
                preferences: preferences.unwrap_or_default(),
                lexicon: HashMap::new(),
                style_traits: Vec::new(),
                emotion_patterns: HashMap::new(),
                created_at: now_ms,
                updated_at: now_ms,
                version: 1,
            };

            write_slot(engine, REC_L0_PROFILE, profile_id_hash, &profile)?;

            Ok(ImportResult {
                status: ImportStatus::Success,
                id: Some(format_hash(profile_id_hash)),
                ids: Some(vec![format_hash(profile_id_hash)]),
                created_ids: vec![format_hash(profile_id_hash)],
                updated_ids: vec![],
                skipped_count: 0,
                errors: vec![],
                knowledge_title: None,
                node_count: 1,
            })
        }
    } else {
        Err(MemHopError::ConfigError(
            "Invalid import data for L0".to_string(),
        ))
    }
}

// ============================================================================
// L2 Topics Import
// ============================================================================

#[allow(clippy::too_many_arguments)]
fn import_l2_topics(
    engine: &mut StorageEngine,
    sparse_index: &mut SparseIndex,
    data: ImportData,
    mode: ImportMode,
    knowledge_title: Option<String>,
) -> Result<ImportResult, MemHopError> {
    let now_ms = common::now_ms();

    if let ImportData::Topics(items) = data {
        let mut created_ids = Vec::new();
        let mut updated_ids = Vec::new();
        let mut skipped_count = 0;
        let mut errors: Vec<ImportError> = Vec::new();

        let l3_hash = if let Some(ref title) = knowledge_title {
            let hash = hash_id(title);
            if engine.contains(hash) {
                Some(hash)
            } else {
                None
            }
        } else {
            None
        };

        for (item_idx, item) in items.iter().enumerate() {
            let result = (|| -> Result<(), MemHopError> {
                let id_hash = hash_id(&item.title);

                if engine.contains(id_hash) {
                    match mode {
                        ImportMode::Merge | ImportMode::Overwrite => {
                            let (_, data_bytes) = engine
                                .read_record(id_hash)?
                                .ok_or(MemHopError::PageNotFound(0))?;

                            let mut ctx = bincode::deserialize::<ContextSlot>(data_bytes)
                                .map_err(|e| MemHopError::Serialization(e.to_string()))?;

                            ctx.fused_keywords = vec![item.title.clone()];
                            ctx.fused_summary = item.summary.clone();

                            if let Some(l3_h) = l3_hash {
                                if !ctx.user_l3_refs.contains(&l3_h)
                                    && !ctx.agent_l3_refs.contains(&l3_h)
                                {
                                    ctx.agent_l3_refs.push(l3_h);
                                }
                            }

                            ctx.updated_at = now_ms;
                            ctx.version += 1;

                            sparse_index.remove_document(ctx.id);
                            let (terms, doc_len) = calculate_l2_sparse_index_data(&ctx, engine);
                            sparse_index.add_document(ctx.id, terms, doc_len);

                            write_slot(engine, REC_L2_TOPIC, id_hash, &ctx)?;

                            updated_ids.push(format_hash(id_hash));
                        }
                        ImportMode::Skip => {
                            skipped_count += 1;
                        }
                    }
                } else {
                    let user_l3_refs = Vec::new();
                    let mut agent_l3_refs = Vec::new();
                    if let Some(l3_h) = l3_hash {
                        agent_l3_refs.push(l3_h);
                    }

                    let ctx = ContextSlot {
                        id: id_hash,
                        fused_keywords: vec![item.title.clone()],
                        fused_summary: item.summary.clone(),
                        children_ids: vec![],
                        scene_id: 0,
                        depth: 1,
                        user_keywords: vec![],
                        user_timestamp: now_ms,
                        user_l4_refs: vec![],
                        user_l3_refs,
                        agent_keywords: vec![],
                        agent_timestamp: now_ms,
                        agent_l4_refs: vec![],
                        agent_l3_refs,
                        parent_id: None,
                        centroid_page_ref: 0,
                        created_at: now_ms,
                        updated_at: now_ms,
                        version: 4,
                    };

                    write_slot(engine, REC_L2_TOPIC, id_hash, &ctx)?;

                    let (terms, doc_len) = calculate_l2_sparse_index_data(&ctx, engine);
                    sparse_index.add_document(id_hash, terms, doc_len);

                    created_ids.push(format_hash(id_hash));
                }

                Ok(())
            })();

            if let Err(e) = result {
                errors.push(ImportError {
                    index: item_idx,
                    message: e.to_string(),
                });
            }
        }

        let status = if errors.is_empty() {
            ImportStatus::Success
        } else {
            ImportStatus::PartialSuccess
        };

        let node_count = created_ids.len();
        let ids = if created_ids.is_empty() {
            None
        } else {
            Some(created_ids.clone())
        };
        let id = created_ids.first().cloned();
        Ok(ImportResult {
            status,
            id,
            ids,
            created_ids,
            updated_ids,
            skipped_count,
            errors,
            knowledge_title,
            node_count,
        })
    } else {
        Err(MemHopError::ConfigError(
            "Invalid import data for L2".to_string(),
        ))
    }
}

// ============================================================================
// L3 Knowledge Import
// ============================================================================

#[allow(clippy::too_many_arguments)]
fn import_l3_knowledge(
    engine: &mut StorageEngine,
    _sparse_index: &mut SparseIndex,
    data: ImportData,
    mode: ImportMode,
    knowledge_title: Option<String>,
    mut tracker: Option<&mut crate::l3::DegreeTracker>,
    mut index_map: Option<&mut std::collections::HashMap<u64, crate::l3::L3Index>>,
) -> Result<ImportResult, MemHopError> {
    let items = match data {
        ImportData::Knowledge(items) => items,
        _ => {
            return Err(MemHopError::ConfigError(
                "Expected Knowledge import data".to_string(),
            ))
        }
    };

    let now_ms = crate::shared::common::now_ms();
    let mut created_ids: Vec<String> = Vec::new();
    let mut updated_ids: Vec<String> = Vec::new();
    let mut skipped_count = 0usize;
    let mut errors: Vec<ImportError> = Vec::new();

    // Track domain → graph_id mapping to create HypergraphSlot per domain
    use std::collections::HashMap;
    let mut graph_cache: HashMap<String, u64> = HashMap::new();

    for (idx, item) in items.iter().enumerate() {
        let title_hash = crate::util::hash_id(&item.title);

        match mode {
            ImportMode::Skip => {
                if engine.contains(title_hash) {
                    skipped_count += 1;
                    continue;
                }
            }
            ImportMode::Overwrite => {
                if engine.contains(title_hash) {
                    let _ = crate::l3::store::delete_node_with_engine(
                        engine,
                        &item.title,
                        tracker.as_deref_mut(),
                        index_map.as_deref_mut(),
                    );
                }
            }
            ImportMode::Merge => {
                if engine.contains(title_hash) {
                    updated_ids.push(format_hash(title_hash));
                    continue;
                }
            }
        }

        let graph_id = *graph_cache.entry(item.domain.clone()).or_insert_with(|| {
            let gid = crate::util::hash_id(&item.domain);
            if !engine.contains(gid) {
                if let Err(e) = create_hypergraph_slot(engine, gid, &item.domain, now_ms) {
                    errors.push(ImportError {
                        index: idx,
                        message: e.to_string(),
                    });
                    return gid; // Still return gid so we don't block node creation
                }
            }
            gid
        });

        let node = crate::layers::hypergraph::HypergraphNode {
            id_hash: title_hash,
            graph_id,
            title: item.title.clone(),
            node_type: item.knowledge_type.clone(),
            // L3 is a knowledge graph — store only a short summary/index,
            // not the original text (which belongs in L4 Archive).
            content: item.text.chars().take(200).collect(),
            keywords: item.keywords.clone(),
            source_ref: item.source_ref.clone(),
            importance: 0.7,
            summary: None,
            valid_from: now_ms,
            valid_until: 0,
            created_at: now_ms,
            updated_at: now_ms,
            version: 1,
        };

        match crate::l3::store::add_node_with_engine(
            engine,
            node,
            tracker.as_deref_mut(),
            index_map.as_deref_mut(),
        ) {
            Ok(id) => created_ids.push(id),
            Err(e) => errors.push(ImportError {
                index: idx,
                message: e.to_string(),
            }),
        }
    }

    let status = if errors.is_empty() && !created_ids.is_empty() {
        ImportStatus::Success
    } else if !created_ids.is_empty() || !updated_ids.is_empty() {
        ImportStatus::PartialSuccess
    } else {
        ImportStatus::Failed
    };

    let node_count = created_ids.len();
    let ids = if created_ids.is_empty() {
        None
    } else {
        Some(created_ids.clone())
    };
    let id = created_ids.first().cloned();
    Ok(ImportResult {
        status,
        id,
        ids,
        created_ids,
        updated_ids,
        skipped_count,
        errors,
        knowledge_title,
        node_count,
    })
}

/// Create a HypergraphSlot (L3 graph container) for a new domain
fn create_hypergraph_slot(
    engine: &mut StorageEngine,
    id_hash: u64,
    name: &str,
    now_ms: i64,
) -> Result<(), MemHopError> {
    use crate::layers::hypergraph::{HypergraphSlot, HypergraphSource};

    let slot = HypergraphSlot {
        id_hash,
        name: name.to_string(),
        source: HypergraphSource::Manual,
        node_count: 0,
        edge_count: 0,
        created_at: now_ms,
        updated_at: now_ms,
        version: 1,
    };

    write_slot(engine, REC_L3_GRAPH_SLOT, id_hash, &slot)?;
    Ok(())
}

// ============================================================================
// File-based L3 Hypergraph Builder
// ============================================================================

/// Supported source file extensions for L3 import
const SOURCE_EXTENSIONS: &[&str] = &["rs", "py", "js", "ts", "go", "java", "c", "cpp", "h"];

/// Regex patterns for extracting import statements by language
fn extract_imports(content: &str, ext: &str) -> Vec<String> {
    let mut imports = Vec::new();
    for line in content.lines() {
        let line = line.trim();
        match ext {
            "rs" => {
                if line.starts_with("use ") && line.ends_with(';') {
                    let path = line.trim_start_matches("use ").trim_end_matches(';').trim();
                    // Only keep crate-internal imports (start with crate:: or super::)
                    if path.starts_with("crate::") || path.starts_with("super::") {
                        // Extract the module path (first 2 segments)
                        let segments: Vec<&str> = path.split("::").collect();
                        if segments.len() >= 2 {
                            let module = if segments[0] == "crate" && segments.len() >= 2 {
                                segments[1].trim_start_matches('{').to_string()
                            } else if segments[0] == "super" {
                                format!("super::{}", segments.get(1).unwrap_or(&""))
                            } else {
                                segments[0].to_string()
                            };
                            if !module.is_empty() && module != "self" {
                                imports.push(module);
                            }
                        }
                    }
                }
                if line.starts_with("mod ") && line.ends_with(';') && !line.contains("pub") {
                    let module = line.trim_start_matches("mod ").trim_end_matches(';').trim();
                    if !module.is_empty() {
                        imports.push(module.to_string());
                    }
                }
            }
            "py" if line.starts_with("import ") || line.starts_with("from ") => {
                let parts: Vec<&str> = line.split_whitespace().collect();
                if parts.len() >= 2 {
                    let module = if parts[0] == "from" {
                        parts[1].trim_end_matches("import").to_string()
                    } else {
                        parts[1].to_string()
                    };
                    // Only relative imports
                    if module.starts_with('.') {
                        imports.push(module);
                    }
                }
            }
            "js" | "ts" if line.contains("import ") && line.contains("from ") => {
                if let Some(path_start) = line.find("from ") {
                    let rest = &line[path_start + 5..];
                    let path = rest
                        .trim()
                        .trim_start_matches(['"', '\''])
                        .split(['"', '\''])
                        .next()
                        .unwrap_or("");
                    if path.starts_with('.') || path.starts_with('/') {
                        imports.push(path.to_string());
                    }
                }
            }
            "go" if line.starts_with('"') && line.contains('/') => {
                let pkg = line.trim_matches('"').trim_matches(';').to_string();
                imports.push(pkg);
            }
            _ => {}
        }
    }
    imports
}

/// Collect source files recursively from a directory
fn collect_source_files(path: &Path) -> Vec<(std::path::PathBuf, String)> {
    let mut files = Vec::new();
    if path.is_file() {
        if let Some(ext) = path.extension().and_then(|e| e.to_str()) {
            if SOURCE_EXTENSIONS.contains(&ext) {
                files.push((path.to_path_buf(), ext.to_string()));
            }
        }
        return files;
    }

    if let Ok(entries) = std::fs::read_dir(path) {
        let mut entries: Vec<_> = entries.filter_map(|e| e.ok()).collect();
        entries.sort_by_key(|e| e.file_name());
        for entry in entries {
            let entry_path = entry.path();
            if entry_path.is_dir() {
                // Skip hidden directories and target/build directories
                let dir_name = entry.file_name().to_string_lossy().to_string();
                if dir_name.starts_with('.') || dir_name == "target" || dir_name == "node_modules" {
                    continue;
                }
                files.extend(collect_source_files(&entry_path));
            } else if entry_path.is_file() {
                let ext_owned = entry_path
                    .extension()
                    .map(|e| e.to_string_lossy().to_string());
                match ext_owned {
                    Some(ext) if SOURCE_EXTENSIONS.contains(&ext.as_str()) => {
                        files.push((entry_path, ext));
                    }
                    _ => {}
                }
            }
        }
    }
    files
}

/// Derive a module name from a file path relative to the base directory
fn derive_module_name(file_path: &Path, base_path: &Path) -> String {
    let relative = file_path.strip_prefix(base_path).unwrap_or(file_path);
    let stem = relative
        .with_extension("")
        .to_string_lossy()
        .replace(['/', '\\'], "::");
    // Remove trailing ::mod or ::index
    let stem = stem
        .trim_end_matches("::mod")
        .trim_end_matches("::index")
        .to_string();
    if stem.is_empty() {
        "root".to_string()
    } else {
        stem
    }
}

/// Extract Rust module/symbol keywords from module name and file content
fn extract_rust_symbols(module_name: &str, content: &str) -> Vec<String> {
    let mut keywords: Vec<String> = module_name
        .split("::")
        .map(|s| s.to_lowercase())
        .filter(|s| !s.is_empty() && s != "mod")
        .collect();

    // Extract pub fn/struct/enum/trait names (Rust-specific heuristic)
    for line in content.lines() {
        let line = line.trim();
        if line.starts_with("pub fn ") || line.starts_with("pub async fn ") {
            if let Some(name) = line
                .split('(')
                .next()
                .map(|s| s.split_whitespace().last().unwrap_or(""))
            {
                if !name.is_empty() && name.len() < 40 {
                    keywords.push(name.to_string());
                }
            }
        } else if line.starts_with("pub struct ") || line.starts_with("pub enum ") {
            if let Some(name) = line
                .split_whitespace()
                .nth(2)
                .map(|s| s.trim_end_matches(|c: char| !c.is_alphanumeric() && c != '_'))
            {
                if !name.is_empty() && name.len() < 40 {
                    keywords.push(name.to_string());
                }
            }
        }
        if keywords.len() >= 15 {
            break;
        }
    }
    keywords.truncate(15);
    keywords
}

/// Build L3 hypergraph from a file path
///
/// Scans source files recursively, creates HypergraphNodes for each module,
/// and creates Dependency edges based on import statements.
/// Also creates an L2 topic linked to the L3 graph for search discoverability.
#[allow(clippy::too_many_arguments)]
pub fn build_l3_hypergraph_from_path(
    engine: &mut StorageEngine,
    sparse_index: &mut SparseIndex,
    path: &Path,
    mut tracker: Option<&mut crate::l3::DegreeTracker>,
    mut index_map: Option<&mut std::collections::HashMap<u64, crate::l3::L3Index>>,
) -> Result<ImportResult, MemHopError> {
    use crate::layers::hypergraph::{
        GraphEdgeKind, HypergraphEdge, HypergraphSlot, HypergraphSource,
    };

    let now_ms = common::now_ms();

    if !path.exists() {
        return Err(MemHopError::Io(std::io::Error::new(
            std::io::ErrorKind::NotFound,
            format!("Path not found: {}", path.display()),
        )));
    }

    let base_path = if path.is_dir() {
        path.to_path_buf()
    } else {
        path.parent().unwrap_or(path).to_path_buf()
    };

    let source_files = collect_source_files(path);
    if source_files.is_empty() {
        return Err(MemHopError::ConfigError(format!(
            "No source files found at: {}",
            path.display()
        )));
    }

    let graph_name = format!(
        "code:{}",
        base_path
            .file_name()
            .map(|n| n.to_string_lossy().to_string())
            .unwrap_or_else(|| "unknown".to_string())
    );
    let graph_id = hash_id(&graph_name);

    if engine.contains(graph_id) {
        return Err(MemHopError::ConfigError(format!(
            "L3 graph '{}' already exists, delete it first",
            graph_name
        )));
    }

    let slot = HypergraphSlot {
        id_hash: graph_id,
        name: graph_name.clone(),
        source: HypergraphSource::Path(path.display().to_string()),
        node_count: 0,
        edge_count: 0,
        created_at: now_ms,
        updated_at: now_ms,
        version: 1,
    };

    // Write to v2 engine
    crate::store::write_slot(engine, REC_L3_GRAPH_SLOT, graph_id, &slot)?;

    let mut created_ids = Vec::new();
    let mut module_to_hash: std::collections::HashMap<String, u64> =
        std::collections::HashMap::new();
    let mut file_data: Vec<(String, String, Vec<String>, u64)> = Vec::new(); // (module, content_preview, imports, hash)

    for (file_path, ext) in &source_files {
        let module_name = derive_module_name(file_path, &base_path);
        let node_hash = hash_id(&format!("{:016x}_{}", graph_id, module_name));

        let content = std::fs::read_to_string(file_path).unwrap_or_default();
        let content_preview: String = content.chars().take(200).collect();
        let content_preview = content_preview.replace('\n', " ").replace('\r', "");

        let imports = extract_imports(&content, ext);

        let keywords = extract_rust_symbols(&module_name, &content);

        let node_type = match ext.as_str() {
            "rs" => "rust_module",
            "py" => "python_module",
            "js" | "ts" => "js_module",
            "go" => "go_package",
            _ => "source_file",
        };

        let source_ref = file_path
            .strip_prefix(&base_path)
            .unwrap_or(file_path)
            .display()
            .to_string();

        let node = crate::layers::hypergraph::HypergraphNode {
            id_hash: node_hash,
            graph_id,
            title: module_name.clone(),
            node_type: node_type.to_string(),
            content: content_preview,
            keywords,
            source_ref: Some(source_ref),
            importance: 0.6,
            summary: None,
            valid_from: now_ms,
            valid_until: 0,
            created_at: now_ms,
            updated_at: now_ms,
            version: 1,
        };

        match crate::l3::store::add_node_with_engine(
            engine,
            node,
            tracker.as_deref_mut(),
            index_map.as_deref_mut(),
        ) {
            Ok(id) => {
                created_ids.push(id);
                module_to_hash.insert(module_name.clone(), node_hash);
                file_data.push((module_name, ext.clone(), imports, node_hash));
            }
            Err(e) => {
                tracing::warn!("Failed to add node for {}: {}", file_path.display(), e);
            }
        }
    }

    let mut edge_count = 0u32;
    let mut edge_ids = Vec::new();
    for (module, _ext, imports, from_hash) in &file_data {
        let mut connected_hashes = Vec::new();
        for imp in imports {
            // Try exact match first, then partial match
            let target = module_to_hash.get(imp).copied().or_else(|| {
                let imp_last = imp.rsplit("::").next().unwrap_or(imp);
                module_to_hash.iter().find_map(|(k, v)| {
                    let k_last = k.rsplit("::").next().unwrap_or(k);
                    if k_last == imp_last || k.contains(imp) {
                        Some(*v)
                    } else {
                        None
                    }
                })
            });

            if let Some(to_hash) = target {
                if to_hash != *from_hash && !connected_hashes.contains(&to_hash) {
                    connected_hashes.push(to_hash);
                }
            }
        }

        for to_hash in &connected_hashes {
            let edge_hash = hash_id(&format!(
                "{:016x}_dep_{:016x}_{:016x}",
                graph_id, from_hash, to_hash
            ));
            let edge = HypergraphEdge {
                id_hash: edge_hash,
                graph_id,
                kind: GraphEdgeKind::Dependency,
                node_ids: vec![*from_hash, *to_hash],
                weight: 0.8,
                label: Some("depends_on".to_string()),
                description: None,
                confidence: 0.8,
                valid_from: now_ms,
                valid_until: 0,
                created_at: now_ms,
            };

            match crate::l3::store::add_edge_with_engine(engine, edge, tracker.as_deref_mut()) {
                Ok(id) => {
                    edge_ids.push(id);
                    edge_count += 1;
                }
                Err(e) => {
                    tracing::warn!("Failed to add edge from {}: {}", module, e);
                }
            }
        }
    }

    let node_count = created_ids.len() as u32;
    let updated_slot = HypergraphSlot {
        id_hash: graph_id,
        name: graph_name.clone(),
        source: HypergraphSource::Path(path.display().to_string()),
        node_count,
        edge_count,
        created_at: now_ms,
        updated_at: now_ms,
        version: 1,
    };
    // Write updated slot to v2 engine
    crate::store::write_slot(engine, REC_L3_GRAPH_SLOT, graph_id, &updated_slot)?;

    // L2 topic for codebase discoverability via l3_id scoped search.
    let l2_title = format!("Codebase: {}", graph_name);
    let l2_id_hash = hash_id(&l2_title);

    let module_names: Vec<String> = file_data.iter().map(|(m, _, _, _)| m.clone()).collect();

    let l2_summary = format!(
        "Auto-imported code graph from {} ({} modules: {}, {} dependencies).",
        path.display(),
        node_count,
        module_names.join(", "),
        edge_count
    );

    let ctx = ContextSlot {
        id: l2_id_hash,
        fused_keywords: vec![l2_title.clone()],
        fused_summary: Some(l2_summary.clone()),
        children_ids: vec![],
        scene_id: 0,
        depth: 1,
        user_keywords: vec![],
        user_timestamp: now_ms,
        user_l4_refs: vec![],
        user_l3_refs: vec![],
        agent_keywords: vec![],
        agent_timestamp: now_ms,
        agent_l4_refs: vec![],
        agent_l3_refs: vec![graph_id],
        parent_id: None,
        centroid_page_ref: 0,
        created_at: now_ms,
        updated_at: now_ms,
        version: 4,
    };

    // Write to v2 engine
    crate::store::write_slot(engine, REC_L2_TOPIC, l2_id_hash, &ctx)?;

    // Add to sparse index for BM25/entity search. Include title, module names,
    // and imports so queries like "parser search main" can recall the codebase.
    let mut terms: Vec<String> = l2_title
        .split_whitespace()
        .chain(l2_title.split("::"))
        .map(|s| s.to_lowercase())
        .collect();
    terms.extend(module_names.iter().map(|s| s.to_lowercase()));
    terms.extend(
        file_data
            .iter()
            .flat_map(|(_, _, imports, _)| imports.iter().cloned())
            .map(|s| s.to_lowercase()),
    );
    terms.sort();
    terms.dedup();
    sparse_index.add_document(l2_id_hash, terms.clone(), terms.len() as u32);

    created_ids.push(format_hash(l2_id_hash));

    let all_errors: Vec<ImportError> = Vec::new();

    let node_count = created_ids.len();
    let ids = if created_ids.is_empty() {
        None
    } else {
        Some(created_ids.clone())
    };
    let id = created_ids.first().cloned();
    Ok(ImportResult {
        status: ImportStatus::Success,
        id,
        ids,
        created_ids,
        updated_ids: edge_ids,
        skipped_count: 0,
        errors: all_errors,
        knowledge_title: None,
        node_count,
    })
}
