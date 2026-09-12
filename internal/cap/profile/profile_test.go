// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package profile

import (
	"strings"
	"testing"

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
