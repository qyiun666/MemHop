// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L5 plan-node primitives: write one node, batch delete by id or by owning
// topic, and group a domain's nodes into per-topic aggregates. L5 holds nothing
// but plan nodes — a turn's events are L4 content beside its dialogue originals.
// The tree view and the retention sweep go through the domain's PlanCache in the
// internal layer, which owns every plan write and delete under the domain lock.
package repo

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// WritePlanNode writes one node record, preserving its caller-derived IDHash
// (core.HashPlanNode(node.TopicID, nodePath)) so the node's address stays stable
// across writes. A node has no id a caller may invent: it is derived from the
// topic and the path, and a mismatch is refused rather than written sideways.
func WritePlanNode(engine *core.StorageEngine, agentID uint64, node *core.PlanNode) (uint64, error) {
	if node == nil {
		return 0, common.NewError(common.ErrInvalidQuery, "plan node is nil")
	}
	if node.IDHash == 0 {
		return 0, common.NewError(common.ErrInvalidQuery, "plan node id required")
	}
	if node.IDHash != core.HashPlanNode(node.TopicID, node.NodePath) {
		return 0, common.NewError(common.ErrInvalidQuery, "plan node id does not match topic/nodePath")
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
	Nodes        []core.PlanNode // NodePath-ascending
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
			return CompareNodePath(a.NodePath, b.NodePath)
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
