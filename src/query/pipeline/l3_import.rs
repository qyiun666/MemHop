// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L3 knowledge extraction from search entity hints.
//!
//! Consumes [`L3EntityHint`] values produced by LLM search preprocessing
//! and imports corresponding hypergraph nodes into the L3 store.
//!
//! Behaviour:
//! - For each hint, check whether a node with that name already exists in the
//!   graph (by keyword exact match). If so, skip (idempotent).
//! - Otherwise, create a new [`HypergraphNode`] and persist it.

use crate::l3::store::add_node_with_engine;
use crate::l3::{DegreeTracker, L3Index};
use crate::layers::hypergraph::HypergraphNode;
use crate::query::types::L3EntityHint;
use crate::shared::common::now_ms;
use crate::storage::StorageEngine;
use crate::util::hash_id;
use std::collections::HashMap;

/// Import L3 knowledge nodes from LLM-extracted entity hints.
///
/// For each hint:
/// 1. Check whether a node with a matching keyword already exists
///    (`search_by_keyword` with exact name).
/// 2. If it does, skip (idempotent).
/// 3. If not, create a new [`HypergraphNode`] and persist it via
///    `add_node_with_engine`.
///
/// # Returns
/// A list of newly created node `id_hash` values (empty when all hints
/// matched existing nodes).
pub fn import_entities_from_hints(
    engine: &mut StorageEngine,
    l3_index_map: &mut HashMap<u64, L3Index>,
    degree_tracker: &mut DegreeTracker,
    graph_id: u64,
    hints: &[L3EntityHint],
) -> crate::Result<Vec<u64>> {
    if hints.is_empty() {
        return Ok(Vec::new());
    }

    let mut imported_ids = Vec::with_capacity(hints.len());

    // Pre-ensure the index entry exists (release the mutable borrow before loop)
    l3_index_map.entry(graph_id).or_default();

    for hint in hints {
        // Idempotent: skip if a node with this exact name already exists.
        // Clone the search result to release any index borrow.
        let existing: Vec<u64> = l3_index_map
            .get(&graph_id)
            .map(|idx| idx.search_by_keyword(&hint.name, 1))
            .unwrap_or_default();
        if !existing.is_empty() {
            continue;
        }

        let id_hash = hash_id(&hint.name);
        let timestamp = now_ms();

        let node = HypergraphNode {
            id_hash,
            graph_id,
            title: hint.name.clone(),
            node_type: hint.entity_type.clone(),
            content: String::new(),
            keywords: vec![hint.name.clone()],
            source_ref: None,
            importance: 0.5,
            valid_from: timestamp,
            valid_until: 0,
            summary: None,
            created_at: timestamp,
            updated_at: timestamp,
            version: 1,
        };

        add_node_with_engine(engine, node, Some(degree_tracker), Some(l3_index_map))?;
        imported_ids.push(id_hash);
    }

    Ok(imported_ids)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::storage::StorageEngine;

    #[test]
    fn test_import_empty_hints() {
        let temp = tempfile::NamedTempFile::new().unwrap();
        let mut engine = StorageEngine::create(temp.path(), 768).unwrap();
        let mut l3_index_map = HashMap::new();
        let mut dt = DegreeTracker::new();
        let graph_id = hash_id("test_graph");

        let result =
            import_entities_from_hints(&mut engine, &mut l3_index_map, &mut dt, graph_id, &[])
                .unwrap();

        assert!(result.is_empty());
    }

    #[test]
    fn test_import_creates_nodes() {
        let temp = tempfile::NamedTempFile::new().unwrap();
        let mut engine = StorageEngine::create(temp.path(), 768).unwrap();
        let mut l3_index_map = HashMap::new();
        let mut dt = DegreeTracker::new();
        let graph_id = hash_id("test_graph");

        let hints = vec![
            L3EntityHint {
                name: "Rust".to_string(),
                entity_type: "language".to_string(),
            },
            L3EntityHint {
                name: "MemHop".to_string(),
                entity_type: "project".to_string(),
            },
        ];

        let ids =
            import_entities_from_hints(&mut engine, &mut l3_index_map, &mut dt, graph_id, &hints)
                .unwrap();

        assert_eq!(ids.len(), 2, "two hints should create two nodes");

        // Verify nodes are in the index
        let index = l3_index_map.get(&graph_id).unwrap();
        let rust_nodes = index.search_by_keyword("Rust", 10);
        assert_eq!(rust_nodes.len(), 1);
        assert_eq!(rust_nodes[0], hash_id("Rust"));

        let memhop_nodes = index.search_by_keyword("MemHop", 10);
        assert_eq!(memhop_nodes.len(), 1);
        assert_eq!(memhop_nodes[0], hash_id("MemHop"));
    }

    #[test]
    fn test_import_idempotent() {
        let temp = tempfile::NamedTempFile::new().unwrap();
        let mut engine = StorageEngine::create(temp.path(), 768).unwrap();
        let mut l3_index_map = HashMap::new();
        let mut dt = DegreeTracker::new();
        let graph_id = hash_id("test_graph");

        let hints = vec![L3EntityHint {
            name: "Rust".to_string(),
            entity_type: "language".to_string(),
        }];

        // First import — should create 1 node
        let ids1 =
            import_entities_from_hints(&mut engine, &mut l3_index_map, &mut dt, graph_id, &hints)
                .unwrap();
        assert_eq!(ids1.len(), 1);

        // Second import with same hints — should create 0 nodes (idempotent)
        let ids2 =
            import_entities_from_hints(&mut engine, &mut l3_index_map, &mut dt, graph_id, &hints)
                .unwrap();
        assert_eq!(ids2.len(), 0, "duplicate hints must not create new nodes");
    }

    #[test]
    fn test_import_multiple_graphs() {
        let temp = tempfile::NamedTempFile::new().unwrap();
        let mut engine = StorageEngine::create(temp.path(), 768).unwrap();
        let mut l3_index_map = HashMap::new();
        let mut dt = DegreeTracker::new();

        let gid_a = hash_id("graph_a");
        let gid_b = hash_id("graph_b");

        let hints_a = vec![L3EntityHint {
            name: "Alpha".to_string(),
            entity_type: "concept".to_string(),
        }];
        let hints_b = vec![L3EntityHint {
            name: "Beta".to_string(),
            entity_type: "concept".to_string(),
        }];

        let ids_a =
            import_entities_from_hints(&mut engine, &mut l3_index_map, &mut dt, gid_a, &hints_a)
                .unwrap();
        assert_eq!(ids_a.len(), 1);

        let ids_b =
            import_entities_from_hints(&mut engine, &mut l3_index_map, &mut dt, gid_b, &hints_b)
                .unwrap();
        assert_eq!(ids_b.len(), 1);

        // Each graph should have exactly its own node
        assert_eq!(
            l3_index_map
                .get(&gid_a)
                .unwrap()
                .search_by_keyword("Alpha", 10)
                .len(),
            1
        );
        assert_eq!(
            l3_index_map
                .get(&gid_b)
                .unwrap()
                .search_by_keyword("Beta", 10)
                .len(),
            1
        );
        assert!(l3_index_map
            .get(&gid_a)
            .unwrap()
            .search_by_keyword("Beta", 10)
            .is_empty());
    }
}
