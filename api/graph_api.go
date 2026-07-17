// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package memhop

import (
	l3 "memhop/internal/query/graph"
	"memhop/internal/query/graph/dsl"
	"memhop/internal/query/crud"
	"memhop/internal/core/model"
	"memhop/internal/common/hash"
	"memhop/internal/common/mherrors"
)

// GraphQuery extracts a subgraph reachable from startNode within maxDepth hops.
func (m *MemHop) GraphQuery(
	graphID, startNode string,
	maxDepth int,
	edgeKinds []string,
) (*crud.Subgraph, error) {
	if m.closed.Load() {
		return nil, mherrors.ErrClosed
	}
	graphHash, err := hash.ParseID(graphID)
	if err != nil {
		return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "parse graph id", err)
	}
	startHash, err := hash.ParseID(startNode)
	if err != nil {
		return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "parse start node", err)
	}
	kinds := l3.ParseGraphEdgeKinds(edgeKinds)
	sub, err := l3.QuerySubgraph(m.engine, m.l3Cache, graphHash, startHash, maxDepth, kinds)
	if err != nil {
		return nil, err
	}
	return subgraphToDTO(sub), nil
}

// subgraphToDTO converts a graph.Subgraph to a crud.Subgraph.
// NOTE: Kept in API layer (not moved to query/graph) because moving it there would
// create a horizontal dependency: query/graph importing query/crud. The architecture
// constraint disallows query sub-package cross-imports, so DTO conversion stays here.
func subgraphToDTO(sub *l3.Subgraph) *crud.Subgraph {
	if sub == nil {
		return nil
	}
	nodes := make([]crud.GraphNode, len(sub.Nodes))
	for i, n := range sub.Nodes {
		nodes[i] = hypergraphNodeToGraphNode(n)
	}
	edges := make([]crud.GraphEdge, len(sub.Edges))
	for i, e := range sub.Edges {
		edges[i] = hypergraphEdgeToGraphEdge(e)
	}
	return &crud.Subgraph{Nodes: nodes, Edges: edges}
}

func hypergraphNodeToGraphNode(n *model.HypergraphNode) crud.GraphNode {
	// Kept in API layer — see subgraphToDTO for rationale.
	return crud.GraphNode{
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

func hypergraphEdgeToGraphEdge(e *model.HypergraphEdge) crud.GraphEdge {
	hexIDs := make([]string, len(e.NodeIDs))
	for i, id := range e.NodeIDs {
		hexIDs[i] = hash.FormatHash(id)
	}
	return crud.GraphEdge{
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
	if m.closed.Load() {
		return nil, mherrors.ErrClosed
	}
	parsed, err := dsl.Parse(dslStr)
	if err != nil {
		return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "dsl parse", err)
	}
	exec := dsl.NewExecutor(m.engine)
	return exec.Execute(parsed)
}

// IsDSLQuery returns true if the input looks like a DSL query.
func IsDSLQuery(input string) bool {
	return l3.IsDSLQuery(input)
}
