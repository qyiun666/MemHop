// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L1 hypergraph edge building: BuildHyperedges creates co-occurrence edges
// between scenes whose keyword sets overlap, and strengthens an existing edge
// only over evidence that has moved. This file never forgets — that is decay.go.

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
// created edges are decayed by the same pass, and it takes the node ids that
// pass wrote: an edge's weight may rise only over evidence one of its endpoints
// has changed since. Re-measuring two keyword sets that did not change returns
// the similarity the edge was already weighted by, so without that gate every
// pass would lift a decayed edge back to full strength and edge forgetting would
// never accumulate. Stale edges are left to DecayNetwork (natural forgetting),
// never deleted here. Returns the number of edges created or strengthened.
func BuildHyperedges(engine *core.StorageEngine, agentID uint64, minSimilarity float64, touched map[uint64]struct{}) (int, error) {
	// A node this enumeration steps over pairs with nothing, and unlike a node this
	// pass refuses to fade, the missing edge never reports itself later.
	nodes, err := core.CollectAllStrict[core.SceneNode](engine, agentID, core.RecL1SceneNode)
	if err != nil {
		return 0, err
	}
	if len(nodes) < 2 {
		return 0, nil
	}
	kwByNode, inverted, err := collectNodeKeywordSets(engine, agentID, nodes)
	if err != nil {
		return 0, err
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
				sim, ok := jaccard(kwByNode[lo], kwByNode[hi])
				if !ok || sim < minSimilarity {
					continue
				}
				_, aChanged := touched[lo]
				_, bChanged := touched[hi]
				if written, err := upsertSceneEdge(engine, agentID, lo, hi, sim, now, aChanged || bChanged); err != nil {
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
func collectNodeKeywordSets(engine *core.StorageEngine, agentID uint64, nodes []core.SceneNode) (map[uint64]map[string]struct{}, map[string][]uint64, error) {
	kwByNode := make(map[uint64]map[string]struct{}, len(nodes))
	inverted := make(map[string][]uint64)
	for i := range nodes {
		node := &nodes[i]
		set := make(map[string]struct{})
		for _, topicID := range node.TopicIDs {
			topic, err := core.ReadTopicLenient(engine, agentID, topicID)
			switch {
			case err != nil && common.CodeOf(err) != common.ErrNotFound:
				// A topic that will not read back is not a topic that is gone. Taking
				// it for gone shrinks the set this node is measured by, and an edge that
				// drops under the similarity floor because of it is never built — and
				// never comes back on its own, since decay only lowers a weight and
				// raising one needs an endpoint a later pass can see as changed.
				return nil, nil, err
			case err != nil, topic == nil:
				continue // gone, or the id names a record of another kind
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
	return kwByNode, inverted, nil
}

// jaccard returns the keyword-set similarity; ok is false for an empty
// union (nothing to compare).
func jaccard(setA, setB map[string]struct{}) (float64, bool) {
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
	return float64(inter) / float64(union), true
}

// upsertSceneEdge writes the co-occurrence edge between two scene nodes
// (ID = hash("l1edge:"+min+":"+max), deterministic and idempotent) and
// attaches it to both nodes' EdgeIDs. A new edge is weighted by the similarity;
// an existing one only rises when evidenceChanged says one endpoint's turn list
// came out different. Returns whether the edge was actually written.
func upsertSceneEdge(engine *core.StorageEngine, agentID uint64, nodeA, nodeB uint64, weight float64, now int64, evidenceChanged bool) (bool, error) {
	lo, hi := min(nodeA, nodeB), max(nodeA, nodeB)
	edgeID := common.HashID(fmt.Sprintf("l1edge:%d:%d", lo, hi))
	edge, err := core.ReadSceneEdge(engine, agentID, edgeID)
	switch {
	case err != nil && common.CodeOf(err) != common.ErrNotFound:
		// An edge that is there but will not read back is not an edge that is
		// missing: rebuilding it restarts CreatedAt, which is what the decay clock
		// runs on, and hands back the full similarity an aged edge had decayed away
		// from — with no record of the weight it was holding.
		return false, err
	case err != nil:
		edge = &core.SceneEdge{
			IDHash:    edgeID,
			NodeIDs:   []uint64{lo, hi},
			CreatedAt: now,
		}
	case weight <= edge.Weight:
		return false, nil // existing edge is at least as strong; nothing to refresh
	case !evidenceChanged:
		// The similarity above the current weight is the one this edge was already
		// weighted by: nothing has been re-experienced, the same two sets were read
		// a second time. Raising on it would undo whatever decay has taken.
		return false, nil
	}
	edge.Weight = weight
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
