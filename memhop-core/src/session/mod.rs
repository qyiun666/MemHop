//! session — in-memory session context for agent conversation tracking.
//! MemHop process is stateless; session state is ephemeral per MCP connection.
//! Agent side maintains session_id; this module provides lightweight tracking.

use crate::types::{ActivatedTopicInfo, ActivationEntry, SessionState};
use std::collections::HashMap;

/// Lightweight in-memory session manager.
/// Does NOT persist to LMDB — sessions are lost on process restart.
pub struct SessionManager {
    sessions: HashMap<String, SessionState>,
}

impl SessionManager {
    pub fn new() -> Self {
        SessionManager {
            sessions: HashMap::new(),
        }
    }

    /// Get or create a session by ID.
    pub fn get_or_create(&mut self, session_id: &str) -> &mut SessionState {
        let now = chrono::Utc::now().timestamp_millis();
        self.sessions
            .entry(session_id.to_string())
            .or_insert_with(|| SessionState {
                session_id: session_id.to_string(),
                active_topics: HashMap::new(),
                turn_count: 0,
                started_at: now,
                last_active_at: now,
            })
    }

    /// Record a turn with optional topic association.
    pub fn record_turn(&mut self, session_id: &str, topic_id: Option<String>) {
        let state = self.get_or_create(session_id);
        state.turn_count += 1;
        state.last_active_at = chrono::Utc::now().timestamp_millis();
        // Auto-activate topic on record with default 1h TTL
        if let Some(tid) = topic_id {
            Self::activate_in_state(state, &tid, 3_600_000);
        }
    }

    /// Activate a topic with TTL (ms).
    pub fn activate(&mut self, session_id: &str, topic_id: &str, ttl_ms: i64) {
        let state = self.get_or_create(session_id);
        Self::activate_in_state(state, topic_id, ttl_ms);
    }

    fn activate_in_state(state: &mut SessionState, topic_id: &str, ttl_ms: i64) {
        let now = chrono::Utc::now().timestamp_millis();
        state.active_topics.insert(
            topic_id.to_string(),
            ActivationEntry {
                topic_id: topic_id.to_string(),
                activated_at: now,
                ttl_ms,
                last_hit_at: now,
            },
        );
    }

    /// Deactivate a topic.
    pub fn deactivate(&mut self, session_id: &str, topic_id: &str) {
        if let Some(state) = self.sessions.get_mut(session_id) {
            state.active_topics.remove(topic_id);
        }
    }

    /// Remove expired activations across all sessions.
    pub fn purge_expired(&mut self) {
        let now = chrono::Utc::now().timestamp_millis();
        // 清理过期 activations
        for state in self.sessions.values_mut() {
            state
                .active_topics
                .retain(|_, entry| (now - entry.activated_at) < entry.ttl_ms);
        }
        // 移除无活跃 topic 且超过 24h 未活跃的空会话
        let cutoff = now - 86_400_000;
        self.sessions
            .retain(|_, state| !state.active_topics.is_empty() || state.last_active_at > cutoff);
    }

    /// Check if a topic is currently active (not expired).
    pub fn is_active(&self, session_id: &str, topic_id: &str) -> bool {
        let now = chrono::Utc::now().timestamp_millis();
        if let Some(state) = self.sessions.get(session_id)
            && let Some(entry) = state.active_topics.get(topic_id)
        {
            return (now - entry.activated_at) < entry.ttl_ms;
        }
        false
    }

    /// Get all active topic infos across all sessions.
    pub fn get_active_list(&self) -> Vec<ActivatedTopicInfo> {
        let now = chrono::Utc::now().timestamp_millis();
        let mut result = Vec::new();
        for state in self.sessions.values() {
            for entry in state.active_topics.values() {
                if (now - entry.activated_at) < entry.ttl_ms {
                    result.push(ActivatedTopicInfo {
                        topic_id: entry.topic_id.clone(),
                        activated_at: entry.activated_at,
                        ttl_ms: entry.ttl_ms,
                        last_hit_at: entry.last_hit_at,
                    });
                }
            }
        }
        result
    }

    /// Get all active topic IDs for a specific session.
    pub fn get_active_topic_ids(&self, session_id: &str) -> Vec<String> {
        let now = chrono::Utc::now().timestamp_millis();
        if let Some(state) = self.sessions.get(session_id) {
            state
                .active_topics
                .iter()
                .filter(|(_, entry)| (now - entry.activated_at) < entry.ttl_ms)
                .map(|(tid, _)| tid.clone())
                .collect()
        } else {
            Vec::new()
        }
    }

    /// Remove a session (cleanup).
    pub fn remove(&mut self, session_id: &str) {
        self.sessions.remove(session_id);
    }

    /// v0.16.0: Adjust activation TTL as feedback signal.
    /// Positive delta extends TTL, negative delta shortens it.
    pub fn adjust_activation(&mut self, session_id: &str, topic_id: &str, delta: f32) {
        if let Some(state) = self.sessions.get_mut(session_id)
            && let Some(entry) = state.active_topics.get_mut(topic_id)
        {
            // Adjust TTL by delta (in ms): +0.1 → +60s, -0.1 → -60s
            let ttl_adjust = (delta * 600_000.0) as i64;
            entry.ttl_ms = (entry.ttl_ms + ttl_adjust).max(60_000); // min 1 minute
            entry.last_hit_at = chrono::Utc::now().timestamp_millis();
        }
    }

    /// Get session count.
    pub fn len(&self) -> usize {
        self.sessions.len()
    }

    pub fn is_empty(&self) -> bool {
        self.sessions.is_empty()
    }
}

impl Default for SessionManager {
    fn default() -> Self {
        Self::new()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_activation_and_purge() {
        let mut mgr = SessionManager::new();
        mgr.activate("sess_1", "topic_a", 100); // 100ms TTL
        assert!(mgr.is_active("sess_1", "topic_a"));

        // Wait for expiry
        std::thread::sleep(std::time::Duration::from_millis(150));
        mgr.purge_expired();
        assert!(!mgr.is_active("sess_1", "topic_a"));
    }

    #[test]
    fn test_get_active_list() {
        let mut mgr = SessionManager::new();
        mgr.activate("sess_1", "topic_a", 60_000);
        mgr.activate("sess_1", "topic_b", 60_000);
        let list = mgr.get_active_list();
        assert_eq!(list.len(), 2);
    }
}
