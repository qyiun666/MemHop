// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L0 ProfileSlot — agent identity (profile.rs).

package model

// ProfileSlot holds the L0 agent profile: personality, vocabulary, and
// emotional patterns. Extended fields (lexicon, style_traits, emotion_patterns)
// capture user language habits.
type ProfileSlot struct {
	IDHash          uint64            `json:"id_hash"`
	Name            string            `json:"name"`
	Role            string            `json:"role"`
	Personality     string            `json:"personality"`
	Worldview       string            `json:"worldview"`
	Preferences     map[string]string `json:"preferences"`
	Lexicon         map[string]string `json:"lexicon"`
	StyleTraits     []string          `json:"style_traits"`
	EmotionPatterns map[string]string `json:"emotion_patterns"`
	CreatedAt       int64             `json:"created_at"`
	UpdatedAt       int64             `json:"updated_at"`
	Version         uint32            `json:"version"`
}
