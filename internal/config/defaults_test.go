// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package config

import (
	"math"
	"testing"
	"time"
)

// The vocabulary three of the four knobs share is one sentence: 0 means "not filled" and
// the library default answers, a negative means "off". Each case below is a host that wrote
// part of the table and left the rest alone. ContentRetentionMs has no off spelling, so it is
// the one knob Validate checks rather than folding (see TestValidateRefuses...).
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
	// "no target", so a negative is folded onto it rather than sent to the model. The
	// retention window below is not an off spelling — a host never reaches this fold with
	// a negative, because Validate refuses it at the entry point; the value shown is what an
	// internal caller hand-building a struct gets, which matches dream's unconfigured answer.
	want := MemHopDefaults{
		SceneDreamTopicThreshold: -1, DreamCompressMinTopics: 0, AgentIdleTTLMs: -1,
		ContentRetentionMs: DefaultMemHopDefaults.ContentRetentionMs,
	}
	if got != want {
		t.Fatalf("off spellings normalized to %+v, want %+v", got, want)
	}
}

// A retention window is the one knob whose wrong value deletes memory instead of just
// failing to be honoured, so the refusal is the whole point and the ceiling is not an
// arbitrary number: measured on this machine, MaxContentRetentionMs still lands the
// cutoff two centuries in the past, while one millisecond more wraps the duration
// around and puts the cutoff in the future — which reads every record as expired.
func TestValidateRefusesWindowsTheSweepCannotRepresent(t *testing.T) {
	for _, ms := range []int64{-1, math.MinInt64, MaxContentRetentionMs + 1, math.MaxInt64} {
		if err := (MemHopDefaults{ContentRetentionMs: ms}).Validate(); err == nil {
			t.Fatalf("content_retention_ms %d was accepted", ms)
		}
	}
	for _, ms := range []int64{0, 1, DefaultMemHopDefaults.ContentRetentionMs, MaxContentRetentionMs} {
		if err := (MemHopDefaults{ContentRetentionMs: ms}).Validate(); err != nil {
			t.Fatalf("content_retention_ms %d refused: %v", ms, err)
		}
	}

	now := time.Now()
	oldest := time.Duration(MaxContentRetentionMs) * time.Millisecond
	if oldest <= 0 || !now.Add(-oldest).Before(now) {
		t.Fatalf("the accepted ceiling no longer lands in the past: %s", oldest)
	}
	// One millisecond past the ceiling has to be the hazard itself, not a slightly bigger
	// window: the multiplication wraps, so the cutoff lands in the future and every record
	// in the domain reads as expired. If this stops failing, the ceiling moved and needs
	// re-deriving — it is not a number to edit.
	past := int64(MaxContentRetentionMs) + 1
	cutoff := now.Add(-time.Duration(past) * time.Millisecond)
	if cutoff.Before(now) {
		t.Fatalf("one past the ceiling still measures as a past window (%s): the ceiling is no longer the overflow boundary", cutoff.Sub(now))
	}
}
