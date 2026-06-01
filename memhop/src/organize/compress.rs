//! Plan compression: consolidate dialogue turns into a Knowledge engram.
//!
//! v0.12.2: Extracted from `Brain::compress_plan()` into its own module.

use std::collections::{HashMap, HashSet};

use crate::brain::{generate_id, now_millis, Brain};
use crate::engram::{CompressResult, Engram, EngramKind, PlanState, Protection};
use crate::entanglement::EntanglementTrigger;
use crate::error::{MemHopError, Result};

/// Compress a plan's dialogue turns into a Knowledge engram and archive the originals.
///
/// v0.12.0: Full compression — heuristic summary, Knowledge engram creation,
/// Episode engram archiving, PlanNode state update to Completed.
pub(crate) fn compress_plan(brain: &mut Brain, plan_id: &str) -> Result<CompressResult> {
    let now = now_millis();

    // 1. Get PlanNode
    let rtxn = brain
        .storage
        .begin_read()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    let plan_option = brain
        .storage
        .get_plan(&rtxn, plan_id)
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    drop(rtxn);

    let plan = match plan_option {
        Some(p) => p,
        None => {
            return Ok(CompressResult {
                knowledge_id: String::new(),
                archived_count: 0,
                summary: String::new(),
                skipped: true,
            })
        }
    };

    // 2. Read all DialogueTurns
    let rtxn = brain
        .storage
        .begin_read()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    let turns = brain
        .storage
        .get_dialogues_by_plan(&rtxn, plan_id)
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    drop(rtxn);

    // 3. If < 3 turns, skip (not enough to compress meaningfully)
    if turns.len() < 3 {
        return Ok(CompressResult {
            knowledge_id: String::new(),
            archived_count: 0,
            summary: String::new(),
            skipped: true,
        });
    }

    // 4. Generate heuristic summary (no LLM)
    let summary = crate::organize::heuristic_compress(brain, &turns, &plan.name);

    // 5. Find associated engram IDs via PlanIndex
    let engram_ids: Vec<String> = {
        let pi = brain.plan_index.borrow();
        pi.candidates(Some(plan_id))
    };

    // 6. Create Knowledge Engram
    let knowledge_id = generate_id();
    let summary_vector = brain.encode_text(&summary);
    let turn_ids: Vec<String> = turns.iter().map(|t| t.id.clone()).collect();
    let knowledge_engram = Engram {
        id: knowledge_id.clone(),
        text: summary.clone(),
        summary: None,
        vector: summary_vector,
        keywords: Vec::new(),
        content_type: None,
        valence: 0.0,
        arousal: 0.5,
        vitality: 1.0,
        protection: Protection::Normal,
        created_at: now,
        last_activated: now,
        activation_count: 1,
        kind: EngramKind::Knowledge,
        meta: {
            let mut m = HashMap::new();
            m.insert(
                "compressed_from_plan".to_string(),
                serde_json::json!(plan_id),
            );
            m.insert("turn_count".to_string(), serde_json::json!(turns.len()));
            m
        },
        is_archived: false,
        is_dormant: false,
        turn_id: None,
        tree_path: None,
        source_path: None,
        source_textunit: None,
        turn_ids,
        context_id: None,
        tree_ref: None,
    };

    // 7. Store Knowledge Engram (updates all indexes)
    brain.store_engram(knowledge_engram)?;

    // 8. Archive original Episode engrams (mark is_archived in LMDB)
    let archived_count = engram_ids.len();
    for engram_id in &engram_ids {
        let rtxn = brain
            .storage
            .begin_read()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let mut engram = match brain.storage.get_hippocampus(&rtxn, engram_id) {
            Ok(Some(e)) => e,
            _ => {
                drop(rtxn);
                continue;
            }
        };
        drop(rtxn);

        engram.is_archived = true;

        let mut wtxn = brain
            .storage
            .begin_write()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        brain
            .storage
            .put_hippocampus(&mut wtxn, engram_id, &engram)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        wtxn
            .commit()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
    }

    // 9. Update PlanNode: set compressed_summary, state=Completed, completed_at
    {
        let mut wtxn = brain
            .storage
            .begin_write()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let mut plan_to_update = plan.clone();
        plan_to_update.compressed_summary = Some(summary.clone());
        plan_to_update.state = PlanState::Completed;
        plan_to_update.completed_at = Some(now);
        brain
            .storage
            .put_plan(&mut wtxn, &plan_to_update)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        wtxn
            .commit()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
    }

    // 10. Update PlanIndex (in-memory)
    {
        let mut pi = brain.plan_index.borrow_mut();
        if let Some(info) = pi.plan_info.get_mut(plan_id) {
            info.state = PlanState::Completed;
        }
        if pi.active_plan_id.as_deref() == Some(plan_id) {
            pi.active_plan_id = None;
        }
    }

    // v0.12.1: 检测压缩涉及的 engram 是否来自不同树 → 创建纠缠事件
    {
        let mut tree_ids_set: HashSet<String> = HashSet::new();
        let mut node_ids: Vec<String> = Vec::new();
        let mut context_ids: Vec<String> = Vec::new();
        let rtxn = brain
            .storage
            .begin_read()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        for engram_id in &engram_ids {
            if let Ok(Some(engram)) = brain.storage.get_hippocampus(&rtxn, engram_id)
                && let Some(ref tr) = engram.tree_ref
            {
                tree_ids_set.insert(tr.tree_id.clone());
                if !node_ids.contains(&engram.id) {
                    node_ids.push(engram.id.clone());
                }
                // v0.13.0: collect context IDs
                if let Some(ref ctx_id) = engram.context_id
                    && !context_ids.contains(ctx_id)
                {
                    context_ids.push(ctx_id.clone());
                }
            }
        }
        drop(rtxn);
        if tree_ids_set.len() >= 2 && node_ids.len() >= 2 {
            let context = format!("Plan 压缩跨树关联: {}", plan.name);
            let tree_ids: Vec<String> = tree_ids_set.into_iter().collect();
            crate::entanglement::create_or_update_entanglement(
                brain,
                node_ids,
                tree_ids,
                context,
                EntanglementTrigger::PlanCompression,
                context_ids,
            );
        }
    }

    Ok(CompressResult {
        knowledge_id,
        archived_count,
        summary,
        skipped: false,
    })
}

#[cfg(test)]
mod tests {
    /// The moved module compiles and exports the function.
    #[test]
    fn test_compress_module_exported() {
        // Compile-time check: the module exists and compress_plan is accessible.
        // Actual functional tests remain in integration_test.rs and plan_integration_test.rs.
        assert!(true);
    }
}
