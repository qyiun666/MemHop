// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package plan

import (
	"testing"

	"github.com/qyiun666/MemHop/internal/repo/core"
)

func TestStatusRoundTrip(t *testing.T) {
	if got, _ := StatusToU8(PlanRunning); got != core.StatusRunning {
		t.Fatalf("StatusToU8(running) = %d want %d", got, core.StatusRunning)
	}
	if StatusToString(core.StatusRunning) != PlanRunning {
		t.Fatalf("StatusToString(%d) = %q want %q", core.StatusRunning, StatusToString(core.StatusRunning), PlanRunning)
	}
	if _, err := StatusToU8(PlanStatus("bogus")); err == nil {
		t.Fatal("unknown status must be rejected")
	}
	if !IsTerminalStatus(core.StatusDone) || !IsTerminalStatus(core.StatusFailed) {
		t.Fatal("done/failed are terminal")
	}
	if IsTerminalStatus(core.StatusPending) {
		t.Fatal("pending is not terminal")
	}
}

func TestParsePlanIDRejectsReservedZero(t *testing.T) {
	if _, err := ParsePlanID("0000000000000000"); err == nil {
		t.Fatal("the all-zero plan id is reserved")
	}
	if _, err := ParsePlanID("not-hex"); err == nil {
		t.Fatal("non-hex plan id must be rejected")
	}
	if got, err := ParsePlanID("0000000000000009"); err != nil || got != 9 {
		t.Fatalf("ParsePlanID = %d, %v", got, err)
	}
}
