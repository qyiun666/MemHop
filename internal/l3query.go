// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L3 query operations of the internal layer: node lookup and BFS subgraph.

package internal

import (
	"slices"
	"strings"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// L3NodeQuery is a node query: GraphID required; one of IDs/Keyword/NodeType.
type L3NodeQuery struct {
	GraphID  string   `json:"graph_id"`
	IDs      []string `json:"ids,omitempty"`
	Keyword  string   `json:"keyword,omitempty"`
	NodeType string   `json:"node_type,omitempty"`
	Limit    int      `json:"limit,omitempty"` // <=0 means unlimited
}

func (db *DB) QueryL3Nodes(q L3NodeQuery) ([]core.HypergraphNode, error) {
	if err := db.beginRead(); err != nil {
		return nil, err
	}
	defer db.mu.RUnlock()
	if q.GraphID == "" {
		return nil, common.NewError(common.ErrInvalidQuery, "graph_id is required")
	}
	graphHash, err := common.ParseID(q.GraphID)
	if err != nil {
		return nil, err
	}
	var out []core.HypergraphNode
	switch {
	case len(q.IDs) > 0:
		for _, id := range q.IDs {
			idHash, err := common.ParseID(id)
			if err != nil {
				continue
			}
			node, err := core.ReadHypergraphNode(db.engine, idHash)
			if err != nil || node.GraphID != graphHash {
				continue
			}
			out = append(out, *node)
		}
	case q.Keyword != "":
		kw := strings.ToLower(q.Keyword)
		for _, n := range repo.ListNodeL3(db.engine, q.GraphID) {
			if nodeMatchesKeyword(n, kw) {
				out = append(out, n)
			}
		}
	case q.NodeType != "":
		for _, n := range repo.ListNodeL3(db.engine, q.GraphID) {
			if n.NodeType == q.NodeType {
				out = append(out, n)
			}
		}
	default:
		return []core.HypergraphNode{}, nil
	}
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	if out == nil {
		return []core.HypergraphNode{}, nil
	}
	return out, nil
}

func nodeMatchesKeyword(n core.HypergraphNode, kw string) bool {
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

type L3Subgraph struct {
	Nodes []core.HypergraphNode
	Edges []core.HypergraphEdge
}

// QueryL3Subgraph BFS from startNodeID up to maxDepth; edgeKinds restricts
// reachable edges (maxDepth<=0 means 1).
func (db *DB) QueryL3Subgraph(graphID, startNodeID string, maxDepth int, edgeKinds []core.GraphEdgeKind) (*L3Subgraph, error) {
	if err := db.beginRead(); err != nil {
		return nil, err
	}
	defer db.mu.RUnlock()
	graphHash, err := common.ParseID(graphID)
	if err != nil {
		return nil, common.NewError(common.ErrInvalidQuery, "parse graph id", err)
	}
	startHash, err := common.ParseID(startNodeID)
	if err != nil {
		return nil, common.NewError(common.ErrInvalidQuery, "parse start node id", err)
	}
	startNode, err := core.ReadHypergraphNode(db.engine, startHash)
	if err != nil {
		return nil, common.NewError(common.ErrNotFound, "start node not found", err)
	}
	if startNode.GraphID != graphHash {
		return nil, common.NewError(common.ErrInvalidQuery,
			"start node does not belong to the requested graph")
	}
	if maxDepth <= 0 {
		maxDepth = 1
	}

	// Adjacency: all graph edges (filtered by edgeKinds), hyperedge nodeIDs fully connected.
	adj := make(map[uint64]map[uint64]struct{})
	var edges []core.HypergraphEdge
	for _, e := range repo.ListEdgeL3(db.engine, graphID) {
		if len(edgeKinds) > 0 && !containsEdgeKind(edgeKinds, e.Kind) {
			continue
		}
		edges = append(edges, e)
		connectNodes(adj, e.NodeIDs)
	}

	// BFS level order: maxDepth hops, one hop per round.
	visited := map[uint64]struct{}{startHash: {}}
	queue := []uint64{startHash}
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

	// Subgraph extraction: visited nodes plus edges with both ends visited.
	nodes := make([]core.HypergraphNode, 0, len(visited))
	for h := range visited {
		if n, err := core.ReadHypergraphNode(db.engine, h); err == nil {
			nodes = append(nodes, *n)
		}
	}
	subEdges := make([]core.HypergraphEdge, 0, len(edges))
	for _, e := range edges {
		if allNodesVisited(e.NodeIDs, visited) {
			subEdges = append(subEdges, e)
		}
	}
	return &L3Subgraph{Nodes: nodes, Edges: subEdges}, nil
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

func allNodesVisited(nodeIDs []uint64, visited map[uint64]struct{}) bool {
	for _, id := range nodeIDs {
		if _, ok := visited[id]; !ok {
			return false
		}
	}
	return true
}
