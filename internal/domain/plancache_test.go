// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package domain

import (
	"slices"
	"testing"

	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// tnode builds a plan node for the cache tests. Identity is the hash derived from
// the owning topic and the step ordinal, so one (topic, seq) pair always means the
// same node being committed again.
func tnode(topicID uint64, seq, parentSeq uint32, status uint8, ts int64) *core.PlanNode {
	return &core.PlanNode{
		IDHash: core.HashPlanNode(topicID, seq), TopicID: topicID,
		Seq: seq, ParentSeq: parentSeq, Status: status, UpdatedAt: ts,
	}
}

func TestPlanCacheUpsertKeepsOrderAndStats(t *testing.T) {
	pc := &PlanCache{plans: make(map[uint64]*repo.PlanAggregate)}
	pc.UpsertNode(9, tnode(9, 1, 0, core.StatusInProgress, 100))
	pc.UpsertNode(9, tnode(9, 2, 0, core.StatusDone, 200))
	// Ordinals sort as numbers, so step 9 lands before step 10 however the nodes
	// arrive.
	pc.UpsertNode(9, tnode(9, 10, 1, core.StatusInProgress, 150))
	pc.UpsertNode(9, tnode(9, 9, 1, core.StatusDone, 150))
	agg := pc.Aggregate(9)
	if agg == nil {
		t.Fatal("aggregate is nil")
	}
	want := []uint32{1, 2, 9, 10}
	for i, p := range want {
		if agg.Nodes[i].Seq != p {
			t.Fatalf("order[%d]=%d want %d (nodes=%v)", i, agg.Nodes[i].Seq, p, agg.Nodes)
		}
	}
	if agg.LastActiveAt != 200 {
		t.Fatalf("LastActiveAt=%d want 200", agg.LastActiveAt)
	}
	if !agg.HasNonDone {
		t.Fatal("HasNonDone should be true (an in-progress node remains)")
	}
	// Marking step 1 done keeps HasNonDone true while step 10 still runs.
	pc.UpsertNode(9, tnode(9, 1, 0, core.StatusDone, 250))
	if !pc.Aggregate(9).HasNonDone {
		t.Fatal("HasNonDone should stay true while step 10 is in progress")
	}
	// Once every node is done, HasNonDone flips false — the exemption that keeps
	// the whole tree alive is gone.
	pc.UpsertNode(9, tnode(9, 10, 1, core.StatusDone, 260))
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
	pc.UpsertNode(9, tnode(9, 1, 0, core.StatusDone, 100))
	pc.UpsertNode(9, tnode(9, 2, 1, core.StatusDone, 150))
	pc.UpsertNode(9, tnode(9, 3, 2, core.StatusDone, 180))
	pc.UpsertNode(9, tnode(9, 4, 0, core.StatusInProgress, 200))

	pc.RemoveNodes(9, []uint64{
		core.HashPlanNode(9, 1), core.HashPlanNode(9, 2), core.HashPlanNode(9, 3)})
	agg := pc.Aggregate(9)
	if agg == nil {
		t.Fatal("aggregate should survive (node 4 remains)")
	}
	if len(agg.Nodes) != 1 || agg.Nodes[0].Seq != 4 {
		t.Fatalf("surviving nodes: %v", agg.Nodes)
	}
	if agg.LastActiveAt != 200 {
		t.Fatalf("LastActiveAt=%d want 200 (swept nodes must not keep the tree exempt)", agg.LastActiveAt)
	}
	pc.RemoveNodes(9, []uint64{core.HashPlanNode(9, 4)})
	if agg := pc.Aggregate(9); agg != nil {
		t.Fatal("a plan whose last node is gone must be detached")
	}
}

func TestPlanCacheRemoveTopicDropsTheWholeTree(t *testing.T) {
	pc := &PlanCache{plans: make(map[uint64]*repo.PlanAggregate)}
	pc.UpsertNode(9, tnode(9, 1, 0, core.StatusInProgress, 100))
	pc.UpsertNode(10, tnode(10, 1, 0, core.StatusInProgress, 100))
	pc.RemoveTopic(9)
	if pc.Aggregate(9) != nil {
		t.Fatal("deleted topic still holds a cached tree")
	}
	if pc.Aggregate(10) == nil {
		t.Fatal("RemoveTopic must not touch another turn's plan")
	}
}

// A step is only bindable when this turn's tree holds it, and the check is scoped
// to the turn: two turns both having a step 1 does not make one visible to the
// other.
func TestPlanCacheHasSeqIsTurnScoped(t *testing.T) {
	pc := &PlanCache{plans: make(map[uint64]*repo.PlanAggregate)}
	pc.UpsertNode(9, tnode(9, 1, 0, core.StatusInProgress, 100))
	if !pc.HasSeq(9, 1) {
		t.Fatal("step 1 of turn 9 exists")
	}
	if pc.HasSeq(9, 2) {
		t.Fatal("a step never created must not read as live")
	}
	if pc.HasSeq(10, 1) {
		t.Fatal("another turn's step 1 must not answer for this turn")
	}
	if pc.HasSeq(11, 1) {
		t.Fatal("a turn with no plan at all holds no step")
	}
}

// The subtree a step read covers is walked over the parent links, so a step's
// branch reaches arbitrarily deep — and a branch that was never made does not
// drag in its neighbour two digits away.
func TestPlanCacheSubtreeWalksParentLinks(t *testing.T) {
	pc := &PlanCache{plans: make(map[uint64]*repo.PlanAggregate)}
	for _, n := range []*core.PlanNode{
		tnode(9, 1, 0, core.StatusDone, 100), // root
		tnode(9, 2, 1, core.StatusDone, 100), // under 1
		tnode(9, 3, 2, core.StatusDone, 100), // under 2
		tnode(9, 4, 1, core.StatusDone, 100), // under 1, sibling of 2
		tnode(9, 5, 0, core.StatusDone, 100), // a second root
	} {
		pc.UpsertNode(9, n)
	}
	if got := pc.Subtree(9, 1); !slices.Equal(got, []uint32{1, 2, 3, 4}) {
		t.Fatalf("subtree(1) = %v, want the whole branch under step 1", got)
	}
	if got := pc.Subtree(9, 2); !slices.Equal(got, []uint32{2, 3}) {
		t.Fatalf("subtree(2) = %v, want step 2 and its child", got)
	}
	if got := pc.Subtree(9, 5); !slices.Equal(got, []uint32{5}) {
		t.Fatalf("subtree(5) = %v, want a leaf root on its own", got)
	}
	// An unknown root still names its own events: the step may have expired while
	// the records bound to it are inside their own window.
	if got := pc.Subtree(9, 77); !slices.Equal(got, []uint32{77}) {
		t.Fatalf("subtree of an unknown step = %v, want just that step", got)
	}
	if got := pc.Subtree(12, 1); !slices.Equal(got, []uint32{1}) {
		t.Fatalf("subtree under a turn with no plan = %v, want just that step", got)
	}
}

// Ordinals are handed out above everything the tree holds now, so two creates in a
// row never collide — and a turn with no plan starts at 1.
func TestPlanCacheNextSeq(t *testing.T) {
	pc := &PlanCache{plans: make(map[uint64]*repo.PlanAggregate)}
	if got := pc.NextSeq(9, 0); got != 1 {
		t.Fatalf("an empty tree's next step = %d, want 1", got)
	}
	pc.UpsertNode(9, tnode(9, 1, 0, core.StatusInProgress, 100))
	pc.UpsertNode(9, tnode(9, 2, 1, core.StatusInProgress, 100))
	if got := pc.NextSeq(9, 0); got != 3 {
		t.Fatalf("next step = %d, want one above the highest held", got)
	}
	if got := pc.NextSeq(10, 0); got != 1 {
		t.Fatalf("another turn's next step = %d, want 1", got)
	}
	// The shape the reserved floor exists for: the tree was swept, so the live nodes
	// say nothing, while an event written for its first step is still on record.
	pc.RemoveTopic(9)
	if got := pc.NextSeq(9, 0); got != 1 {
		t.Fatalf("an emptied tree = %d, want the numbering to start again", got)
	}
	if got := pc.NextSeq(9, 4); got != 5 {
		t.Fatalf("next step above an ordinal an event still names = %d, want 5", got)
	}
	// Both sources count, whichever is higher: a live tree is not moved down by a
	// floor below its own top, nor left behind by a floor above it.
	pc.UpsertNode(9, tnode(9, 1, 0, core.StatusInProgress, 100))
	pc.UpsertNode(9, tnode(9, 2, 1, core.StatusInProgress, 100))
	if got := pc.NextSeq(9, 1); got != 3 {
		t.Fatalf("a live tree above the floor = %d, want one above the nodes", got)
	}
	if got := pc.NextSeq(9, 7); got != 8 {
		t.Fatalf("a floor above the live nodes = %d, want one above the floor", got)
	}
}
