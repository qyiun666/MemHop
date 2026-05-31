//! Plan management: set name, query tree, complete.
//!
//! v0.12.2: Extracted from brain.rs.

use crate::brain::{now_millis, Brain};
use crate::engram::{PlanNode, PlanState};
use crate::error::{MemHopError, Result};

/// 1. Set the name of a plan.
pub(crate) fn set_plan_name(brain: &Brain, plan_id: &str, name: &str) -> Result<()> {
    let mut txn = brain.storage.begin_write()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    let mut plan = brain.storage.get_plan(&txn, plan_id)
        .map_err(|e| MemHopError::Storage(e.to_string()))?
        .ok_or_else(|| MemHopError::Storage(format!("plan {} not found", plan_id)))?;
    plan.name = name.to_string();
    brain.storage.put_plan(&mut txn, &plan)
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    txn.commit().map_err(|e| MemHopError::Storage(e.to_string()))?;
    Ok(())
}

/// 2. Get the plan tree. If plan_id is None, returns all root plans.
///    If plan_id is Some, returns that plan and all its descendants (flat list).
pub(crate) fn get_plan_tree(
    brain: &Brain,
    plan_id: Option<&str>,
) -> Result<Vec<PlanNode>> {
    let txn = brain.storage.begin_read()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    let all = brain.storage.get_all_plans(&txn)
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    match plan_id {
        None => Ok(all.into_iter().filter(|p| p.parent_id.is_none()).collect()),
        Some(pid) => {
            let mut result = Vec::new();
            let mut queue: Vec<String> = vec![pid.to_string()];
            while let Some(id) = queue.pop() {
                for plan in &all {
                    if plan.id == id {
                        result.push(plan.clone());
                        break;
                    }
                }
                for plan in &all {
                    if plan.parent_id.as_deref() == Some(&id) {
                        queue.push(plan.id.clone());
                    }
                }
            }
            Ok(result)
        }
    }
}

/// 3. Complete a plan: change state to Completed, set completed_at,
///    optionally generate compressed summary via LLM.
///    All-or-nothing transaction semantics.
pub(crate) fn complete_plan(brain: &mut Brain, plan_id: &str) -> Result<()> {
    let mut txn = brain.storage.begin_write()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    let mut plan = brain.storage.get_plan(&txn, plan_id)
        .map_err(|e| MemHopError::Storage(e.to_string()))?
        .ok_or_else(|| MemHopError::Storage(format!("plan {} not found", plan_id)))?;

    let now = now_millis();
    plan.state = PlanState::Completed;
    plan.completed_at = Some(now);

    // Generate compressed summary if LLM is available
    if let Some(ref llm) = brain.llm {
        let turns = brain.storage.get_dialogues_by_plan(&txn, plan_id)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        if !turns.is_empty() {
            let content: String = turns.iter()
                .flat_map(|t| vec![t.user_input.as_str(), t.agent_response.as_str()])
                .collect::<Vec<_>>()
                .join("\n");
            let prompt = crate::llm_provider::PromptTemplates::summarize(&content);
            match llm.generate(&prompt, 256) {
                Ok(summary) => { plan.compressed_summary = Some(summary); }
                Err(e) => eprintln!("[brain] LLM summary failed for plan {}: {}", plan_id, e),
            }
        }
    }

    brain.storage.put_plan(&mut txn, &plan)
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    txn.commit().map_err(|e| MemHopError::Storage(e.to_string()))?;
    Ok(())
}
