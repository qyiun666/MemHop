// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L1 hypergraph edge building: BuildHyperedges creates co-occurrence
// edges between scenes whose depth-1 keyword sets overlap. SceneNode writes
// happen only during Dream via SyncL1NodesFromL2 (l1layer_sync.go); decay
// and rebuild live in l1layer_decay.go.

package engram

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// BuildHyperedges creates or refreshes co-occurrence hyperedges between
// scene nodes whose topic keyword sets overlap (Jaccard >= minSimilarity).
// It must run after SyncL1NodesFromL2 and before DecayNetwork so freshly
// created edges are decayed by the same pass. Edge weight is the Jaccard
// similarity; updates keep the max of old and new so decayed weights are
// never silently restored by stale similarities. Stale edges are left to
// DecayNetwork (natural forgetting), never deleted here. Returns the
// number of edges created or updated.
func BuildHyperedges(engine *core.StorageEngine, agentID uint64, minSimilarity float32) (int, error) {
	nodes := core.CollectAllSceneNodes(engine, agentID)
	if len(nodes) < 2 {
		return 0, nil
	}
	kwByNode, inverted := collectNodeKeywordSets(engine, agentID, nodes)
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
				sim, ok := jaccard(kwByNode[lo], kwByNode[hi])
				if !ok || sim < minSimilarity {
					continue
				}
				if written, err := upsertSceneEdge(engine, agentID, lo, hi, sim, now); err != nil {
					return changed, err
				} else if written {
					changed++
				}
			}
		}
	}
	return changed, nil
}

// collectNodeKeywordSets aggregates the lowercased deduplicated keyword
// set per node and the keyword → nodeID inverted index used to skip pairs
// sharing no terms.
func collectNodeKeywordSets(engine *core.StorageEngine, agentID uint64, nodes []core.SceneNode) (map[uint64]map[string]struct{}, map[string][]uint64) {
	kwByNode := make(map[uint64]map[string]struct{}, len(nodes))
	inverted := make(map[string][]uint64)
	for i := range nodes {
		node := &nodes[i]
		set := make(map[string]struct{})
		for _, topicID := range node.TopicIDs {
			topic, err := core.ReadTopicLenient(engine, agentID, topicID)
			if err != nil || topic == nil {
				continue
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
	return kwByNode, inverted
}

// jaccard returns the keyword-set similarity; ok is false for an empty
// union (nothing to compare).
func jaccard(setA, setB map[string]struct{}) (float32, bool) {
	inter, union := 0, len(setA)
	for kw := range setB {
		if _, ok := setA[kw]; ok {
			inter++
		} else {
			union++
		}
	}
	if union == 0 {
		return 0, false
	}
	return float32(inter) / float32(union), true
}

// upsertSceneEdge writes the co-occurrence edge between two scene nodes
// (ID = hash("l1edge:"+min+":"+max), deterministic and idempotent) and
// attaches it to both nodes' EdgeIDs. Weight keeps max(old, new); returns
// whether the edge was actually written.
func upsertSceneEdge(engine *core.StorageEngine, agentID uint64, nodeA, nodeB uint64, weight float32, now int64) (bool, error) {
	lo, hi := min(nodeA, nodeB), max(nodeA, nodeB)
	edgeID := common.HashID(fmt.Sprintf("l1edge:%d:%d", lo, hi))
	edge, err := core.ReadSceneEdge(engine, agentID, edgeID)
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
	if err := core.WriteSceneEdge(engine, agentID, edgeID, edge); err != nil {
		return false, err
	}
	for _, nodeID := range []uint64{lo, hi} {
		node, err := core.ReadSceneNode(engine, agentID, nodeID)
		if err != nil {
			continue // node vanished between Sync and here; edge dangles but decays away
		}
		if !slices.Contains(node.EdgeIDs, edgeID) {
			node.EdgeIDs = append(node.EdgeIDs, edgeID)
			if err := core.WriteSceneNode(engine, agentID, nodeID, node); err != nil {
				return false, err
			}
		}
	}
	return true, nil
}
