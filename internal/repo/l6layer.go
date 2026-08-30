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
	slices.SortFunc(out, func(a, b core.TrajectorySlot) int { return cmp.Compare(a.Seq, b.Seq) })
	return out
}

// CollectNodeEvents returns the event records (NodeType=event) pointing at a
// plan node via PlanNodeRef, sorted by Seq; nil when none.
func CollectNodeEvents(engine *core.StorageEngine, agentID uint64, nodeID uint64) []core.TrajectorySlot {
	var out []core.TrajectorySlot
	for _, ev := range core.CollectAllTrajectories(engine, agentID) {
		if ev.NodeType != core.NodeTypeEvent || ev.PlanNodeRef != nodeID {
			continue
		}
		out = append(out, ev)
	}
	slices.SortFunc(out, func(a, b core.TrajectorySlot) int { return cmp.Compare(a.Seq, b.Seq) })
	return out
}
