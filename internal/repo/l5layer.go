// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L5 plan-node primitives: write one node, batch delete by id or by owning
// topic, and group a domain's nodes into per-topic aggregates. L5 holds nothing
// but plan nodes — a turn's events are L4 content beside its dialogue originals.
// This package keeps no plan cache and runs no retention sweep.
package repo

import (
	"cmp"
	"slices"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// WritePlanNode writes one node record. The IDHash must already be
// core.HashPlanNode(node.TopicID, node.Seq): the ordinal is handed out
// elsewhere and the id follows from it, so a mismatch is a step written
// sideways onto another address and is refused.
func WritePlanNode(engine *core.StorageEngine, agentID uint64, node *core.PlanNode) error {
	if node == nil {
		return common.NewError(common.ErrInvalidQuery, "plan node is nil")
	}
	if node.Seq == 0 {
		return common.NewError(common.ErrInvalidQuery, "plan node seq required")
	}
	if node.IDHash != core.HashPlanNode(node.TopicID, node.Seq) {
		return common.NewError(common.ErrInvalidQuery, "plan node id does not match topic/seq")
	}
	return core.WritePlanNode(engine, agentID, node.IDHash, node)
}

// DeletePlanNodesByIDs batch-deletes plan nodes by record id. It reads nothing
// and reports no count: the ids come from an enumeration the caller already ran.
func DeletePlanNodesByIDs(engine *core.StorageEngine, agentID uint64, idHashes []uint64) error {
	if _, err := engine.DeleteRecordBatch(agentID, idHashes); err != nil {
		return common.NewError(common.ErrIO, "delete plan nodes", err)
	}
	return nil
}

// PlanNodeIDsByTopicIDs enumerates the record ids of every plan node a listed
// topic owns, found by a strict scan of the node bucket — the result is what a
// cascade tombstones afterwards. This only enumerates: whoever deletes runs
// every enumeration before the first tombstone.
func PlanNodeIDsByTopicIDs(engine *core.StorageEngine, agentID uint64, topics []uint64) ([]uint64, error) {
	if len(topics) == 0 {
		return nil, nil
	}
	nodes, err := core.CollectAllPlanNodesStrict(engine, agentID)
	if err != nil {
		return nil, err
	}
	inTopic := make(map[uint64]struct{}, len(topics))
	for _, id := range topics {
		inTopic[id] = struct{}{}
	}
	var doomed []uint64
	for _, node := range nodes {
		if _, ok := inTopic[node.TopicID]; ok {
			doomed = append(doomed, node.IDHash)
		}
	}
	return doomed, nil
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

// ComparePlanNodeSeq orders plan nodes by the per-turn ordinal they were
// created under — the order a tree reads in.
func ComparePlanNodeSeq(a, b core.PlanNode) int { return cmp.Compare(a.Seq, b.Seq) }

// RecomputePlanAgg rescans an aggregate's two derived figures over its current
// node set. Both start from zero, so a recompute after nodes were removed
// cannot keep a figure whose only source is gone.
func RecomputePlanAgg(agg *PlanAggregate) {
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

// CollectPlanNodes groups one agent domain's plan nodes by the turn topic that
// opened them, reading the node bucket strictly: its output drives the
// retention sweep.
func CollectPlanNodes(engine *core.StorageEngine, agentID uint64) ([]PlanAggregate, error) {
	nodes, err := core.CollectAllPlanNodesStrict(engine, agentID)
	if err != nil {
		return nil, err
	}
	return GroupPlanNodes(nodes), nil
}

// GroupPlanNodes aggregates a caller-supplied node set by owning topic: a topic
// with no node in the set yields no aggregate. Result is TopicID-ascending for
// determinism.
func GroupPlanNodes(nodes []core.PlanNode) []PlanAggregate {
	byTopic := make(map[uint64]*PlanAggregate)
	for _, node := range nodes {
		agg := byTopic[node.TopicID]
		if agg == nil {
			agg = &PlanAggregate{TopicID: node.TopicID}
			byTopic[node.TopicID] = agg
		}
		agg.Nodes = append(agg.Nodes, node)
	}
	out := make([]PlanAggregate, 0, len(byTopic))
	for _, agg := range byTopic {
		slices.SortFunc(agg.Nodes, ComparePlanNodeSeq)
		RecomputePlanAgg(agg)
		out = append(out, *agg)
	}
	slices.SortFunc(out, func(a, b PlanAggregate) int { return cmp.Compare(a.TopicID, b.TopicID) })
	return out
}
