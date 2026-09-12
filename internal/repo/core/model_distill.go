// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Distillation shapes shared by the LLM capability that produces them, the
// profile capability that consumes them and the record layer that stores
// them. Pure data, no behavior.

package core

// EmotionScore is the VAD emotion estimate of one distillation round. Each signal
// runs 0..1 with both ends meaningful: valence 0 = very negative, 0.5 = neutral,
// 1 = very positive; arousal 0 = calm, 1 = highly excited; dominance
// 0 = submissive, 1 = dominant.
type EmotionScore struct {
	Valence   float64 `json:"valence"`
	Arousal   float64 `json:"arousal"`
	Dominance float64 `json:"dominance"`
}

// MBTIScore holds four MBTI dimensions in [-1,1] — negative = I/N/T/J,
// positive = E/S/F/P, magnitude = strength. Type is derived from those four:
// an axis answered 0 carries no strength and reads as X, and four silent axes
// derive no type word at all.
type MBTIScore struct {
	IE   float64 `json:"i_e"`
	NS   float64 `json:"n_s"`
	TF   float64 `json:"t_f"`
	JP   float64 `json:"j_p"`
	Type string  `json:"type"`
}

// NodeEmotion is the per-L1-node emotion signal written back after a
// distillation round.
type NodeEmotion struct {
	Valence float64
	Arousal float64
}

// DistillSample is one L1 node prepared for the distillation prompt: the three
// fields the prompt renders. The node clock the ranking consumed stays on the
// record it came from — nothing reads it after the cut.
type DistillSample struct {
	IDHash     uint64
	Keywords   []string
	Importance float32
}
