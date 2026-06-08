//! shelf — knowledge base mounting for L3 Domain Graph.
//! Scans directories, chunks files, feeds into L3 via batch_store.
//! Each chunk becomes a StoreItem { source: "shelf", domain_id, ... }.

use crate::brain::Brain;
use crate::error::{MemHopError, Result};
use crate::types::*;

pub mod chunker;
pub mod scanner;

/// Mount a directory as a knowledge domain in L3.
/// Returns metadata about the mounted knowledge base.
pub fn mount(
    brain: &mut Brain,
    dir_path: &str,
    domain: ShelfDomain,
    domain_name: &str,
) -> Result<ShelfMeta> {
    // 1. Scan files
    let files = scanner::scan(dir_path, &domain).map_err(MemHopError::InvalidArgument)?;
    if files.is_empty() {
        return Err(MemHopError::InvalidArgument(format!(
            "no files found in '{}' for domain {:?}",
            dir_path, domain
        )));
    }

    // 2. Chunk each file
    let mut chunks: Vec<String> = Vec::new();
    for file in &files {
        let file_chunks = chunker::chunk(&file.content, &domain, 1024);
        chunks.extend(file_chunks);
    }

    if chunks.is_empty() {
        return Err(MemHopError::InvalidArgument("no chunks generated".into()));
    }

    // 3. Store each chunk via batch_store (100 per batch)
    let now = chrono::Utc::now().timestamp_millis();
    let domain_id = format!("shelf_{}", now);
    let mut stored = 0usize;
    let mut all_engram_ids: std::collections::HashMap<String, String> =
        std::collections::HashMap::new();
    let mut all_l3_engram_ids: std::collections::HashMap<String, String> =
        std::collections::HashMap::new();

    for chunk_batch in chunks.chunks(100) {
        let items: Vec<StoreItem> = chunk_batch
            .iter()
            .map(|text| StoreItem {
                text: text.clone(),
                source: "shelf".to_string(),
                domain_id: Some(domain_id.clone()),
                turn_id: None,
                session_id: None,
                topic_label: Some(domain_name.to_string()),
                llm_keywords: None,
                llm_compressed_summary: None,
                valence: None,
                arousal: None,
                chain_parent_id: None,
                chain_label: None,
                importance: None,
            })
            .collect();

        let batch = StoreBatch { items };
        let report = brain.batch_store(batch)?;
        // v0.17.3: 收集 engram_ids 和 l3_engram_ids 映射
        for (idx, node_id) in report.engram_ids {
            let global_idx = format!("{}", stored + idx.parse::<usize>().unwrap_or(0));
            all_engram_ids.insert(global_idx, node_id);
        }
        for (idx, node_id) in report.l3_engram_ids {
            let global_idx = format!("{}", stored + idx.parse::<usize>().unwrap_or(0));
            all_l3_engram_ids.insert(global_idx, node_id);
        }
        stored += chunk_batch.len();
    }

    // 4. Save ShelfMeta to L3 domain_meta
    let meta = ShelfMeta {
        id: domain_id.clone(),
        path: dir_path.to_string(),
        doc_type: domain,
        chunk_count: stored,
        mounted_at: now,
        engram_ids: all_engram_ids,
        l3_engram_ids: all_l3_engram_ids,
    };
    let meta_key = format!("shelf_meta:{}", domain_id);
    let meta_bytes = bincode::serialize(&meta).map_err(|e| MemHopError::Storage(e.to_string()))?;
    {
        brain.ensure_l3_env()?;
        let l3_env = brain.l3_env.as_ref().unwrap();
        let env = l3_env.env.clone();
        let mut wtxn = env
            .write_txn()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        l3_env
            .domain_meta
            .put(&mut wtxn, &meta_key, &meta_bytes)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        wtxn.commit()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
    }

    Ok(meta)
}

/// Unmount a knowledge base: remove its L3 domain nodes and metadata.
pub fn unmount(brain: &mut Brain, domain_id: &str) -> Result<()> {
    brain.ensure_l3_env()?;
    let l3_env = brain.l3_env.as_ref().unwrap();
    let meta_key = format!("shelf_meta:{}", domain_id);
    let env = l3_env.env.clone();
    let mut wtxn = env
        .write_txn()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;

    // Remove domain metadata
    l3_env
        .domain_meta
        .delete(&mut wtxn, &meta_key)
        .map_err(|e| MemHopError::Storage(e.to_string()))?;

    // Remove all domain nodes with this domain_id prefix
    let prefix = format!("node:{}:", domain_id);
    let mut to_delete: Vec<String> = Vec::new();
    if let Ok(iter) = l3_env.domain_nodes.iter(&wtxn) {
        for item in iter {
            if let Ok((key, _)) = item
                && key.starts_with(&prefix)
            {
                to_delete.push(key.to_string());
            }
        }
    }
    for key in &to_delete {
        l3_env
            .domain_nodes
            .delete(&mut wtxn, key)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
    }

    wtxn.commit()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    Ok(())
}

/// List all mounted knowledge bases.
pub fn list(brain: &mut Brain) -> Result<Vec<ShelfMeta>> {
    brain.ensure_l3_env()?;
    let l3_env = brain.l3_env.as_ref().unwrap();
    let txn = l3_env
        .env
        .read_txn()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    let mut results = Vec::new();

    if let Ok(iter) = l3_env.domain_meta.iter(&txn) {
        for item in iter {
            if let Ok((key, bytes)) = item
                && key.starts_with("shelf_meta:")
                && let Ok(meta) = bincode::deserialize::<ShelfMeta>(bytes)
            {
                results.push(meta);
            }
        }
    }

    Ok(results)
}
