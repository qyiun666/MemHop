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

// Brief renders a compact profile digest for prompt injection: name,
// role, top preferences, style traits and emotion patterns, bounded so the
// per-turn Search payload stays small. Hosts needing the full profile read
// it once via GetL0 instead of every turn.
func Brief(slot core.ProfileSlot) string {
	if slot.Name == "" && slot.Role == "" && len(slot.Preferences) == 0 &&
		len(slot.StyleTraits) == 0 && len(slot.EmotionPatterns) == 0 {
		return ""
	}
	var b strings.Builder
	if slot.Name != "" {
		fmt.Fprintf(&b, "name: %s\n", slot.Name)
	}
	if slot.Role != "" {
		fmt.Fprintf(&b, "role: %s\n", slot.Role)
	}
	if len(slot.Preferences) > 0 {
		b.WriteString("preferences: ")
		writeKV(&b, slot.Preferences, 5)
		b.WriteByte('\n')
	}
	if len(slot.StyleTraits) > 0 {
		b.WriteString("style: ")
		b.WriteString(strings.Join(head3(slot.StyleTraits), ", "))
		b.WriteByte('\n')
	}
	if len(slot.EmotionPatterns) > 0 {
		b.WriteString("emotions: ")
		writeKV(&b, slot.EmotionPatterns, 3)
		b.WriteByte('\n')
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

func head3(s []string) []string {
	if len(s) <= 3 {
		return s
	}
	return s[:3]
}
