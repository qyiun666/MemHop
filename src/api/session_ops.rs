// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Session management API operations.

use crate::MemHop;

impl MemHop {
    /// Activate a Topic for session management. If capacity is exceeded,
    /// the LRU topic is evicted and optionally processed through a lightweight
    /// dream consolidation before removal from the active set.
    ///
    /// # Arguments
    /// * `topic_id` - Topic ID string (will be converted to hash)
    /// * `ttl_ms` - Optional custom TTL in milliseconds, uses default if None
    pub fn activate_topic(&mut self, topic_id: &str, ttl_ms: Option<i64>) {
        let id_hash = crate::shared::common::parse_id_to_hash(topic_id);
        let evicted = self.session_manager.activate_topic(id_hash, ttl_ms);

        if let Some(evicted_id) = evicted {
            if self.config.auto_dream_on_evict {
                if let Err(e) = self.dream_single_topic(evicted_id) {
                    tracing::warn!(
                        "[memhop] Warning: dream_single_topic failed for evicted topic: {}",
                        e
                    );
                }
            }
        }
    }

    /// Deactivate the specified Topic
    ///
    /// # Arguments
    /// * `topic_id` - Topic ID string to deactivate
    pub fn deactivate_topic(&mut self, topic_id: &str) {
        let id_hash = crate::shared::common::parse_id_to_hash(topic_id);
        self.session_manager.deactivate_topic(id_hash);
    }

    /// Get all currently active Topic IDs in hex string format
    ///
    /// # Returns
    /// Vector of active topic IDs as hex strings
    pub fn get_active_topic_ids(&self) -> Vec<String> {
        self.session_manager
            .get_active_topic_ids()
            .iter()
            .map(|id| crate::shared::common::format_hash(*id))
            .collect()
    }

    /// Adjust the activation TTL of a Topic
    ///
    /// # Arguments
    /// * `topic_id` - Topic ID string
    /// * `delta` - Adjustment factor, TTL change = delta × 600,000 ms
    pub fn adjust_activation(&mut self, topic_id: &str, delta: f32) {
        let id_hash = crate::shared::common::parse_id_to_hash(topic_id);
        self.session_manager.adjust_activation(id_hash, delta);
    }

    /// Purge expired topics from the session manager.
    pub fn purge_expired_sessions(&mut self) {
        let now = crate::util::get_current_timestamp();
        self.session_manager.purge_expired(now);
    }

    /// Return the number of active topics in the session manager.
    pub fn session_count(&self) -> usize {
        self.session_manager.len()
    }

    /// Return true if the session manager has no active topics.
    pub fn sessions_empty(&self) -> bool {
        self.session_manager.is_empty()
    }
}
