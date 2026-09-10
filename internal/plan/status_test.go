// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package plan

import (
	"testing"

	"github.com/qyiun666/MemHop/internal/repo/core"
)

func TestStatusRoundTrip(t *testing.T) {
	for u, name := range map[uint8]PlanStatus{
		core.StatusInProgress: PlanInProgress,
		core.StatusDone:       PlanDone,
		core.StatusFailed:     PlanFailed,
	} {
		if got, err := StatusToString(u); err != nil || got != name {
			t.Fatalf("StatusToString(%d) = %q, %v want %q", u, got, err, name)
		}
		if got, err := StatusToU8(name); err != nil || got != u {
			t.Fatalf("StatusToU8(%q) = %d, %v want %d", name, got, err, u)
		}
	}
	if _, err := StatusToU8(PlanStatus("bogus")); err == nil {
		t.Fatal("unknown status must be rejected")
	}
	// pending left the vocabulary with the whole-tree declaration: a step is
	// created in progress, so there is no "planned but not started" state to name.
	// The write side has to refuse the word rather than accept it under in_progress.
	if _, err := StatusToU8(PlanStatus("pending")); err == nil {
		t.Fatal("the retired pending status must be rejected")
	}
	if _, err := StatusToU8(PlanStatus("running")); err == nil {
		t.Fatal("the retired running status must be rejected")
	}
	// The read side must refuse too: Status is the one bare uint8 in a plan node
	// whose meaning crosses file versions, and a fallback would render a step the
	// engine cannot name as one it can.
	if _, err := StatusToString(9); err == nil {
		t.Fatal("an undefined stored status must be reported, not defaulted")
	}
	// 3 is where failed sat before this table cut pending out and moved every value
	// down one: a file from that era must not read as a live status.
	if _, err := StatusToString(3); err == nil {
		t.Fatal("a retired stored value must be reported as an undefined stored status")
	}
	if !IsTerminalStatus(core.StatusDone) || !IsTerminalStatus(core.StatusFailed) {
		t.Fatal("done/failed are terminal")
	}
	if IsTerminalStatus(core.StatusInProgress) {
		t.Fatal("in progress is not terminal")
	}
}

// A created step starts in progress, so the zero value a fresh record carries is a
// state the surface can name — nothing has to remember to stamp a status.
func TestFreshNodeStatusIsNameable(t *testing.T) {
	got, err := StatusToString(core.PlanNode{}.Status)
	if err != nil {
		t.Fatalf("an unstamped status must be the created state, got %v", err)
	}
	if got != PlanInProgress {
		t.Fatalf("fresh status = %q, want %q", got, PlanInProgress)
	}
}
