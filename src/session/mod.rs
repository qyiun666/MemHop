//! Session activation management module for MemHop v0.32+
//!
//! This module implements session-level topic tracking that manages active L2 Topics
//! with TTL-based lifecycle control. Unlike the persistent Activation system, this is
//! purely in-memory and tracks which topics are currently "hot" during a user session.

use std::collections::HashMap;

/// Topic activation state (pure memory, not persisted)
pub struct TopicActivation {
    /// L2 Topic id_hash
    pub topic_id: u64,
    /// Activation time (Unix milliseconds)
    pub activated_at: i64,
    /// Last hit time (Unix milliseconds)
    pub last_hit_at: i64,
    /// Time-to-live in milliseconds (default: 3,600,000 = 1 hour)
    pub ttl_ms: i64,
}

impl TopicActivation {
    /// Create a new TopicActivation
    ///
    /// # Arguments
    /// * `topic_id` - The topic identifier
    /// * `ttl_ms` - Time-to-live in milliseconds
    fn new(topic_id: u64, ttl_ms: i64) -> Self {
        let now = current_timestamp_ms();
        Self {
            topic_id,
            activated_at: now,
            last_hit_at: now,
            ttl_ms,
        }
    }

    /// Update the last hit timestamp and optionally refresh TTL
    ///
    /// # Arguments
    /// * `new_ttl_ms` - Optional new TTL value, None keeps existing TTL
    fn update(&mut self, new_ttl_ms: Option<i64>) {
        self.last_hit_at = current_timestamp_ms();
        if let Some(ttl) = new_ttl_ms {
            self.ttl_ms = ttl;
        }
    }

    /// Check if this topic has expired based on last hit time and its own TTL
    ///
    /// # Arguments
    /// * `current_time` - Current timestamp in milliseconds
    ///
    /// # Returns
    /// true if expired, false otherwise
    fn is_expired(&self, current_time: i64) -> bool {
        (current_time - self.last_hit_at) > self.ttl_ms
    }
}

/// Session manager: tracks active L2 Topics with working-memory capacity limit.
///
/// Inspired by human working memory capacity (7±2 items, Miller 1956).
/// When the active set exceeds the capacity, the least-recently-hit topic is
/// automatically deactivated (moved to long-term storage, not deleted).
pub struct SessionManager {
    active_topics: HashMap<u64, TopicActivation>,
    default_ttl_ms: i64,
    /// Working memory capacity — maximum number of simultaneously active topics.
    /// Default is 7 (Miller's law: 7±2 chunks).
    capacity: usize,
}

impl SessionManager {
    /// Create a new SessionManager with default TTL of 1 hour and capacity of 7
    ///
    /// # Returns
    /// A new SessionManager instance
    pub fn new() -> Self {
        Self {
            active_topics: HashMap::new(),
            default_ttl_ms: 3_600_000, // 1 hour in milliseconds
            capacity: 7,
        }
    }

    /// Create a new SessionManager with custom capacity
    ///
    /// # Arguments
    /// * `capacity` - Working memory capacity (recommended: 5-9)
    pub fn with_capacity(capacity: usize) -> Self {
        Self {
            active_topics: HashMap::new(),
            default_ttl_ms: 3_600_000,
            capacity: capacity.max(1),
        }
    }

    /// Activate or refresh a topic. If capacity is exceeded, evict the least-recently-hit topic.
    ///
    /// If the topic already exists, updates its last_hit_at and optionally refreshes TTL.
    /// If the topic doesn't exist, creates a new TopicActivation entry.
    /// If adding would exceed capacity, the LRU topic is deactivated first.
    ///
    /// # Arguments
    /// * `topic_id` - The topic identifier to activate
    /// * `ttl_ms` - Optional custom TTL in milliseconds, uses default_ttl_ms if None
    ///
    /// # Returns
    /// The evicted topic_id if capacity was exceeded, None otherwise.
    pub fn activate_topic(&mut self, topic_id: u64, ttl_ms: Option<i64>) -> Option<u64> {
        let effective_ttl = ttl_ms.unwrap_or(self.default_ttl_ms);

        if let Some(activation) = self.active_topics.get_mut(&topic_id) {
            // Topic exists, update last_hit_at and optionally refresh TTL
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

    /// Evict the least-recently-hit topic to make room for new activations.
    ///
    /// This is called automatically when `activate_topic` would exceed capacity.
    /// The evicted topic is simply removed from the active set; it remains in
    /// long-term storage (L2) and can be reactivated later.
    ///
    /// # Returns
    /// The evicted topic_id, or None if the active set was empty.
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

    /// Deactivate a topic by removing it from the active set
    ///
    /// # Arguments
    /// * `topic_id` - The topic identifier to deactivate
    pub fn deactivate_topic(&mut self, topic_id: u64) {
        self.active_topics.remove(&topic_id);
    }

    /// Get all active (non-expired) topic IDs
    ///
    /// Filters out topics based on their individual TTL values.
    ///
    /// # Returns
    /// Vector of active topic IDs
    pub fn get_active_topic_ids(&self) -> Vec<u64> {
        let current_time = current_timestamp_ms();

        self.active_topics
            .values()
            .filter(|activation| !activation.is_expired(current_time))
            .map(|activation| activation.topic_id)
            .collect()
    }

    /// Adjust the TTL of an active topic using delta multiplier
    ///
    /// Formula: `ttl += delta × 600,000 ms`
    /// Minimum TTL: 60,000 ms
    ///
    /// If the topic doesn't exist, this operation is silently ignored.
    ///
    /// # Arguments
    /// * `topic_id` - The topic identifier to adjust
    /// * `delta` - Multiplier for TTL adjustment (can be negative)
    pub fn adjust_activation(&mut self, topic_id: u64, delta: f32) {
        if let Some(activation) = self.active_topics.get_mut(&topic_id) {
            let adjustment = (delta * 600_000.0) as i64;
            let new_ttl = activation.ttl_ms + adjustment;

            // Enforce minimum TTL of 60,000 ms
            activation.ttl_ms = new_ttl.max(60_000);
        }
        // If topic doesn't exist, silently ignore
    }

    /// Purge all expired topics based on current time
    ///
    /// Removes topics where `current_time - last_hit_at > topic.ttl_ms`.
    ///
    /// # Arguments
    /// * `current_time` - Current timestamp in milliseconds for expiration check
    pub fn purge_expired(&mut self, current_time: i64) {
        self.active_topics
            .retain(|_, activation| !activation.is_expired(current_time));
    }

    /// Get the number of tracked topics (including expired ones)
    ///
    /// # Returns
    /// Total count of topics in the session manager
    pub fn len(&self) -> usize {
        self.active_topics.len()
    }

    /// Check if the session manager is empty
    ///
    /// # Returns
    /// true if no topics are tracked
    pub fn is_empty(&self) -> bool {
        self.active_topics.is_empty()
    }

    /// Get reference to the default TTL
    ///
    /// # Returns
    /// Default TTL in milliseconds
    pub fn default_ttl_ms(&self) -> i64 {
        self.default_ttl_ms
    }
}

impl Default for SessionManager {
    fn default() -> Self {
        Self::new()
    }
}

/// Get current timestamp in milliseconds since Unix epoch
///
/// # Returns
/// Current time as i64 milliseconds
fn current_timestamp_ms() -> i64 {
    crate::util::get_current_timestamp()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_activate_and_get_topics() {
        let mut manager = SessionManager::new();

        // Activate some topics
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
        let mut manager = SessionManager::new();

        manager.activate_topic(2001, None);
        manager.activate_topic(2002, None);

        assert_eq!(manager.len(), 2);

        // Deactivate one topic
        manager.deactivate_topic(2001);

        assert_eq!(manager.len(), 1);

        let active_ids = manager.get_active_topic_ids();
        assert!(!active_ids.contains(&2001));
        assert!(active_ids.contains(&2002));
    }

    #[test]
    fn test_ttl_adjustment() {
        let mut manager = SessionManager::new();

        // Activate topic with default TTL (3,600,000 ms)
        manager.activate_topic(3001, None);

        // Verify initial TTL
        let initial_ttl = manager.active_topics.get(&3001).unwrap().ttl_ms;
        assert_eq!(initial_ttl, 3_600_000);

        // Adjust with delta=2.0 (should add 1,200,000 ms)
        manager.adjust_activation(3001, 2.0);

        let adjusted_ttl = manager.active_topics.get(&3001).unwrap().ttl_ms;
        assert_eq!(adjusted_ttl, 4_800_000);

        // Adjust with delta=-1.0 (should subtract 600,000 ms)
        manager.adjust_activation(3001, -1.0);

        let final_ttl = manager.active_topics.get(&3001).unwrap().ttl_ms;
        assert_eq!(final_ttl, 4_200_000);
    }

    #[test]
    fn test_ttl_minimum_enforcement() {
        let mut manager = SessionManager::new();

        manager.activate_topic(4001, Some(100_000)); // Start with 100,000 ms

        // Try to reduce TTL below minimum with large negative delta
        manager.adjust_activation(4001, -10.0); // Would be 100,000 - 6,000,000 = -5,900,000

        let ttl = manager.active_topics.get(&4001).unwrap().ttl_ms;
        assert_eq!(ttl, 60_000); // Should be clamped to minimum
    }

    #[test]
    fn test_adjust_nonexistent_topic() {
        let mut manager = SessionManager::new();

        // Adjusting a non-existent topic should not panic
        manager.adjust_activation(9999, 1.0);

        assert_eq!(manager.len(), 0);
    }

    #[test]
    fn test_purge_expired() {
        let mut manager = SessionManager::new();

        // Manually insert topics with different timestamps
        let current_time = current_timestamp_ms();
        let twenty_five_hours_ago = current_time - (25 * 3_600_000);
        let one_hour_ago = current_time - 3_600_000;

        // Insert expired topic
        let mut expired_activation = TopicActivation::new(5001, 3_600_000);
        expired_activation.last_hit_at = twenty_five_hours_ago;
        manager.active_topics.insert(5001, expired_activation);

        // Insert active topic
        let mut active_activation = TopicActivation::new(5002, 3_600_000);
        active_activation.last_hit_at = one_hour_ago;
        manager.active_topics.insert(5002, active_activation);

        assert_eq!(manager.len(), 2);

        // Purge expired topics
        manager.purge_expired(current_time);

        assert_eq!(manager.len(), 1);
        assert!(manager.active_topics.contains_key(&5002));
        assert!(!manager.active_topics.contains_key(&5001));
    }

    #[test]
    fn test_24_hour_auto_expire() {
        let mut manager = SessionManager::new();

        // Simulate a topic activated 25 hours ago
        let current_time = current_timestamp_ms();
        let twenty_five_hours_ago = current_time - (25 * 3_600_000);

        let mut old_activation = TopicActivation::new(6001, 3_600_000);
        old_activation.last_hit_at = twenty_five_hours_ago;
        manager.active_topics.insert(6001, old_activation);

        // Topic should still be in the map
        assert_eq!(manager.len(), 1);

        // But should not appear in active list (auto-expired)
        let active_ids = manager.get_active_topic_ids();
        assert!(!active_ids.contains(&6001));
        assert_eq!(active_ids.len(), 0);
    }

    #[test]
    fn test_refresh_existing_topic() {
        let mut manager = SessionManager::new();

        manager.activate_topic(7001, Some(1_800_000)); // 30 minutes

        let initial_ttl = manager.active_topics.get(&7001).unwrap().ttl_ms;
        assert_eq!(initial_ttl, 1_800_000);

        // Refresh with new TTL
        manager.activate_topic(7001, Some(7_200_000)); // 2 hours

        let refreshed_ttl = manager.active_topics.get(&7001).unwrap().ttl_ms;
        assert_eq!(refreshed_ttl, 7_200_000);

        // Count should remain the same
        assert_eq!(manager.len(), 1);
    }

    #[test]
    fn test_default_manager_properties() {
        let manager = SessionManager::new();

        assert_eq!(manager.default_ttl_ms(), 3_600_000);
        assert!(manager.is_empty());
        assert_eq!(manager.len(), 0);
    }

    #[test]
    fn test_custom_ttl_on_activation() {
        let mut manager = SessionManager::new();

        // Activate with custom TTL
        manager.activate_topic(8001, Some(5_400_000)); // 1.5 hours

        let activation = manager.active_topics.get(&8001).unwrap();
        assert_eq!(activation.ttl_ms, 5_400_000);
    }
}
