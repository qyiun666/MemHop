// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package domain

import (
	"testing"

	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// tnode builds a plan node for the cache tests. Identity is the derived IDHash,
// not the path, so the tests can reuse one id to mean one node being committed
// again.
func tnode(id, topicID uint64, nodePath string, status uint8, ts int64) *core.PlanNode {
	return &core.PlanNode{
		IDHash: id, TopicID: topicID, NodePath: nodePath,
		Status: status, UpdatedAt: ts,
	}
}

func TestPlanCacheUpsertKeepsOrderAndStats(t *testing.T) {
	pc := &PlanCache{plans: make(map[uint64]*repo.PlanAggregate)}
	pc.UpsertNode(9, tnode(11, 9, "1", core.StatusPending, 100))
	pc.UpsertNode(9, tnode(12, 9, "2", core.StatusDone, 200))
	// Nodes are ordered by node path alone now: a Seq field no longer exists on a
	// node to sort by, and the path is what the tree is addressed by.
	pc.UpsertNode(9, tnode(13, 9, "1.1", core.StatusPending, 150))
	agg := pc.Aggregate(9)
	if agg == nil {
		t.Fatal("aggregate is nil")
	}
	want := []string{"1", "1.1", "2"}
	for i, p := range want {
		if agg.Nodes[i].NodePath != p {
			t.Fatalf("order[%d]=%s want %s (nodes=%v)", i, agg.Nodes[i].NodePath, p, agg.Nodes)
		}
	}
	if agg.LastActiveAt != 200 {
		t.Fatalf("LastActiveAt=%d want 200", agg.LastActiveAt)
	}
	if !agg.HasNonDone {
		t.Fatal("HasNonDone should be true (a pending node remains)")
	}
	// Updating "1" to done keeps HasNonDone true while "1.1" is still pending.
	pc.UpsertNode(9, tnode(11, 9, "1", core.StatusDone, 250))
	agg = pc.Aggregate(9)
	if !agg.HasNonDone {
		t.Fatal("HasNonDone should stay true while 1.1 is pending")
	}
	// Once every node is done, HasNonDone flips false — the exemption that keeps
	// the whole tree alive is gone.
	pc.UpsertNode(9, tnode(13, 9, "1.1", core.StatusDone, 260))
	agg = pc.Aggregate(9)
	if agg.HasNonDone {
		t.Fatal("HasNonDone should be false once every node is done")
	}
	if agg.LastActiveAt != 260 {
		t.Fatalf("LastActiveAt=%d want 260", agg.LastActiveAt)
	}
}

// A plan is a live plan only while a node of it exists: dropping the last one
// detaches the aggregate, so a tree whose whole branch was swept stops being
// addressed at all.
func TestPlanCacheRemoveNodesAndDetachWhenEmpty(t *testing.T) {
	pc := &PlanCache{plans: make(map[uint64]*repo.PlanAggregate)}
	pc.UpsertNode(9, tnode(11, 9, "1", core.StatusDone, 100))
	pc.UpsertNode(9, tnode(12, 9, "1.1", core.StatusDone, 150))
	pc.UpsertNode(9, tnode(13, 9, "1.1.1", core.StatusDone, 180))
	pc.UpsertNode(9, tnode(14, 9, "2", core.StatusPending, 200))

	pc.RemoveNodes(9, []uint64{11, 12, 13})
	agg := pc.Aggregate(9)
	if agg == nil {
		t.Fatal("aggregate should survive (node 2 remains)")
	}
	if len(agg.Nodes) != 1 || agg.Nodes[0].NodePath != "2" {
		t.Fatalf("surviving nodes: %v", agg.Nodes)
	}
	if agg.LastActiveAt != 200 {
		t.Fatalf("LastActiveAt=%d want 200 (swept nodes must not keep the tree exempt)", agg.LastActiveAt)
	}
	pc.RemoveNodes(9, []uint64{14})
	if agg := pc.Aggregate(9); agg != nil {
		t.Fatal("a plan whose last node is gone must be detached")
	}
}

func TestPlanCacheRemoveTopicDropsTheWholeTree(t *testing.T) {
	pc := &PlanCache{plans: make(map[uint64]*repo.PlanAggregate)}
	pc.UpsertNode(9, tnode(11, 9, "1", core.StatusPending, 100))
	pc.UpsertNode(10, tnode(21, 10, "1", core.StatusPending, 100))
	pc.RemoveTopic(9)
	if pc.Aggregate(9) != nil {
		t.Fatal("deleted topic still holds a cached tree")
	}
	if pc.Aggregate(10) == nil {
		t.Fatal("RemoveTopic must not touch another turn's plan")
	}
}
