// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
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

// A profile that cannot be decoded is not a profile that was never written.
// UpdateL0 inherits the distilled half from the stored record, so reading an
// unreadable payload as "absent" would let a host edit claim the whole slot and
// drop emotion, MBTI and the domain's agent type — the one write this layer must
// refuse rather than guess through.
func TestUnreadableProfileIsNotAbsentProfile(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	if err := db.UpdateL0(core.DefaultAgentID, &core.ProfileSlot{
		Name: "keeper", Personality: "steady",
	}); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	const corrupt = `{"name":`
	if _, err := db.engine.WriteRecord(core.DefaultAgentID, core.RecL0Profile,
		common.HashID("profile"), []byte(corrupt)); err != nil {
		t.Fatalf("replace the payload with an undecodable one: %v", err)
	}

	if _, err := db.GetL0(core.DefaultAgentID); common.CodeOf(err) != common.ErrDeserialization {
		t.Fatalf("GetL0 on an undecodable profile: want ErrDeserialization, got %v", err)
	}
	if err := db.UpdateL0(core.DefaultAgentID, &core.ProfileSlot{Name: "intruder"}); common.CodeOf(err) != common.ErrDeserialization {
		t.Fatalf("UpdateL0 must abort instead of rewriting a record it could not read, got %v", err)
	}
	// The refused write left the unreadable record where it was: a following read
	// still fails the same way, rather than finding the edit that was refused.
	if _, err := db.GetL0(core.DefaultAgentID); common.CodeOf(err) != common.ErrDeserialization {
		t.Fatalf("an aborted UpdateL0 must leave no trace, got %v", err)
	}
}

// All three entries that write a profile require a name, because the name is how
// a domain is addressed at all. UpdateL0 is the one that can clear it, so it
// refuses a blank the way Open and SubAgent do rather than storing an
// unaddressable profile.
func TestUpdateL0RequiresName(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	if err := db.UpdateL0(core.DefaultAgentID, &core.ProfileSlot{
		Name: "keeper", Personality: "steady",
	}); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	if err := db.UpdateL0(core.DefaultAgentID, &core.ProfileSlot{
		Name: "   ", Personality: "nameless",
	}); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("want ErrInvalidQuery for a blank name, got %v", err)
	}
	got, err := db.GetL0(core.DefaultAgentID)
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}
	if got.Name != "keeper" || got.Personality != "steady" {
		t.Fatalf("the refused edit must have written nothing, got %+v", got)
	}
}
