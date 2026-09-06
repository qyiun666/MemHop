// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package trajectory

import (
	"path/filepath"
	"testing"

	"github.com/qyiun666/MemHop/internal/cap/llmops"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// A merge candidate overwrites the stored resources wholesale, so its
// entries must pass the same resource checks a create would — an invalid
// one is recorded as a skip, not folded into a stored card.
func TestApplyCandidateMergeValidatesResources(t *testing.T) {
	engine, err := core.Create(filepath.Join(t.TempDir(), "t.meh"))
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	defer engine.Close(nil)

	result := &core.CrystallizeResult{}
	cand := llmops.CrystallizeCapability{
		Action: "merge",
		Capability: core.CapabilityImport{
			Name:      "x",
			Summary:   "s",
			Resources: []core.ResourceRef{{Type: "bogus", Name: "r"}},
		},
	}
	reserved := func(uint64) bool { return false }
	if err := ApplyCandidate(engine, core.SharedPoolAgentID, cand, result, reserved); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(result.Errors) != 1 || len(result.MergedIDs)+len(result.CreatedIDs) != 0 {
		t.Fatalf("invalid merge must be skipped: %+v", result)
	}
}
