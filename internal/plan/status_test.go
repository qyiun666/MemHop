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
	if got, err := StatusToString(core.StatusRunning); err != nil || got != PlanRunning {
		t.Fatalf("StatusToString(%d) = %q, %v want %q", core.StatusRunning, got, err, PlanRunning)
	}
	if _, err := StatusToU8(PlanStatus("bogus")); err == nil {
		t.Fatal("unknown status must be rejected")
	}
	// The read side must refuse too: Status is the one bare uint8 in a plan node
	// whose meaning crosses file versions, and falling back to pending would
	// render a step the engine cannot name as one that has not started.
	if _, err := StatusToString(9); err == nil {
		t.Fatal("an undefined stored status must be reported, not defaulted")
	}
	if !IsTerminalStatus(core.StatusDone) || !IsTerminalStatus(core.StatusFailed) {
		t.Fatal("done/failed are terminal")
	}
	if IsTerminalStatus(core.StatusPending) {
		t.Fatal("pending is not terminal")
	}
}
