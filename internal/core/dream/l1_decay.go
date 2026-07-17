// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package dream

import (
	"encoding/json"
	"math"

	"memhop/internal/core"
	"memhop/internal/core/index"
	"memhop/internal/core/model"
	"memhop/internal/core/storage"
	"memhop/internal/timeutil"
)

// L1DecayReport holds metrics from the L1 decay stage.
type L1DecayReport struct {
	DecayedNodes int
	PrunedEdges  int
	RemovedNodes int
	RemovedEdges int
}

// DecayL1Network runs time-based exponential decay on L1 nodes and edges.
func DecayL1Network(
	engine *storage.StorageEngine,
	cfg *DecayParams,
	l2Meta *index.L2MetaIndex,
	sparseIdx *index.SparseIndex,
) (*L1DecayReport, error) {
	nowMs := timeutil.NowMs()
	report := &L1DecayReport{}

	removedNodeIDs, clearedEdges, err := decayNodes(engine, cfg, l2Meta, sparseIdx, nowMs, report)
	if err != nil {
		return report, err
	}
	if err := processClearedEdges(engine, cfg, clearedEdges, report); err != nil {
		return report, err
	}
	if err := decayRemainingEdges(engine, cfg, removedNodeIDs, nowMs, report); err != nil {
		return report, err
	}
	return report, nil
}

// DecayParams holds the decay configuration values used by L1 decay.
type DecayParams struct {
	LambdaNode             float64
	LambdaEdge             float64
	NodeRemoveThreshold    float32
	NodePruneEdgeThreshold float32
	EdgeRemoveThreshold    float32
	MinEdgeNodes           int
}

// DecayParamsFromConfig converts core.DecayConfig to DecayParams.
func DecayParamsFromConfig(cfg *core.DecayConfig) *DecayParams {
	if cfg == nil {
		return &DecayParams{
			LambdaNode:             0.01,
			LambdaEdge:             0.02,
			NodeRemoveThreshold:    0.05,
			NodePruneEdgeThreshold: 0.15,
			EdgeRemoveThreshold:    0.05,
			MinEdgeNodes:           2,
		}
	}
	return &DecayParams{
		LambdaNode:             float64(cfg.LambdaNode),
		LambdaEdge:             float64(cfg.LambdaEdge),
		NodeRemoveThreshold:    cfg.NodeRemoveThreshold,
		NodePruneEdgeThreshold: cfg.NodePruneEdgesThreshold,
		EdgeRemoveThreshold:    cfg.EdgeRemoveThreshold,
		MinEdgeNodes:           cfg.MinEdgeNodes,
	}
}

func decayNodes(
	engine *storage.StorageEngine,
	cfg *DecayParams,
	l2Meta *index.L2MetaIndex,
	sparseIdx *index.SparseIndex,
	nowMs int64,
	report *L1DecayReport,
) (map[uint64]bool, map[uint64]map[uint64]bool, error) {
	removedNodeIDs := make(map[uint64]bool)
	clearedEdges := make(map[uint64]map[uint64]bool)

	var entries []uint64
	engine.IterIndex(func(idHash, _ uint64) bool {
		entries = append(entries, idHash)
		return true
	})

	for _, idHash := range entries {
		node := readSceneNode(engine, idHash)
		if node == nil {
			continue
		}
		if skipDeepNode(node, l2Meta) {
			continue
		}
		if err := processNodeDecay(engine, cfg, node, idHash, sparseIdx, nowMs, report, removedNodeIDs, clearedEdges); err != nil {
			return removedNodeIDs, clearedEdges, err
		}
	}
	return removedNodeIDs, clearedEdges, nil
}

func skipDeepNode(node *model.SceneNode, l2Meta *index.L2MetaIndex) bool {
	if len(node.TopicIDs) == 0 {
		return false
	}
	meta := l2Meta.Get(node.TopicIDs[0])
	return meta != nil && meta.Depth > 2
}

func processNodeDecay(
	engine *storage.StorageEngine,
	cfg *DecayParams,
	node *model.SceneNode,
	idHash uint64,
	sparseIdx *index.SparseIndex,
	nowMs int64,
	report *L1DecayReport,
	removedNodeIDs map[uint64]bool,
	clearedEdges map[uint64]map[uint64]bool,
) error {
	dtHours := dtHoursFrom(nowMs, node.UpdatedAt)
	lambda := ApplyEmotionalBoost(cfg.LambdaNode, node.Valence, node.Arousal)
	newImportance := node.Importance * float32(math.Exp(-lambda*dtHours))
	if newImportance < cfg.NodeRemoveThreshold {
		_, err := engine.DeleteRecord(idHash)
		if err != nil {
			return err
		}
		sparseIdx.RemoveDocument(idHash)
		removedNodeIDs[idHash] = true
		report.RemovedNodes++
		return nil
	}

	node.Importance = newImportance
	if newImportance < cfg.NodePruneEdgeThreshold {
		report.PrunedEdges += len(node.EdgeIDs)
		for _, edgeID := range node.EdgeIDs {
			if clearedEdges[edgeID] == nil {
				clearedEdges[edgeID] = make(map[uint64]bool)
			}
			clearedEdges[edgeID][idHash] = true
		}
		node.EdgeIDs = nil
	}
	node.UpdatedAt = nowMs
	if err := writeSceneNodeRecord(engine, idHash, node); err != nil {
		return err
	}
	report.DecayedNodes++
	return nil
}

func processClearedEdges(
	engine *storage.StorageEngine,
	cfg *DecayParams,
	clearedEdges map[uint64]map[uint64]bool,
	report *L1DecayReport,
) error {
	edgesRemoved := make(map[uint64]bool)
	for edgeID, nodeIDs := range clearedEdges {
		for nodeID := range nodeIDs {
			removed, err := removeNodeFromEdge(engine, edgeID, nodeID, cfg)
			if err != nil {
				return err
			}
			if removed {
				edgesRemoved[edgeID] = true
				report.RemovedEdges++
			}
		}
	}
	return nil
}

func decayRemainingEdges(
	engine *storage.StorageEngine,
	cfg *DecayParams,
	removedNodeIDs map[uint64]bool,
	nowMs int64,
	report *L1DecayReport,
) error {
	entries := collectEdgeEntries(engine)
	for _, idHash := range entries {
		edge := readSceneEdge(engine, idHash)
		if edge == nil {
			continue
		}
		if err := decayOneEdge(engine, cfg, edge, idHash, removedNodeIDs, nowMs, report); err != nil {
			return err
		}
	}
	return nil
}

func collectEdgeEntries(engine *storage.StorageEngine) []uint64 {
	var entries []uint64
	engine.IterIndex(func(idHash, _ uint64) bool {
		entries = append(entries, idHash)
		return true
	})
	return entries
}

func decayOneEdge(
	engine *storage.StorageEngine,
	cfg *DecayParams,
	edge *model.SceneEdge,
	idHash uint64,
	removedNodeIDs map[uint64]bool,
	nowMs int64,
	report *L1DecayReport,
) error {
	// Decay incrementally since the last decay run; old records without a
	// LastDecayAt timestamp decay from CreatedAt on their first run.
	baseMs := edge.LastDecayAt
	if baseMs == 0 {
		baseMs = edge.CreatedAt
	}
	dtHours := dtHoursFrom(nowMs, baseMs)
	newWeight := edge.Weight * float32(math.Exp(-cfg.LambdaEdge*dtHours))

	// Clean references to removed nodes.
	filtered := edge.NodeIDs[:0]
	for _, ptr := range edge.NodeIDs {
		if !removedNodeIDs[ptr] {
			filtered = append(filtered, ptr)
		}
	}
	edge.NodeIDs = filtered

	if len(edge.NodeIDs) < cfg.MinEdgeNodes || newWeight < cfg.EdgeRemoveThreshold {
		for _, nodePtr := range edge.NodeIDs {
			if err := removeEdgeFromNode(engine, nodePtr, idHash); err != nil {
				return err
			}
		}
		_, err := engine.DeleteRecord(idHash)
		if err != nil {
			return err
		}
		report.RemovedEdges++
		return nil
	}

	edge.Weight = newWeight
	edge.LastDecayAt = nowMs
	return writeSceneEdge(engine, idHash, edge)
}

func removeNodeFromEdge(
	engine *storage.StorageEngine,
	edgeID, nodeID uint64,
	cfg *DecayParams,
) (bool, error) {
	edge := readSceneEdge(engine, edgeID)
	if edge == nil {
		return false, nil
	}
	found := false
	filtered := edge.NodeIDs[:0]
	for _, n := range edge.NodeIDs {
		if n == nodeID {
			found = true
		} else {
			filtered = append(filtered, n)
		}
	}
	if !found {
		return false, nil
	}
	edge.NodeIDs = filtered
	if len(edge.NodeIDs) < cfg.MinEdgeNodes {
		for _, survivingNode := range edge.NodeIDs {
			if err := removeEdgeFromNode(engine, survivingNode, edgeID); err != nil {
				return false, err
			}
		}
		_, err := engine.DeleteRecord(edgeID)
		if err != nil {
			return false, err
		}
		return true, nil
	}
	return false, writeSceneEdge(engine, edgeID, edge)
}

func writeSceneNodeRecord(engine *storage.StorageEngine, id uint64, node *model.SceneNode) error {
	data, err := json.Marshal(node)
	if err != nil {
		return err
	}
	_, err = engine.WriteRecord(storage.RecL1SceneNode, id, data)
	return err
}

func dtHoursFrom(nowMs, updatedAtMs int64) float64 {
	dtMs := nowMs - updatedAtMs
	if dtMs < 0 {
		dtMs = 0
	}
	return float64(dtMs) / 3_600_000.0
}
