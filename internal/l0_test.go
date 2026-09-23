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
		MBTI:         core.MBTIScore{IE: 0.3, NS: 0.5, TF: 0.1, JP: 0.7, Type: "ESFP"},
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
	if got.EmotionState.Valence != 0.4 || got.MBTI.Type != "ESFP" {
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

// The host's half of the profile is written whole, not merged: a call that names one
// preference drops the others, and one that omits Role clears it. That is the price of
// being able to delete anything at all — a per-key merge would leave a preference with no
// way to go away — so it is pinned as the contract rather than left as a surprise, with
// the read-merge-write path that follows from it.
func TestUpdateL0WritesTheHostHalfWhole(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	seed := &core.ProfileSlot{
		Name: "cat", Role: "assistant", Personality: "steady",
		Preferences:  map[string]string{"language": "zh", "tone": "terse"},
		EmotionState: core.EmotionScore{Valence: 0.4, Arousal: 0.2, Dominance: 0.6},
		MBTI:         core.MBTIScore{IE: 0.3, NS: 0.5, TF: 0.1, JP: 0.7, Type: "ESFP"},
	}
	if err := db.UpdateL0(core.DefaultAgentID, seed); err != nil {
		t.Fatalf("seed profile: %v", err)
	}

	// One preference named, nothing else but the required Name: the rest of the host half goes.
	if err := db.UpdateL0(core.DefaultAgentID, &core.ProfileSlot{
		Name: "cat", Preferences: map[string]string{"language": "en"},
	}); err != nil {
		t.Fatalf("host edit: %v", err)
	}
	got, err := db.GetL0(core.DefaultAgentID)
	if err != nil {
		t.Fatalf("read after the edit: %v", err)
	}
	if len(got.Preferences) != 1 || got.Preferences["language"] != "en" {
		t.Fatalf("Preferences survived the edit as %+v, want exactly the one this write named", got.Preferences)
	}
	if got.Role != "" || got.Personality != "" {
		t.Fatalf("the host half merged instead of replaced: role=%q personality=%q, want both cleared",
			got.Role, got.Personality)
	}
	if got.EmotionState.Valence != 0.4 || got.MBTI.Type != "ESFP" {
		t.Fatalf("the distilled half has no host in it: %+v", got)
	}

	// The path a host actually needs: read the table, change it, write the whole thing back.
	// That is how a single preference gets deleted.
	remaining := map[string]string{}
	for k, v := range got.Preferences {
		remaining[k] = v
	}
	delete(remaining, "language")
	if err := db.UpdateL0(core.DefaultAgentID, &core.ProfileSlot{
		Name: got.Name, Role: got.Role, Personality: got.Personality, Preferences: remaining,
	}); err != nil {
		t.Fatalf("write the merged table back: %v", err)
	}
	after, err := db.GetL0(core.DefaultAgentID)
	if err != nil {
		t.Fatalf("read after the deletion: %v", err)
	}
	if len(after.Preferences) != 0 {
		t.Fatalf("a preference could not be removed by the read-merge-write path: %+v", after.Preferences)
	}

	// Naming no table at all is the same answer as naming an empty one.
	if err := db.UpdateL0(core.DefaultAgentID, &core.ProfileSlot{Name: "cat"}); err != nil {
		t.Fatalf("write without a table: %v", err)
	}
	if last, err := db.GetL0(core.DefaultAgentID); err != nil || len(last.Preferences) != 0 {
		t.Fatalf("a nil table read back as %+v (err %v), want no preferences", last.Preferences, err)
	}
}
