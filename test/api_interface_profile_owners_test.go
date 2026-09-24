// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// The profile record is the second place where two owners share one row: the host writes
// Name / Role / Preferences through `UpdateL0`, and the consolidation's distillation stage writes
// the emotion, the MBTI axes and the personality summary back into the same record. The host's
// side of that bargain is documented — `UpdateL0` inherits the distilled fields so a profile
// edit never wipes what a pass learned — and the library's side of the same bargain was the mirror
// image of it, asserted nowhere: `MergeDistill` had no test reaching it at all.
//
// So this drives the only order a host can actually produce (settle rounds, edit the profile, let
// a consolidation run) and requires that the fields the pass does not own are still there —
// including the preferences map, which is the one field a rewrite that assembled a record from
// what it computed would drop without any call failing. The distilled half is asserted in the same
// read, because a pass that never wrote anything would leave this test satisfied by the fixture.

package test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	memhop "github.com/qyiun666/MemHop/api"
)

func TestInterfaceDistillLeavesTheHostsProfileFieldsAlone(t *testing.T) {
	llm := newMockLLM(t)
	path := filepath.Join(t.TempDir(), "profile_owners.meh")
	m := openMockDB(t, path, llm.srv.URL)
	db := newTestDB(t, m)

	sceneID := openSession(t, db)
	settleTurn(t, db, sceneID, "用户要求重构代码", "好的,我来重构这段代码")
	settleTurn(t, db, sceneID, "继续重构第二个模块", "第二个模块也补上测试")

	if err := db.UpdateL0(memhop.ProfileInput{
		Name: "重构那一域", Role: "管回归与重构",
		Preferences: map[string]string{"lang": "zh", "style": "先复现再动手"},
	}); err != nil {
		t.Fatalf("UpdateL0: %v", err)
	}

	rep, err := db.Dream(context.Background(), "")
	if err != nil {
		t.Fatalf("Dream: %v", err)
	}
	if !rep.L0Updated {
		t.Fatalf("the pass did not touch the profile (%+v), so nothing below is evidence", rep)
	}

	prof, err := db.GetL0()
	if err != nil {
		t.Fatalf("GetL0: %v", err)
	}
	check := func(who string, slot *memhop.ProfileSlot) {
		if slot.Name != "重构那一域" {
			t.Fatalf("%s: the host's name reads %q after the consolidation", who, slot.Name)
		}
		if slot.Role != "管回归与重构" {
			t.Fatalf("%s: the host's role reads %q after the consolidation", who, slot.Role)
		}
		if len(slot.Preferences) != 2 || slot.Preferences["lang"] != "zh" ||
			slot.Preferences["style"] != "先复现再动手" {
			t.Fatalf("%s: the host's preferences read %+v after the consolidation, want both entries", who, slot.Preferences)
		}
		// The half the pass owns, in the same read: proof this is the result of a write and not
		// the fixture left standing.
		// ESTP, not the word the endpoint claimed: the type is read off the four axes the record
		// holds, so this doubles as proof the distilled axes landed and that no implementation
		// copies the model's own label through.
		if !strings.Contains(slot.Personality, "务实直接") || slot.MBTI.Type != "ESTP" {
			t.Fatalf("%s: the distilled half reads personality=%q type=%q, want the answer this fixture's "+
				"endpoint gave — without it the host-fields assertions above prove nothing",
				who, slot.Personality, slot.MBTI.Type)
		}
	}
	check("this process", prof)

	// And off the disk: a host edit kept only in a warm cache would survive the pass and vanish
	// at the next open.
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reopened := newTestDB(t, openMockDB(t, path, llm.srv.URL))
	again, err := reopened.GetL0()
	if err != nil {
		t.Fatalf("GetL0 after restart: %v", err)
	}
	check("after a restart", again)
}
