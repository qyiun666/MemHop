// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"context"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// UpdateL0 owns only the host-authored half: the fields Dream evolves survive
// a host edit that never mentions them, and the timestamp is the library's
// rather than whatever the caller sent.
func TestUpdateL0KeepsDistilledHalf(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	seed := &core.ProfileSlot{
		Name:         "seed",
		Role:         "assistant",
		Personality:  "curious",
		EmotionState: core.EmotionScore{Valence: 0.4, Arousal: 0.2, Dominance: 0.6},
		MBTI:         core.MBTIScore{IE: 0.3, NS: 0.5, TF: 0.1, JP: 0.7, Type: "INTP"},
	}
	if err := db.UpdateL0(core.DefaultAgentID, seed); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	first, err := db.GetL0(core.DefaultAgentID)
	if err != nil {
		t.Fatalf("read seeded profile: %v", err)
	}
	if first.UpdatedAtMs == 0 {
		t.Fatal("UpdateL0 must stamp UpdatedAtMs")
	}

	if err := db.UpdateL0(core.DefaultAgentID, &core.ProfileSlot{
		Name:        "renamed",
		Preferences: map[string]string{"tone": "terse"},
		UpdatedAtMs: 7,
	}); err != nil {
		t.Fatalf("host edit: %v", err)
	}
	got, err := db.GetL0(core.DefaultAgentID)
	if err != nil {
		t.Fatalf("read edited profile: %v", err)
	}
	if got.Name != "renamed" || got.Preferences["tone"] != "terse" {
		t.Fatalf("host fields not written: %+v", got)
	}
	if got.EmotionState.Valence != 0.4 || got.MBTI.Type != "INTP" {
		t.Fatalf("distilled half wiped by a host edit: %+v", got)
	}
	if got.UpdatedAtMs == 7 || got.UpdatedAtMs < first.UpdatedAtMs {
		t.Fatalf("UpdatedAtMs must be stamped by the library, got %d", got.UpdatedAtMs)
	}
}
func TestDistillL0Entry(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))

	// No samples yet: the entry succeeds without an LLM call (skip guard),
	// for both the DB and the Session entry points.
	if err := db.DistillL0(context.Background(), core.DefaultAgentID); err != nil {
		t.Fatalf("db entry on empty domain: %v", err)
	}
	sess, err := db.NewSession(core.DefaultAgentID)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	if err := sess.DistillL0(context.Background()); err != nil {
		t.Fatalf("session entry on empty domain: %v", err)
	}

	// A closed database must be rejected, not silently distilled.
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := sess.DistillL0(context.Background()); common.CodeOf(err) != common.ErrClosed {
		t.Fatalf("want ErrClosed after Close, got %v", err)
	}
}
