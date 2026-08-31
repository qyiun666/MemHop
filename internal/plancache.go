// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"cmp"
	"slices"
	"strings"

	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// planCache caches every plan's nodes and bound-event count in memory so
// PlanState/ListPlans/rollup avoid a full engine scan per operation. Built from
// the engine when the agent context is created (and rebuilt on idle reclaim)
// and maintained incrementally by the internal layer, which owns every plan
// write/delete under the same domain lock (ac.mu) — so the cache carries no
// lock of its own and is only ever touched while the caller holds ac.mu.
type planCache struct {
	plans map[uint64]*repo.PlanAggregate
}

func buildPlanCache(engine *core.StorageEngine, agentID uint64) *planCache {
	pc := &planCache{plans: make(map[uint64]*repo.PlanAggregate)}
	for _, agg := range repo.CollectPlanAggregates(engine, agentID) {
		a := agg
		pc.plans[a.PlanID] = &a
	}
	return pc
}

// aggregate returns the cached aggregate of one plan; nil when unknown.
func (pc *planCache) aggregate(planID uint64) *repo.PlanAggregate {
	return pc.plans[planID]
}

// all returns every cached aggregate in PlanID-ascending order (deterministic
// ListPlans).
func (pc *planCache) all() []*repo.PlanAggregate {
	out := make([]*repo.PlanAggregate, 0, len(pc.plans))
	for _, agg := range pc.plans {
		out = append(out, agg)
	}
	slices.SortFunc(out, func(a, b *repo.PlanAggregate) int { return cmp.Compare(a.PlanID, b.PlanID) })
	return out
}

// upsertNode inserts or updates a plan node in its aggregate, keeping Nodes
// sorted by (Seq, NodePath) so planForest can consume them directly. Node
// identity is the stable derived IDHash (HashPlanNode), so an in-place
// replacement preserves the reference.
func (pc *planCache) upsertNode(planID uint64, node *core.TrajectorySlot) {
	if node == nil {
		return
	}
	agg := pc.plans[planID]
	if agg == nil {
		agg = &repo.PlanAggregate{PlanID: planID, EventCount: make(map[uint64]int)}
		pc.plans[planID] = agg
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
	slices.SortFunc(agg.Nodes, func(a, b core.TrajectorySlot) int {
		return cmp.Or(cmp.Compare(a.Seq, b.Seq), repo.CompareNodePath(a.NodePath, b.NodePath))
	})
	recomputePlanAggStat(agg)
}

// upsertEvent appends a plan-bound event and bumps its node's count. Used by
// appendPlanEventLocked; the timestamp is monotonic, so CreatedAt/LastActiveAt
// update incrementally instead of rescanning.
func (pc *planCache) upsertEvent(planID, nodeID uint64, ev core.TrajectorySlot) {
	agg := pc.plans[planID]
	if agg == nil {
		agg = &repo.PlanAggregate{PlanID: planID, EventCount: make(map[uint64]int)}
		pc.plans[planID] = agg
	}
	agg.Events = append(agg.Events, ev)
	agg.EventCount[nodeID]++
	if agg.CreatedAt == 0 || ev.Timestamp < agg.CreatedAt {
		agg.CreatedAt = ev.Timestamp
	}
	if ev.Timestamp > agg.LastActiveAt {
		agg.LastActiveAt = ev.Timestamp
	}
}

// removeNodeBranch drops the branch nodes and their bound events from the
// cache, mirroring repo.DeletePlanNodeBranch (which removes them on disk).
// Does not touch the engine.
func (pc *planCache) removeNodeBranch(planID uint64, nodePath string) {
	agg := pc.plans[planID]
	if agg == nil {
		return
	}
	prefix := nodePath + "."
	target := make(map[uint64]struct{})
	agg.Nodes = slices.DeleteFunc(agg.Nodes, func(n core.TrajectorySlot) bool {
		if n.NodePath == nodePath || strings.HasPrefix(n.NodePath, prefix) {
			target[n.IDHash] = struct{}{}
			return true
		}
		return false
	})
	agg.Events = slices.DeleteFunc(agg.Events, func(e core.TrajectorySlot) bool {
		if _, ok := target[e.PlanNodeRef]; ok {
			return true
		}
		return false
	})
	for id := range target {
		delete(agg.EventCount, id)
	}
	if len(target) > 0 {
		recomputePlanAggStat(agg)
	}
	pc.detachIfEmpty(planID)
}

// removePlan drops a plan's whole aggregate; used by PlanReplace.
func (pc *planCache) removePlan(planID uint64) {
	delete(pc.plans, planID)
}

// removePlanIDs drops a specific set of nodes and bound events from the cache,
// used by the Dream retention sweep (expired nodes cascade their events, but
// the surviving fresh subtree stays). Does not touch the engine.
func (pc *planCache) removePlanIDs(planID uint64, nodeIDs, eventIDs []uint64) {
	agg := pc.plans[planID]
	if agg == nil {
		return
	}
	nodeTarget := make(map[uint64]struct{}, len(nodeIDs))
	for _, id := range nodeIDs {
		nodeTarget[id] = struct{}{}
	}
	agg.Nodes = slices.DeleteFunc(agg.Nodes, func(n core.TrajectorySlot) bool {
		_, ok := nodeTarget[n.IDHash]
		return ok
	})
	for _, id := range nodeIDs {
		delete(agg.EventCount, id)
	}
	if len(eventIDs) > 0 {
		eventTarget := make(map[uint64]struct{}, len(eventIDs))
		for _, id := range eventIDs {
			eventTarget[id] = struct{}{}
		}
		agg.Events = slices.DeleteFunc(agg.Events, func(e core.TrajectorySlot) bool {
			_, ok := eventTarget[e.IDHash]
			return ok
		})
	}
	recomputePlanAggStat(agg)
	pc.detachIfEmpty(planID)
}

// detachIfEmpty drops an aggregate that no longer holds any node so it stops
// appearing in ListPlans (a plan whose whole tree was pruned is gone).
func (pc *planCache) detachIfEmpty(planID uint64) {
	agg := pc.plans[planID]
	if agg == nil || len(agg.Nodes) == 0 {
		delete(pc.plans, planID)
	}
}

// recomputePlanAggStat rescans an aggregate's nodes and events to recompute
// CreatedAt/LastActiveAt/HasNonDone after a node mutation (insert/update/
// branch delete), where statuses and extreme timestamps may have changed.
func recomputePlanAggStat(agg *repo.PlanAggregate) {
	agg.CreatedAt = 0
	agg.LastActiveAt = 0
	agg.HasNonDone = false
	for _, n := range agg.Nodes {
		if agg.CreatedAt == 0 || n.Timestamp < agg.CreatedAt {
			agg.CreatedAt = n.Timestamp
		}
		if n.Timestamp > agg.LastActiveAt {
			agg.LastActiveAt = n.Timestamp
		}
		if n.Status != core.StatusDone {
			agg.HasNonDone = true
		}
	}
	for _, e := range agg.Events {
		if agg.CreatedAt == 0 || e.Timestamp < agg.CreatedAt {
			agg.CreatedAt = e.Timestamp
		}
		if e.Timestamp > agg.LastActiveAt {
			agg.LastActiveAt = e.Timestamp
		}
	}
}
