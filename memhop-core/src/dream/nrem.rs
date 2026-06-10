//! dream/nrem — NREM stages: hyperedge weight time decay + pruning + temporal binding.
//! Lowers weights of old hyperedges; removes those below threshold.
//! Temporal binding creates new hyperedges for nodes in the same time window.

use crate::brain::Brain;
use crate::engram::{Hyperedge, KnowledgeNode};
use crate::error::{MemHopError, Result};
use crate::storage::L1_HYPEREDGES;
use crate::types::ConsolidateReport;
use redb::ReadableTable;
use std::collections::HashMap;

/// Decay hyperedge weights based on time since last update.
/// Uses personalized lambda per node instead of global hardcoded lambda.
/// new_weight = weight * exp(-personalized_lambda * hours_since_update)
/// Removes hyperedges with weight < 0.05.
pub fn nrem_decay(brain: &mut Brain, report: &mut ConsolidateReport) -> Result<()> {
    let now = chrono::Utc::now().timestamp_millis();
    let store = brain.redb_store.as_ref()
        .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;

    // Phase 1: Collect all hyperedges and count edges per node
    let rtxn = store.begin_read()?;
    let table = rtxn.open_table(L1_HYPEREDGES)
        .map_err(|e| MemHopError::Storage(format!("open L1_HYPEREDGES: {}", e)))?;
    let mut hyperedges: Vec<Hyperedge> = Vec::new();
    let mut node_edge_count: HashMap<String, usize> = HashMap::new();

    for result in table.iter()
        .map_err(|e| MemHopError::Storage(format!("iter L1_HYPEREDGES: {}", e)))?
    {
        let (_key, bytes) = result
            .map_err(|e| MemHopError::Storage(format!("iter entry: {}", e)))?;
        if let Ok(he) = bincode::deserialize::<Hyperedge>(bytes.value()) {
            for node_id in &he.node_ids {
                *node_edge_count.entry(node_id.clone()).or_default() += 1;
            }
            hyperedges.push(he);
        }
    }
    drop(table);
    drop(rtxn);

    if hyperedges.is_empty() {
        return Ok(());
    }

    // Phase 2: Read all L1 nodes needed for personalized decay lambda
    let rtxn = store.begin_read()?;
    let node_table = rtxn.open_table(crate::storage::L1_NODES)
        .map_err(|e| MemHopError::Storage(format!("open L1_NODES: {}", e)))?;
    let mut nodes: HashMap<String, KnowledgeNode> = HashMap::new();
    for result in node_table.iter()
        .map_err(|e| MemHopError::Storage(format!("iter L1_NODES: {}", e)))?
    {
        let (_key, bytes) = result
            .map_err(|e| MemHopError::Storage(format!("iter node entry: {}", e)))?;
        if let Ok(node) = bincode::deserialize::<KnowledgeNode>(bytes.value()) {
            nodes.insert(node.id.clone(), node);
        }
    }
    drop(node_table);
    drop(rtxn);

    // Phase 3: Decay computation with personalized lambda
    let mut to_update: Vec<(String, Hyperedge)> = Vec::new();
    let mut to_delete: Vec<String> = Vec::new();

    for mut he in hyperedges {
        let hours_since_update = (now - he.updated_at) as f32 / 3600000.0;
        if hours_since_update <= 0.0 {
            continue;
        }

        // Compute average personalized lambda for connected nodes
        let mut lambdas: Vec<f32> = Vec::new();
        for nid in &he.node_ids {
            if let Some(node) = nodes.get(nid) {
                let count = node_edge_count.get(nid).copied().unwrap_or(0);
                lambdas.push(crate::activation::personal_decay_lambda(node, count));
            }
        }
        let avg_lambda = if lambdas.is_empty() {
            0.01
        } else {
            lambdas.iter().sum::<f32>() / lambdas.len() as f32
        };

        let new_weight = he.weight * (-avg_lambda * hours_since_update).exp();

        if new_weight < 0.05 {
            to_delete.push(he.id.clone());
            report.vitality_decayed += 1;
        } else {
            he.weight = new_weight;
            he.updated_at = now;
            to_update.push((he.id.clone(), he));
            report.vitality_decayed += 1;
        }
    }

    if to_update.is_empty() && to_delete.is_empty() {
        return Ok(());
    }

    // Apply changes in a single write transaction
    let wtxn = store.begin_write()?;
    let mut hyperedges_table = wtxn.open_table(L1_HYPEREDGES)
        .map_err(|e| MemHopError::Storage(format!("open L1_HYPEREDGES: {}", e)))?;

    for (id, he) in &to_update {
        let bytes = bincode::serialize(he)?;
        hyperedges_table.insert(id.as_str(), bytes.as_slice())
            .map_err(|e| MemHopError::Storage(format!("insert hyperedge: {}", e)))?;
    }

    for id in &to_delete {
        hyperedges_table.remove(id.as_str())
            .map_err(|e| MemHopError::Storage(format!("remove hyperedge: {}", e)))?;
    }

    drop(hyperedges_table);

    wtxn.commit()
        .map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;

    Ok(())
}

/// 时间绑定 — 同一时间窗口内的节点自动创建 Association 超边
pub fn temporal_binding(brain: &mut Brain, window_hours: f32) -> Result<u32> {
    let store = match brain.redb_store.as_ref() {
        Some(s) => s,
        None => return Ok(0),
    };

    // 1. 读取所有 L1 节点，按 created_at 排序
    let rtxn = store.begin_read()?;
    let table = rtxn.open_table(crate::storage::L1_NODES)
        .map_err(|e| MemHopError::Storage(format!("open L1_NODES: {}", e)))?;

    let mut nodes: Vec<KnowledgeNode> = Vec::new();
    for result in table.iter()
        .map_err(|e| MemHopError::Storage(format!("iter L1_NODES: {}", e)))?
    {
        let (_key, bytes) = result
            .map_err(|e| MemHopError::Storage(format!("iter entry: {}", e)))?;
        if let Ok(node) = bincode::deserialize::<KnowledgeNode>(bytes.value()) {
            nodes.push(node);
        }
    }
    drop(table);
    drop(rtxn);

    if nodes.len() < 2 {
        return Ok(0);
    }

    // 按 created_at 排序
    nodes.sort_by_key(|n| n.created_at);

    // 2. 滑动窗口扫描
    let window_ms = (window_hours * 3600.0 * 1000.0) as i64;
    let mut hyperedges_created = 0u32;
    let wtxn = store.begin_write()?;
    let mut hyp_table = wtxn.open_table(L1_HYPEREDGES)
        .map_err(|e| MemHopError::Storage(format!("open L1_HYPEREDGES: {}", e)))?;

    for i in 0..nodes.len() {
        for j in i+1..nodes.len() {
            let time_gap = nodes[j].created_at - nodes[i].created_at;
            if time_gap > window_ms {
                break;
            }
            if time_gap <= 0 {
                continue;
            }

            let weight = 1.0 - (time_gap as f32 / window_ms as f32);
            if weight < 0.1 {
                continue;
            }

            let he_id = crate::batch_store::unique_id("tmp");
            let now = chrono::Utc::now().timestamp_millis();
            let he = Hyperedge {
                id: he_id.clone(),
                node_ids: vec![nodes[i].id.clone(), nodes[j].id.clone()],
                kind: crate::types::HyperedgeKind::Association,
                weight,
                created_at: now,
                updated_at: now,
                version: 1,
                history: Vec::new(),
                meta: HashMap::new(),
                chain_prev: None,
                chain_next: None,
                chain_label: None,
            };
            let bytes = bincode::serialize(&he)?;
            hyp_table.insert(he_id.as_str(), bytes.as_slice())
                .map_err(|e| MemHopError::Storage(format!("insert hyperedge: {}", e)))?;
            hyperedges_created += 1;
        }
    }

    drop(hyp_table);
    wtxn.commit()
        .map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;

    eprintln!("[dream] temporal binding: {} hyperedges created", hyperedges_created);
    Ok(hyperedges_created)
}
