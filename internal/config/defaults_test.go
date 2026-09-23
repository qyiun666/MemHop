// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package config

import "testing"

// The vocabulary the four knobs share is one sentence: 0 means "not filled" and the
// library default answers, a negative means "off". Each case below is a host that wrote
// part of the table and left the rest alone.
func TestNormalizedTakesUnfilledKnobsAsDefaults(t *testing.T) {
	if got := (MemHopDefaults{}).Normalized(); got != DefaultMemHopDefaults {
		t.Fatalf("an all-zero table normalized to %+v, want %+v", got, DefaultMemHopDefaults)
	}
	// Half-filling is the common case: the untouched knobs still answer as defaults.
	half := MemHopDefaults{AgentIdleTTLMs: 60000}.Normalized()
	if half.AgentIdleTTLMs != 60000 || half.DreamCompressMinTopics != DefaultMemHopDefaults.DreamCompressMinTopics {
		t.Fatalf("half-filled table became %+v", half)
	}
}

func TestNormalizedKeepsWhatTheCallerFilled(t *testing.T) {
	in := MemHopDefaults{
		SceneDreamTopicThreshold: 5, DreamCompressMinTopics: 3,
		AgentIdleTTLMs: 1000, ContentRetentionMs: 2000,
	}
	if got := in.Normalized(); got != in {
		t.Fatalf("filled knobs were rewritten to %+v", got)
	}
}

func TestNormalizedOffSpellings(t *testing.T) {
	got := MemHopDefaults{
		SceneDreamTopicThreshold: -1, DreamCompressMinTopics: -1,
		AgentIdleTTLMs: -1, ContentRetentionMs: -1,
	}.Normalized()
	// The compress floor's off spelling is zero: that is the value the prompt renders as
	// "no target", so a negative is folded onto it rather than sent to the model.
	want := MemHopDefaults{
		SceneDreamTopicThreshold: -1, DreamCompressMinTopics: 0, AgentIdleTTLMs: -1,
		ContentRetentionMs: DefaultMemHopDefaults.ContentRetentionMs,
	}
	if got != want {
		t.Fatalf("off spellings normalized to %+v, want %+v", got, want)
	}
}
