// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Session management API operations.

use crate::query::types::SessionStatus;
use crate::MemHop;

impl MemHop {
    /// Get the current session status (active topic IDs, count, empty flag).
    pub fn session_status(&self) -> SessionStatus {
        let active_topic_ids = self.get_active_topic_ids();
        let count = self.session_count();
        let is_empty = self.sessions_empty();
        SessionStatus {
            active_topic_ids,
            count,
            is_empty,
        }
    }

    /// Get all currently active Topic IDs in hex string format
    ///
    /// # Returns
    /// Vector of active topic IDs as hex strings
    pub(crate) fn get_active_topic_ids(&self) -> Vec<String> {
        self.session_manager
            .get_active_topic_ids()
            .iter()
            .map(|id| crate::shared::common::format_hash(*id))
            .collect()
    }

    /// Return the number of active topics in the session manager.
    pub(crate) fn session_count(&self) -> usize {
        self.session_manager.len()
    }

    /// Return true if the session manager has no active topics.
    pub(crate) fn sessions_empty(&self) -> bool {
        self.session_manager.is_empty()
    }
}
