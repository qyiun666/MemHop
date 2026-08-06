// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package model

// L0 agent画像
type ProfileSlot struct {
	IDHash          uint64            `json:"id_hash"`          //agent唯一标识
	Name            string            `json:"name"`             // agent名称
	Role            string            `json:"role"`             // agent角色
	Personality     string            `json:"personality"`      // agent人格
	Preferences     map[string]string `json:"preferences"`      // agent偏好
	Lexicon         map[string]string `json:"lexicon"`          // agent词汇表
	StyleTraits     []string          `json:"style_traits"`     // agent风格特征
	EmotionPatterns map[string]string `json:"emotion_patterns"` // agent情感模式
}
