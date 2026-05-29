//! Vitality system — decay, interference, and reconsolidation formulas for MemHop.

#![allow(dead_code)]

use std::time::{SystemTime, UNIX_EPOCH};

/// Context for decay computations, pre-computed by the caller.
///
/// Fields:
/// - `hours_since_last_activated`: how long (in hours) since this memory was last accessed.
/// - `recent_similar`: cosine similarities to competing memories.
/// - `lambda`: from [`Personality::decay_lambda`](crate::types::Personality::decay_lambda).
/// - `interference_alpha`: from [`Personality::interference_alpha`](crate::types::Personality::interference_alpha).
/// - `arousal_beta`: from [`Personality::arousal_beta`](crate::types::Personality::arousal_beta).
pub struct DecayContext {
    pub hours_since_last_activated: f64,
    pub recent_similar: Vec<f32>,
    pub lambda: f32,
    pub interference_alpha: f32,
    pub arousal_beta: f32,
}

/// Sigmoid compression of an interference signal into `[0, 1]`.
///
/// The formula is a shifted sigmoid:
/// ```text
/// 1.0 / (1.0 + e^(interference - 0.5))
/// ```
///
/// - `interference = 0` → ≈0.62 (low interference → weak decay pressure)
/// - `interference = 1` → ≈0.38 (moderate interference)
/// - As `interference → ∞` the factor approaches 0 asymptotically.
pub fn interference_decay_factor(interference: f32) -> f32 {
    1.0 / (1.0 + (interference - 0.5).exp())
}

/// Compute the new vitality for a memory after a decay interval.
///
/// The weighted decay formula blends four factors:
///
/// | Component | Weight | Source |
/// |-----------|--------|--------|
/// | Time-based decay | 25 % | `exp(-λ · √hours)` |
/// | Interference | 60 % | Sigmoid-compressed sum of similar competing memories |
/// | Arousal protection | variable | `-β · arousal` (subtracted, so high arousal → less decay) |
/// | Activation protection | ≤15 % | `-0.05 · ln(1 + activation_count)`, capped at 0.15 |
///
/// The final decay rate is clamped to `[0.0, 0.95]` and the resulting vitality
/// is clamped to `[0.0, 1.0]`.
pub fn compute_vitality(
    vitality: f32,
    arousal: f32,
    activation_count: u32,
    _last_activated: i64,
    ctx: &DecayContext,
    kind_decay_scale: f32,
) -> f32 {
    // ── 1. Time-based decay ────────────────────────────────────────
    let hours = ctx.hours_since_last_activated as f32;
    let time_factor = (-ctx.lambda * hours.sqrt()).exp();

    // ── 2. Interference from competing memories ────────────────────
    //     Only similarities > 0.7 contribute.
    let interference_sum: f32 = ctx
        .recent_similar
        .iter()
        .filter(|&&sim| sim > 0.7)
        .map(|&sim| sim * ctx.interference_alpha)
        .sum();
    let interference_factor = interference_decay_factor(interference_sum);

    // ── 3. Activation-count protection ─────────────────────────────
    let activation_penalty = (0.05 * (1.0 + activation_count as f32).ln()).min(0.15);

    // ── 4. Weighted decay rate ─────────────────────────────────────
    let decay_rate = 0.25 * (1.0 - time_factor)
        + 0.60 * (1.0 - interference_factor)
        - ctx.arousal_beta * arousal
        - activation_penalty;

    // v0.11.0: Scale by kind-specific decay rate (Knowledge decays slower)
    let decay_rate = (decay_rate * kind_decay_scale).clamp(0.0, 0.95);
    (vitality * (1.0 - decay_rate)).clamp(0.0, 1.0)
}

/// Reconsolidate a memory upon access — boost vitality and update metadata.
///
/// The boost is larger for memories with low vitality:
/// ```text
/// boost = 0.05 + 0.15 × (1.0 - vitality)
/// ```
///
/// This implements the reconsolidation effect: the more faded a memory is,
/// the more it is strengthened by being re-accessed.
pub fn reconsolidate(vitality: &mut f32, activation_count: &mut u32, last_activated: &mut i64) {
    let boost = 0.05 + 0.15 * (1.0 - *vitality);
    *vitality = (*vitality + boost).min(1.0);
    *activation_count += 1;
    *last_activated = now_millis();
}

/// Current time in milliseconds since the Unix epoch.
fn now_millis() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .expect("Time went backwards")
        .as_millis() as i64
}

// ── Tests ──────────────────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;

    // ── interference_decay_factor ───────────────────────────────────

    #[test]
    fn test_interference_decay_expected_values() {
        // interference = 0  → sigmoid(-0.5) ≈ 0.62
        let f0 = interference_decay_factor(0.0);
        assert!(
            (f0 - 0.62).abs() < 0.01,
            "expected ~0.62 for interference=0, got {}",
            f0
        );

        // interference = 1  → sigmoid(0.5) ≈ 0.38
        let f1 = interference_decay_factor(1.0);
        assert!(
            (f1 - 0.38).abs() < 0.01,
            "expected ~0.38 for interference=1, got {}",
            f1
        );

        // Very large interference → approaches 0
        let f_large = interference_decay_factor(10.0);
        assert!(f_large > 0.0, "must still be positive");
        assert!(f_large < 0.01, "expected near 0, got {}", f_large);

        // Negative interference → approaches 1
        let f_neg = interference_decay_factor(-10.0);
        assert!(f_neg > 0.99, "expected near 1 for -10, got {}", f_neg);
    }

    #[test]
    fn test_interference_decay_monotonic() {
        // The function should be strictly decreasing
        let mut prev = interference_decay_factor(-1.0);
        for i in 0..=20 {
            let x = i as f32 * 0.5;
            let cur = interference_decay_factor(x);
            assert!(
                cur <= prev,
                "must be non-increasing at x={}, prev={}, cur={}",
                x,
                prev,
                cur
            );
            prev = cur;
        }
    }

    // ── compute_vitality ────────────────────────────────────────────

    /// Helper to build a default DecayContext for testing.
    fn ctx_basic() -> DecayContext {
        DecayContext {
            hours_since_last_activated: 1.0,
            recent_similar: vec![0.5, 0.8, 0.3],
            lambda: 0.02,
            interference_alpha: 0.1,
            arousal_beta: 0.3,
        }
    }

    #[test]
    fn test_compute_vitality_clamped_to_unit_interval() {
        let ctx = ctx_basic();

        // Typical case — should stay well within [0, 1]
        let result = compute_vitality(0.8, 0.3, 5, 1700000000000, &ctx, 1.0);
        assert!(
            (0.0..=1.0).contains(&result),
            "vitality must be in [0, 1], got {}",
            result
        );
    }

    #[test]
    fn test_compute_vitality_lower_with_high_interference() {
        let mut ctx = ctx_basic();
        ctx.recent_similar = vec![0.95, 0.92, 0.88, 0.85]; // high similarity → more interference
        ctx.interference_alpha = 0.5; // high sensitivity to interference

        let quiet = compute_vitality(0.8, 0.3, 5, 1700000000000, &ctx_basic(), 1.0);
        let noisy = compute_vitality(0.8, 0.3, 5, 1700000000000, &ctx, 1.0);

        assert!(
            noisy <= quiet,
            "high interference should not increase vitality (quiet={}, noisy={})",
            quiet,
            noisy
        );
    }

    #[test]
    fn test_compute_vitality_higher_with_more_activation() {
        let ctx = ctx_basic();

        let low_act = compute_vitality(0.5, 0.3, 0, 1700000000000, &ctx, 1.0);
        let high_act = compute_vitality(0.5, 0.3, 100, 1700000000000, &ctx, 1.0);

        assert!(
            high_act >= low_act,
            "more activation should not decrease vitality (low={}, high={})",
            low_act,
            high_act
        );
    }

    #[test]
    fn test_compute_vitality_extremes() {
        let ctx = ctx_basic();

        // Very low vitality — should not go below 0
        let low = compute_vitality(0.01, 0.0, 0, 1700000000000, &ctx, 1.0);
        assert!(low >= 0.0, "vitality should never go below 0, got {}", low);

        // Very high vitality — should not exceed 1
        let high = compute_vitality(1.0, 1.0, 1000, 1700000000000, &ctx, 1.0);
        assert!(
            high <= 1.0,
            "vitality should never exceed 1, got {}",
            high
        );
    }

    // ── reconsolidate ───────────────────────────────────────────────

    #[test]
    fn test_reconsolidate_boost_higher_for_low_vitality() {
        let mut v_low = 0.2;
        let mut c_low = 0;
        let mut t_low = 1000;
        reconsolidate(&mut v_low, &mut c_low, &mut t_low);
        let boost_low = v_low - 0.2;

        let mut v_high = 0.9;
        let mut c_high = 0;
        let mut t_high = 1000;
        reconsolidate(&mut v_high, &mut c_high, &mut t_high);
        let boost_high = v_high - 0.9;

        assert!(
            boost_low > boost_high,
            "low vitality boost ({}) should exceed high vitality boost ({})",
            boost_low,
            boost_high
        );
    }

    #[test]
    fn test_reconsolidate_updates_metadata() {
        let mut vitality = 0.5;
        let mut activation_count = 10;
        let mut last_activated = 1000;

        reconsolidate(&mut vitality, &mut activation_count, &mut last_activated);

        assert_eq!(activation_count, 11, "activation_count should increment");
        assert!(
            last_activated > 1000,
            "last_activated should update to current time"
        );
        assert!(vitality > 0.5, "vitality should increase");
    }

    #[test]
    fn test_reconsolidate_never_exceeds_one() {
        let mut vitality = 0.95;
        let mut activation_count = 0;
        let mut last_activated = 1000;

        // Multiple reconsolidations should not push vitality past 1.0
        for _ in 0..10 {
            reconsolidate(&mut vitality, &mut activation_count, &mut last_activated);
        }

        assert!(
            vitality <= 1.0,
            "vitality must never exceed 1.0, got {}",
            vitality
        );
        assert_eq!(activation_count, 10);
    }
}
