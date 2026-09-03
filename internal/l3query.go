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

func (db *DB) QueryL3Nodes(agentID uint64, q L3NodeQuery) ([]core.HypergraphNode, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
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
		out = db.queryNodesByIDs(agentID, graphHash, q.IDs)
	case q.Keyword != "":
		kw := strings.ToLower(q.Keyword)
		for _, n := range repo.ListNodeL3(db.engine, agentID, graphHash) {
			if nodeMatchesKeyword(n, kw) {
				out = append(out, n)
			}
		}
	case q.NodeType != "":
		for _, n := range repo.ListNodeL3(db.engine, agentID, graphHash) {
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

// queryNodesByIDs reads the requested nodes, keeping only the ones that
// exist and belong to graphHash; unparsable/missing ids are skipped.
func (db *DB) queryNodesByIDs(agentID, graphHash uint64, ids []string) []core.HypergraphNode {
	var out []core.HypergraphNode
	for _, id := range ids {
		idHash, err := common.ParseID(id)
		if err != nil {
			continue
		}
		node, err := core.ReadHypergraphNode(db.engine, agentID, idHash)
		if err != nil || node.GraphID != graphHash {
			continue
		}
		out = append(out, *node)
	}
	return out
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

// QueryL3Subgraph BFS from startNodeID up to maxDepth; edgeKinds restricts
// reachable edges (maxDepth<=0 means 1).
func (db *DB) QueryL3Subgraph(agentID uint64, graphID, startNodeID string, maxDepth int, edgeKinds []core.GraphEdgeKind) (*L3Subgraph, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	graphHash, startHash, err := db.resolveSubgraphStart(agentID, graphID, startNodeID)
	if err != nil {
		return nil, err
	}
	if maxDepth <= 0 {
		maxDepth = 1
	}

	// Adjacency: all graph edges (filtered by edgeKinds), hyperedge nodeIDs fully connected.
	adj, edges := db.subgraphAdjacency(agentID, graphHash, edgeKinds)

	// BFS level order: maxDepth hops, one hop per round.
	visited := bfsWithinDepth(startHash, adj, maxDepth)

	// Subgraph extraction: visited nodes plus edges with both ends visited.
	nodes := make([]core.HypergraphNode, 0, len(visited))
	for h := range visited {
		if n, err := core.ReadHypergraphNode(db.engine, agentID, h); err == nil {
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

// resolveSubgraphStart parses the graph/start ids and verifies the start
// node exists and belongs to the requested graph.
func (db *DB) resolveSubgraphStart(agentID uint64, graphID, startNodeID string) (graphHash, startHash uint64, err error) {
	graphHash, err = common.ParseID(graphID)
	if err != nil {
		return 0, 0, common.NewError(common.ErrInvalidQuery, "parse graph id", err)
	}
	startHash, err = common.ParseID(startNodeID)
	if err != nil {
		return 0, 0, common.NewError(common.ErrInvalidQuery, "parse start node id", err)
	}
	startNode, err := core.ReadHypergraphNode(db.engine, agentID, startHash)
	if err != nil {
		return 0, 0, common.NewError(common.ErrNotFound, "start node not found", err)
	}
	if startNode.GraphID != graphHash {
		return 0, 0, common.NewError(common.ErrInvalidQuery,
			"start node does not belong to the requested graph")
	}
	return graphHash, startHash, nil
}

// subgraphAdjacency builds the undirected adjacency map from the graph's
// edges (restricted to edgeKinds when non-empty) and returns the kept
// edges alongside.
func (db *DB) subgraphAdjacency(agentID uint64, graphID uint64, edgeKinds []core.GraphEdgeKind) (map[uint64]map[uint64]struct{}, []core.HypergraphEdge) {
	adj := make(map[uint64]map[uint64]struct{})
	var edges []core.HypergraphEdge
	for _, e := range repo.ListEdgeL3(db.engine, agentID, graphID) {
		if len(edgeKinds) > 0 && !containsEdgeKind(edgeKinds, e.Kind) {
			continue
		}
		edges = append(edges, e)
		connectNodes(adj, e.NodeIDs)
	}
	return adj, edges
}

// bfsWithinDepth returns the ids reachable from start within maxDepth
// hops (level order, one hop per round), including start itself.
func bfsWithinDepth(start uint64, adj map[uint64]map[uint64]struct{}, maxDepth int) map[uint64]struct{} {
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

func allNodesVisited(nodeIDs []uint64, visited map[uint64]struct{}) bool {
	for _, id := range nodeIDs {
		if _, ok := visited[id]; !ok {
			return false
		}
	}
	return true
}
