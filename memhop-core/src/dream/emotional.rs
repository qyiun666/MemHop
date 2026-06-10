//! dream/emotional — Emotional Entanglement (Dream Stage 5.5).
//! Nodes sharing high emotional intensity auto-create Association hyperedges.

use crate::brain::Brain;
use crate::engram::{Hyperedge, KnowledgeNode};
use crate::error::{MemHopError, Result};
use crate::types::HyperedgeKind;
use std::collections::HashMap;

/// Dream Stage 5.5: 情感纠缠 — 共享高情感强度的节点自动建边
pub fn emotional_entanglement(brain: &mut Brain) -> Result<u32> {
    if brain.emotion_index.is_empty() {
        return Ok(0);
    }

    let store = match brain.redb_store.as_ref() {
        Some(s) => s,
        None => return Ok(0),
    };

    let mut hyperedges_created = 0u32;

    // 1. 从 emotion_index 获取各情感类型的高强度节点
    let emotions: Vec<(crate::types::Emotion, Vec<String>)> = brain.emotion_index
        .iter()
        .map(|(e, ids)| (*e, ids.clone()))
        .collect();

    for (_emotion, node_ids) in &emotions {
        // 读取节点详情
        let rtxn = store.begin_read()?;
        let table = rtxn.open_table(crate::storage::L1_NODES)
            .map_err(|e| MemHopError::Storage(format!("open L1_NODES: {}", e)))?;

        let mut high_intensity_nodes: Vec<KnowledgeNode> = Vec::new();
        for nid in node_ids {
            if let Some(bytes) = table.get(nid.as_str())
                .map_err(|e| MemHopError::Storage(format!("get node: {}", e)))?
                && let Ok(node) = bincode::deserialize::<KnowledgeNode>(bytes.value())
                && node.memory.emotion_intensity > 0.6
            {
                high_intensity_nodes.push(node);
            }
        }
        drop(table);
        drop(rtxn);

        // 2. 同情感类型高激活节点对 → Association 超边
        if high_intensity_nodes.len() < 2 {
            continue;
        }

        let wtxn = store.begin_write()?;
        let mut hyp_table = wtxn.open_table(crate::storage::L1_HYPEREDGES)
            .map_err(|e| MemHopError::Storage(format!("open L1_HYPEREDGES: {}", e)))?;

        for i in 0..high_intensity_nodes.len() {
            for j in i+1..high_intensity_nodes.len() {
                let a = &high_intensity_nodes[i];
                let b = &high_intensity_nodes[j];
                let weight = (a.memory.emotion_intensity + b.memory.emotion_intensity) / 2.0;

                let he_id = crate::batch_store::unique_id("emo");
                let now = chrono::Utc::now().timestamp_millis();
                let he = Hyperedge {
                    id: he_id.clone(),
                    node_ids: vec![a.id.clone(), b.id.clone()],
                    kind: HyperedgeKind::Association,
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
    }

    Ok(hyperedges_created)
}
