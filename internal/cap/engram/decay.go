// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L1 network forgetting: RebuildFromL2 drops stale scene nodes and
// DecayNetwork applies exponential decay to node importance and edge
// weights, removing what falls below the configured thresholds.

package engram

import (
	"math"
	"slices"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

type DecayParams struct {
	LambdaNode             float64
	LambdaEdge             float64
	NodeRemoveThreshold    float32
	NodePruneEdgeThreshold float32
	EdgeRemoveThreshold    float32
	MinEdgeNodes           int
}

// DecayReport counts what one decay pass removed. RemovedNodes and
// RemovedEdges are the only two figures it reports; a node whose edges were
// pruned but which survived is not a removal and is not counted.
type DecayReport struct {
	RemovedNodes int
	RemovedEdges int
}

// RebuildFromL2 removes stale L1 nodes (empty TopicIDs, missing first
// topic, or over-deep topics not meeting the keep rule) with their edge
// references; returns the hex IDs of removed nodes.
func RebuildFromL2(engine *core.StorageEngine, agentID uint64, l2Meta *index.L2MetaIndex, cfg *DecayParams) ([]string, error) {
	var updated []string
	for _, node := range core.CollectAllSceneNodes(engine, agentID) {
		stale, err := isNodeStale(&node, engine, agentID, l2Meta)
		if err != nil {
			return updated, err
		}
		if !stale {
			continue
		}
		for _, edgeID := range node.EdgeIDs {
			if _, err := removeNodeFromEdge(engine, agentID, edgeID, node.IDHash, cfg); err != nil {
				return updated, err
			}
		}
		if _, err := engine.DeleteRecord(agentID, node.IDHash); err != nil {
			return updated, err
		}
		updated = append(updated, common.FormatHash(node.IDHash))
	}
	return updated, nil
}

func isNodeStale(node *core.SceneNode, engine *core.StorageEngine, agentID uint64, l2Meta *index.L2MetaIndex) (bool, error) {
	if len(node.TopicIDs) == 0 {
		return true, nil
	}
	firstID := node.TopicIDs[0]
	if firstID == 0 || !engine.Contains(agentID, firstID) {
		return true, nil
	}
	meta := l2Meta.Get(firstID)
	if meta == nil {
		return false, nil
	}
	if meta.Depth <= 2 {
		return false, nil
	}
	keep, err := keepDeepNode(firstID, meta, engine, agentID, l2Meta)
	if err != nil {
		return false, err
	}
	return !keep, nil
}

// keepDeepNode keeps depth-3 nodes whose parent topic is depth <= 2
// (compression-group nodes stay visible while the parent is retrievable).
func keepDeepNode(topicID uint64, meta *index.L2Meta, engine *core.StorageEngine, agentID uint64, l2Meta *index.L2MetaIndex) (bool, error) {
	if meta.Depth != 3 {
		return false, nil
	}
	topic, err := core.ReadTopicLenient(engine, agentID, topicID)
	switch {
	case err != nil && common.CodeOf(err) != common.ErrNotFound:
		// A topic that cannot be read is not a topic that went away, and the
		// answer here is used to delete an L1 node: stop the pass instead.
		return false, err
	case err != nil, topic == nil, topic.ParentID == nil:
		// Gone, or the id names something that is not a topic: the node has no
		// depth-3 topic standing for it.
		return false, nil
	}
	parentMeta := l2Meta.Get(*topic.ParentID)
	return parentMeta != nil && parentMeta.Depth <= 2, nil
}

// DecayNetwork decays node and edge weights exponentially: nodes first
// (below threshold removed, below prune threshold edges cleared), then
// propagates cleared edges, then decays the remaining edges.
func DecayNetwork(engine *core.StorageEngine, agentID uint64, l2Meta *index.L2MetaIndex, cfg *DecayParams) (*DecayReport, error) {
	nowMs := time.Now().UnixMilli()
	report := &DecayReport{}
	removedNodeIDs, clearedEdges, err := decayNodes(engine, agentID, l2Meta, cfg, nowMs, report)
	if err != nil {
		return report, err
	}
	if err := propagateClearedEdges(engine, agentID, cfg, clearedEdges, report); err != nil {
		return report, err
	}
	if err := decayRemainingEdges(engine, agentID, cfg, removedNodeIDs, nowMs, report); err != nil {
		return report, err
	}
	return report, nil
}

func decayNodes(engine *core.StorageEngine, agentID uint64, l2Meta *index.L2MetaIndex, cfg *DecayParams, nowMs int64, report *DecayReport) (map[uint64]bool, map[uint64]map[uint64]bool, error) {
	removedNodeIDs := make(map[uint64]bool)
	clearedEdges := make(map[uint64]map[uint64]bool)
	for _, node := range core.CollectAllSceneNodes(engine, agentID) {
		if skipDeepNode(&node, l2Meta) {
			continue
		}
		if err := decayOneNode(engine, agentID, cfg, &node, nowMs, report, removedNodeIDs, clearedEdges); err != nil {
			return removedNodeIDs, clearedEdges, err
		}
	}
	return removedNodeIDs, clearedEdges, nil
}

// skipDeepNode: nodes deeper than 2 are managed by compression, not decay.
func skipDeepNode(node *core.SceneNode, l2Meta *index.L2MetaIndex) bool {
	if len(node.TopicIDs) == 0 {
		return false
	}
	meta := l2Meta.Get(node.TopicIDs[0])
	return meta != nil && meta.Depth > 2
}

func decayOneNode(engine *core.StorageEngine, agentID uint64, cfg *DecayParams, node *core.SceneNode, nowMs int64, report *DecayReport, removedNodeIDs map[uint64]bool, clearedEdges map[uint64]map[uint64]bool) error {
	dtHours := common.ElapsedHours(nowMs, node.UpdatedAt)
	lambda := applyEmotionalBoost(cfg.LambdaNode, node.Valence, node.Arousal)
	newImportance := node.Importance * float32(math.Exp(-lambda*dtHours))
	if newImportance < cfg.NodeRemoveThreshold {
		if _, err := engine.DeleteRecord(agentID, node.IDHash); err != nil {
			return err
		}
		removedNodeIDs[node.IDHash] = true
		report.RemovedNodes++
		return nil
	}
	node.Importance = newImportance
	if newImportance < cfg.NodePruneEdgeThreshold {
		for _, edgeID := range node.EdgeIDs {
			if clearedEdges[edgeID] == nil {
				clearedEdges[edgeID] = make(map[uint64]bool)
			}
			clearedEdges[edgeID][node.IDHash] = true
		}
		node.EdgeIDs = nil
	}
	// The clock is read and re-based in the same step, so a node's importance
	// accrues decay per Dream interval, not from its last activity. A node the
	// sync pass restamped because its scene gained a turn lands here with dt≈0:
	// a scene still being lived through is not faded by the pass that saw it grow.
	node.UpdatedAt = nowMs
	if err := core.WriteSceneNode(engine, agentID, node.IDHash, node); err != nil {
		return err
	}
	return nil
}

func propagateClearedEdges(engine *core.StorageEngine, agentID uint64, cfg *DecayParams, clearedEdges map[uint64]map[uint64]bool, report *DecayReport) error {
	for edgeID, nodeIDs := range clearedEdges {
		for nodeID := range nodeIDs {
			gone, err := removeNodeFromEdge(engine, agentID, edgeID, nodeID, cfg)
			if err != nil {
				return err
			}
			if gone {
				report.RemovedEdges++
			}
		}
	}
	return nil
}

func decayRemainingEdges(engine *core.StorageEngine, agentID uint64, cfg *DecayParams, removedNodeIDs map[uint64]bool, nowMs int64, report *DecayReport) error {
	entries := slices.Collect(engine.IndexByType(agentID, core.RecL1Hyperedge))
	for _, idHash := range entries {
		edge, err := core.ReadSceneEdge(engine, agentID, idHash)
		if err != nil {
			// The index names this edge, so not being able to read it must not
			// decay into "nothing to do" — that would leave a live edge at full
			// weight while the report claims the sweep ran.
			if common.CodeOf(err) == common.ErrNotFound {
				continue
			}
			return err
		}
		if err := decayOneEdge(engine, agentID, cfg, edge, idHash, removedNodeIDs, nowMs, report); err != nil {
			return err
		}
	}
	return nil
}

// decayOneEdge decays the edge weight incrementally from the last decay
// time, drops members that are no longer nodes of this domain (removed by this
// pass or by an out-of-band delete), and deletes the edge when it falls below
// MinEdgeNodes or the weight threshold.
func decayOneEdge(engine *core.StorageEngine, agentID uint64, cfg *DecayParams, edge *core.SceneEdge, idHash uint64, removedNodeIDs map[uint64]bool, nowMs int64, report *DecayReport) error {
	baseMs := edge.LastDecayAt
	if baseMs == 0 {
		baseMs = edge.CreatedAt
	}
	dtHours := common.ElapsedHours(nowMs, baseMs)
	newWeight := edge.Weight * float32(math.Exp(-cfg.LambdaEdge*dtHours))

	// A member is gone either because this pass removed it or because something
	// outside it did — a scene delete takes its node with it between two Dreams.
	// Either way the edge is not a co-occurrence of nodes this domain holds.
	edge.NodeIDs = slices.DeleteFunc(edge.NodeIDs, func(id uint64) bool {
		return removedNodeIDs[id] || !engine.Contains(agentID, id)
	})

	if len(edge.NodeIDs) < cfg.MinEdgeNodes || newWeight < cfg.EdgeRemoveThreshold {
		for _, nodePtr := range edge.NodeIDs {
			if err := removeEdgeFromNode(engine, agentID, nodePtr, idHash); err != nil {
				return err
			}
		}
		if _, err := engine.DeleteRecord(agentID, idHash); err != nil {
			return err
		}
		report.RemovedEdges++
		return nil
	}
	edge.Weight = newWeight
	edge.LastDecayAt = nowMs
	return core.WriteSceneEdge(engine, agentID, idHash, edge)
}

// removeNodeFromEdge removes a node from an edge; when the edge falls below
// MinEdgeNodes it is deleted and its refs are cleared from other nodes.
// Returns whether the edge was deleted.
func removeNodeFromEdge(engine *core.StorageEngine, agentID uint64, edgeID, nodeID uint64, cfg *DecayParams) (bool, error) {
	edge, err := core.ReadSceneEdge(engine, agentID, edgeID)
	if err != nil {
		// An edge that is gone has no member list left to prune; one that cannot be
		// read is a real failure and must stop the cascade rather than leaving the
		// node holding a reference to an edge nobody trimmed.
		if common.CodeOf(err) == common.ErrNotFound {
			return false, nil
		}
		return false, err
	}
	before := len(edge.NodeIDs)
	edge.NodeIDs = slices.DeleteFunc(edge.NodeIDs, func(id uint64) bool { return id == nodeID })
	if len(edge.NodeIDs) == before {
		return false, nil
	}
	if len(edge.NodeIDs) < cfg.MinEdgeNodes {
		for _, surviving := range edge.NodeIDs {
			if err := removeEdgeFromNode(engine, agentID, surviving, edgeID); err != nil {
				return false, err
			}
		}
		if _, err := engine.DeleteRecord(agentID, edgeID); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, core.WriteSceneEdge(engine, agentID, edgeID, edge)
}

func removeEdgeFromNode(engine *core.StorageEngine, agentID uint64, nodeID, edgeID uint64) error {
	node, err := core.ReadSceneNode(engine, agentID, nodeID)
	if err != nil {
		// A node that is gone has no edge list left to prune; one that cannot be
		// read is a real failure and must stop the cascade rather than leaving a
		// node pointing at a deleted edge.
		if common.CodeOf(err) == common.ErrNotFound {
			return nil
		}
		return err
	}
	before := len(node.EdgeIDs)
	node.EdgeIDs = slices.DeleteFunc(node.EdgeIDs, func(id uint64) bool { return id == edgeID })
	if len(node.EdgeIDs) == before {
		return nil
	}
	return core.WriteSceneNode(engine, agentID, nodeID, node)
}

// applyEmotionalBoost: stronger emotions (|valence|×arousal) decay slower;
// the result is never negative.
func applyEmotionalBoost(baseLambda float64, valence, arousal float64) float64 {
	result := baseLambda - math.Abs(valence)*arousal*2.0
	if result < 0 {
		return 0
	}
	return result
}
