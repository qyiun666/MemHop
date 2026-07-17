// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package memhop

import (
	l3 "memhop/internal/query/graph"
	"memhop/internal/core/model"
	"memhop/internal/query/crud"
	"memhop/internal/common/hash"
	"memhop/internal/common/mherrors"
)

// GetL3 loads an L3 hypergraph by ID with all nodes and edges.
func (m *MemHop) GetL3(id string) (*crud.L3Detail, error) {
	if m.closed.Load() {
		return nil, mherrors.ErrClosed
	}
	return crud.GetL3(m.engine, id)
}

// AddL3Node adds a node to an L3 graph and updates all indexes.
func (m *MemHop) AddL3Node(graphID string, node *model.HypergraphNode) error {
	if m.closed.Load() {
		return mherrors.ErrClosed
	}
	return l3.AddNodeWithIndexes(m.engine, node, m.l3Index, m.l3Degree, m.l3Cache)
}

// AddL3Edge adds an edge to an L3 graph and updates all indexes.
func (m *MemHop) AddL3Edge(graphID string, edge *model.HypergraphEdge) error {
	if m.closed.Load() {
		return mherrors.ErrClosed
	}
	return l3.AddEdgeWithIndexes(m.engine, edge, m.l3Degree, m.l3Cache)
}

// DeleteL3Node deletes a node and updates all indexes.
func (m *MemHop) DeleteL3Node(nodeHash uint64) error {
	if m.closed.Load() {
		return mherrors.ErrClosed
	}
	return l3.DeleteNodeWithIndexes(m.engine, nodeHash, m.l3Index, m.l3Degree, m.l3Cache)
}

// DeleteL3Edge deletes an edge and updates all indexes.
func (m *MemHop) DeleteL3Edge(edgeHash uint64) error {
	if m.closed.Load() {
		return mherrors.ErrClosed
	}
	return l3.DeleteEdgeWithIndexes(m.engine, edgeHash, m.l3Degree, m.l3Cache)
}

// CreateL3Graph creates a new L3 hypergraph slot.
func (m *MemHop) CreateL3Graph(name string) (*model.HypergraphSlot, error) {
	if m.closed.Load() {
		return nil, mherrors.ErrClosed
	}
	return l3.CreateGraph(m.engine, name, model.HypergraphSource{Kind: model.SourceManual})
}

// DetectCommunities runs Louvain community detection on an L3 graph.
func (m *MemHop) DetectCommunities(
	graphID string, cfg *l3.CommunityConfig,
) (*l3.CommunityResult, error) {
	if m.closed.Load() {
		return nil, mherrors.ErrClosed
	}
	graphHash, err := hash.ParseID(graphID)
	if err != nil {
		return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "parse graph id", err)
	}
	cc := l3.DefaultCommunityConfig()
	if cfg != nil {
		cc = *cfg
	}
	return l3.DetectCommunities(m.engine, graphHash, cc)
}

// SearchL3Nodes is the unified L3 knowledge search entry point.
// Routes to keyword, type, or score-based search depending on query fields.
func (m *MemHop) SearchL3Nodes(q crud.L3SearchQuery) (*crud.L3SearchResult, error) {
	if m.closed.Load() {
		return nil, mherrors.ErrClosed
	}
	return crud.SearchL3Nodes(m.l3Index, m.engine, q)
}

// DeleteL3 deletes an L3 hypergraph and cleans up L2 references.
func (m *MemHop) DeleteL3(id string) error {
	if m.closed.Load() {
		return mherrors.ErrClosed
	}
	if err := crud.DeleteL3(m.engine, m.l3Index, id); err != nil {
		return err
	}
	graphHash, _ := hash.ParseID(id)
	m.l3Cache.Invalidate(graphHash)
	m.l3Degree.ClearGraph(graphHash)
	return nil
}

// ListKnowledge lists L3 hypergraphs with pagination.
func (m *MemHop) ListKnowledge(q crud.KnowledgeListQuery) (*crud.KnowledgeListResult, error) {
	if m.closed.Load() {
		return nil, mherrors.ErrClosed
	}
	return crud.ListKnowledge(m.engine, q)
}

// GetKnowledgeNodes returns L3 nodes matching a query.
func (m *MemHop) GetKnowledgeNodes(q crud.KnowledgeNodeQuery) (*crud.KnowledgeNodesResult, error) {
	if m.closed.Load() {
		return nil, mherrors.ErrClosed
	}
	return crud.GetKnowledgeNodes(m.engine, q)
}
