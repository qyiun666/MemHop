//! Import memory implementation for MemHop
//!
//! Implements the import_memory() interface to batch import memories into L0/L2/L3 layers.
//! Also provides import_l3_from_path() for file-based L3 import with auto L2 creation.

use crate::file::free_list::allocate_from_free_list;
use crate::file::header::FileHeader;
use crate::index::btree::BTreeIndex;
use crate::index::sparse::SparseIndex;
use crate::query::common;
use crate::query::types::*;
use crate::slot::context::{ActivationState, ContextSlot};
use crate::slot::profile::ProfileSlot;
use crate::util::{hash_id, PAGE_SIZE};
use crate::MemHopError;
use memmap2::MmapMut;
use std::collections::HashMap;
use std::path::Path;

/// Helper function to calculate search terms and doc_len for L2 context
fn calculate_l2_sparse_index_data(
    ctx: &ContextSlot,
    mmap: &MmapMut,
    btree: &BTreeIndex,
) -> (Vec<String>, u32) {
    let mut terms = Vec::new();

    // Primary key: title
    terms.extend(ctx.title.split_whitespace().map(|s| s.to_lowercase()));

    // Secondary keys: summary
    if let Some(ref summary) = ctx.summary {
        terms.extend(summary.split_whitespace().map(|s| s.to_lowercase()));
    }

    // Secondary keys: L3 refs (if available)
    let mut l3_doc_len = 0;
    for &l3_id_hash in &ctx.l3_refs {
        if let Some(page_ref) = btree.search(l3_id_hash) {
            let l3_page_id = (page_ref >> 16) as u32;
            let l3_offset = (l3_page_id as usize) * PAGE_SIZE + 32;

            if l3_offset < mmap.len() {
                // Try to read L3 hypergraph node for additional search terms
                if let Ok(node) =
                    crate::slot::hypergraph::HypergraphSlot::deserialize(&mmap[l3_offset..])
                {
                    terms.extend(node.name.split_whitespace().map(|s| s.to_lowercase()));
                    l3_doc_len += node.name.len();
                }
            }
        }
    }

    let doc_len = ctx.title.len() + ctx.summary.as_ref().map_or(0, |s| s.len()) + l3_doc_len;

    (terms, doc_len as u32)
}

/// Import memory into specified layer
pub fn import_memory(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    request: ImportRequest,
) -> Result<ImportResult, MemHopError> {
    match request.target_layer {
        TargetLayer::Profile => import_l0_profile(mmap, header, btree, request.data, request.mode),
        TargetLayer::Topic => import_l2_topics(
            mmap,
            header,
            btree,
            sparse_index,
            request.data,
            request.mode,
            request.knowledge_title,
        ),
        TargetLayer::Knowledge => import_l3_knowledge(
            mmap,
            header,
            btree,
            sparse_index,
            request.data,
            request.mode,
        ),
    }
}

// ============================================================================
// L0 Profile Import
// ============================================================================

fn import_l0_profile(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
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

        match btree.search(profile_id_hash) {
            Some(page_ref) => {
                // Profile exists
                match mode {
                    ImportMode::Merge | ImportMode::Overwrite => {
                        let page_id = (page_ref >> 16) as u32;
                        let offset = (page_id as usize) * PAGE_SIZE + 32;

                        let mut profile = ProfileSlot::deserialize(&mmap[offset..])
                            .map_err(|e| MemHopError::Serialization(e.to_string()))?;

                        // Update fields
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

                        let data_bytes = profile
                            .serialize()
                            .map_err(|e| MemHopError::Serialization(e.to_string()))?;

                        if offset + data_bytes.len() > mmap.len() {
                            return Err(MemHopError::Serialization(format!(
                                "ProfileSlot data too large for page: {} > {}",
                                data_bytes.len(),
                                mmap.len() - offset
                            )));
                        }
                        mmap[offset..offset + data_bytes.len()].copy_from_slice(&data_bytes);

                        Ok(ImportResult {
                            status: ImportStatus::Success,
                            created_ids: vec![],
                            updated_ids: vec![format!("{:016x}", profile_id_hash)],
                            skipped_count: 0,
                            errors: vec![],
                        })
                    }
                    ImportMode::Skip => Ok(ImportResult {
                        status: ImportStatus::Success,
                        created_ids: vec![],
                        updated_ids: vec![],
                        skipped_count: 1,
                        errors: vec![],
                    }),
                }
            }
            None => {
                // Profile doesn't exist, create new
                let page_id = allocate_from_free_list(mmap, header)?;
                let offset = (page_id as usize) * PAGE_SIZE + 32;

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

                let data_bytes = profile
                    .serialize()
                    .map_err(|e| MemHopError::Serialization(e.to_string()))?;

                if offset + data_bytes.len() > mmap.len() {
                    return Err(MemHopError::Serialization(format!(
                        "ProfileSlot data too large for page: {} > {}",
                        data_bytes.len(),
                        mmap.len() - offset
                    )));
                }
                mmap[offset..offset + data_bytes.len()].copy_from_slice(&data_bytes);

                btree.insert(profile_id_hash, (page_id as u64) << 16);

                Ok(ImportResult {
                    status: ImportStatus::Success,
                    created_ids: vec![format!("{:016x}", profile_id_hash)],
                    updated_ids: vec![],
                    skipped_count: 0,
                    errors: vec![],
                })
            }
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

fn import_l2_topics(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
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

        // Find L3 domain if specified
        let l3_hash = if let Some(ref title) = knowledge_title {
            let hash = hash_id(title);
            if btree.search(hash).is_some() {
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

                match btree.search(id_hash) {
                    Some(page_ref) => {
                        // L2 context exists
                        match mode {
                            ImportMode::Merge | ImportMode::Overwrite => {
                                let page_id = (page_ref >> 16) as u32;
                                let offset = (page_id as usize) * PAGE_SIZE + 32;

                                let mut ctx = ContextSlot::deserialize(&mmap[offset..])
                                    .map_err(|e| MemHopError::Serialization(e.to_string()))?;

                                // Update fields
                                ctx.title = item.title.clone();
                                ctx.summary = item.summary.clone();

                                // Update L3 reference if provided
                                if let Some(l3_h) = l3_hash {
                                    if !ctx.l3_refs.contains(&l3_h) {
                                        ctx.l3_refs.push(l3_h);
                                    }
                                }

                                ctx.updated_at = now_ms;
                                ctx.version += 1;

                                // Update sparse index
                                sparse_index.remove_document(ctx.id_hash);
                                let (terms, doc_len) =
                                    calculate_l2_sparse_index_data(&ctx, mmap, btree);
                                sparse_index.add_document(ctx.id_hash, terms, doc_len);

                                let data_bytes = ctx
                                    .serialize()
                                    .map_err(|e| MemHopError::Serialization(e.to_string()))?;

                                if offset + data_bytes.len() > mmap.len() {
                                    return Err(MemHopError::Serialization(format!(
                                        "ContextSlot data too large for page: {} > {}",
                                        data_bytes.len(),
                                        mmap.len() - offset
                                    )));
                                }
                                mmap[offset..offset + data_bytes.len()]
                                    .copy_from_slice(&data_bytes);

                                updated_ids.push(format!("{:016x}", id_hash));
                            }
                            ImportMode::Skip => {
                                skipped_count += 1;
                            }
                        }
                    }
                    None => {
                        // Create new L2 context
                        let page_id = allocate_from_free_list(mmap, header)?;
                        let offset = (page_id as usize) * PAGE_SIZE + 32;

                        let mut l3_refs = Vec::new();
                        if let Some(l3_h) = l3_hash {
                            l3_refs.push(l3_h);
                        }

                        let ctx = ContextSlot {
                            id_hash,
                            title: item.title.clone(),
                            summary: item.summary.clone(),
                            depth: 1,
                            archive_refs: vec![],
                            l3_refs,
                            turn_count: 0,
                            parent_id: None,
                            created_at: now_ms,
                            updated_at: now_ms,
                            version: 1,
                            importance: 0.5,
                            activation_score: 0.0,
                            is_active: false,
                            activation_state: ActivationState::Dormant,
                            centroid_page_ref: 0,
                            dialogue_range: (now_ms, now_ms),
                            llm_params: crate::slot::context::LlmParams::default(),
                        };

                        let data_bytes = ctx
                            .serialize()
                            .map_err(|e| MemHopError::Serialization(e.to_string()))?;

                        if offset + data_bytes.len() > mmap.len() {
                            return Err(MemHopError::Serialization(format!(
                                "ContextSlot data too large for page: {} > {}",
                                data_bytes.len(),
                                mmap.len() - offset
                            )));
                        }
                        mmap[offset..offset + data_bytes.len()].copy_from_slice(&data_bytes);

                        // Add to sparse index
                        let (terms, doc_len) = calculate_l2_sparse_index_data(&ctx, mmap, btree);
                        sparse_index.add_document(id_hash, terms, doc_len);

                        btree.insert(id_hash, (page_id as u64) << 16);

                        created_ids.push(format!("{:016x}", id_hash));
                    }
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

        Ok(ImportResult {
            status,
            created_ids,
            updated_ids,
            skipped_count,
            errors,
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

fn import_l3_knowledge(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    _sparse_index: &mut SparseIndex,
    data: ImportData,
    mode: ImportMode,
) -> Result<ImportResult, MemHopError> {
    let items = match data {
        ImportData::Knowledge(items) => items,
        _ => {
            return Err(MemHopError::ConfigError(
                "Expected Knowledge import data".to_string(),
            ))
        }
    };

    let now_ms = crate::query::common::now_ms();
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
                if btree.search(title_hash).is_some() {
                    skipped_count += 1;
                    continue;
                }
            }
            ImportMode::Overwrite => {
                if btree.search(title_hash).is_some() {
                    let _ = crate::l3::store::delete_node(mmap, header, btree, &item.title);
                }
            }
            ImportMode::Merge => {
                if btree.search(title_hash).is_some() {
                    updated_ids.push(item.title.clone());
                    continue;
                }
            }
        }

        // Ensure HypergraphSlot exists for this domain before creating nodes
        let graph_id = *graph_cache.entry(item.domain.clone()).or_insert_with(|| {
            let gid = crate::util::hash_id(&item.domain);
            if btree.search(gid).is_none() {
                if let Err(e) =
                    create_hypergraph_slot(mmap, header, btree, gid, &item.domain, now_ms)
                {
                    errors.push(ImportError {
                        index: idx,
                        message: e.to_string(),
                    });
                    return gid; // Still return gid so we don't block node creation
                }
            }
            gid
        });

        let node = crate::slot::hypergraph::HypergraphNode {
            id_hash: title_hash,
            graph_id,
            title: item.title.clone(),
            node_type: item.knowledge_type.clone(),
            content: item.text.clone(),
            keywords: item.keywords.clone(),
            source_ref: item.source_ref.clone(),
            importance: 0.7,
            created_at: now_ms,
            updated_at: now_ms,
            version: 1,
        };

        match crate::l3::store::add_node(mmap, header, btree, node) {
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

    Ok(ImportResult {
        status,
        created_ids,
        updated_ids,
        skipped_count,
        errors,
    })
}

/// Create a HypergraphSlot (L3 graph container) for a new domain
fn create_hypergraph_slot(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    id_hash: u64,
    name: &str,
    now_ms: i64,
) -> Result<(), MemHopError> {
    use crate::file::free_list::allocate_from_free_list;
    use crate::file::page::PageHeader;
    use crate::slot::hypergraph::{HypergraphSlot, HypergraphSource};
    use crate::util::{PageType, PAGE_SIZE};

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

    let data_bytes = slot
        .serialize()
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;

    let page_id = allocate_from_free_list(mmap, header)?;
    let page_offset = (page_id as usize) * PAGE_SIZE;

    // Write page header
    let page_hdr = PageHeader::new(page_id, PageType::HypergraphSlot, 3, 0xFFFFFFFF);
    mmap[page_offset..page_offset + 32].copy_from_slice(&page_hdr.to_bytes());

    // Write slot data after header
    let data_offset = page_offset + 32;
    if data_offset + data_bytes.len() > mmap.len() {
        // Rollback: free the allocated page
        crate::file::free_list::free_page(mmap, header, page_id)?;
        return Err(MemHopError::Serialization(
            "HypergraphSlot too large for page".to_string(),
        ));
    }
    mmap[data_offset..data_offset + data_bytes.len()].copy_from_slice(&data_bytes);

    btree.insert(id_hash, (page_id as u64) << 16);
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
                // Rust: use crate::foo::bar or use super::foo
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
                // Also catch: mod foo;
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

/// Extract keywords from module name and file content
fn extract_keywords(module_name: &str, content: &str) -> Vec<String> {
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
pub fn build_l3_hypergraph_from_path(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    path: &Path,
) -> Result<ImportResult, MemHopError> {
    use crate::slot::hypergraph::{
        GraphEdgeKind, HypergraphEdge, HypergraphSlot, HypergraphSource,
    };

    let now_ms = common::now_ms();

    // Validate path
    if !path.exists() {
        return Err(MemHopError::Io(std::io::Error::new(
            std::io::ErrorKind::NotFound,
            format!("Path not found: {}", path.display()),
        )));
    }

    // Determine base path for module name derivation
    let base_path = if path.is_dir() {
        path.to_path_buf()
    } else {
        path.parent().unwrap_or(path).to_path_buf()
    };

    // Collect source files
    let source_files = collect_source_files(path);
    if source_files.is_empty() {
        return Err(MemHopError::ConfigError(format!(
            "No source files found at: {}",
            path.display()
        )));
    }

    // Create HypergraphSlot (graph container)
    let graph_name = format!(
        "code:{}",
        base_path
            .file_name()
            .map(|n| n.to_string_lossy().to_string())
            .unwrap_or_else(|| "unknown".to_string())
    );
    let graph_id = hash_id(&graph_name);

    // Check if graph already exists
    if btree.search(graph_id).is_some() {
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

    // Allocate page for HypergraphSlot
    let page_id = allocate_from_free_list(mmap, header)?;
    let page_offset = (page_id as usize) * PAGE_SIZE;
    let page_hdr = crate::file::page::PageHeader::new(
        page_id,
        crate::util::PageType::HypergraphSlot,
        3,
        0xFFFFFFFF,
    );
    mmap[page_offset..page_offset + 32].copy_from_slice(&page_hdr.to_bytes());

    let slot_data = slot
        .serialize()
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;
    let data_offset = page_offset + 32;
    if data_offset + slot_data.len() > mmap.len() {
        crate::file::free_list::free_page(mmap, header, page_id)?;
        return Err(MemHopError::Serialization(
            "HypergraphSlot too large for page".to_string(),
        ));
    }
    mmap[data_offset..data_offset + slot_data.len()].copy_from_slice(&slot_data);
    btree.insert(graph_id, (page_id as u64) << 16);

    // Create nodes for each source file
    let mut created_ids = Vec::new();
    let mut module_to_hash: std::collections::HashMap<String, u64> =
        std::collections::HashMap::new();
    let mut file_data: Vec<(String, String, Vec<String>, u64)> = Vec::new(); // (module, content_preview, imports, hash)

    for (file_path, ext) in &source_files {
        let module_name = derive_module_name(file_path, &base_path);
        let node_hash = hash_id(&format!("{:016x}_{}", graph_id, module_name));

        // Read file content (first 200 chars as preview)
        let content = std::fs::read_to_string(file_path).unwrap_or_default();
        let content_preview: String = content.chars().take(200).collect();
        let content_preview = content_preview.replace('\n', " ").replace('\r', "");

        // Extract imports
        let imports = extract_imports(&content, ext);

        // Extract keywords
        let keywords = extract_keywords(&module_name, &content);

        // Determine node type from extension
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

        let node = crate::slot::hypergraph::HypergraphNode {
            id_hash: node_hash,
            graph_id,
            title: module_name.clone(),
            node_type: node_type.to_string(),
            content: content_preview,
            keywords,
            source_ref: Some(source_ref),
            importance: 0.6,
            created_at: now_ms,
            updated_at: now_ms,
            version: 1,
        };

        match crate::l3::store::add_node(mmap, header, btree, node) {
            Ok(id) => {
                created_ids.push(id);
                module_to_hash.insert(module_name.clone(), node_hash);
                file_data.push((module_name, ext.clone(), imports, node_hash));
            }
            Err(e) => {
                eprintln!(
                    "Warning: Failed to add node for {}: {}",
                    file_path.display(),
                    e
                );
            }
        }
    }

    // Create Dependency edges based on import statements
    let mut edge_count = 0u32;
    let mut edge_ids = Vec::new();
    for (module, _ext, imports, from_hash) in &file_data {
        let mut connected_hashes = Vec::new();
        for imp in imports {
            // Try to find the target module in our graph
            // Try exact match first, then partial match
            let target = module_to_hash.get(imp).copied().or_else(|| {
                // Try matching by last segment
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

        // Create one edge per dependency pair (binary hyperedge)
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
                created_at: now_ms,
            };

            match crate::l3::store::add_edge(mmap, header, btree, edge) {
                Ok(id) => {
                    edge_ids.push(id);
                    edge_count += 1;
                }
                Err(e) => {
                    eprintln!("Warning: Failed to add edge from {}: {}", module, e);
                }
            }
        }
    }

    // Update HypergraphSlot with final counts
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
    let slot_data = updated_slot
        .serialize()
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;
    let data_offset = (page_id as usize) * PAGE_SIZE + 32;
    if data_offset + slot_data.len() <= mmap.len() {
        mmap[data_offset..data_offset + slot_data.len()].copy_from_slice(&slot_data);
    }

    // Create L2 topic linked to this L3 graph so the codebase is discoverable
    // through normal search channels (BM25/entity) when scoped by l3_id.
    let l2_title = format!("Codebase: {}", graph_name);
    let l2_id_hash = hash_id(&l2_title);

    // Collect module names from the imported files for indexing.
    let module_names: Vec<String> = file_data.iter().map(|(m, _, _, _)| m.clone()).collect();

    let l2_summary = format!(
        "Auto-imported code graph from {} ({} modules: {}, {} dependencies).",
        path.display(),
        node_count,
        module_names.join(", "),
        edge_count
    );

    let ctx = ContextSlot {
        id_hash: l2_id_hash,
        title: l2_title.clone(),
        summary: Some(l2_summary.clone()),
        depth: 1,
        archive_refs: vec![],
        l3_refs: vec![graph_id],
        turn_count: 0,
        parent_id: None,
        created_at: now_ms,
        updated_at: now_ms,
        version: 1,
        importance: 0.7,
        activation_score: 0.0,
        is_active: false,
        activation_state: ActivationState::Dormant,
        centroid_page_ref: 0,
        dialogue_range: (now_ms, now_ms),
        llm_params: crate::slot::context::LlmParams::default(),
    };

    let ctx_data = ctx
        .serialize()
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;
    let l2_page_id = allocate_from_free_list(mmap, header)?;
    let l2_offset = (l2_page_id as usize) * PAGE_SIZE + 32;

    if l2_offset + ctx_data.len() > mmap.len() {
        crate::file::free_list::free_page(mmap, header, l2_page_id)?;
        return Err(MemHopError::Serialization(
            "ContextSlot too large for page".to_string(),
        ));
    }
    mmap[l2_offset..l2_offset + ctx_data.len()].copy_from_slice(&ctx_data);

    // Write page header for the L2 context
    let l2_hdr = crate::file::page::PageHeader::new(
        l2_page_id,
        crate::util::PageType::Context,
        2,
        0xFFFFFFFF,
    );
    let l2_page_offset = (l2_page_id as usize) * PAGE_SIZE;
    mmap[l2_page_offset..l2_page_offset + 32].copy_from_slice(&l2_hdr.to_bytes());

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

    btree.insert(l2_id_hash, (l2_page_id as u64) << 16);
    created_ids.push(format!("{:016x}", l2_id_hash));

    let all_errors: Vec<ImportError> = Vec::new();

    Ok(ImportResult {
        status: ImportStatus::Success,
        created_ids,
        updated_ids: edge_ids,
        skipped_count: 0,
        errors: all_errors,
    })
}
