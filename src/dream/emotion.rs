// Emotion system for v0.33 - valence × arousal → emotion_type inference

/// Emotion type enumeration based on valence and arousal
#[derive(Debug, Clone, Copy, PartialEq)]
#[repr(u8)]
pub enum EmotionType {
    Joy = 0,      // valence > 0.3, arousal > 0.5
    Sadness = 1,  // valence < -0.3, arousal < 0.3
    Anger = 2,    // valence < -0.3, arousal > 0.5
    Fear = 3,     // valence < -0.2, arousal > 0.7
    Surprise = 4, // arousal > 0.8
    Disgust = 5,  // valence < -0.4
    Neutral = 6,  // 其他
}

impl EmotionType {
    /// Convert from u8 to EmotionType
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

/// Infer emotion type from valence and arousal values
///
/// # Arguments
/// * `valence` - Pleasure dimension (-1.0 to 1.0)
/// * `arousal` - Arousal dimension (0.0 to 1.0)
///
/// # Returns
/// EmotionType based on the combination of valence and arousal
pub fn infer_emotion(valence: f64, arousal: f64) -> EmotionType {
    // Priority-based evaluation (Surprise has highest priority)
    if arousal > 0.8 {
        return EmotionType::Surprise;
    }

    // Check positive emotions first
    if valence > 0.3 && arousal > 0.5 {
        return EmotionType::Joy;
    }

    // Check negative emotions based on valence and arousal
    if valence < -0.3 {
        if arousal < 0.3 {
            return EmotionType::Sadness;
        } else if arousal > 0.5 {
            return EmotionType::Anger;
        }
    }

    // Fear: moderate negative valence with high arousal
    if valence < -0.2 && arousal > 0.7 {
        return EmotionType::Fear;
    }

    // Disgust: strong negative valence
    if valence < -0.4 {
        return EmotionType::Disgust;
    }

    EmotionType::Neutral
}

/// Calculate emotion intensity as |valence| × arousal
///
/// Higher intensity means stronger emotional impact
pub fn calculate_emotion_intensity(valence: f64, arousal: f64) -> f64 {
    valence.abs() * arousal
}

/// Apply emotional boost to decay lambda
///
/// Emotional memories decay slower. The boost reduces the decay lambda,
/// making high-intensity emotions persist longer.
///
/// # Arguments
/// * `base_lambda` - Base decay rate
/// * `valence` - Valence value
/// * `arousal` - Arousal value
///
/// # Returns
/// Adjusted decay lambda (lower = slower decay)
pub fn apply_emotional_boost(base_lambda: f32, valence: f64, arousal: f64) -> f32 {
    let intensity = calculate_emotion_intensity(valence, arousal);
    let boost = (intensity * 2.0) as f32;
    // Emotional memories decay slower (lambda decreases)
    base_lambda - boost
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_infer_emotion_joy() {
        assert_eq!(infer_emotion(0.5, 0.7), EmotionType::Joy);
        assert_eq!(infer_emotion(0.8, 0.6), EmotionType::Joy);
    }

    #[test]
    fn test_infer_emotion_sadness() {
        assert_eq!(infer_emotion(-0.5, 0.2), EmotionType::Sadness);
        assert_eq!(infer_emotion(-0.8, 0.1), EmotionType::Sadness);
    }

    #[test]
    fn test_infer_emotion_anger() {
        assert_eq!(infer_emotion(-0.5, 0.7), EmotionType::Anger);
    }

    #[test]
    fn test_infer_emotion_fear() {
        assert_eq!(infer_emotion(-0.3, 0.8), EmotionType::Fear);
    }

    #[test]
    fn test_infer_emotion_surprise() {
        // High arousal takes priority
        assert_eq!(infer_emotion(0.0, 0.9), EmotionType::Surprise);
        assert_eq!(infer_emotion(-0.5, 0.9), EmotionType::Surprise);
    }

    #[test]
    fn test_infer_emotion_disgust() {
        assert_eq!(infer_emotion(-0.6, 0.5), EmotionType::Disgust);
        assert_eq!(infer_emotion(-0.8, 0.3), EmotionType::Disgust);
    }

    #[test]
    fn test_infer_emotion_neutral() {
        assert_eq!(infer_emotion(0.0, 0.3), EmotionType::Neutral);
        assert_eq!(infer_emotion(0.1, 0.4), EmotionType::Neutral);
    }

    #[test]
    fn test_emotion_intensity() {
        let intensity = calculate_emotion_intensity(0.8, 0.6);
        assert!((intensity - 0.48).abs() < 1e-6);

        let intensity_neg = calculate_emotion_intensity(-0.8, 0.6);
        assert!((intensity_neg - 0.48).abs() < 1e-6);
    }

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
