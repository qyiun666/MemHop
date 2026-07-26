// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Internal helpers for the API layer (DTO conversion + ID parsing).
// Extracted from the removed l3_api.go so both crud_api.go and knowledge_api.go
// can share them without a cross-package dependency.

package memhop

import (
	"github.com/qyiun666/MemHop/internal/common/hash"
	"github.com/qyiun666/MemHop/internal/common/mherrors"
	"github.com/qyiun666/MemHop/internal/core/model"
	"github.com/qyiun666/MemHop/internal/query/crud"
	l3 "github.com/qyiun666/MemHop/internal/query/graph"
)

// parseGraphID parses a hex graph identifier, wrapping the error with a
// consistent MemHopError so API callers see uniform ErrInvalidQuery messages.
func parseGraphID(id string) (uint64, error) {
	h, err := hash.ParseID(id)
	if err != nil {
		return 0, mherrors.NewError(mherrors.ErrInvalidQuery, "parse graph id", err)
	}
	return h, nil
}

// subgraphToDTO converts a l3.Subgraph to the exported crud.Subgraph DTO.
// Kept in the API layer to avoid a horizontal dependency from query/graph
// into query/crud (which the internal architecture forbids).
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
