// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package trajectory

import (
	"path/filepath"
	"testing"

	"github.com/qyiun666/MemHop/internal/cap/llmops"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// Validation is gated by where a candidate can reach the store: merge
// overwrites the stored card wholesale (upfront check) and a reuse that
// misses its target degrades into a create (gated before the upsert), so
// both malformed payloads are recorded as a skip. A reuse that hits its
// target writes nothing and needs no validation — pinned separately by
// TestCrystallizeReuseMinimalPayload.
func TestApplyCandidateValidationGating(t *testing.T) {
	engine, err := core.Create(filepath.Join(t.TempDir(), "t.meh"))
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	defer engine.Close(nil)

	reserved := func(uint64) bool { return false }
	cases := []struct {
		name string
		cand llmops.CrystallizeCapability
	}{
		{"merge with unknown resource type", llmops.CrystallizeCapability{
			Action: "merge",
			Capability: core.CapabilityImport{
				Name: "x", Summary: "s", Trigger: "t",
				Resources: []core.ResourceRef{{Type: "bogus", Name: "r"}},
			},
		}},
		{"reuse miss with no resources", llmops.CrystallizeCapability{
			Action: "reuse", ReuseID: "0000000000000000",
			Capability: core.CapabilityImport{Name: "y"},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := &core.CrystallizeResult{}
			if err := ApplyCandidate(engine, core.SharedPoolAgentID, tc.cand, result, reserved); err != nil {
				t.Fatalf("apply: %v", err)
			}
			if len(result.Errors) != 1 || len(result.CreatedIDs)+len(result.MergedIDs)+len(result.ReusedIDs) != 0 {
				t.Fatalf("malformed candidate must be skipped: %+v", result)
			}
		})
	}
}
