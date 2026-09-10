// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L5 plan-node primitives: write one node, batch delete by id or by owning
// topic, and group a domain's nodes into per-topic aggregates. L5 holds nothing
// but plan nodes — a turn's events are L4 content beside its dialogue originals.
// The tree view, the subtree walk and the retention sweep go through the domain's
// PlanCache in the internal layer, which owns every plan write and delete under
// the domain lock.
package repo

import (
	"cmp"
	"slices"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// WritePlanNode writes one node record, preserving its caller-derived IDHash
// (core.HashPlanNode(node.TopicID, node.Seq)) so the node's address stays stable
// across writes. A node has no ordinal a caller may invent: the plan cache hands
// it out, the id follows from it, and a mismatch is refused rather than written
// sideways onto another step.
func WritePlanNode(engine *core.StorageEngine, agentID uint64, node *core.PlanNode) (uint64, error) {
	if node == nil {
		return 0, common.NewError(common.ErrInvalidQuery, "plan node is nil")
	}
	if node.Seq == 0 {
		return 0, common.NewError(common.ErrInvalidQuery, "plan node seq required")
	}
	if node.IDHash != core.HashPlanNode(node.TopicID, node.Seq) {
		return 0, common.NewError(common.ErrInvalidQuery, "plan node id does not match topic/seq")
	}
	if err := core.WritePlanNode(engine, agentID, node.IDHash, node); err != nil {
		return 0, err
	}
	return node.IDHash, nil
}

// DeletePlanNodesByIDs batch-deletes plan nodes by record id and returns how
// many were removed.
func DeletePlanNodesByIDs(engine *core.StorageEngine, agentID uint64, idHashes []uint64) (int, error) {
	if len(idHashes) == 0 {
		return 0, nil
	}
	n, err := engine.DeleteRecordBatch(agentID, idHashes)
	if err != nil {
		return 0, common.NewError(common.ErrIO, "delete plan nodes", err)
	}
	return n, nil
}

// DeletePlanNodesByTopicIDs tombstones every plan tree a topic owns. A node has
// no content-side key to hang on, so the owning topic is found by scanning the
// node bucket — the same way a topic's whole subtree is enumerated for a scene or
// topic deletion.
func DeletePlanNodesByTopicIDs(engine *core.StorageEngine, agentID uint64, topics []uint64) (int, error) {
	inTopic := make(map[uint64]struct{}, len(topics))
	for _, id := range topics {
		inTopic[id] = struct{}{}
	}
	var doomed []uint64
	for _, node := range core.CollectAllPlanNodes(engine, agentID) {
		if _, ok := inTopic[node.TopicID]; ok {
			doomed = append(doomed, node.IDHash)
		}
	}
	return DeletePlanNodesByIDs(engine, agentID, doomed)
}

// PlanAggregate is one topic's plan footprint: its whole tree, computed in a
// single pass over the domain's node records. LastActiveAt is the newest node
// timestamp, which is what the retention window reads; HasNonDone marks a tree
// still in flight, which the window exempts while it is also active.
type PlanAggregate struct {
	TopicID      uint64
	Nodes        []core.PlanNode // Seq-ascending, the order they were created in
	LastActiveAt int64
	HasNonDone   bool
}

// CollectPlanNodes groups every plan node of one agent domain by the turn topic
// that opened it. A key yields an aggregate exactly while at least one of its
// nodes lives: a plan whose whole tree has been pruned is gone. Result is
// TopicID-ascending for determinism.
func CollectPlanNodes(engine *core.StorageEngine, agentID uint64) []PlanAggregate {
	byTopic := make(map[uint64]*PlanAggregate)
	for _, node := range core.CollectAllPlanNodes(engine, agentID) {
		agg := byTopic[node.TopicID]
		if agg == nil {
			agg = &PlanAggregate{TopicID: node.TopicID}
			byTopic[node.TopicID] = agg
		}
		agg.Nodes = append(agg.Nodes, node)
	}
	out := make([]PlanAggregate, 0, len(byTopic))
	for _, agg := range byTopic {
		slices.SortFunc(agg.Nodes, func(a, b core.PlanNode) int {
			return cmp.Compare(a.Seq, b.Seq)
		})
		for _, n := range agg.Nodes {
			if n.UpdatedAt > agg.LastActiveAt {
				agg.LastActiveAt = n.UpdatedAt
			}
			if n.Status != core.StatusDone {
				agg.HasNonDone = true
			}
		}
		out = append(out, *agg)
	}
	slices.SortFunc(out, func(a, b PlanAggregate) int { return cmp.Compare(a.TopicID, b.TopicID) })
	return out
}
