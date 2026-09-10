// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package domain

import (
	"slices"

	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// PlanCache caches each topic's plan tree in memory so PlanState and the rollup
// avoid a full engine scan per operation. Built from the engine when the agent
// context is created (and rebuilt on idle reclaim) and maintained incrementally
// by the internal layer, which owns every plan write and delete under the same
// domain lock (Context.Mu) — so the cache carries no lock of its own and is only
// ever touched while the caller holds Context.Mu. A tree is keyed by the topic of
// the turn that opened it, so a key exists exactly while at least one of its
// nodes does.
type PlanCache struct {
	plans map[uint64]*repo.PlanAggregate
}

func buildPlanCache(engine *core.StorageEngine, agentID uint64) *PlanCache {
	pc := &PlanCache{plans: make(map[uint64]*repo.PlanAggregate)}
	for _, agg := range repo.CollectPlanNodes(engine, agentID) {
		a := agg
		pc.plans[a.TopicID] = &a
	}
	return pc
}

// Aggregate returns the cached tree of one topic's plan; nil when unknown.
func (pc *PlanCache) Aggregate(topicID uint64) *repo.PlanAggregate {
	return pc.plans[topicID]
}

// UpsertNode inserts or updates one node in its aggregate, keeping the nodes
// NodePath-ordered so planForest can consume them directly. Node identity is the
// stable derived IDHash, so an in-place replacement preserves the address.
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
	slices.SortFunc(agg.Nodes, func(a, b core.PlanNode) int {
		return repo.CompareNodePath(a.NodePath, b.NodePath)
	})
	recomputePlanAggStat(agg)
}

// RemoveNodes drops specific nodes from the cache, the counterpart of a retention
// sweep that tombstoned them; a subtree that loses its last node stops being a
// live plan. Does not touch the engine.
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
	recomputePlanAggStat(agg)
}

// RemoveTopic drops a whole tree from the cache, the counterpart of deleting the
// turn that owned it.
func (pc *PlanCache) RemoveTopic(topicID uint64) {
	delete(pc.plans, topicID)
}

// recomputePlanAggStat rescans an aggregate after a node mutation (insert,
// update, delete), where statuses and the newest timestamp may have changed.
func recomputePlanAggStat(agg *repo.PlanAggregate) {
	agg.LastActiveAt = 0
	agg.HasNonDone = false
	for _, n := range agg.Nodes {
		if n.UpdatedAt > agg.LastActiveAt {
			agg.LastActiveAt = n.UpdatedAt
		}
		if n.Status != core.StatusDone {
			agg.HasNonDone = true
		}
	}
}
