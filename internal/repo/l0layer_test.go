// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package repo

import (
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// Whoever opens a file acts on this probe to decide whether the primary domain
// still needs a profile, so "no record" and "record I cannot read" have to stay
// two answers: collapsing them would let a transient failure read as an empty
// domain and get seeded over.
func TestHasProfileL0TellsAbsentFromUnreadable(t *testing.T) {
	engine := tempEngine(t)

	has, err := HasProfileL0(engine, core.DefaultAgentID)
	if has || err != nil {
		t.Fatalf("an empty domain has no profile: has=%v err=%v", has, err)
	}

	if err := UpdateProfileL0(engine, core.DefaultAgentID, &core.ProfileSlot{Name: "Meow"}); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	if has, err := HasProfileL0(engine, core.DefaultAgentID); !has || err != nil {
		t.Fatalf("a seeded domain has one: has=%v err=%v", has, err)
	}

	// A record that is there but does not parse is an error, not an absence.
	if _, err := engine.WriteRecord(core.DefaultAgentID, core.RecL0Profile,
		common.HashID("profile"), []byte("{not json")); err != nil {
		t.Fatalf("corrupt the profile record: %v", err)
	}
	has, err = HasProfileL0(engine, core.DefaultAgentID)
	if err == nil {
		t.Fatalf("an unreadable profile must be reported, got has=%v err=nil", has)
	}
	if has {
		t.Fatal("an unreadable profile must not claim to be there")
	}
	if common.CodeOf(err) == common.ErrNotFound {
		t.Fatalf("the unreadable case must not wear the absent case's code: %v", err)
	}
}
