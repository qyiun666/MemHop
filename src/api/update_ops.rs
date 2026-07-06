// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Update API operations.

use crate::query::types::{UpdateRequest, UpdateResult};
use crate::MemHop;
use crate::Result;

impl MemHop {
    /// Update memory with multi-level updates.
    ///
    /// This is a thin orchestration wrapper: validate parameters and delegate to
    /// `query::update::update_memory_internal`, which performs WAL-protected
    /// cross-layer writes.
    pub fn update_memory(&mut self, request: UpdateRequest) -> Result<UpdateResult> {
        use crate::query::update::update_memory_internal;

        if request.topic_id.is_empty() {
            return Err(crate::MemHopError::InvalidQuery(
                "topic_id is required".to_string(),
            ));
        }
        if request.dialogue_text.is_empty() {
            return Err(crate::MemHopError::InvalidQuery(
                "dialogue_text is required".to_string(),
            ));
        }

        let result = update_memory_internal(
            &mut self.mmap,
            &mut self.header,
            request,
            &mut self.btree,
            &mut self.sparse_index,
            &mut self.l2_meta,
            &mut self.file,
            &self.config,
            &mut self.journal_buffer,
            Some(&mut self.degree_tracker),
            Some(&mut self.l3_index_map),
        );

        // Rebuild IVF index after mutation (single update may have added vectors)
        self.rebuild_ivf_index();

        // Trigger checkpoint if the buffered WAL has reached the configured interval.
        if result.is_ok() && should_checkpoint(&self.config, self.journal_buffer.len()) {
            if let Err(e) = self.checkpoint() {
                tracing::warn!("Auto-checkpoint failed: {}", e);
            }
        }

        result
    }
}

/// Determine whether the buffered journal should be flushed based on config.
fn should_checkpoint(config: &crate::config::MemHopConfig, buffered_count: usize) -> bool {
    match config.auto_checkpoint_interval {
        None => true,
        Some(0) => false,
        Some(n) => buffered_count as u64 >= n,
    }
}
