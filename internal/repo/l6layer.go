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
// IDHash (core.HashPlanNode(node.SessionID, nodePath)) so the node reference
// stays stable across writes. Unlike AppendTrajectory it does NOT re-hash the id.
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
	if node.IDHash != core.HashPlanNode(node.SessionID, node.NodePath) {
		return 0, common.NewError(common.ErrInvalidQuery, "plan node id does not match topic/nodePath")
	}
	if err := core.WriteTrajectorySlot(engine, agentID, node.IDHash, node); err != nil {
		return 0, err
	}
	return node.IDHash, nil
}

// PlanAggregate is one turn's plan footprint, computed in a single scan of the
// domain's L6 records (no per-node rescans).
type PlanAggregate struct {
	TopicID      uint64
	Nodes        []core.TrajectorySlot // NodeTypePlan, sorted by (Seq, NodePath)
	EventCount   map[uint64]int        // node IDHash -> bound event count
	Events       []core.TrajectorySlot // every node-bound event (cascade sweeps need the refs)
	CreatedAt    int64                 // earliest record timestamp (Unix ms)
	LastActiveAt int64                 // latest node/event timestamp (Unix ms)
	HasNonDone   bool                  // any node carries a non-Done status
}

// CollectPlanAggregates groups every plan's nodes and bound events in ONE pass
// over the agent domain's L6 records, keyed by the turn that opened the plan.
// A record joins a plan only when it is a node or hangs on one: a bare turn
// event references no node and belongs to no tree. A key whose nodes have all
// expired yields no aggregate — a plan with no nodes is gone. Result is
// TopicID-ascending for determinism.
func CollectPlanAggregates(engine *core.StorageEngine, agentID uint64) []PlanAggregate {
	byTopic := make(map[uint64]*PlanAggregate)
	for _, ev := range core.CollectAllTrajectories(engine, agentID) {
		if ev.NodeType != core.NodeTypePlan && ev.PlanNodeRef == 0 {
			continue
		}
		agg := byTopic[ev.SessionID]
		if agg == nil {
			agg = &PlanAggregate{TopicID: ev.SessionID, EventCount: make(map[uint64]int)}
			byTopic[ev.SessionID] = agg
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
	out := make([]PlanAggregate, 0, len(byTopic))
	for _, agg := range byTopic {
		if len(agg.Nodes) == 0 {
			continue
		}
		slices.SortFunc(agg.Nodes, func(a, b core.TrajectorySlot) int {
			return cmp.Or(cmp.Compare(a.Seq, b.Seq), CompareNodePath(a.NodePath, b.NodePath))
		})
		out = append(out, *agg)
	}
	slices.SortFunc(out, func(a, b PlanAggregate) int { return cmp.Compare(a.TopicID, b.TopicID) })
	return out
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
