// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! V2 snapshot-based checkpoint.

use crate::storage::engine::IndexSnapshotData;
use crate::MemHop;
use crate::MemHopError;
use crate::Result;

impl MemHop {
    /// Checkpoint: serialize all in-memory indices to a snapshot and persist
    /// via the v2 storage engine.
    pub fn checkpoint(&mut self) -> Result<()> {
        let snapshot = IndexSnapshotData {
            sparse_data: bincode::serialize(&self.sparse_index)
                .map_err(|e| MemHopError::Serialization(e.to_string()))?,
            ivf_data: vec![],
            l1_reverse_data: self.l1_reverse_index.serialize()?,
            l3_index_data: bincode::serialize(&self.l3_index_map)
                .map_err(|e| MemHopError::Serialization(e.to_string()))?,
        };

        // Persist IVF index via engine records (non-fatal: warn on failure)
        if let Some(ref ivf) = self.ivf_index {
            if let Err(e) = crate::index::vector::write_ivf_index(&mut self.engine, ivf) {
                tracing::warn!("Failed to persist IVF index: {}", e);
            }
        }

        self.engine.checkpoint(&snapshot)?;
        Ok(())
    }
}
