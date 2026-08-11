package repo

// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L1 hypergraph operations: SceneNode writes synced with the L1ReverseIndex.
// Dream calls CreateNodeL1/UpdateNodeL1; search calls FindAssociatedNodesL1.
import (
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
	"github.com/qyiun666/MemHop/internal/sub/repo/index"
)

// CreateNodeL1 writes a SceneNode (ID = hash(sceneID:topics)) and registers
// it in the L1 reverse index.
func CreateNodeL1(engine *core.StorageEngine, l1Idx *index.L1ReverseIndex, sceneID string, topicIDs []uint64) (uint64, error) {
	sceneHash, err := common.ParseID(sceneID)
	if err != nil {
		return 0, common.NewError(common.ErrInvalidQuery, "parse scene id", err)
	}
	nodeID := common.HashID(fmt.Sprintf("%s:%v", sceneID, topicIDs))
	now := time.Now().UnixMilli()
	node := &core.SceneNode{
		IDHash:    nodeID,
		SceneID:   sceneHash,
		TopicIDs:  topicIDs,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := core.WriteSceneNode(engine, nodeID, node); err != nil {
		return 0, err
	}
	l1Idx.Add(sceneHash, nodeID)
	return nodeID, nil
}

// UpdateNodeL1 overwrites the node and re-registers it: old registrations
// are removed from all scenes, then the new SceneID is registered.
func UpdateNodeL1(engine *core.StorageEngine, l1Idx *index.L1ReverseIndex, id string, slot *core.SceneNode) error {
	idHash, err := common.ParseID(id)
	if err != nil {
		return common.NewError(common.ErrInvalidQuery, "parse node id", err)
	}
	if _, err := core.ReadSceneNode(engine, idHash); err != nil {
		return err
	}
	slot.IDHash = idHash
	slot.UpdatedAt = time.Now().UnixMilli()
	if err := core.WriteSceneNode(engine, idHash, slot); err != nil {
		return err
	}
	l1Idx.RemoveNode(idHash)
	l1Idx.Add(slot.SceneID, idHash)
	return nil
}

func RebuildIndexL1(engine *core.StorageEngine) *index.L1ReverseIndex {
	return index.BuildL1ReverseIndex(engine)
}

func ListNodesL1(engine *core.StorageEngine, sceneID *string) []core.SceneNode {
	var sceneHash uint64
	filter := false
	if sceneID != nil {
		h, err := common.ParseID(*sceneID)
		if err != nil {
			return nil
		}
		sceneHash = h
		filter = true
	}
	var out []core.SceneNode
	for _, node := range core.CollectAllSceneNodes(engine) {
		if filter && node.SceneID != sceneHash {
			continue
		}
		out = append(out, node)
	}
	return out
}

// FindAssociatedNodesL1 looks up nodes for the given scenes via the L1
// reverse index; callers read node.TopicIDs for the associated contexts.
func FindAssociatedNodesL1(engine *core.StorageEngine, l1Idx *index.L1ReverseIndex, sceneIDs []string) []core.SceneNode {
	ctxSet := make(map[uint64]struct{}, len(sceneIDs))
	for _, sid := range sceneIDs {
		h, err := common.ParseID(sid)
		if err != nil {
			continue
		}
		ctxSet[h] = struct{}{}
	}
	var out []core.SceneNode
	for _, nodeID := range l1Idx.FindAssociated(ctxSet) {
		node, err := core.ReadSceneNode(engine, nodeID)
		if err != nil {
			continue
		}
		out = append(out, *node)
	}
	return out
}

type DecayParams struct {
	LambdaNode             float64
	LambdaEdge             float64
	NodeRemoveThreshold    float32
	NodePruneEdgeThreshold float32
	EdgeRemoveThreshold    float32
	MinEdgeNodes           int
}

type L1DecayReport struct {
	DecayedNodes int
	PrunedEdges  int
	RemovedNodes int
	RemovedEdges int
}

// RebuildL1FromL2 removes stale L1 nodes (empty TopicIDs, missing first
// topic, or over-deep topics not meeting the keep rule) with their edge
// references; returns the hex IDs of removed nodes.
func RebuildL1FromL2(engine *core.StorageEngine, l2Meta *index.L2MetaIndex, cfg *DecayParams) ([]string, error) {
	var updated []string
	for _, node := range core.CollectAllSceneNodes(engine) {
		if !isNodeStale(&node, engine, l2Meta) {
			continue
		}
		for _, edgeID := range node.EdgeIDs {
			if _, err := removeNodeFromEdge(engine, edgeID, node.IDHash, cfg); err != nil {
				return updated, err
			}
		}
		if _, err := engine.DeleteRecord(node.IDHash); err != nil {
			return updated, err
		}
		updated = append(updated, common.FormatHash(node.IDHash))
	}
	return updated, nil
}

func isNodeStale(node *core.SceneNode, engine *core.StorageEngine, l2Meta *index.L2MetaIndex) bool {
	if len(node.TopicIDs) == 0 {
		return true
	}
	firstID := node.TopicIDs[0]
	if firstID == 0 || !engine.Contains(firstID) {
		return true
	}
	meta := l2Meta.Get(firstID)
	if meta == nil {
		return false
	}
	if meta.Depth <= 2 {
		return false
	}
	return !keepDeepNode(node, firstID, meta, engine, l2Meta)
}

// keepDeepNode keeps depth-3 nodes whose parent topic is depth <= 2
// (compression-group nodes stay visible while the parent is retrievable).
func keepDeepNode(node *core.SceneNode, topicID uint64, meta *index.L2Meta, engine *core.StorageEngine, l2Meta *index.L2MetaIndex) bool {
	if meta.Depth != 3 {
		return false
	}
	topic, err := core.ReadTopicLenient(engine, topicID)
	if err != nil || topic == nil || topic.ParentID == nil {
		return false
	}
	parentMeta := l2Meta.Get(*topic.ParentID)
	return parentMeta != nil && parentMeta.Depth <= 2
}

// DecayL1Network decays node and edge weights exponentially: nodes first
// (below threshold removed, below prune threshold edges cleared), then
// propagates cleared edges, then decays the remaining edges.
func DecayL1Network(engine *core.StorageEngine, l2Meta *index.L2MetaIndex, cfg *DecayParams) (*L1DecayReport, error) {
	nowMs := time.Now().UnixMilli()
	report := &L1DecayReport{}
	removedNodeIDs, clearedEdges, err := decayNodes(engine, l2Meta, cfg, nowMs, report)
	if err != nil {
		return report, err
	}
	if err := propagateClearedEdges(engine, cfg, clearedEdges, report); err != nil {
		return report, err
	}
	if err := decayRemainingEdges(engine, cfg, removedNodeIDs, nowMs, report); err != nil {
		return report, err
	}
	return report, nil
}

func decayNodes(engine *core.StorageEngine, l2Meta *index.L2MetaIndex, cfg *DecayParams, nowMs int64, report *L1DecayReport) (map[uint64]bool, map[uint64]map[uint64]bool, error) {
	removedNodeIDs := make(map[uint64]bool)
	clearedEdges := make(map[uint64]map[uint64]bool)
	for _, node := range core.CollectAllSceneNodes(engine) {
		if skipDeepNode(&node, l2Meta) {
			continue
		}
		if err := decayOneNode(engine, cfg, &node, nowMs, report, removedNodeIDs, clearedEdges); err != nil {
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

func decayOneNode(engine *core.StorageEngine, cfg *DecayParams, node *core.SceneNode, nowMs int64, report *L1DecayReport, removedNodeIDs map[uint64]bool, clearedEdges map[uint64]map[uint64]bool) error {
	dtHours := dtHoursFrom(nowMs, node.UpdatedAt)
	lambda := applyEmotionalBoost(cfg.LambdaNode, node.Valence, node.Arousal)
	newImportance := node.Importance * float32(math.Exp(-lambda*dtHours))
	if newImportance < cfg.NodeRemoveThreshold {
		if _, err := engine.DeleteRecord(node.IDHash); err != nil {
			return err
		}
		removedNodeIDs[node.IDHash] = true
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
			clearedEdges[edgeID][node.IDHash] = true
		}
		node.EdgeIDs = nil
	}
	node.UpdatedAt = nowMs
	if err := core.WriteSceneNode(engine, node.IDHash, node); err != nil {
		return err
	}
	report.DecayedNodes++
	return nil
}

func propagateClearedEdges(engine *core.StorageEngine, cfg *DecayParams, clearedEdges map[uint64]map[uint64]bool, report *L1DecayReport) error {
	for edgeID, nodeIDs := range clearedEdges {
		for nodeID := range nodeIDs {
			gone, err := removeNodeFromEdge(engine, edgeID, nodeID, cfg)
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

func decayRemainingEdges(engine *core.StorageEngine, cfg *DecayParams, removedNodeIDs map[uint64]bool, nowMs int64, report *L1DecayReport) error {
	var entries []uint64
	_ = engine.IterIndexByType(core.RecL1Hyperedge, func(idHash uint64) error {
		entries = append(entries, idHash)
		return nil
	})
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

// decayOneEdge decays the edge weight incrementally from the last decay
// time, drops references to removed nodes, and deletes the edge when it
// falls below MinEdgeNodes or the weight threshold.
func decayOneEdge(engine *core.StorageEngine, cfg *DecayParams, edge *core.SceneEdge, idHash uint64, removedNodeIDs map[uint64]bool, nowMs int64, report *L1DecayReport) error {
	baseMs := edge.LastDecayAt
	if baseMs == 0 {
		baseMs = edge.CreatedAt
	}
	dtHours := dtHoursFrom(nowMs, baseMs)
	newWeight := edge.Weight * float32(math.Exp(-cfg.LambdaEdge*dtHours))

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
		if _, err := engine.DeleteRecord(idHash); err != nil {
			return err
		}
		report.RemovedEdges++
		return nil
	}
	edge.Weight = newWeight
	edge.LastDecayAt = nowMs
	return writeSceneEdge(engine, idHash, edge)
}

// removeNodeFromEdge removes a node from an edge; when the edge falls below
// MinEdgeNodes it is deleted and its refs are cleared from other nodes.
// Returns whether the edge was deleted.
func removeNodeFromEdge(engine *core.StorageEngine, edgeID, nodeID uint64, cfg *DecayParams) (bool, error) {
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
		for _, surviving := range edge.NodeIDs {
			if err := removeEdgeFromNode(engine, surviving, edgeID); err != nil {
				return false, err
			}
		}
		if _, err := engine.DeleteRecord(edgeID); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, writeSceneEdge(engine, edgeID, edge)
}

func removeEdgeFromNode(engine *core.StorageEngine, nodeID, edgeID uint64) error {
	node := readSceneNode(engine, nodeID)
	if node == nil {
		return nil
	}
	found := false
	filtered := node.EdgeIDs[:0]
	for _, e := range node.EdgeIDs {
		if e == edgeID {
			found = true
		} else {
			filtered = append(filtered, e)
		}
	}
	if !found {
		return nil
	}
	node.EdgeIDs = filtered
	return core.WriteSceneNode(engine, nodeID, node)
}

func readSceneNode(engine *core.StorageEngine, idHash uint64) *core.SceneNode {
	rt, data, err := engine.ReadRecord(idHash)
	if err != nil || rt != core.RecL1SceneNode {
		return nil
	}
	var node core.SceneNode
	if json.Unmarshal(data, &node) != nil {
		return nil
	}
	return &node
}

func readSceneEdge(engine *core.StorageEngine, idHash uint64) *core.SceneEdge {
	rt, data, err := engine.ReadRecord(idHash)
	if err != nil || rt != core.RecL1Hyperedge {
		return nil
	}
	var edge core.SceneEdge
	if json.Unmarshal(data, &edge) != nil {
		return nil
	}
	return &edge
}

func writeSceneEdge(engine *core.StorageEngine, id uint64, edge *core.SceneEdge) error {
	data, err := json.Marshal(edge)
	if err != nil {
		return common.NewError(common.ErrSerialization, "marshal scene edge", err)
	}
	_, err = engine.WriteRecord(core.RecL1Hyperedge, id, data)
	return err
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

func dtHoursFrom(nowMs, updatedAtMs int64) float64 {
	dtMs := nowMs - updatedAtMs
	if dtMs < 0 {
		dtMs = 0
	}
	return float64(dtMs) / 3_600_000.0
}
