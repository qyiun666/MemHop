//! Scene Gating — Anchor-based retrieval scoping.
//!
//! Manages the anchor index: anchor_name → set of engram IDs.
//! Used during recall to narrow the candidate set to memories
//! associated with the current attention anchors.

use std::collections::HashSet;

use crate::error::Result;
use crate::storage::LmdbStorage;

/// Scene gate for anchor-based candidate filtering.
pub struct SceneGate;

impl SceneGate {
    /// Get candidate engram IDs for a set of anchors (union).
    /// Returns None when anchors is empty (caller should skip scene gating).
    pub fn get_candidates(
        storage: &LmdbStorage,
        anchors: &[String],
    ) -> Result<Option<HashSet<String>>> {
        if anchors.is_empty() {
            return Ok(None);
        }
        let txn = storage.begin_read()?;
        let candidates = storage.anchor_candidates(&txn, anchors)?;
        drop(txn);
        Ok(candidates)
    }

    /// Associate an engram ID with the given anchors.
    pub fn add_to_anchors(
        storage: &LmdbStorage,
        engram_id: &str,
        anchors: &[String],
    ) -> Result<()> {
        if anchors.is_empty() {
            return Ok(());
        }
        let mut txn = storage.begin_write()?;
        for anchor in anchors {
            storage.anchor_add(&mut txn, anchor, engram_id)?;
        }
        txn.commit().map_err(|e| crate::error::MemHopError::Storage(e.to_string()))?;
        Ok(())
    }

    /// Remove an engram ID from the given anchors.
    #[allow(dead_code)]
    pub fn remove_from_anchors(
        storage: &LmdbStorage,
        engram_id: &str,
        anchors: &[String],
    ) -> Result<()> {
        if anchors.is_empty() {
            return Ok(());
        }
        let mut txn = storage.begin_write()?;
        for anchor in anchors {
            storage.anchor_remove(&mut txn, anchor, engram_id)?;
        }
        txn.commit().map_err(|e| crate::error::MemHopError::Storage(e.to_string()))?;
        Ok(())
    }

    /// Get all anchor names in the index.
    #[allow(dead_code)]
    pub fn all_anchors(storage: &LmdbStorage) -> Result<Vec<String>> {
        let txn = storage.begin_read()?;
        let names = storage.all_anchor_names(&txn)?;
        drop(txn);
        Ok(names)
    }
}
