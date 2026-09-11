// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package engram

import (
	"path/filepath"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// An edge the decay pass cannot read is not an edge that went away: reporting it
// as pruned lets the pass carry on and leave nodes holding a reference to an edge
// nobody trimmed. The sibling that prunes the other half of the same link already
// draws this line, so the two directions of one cascade now agree.
func TestRemoveNodeFromEdgeReportsUnreadableEdge(t *testing.T) {
	engine, err := core.Create(filepath.Join(t.TempDir(), "decay.meh"))
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	const (
		edgeID   = uint64(0xE1)
		victimID = uint64(1)
		otherID  = uint64(2)
	)
	edge := &core.SceneEdge{
		IDHash: edgeID, Kind: core.HyperCoOccurrence, NodeIDs: []uint64{victimID, otherID},
		Weight: 0.9, CreatedAt: 1,
	}
	if err := core.WriteSceneEdge(engine, core.DefaultAgentID, edgeID, edge); err != nil {
		t.Fatalf("write edge: %v", err)
	}
	if _, err := engine.WriteRecord(core.DefaultAgentID, core.RecL1Hyperedge, edgeID,
		[]byte(`{"node_ids":`)); err != nil {
		t.Fatalf("replace the edge payload with an undecodable one: %v", err)
	}

	deleted, err := removeNodeFromEdge(engine, core.DefaultAgentID, edgeID, victimID, &DecayParams{MinEdgeNodes: 2})
	if err == nil {
		t.Fatal("an unreadable edge must stop the cascade, not be reported as pruned")
	}
	if common.CodeOf(err) != common.ErrDeserialization {
		t.Fatalf("want the read's own classification, got %v", err)
	}
	if deleted {
		t.Fatal("nothing was deleted, so the report must not say otherwise")
	}
}

// An edge that is genuinely gone has no member list left to prune, and that is a
// clean answer rather than a failure — the node's stale reference is trimmed by
// whoever reads it next.
func TestRemoveNodeFromEdgeToleratesGoneEdge(t *testing.T) {
	engine, err := core.Create(filepath.Join(t.TempDir(), "decay.meh"))
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	deleted, err := removeNodeFromEdge(engine, core.DefaultAgentID, 0xE2, 1, &DecayParams{MinEdgeNodes: 2})
	if err != nil {
		t.Fatalf("a missing edge is not a failure: %v", err)
	}
	if deleted {
		t.Fatal("an edge that was never there cannot be deleted")
	}
}
