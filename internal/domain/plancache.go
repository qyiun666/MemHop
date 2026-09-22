// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package domain

import (
	"slices"

	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// PlanCache holds each topic's plan tree in memory so a tree read costs no engine
// scan per operation. It is built from the engine when a Context is created and
// maintained incrementally by whoever writes plan records; it carries no lock of its
// own, so it is only ever touched while the caller holds Context.Mu. A tree is keyed
// by the topic of the turn that opened it, so a key exists exactly while at least
// one of its nodes does.
type PlanCache struct {
	plans map[uint64]*repo.PlanAggregate
}

func buildPlanCache(engine *core.StorageEngine, agentID uint64) *PlanCache {
	// Rebuilt from what still decodes: an unreadable node is absent in exactly the
	// shape an expired one has, and the mirror cannot tell the two apart from here.
	pc := &PlanCache{plans: make(map[uint64]*repo.PlanAggregate)}
	for _, agg := range repo.GroupPlanNodes(core.CollectAllPlanNodes(engine, agentID)) {
		a := agg
		pc.plans[a.TopicID] = &a
	}
	return pc
}

// Aggregate returns the cached tree of one topic's plan; nil when unknown.
func (pc *PlanCache) Aggregate(topicID uint64) *repo.PlanAggregate {
	return pc.plans[topicID]
}

// HasSeq reports whether one topic's live plan tree holds a node at seq — the check a
// caller runs before binding anything to a step.
func (pc *PlanCache) HasSeq(topicID uint64, seq uint32) bool {
	agg := pc.plans[topicID]
	if agg == nil {
		return false
	}
	for i := range agg.Nodes {
		if agg.Nodes[i].Seq == seq {
			return true
		}
	}
	return false
}

// Subtree returns the ordinals of one step and every step nested under it,
// Seq-ascending and including the step itself. An unknown root yields just itself: a
// step whose record expired still names its own events. Callers hold Context.Mu.
func (pc *PlanCache) Subtree(topicID uint64, root uint32) []uint32 {
	agg := pc.plans[topicID]
	if agg == nil {
		return []uint32{root}
	}
	children := make(map[uint32][]uint32, len(agg.Nodes))
	for _, n := range agg.Nodes {
		children[n.ParentSeq] = append(children[n.ParentSeq], n.Seq)
	}
	out := []uint32{root}
	for queue := []uint32{root}; len(queue) > 0; {
		cur := queue[0]
		queue = queue[1:]
		for _, kid := range children[cur] {
			out = append(out, kid)
			queue = append(queue, kid)
		}
	}
	slices.Sort(out)
	return out
}

// NextSeq hands out the next ordinal of one topic's tree.
// ponytail: derived from the live nodes, so any removal frees an ordinal, and an
// emptied tree's key goes with it — that turn starts again at 1. An event ages on its
// own clock and can outlive the step it names, so writing into an old turn key after
// that tree was swept can meet an ordinal an event still points at. The upgrade path
// is a persisted per-topic high-water record. Callers hold Context.Mu.
func (pc *PlanCache) NextSeq(topicID uint64) uint32 {
	agg := pc.plans[topicID]
	if agg == nil {
		return 1
	}
	var top uint32
	for _, n := range agg.Nodes {
		if n.Seq > top {
			top = n.Seq
		}
	}
	return top + 1
}

// UpsertNode inserts or updates one node in its aggregate, keeping the nodes
// Seq-ordered. A node's ordinal is never rewritten, so an in-place replacement
// keeps the same derived IDHash and the same address.
func (pc *PlanCache) UpsertNode(topicID uint64, node *core.PlanNode) {
	if node == nil {
		return
	}
	agg := pc.plans[topicID]
	if agg == nil {
		agg = &repo.PlanAggregate{TopicID: topicID}
		pc.plans[topicID] = agg
	}
	found := false
	for i := range agg.Nodes {
		if agg.Nodes[i].IDHash == node.IDHash {
			agg.Nodes[i] = *node
			found = true
			break
		}
	}
	if !found {
		agg.Nodes = append(agg.Nodes, *node)
	}
	slices.SortFunc(agg.Nodes, repo.ComparePlanNodeSeq)
	repo.RecomputePlanAgg(agg)
}

// RemoveNodes drops specific nodes from the cache — the mirror step of a delete
// that has already tombstoned them. A tree that loses its last node stops being a
// live plan. It does not touch the engine.
func (pc *PlanCache) RemoveNodes(topicID uint64, nodeIDs []uint64) {
	agg := pc.plans[topicID]
	if agg == nil {
		return
	}
	doomed := make(map[uint64]struct{}, len(nodeIDs))
	for _, id := range nodeIDs {
		doomed[id] = struct{}{}
	}
	agg.Nodes = slices.DeleteFunc(agg.Nodes, func(n core.PlanNode) bool {
		_, ok := doomed[n.IDHash]
		return ok
	})
	if len(agg.Nodes) == 0 {
		delete(pc.plans, topicID)
		return
	}
	repo.RecomputePlanAgg(agg)
}

// RemoveTopic drops a whole tree from the cache — the mirror step of a topic
// delete. It does not touch the engine.
func (pc *PlanCache) RemoveTopic(topicID uint64) {
	delete(pc.plans, topicID)
}
