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

// NodeFilter is a graph-scoped node query with its conditions already parsed:
// a nil IDs means no id filter, and Keyword is expected lower-cased. Every
// condition that is set has to hold.
type NodeFilter struct {
	IDs      map[uint64]struct{}
	Keyword  string
	NodeType string
}

// Matches reports whether one node of the requested graph satisfies every
// condition the filter sets.
func (f NodeFilter) Matches(n core.HypergraphNode) bool {
	if f.IDs != nil {
		if _, ok := f.IDs[n.IDHash]; !ok {
			return false
		}
	}
	if f.NodeType != "" && n.NodeType != f.NodeType {
		return false
	}
	if f.Keyword != "" && !matchesKeyword(n, f.Keyword) {
		return false
	}
	return true
}

// matchesKeyword is a case-insensitive substring test over the node's title,
// content and keyword track; kw must already be lower-cased.
func matchesKeyword(n core.HypergraphNode, kw string) bool {
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

// CheckSubgraphStart verifies the start node of a subgraph walk exists and belongs
// to the graph being walked; both ids are the caller's parsed numerics.
func CheckSubgraphStart(engine *core.StorageEngine, agentID uint64, graphHash, startHash uint64) error {
	startNode, err := core.ReadHypergraphNode(engine, agentID, startHash)
	if err != nil {
		if common.CodeOf(err) != common.ErrNotFound {
			// A start node that exists but will not read back is not a start node that
			// is missing: the first sends the host elsewhere, the second says the graph
			// is damaged here.
			return err
		}
		return common.NewError(common.ErrNotFound, "start node not found", err)
	}
	if startNode.GraphID != graphHash {
		return common.NewError(common.ErrInvalidQuery,
			"start node does not belong to the requested graph")
	}
	return nil
}

// SubgraphAdjacency builds the undirected adjacency map from the graph's edges
// (restricted to edgeKinds when non-empty) and returns the kept edges alongside. An
// edge that will not read back stops the build: the adjacency decides reachability,
// so a gap in it is not one less edge but a member the walk can no longer reach.
func SubgraphAdjacency(engine *core.StorageEngine, agentID uint64, graphID uint64, edgeKinds []core.GraphEdgeKind) (map[uint64]map[uint64]struct{}, []core.HypergraphEdge, error) {
	adj := make(map[uint64]map[uint64]struct{})
	var edges []core.HypergraphEdge
	all, err := repo.ListEdgeL3(engine, agentID, graphID)
	if err != nil {
		return nil, nil, err
	}
	for _, e := range all {
		if len(edgeKinds) > 0 && !slices.Contains(edgeKinds, e.Kind) {
			continue
		}
		edges = append(edges, e)
		connectNodes(adj, e.NodeIDs)
	}
	return adj, edges, nil
}

// BfsWithinDepth returns the ids reachable from start within maxDepth hops (level order,
// one hop per round), including start itself. A non-positive maxDepth sets no bound and the
// walk runs to the reachable component — the same reading `limit` carries on every other
// L3 and L4 read, so one zero means one thing across the surface. The walk cannot spin:
// a node is visited once, so a cycle in the hypergraph ends the round rather than the run.
func BfsWithinDepth(start uint64, adj map[uint64]map[uint64]struct{}, maxDepth int) map[uint64]struct{} {
	visited := map[uint64]struct{}{start: {}}
	queue := []uint64{start}
	for depth := 0; (maxDepth <= 0 || depth < maxDepth) && len(queue) > 0; depth++ {
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

func AllNodesVisited(nodeIDs []uint64, visited map[uint64]struct{}) bool {
	for _, id := range nodeIDs {
		if _, ok := visited[id]; !ok {
			return false
		}
	}
	return true
}
