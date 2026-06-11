//! shelf — knowledge base mounting for L3 Domain Graph.
//! Scans directories, chunks files, feeds into L3 via batch_store.
//! Each chunk becomes a StoreItem { source: "shelf", domain_id, ... }.

use crate::batch_store;
use crate::brain::Brain;
use crate::engram::Hyperedge;
use crate::error::{MemHopError, Result};
use crate::storage::L3_DOMAIN_HYPEREDGES;
use crate::storage::store::RedbStore;
use crate::types::*;
use redb::ReadableTable;
use std::collections::HashMap;

pub mod chunker;
pub mod scanner;
pub mod summarizer;

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

    // 2. For each file: summarize → structural + chunk → detail
    // Track structural + detail items in order for hyperedge building
    let now = chrono::Utc::now().timestamp_millis();
    let domain_id = format!("shelf_{}", now);
    let mut all_items: Vec<StoreItem> = Vec::new();
    // file_path → (structural_node_count, detail_node_count) for hyperedge mapping
    let mut file_node_counts: Vec<(String, usize, usize)> = Vec::new();

    for file in &files {
        // 2a. Summarize to extract structural nodes
        let summary = summarizer::summarize(&file.content, &domain);
        let structural_count = summary.structural_nodes.len();

        // 2b. Create structural StoreItems (is_structural=true, skeletal_text)
        for sc in &summary.structural_nodes {
            let mut source_ref = sc.source_ref.clone();
            source_ref.location = file.path.clone();
            all_items.push(StoreItem {
                text: String::new(),  // structural nodes don't need full text
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
                is_structural: Some(true),
                source_ref: Some(source_ref),
                skeletal_text: Some(sc.text.clone()),
            });
        }

        // 2c. Chunk to extract detail nodes
        let file_chunk_texts = chunker::chunk(&file.content, &domain, 1024);
        let detail_count = file_chunk_texts.len();

        // 2d. Create detail StoreItems (is_structural=false, source_ref with line_range)
        let mut char_offset: usize = 0;
        let total_lines = file.content.len();
        for chunk_text in &file_chunk_texts {
            // Estimate line range for this chunk
            let chunk_start = file.content[char_offset..]
                .find(chunk_text.as_str())
                .unwrap_or(0);
            let start_line = file.content[..(char_offset + chunk_start).min(total_lines)]
                .matches('\n').count() + 1;
            let end_line = start_line + chunk_text.matches('\n').count();
            char_offset += chunk_start + chunk_text.len();

            all_items.push(StoreItem {
                text: chunk_text.clone(),
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
                is_structural: Some(false),
                source_ref: Some(SourceRef {
                    kind: SourceKind::File,
                    location: file.path.clone(),
                    line_range: Some((start_line, end_line)),
                    selector: None,
                    content_hash: None,
                }),
                skeletal_text: None,
            });
        }

        file_node_counts.push((file.path.clone(), structural_count, detail_count));
    }

    if all_items.is_empty() {
        return Err(MemHopError::InvalidArgument("no items generated".into()));
    }

    // 3. Store all items via batch_store (100 per batch)
    let mut stored = 0usize;
    let mut all_engram_ids: HashMap<String, String> = HashMap::new();
    let mut all_l3_engram_ids: HashMap<String, String> = HashMap::new();

    for item_batch in all_items.chunks(100) {
        let batch = StoreBatch { items: item_batch.to_vec() };
        let report = brain.batch_store(batch)?;
        for (idx, node_id) in report.engram_ids {
            let global_idx = format!("{}", stored + idx.parse::<usize>().unwrap_or(0));
            all_engram_ids.insert(global_idx, node_id);
        }
        for (idx, node_id) in report.l3_engram_ids {
            let global_idx = format!("{}", stored + idx.parse::<usize>().unwrap_or(0));
            all_l3_engram_ids.insert(global_idx, node_id);
        }
        stored += item_batch.len();
    }

    // 4. Build file_path → node_ids mapping for hyperedge construction
    let mut file_to_node_ids: HashMap<String, Vec<String>> = HashMap::new();
    let mut global_idx = 0usize;
    for (file_path, str_count, det_count) in &file_node_counts {
        let mut node_ids = Vec::new();
        for _ in 0..(*str_count + *det_count) {
            let idx_key = format!("{}", global_idx);
            if let Some(node_id) = all_l3_engram_ids.get(&idx_key) {
                node_ids.push(node_id.clone());
            }
            global_idx += 1;
        }
        if !node_ids.is_empty() {
            file_to_node_ids.insert(file_path.clone(), node_ids);
        }
    }

    // 5. Build L3 内部超边（将同文件 chunk 连接为超图）
    let hyperedge_count = if brain.l3.is_some() {
        let strategy = domain.hyperedge_strategy();
        match build_l3_domain_hyperedges(brain, &domain_id, &file_to_node_ids, strategy) {
            Ok(n) => n,
            Err(e) => {
                eprintln!("[shelf] WARNING: failed to build L3 hyperedges: {}", e);
                0
            }
        }
    } else {
        0
    };
    if hyperedge_count > 0 {
        eprintln!("[shelf] built {} L3 hyperedges for domain {}", hyperedge_count, domain_id);
    }

    // 6. Save ShelfMeta to L3 domain_meta via redb
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
    let meta_bytes = bincode::serialize(&meta)?;
    {
        let store = brain.redb_store.as_ref()
            .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;
        let mut wtxn = store.begin_write()
            .map_err(|e| MemHopError::Storage(format!("begin_write: {}", e)))?;
        store.write_raw(&mut wtxn, crate::storage::L3_DOMAIN_META, &meta_key, &meta_bytes)?;
        wtxn.commit()
            .map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;
    }

    Ok(meta)
}

/// 为 L3 domain 构建内部超边
fn build_l3_domain_hyperedges(
    brain: &mut Brain,
    domain_id: &str,
    file_to_nodes: &HashMap<String, Vec<String>>,
    strategy: L3HyperedgeStrategy,
) -> Result<u32> {
    brain.ensure_l3()?;
    let store = brain.redb_store.as_ref()
        .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;
    let mut wtxn = store.begin_write()
        .map_err(|e| MemHopError::Storage(format!("begin_write: {}", e)))?;
    let mut count = 0u32;

    match strategy {
        L3HyperedgeStrategy::Sequential => {
            build_sequential_hyperedges(store, &mut wtxn, domain_id, file_to_nodes, &mut count)?;
        }
        L3HyperedgeStrategy::Structural => {
            build_sequential_hyperedges(store, &mut wtxn, domain_id, file_to_nodes, &mut count)?;
            // TODO: 当 scanner 提供 import 分析后，添加跨文件关联超边
        }
        L3HyperedgeStrategy::Citation => {
            build_citation_hyperedges(store, &mut wtxn, domain_id, file_to_nodes, &mut count)?;
        }
    }

    wtxn.commit()
        .map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;

    Ok(count)
}

/// 同文件 Association 超边 + 相邻节点 Evolution 链
fn build_sequential_hyperedges(
    store: &RedbStore,
    wtxn: &mut redb::WriteTransaction,
    domain_id: &str,
    file_to_nodes: &HashMap<String, Vec<String>>,
    count: &mut u32,
) -> Result<()> {
    for node_ids in file_to_nodes.values() {
        if node_ids.len() < 2 {
            continue;
        }

        // 同文件 Association 超边
        let assoc_id = batch_store::unique_id("l3hyp");
        let assoc_he = Hyperedge {
            id: format!("{}:{}", domain_id, assoc_id),
            node_ids: node_ids.clone(),
            kind: crate::types::HyperedgeKind::Association,
            weight: 0.5,
            created_at: chrono::Utc::now().timestamp_millis(),
            updated_at: chrono::Utc::now().timestamp_millis(),
            version: 1,
            history: Vec::new(),
            meta: HashMap::new(),
            chain_prev: None,
            chain_next: None,
            chain_label: None,
        };
        let hyp_key = format!("hyp:{}:{}", domain_id, assoc_id);
        let hyp_bytes = bincode::serialize(&assoc_he)
            .map_err(|e| MemHopError::Internal(format!("serialize hyperedge: {}", e)))?;
        store.write_raw(wtxn, L3_DOMAIN_HYPEREDGES, &hyp_key, &hyp_bytes)?;
        *count += 1;

        // 相邻节点 Evolution 链
        for i in 0..node_ids.len() - 1 {
            let evol_id = batch_store::unique_id("l3hyp");
            let evol_he = Hyperedge {
                id: format!("{}:{}", domain_id, evol_id),
                node_ids: vec![node_ids[i].clone(), node_ids[i+1].clone()],
                kind: crate::types::HyperedgeKind::Evolution,
                weight: 0.3,
                created_at: chrono::Utc::now().timestamp_millis(),
                updated_at: chrono::Utc::now().timestamp_millis(),
                version: 1,
                history: Vec::new(),
                meta: HashMap::new(),
                chain_prev: None,
                chain_next: None,
                chain_label: None,
            };
            let hyp_key = format!("hyp:{}:{}", domain_id, evol_id);
            let hyp_bytes = bincode::serialize(&evol_he)
                .map_err(|e| MemHopError::Internal(format!("serialize hyperedge: {}", e)))?;
            store.write_raw(wtxn, L3_DOMAIN_HYPEREDGES, &hyp_key, &hyp_bytes)?;
            *count += 1;
        }
    }
    Ok(())
}

/// 仅 Association 团超边（无 Evolution 链），用于 Paper 领域
fn build_citation_hyperedges(
    store: &RedbStore,
    wtxn: &mut redb::WriteTransaction,
    domain_id: &str,
    file_to_nodes: &HashMap<String, Vec<String>>,
    count: &mut u32,
) -> Result<()> {
    for node_ids in file_to_nodes.values() {
        if node_ids.len() < 2 {
            continue;
        }

        // 仅创建 Association 超边（单条，覆盖整个文件的所有 node）
        let assoc_id = batch_store::unique_id("l3hyp");
        let assoc_he = Hyperedge {
            id: format!("{}:{}", domain_id, assoc_id),
            node_ids: node_ids.clone(),
            kind: crate::types::HyperedgeKind::Association,
            weight: 0.5,
            created_at: chrono::Utc::now().timestamp_millis(),
            updated_at: chrono::Utc::now().timestamp_millis(),
            version: 1,
            history: Vec::new(),
            meta: HashMap::new(),
            chain_prev: None,
            chain_next: None,
            chain_label: None,
        };
        let hyp_key = format!("hyp:{}:{}", domain_id, assoc_id);
        let hyp_bytes = bincode::serialize(&assoc_he)
            .map_err(|e| MemHopError::Internal(format!("serialize hyperedge: {}", e)))?;
        store.write_raw(wtxn, L3_DOMAIN_HYPEREDGES, &hyp_key, &hyp_bytes)?;
        *count += 1;
    }
    Ok(())
}

/// Unmount a knowledge base: remove its L3 domain nodes and metadata via redb.
pub fn unmount(brain: &mut Brain, domain_id: &str) -> Result<()> {
    let store = brain.redb_store.as_ref()
        .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;
    let meta_key = format!("shelf_meta:{}", domain_id);

    // 1. 清理 L2→L3 正向链接
    let topics = store.l2_list_topics()?;
    let mut updated_topic_ids: Vec<String> = Vec::new();
    for mut topic in topics {
        if topic.linked_domain_ids.contains(&domain_id.to_string()) {
            topic.linked_domain_ids.retain(|d| d != domain_id);
            topic.domain_weights.remove(domain_id);
            store.l2_store_topic(&topic)?;
            updated_topic_ids.push(topic.id.clone());
        }
    }

    // 2. 清理 L3→L2 反向索引（内存）
    if let Some(ref mut l3) = brain.l3 {
        l3.domain_to_topics.remove(domain_id);
    }

    // 3. 删除所有 domain 超边
    {
        let rtxn = store.begin_read()?;
        let table = rtxn.open_table(L3_DOMAIN_HYPEREDGES)
            .map_err(|e| MemHopError::Storage(format!("open L3_DOMAIN_HYPEREDGES: {}", e)))?;
        let prefix = format!("hyp:{}:", domain_id);
        let mut to_delete: Vec<String> = Vec::new();
        for result in table.iter()
            .map_err(|e| MemHopError::Storage(format!("iter L3_DOMAIN_HYPEREDGES: {}", e)))?
        {
            if let Ok((key, _)) = result
                && key.value().starts_with(&prefix)
            {
                to_delete.push(key.value().to_string());
            }
        }
        drop(table);
        drop(rtxn);

        if !to_delete.is_empty() {
            let mut wtxn = store.begin_write()?;
            for key in &to_delete {
                store.delete(&mut wtxn, L3_DOMAIN_HYPEREDGES, key)?;
            }
            wtxn.commit()
                .map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;
        }
    }

    // 4. 删除 metadata + domain nodes（已有逻辑）
    let mut wtxn = store.begin_write()?;

    // Remove domain metadata
    store.delete(&mut wtxn, crate::storage::L3_DOMAIN_META, &meta_key)?;

    // 还需清理 DomainMeta（key = meta:{domain_id}）
    let domain_meta_key = format!("meta:{}", domain_id);
    store.delete(&mut wtxn, crate::storage::L3_DOMAIN_META, &domain_meta_key)?;

    // Remove all domain nodes with this domain_id prefix
    let prefix = format!("node:{}:", domain_id);
    let rtxn = store.begin_read()?;
    let table = rtxn.open_table(crate::storage::L3_DOMAIN_NODES)
        .map_err(|e| MemHopError::Storage(format!("open L3_DOMAIN_NODES: {}", e)))?;
    let mut to_delete: Vec<String> = Vec::new();
    for result in table.iter()
        .map_err(|e| MemHopError::Storage(format!("iter L3_DOMAIN_NODES: {}", e)))?
    {
        if let Ok((key, _)) = result
            && key.value().starts_with(&prefix)
        {
            to_delete.push(key.value().to_string());
        }
    }
    drop(table);
    drop(rtxn);

    for key in &to_delete {
        store.delete(&mut wtxn, crate::storage::L3_DOMAIN_NODES, key)?;
    }

    wtxn.commit()
        .map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;

    eprintln!("[shelf] unmounted domain {}, removed {} L2 links, {} nodes, {} hyperedges",
        domain_id, updated_topic_ids.len(), to_delete.len(), 0);

    Ok(())
}

/// List all mounted knowledge bases via redb.
pub fn list(brain: &mut Brain) -> Result<Vec<ShelfMeta>> {
    let store = brain.redb_store.as_ref()
        .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;
    let rtxn = store.begin_read()?;
    let table = rtxn.open_table(crate::storage::L3_DOMAIN_META)
        .map_err(|e| MemHopError::Storage(format!("open L3_DOMAIN_META: {}", e)))?;
    let mut results = Vec::new();

    for result in table.iter()
        .map_err(|e| MemHopError::Storage(format!("iter L3_DOMAIN_META: {}", e)))?
    {
        if let Ok((key, bytes)) = result
            && key.value().starts_with("shelf_meta:")
            && let Ok(meta) = bincode::deserialize::<ShelfMeta>(bytes.value())
        {
            results.push(meta);
        }
    }

    Ok(results)
}
