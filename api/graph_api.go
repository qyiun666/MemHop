// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package memhop

import (
	"strings"

	"memhop/internal/core"
	"memhop/internal/core/l3"
	"memhop/internal/core/l3/dsl"
	"memhop/internal/core/model"
	"memhop/internal/core/query"
	"memhop/internal/hash"
)

// GraphQuery extracts a subgraph reachable from startNode within maxDepth hops.
// It uses the L3 adjacency cache when available, falling back to engine BFS.
func (m *MemHop) GraphQuery(
	graphID, startNode string,
	maxDepth int,
	edgeKinds []string,
) (*query.Subgraph, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, core.ErrClosed
	}
	graphHash, err := hash.ParseID(graphID)
	if err != nil {
		return nil, core.NewError(core.ErrInvalidQuery, "parse graph id", err)
	}
	startHash, err := hash.ParseID(startNode)
	if err != nil {
		return nil, core.NewError(core.ErrInvalidQuery, "parse start node", err)
	}
	kinds := parseGraphEdgeKinds(edgeKinds)
	return m.graphQueryL3(graphHash, startHash, maxDepth, kinds)
}

// graphQueryL3 performs BFS using cache or engine, then extracts a subgraph.
func (m *MemHop) graphQueryL3(
	graphHash, startHash uint64,
	maxDepth int,
	kinds []model.GraphEdgeKind,
) (*query.Subgraph, error) {
	visited := collectBFSVisited(m, graphHash, startHash, maxDepth, kinds)
	if len(visited) == 0 {
		return &query.Subgraph{}, nil
	}
	sub, err := l3.ExtractSubgraph(m.engine, visited)
	if err != nil {
		return nil, err
	}
	return convertSubgraph(sub), nil
}

// collectBFSVisited runs BFS and returns all visited node hashes (including start).
func collectBFSVisited(
	m *MemHop,
	graphHash, startHash uint64,
	maxDepth int,
	kinds []model.GraphEdgeKind,
) map[uint64]bool {
	var layers [][]uint64
	if adj, ok := m.l3Cache.Get(graphHash); ok {
		layers = l3.BFSWithAdjacency(adj, startHash, maxDepth, kinds)
	} else {
		layers = l3.BFSFromEngine(m.engine, graphHash, startHash, maxDepth, kinds)
		cacheAdj := l3.BuildAdjacencyIndex(m.engine, graphHash)
		m.l3Cache.Put(graphHash, cacheAdj)
	}
	visited := map[uint64]bool{startHash: true}
	for _, layer := range layers {
		for _, h := range layer {
			visited[h] = true
		}
	}
	return visited
}

// convertSubgraph converts l3.Subgraph to query.Subgraph.
func convertSubgraph(sub *l3.Subgraph) *query.Subgraph {
	nodes := make([]query.GraphNode, len(sub.Nodes))
	for i, n := range sub.Nodes {
		nodes[i] = nodeToGraphNode(n)
	}
	edges := make([]query.GraphEdge, len(sub.Edges))
	for i, e := range sub.Edges {
		edges[i] = edgeToGraphEdge(e)
	}
	return &query.Subgraph{Nodes: nodes, Edges: edges}
}

// nodeToGraphNode converts a model HypergraphNode to a query GraphNode.
func nodeToGraphNode(n *model.HypergraphNode) query.GraphNode {
	return query.GraphNode{
		ID:         hash.FormatHash(n.IDHash),
		GraphID:    hash.FormatHash(n.GraphID),
		Title:      n.Title,
		NodeType:   n.NodeType,
		Content:    n.Content,
		Keywords:   n.Keywords,
		SourceRef:  n.SourceRef,
		Importance: n.Importance,
		Summary:    n.Summary,
		CreatedAt:  n.CreatedAt,
		UpdatedAt:  n.UpdatedAt,
	}
}

// edgeToGraphEdge converts a model HypergraphEdge to a query GraphEdge.
func edgeToGraphEdge(e *model.HypergraphEdge) query.GraphEdge {
	hexIDs := make([]string, len(e.NodeIDs))
	for i, id := range e.NodeIDs {
		hexIDs[i] = hash.FormatHash(id)
	}
	return query.GraphEdge{
		ID:          hash.FormatHash(e.IDHash),
		GraphID:     hash.FormatHash(e.GraphID),
		Kind:        e.Kind,
		NodeIDs:     hexIDs,
		Weight:      e.Weight,
		Label:       e.Label,
		Description: e.Description,
		Confidence:  e.Confidence,
		CreatedAt:   e.CreatedAt,
	}
}

// DSLQuery executes a DSL query string (MATCH, PATH, SUBGRAPH, etc.).
func (m *MemHop) DSLQuery(dslStr string) (*dsl.QueryResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, core.ErrClosed
	}
	parsed, err := dsl.Parse(dslStr)
	if err != nil {
		return nil, core.NewError(core.ErrInvalidQuery, "dsl parse", err)
	}
	exec := dsl.NewExecutor(m.engine)
	return exec.Execute(parsed)
}

// IsDSLQuery returns true if the input looks like a DSL query.
func IsDSLQuery(input string) bool {
	trimmed := strings.TrimSpace(strings.ToUpper(input))
	return strings.HasPrefix(trimmed, "MATCH") ||
		strings.HasPrefix(trimmed, "PATH") ||
		strings.HasPrefix(trimmed, "SUBGRAPH")
}

// parseGraphEdgeKinds converts string edge kinds to model types.
func parseGraphEdgeKinds(kinds []string) []model.GraphEdgeKind {
	if len(kinds) == 0 {
		return nil
	}
	var parsed []model.GraphEdgeKind
	for _, s := range kinds {
		k := parseOneEdgeKind(s)
		if k != nil {
			parsed = append(parsed, *k)
		}
	}
	if len(parsed) == 0 {
		return nil
	}
	return parsed
}

func parseOneEdgeKind(s string) *model.GraphEdgeKind {
	switch strings.ToLower(s) {
	case "related":
		k := model.EdgeRelated
		return &k
	case "causal":
		k := model.EdgeCausal
		return &k
	case "part_of", "partof":
		k := model.EdgePartOf
		return &k
	case "sequence":
		k := model.EdgeSequence
		return &k
	case "dependency":
		k := model.EdgeDependency
		return &k
	case "custom":
		k := model.EdgeCustom
		return &k
	default:
		return nil
	}
}
