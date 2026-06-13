//! Activation management module for MemHop v0.31+
//!
//! This module implements a three-layer memory state system (Active/Latent/Dormant)
//! that simulates human brain memory activation levels.

pub mod decay;

/// Re-export MemoryState from util
pub use crate::util::MemoryState;

/// Activation configuration
#[derive(Debug, Clone)]
pub struct ActivationConfig {
    pub active_capacity: usize,
    /// Score threshold to promote to Active state
    pub active_threshold: f32,
    /// Score threshold below which memory becomes Dormant
    pub dormant_threshold: f32,
    /// Base decay coefficient (~29 days half-life)
    pub decay_lambda: f32,
    /// Bonus score applied when memory is recalled
    pub recall_bonus: f32,
    /// Importance floor: memories with importance >= this won't go below Latent
    pub importance_floor_active: f32,
    /// Importance floor: memories with importance >= this always stay Active
    pub importance_floor_latent: f32,
}

impl Default for ActivationConfig {
    fn default() -> Self {
        Self {
            active_capacity: 5000,
            active_threshold: 0.2,
            dormant_threshold: 0.05,
            decay_lambda: 0.001,
            recall_bonus: 0.3,
            importance_floor_active: 0.8,
            importance_floor_latent: 0.95,
        }
    }
}

/// Activation manager responsible for calculating scores and determining state transitions
pub struct ActivationManager {
    config: ActivationConfig,
}

impl ActivationManager {
    /// Create a new ActivationManager with the given configuration
    pub fn new(config: ActivationConfig) -> Self {
        Self { config }
    }

    /// Calculate activation score based on importance and time since last access
    ///
    /// Formula: score = importance × exp(-lambda × hours_since_last_access)
    ///
    /// # Arguments
    /// * `importance` - Memory importance value [0.0, 1.0]
    /// * `hours_since_last_access` - Hours elapsed since last access
    ///
    /// # Returns
    /// Activation score clamped to [0.0, 1.0]
    pub fn calculate_score(&self, importance: f32, hours_since_last_access: f32) -> f32 {
        let score = importance * (-self.config.decay_lambda * hours_since_last_access).exp();
        score.clamp(0.0, 1.0)
    }

    /// Apply recall bonus to activation score
    ///
    /// When a memory is recalled, its activation score receives a temporary boost.
    ///
    /// # Arguments
    /// * `score` - Current activation score
    ///
    /// # Returns
    /// Boosted score clamped to [0.0, 1.0]
    pub fn apply_recall_bonus(&self, score: f32) -> f32 {
        (score + self.config.recall_bonus).clamp(0.0, 1.0)
    }

    /// Determine target memory state based on activation score and importance
    ///
    /// Priority rules (high to low):
    /// 1. importance >= 0.95 → always Active
    /// 2. importance >= 0.80 && score >= dormant_threshold → minimum Latent
    /// 3. score >= active_threshold (0.2) → Active
    /// 4. score >= dormant_threshold (0.05) → Latent
    /// 5. otherwise → Dormant
    ///
    /// # Arguments
    /// * `score` - Current activation score
    /// * `importance` - Memory importance value
    ///
    /// # Returns
    /// Target MemoryState
    pub fn should_transition(&self, score: f32, importance: f32) -> MemoryState {
        // Priority 1: Core memories never degrade
        if importance >= self.config.importance_floor_latent {
            return MemoryState::Active;
        }

        // Priority 2: High-importance + high-score → Active
        if importance >= self.config.importance_floor_active
            && score >= self.config.active_threshold
        {
            return MemoryState::Active;
        }

        // Priority 2b: High-importance + medium-score → Latent (prevents going dormant)
        if importance >= self.config.importance_floor_active
            && score >= self.config.dormant_threshold
        {
            return MemoryState::Latent;
        }

        // Priority 3-5: Score-based transitions
        if score >= self.config.active_threshold {
            MemoryState::Active
        } else if score >= self.config.dormant_threshold {
            MemoryState::Latent
        } else {
            MemoryState::Dormant
        }
    }

    /// Check if memory should be demoted from Active state
    ///
    /// # Arguments
    /// * `score` - Current activation score
    /// * `importance` - Memory importance value
    ///
    /// # Returns
    /// true if should demote, false otherwise
    pub fn should_demote_from_active(&self, score: f32, importance: f32) -> bool {
        if importance >= self.config.importance_floor_latent {
            return false; // Never demote core memories
        }
        score < self.config.active_threshold
    }

    /// Check if memory should be demoted from Latent state
    ///
    /// # Arguments
    /// * `score` - Current activation score
    /// * `importance` - Memory importance value
    ///
    /// # Returns
    /// true if should demote, false otherwise
    pub fn should_demote_from_latent(&self, score: f32, importance: f32) -> bool {
        if importance >= self.config.importance_floor_active {
            return false; // Important memories stay at least Latent
        }
        score < self.config.dormant_threshold
    }

    /// Check if memory should be promoted to Active state
    ///
    /// # Arguments
    /// * `score` - Current activation score
    ///
    /// # Returns
    /// true if should promote, false otherwise
    pub fn should_promote_to_active(&self, score: f32) -> bool {
        score >= self.config.active_threshold
    }

    /// Check if memory should be promoted to Latent state
    ///
    /// # Arguments
    /// * `score` - Current activation score
    ///
    /// # Returns
    /// true if should promote, false otherwise
    pub fn should_promote_to_latent(&self, score: f32) -> bool {
        score >= self.config.dormant_threshold
    }

    /// Get reference to the activation configuration
    pub fn config(&self) -> &ActivationConfig {
        &self.config
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_default_config() {
        let config = ActivationConfig::default();
        assert_eq!(config.active_capacity, 5000);
        assert!((config.active_threshold - 0.2).abs() < 1e-6);
        assert!((config.dormant_threshold - 0.05).abs() < 1e-6);
        assert!((config.decay_lambda - 0.001).abs() < 1e-6);
        assert!((config.recall_bonus - 0.3).abs() < 1e-6);
        assert!((config.importance_floor_active - 0.8).abs() < 1e-6);
        assert!((config.importance_floor_latent - 0.95).abs() < 1e-6);
    }

    #[test]
    fn test_calculate_score_fresh_memory() {
        let config = ActivationConfig::default();
        let manager = ActivationManager::new(config);

        // High importance, recently accessed
        let score = manager.calculate_score(0.9, 1.0);
        assert!(score > 0.8);
        assert!(score <= 1.0);
    }

    #[test]
    fn test_calculate_score_old_memory() {
        let config = ActivationConfig::default();
        let manager = ActivationManager::new(config);

        // Low importance, long time ago (30 days = 720 hours)
        let score = manager.calculate_score(0.3, 720.0);
        assert!(score < 0.2);
        assert!(score >= 0.0);
    }

    #[test]
    fn test_calculate_score_clamping() {
        let config = ActivationConfig::default();
        let manager = ActivationManager::new(config);

        // Extreme values should be clamped
        let score1 = manager.calculate_score(1.5, 0.0);
        assert!(score1 <= 1.0);

        let score2 = manager.calculate_score(-0.1, 100.0);
        assert!(score2 >= 0.0);
    }

    #[test]
    fn test_apply_recall_bonus() {
        let config = ActivationConfig::default();
        let manager = ActivationManager::new(config);

        let base_score = 0.5;
        let boosted = manager.apply_recall_bonus(base_score);
        assert!((boosted - 0.8).abs() < 1e-6);

        // Should clamp to 1.0
        let high_score = 0.9;
        let clamped = manager.apply_recall_bonus(high_score);
        assert!((clamped - 1.0).abs() < 1e-6);
    }

    #[test]
    fn test_should_transition_core_memory() {
        let config = ActivationConfig::default();
        let manager = ActivationManager::new(config);

        // Core memory (importance >= 0.95) always stays Active
        assert_eq!(manager.should_transition(0.01, 0.96), MemoryState::Active);
        assert_eq!(manager.should_transition(0.0, 1.0), MemoryState::Active);
    }

    #[test]
    fn test_should_transition_important_memory() {
        let config = ActivationConfig::default();
        let manager = ActivationManager::new(config);

        // Important memory (importance >= 0.8) won't go below Latent if score >= dormant_threshold
        assert_eq!(manager.should_transition(0.1, 0.85), MemoryState::Latent);
        assert_eq!(manager.should_transition(0.05, 0.85), MemoryState::Latent);

        // But can go Dormant if score < dormant_threshold
        assert_eq!(manager.should_transition(0.01, 0.85), MemoryState::Dormant);
    }

    #[test]
    fn test_should_transition_normal_memory() {
        let config = ActivationConfig::default();
        let manager = ActivationManager::new(config);

        // High score → Active
        assert_eq!(manager.should_transition(0.5, 0.5), MemoryState::Active);

        // Medium score → Latent
        assert_eq!(manager.should_transition(0.1, 0.5), MemoryState::Latent);

        // Low score → Dormant
        assert_eq!(manager.should_transition(0.01, 0.3), MemoryState::Dormant);
    }

    #[test]
    fn test_demote_from_active() {
        let config = ActivationConfig::default();
        let manager = ActivationManager::new(config);

        // Normal memory with low score should be demoted
        assert!(manager.should_demote_from_active(0.1, 0.5));

        // Core memory should never be demoted
        assert!(!manager.should_demote_from_active(0.1, 0.96));

        // High score should not trigger demotion
        assert!(!manager.should_demote_from_active(0.3, 0.5));
    }

    #[test]
    fn test_demote_from_latent() {
        let config = ActivationConfig::default();
        let manager = ActivationManager::new(config);

        // Normal memory with very low score should be demoted
        assert!(manager.should_demote_from_latent(0.01, 0.5));

        // Important memory should not be demoted from Latent
        assert!(!manager.should_demote_from_latent(0.01, 0.85));

        // Score above threshold should not trigger demotion
        assert!(!manager.should_demote_from_latent(0.1, 0.5));
    }

    #[test]
    fn test_promote_to_active() {
        let config = ActivationConfig::default();
        let manager = ActivationManager::new(config);

        assert!(manager.should_promote_to_active(0.3));
        assert!(!manager.should_promote_to_active(0.1));
        assert!(manager.should_promote_to_active(0.2)); // Exactly at threshold
    }

    #[test]
    fn test_promote_to_latent() {
        let config = ActivationConfig::default();
        let manager = ActivationManager::new(config);

        assert!(manager.should_promote_to_latent(0.1));
        assert!(!manager.should_promote_to_latent(0.01));
        assert!(manager.should_promote_to_latent(0.05)); // Exactly at threshold
    }

    #[test]
    fn test_priority_2_high_score_goes_active() {
        // Regression test for audit report bug: importance=0.85 + score=0.9 should be Active
        let config = ActivationConfig::default();
        let manager = ActivationManager::new(config);

        // High-importance (0.85) + high-score (0.9 >= active_threshold 0.2) → Active
        assert_eq!(manager.should_transition(0.9, 0.85), MemoryState::Active);

        // High-importance (0.85) + medium-score (0.1 >= dormant_threshold 0.05) → Latent
        assert_eq!(manager.should_transition(0.1, 0.85), MemoryState::Latent);

        // High-importance (0.85) + low-score (0.01 < dormant_threshold 0.05) → Dormant
        assert_eq!(manager.should_transition(0.01, 0.85), MemoryState::Dormant);
    }
}
