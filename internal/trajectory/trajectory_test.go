// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package trajectory

import (
	"strings"
	"testing"

	"github.com/qyiun666/MemHop/internal/repo/core"
)

func TestTrimByBudgetKeepsNewest(t *testing.T) {
	events := []core.TrajectorySlot{
		{Payload: strings.Repeat("a", 60)},
		{Payload: strings.Repeat("b", 60)},
		{Payload: strings.Repeat("c", 60)},
	}
	if got := TrimByBudget(events, 100); len(got) != 1 || got[0].Payload[0] != 'c' {
		t.Fatalf("trim = %+v, want only the newest event", got)
	}
	if got := TrimByBudget(events, 1000); len(got) != 3 {
		t.Fatalf("under budget must keep all: %+v", got)
	}
	if got := TrimByBudget(events, 1); len(got) != 1 || got[0].Payload[0] != 'c' {
		t.Fatalf("tiny budget must still keep the newest: %+v", got)
	}
}
