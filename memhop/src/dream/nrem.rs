//! dream/nrem — NREM stages: hyperedge weight time decay + pruning.
//! Lowers weights of old hyperedges; removes those below threshold.

use crate::brain::Brain;
use crate::engram::Hyperedge;
use crate::error::{Result, MemHopError};
use crate::types::ConsolidateReport;

/// Decay hyperedge weights based on time since last update.
/// new_weight = weight * exp(-0.01 * hours_since_update)
/// Removes hyperedges with weight < 0.05.
pub fn nrem_decay(brain: &mut Brain, report: &mut ConsolidateReport) -> Result<()> {
    let now = chrono::Utc::now().timestamp_millis();
    let env = brain.l1_env.env.clone();

    // Collect all hyperedges and their decayed states
    let txn = env.read_txn()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    let mut to_update: Vec<(String, Hyperedge)> = Vec::new();
    let mut to_delete: Vec<String> = Vec::new();

    if let Ok(iter) = brain.l1_env.hyperedges.iter(&txn) {
        for item in iter {
            if let Ok((_key, bytes)) = item
                && let Ok(mut he) = bincode::deserialize::<Hyperedge>(bytes) {
                    let hours_since_update = (now - he.updated_at) as f32 / 3600000.0;
                    if hours_since_update <= 0.0 {
                        continue;
                    }

                    let new_weight = he.weight * (-0.01 * hours_since_update).exp();

                    if new_weight < 0.05 {
                        // Mark for deletion
                        to_delete.push(he.id.clone());
                        report.vitality_decayed += 1;
                    } else {
                        // Mark for weight update
                        he.weight = new_weight;
                        he.updated_at = now;
                        to_update.push((he.id.clone(), he));
                        report.vitality_decayed += 1;
                    }
                }
        }
    }
    drop(txn);

    if to_update.is_empty() && to_delete.is_empty() {
        return Ok(());
    }

    // Apply changes in a single write transaction
    let mut wtxn = env.write_txn()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;

    for (id, he) in &to_update {
        let bytes = bincode::serialize(he)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        brain.l1_env.hyperedges.put(&mut wtxn, id, &bytes)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
    }

    for id in &to_delete {
        // Remove from hyperedges
        brain.l1_env.hyperedges.delete(&mut wtxn, id)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        // Also remove from node_to_hyperedges back-references
        // We need to find all nodes that reference this hyperedge
        // Since we don't have a hyperedge_to_nodes index, we skip cleanup
        // The orphaned references are harmless for lookup (they just won't resolve)
    }

    wtxn.commit().map_err(|e| MemHopError::Storage(e.to_string()))?;

    Ok(())
}
