//! EntanglementEvent — 跨树纠缠事件（v0.12.1）
//!
//! 当不同知识树中的记忆在 recall / plan 压缩 / dream 中
//! 被同时激活时，记录为纠缠事件，用于后续展开和衰减。

use serde::{Deserialize, Serialize};
use std::collections::HashSet;

use crate::brain::Brain;
use crate::engram::Engram;
use crate::error::MemHopError;
use crate::error::Result;

/// 纠缠事件 — 记录来自不同知识树的记忆节点之间的关联。
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct EntanglementEvent {
    #[serde(default)]
    pub id: String,
    /// 参与的记忆节点 engram IDs
    #[serde(default)]
    pub nodes: Vec<String>,
    /// 跨了哪些知识树
    #[serde(default)]
    pub tree_ids: Vec<String>,
    /// 纠缠描述
    #[serde(default)]
    pub context: String,
    #[serde(default)]
    pub trigger: EntanglementTrigger,
    /// 纠缠强度 0–1
    #[serde(default)]
    pub strength: f32,
    /// 关联的计划 IDs
    #[serde(default)]
    pub plan_ids: Vec<String>,
    /// 创建时间（Unix ms）
    #[serde(default)]
    pub created_at: i64,
    /// 最后命中时间（Unix ms）
    #[serde(default)]
    pub last_hit_at: i64,
    /// 命中次数
    #[serde(default)]
    pub hit_count: u32,
}

/// 纠缠事件触发原因。
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub enum EntanglementTrigger {
    /// 召回时跨树命中
    RecallCrossTree,
    /// Plan 压缩时发现
    PlanCompression,
    /// Dream REM 阶段涌现
    DreamEmergence,
    /// 手动创建
    #[default]
    Manual,
}

// ── 公开查询方法 ─────────────────────────────────────────

/// v0.12.1: 获取所有纠缠事件
pub fn get_all_entanglements(brain: &Brain) -> Result<Vec<EntanglementEvent>> {
    let rtxn = brain
        .storage
        .begin_read()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    let events = brain
        .storage
        .get_all_entanglements(&rtxn)
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    Ok(events)
}

/// v0.12.1: 获取单个纠缠事件
pub fn get_entanglement(brain: &Brain, event_id: &str) -> Result<Option<EntanglementEvent>> {
    let rtxn = brain
        .storage
        .begin_read()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    brain
        .storage
        .get_entanglement(&rtxn, event_id)
        .map_err(|e| MemHopError::Storage(e.to_string()))
}

// ── 展开 / 更新方法 ───────────────────────────────────────

/// v0.12.1: 展开纠缠事件中的节点 — 将 strength > 0.5 的事件中
/// 尚未在结果中的 engram 添加到 associations。
pub(crate) fn expand_entangled_results(brain: &Brain, associations: &mut Vec<Engram>) {
    let mut included_ids: HashSet<String> = HashSet::new();
    for eng in associations.iter() {
        included_ids.insert(eng.id.clone());
    }

    let rtxn = match brain.storage.begin_read() {
        Ok(t) => t,
        Err(_) => return,
    };

    let mut to_add: Vec<Engram> = Vec::new();
    for eng in associations.iter() {
        let event_ids = match brain.storage.get_entanglement_ids_for_node(&rtxn, &eng.id) {
            Ok(ids) => ids,
            Err(_) => continue,
        };
        for eid in &event_ids {
            let event = match brain.storage.get_entanglement(&rtxn, eid) {
                Ok(Some(e)) => e,
                _ => continue,
            };
            if event.strength > 0.5 {
                for node_id in &event.nodes {
                    if !included_ids.contains(node_id)
                        && let Ok(Some(extra)) =
                            brain.storage.get_hippocampus(&rtxn, node_id)
                    {
                        included_ids.insert(node_id.clone());
                        to_add.push(extra);
                    }
                }
            }
        }
    }
    drop(rtxn);

    associations.extend(to_add);
}

/// v0.12.1: 跨树纠缠 — 创建或更新纠缠事件
pub(crate) fn create_or_update_entanglement(
    brain: &Brain,
    nodes: Vec<String>,
    tree_ids: Vec<String>,
    context: String,
    trigger: EntanglementTrigger,
) {
    let now = crate::brain::now_millis();

    // Check if an existing event covers these same nodes
    let rtxn = match brain.storage.begin_read() {
        Ok(t) => t,
        Err(e) => {
            eprintln!("[entanglement] begin_read error: {}", e);
            return;
        }
    };

    let existing_event_ids = if let Some(first_node) = nodes.first() {
        brain.storage
            .get_entanglement_ids_for_node(&rtxn, first_node)
            .unwrap_or_default()
    } else {
        vec![]
    };
    let rtxn_ref = &rtxn; // borrow for the find_map closure

    let found = existing_event_ids.iter().find_map(|eid| {
        match brain.storage.get_entanglement(rtxn_ref, eid) {
            Ok(Some(event)) => {
                if event.nodes.len() == nodes.len()
                    && event.nodes.iter().all(|n| nodes.contains(n))
                {
                    Some(event.clone())
                } else {
                    None
                }
            }
            _ => None,
        }
    });
    drop(rtxn);

    if let Some(mut existing) = found {
        // Update existing event
        existing.hit_count += 1;
        existing.strength = (existing.strength + 0.2).min(1.0);
        existing.last_hit_at = now;

        let mut wtxn = match brain.storage.begin_write() {
            Ok(t) => t,
            Err(e) => {
                eprintln!("[entanglement] begin_write error: {}", e);
                return;
            }
        };
        if let Err(e) = brain.storage.put_entanglement(&mut wtxn, &existing) {
            eprintln!("[entanglement] put error: {}", e);
        }
        let _ = wtxn.commit();
    } else {
        // Create new event
        let id = crate::brain::generate_id();
        let event = EntanglementEvent {
            id: id.clone(),
            nodes: nodes.clone(),
            tree_ids,
            context,
            trigger,
            strength: 0.3,
            plan_ids: vec![],
            created_at: now,
            last_hit_at: now,
            hit_count: 1,
        };

        let mut wtxn = match brain.storage.begin_write() {
            Ok(t) => t,
            Err(e) => {
                eprintln!("[entanglement] begin_write error: {}", e);
                return;
            }
        };
        if let Err(e) = brain.storage.put_entanglement(&mut wtxn, &event) {
            eprintln!("[entanglement] put error: {}", e);
            let _ = wtxn.commit();
            return;
        }
        // Build node reverse index
        for node_id in &nodes {
            if let Err(e) = brain.storage.add_entanglement_node(&mut wtxn, node_id, &id) {
                eprintln!("[entanglement] add_node error: {}", e);
                break;
            }
        }
        let _ = wtxn.commit();
    }
}
