// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package profile

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/qyiun666/MemHop/internal/repo/core"
)

func TestBriefEmptyProfile(t *testing.T) {
	if got := Brief(core.ProfileSlot{}); got != "" {
		t.Fatalf("empty profile must render nothing, got %q", got)
	}
}

func TestBriefRendersEveryTrack(t *testing.T) {
	slot := core.ProfileSlot{
		Name:         "小Mem",
		Role:         "assistant",
		Personality:  "concise and technical",
		MBTI:         core.MBTIScore{Type: "INTJ"},
		Preferences:  map[string]string{"lang": "zh", "tone": "warm"},
		EmotionState: core.EmotionScore{Valence: 0.8},
	}
	got := Brief(slot)
	for _, want := range []string{"name: 小Mem\n", "role: assistant\n", "personality: concise and technical\n",
		"mbti: INTJ\n", "lang=zh", "tone=warm", "emotions: valence=0.80"} {
		if !strings.Contains(got, want) {
			t.Fatalf("digest missing %q:\n%s", want, got)
		}
	}
	// Preferences render key-sorted for a stable digest.
	if i, j := strings.Index(got, "lang=zh"), strings.Index(got, "tone=warm"); i > j {
		t.Fatalf("preferences must be key-sorted:\n%s", got)
	}
}

func TestBriefTruncatesLongValues(t *testing.T) {
	long := strings.Repeat("字", 200)
	for _, tc := range []struct {
		name string
		slot core.ProfileSlot
	}{
		{"preference value", core.ProfileSlot{Preferences: map[string]string{"k": long}}},
		{"preference key", core.ProfileSlot{Preferences: map[string]string{long: "v"}}},
		{"name", core.ProfileSlot{Name: long}},
		{"role", core.ProfileSlot{Role: long}},
		{"personality", core.ProfileSlot{Personality: long}},
	} {
		got := Brief(tc.slot)
		if !strings.Contains(got, "…") || strings.Contains(got, long) {
			t.Fatalf("%s: long text must be rune-truncated with ellipsis:\n%.200s", tc.name, got)
		}
	}
}

// The digest is promised as "bounded", so the bound has to be a number a host can hold the
// library to rather than an adjective: each free-text field capped, exactly the five lowest
// preference keys present, in key order — the same profile has to inject the same bytes
// every turn — and the whole thing under briefWorstCaseRunes.
func TestBriefIsBoundedAndRepeatable(t *testing.T) {
	long := strings.Repeat("记", 400)
	// Keys must stay distinguishable after truncation, or a fixture of identical prefixes
	// hides exactly the drift this checks: one key is long enough to be capped (and sorts
	// first because it starts with 'A'), the rest are short and ordered.
	longKey := strings.Repeat("A", briefFieldMaxRunes+40)
	prefs := map[string]string{longKey: strings.Repeat("v", 300)}
	for i := 0; i < 40; i++ {
		prefs[fmt.Sprintf("k%02d", i)] = strings.Repeat("v", 300)
	}
	slot := core.ProfileSlot{
		Name: long, Role: long, Personality: long,
		MBTI:         core.MBTIScore{IE: 0.4, NS: 0.4, TF: 0.4, JP: 0.4, Type: "ESTP"},
		Preferences:  prefs,
		EmotionState: core.EmotionScore{Valence: 0.8, Arousal: 0.6, Dominance: 0.4},
	}
	first := Brief(slot)
	if first != Brief(slot) {
		t.Fatal("the same profile digested twice differently, so per-turn injection churns")
	}

	lines := strings.Split(strings.TrimRight(first, "\n"), "\n")
	if len(lines) != 6 {
		t.Fatalf("digest has %d lines, want the six tracks it renders: %q", len(lines), lines)
	}
	for _, line := range lines[:3] {
		text := line[strings.Index(line, ": ")+2:]
		if n := utf8.RuneCountInString(text); n > briefFieldMaxRunes+1 {
			t.Errorf("%q is %d runes, over the field cap of %d plus the ellipsis", line[:20], n, briefFieldMaxRunes)
		}
		if !strings.HasSuffix(text, "…") {
			t.Errorf("%q… was not marked as truncated", line[:20])
		}
	}
	pairLine := lines[4]
	if !strings.HasPrefix(pairLine, "preferences: ") {
		t.Fatalf("line 5 is %q, want the preference digest", pairLine[:20])
	}
	pairs := strings.Split(strings.TrimPrefix(pairLine, "preferences: "), ", ")
	if len(pairs) != 5 {
		t.Fatalf("the digest carries %d preferences, want the stated cap of 5", len(pairs))
	}
	if !slices.IsSorted(pairs) {
		t.Error("preferences come out unsorted, so the same profile injects different bytes")
	}
	wantKeys := []string{longKey[:briefFieldMaxRunes] + "…", "k00", "k01", "k02", "k03"}
	for i, pair := range pairs {
		got, value, _ := strings.Cut(pair, "=")
		if got != wantKeys[i] {
			t.Fatalf("pair %d is keyed %q, want the %dth lowest key %q", i, got[:8], i+1, wantKeys[i][:8])
		}
		if utf8.RuneCountInString(value) > 121 {
			t.Errorf("preference value for %q is %d runes, over the 120-rune cap", got[:4], utf8.RuneCountInString(value))
		}
	}
	if got := utf8.RuneCountInString(first); got > briefWorstCaseRunes {
		t.Errorf("the digest reached %d runes, over the %d ceiling it promises", got, briefWorstCaseRunes)
	} else {
		t.Logf("worst-case digest measured at %d runes, ceiling %d", got, briefWorstCaseRunes)
	}
}
