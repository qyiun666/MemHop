// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

/// Emotion type enumeration based on valence and arousal
#[derive(Debug, Clone, Copy, PartialEq)]
#[repr(u8)]
pub enum EmotionType {
    Joy = 0,
    Sadness = 1,
    Anger = 2,
    Fear = 3,
    Surprise = 4,
    Disgust = 5,
    Neutral = 6,
}

impl EmotionType {
    // Kept for future serialization / round-trip of persisted emotion tags.
    #[allow(dead_code)]
    pub fn from_u8(value: u8) -> Self {
        match value {
            0 => EmotionType::Joy,
            1 => EmotionType::Sadness,
            2 => EmotionType::Anger,
            3 => EmotionType::Fear,
            4 => EmotionType::Surprise,
            5 => EmotionType::Disgust,
            _ => EmotionType::Neutral,
        }
    }
}

/// Apply emotional boost to decay lambda
///
/// Emotional memories decay slower. The boost reduces the decay lambda,
/// making high-intensity emotions persist longer.
pub fn apply_emotional_boost(base_lambda: f32, valence: f64, arousal: f64) -> f32 {
    let intensity = valence.abs() * arousal;
    let boost = (intensity * 2.0) as f32;
    base_lambda - boost
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_apply_emotional_boost() {
        let base_lambda = 1.0;

        // High emotion intensity should reduce lambda (slower decay)
        let boosted = apply_emotional_boost(base_lambda, 0.8, 0.7);
        assert!(boosted < base_lambda);

        // Low emotion intensity should have minimal effect
        let low_boost = apply_emotional_boost(base_lambda, 0.1, 0.1);
        assert!((low_boost - base_lambda).abs() < 0.1);
    }

    #[test]
    fn test_emotion_type_from_u8() {
        assert_eq!(EmotionType::from_u8(0), EmotionType::Joy);
        assert_eq!(EmotionType::from_u8(1), EmotionType::Sadness);
        assert_eq!(EmotionType::from_u8(6), EmotionType::Neutral);
        assert_eq!(EmotionType::from_u8(99), EmotionType::Neutral);
    }
}
