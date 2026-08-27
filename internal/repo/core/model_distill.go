// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Distillation shapes shared by the LLM capability that produces them, the
// profile capability that consumes them and the record layer that stores
// them. Pure data, no behavior (G-01 bottom layer).

package core

// EmotionScore is the VAD emotion estimate of one distillation round.
type EmotionScore struct {
	Valence   float64 `json:"valence"`
	Arousal   float64 `json:"arousal"`
	Dominance float64 `json:"dominance"`
}

// MBTIScore holds four MBTI dimensions in [-1,1]; Type is derived from the
// dimensions.
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

// DistillSample is one L1 node prepared for the distillation prompt: its
// keywords, importance and last update.
type DistillSample struct {
	IDHash     uint64
	Keywords   []string
	Importance float32
	UpdatedAt  int64
}
