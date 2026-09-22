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

// briefFieldMaxRunes is this digest's own budget for one free-text field: Name,
// Role, Personality and preference keys are the host's text and arrive at any
// length, and this string rides with every LLM call.
const briefFieldMaxRunes = 160

// Brief renders a compact profile digest for prompt injection: identity,
// personality, MBTI, top preferences and the current emotional state. Every field it
// carries is bounded — one budget per free-text value, five preferences — because a
// digest rides along with every call. An all-empty slot renders as the empty string,
// and an all-zero emotional state is left out with it: 0/0/0 is the extreme reading
// "very negative, calm, submissive" on this scale, but it is also what a profile
// nothing was ever distilled onto carries, and the digest cannot tell the two apart.
func Brief(slot core.ProfileSlot) string {
	if slot.Name == "" && slot.Role == "" && slot.Personality == "" &&
		slot.MBTI.Type == "" && len(slot.Preferences) == 0 &&
		slot.EmotionState == (core.EmotionScore{}) {
		return ""
	}
	var b strings.Builder
	if slot.Name != "" {
		fmt.Fprintf(&b, "name: %s\n", truncateRunes(slot.Name, briefFieldMaxRunes))
	}
	if slot.Role != "" {
		fmt.Fprintf(&b, "role: %s\n", truncateRunes(slot.Role, briefFieldMaxRunes))
	}
	if slot.Personality != "" {
		fmt.Fprintf(&b, "personality: %s\n", truncateRunes(slot.Personality, briefFieldMaxRunes))
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

// writeKV writes up to max sorted key=value pairs of m into b: map iteration order is
// random, so keys are sorted for a stable digest. Both halves are truncated.
func writeKV(b *strings.Builder, m map[string]string, max int) {
	keys := slices.Sorted(maps.Keys(m))
	for i, k := range keys {
		if i == max {
			break
		}
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(b, "%s=%s", truncateRunes(k, briefFieldMaxRunes), truncateRunes(m[k], 120))
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
