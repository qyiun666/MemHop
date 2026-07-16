// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package dream

import "math"

// EmotionType classifies emotion by valence/arousal quadrant.
type EmotionType uint8

const (
	EmotionJoy     EmotionType = 0
	EmotionSadness EmotionType = 1
	EmotionAnger   EmotionType = 2
	EmotionFear    EmotionType = 3
	EmotionSurprise EmotionType = 4
	EmotionDisgust EmotionType = 5
	EmotionNeutral EmotionType = 6
)

// ApplyEmotionalBoost adjusts decay lambda based on emotional intensity.
// High-intensity emotions reduce lambda (slower decay).
// The result is clamped to be non-negative to ensure importance only decays.
func ApplyEmotionalBoost(baseLambda float64, valence, arousal float64) float64 {
	intensity := math.Abs(valence) * arousal
	boost := intensity * 2.0
	result := baseLambda - boost
	if result < 0 {
		return 0
	}
	return result
}
