//! Personalized decay coefficient calculation for Activation system
//!
//! This module implements the personalized decay lambda formula:
//! personal_lambda = base_lambda / (1 + emotional_boost + recall_boost + connectivity_boost)
//!
//! Where:
//! - emotional_boost = emotion_intensity × 2.0
//! - recall_boost = activation_score × 1.5
//! - connectivity_boost = min(hyperedge_count, 5) × 0.3
//!
//! Effect: Memories with strong emotions, frequent recalls, or rich connections
//! decay more slowly, simulating human brain memory consolidation.

use crate::dream::emotion::apply_emotional_boost;

/// Calculate personalized decay lambda
///
/// # Arguments
/// * `base_lambda` - Base decay coefficient (e.g., 0.001 for ~29 days half-life)
/// * `emotion_intensity` - Emotional intensity of the memory [0.0, 1.0]
/// * `activation_score` - Current activation score [0.0, 1.0]
/// * `hyperedge_count` - Number of hyperedges connected to this memory
///
/// # Returns
/// Personalized decay lambda (always <= base_lambda)
///
/// # Formula
/// ```text
/// personal_lambda = base_lambda / (1 + emotional_boost + recall_boost + connectivity_boost)
/// where:
///   emotional_boost = emotion_intensity × 2.0
///   recall_boost = activation_score × 1.5
///   connectivity_boost = min(hyperedge_count, 5) × 0.3
/// ```
pub fn personalized_decay_lambda(
    base_lambda: f32,
    emotion_intensity: f32,
    activation_score: f32,
    hyperedge_count: u16,
) -> f32 {
    let emotional_boost = emotion_intensity * 2.0;
    let recall_boost = activation_score * 1.5;
    let connectivity_boost = (hyperedge_count.min(5) as f32) * 0.3;

    let divisor = 1.0 + emotional_boost + recall_boost + connectivity_boost;
    base_lambda / divisor
}

/// Calculate personalized decay lambda from valence and arousal directly
///
/// This is a convenience wrapper that calculates emotion intensity from
/// valence and arousal, then applies the standard personalized decay formula.
///
/// # Arguments
/// * `base_lambda` - Base decay coefficient
/// * `valence` - Valence value (-1.0 to 1.0)
/// * `arousal` - Arousal value (0.0 to 1.0)
/// * `activation_score` - Current activation score [0.0, 1.0]
/// * `hyperedge_count` - Number of hyperedges connected to this memory
///
/// # Returns
/// Personalized decay lambda with emotional boost applied
pub fn personalized_decay_lambda_from_emotion(
    base_lambda: f32,
    valence: f64,
    arousal: f64,
    activation_score: f32,
    hyperedge_count: u16,
) -> f32 {
    // Apply emotional boost using valence and arousal
    let lambda_after_emotion = apply_emotional_boost(base_lambda, valence, arousal);

    // Then apply recall and connectivity boosts
    let recall_boost = activation_score * 1.5;
    let connectivity_boost = (hyperedge_count.min(5) as f32) * 0.3;

    let divisor = 1.0 + recall_boost + connectivity_boost;
    lambda_after_emotion / divisor
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_neutral_memory_decay() {
        // Neutral emotion, no recent recalls, no connections
        let lambda = personalized_decay_lambda(0.001, 0.0, 0.0, 0);
        assert!((lambda - 0.001).abs() < 1e-6);
    }

    #[test]
    fn test_emotional_memory_slower_decay() {
        // High emotion intensity should reduce decay rate
        let lambda = personalized_decay_lambda(0.001, 1.0, 0.0, 0);
        assert!(lambda < 0.001);
        // emotional_boost = 2.0, divisor = 3.0
        assert!((lambda - 0.001 / 3.0).abs() < 1e-6);
    }

    #[test]
    fn test_connected_memory_slower_decay() {
        // Many connections should reduce decay rate
        let lambda = personalized_decay_lambda(0.001, 0.0, 0.0, 10);
        assert!(lambda < 0.001);
        // min(10, 5) * 0.3 = 1.5, divisor = 2.5
        assert!((lambda - 0.001 / 2.5).abs() < 1e-6);
    }

    #[test]
    fn test_recently_recalled_memory() {
        // Recently recalled memory should decay slower
        let lambda = personalized_decay_lambda(0.001, 0.0, 0.8, 0);
        assert!(lambda < 0.001);
        // recall_boost = 1.2, divisor = 2.2
        assert!((lambda - 0.001 / 2.2).abs() < 1e-6);
    }

    #[test]
    fn test_combined_boosts() {
        // All boosts combined
        let lambda = personalized_decay_lambda(0.001, 0.5, 0.8, 3);
        // emotional = 1.0, recall = 1.2, connectivity = 0.9
        // divisor = 1 + 1.0 + 1.2 + 0.9 = 4.1
        let expected = 0.001 / 4.1;
        assert!((lambda - expected).abs() < 1e-6);
    }

    #[test]
    fn test_maximum_connectivity_boost() {
        // Connectivity boost capped at 5 edges
        let lambda1 = personalized_decay_lambda(0.001, 0.0, 0.0, 5);
        let lambda2 = personalized_decay_lambda(0.001, 0.0, 0.0, 100);
        // Both should have same connectivity_boost = 1.5
        assert!((lambda1 - lambda2).abs() < 1e-6);
    }

    #[test]
    fn test_zero_base_lambda() {
        // Edge case: zero base lambda
        let lambda = personalized_decay_lambda(0.0, 1.0, 1.0, 10);
        assert!((lambda - 0.0).abs() < 1e-6);
    }

    #[test]
    fn test_strong_emotional_memory() {
        // Strong emotion + high activation + many connections
        let lambda = personalized_decay_lambda(0.001, 1.0, 1.0, 5);
        // emotional = 2.0, recall = 1.5, connectivity = 1.5
        // divisor = 1 + 2.0 + 1.5 + 1.5 = 6.0
        let expected = 0.001 / 6.0;
        assert!((lambda - expected).abs() < 1e-6);
        // Should decay 6x slower than base
        assert!(lambda < 0.001 / 5.0);
    }

    #[test]
    fn test_decay_from_valence_arousal_joy() {
        // Joy: high valence, high arousal
        let lambda = personalized_decay_lambda_from_emotion(0.001, 0.8, 0.7, 0.5, 2);
        assert!(lambda < 0.001); // Should decay slower due to emotion
    }

    #[test]
    fn test_decay_from_valence_arousal_neutral() {
        // Neutral: low valence, low arousal
        let lambda = personalized_decay_lambda_from_emotion(0.001, 0.0, 0.1, 0.0, 0);
        // Minimal emotional boost, should be close to base
        assert!((lambda - 0.001).abs() < 0.0005);
    }

    #[test]
    fn test_decay_from_valence_arousal_sadness() {
        // Sadness: negative valence, low arousal
        let lambda = personalized_decay_lambda_from_emotion(0.001, -0.6, 0.2, 0.3, 1);
        assert!(lambda < 0.001); // Should still have some emotional boost
    }
}
