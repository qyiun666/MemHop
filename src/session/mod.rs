// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! In-memory session topic tracking with TTL-based lifecycle and
//! working-memory capacity limit (Miller's law: 7±2).

use crate::config::SessionConfig;
use std::collections::HashMap;

/// Topic activation state (pure memory, not persisted).
pub struct TopicActivation {
    pub topic_id: u64,
    pub last_hit_at: i64,
    pub ttl_ms: i64,
}

impl TopicActivation {
    fn new(topic_id: u64, ttl_ms: i64) -> Self {
        let now = current_timestamp_ms();
        Self {
            topic_id,
            last_hit_at: now,
            ttl_ms,
        }
    }

    fn update(&mut self, new_ttl_ms: Option<i64>) {
        self.last_hit_at = current_timestamp_ms();
        if let Some(ttl) = new_ttl_ms {
            self.ttl_ms = ttl;
        }
    }

    fn is_expired(&self, current_time: i64) -> bool {
        (current_time - self.last_hit_at) > self.ttl_ms
    }
}

/// Tracks active L2 Topics with working-memory capacity limit.
/// When capacity exceeded, LRU topic auto-deactivates (not deleted).
pub struct SessionManager {
    active_topics: HashMap<u64, TopicActivation>,
    default_ttl_ms: i64,
    /// Max simultaneously active topics (default 7, Miller's law).
    capacity: usize,
}

impl SessionManager {
    pub fn new(config: &SessionConfig) -> Self {
        Self {
            active_topics: HashMap::new(),
            default_ttl_ms: config.default_ttl_ms,
            capacity: config.capacity,
        }
    }

    /// Activates or refreshes a scene; evicts LRU if capacity exceeded.
    /// Scenes use the same internal tracking as topics.
    /// Returns evicted scene_id, or None.
    pub fn activate_scene(&mut self, scene_id: u64, ttl_ms: Option<i64>) -> Option<u64> {
        self.activate_topic(scene_id, ttl_ms)
    }

    /// Activates or refreshes a topic; evicts LRU if capacity exceeded.
    /// Returns evicted topic_id, or None.
    pub fn activate_topic(&mut self, topic_id: u64, ttl_ms: Option<i64>) -> Option<u64> {
        let effective_ttl = ttl_ms.unwrap_or(self.default_ttl_ms);

        if let Some(activation) = self.active_topics.get_mut(&topic_id) {
            activation.update(Some(effective_ttl));
            return None;
        }

        // New topic: check capacity before inserting
        let evicted = if self.active_topics.len() >= self.capacity {
            self.evict_lru()
        } else {
            None
        };

        let activation = TopicActivation::new(topic_id, effective_ttl);
        self.active_topics.insert(topic_id, activation);

        evicted
    }

    fn evict_lru(&mut self) -> Option<u64> {
        if self.active_topics.is_empty() {
            return None;
        }

        let lru_id = self
            .active_topics
            .iter()
            .min_by_key(|(_, activation)| activation.last_hit_at)
            .map(|(id, _)| *id);

        if let Some(id) = lru_id {
            self.active_topics.remove(&id);
        }

        lru_id
    }

    pub fn deactivate_topic(&mut self, topic_id: u64) {
        self.active_topics.remove(&topic_id);
    }

    pub fn get_active_topic_ids(&self) -> Vec<u64> {
        let current_time = current_timestamp_ms();

        self.active_topics
            .values()
            .filter(|activation| !activation.is_expired(current_time))
            .map(|activation| activation.topic_id)
            .collect()
    }

    /// TTL adjustment: `ttl += delta × 600_000 ms`, minimum 60_000 ms.
    pub fn adjust_activation(&mut self, topic_id: u64, delta: f32) {
        if let Some(activation) = self.active_topics.get_mut(&topic_id) {
            let adjustment = (delta * 600_000.0) as i64;
            let new_ttl = activation.ttl_ms + adjustment;
            activation.ttl_ms = new_ttl.max(60_000);
        }
    }

    pub fn purge_expired(&mut self, current_time: i64) {
        self.active_topics
            .retain(|_, activation| !activation.is_expired(current_time));
    }

    pub fn len(&self) -> usize {
        self.active_topics.len()
    }

    pub fn is_empty(&self) -> bool {
        self.active_topics.is_empty()
    }

    #[cfg(test)]
    pub fn default_ttl_ms(&self) -> i64 {
        self.default_ttl_ms
    }
}

impl Default for SessionManager {
    fn default() -> Self {
        Self::new(&SessionConfig {
            default_ttl_ms: 3_600_000,
            capacity: 7,
        })
    }
}

fn current_timestamp_ms() -> i64 {
    crate::util::get_current_timestamp()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn default_config() -> SessionConfig {
        SessionConfig {
            default_ttl_ms: 3_600_000,
            capacity: 7,
        }
    }

    #[test]
    fn test_activate_and_get_topics() {
        let mut manager = SessionManager::new(&default_config());

        manager.activate_topic(1001, None);
        manager.activate_topic(1002, None);
        manager.activate_topic(1003, Some(7_200_000)); // 2 hours

        let active_ids = manager.get_active_topic_ids();

        assert_eq!(active_ids.len(), 3);
        assert!(active_ids.contains(&1001));
        assert!(active_ids.contains(&1002));
        assert!(active_ids.contains(&1003));
    }

    #[test]
    fn test_deactivate_topic() {
        let mut manager = SessionManager::new(&default_config());

        manager.activate_topic(2001, None);
        manager.activate_topic(2002, None);

        assert_eq!(manager.len(), 2);

        manager.deactivate_topic(2001);

        assert_eq!(manager.len(), 1);

        let active_ids = manager.get_active_topic_ids();
        assert!(!active_ids.contains(&2001));
        assert!(active_ids.contains(&2002));
    }

    #[test]
    fn test_ttl_adjustment() {
        let mut manager = SessionManager::new(&default_config());

        manager.activate_topic(3001, None);

        let initial_ttl = manager.active_topics.get(&3001).unwrap().ttl_ms;
        assert_eq!(initial_ttl, 3_600_000);

        // delta=2.0 → +1,200,000 ms
        manager.adjust_activation(3001, 2.0);

        let adjusted_ttl = manager.active_topics.get(&3001).unwrap().ttl_ms;
        assert_eq!(adjusted_ttl, 4_800_000);

        // delta=-1.0 → -600,000 ms
        manager.adjust_activation(3001, -1.0);

        let final_ttl = manager.active_topics.get(&3001).unwrap().ttl_ms;
        assert_eq!(final_ttl, 4_200_000);
    }

    #[test]
    fn test_ttl_minimum_enforcement() {
        let mut manager = SessionManager::new(&default_config());

        manager.activate_topic(4001, Some(100_000));

        // Large negative delta → clamped to 60,000 ms minimum
        manager.adjust_activation(4001, -10.0);

        let ttl = manager.active_topics.get(&4001).unwrap().ttl_ms;
        assert_eq!(ttl, 60_000);
    }

    #[test]
    fn test_adjust_nonexistent_topic() {
        let mut manager = SessionManager::new(&default_config());

        manager.adjust_activation(9999, 1.0);

        assert_eq!(manager.len(), 0);
    }

    #[test]
    fn test_purge_expired() {
        let mut manager = SessionManager::new(&default_config());

        let current_time = current_timestamp_ms();
        let twenty_five_hours_ago = current_time - (25 * 3_600_000);
        let one_hour_ago = current_time - 3_600_000;

        let mut expired_activation = TopicActivation::new(5001, 3_600_000);
        expired_activation.last_hit_at = twenty_five_hours_ago;
        manager.active_topics.insert(5001, expired_activation);

        let mut active_activation = TopicActivation::new(5002, 3_600_000);
        active_activation.last_hit_at = one_hour_ago;
        manager.active_topics.insert(5002, active_activation);

        assert_eq!(manager.len(), 2);

        manager.purge_expired(current_time);

        assert_eq!(manager.len(), 1);
        assert!(manager.active_topics.contains_key(&5002));
        assert!(!manager.active_topics.contains_key(&5001));
    }

    #[test]
    fn test_24_hour_auto_expire() {
        let mut manager = SessionManager::new(&default_config());

        let current_time = current_timestamp_ms();
        let twenty_five_hours_ago = current_time - (25 * 3_600_000);

        let mut old_activation = TopicActivation::new(6001, 3_600_000);
        old_activation.last_hit_at = twenty_five_hours_ago;
        manager.active_topics.insert(6001, old_activation);

        assert_eq!(manager.len(), 1);

        // Auto-expired: still in map but not in active list
        let active_ids = manager.get_active_topic_ids();
        assert!(!active_ids.contains(&6001));
        assert_eq!(active_ids.len(), 0);
    }

    #[test]
    fn test_refresh_existing_topic() {
        let mut manager = SessionManager::new(&default_config());

        manager.activate_topic(7001, Some(1_800_000)); // 30 min

        let initial_ttl = manager.active_topics.get(&7001).unwrap().ttl_ms;
        assert_eq!(initial_ttl, 1_800_000);

        manager.activate_topic(7001, Some(7_200_000)); // 2 hours

        let refreshed_ttl = manager.active_topics.get(&7001).unwrap().ttl_ms;
        assert_eq!(refreshed_ttl, 7_200_000);

        assert_eq!(manager.len(), 1);
    }

    #[test]
    fn test_default_manager_properties() {
        let manager = SessionManager::new(&default_config());

        assert_eq!(manager.default_ttl_ms(), 3_600_000);
        assert!(manager.is_empty());
        assert_eq!(manager.len(), 0);
    }

    #[test]
    fn test_custom_ttl_on_activation() {
        let mut manager = SessionManager::new(&default_config());

        manager.activate_topic(8001, Some(5_400_000)); // 1.5 hours

        let activation = manager.active_topics.get(&8001).unwrap();
        assert_eq!(activation.ttl_ms, 5_400_000);
    }
}
