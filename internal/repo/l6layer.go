// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L6 trajectory record primitives: append one event, batch delete by id.
// Reads, listing, pruning and topic aggregation run through the domain's
// TrajIndex in the internal layer, which owns every trajectory write and
// delete under the same domain lock.
package repo

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// AppendTrajectory writes one trajectory event; ID = hash(sessionID:seq).
// Re-writing the same sessionID+seq points the index at the newest record
// (append-only upsert). Returns the assigned record id.
func AppendTrajectory(engine *core.StorageEngine, agentID uint64, ev core.TrajectorySlot) (uint64, error) {
	ev.IDHash = common.HashID(fmt.Sprintf("%d:%d", ev.SessionID, ev.Seq))
	if err := core.WriteTrajectorySlot(engine, agentID, ev.IDHash, &ev); err != nil {
		return 0, err
	}
	return ev.IDHash, nil
}

// DeleteTrajectoryByIDs batch-deletes trajectory events by record id and
// returns how many were removed.
func DeleteTrajectoryByIDs(engine *core.StorageEngine, agentID uint64, idHashes []uint64) (int, error) {
	if len(idHashes) == 0 {
		return 0, nil
	}
	n, err := engine.DeleteRecordBatch(agentID, idHashes)
	if err != nil {
		return 0, common.NewError(common.ErrIO, "delete trajectory", err)
	}
	return n, nil
}

// WritePlanNode writes one plan-node record, preserving its caller-derived
// IDHash (core.HashPlanNode(planID, nodePath)) so the node reference stays
// stable across writes. Unlike AppendTrajectory it does NOT re-hash the id.
func WritePlanNode(engine *core.StorageEngine, agentID uint64, node *core.TrajectorySlot) (uint64, error) {
	if node == nil {
		return 0, common.NewError(common.ErrInvalidQuery, "plan node is nil")
	}
	if node.IDHash == 0 {
		return 0, common.NewError(common.ErrInvalidQuery, "plan node id required")
	}
	if node.NodeType != core.NodeTypePlan {
		return 0, common.NewError(common.ErrInvalidQuery, "WritePlanNode requires NodeTypePlan")
	}
	if node.IDHash != core.HashPlanNode(node.PlanID, node.NodePath) {
		return 0, common.NewError(common.ErrInvalidQuery, "plan node id does not match planID/nodePath")
	}
	if err := core.WriteTrajectorySlot(engine, agentID, node.IDHash, node); err != nil {
		return 0, err
	}
	return node.IDHash, nil
}

// CollectPlanNodes returns the plan-node records of one plan (any NodePath),
// sorted by Seq; events are excluded. Callers group the tree.
func CollectPlanNodes(engine *core.StorageEngine, agentID uint64, planID uint64) []core.TrajectorySlot {
	var out []core.TrajectorySlot
	for _, ev := range core.CollectAllTrajectories(engine, agentID) {
		if ev.NodeType != core.NodeTypePlan || ev.PlanID != planID {
			continue
		}
		out = append(out, ev)
	}
	slices.SortFunc(out, func(a, b core.TrajectorySlot) int {
		return cmp.Or(cmp.Compare(a.Seq, b.Seq), CompareNodePath(a.NodePath, b.NodePath))
	})
	return out
}

// PlanAggregate is one plan's stored footprint, computed in a single scan of
// the domain's L6 records (no per-node rescans).
type PlanAggregate struct {
	PlanID       uint64
	Nodes        []core.TrajectorySlot // NodeTypePlan, sorted by (Seq, NodePath)
	EventCount   map[uint64]int        // node IDHash -> bound event count
	Events       []core.TrajectorySlot // every bound event (cascade sweeps need the refs)
	CreatedAt    int64                 // earliest node timestamp (Unix ms)
	LastActiveAt int64                 // latest node/event timestamp (Unix ms)
	HasNonDone   bool                  // any node carries a non-Done status
}

// CollectPlanAggregates groups every plan's nodes and bound events in ONE
// pass over the agent domain's L6 records; bare turn events (PlanID==0) are
// excluded. Result is PlanID-ascending for determinism.
func CollectPlanAggregates(engine *core.StorageEngine, agentID uint64) []PlanAggregate {
	byPlan := make(map[uint64]*PlanAggregate)
	for _, ev := range core.CollectAllTrajectories(engine, agentID) {
		if ev.PlanID == 0 {
			continue
		}
		agg := byPlan[ev.PlanID]
		if agg == nil {
			agg = &PlanAggregate{PlanID: ev.PlanID, EventCount: make(map[uint64]int)}
			byPlan[ev.PlanID] = agg
		}
		// Node timestamps are refreshed on every commit, so the earliest
		// record across nodes AND bound events marks the plan's creation.
		if agg.CreatedAt == 0 || ev.Timestamp < agg.CreatedAt {
			agg.CreatedAt = ev.Timestamp
		}
		if ev.Timestamp > agg.LastActiveAt {
			agg.LastActiveAt = ev.Timestamp
		}
		switch ev.NodeType {
		case core.NodeTypePlan:
			agg.Nodes = append(agg.Nodes, ev)
			if ev.Status != core.StatusDone {
				agg.HasNonDone = true
			}
		case core.NodeTypeEvent:
			agg.Events = append(agg.Events, ev)
			agg.EventCount[ev.PlanNodeRef]++
		}
	}
	out := make([]PlanAggregate, 0, len(byPlan))
	for _, agg := range byPlan {
		slices.SortFunc(agg.Nodes, func(a, b core.TrajectorySlot) int {
			return cmp.Or(cmp.Compare(a.Seq, b.Seq), CompareNodePath(a.NodePath, b.NodePath))
		})
		out = append(out, *agg)
	}
	slices.SortFunc(out, func(a, b PlanAggregate) int { return cmp.Compare(a.PlanID, b.PlanID) })
	return out
}

// DeletePlanRecords removes one plan's nodes and bound events in a single
// scan and returns how many records were removed; unknown plans remove
// nothing (idempotent).
func DeletePlanRecords(engine *core.StorageEngine, agentID, planID uint64) (int, error) {
	var ids []uint64
	for _, ev := range core.CollectAllTrajectories(engine, agentID) {
		if ev.PlanID == planID {
			ids = append(ids, ev.IDHash)
		}
	}
	return DeleteTrajectoryByIDs(engine, agentID, ids)
}

// DeletePlanNodeBranch removes one plan node and its whole descendant subtree
// along with every event bound to those nodes (PlanNodeRef within the branch).
// nodePath matches itself and any "nodePath.N..." descendant; unknown paths or
// plans remove nothing (idempotent).
func DeletePlanNodeBranch(engine *core.StorageEngine, agentID, planID uint64, nodePath string) (int, error) {
	if nodePath == "" {
		return 0, common.NewError(common.ErrInvalidQuery, "nodePath required")
	}
	prefix := nodePath + "."
	var nodeIDs []uint64
	for _, ev := range core.CollectAllTrajectories(engine, agentID) {
		if ev.PlanID != planID || ev.NodeType != core.NodeTypePlan {
			continue
		}
		if ev.NodePath == nodePath || strings.HasPrefix(ev.NodePath, prefix) {
			nodeIDs = append(nodeIDs, ev.IDHash)
		}
	}
	if len(nodeIDs) == 0 {
		return 0, nil
	}
	target := make(map[uint64]struct{}, len(nodeIDs))
	for _, id := range nodeIDs {
		target[id] = struct{}{}
	}
	delIDs := append([]uint64(nil), nodeIDs...)
	for _, ev := range core.CollectAllTrajectories(engine, agentID) {
		if ev.NodeType != core.NodeTypeEvent || ev.PlanID != planID {
			continue
		}
		if _, ok := target[ev.PlanNodeRef]; ok {
			delIDs = append(delIDs, ev.IDHash)
		}
	}
	return DeleteTrajectoryByIDs(engine, agentID, delIDs)
}

// CompareNodePath compares two node-path strings ("1", "1.2.1") numerically
// segment by segment, so "1.10" sorts after "1.9" (not lexicographically
// where "1.10" < "1.9"). Tie-breaks on length for equal numeric prefixes.
func CompareNodePath(a, b string) int {
	as := splitDotSegments(a)
	bs := splitDotSegments(b)
	for i := 0; i < len(as) && i < len(bs); i++ {
		ai, _ := strconv.Atoi(as[i])
		bi, _ := strconv.Atoi(bs[i])
		if ai != bi {
			return cmp.Compare(ai, bi)
		}
	}
	return cmp.Compare(len(as), len(bs))
}

// splitDotSegments splits a node path on '.' returning non-empty numeric
// segments; a path like "1.2.1" yields ["1","2","1"].
func splitDotSegments(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ".")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
