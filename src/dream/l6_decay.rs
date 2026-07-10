// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Stage: L6 Pathway Weight Decay — time-based exponential decay of procedural memory weights.

use crate::config::DecayConfig;
use crate::layers::pathway::PathwayWeightSlot;
use crate::shared::common::now_ms;
use crate::storage::StorageEngine;
use crate::MemHopError;

/// Report produced by the L6 pathway decay stage.
#[derive(Debug, Clone, PartialEq)]
pub struct L6DecayReport {
    /// Number of pathway slots whose weight was decayed.
    pub decayed: usize,
    /// Number of pathway slots removed because weight fell below threshold.
    pub pruned: usize,
    /// Decayed pathway slots with their updated weight values.
    pub decayed_details: Vec<PathwayWeightSlot>,
    /// Pruned pathway slots with their final weight values before removal.
    pub pruned_details: Vec<PathwayWeightSlot>,
}

/// Apply time-based exponential decay to all L6 pathway weights.
///
/// Reads all persisted `PathwayWeightSlot` entries, decays their `weight` by
/// `weight *= exp(-lambda * delta_seconds)`, and removes any slot whose weight
/// drops below `pathway_remove_threshold`. The surviving slots are written back
/// to the mmap page chain.
///
/// Returns the number of decayed and pruned slots, plus lists of affected slots.
pub fn decay_l6_pathways(
    engine: &mut StorageEngine,
    decay_config: &DecayConfig,
) -> Result<L6DecayReport, MemHopError> {
    let pathways = crate::query::l6_ops::read_pathways(engine)?;
    if pathways.is_empty() {
        return Ok(L6DecayReport {
            decayed: 0,
            pruned: 0,
            decayed_details: Vec::new(),
            pruned_details: Vec::new(),
        });
    }

    let now = now_ms();
    let lambda = decay_config.lambda_pathway;
    let threshold = decay_config.pathway_remove_threshold;
    let mut decayed = 0usize;
    let mut pruned = 0usize;
    let mut retained = Vec::with_capacity(pathways.len());
    let mut decayed_details = Vec::new();
    let mut pruned_details = Vec::new();

    for mut pathway in pathways {
        let dt_ms = now.saturating_sub(pathway.last_accessed as i64).max(0) as f32;
        let dt_seconds = dt_ms / 1000.0;
        let new_weight = pathway.weight * (-lambda * dt_seconds).exp();

        if new_weight < threshold {
            pruned_details.push(pathway.clone());
            pruned += 1;
            continue;
        }

        if (new_weight - pathway.weight).abs() > f32::EPSILON {
            pathway.weight = new_weight;
            pathway.updated_at = now;
            pathway.version += 1;
            decayed_details.push(pathway.clone());
            decayed += 1;
        }
        retained.push(pathway);
    }

    crate::query::l6_ops::write_pathways(engine, &retained)?;

    Ok(L6DecayReport {
        decayed,
        pruned,
        decayed_details,
        pruned_details,
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::layers::pathway::PathwayWeightSlot;
    use tempfile::NamedTempFile;

    fn default_decay_config() -> DecayConfig {
        DecayConfig {
            lambda_node: 0.01,
            lambda_edge: 0.02,
            node_remove_threshold: 0.05,
            node_prune_edges_threshold: 0.15,
            edge_remove_threshold: 0.05,
            min_edge_nodes: 2,
            lambda_pathway: 0.01,
            pathway_remove_threshold: 0.05,
        }
    }

    #[test]
    fn test_decay_l6_pathways_basic() {
        let temp = NamedTempFile::new().unwrap();
        let mut engine = StorageEngine::create(temp.path(), 768).unwrap();
        let old = (now_ms() - 100_000) as u64; // ~1.7 minutes ago
        let slot = PathwayWeightSlot {
            id_hash: 6001,
            source_node: "condition:deploy".into(),
            target_node: "action:restart".into(),
            weight: 1.0,
            trigger_count: 1,
            success_rate: 0.9,
            last_accessed: old,
            metadata: "{}".into(),
            created_at: old as i64,
            updated_at: old as i64,
            version: 1,
        };
        crate::query::l6_ops::add_l6(&mut engine, vec![slot]).unwrap();

        let report = decay_l6_pathways(&mut engine, &default_decay_config()).unwrap();

        assert_eq!(report.decayed, 1);
        assert_eq!(report.pruned, 0);

        let list = crate::query::l6_ops::list_l6(&engine, None).unwrap();
        assert_eq!(list.len(), 1);
        assert!(list[0].weight < 1.0);
        assert!(list[0].weight >= 0.05);

        let _ = temp;
    }

    #[test]
    fn test_decay_l6_pathways_prunes_weak_pathway() {
        let temp = NamedTempFile::new().unwrap();
        let mut engine = StorageEngine::create(temp.path(), 768).unwrap();
        let old = (now_ms() - 10_000_000) as u64; // ~2.8 hours ago
        let slot = PathwayWeightSlot {
            id_hash: 6002,
            source_node: "condition:idle".into(),
            target_node: "action:nop".into(),
            weight: 0.1,
            trigger_count: 1,
            success_rate: 0.5,
            last_accessed: old,
            metadata: "{}".into(),
            created_at: old as i64,
            updated_at: old as i64,
            version: 1,
        };
        crate::query::l6_ops::add_l6(&mut engine, vec![slot]).unwrap();

        let report = decay_l6_pathways(&mut engine, &default_decay_config()).unwrap();

        assert_eq!(report.pruned, 1);
        assert_eq!(report.decayed, 0);

        let list = crate::query::l6_ops::list_l6(&engine, None).unwrap();
        assert!(list.is_empty());

        let _ = temp;
    }

    #[test]
    fn test_decay_l6_pathways_empty() {
        let temp = NamedTempFile::new().unwrap();
        let mut engine = StorageEngine::create(temp.path(), 768).unwrap();
        let report = decay_l6_pathways(&mut engine, &default_decay_config()).unwrap();
        assert_eq!(report.decayed, 0);
        assert_eq!(report.pruned, 0);
        let _ = temp;
    }
}
