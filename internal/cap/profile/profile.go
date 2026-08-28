// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package profile is the L0 profile capability: rendering the compact
// profile digest injected into every Search response. It is a pure
// projection of the stored profile slot with its own size budget.
package profile

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/qyiun666/MemHop/internal/repo/core"
)

// Brief renders a compact profile digest for prompt injection: identity,
// personality, MBTI, top preferences and the current emotional state,
// bounded so the per-turn Search payload stays small. Hosts needing the
// full profile read it once via GetL0 instead of every turn.
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
