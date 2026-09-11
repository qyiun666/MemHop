// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package profile is the L0 profile capability: the first profile a domain gets,
// a compact digest of a stored one, the distillation samples read out of L1, and
// writing a distillation result back. Every projection here carries its own size
// budget.
package profile

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/qyiun666/MemHop/internal/repo/core"
)

// Brief renders a compact profile digest for prompt injection: identity,
// personality, MBTI, top preferences and the current emotional state. It is
// bounded by construction — five preferences, each value truncated — because a
// digest rides along with every call and must not grow with the profile it came
// from. An all-empty slot renders as the empty string.
func Brief(slot core.ProfileSlot) string {
	if slot.Name == "" && slot.Role == "" && slot.Personality == "" &&
		slot.MBTI.Type == "" && len(slot.Preferences) == 0 &&
		slot.EmotionState == (core.EmotionScore{}) {
		return ""
	}
	var b strings.Builder
	if slot.Name != "" {
		fmt.Fprintf(&b, "name: %s\n", slot.Name)
	}
	if slot.Role != "" {
		fmt.Fprintf(&b, "role: %s\n", slot.Role)
	}
	if slot.Personality != "" {
		fmt.Fprintf(&b, "personality: %s\n", slot.Personality)
	}
	if slot.MBTI.Type != "" {
		fmt.Fprintf(&b, "mbti: %s\n", slot.MBTI.Type)
	}
	if len(slot.Preferences) > 0 {
		b.WriteString("preferences: ")
		writeKV(&b, slot.Preferences, 5)
		b.WriteByte('\n')
	}
	if slot.EmotionState != (core.EmotionScore{}) {
		fmt.Fprintf(&b, "emotions: valence=%.2f arousal=%.2f dominance=%.2f\n",
			slot.EmotionState.Valence, slot.EmotionState.Arousal, slot.EmotionState.Dominance)
	}
	return b.String()
}

// writeKV writes up to max sorted key=value pairs of m into b; map
// iteration order is random, so keys are sorted for a stable digest. Each
// value is truncated to keep the digest compact even for long inputs.
func writeKV(b *strings.Builder, m map[string]string, max int) {
	keys := slices.Sorted(maps.Keys(m))
	for i, k := range keys {
		if i == max {
			break
		}
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(b, "%s=%s", k, truncateRunes(m[k], 120))
	}
}

// truncateRunes caps s at n runes, appending "…" when truncated.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
