// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L3 query big methods of the composition root: node lookup and BFS
// subgraph. The query steps live in internal/graph.

package internal

import (
	"strings"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/graph"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// QueryL3Nodes reads one graph's nodes through every condition the query
// names; the conditions AND together, and an unset condition does not filter.
// Naming only the graph therefore lists its nodes. Results keep graph order
// and Limit caps them. A malformed node id or a graph that does not exist is
// an error — an empty result means the graph exists and nothing matched.
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
	if _, err := core.ReadGraphSlot(db.engine, agentID, graphHash); err != nil {
		return nil, err
	}
	filter, err := nodeFilter(q)
	if err != nil {
		return nil, err
	}
	out := make([]core.HypergraphNode, 0)
	for _, n := range repo.ListNodeL3(db.engine, agentID, graphHash) {
		if filter.Matches(n) {
			out = append(out, n)
		}
	}
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

// nodeFilter parses the query's conditions; a node id that does not parse is
// refused rather than dropped.
func nodeFilter(q L3NodeQuery) (graph.NodeFilter, error) {
	f := graph.NodeFilter{Keyword: strings.ToLower(q.Keyword), NodeType: q.NodeType}
	if len(q.IDs) > 0 {
		f.IDs = make(map[uint64]struct{}, len(q.IDs))
		for _, id := range q.IDs {
			idHash, err := common.ParseID(id)
			if err != nil {
				return graph.NodeFilter{}, common.NewError(common.ErrInvalidQuery, "parse node id", err)
			}
			f.IDs[idHash] = struct{}{}
		}
	}
	return f, nil
}

// QueryL3Subgraph BFS from startNodeID up to maxDepth; edgeKinds restricts
// reachable edges (maxDepth<=0 means 1).
func (db *DB) QueryL3Subgraph(agentID uint64, graphID, startNodeID string, maxDepth int, edgeKinds []core.GraphEdgeKind) (*L3Subgraph, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	graphHash, startHash, err := graph.ResolveSubgraphStart(db.engine, agentID, graphID, startNodeID)
	if err != nil {
		return nil, err
	}
	if maxDepth <= 0 {
		maxDepth = 1
	}

	// Adjacency: all graph edges (filtered by edgeKinds), hyperedge nodeIDs fully connected.
	adj, edges := graph.SubgraphAdjacency(db.engine, agentID, graphHash, edgeKinds)

	// BFS level order: maxDepth hops, one hop per round.
	visited := graph.BfsWithinDepth(startHash, adj, maxDepth)

	// Subgraph extraction: visited nodes plus edges with both ends visited.
	nodes := make([]core.HypergraphNode, 0, len(visited))
	for h := range visited {
		if n, err := core.ReadHypergraphNode(db.engine, agentID, h); err == nil {
			nodes = append(nodes, *n)
		}
	}
	subEdges := make([]core.HypergraphEdge, 0, len(edges))
	for _, e := range edges {
		if graph.AllNodesVisited(e.NodeIDs, visited) {
			subEdges = append(subEdges, e)
		}
	}
	return &L3Subgraph{Nodes: nodes, Edges: subEdges}, nil
}
