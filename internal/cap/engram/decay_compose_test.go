// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package engram

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/qyiun666/MemHop/internal/repo/core"
)

const decayHourMs = int64(3_600_000)

// Forgetting is measured in wall-clock hours, and the pass that applies it re-bases the
// very field it just measured. Those two only agree if the decay composes: two passes an
// hour apart have to land on the importance a single two-hour pass reaches. That re-based
// field is also what a host reads as SceneNodeView.UpdatedAt, so this pins what the answer
// means: when consolidation last held this node, not when the memory was last used.
func TestNodeDecayComposesAcrossPasses(t *testing.T) {
	engine, err := core.Create(filepath.Join(t.TempDir(), "decay.meh"))
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	cfg := &DecayParams{
		LambdaNode:             0.01,
		NodeRemoveThreshold:    0.01,
		NodePruneEdgeThreshold: 0.05,
		EdgeRemoveThreshold:    0.01,
		MinEdgeNodes:           2,
	}
	const t0 = int64(1_700_000_000_000)

	seed := func(id uint64, updatedAt int64) {
		if err := core.WriteSceneNode(engine, core.DefaultAgentID, id, &core.SceneNode{
			IDHash: id, SceneID: id, Importance: 1, CreatedAt: t0, UpdatedAt: updatedAt,
		}); err != nil {
			t.Fatalf("seed node %d: %v", id, err)
		}
	}
	pass := func(id uint64, nowMs int64) *core.SceneNode {
		node, err := core.ReadSceneNode(engine, core.DefaultAgentID, id)
		if err != nil {
			t.Fatalf("read node before the pass: %v", err)
		}
		report := &DecayReport{}
		if err := decayOneNode(engine, core.DefaultAgentID, cfg, node, nowMs, report,
			map[uint64]bool{}, map[uint64]map[uint64]bool{}); err != nil {
			t.Fatalf("decay pass: %v", err)
		}
		if report.RemovedNodes != 0 {
			t.Fatalf("a node at importance %v was removed, want it to survive the pass", node.Importance)
		}
		after, err := core.ReadSceneNode(engine, core.DefaultAgentID, id)
		if err != nil {
			t.Fatalf("read node after the pass: %v", err)
		}
		return after
	}

	seed(1, t0)
	onePass := pass(1, t0+2*decayHourMs)
	if onePass.UpdatedAt != t0+2*decayHourMs {
		t.Fatalf("the pass left the node's clock at %d, want it re-based to %d",
			onePass.UpdatedAt, t0+2*decayHourMs)
	}

	seed(2, t0)
	twoPasses := pass(2, t0+decayHourMs)
	if twoPasses.UpdatedAt != t0+decayHourMs {
		t.Fatalf("the first of two passes left the clock at %d, want %d",
			twoPasses.UpdatedAt, t0+decayHourMs)
	}
	twoPasses = pass(2, t0+2*decayHourMs)

	if diff := math.Abs(onePass.Importance - twoPasses.Importance); diff > 1e-12 {
		t.Fatalf("forgetting does not compose: two one-hour passes left importance %.12f, "+
			"one two-hour pass left %.12f (the re-based clock has to keep the wall clock honest)",
			twoPasses.Importance, onePass.Importance)
	}

	// A memory touched again inside the interval is not faded by the whole interval: the
	// pass measures from the touch, which is the same field.
	seed(3, t0+decayHourMs+decayHourMs/2)
	touched := pass(3, t0+2*decayHourMs)
	if touched.Importance <= onePass.Importance {
		t.Fatalf("a node touched an hour into the interval faded to %.12f, no better than a node "+
			"untouched for two hours at %.12f", touched.Importance, onePass.Importance)
	}
}
