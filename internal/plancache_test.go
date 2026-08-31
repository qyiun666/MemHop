// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"testing"

	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// tnode builds a plan-node slot with stable derived IDHash semantics for the
// cache tests (identity is IDHash, not path).
func tnode(id, planID uint64, nodePath string, seq uint64, status uint8, ts int64) *core.TrajectorySlot {
	return &core.TrajectorySlot{
		IDHash: id, PlanID: planID, NodePath: nodePath, Seq: seq,
		NodeType: core.NodeTypePlan, Status: status, Timestamp: ts,
	}
}

func TestPlanCacheUpsertKeepsOrderAndStats(t *testing.T) {
	pc := &planCache{plans: make(map[uint64]*repo.PlanAggregate)}
	pc.upsertNode(9, tnode(11, 9, "1", 1, core.StatusPending, 100))
	pc.upsertNode(9, tnode(12, 9, "2", 1, core.StatusDone, 200))
	// "1.1" has Seq=2, so it sorts after the Seq=1 roots "1" and "2" even
	// though its path prefix is "1"; the cache mirrors the repo sort order.
	pc.upsertNode(9, tnode(13, 9, "1.1", 2, core.StatusPending, 150))
	agg := pc.aggregate(9)
	if agg == nil {
		t.Fatal("aggregate is nil")
	}
	want := []string{"1", "2", "1.1"}
	for i, p := range want {
		if agg.Nodes[i].NodePath != p {
			t.Fatalf("order[%d]=%s want %s (nodes=%v)", i, agg.Nodes[i].NodePath, p, agg.Nodes)
		}
	}
	if !agg.HasNonDone {
		t.Fatal("HasNonDone should be true (a pending node remains)")
	}
	if agg.CreatedAt != 100 || agg.LastActiveAt != 200 {
		t.Fatalf("stats created=%d last=%d", agg.CreatedAt, agg.LastActiveAt)
	}
	// Updating "1" to done keeps HasNonDone true while "1.1" is still pending.
	pc.upsertNode(9, tnode(11, 9, "1", 1, core.StatusDone, 250))
	agg = pc.aggregate(9)
	if !agg.HasNonDone {
		t.Fatal("HasNonDone should stay true while 1.1 is pending")
	}
	// Once every node is done, HasNonDone flips false.
	pc.upsertNode(9, tnode(13, 9, "1.1", 2, core.StatusDone, 260))
	agg = pc.aggregate(9)
	if agg.HasNonDone {
		t.Fatal("HasNonDone should be false once every node is done")
	}
	if agg.LastActiveAt != 260 {
		t.Fatalf("LastActiveAt=%d want 260", agg.LastActiveAt)
	}
}

func TestPlanCacheUpsertEventAndRemoveBranch(t *testing.T) {
	pc := &planCache{plans: make(map[uint64]*repo.PlanAggregate)}
	pc.upsertNode(9, tnode(11, 9, "1", 1, core.StatusPending, 100))
	pc.upsertNode(9, tnode(12, 9, "1.1", 2, core.StatusPending, 150))
	pc.upsertNode(9, tnode(13, 9, "1.1.1", 3, core.StatusPending, 180))
	pc.upsertNode(9, tnode(14, 9, "2", 1, core.StatusPending, 200))
	pc.upsertEvent(9, 12, core.TrajectorySlot{IDHash: 101, PlanID: 9, PlanNodeRef: 12, Timestamp: 300})
	pc.upsertEvent(9, 14, core.TrajectorySlot{IDHash: 102, PlanID: 9, PlanNodeRef: 14, Timestamp: 400})
	agg := pc.aggregate(9)
	if agg.EventCount[12] != 1 || agg.EventCount[14] != 1 {
		t.Fatalf("event counts: %v", agg.EventCount)
	}
	if agg.LastActiveAt != 400 {
		t.Fatalf("LastActiveAt=%d want 400", agg.LastActiveAt)
	}
	// Remove branch "1": nodes 1/1.1/1.1.1 and the event bound to 12 drop; the
	// sibling "2" (and its event) survive.
	pc.removeNodeBranch(9, "1")
	agg = pc.aggregate(9)
	if agg == nil {
		t.Fatal("aggregate should survive (node 2 remains)")
	}
	if len(agg.Nodes) != 1 || agg.Nodes[0].NodePath != "2" {
		t.Fatalf("surviving nodes: %v", agg.Nodes)
	}
	if _, ok := agg.EventCount[12]; ok {
		t.Fatalf("removed branch event must be dropped: %v", agg.EventCount)
	}
	if agg.EventCount[14] != 1 {
		t.Fatalf("sibling event must stay: %v", agg.EventCount)
	}
	// Removing the last node detaches the whole aggregate.
	pc.removeNodeBranch(9, "2")
	if agg := pc.aggregate(9); agg != nil {
		t.Fatal("empty plan must be detached")
	}
}

func TestPlanCacheRemovePlanDetaches(t *testing.T) {
	pc := &planCache{plans: make(map[uint64]*repo.PlanAggregate)}
	pc.upsertNode(9, tnode(11, 9, "1", 1, core.StatusPending, 100))
	pc.removePlan(9)
	if pc.aggregate(9) != nil {
		t.Fatal("removePlan must drop the aggregate")
	}
}
