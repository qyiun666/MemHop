// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package graph

import (
	"slices"
	"strings"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// QueryNodesByIDs reads the requested nodes, keeping only the ones that
// exist and belong to graphHash; unparsable/missing ids are skipped.
func QueryNodesByIDs(engine *core.StorageEngine, agentID, graphHash uint64, ids []string) []core.HypergraphNode {
	var out []core.HypergraphNode
	for _, id := range ids {
		idHash, err := common.ParseID(id)
		if err != nil {
			continue
		}
		node, err := core.ReadHypergraphNode(engine, agentID, idHash)
		if err != nil || node.GraphID != graphHash {
			continue
		}
		out = append(out, *node)
	}
	return out
}

func NodeMatchesKeyword(n core.HypergraphNode, kw string) bool {
	if strings.Contains(strings.ToLower(n.Title), kw) {
		return true
	}
	if strings.Contains(strings.ToLower(n.Content), kw) {
		return true
	}
	for _, k := range n.Keywords {
		if strings.Contains(strings.ToLower(k), kw) {
			return true
		}
	}
	return false
}

// ResolveSubgraphStart parses the graph/start ids and verifies the start
// node exists and belongs to the requested graph.
func ResolveSubgraphStart(engine *core.StorageEngine, agentID uint64, graphID, startNodeID string) (graphHash, startHash uint64, err error) {
	graphHash, err = common.ParseID(graphID)
	if err != nil {
		return 0, 0, common.NewError(common.ErrInvalidQuery, "parse graph id", err)
	}
	startHash, err = common.ParseID(startNodeID)
	if err != nil {
		return 0, 0, common.NewError(common.ErrInvalidQuery, "parse start node id", err)
	}
	startNode, err := core.ReadHypergraphNode(engine, agentID, startHash)
	if err != nil {
		return 0, 0, common.NewError(common.ErrNotFound, "start node not found", err)
	}
	if startNode.GraphID != graphHash {
		return 0, 0, common.NewError(common.ErrInvalidQuery,
			"start node does not belong to the requested graph")
	}
	return graphHash, startHash, nil
}

// SubgraphAdjacency builds the undirected adjacency map from the graph's
// edges (restricted to edgeKinds when non-empty) and returns the kept
// edges alongside.
func SubgraphAdjacency(engine *core.StorageEngine, agentID uint64, graphID uint64, edgeKinds []core.GraphEdgeKind) (map[uint64]map[uint64]struct{}, []core.HypergraphEdge) {
	adj := make(map[uint64]map[uint64]struct{})
	var edges []core.HypergraphEdge
	for _, e := range repo.ListEdgeL3(engine, agentID, graphID) {
		if len(edgeKinds) > 0 && !containsEdgeKind(edgeKinds, e.Kind) {
			continue
		}
		edges = append(edges, e)
		connectNodes(adj, e.NodeIDs)
	}
	return adj, edges
}

// BfsWithinDepth returns the ids reachable from start within maxDepth
// hops (level order, one hop per round), including start itself.
func BfsWithinDepth(start uint64, adj map[uint64]map[uint64]struct{}, maxDepth int) map[uint64]struct{} {
	visited := map[uint64]struct{}{start: {}}
	queue := []uint64{start}
	for depth := 0; depth < maxDepth && len(queue) > 0; depth++ {
		var next []uint64
		for _, cur := range queue {
			for nb := range adj[cur] {
				if _, seen := visited[nb]; seen {
					continue
				}
				visited[nb] = struct{}{}
				next = append(next, nb)
			}
		}
		queue = next
	}
	return visited
}

func connectNodes(adj map[uint64]map[uint64]struct{}, nodeIDs []uint64) {
	for i, a := range nodeIDs {
		for _, b := range nodeIDs[i+1:] {
			if a == b {
				continue
			}
			if adj[a] == nil {
				adj[a] = make(map[uint64]struct{})
			}
			if adj[b] == nil {
				adj[b] = make(map[uint64]struct{})
			}
			adj[a][b] = struct{}{}
			adj[b][a] = struct{}{}
		}
	}
}

func containsEdgeKind(kinds []core.GraphEdgeKind, k core.GraphEdgeKind) bool {
	return slices.Contains(kinds, k)
}

func AllNodesVisited(nodeIDs []uint64, visited map[uint64]struct{}) bool {
	for _, id := range nodeIDs {
		if _, ok := visited[id]; !ok {
			return false
		}
	}
	return true
}
