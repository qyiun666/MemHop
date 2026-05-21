/// Recall scope filtering strategies.
///
/// Converts a Python scope dict into a filtered candidate ID set
/// for recall_among(), enabling targeted recall within domain, layer,
/// knowledge tree, time range, or session.

use std::collections::HashSet;

use crate::meta_index::MetaIndex;
use crate::storage::{LmdbStorage, StorageError};

/// Parsed time range filter.
#[derive(Debug, Clone)]
pub(crate) struct TimeRange {
    pub after_ms: Option<i64>,
    pub before_ms: Option<i64>,
}

/// Parsed recall scope from Python dict.
#[derive(Debug, Clone, Default)]
pub(crate) struct RecallScope {
    pub domain: Option<String>,
    pub layer: Option<String>,
    pub knowledge_tree: Option<String>,
    pub session_id: Option<String>,
    pub time_range: Option<TimeRange>,
}

/// Build a candidate ID set from scope + meta_index.
/// Returns None if no filters are active (full recall).
/// Returns Some(empty) if filters yield no candidates.
pub(crate) fn scope_to_candidates(
    scope: &RecallScope,
    meta_index: &MetaIndex,
    storage: &LmdbStorage,
    _now_ms: i64,
) -> Result<Option<HashSet<String>>, StorageError> {
    if scope.domain.is_none()
        && scope.layer.is_none()
        && scope.knowledge_tree.is_none()
        && scope.session_id.is_none()
        && scope.time_range.is_none()
    {
        return Ok(None);
    }

    // Step 1: MetaIndex-based equality filters (domain, layer, session_id)
    let idx_candidates = meta_index.get_candidates(
        scope.layer.as_deref(),
        None,            // r#type
        scope.domain.as_deref(),
        scope.session_id.as_deref(),
        None,            // path
        None,            // parent
    );

    // Step 2: knowledge_tree → BFS gather all IDs under the tree root
    let tree_candidates = if let Some(ref root) = scope.knowledge_tree {
        let mut tree_set = HashSet::new();
        tree_set.insert(root.clone());
        // Expand: find children of each node via by_parent index
        let mut frontier: Vec<String> = vec![root.clone()];
        while let Some(parent) = frontier.pop() {
            if let Some(children) = meta_index.by_parent.get(&parent) {
                for child in children {
                    if tree_set.insert(child.clone()) {
                        frontier.push(child.clone());
                    }
                }
            }
        }
        Some(tree_set)
    } else {
        None
    };

    // Step 3: Merge MetaIndex candidates with tree candidates
    let mut candidates = match (idx_candidates, tree_candidates) {
        (Some(a), Some(b)) => Some(a.intersection(&b).cloned().collect()),
        (Some(a), None) => Some(a),
        (None, Some(b)) => Some(b),
        (None, None) => None,
    };

    // Step 4: Time range filter via MetaRecord.created_at
    if let Some(ref tr) = scope.time_range {
        let after_ms = tr.after_ms.unwrap_or(0);
        let before_ms = tr.before_ms.unwrap_or(i64::MAX);

        let all_metas = storage.all_metas()?;
        let within_time: HashSet<String> = all_metas
            .iter()
            .filter(|(_, m)| m.created_at >= after_ms && m.created_at <= before_ms)
            .map(|(id, _)| id.clone())
            .collect();

        candidates = match candidates {
            Some(c) => Some(c.intersection(&within_time).cloned().collect()),
            None => Some(within_time),
        };
    }

    // Step 5: If candidates exist but are empty, return Some(empty)
    match candidates {
        Some(ref c) if c.is_empty() => Ok(Some(HashSet::new())),
        other => Ok(other),
    }
}
