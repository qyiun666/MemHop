package repo

// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L1 hypergraph operations: SceneNode writes happen only during Dream via
// SyncL1NodesFromL2; BuildL1Hyperedges creates co-occurrence edges between
// scenes whose topic keyword sets overlap; search walks the graph at query
// time via SpreadingActivation (internal/scenefind.go).
import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

// DeleteSceneNodeL1 removes one scene's L1 node record by scene ID (the
// node ID is derivable without an index); missing nodes are a no-op.
// Incident hyperedges are cleaned by the next Dream's rebuild.
func DeleteSceneNodeL1(engine *core.StorageEngine, sceneID uint64) error {
	if _, err := engine.DeleteRecordBatch([]uint64{core.SceneNodeID(sceneID)}); err != nil {
		return common.NewError(common.ErrIO, "delete l1 scene node", err)
	}
	return nil
}

// SyncL1NodesFromL2 rebuilds one L1 node per scene from the current
// depth<=2 topics; L1 is written/updated only during Dream. The node ID
// (hash("l1:"+sceneID)) is stable across dreams: existing nodes keep
// Importance/Valence/Arousal (decay belongs to DecayL1Network) and are
// refreshed only when the topic set changed, so UpdatedAt keeps
// accumulating decay. Returns the number of nodes created or updated.
func SyncL1NodesFromL2(engine *core.StorageEngine) (int, error) {
	byScene := make(map[uint64]map[uint64]struct{})
	for idHash := range engine.IndexByType(core.RecL2Topic) {
		topic, err := core.ReadTopicLenient(engine, idHash)
		if err != nil || topic == nil || topic.Depth > 2 {
			continue
		}
		set := byScene[topic.SceneID]
		if set == nil {
			set = make(map[uint64]struct{})
			byScene[topic.SceneID] = set
		}
		set[idHash] = struct{}{}
	}
	now := time.Now().UnixMilli()
	changed := 0
	for sceneID, set := range byScene {
		ids := make([]uint64, 0, len(set))
		for id := range set {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		nodeID := core.SceneNodeID(sceneID)
		node, err := core.ReadSceneNode(engine, nodeID)
		if err != nil {
			node = nil
		}
		if node != nil && equalUint64s(node.TopicIDs, ids) {
			continue // unchanged; keep UpdatedAt so decay accumulates
		}
		if node == nil {
			node = &core.SceneNode{IDHash: nodeID, SceneID: sceneID, CreatedAt: now, Importance: 1.0}
		}
		node.TopicIDs = ids
		node.UpdatedAt = now
		if err := core.WriteSceneNode(engine, nodeID, node); err != nil {
			return changed, err
		}
		changed++
	}
	return changed, nil
}

func equalUint64s(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// BuildL1Hyperedges creates or refreshes co-occurrence hyperedges between
// scene nodes whose topic keyword sets overlap (Jaccard >= minSimilarity).
// It must run after SyncL1NodesFromL2 and before DecayL1Network so freshly
// created edges are decayed by the same pass. Edge weight is the Jaccard
// similarity; updates keep the max of old and new so decayed weights are
// never silently restored by stale similarities. Stale edges are left to
// DecayL1Network (natural forgetting), never deleted here. Returns the
// number of edges created or updated.
func BuildL1Hyperedges(engine *core.StorageEngine, minSimilarity float32) (int, error) {
	nodes := core.CollectAllSceneNodes(engine)
	if len(nodes) < 2 {
		return 0, nil
	}
	// Aggregate per-node keyword sets (lowercased, deduped) and build the
	// keyword → nodeID inverted index to skip pairs sharing no terms.
	kwByNode := make(map[uint64]map[string]struct{}, len(nodes))
	inverted := make(map[string][]uint64)
	for i := range nodes {
		node := &nodes[i]
		set := make(map[string]struct{})
		for _, topicID := range node.TopicIDs {
			topic, err := core.ReadTopicLenient(engine, topicID)
			if err != nil || topic == nil {
				continue
			}
			for _, kw := range topic.UserKeywords {
				set[strings.ToLower(kw)] = struct{}{}
			}
			for _, kw := range topic.AgentKeywords {
				set[strings.ToLower(kw)] = struct{}{}
			}
			for _, kw := range topic.FusedKeywords {
				set[strings.ToLower(kw)] = struct{}{}
			}
		}
		if len(set) == 0 {
			continue
		}
		kwByNode[node.IDHash] = set
		for kw := range set {
			inverted[kw] = append(inverted[kw], node.IDHash)
		}
	}
	// Pairwise Jaccard over keyword-sharing node pairs only.
	now := time.Now().UnixMilli()
	changed := 0
	seen := make(map[[2]uint64]struct{})
	for _, nodes := range inverted {
		for i, a := range nodes {
			for _, b := range nodes[i+1:] {
				lo, hi := min(a, b), max(a, b)
				pair := [2]uint64{lo, hi}
				if _, dup := seen[pair]; dup {
					continue
				}
				seen[pair] = struct{}{}
				setA, setB := kwByNode[lo], kwByNode[hi]
				inter, union := 0, len(setA)
				for kw := range setB {
					if _, ok := setA[kw]; ok {
						inter++
					} else {
						union++
					}
				}
				if union == 0 || float32(inter)/float32(union) < minSimilarity {
					continue
				}
				if written, err := upsertSceneEdge(engine, lo, hi, float32(inter)/float32(union), now); err != nil {
					return changed, err
				} else if written {
					changed++
				}
			}
		}
	}
	return changed, nil
}

// upsertSceneEdge writes the co-occurrence edge between two scene nodes
// (ID = hash("l1edge:"+min+":"+max), deterministic and idempotent) and
// attaches it to both nodes' EdgeIDs. Weight keeps max(old, new); returns
// whether the edge was actually written.
func upsertSceneEdge(engine *core.StorageEngine, nodeA, nodeB uint64, weight float32, now int64) (bool, error) {
	lo, hi := min(nodeA, nodeB), max(nodeA, nodeB)
	edgeID := common.HashID(fmt.Sprintf("l1edge:%d:%d", lo, hi))
	edge, err := core.ReadSceneEdge(engine, edgeID)
	if err != nil {
		edge = &core.SceneEdge{
			IDHash:    edgeID,
			Kind:      core.HyperCoOccurrence,
			NodeIDs:   []uint64{lo, hi},
			CreatedAt: now,
		}
	} else if weight <= edge.Weight {
		return false, nil // existing edge is at least as strong; nothing to refresh
	}
	edge.Weight = max(edge.Weight, weight)
	if err := core.WriteSceneEdge(engine, edgeID, edge); err != nil {
		return false, err
	}
	for _, nodeID := range []uint64{lo, hi} {
		node, err := core.ReadSceneNode(engine, nodeID)
		if err != nil {
			continue // node vanished between Sync and here; edge dangles but decays away
		}
		if !slices.Contains(node.EdgeIDs, edgeID) {
			node.EdgeIDs = append(node.EdgeIDs, edgeID)
			if err := core.WriteSceneNode(engine, nodeID, node); err != nil {
				return false, err
			}
		}
	}
	return true, nil
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
	entries := slices.Collect(engine.IndexByType(core.RecL1Hyperedge))
	for _, idHash := range entries {
		edge, err := core.ReadSceneEdge(engine, idHash)
		if err != nil {
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
	return core.WriteSceneEdge(engine, idHash, edge)
}

// removeNodeFromEdge removes a node from an edge; when the edge falls below
// MinEdgeNodes it is deleted and its refs are cleared from other nodes.
// Returns whether the edge was deleted.
func removeNodeFromEdge(engine *core.StorageEngine, edgeID, nodeID uint64, cfg *DecayParams) (bool, error) {
	edge, err := core.ReadSceneEdge(engine, edgeID)
	if err != nil {
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
	return false, core.WriteSceneEdge(engine, edgeID, edge)
}

func removeEdgeFromNode(engine *core.StorageEngine, nodeID, edgeID uint64) error {
	node, err := core.ReadSceneNode(engine, nodeID)
	if err != nil {
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
	dtMs := max(nowMs-updatedAtMs, 0)
	return float64(dtMs) / 3_600_000.0
}
