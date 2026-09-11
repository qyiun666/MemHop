// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L3 query big methods of the composition root: node lookup and BFS
// subgraph. The query steps live in internal/graph. Like every L3 method,
// both read the file-wide shared domain (core.SharedPoolAgentID); the agentID
// parameter only proves that the caller's own domain is still alive.

package internal

import (
	"maps"
	"slices"
	"strings"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/graph"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// QueryL3Nodes reads one graph's nodes through every condition the query
// names; the conditions AND together, and an unset condition does not filter.
// Naming only the graph therefore lists its nodes. Results are sorted by id and
// Limit keeps the first N of that order, so a capped query is the same subset
// every time. A malformed node id or a graph that does not exist is an error —
// an empty result means the graph exists and nothing matched.
func (db *DB) QueryL3Nodes(agentID uint64, q L3NodeQuery) ([]core.HypergraphNode, error) {
	ac, err := db.lockSharedPool(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	if q.GraphID == "" {
		return nil, common.NewError(common.ErrInvalidQuery, "graph_id is required")
	}
	slot, err := repo.ReadSharedGraphL3(db.engine, q.GraphID)
	if err != nil {
		return nil, err
	}
	graphHash := slot.IDHash
	filter, err := nodeFilter(q)
	if err != nil {
		return nil, err
	}
	out := make([]core.HypergraphNode, 0)
	for _, n := range repo.ListNodeL3(db.engine, core.SharedPoolAgentID, graphHash) {
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
// reachable edges (maxDepth<=0 means 1). Nodes come back sorted by id, and so do
// edges, because the listings under both are assembled from a hash-map scan.
func (db *DB) QueryL3Subgraph(agentID uint64, graphID, startNodeID string, maxDepth int, edgeKinds []core.GraphEdgeKind) (*L3Subgraph, error) {
	ac, err := db.lockSharedPool(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	graphHash, startHash, err := graph.ResolveSubgraphStart(db.engine, core.SharedPoolAgentID, graphID, startNodeID)
	if err != nil {
		return nil, err
	}
	if maxDepth <= 0 {
		maxDepth = 1
	}

	// Adjacency: all graph edges (filtered by edgeKinds), hyperedge nodeIDs fully connected.
	adj, edges := graph.SubgraphAdjacency(db.engine, core.SharedPoolAgentID, graphHash, edgeKinds)

	// BFS level order: maxDepth hops, one hop per round.
	visited := graph.BfsWithinDepth(startHash, adj, maxDepth)

	// Subgraph extraction: visited nodes plus edges with both ends visited.
	nodes := make([]core.HypergraphNode, 0, len(visited))
	for _, h := range slices.Sorted(maps.Keys(visited)) {
		// A visited id is one an edge named, and an edge is only written over
		// nodes the import that created it had in hand — so a node the pool
		// reaches but cannot read is the pool disagreeing with itself, not a
		// hole to step over.
		n, err := core.ReadHypergraphNode(db.engine, core.SharedPoolAgentID, h)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, *n)
	}
	subEdges := make([]core.HypergraphEdge, 0, len(edges))
	for _, e := range edges {
		if graph.AllNodesVisited(e.NodeIDs, visited) {
			subEdges = append(subEdges, e)
		}
	}
	return &L3Subgraph{Nodes: nodes, Edges: subEdges}, nil
}
