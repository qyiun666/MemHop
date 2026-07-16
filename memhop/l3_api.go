// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package memhop

import (
	"github.com/qyiun666/memhop/memhop/internal/core"
	"github.com/qyiun666/memhop/memhop/internal/core/l3"
	"github.com/qyiun666/memhop/memhop/internal/core/model"
	"github.com/qyiun666/memhop/memhop/internal/core/query"
	"github.com/qyiun666/memhop/memhop/internal/hash"
)

// GetL3 loads an L3 hypergraph by ID with all nodes and edges.
func (m *MemHop) GetL3(id string) (*query.L3Detail, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, core.ErrClosed
	}
	return query.GetL3(m.engine, id)
}

// AddL3Node adds a node to an L3 graph and updates all indexes.
func (m *MemHop) AddL3Node(graphID string, node *model.HypergraphNode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return core.ErrClosed
	}
	if err := l3.AddNode(m.engine, node); err != nil {
		return err
	}
	m.l3Index.AddNode(node)
	m.l3Degree.OnNodeAdded(node.GraphID, node.IDHash)
	m.l3Cache.Invalidate(node.GraphID)
	return nil
}

// AddL3Edge adds an edge to an L3 graph and updates all indexes.
func (m *MemHop) AddL3Edge(graphID string, edge *model.HypergraphEdge) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return core.ErrClosed
	}
	if err := l3.AddEdge(m.engine, edge); err != nil {
		return err
	}
	m.l3Degree.OnEdgeAdded(edge.GraphID, edge.NodeIDs)
	m.l3Cache.Invalidate(edge.GraphID)
	return nil
}

// DeleteL3Node deletes a node and updates all indexes.
func (m *MemHop) DeleteL3Node(nodeHash uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return core.ErrClosed
	}
	node, err := l3.GetNode(m.engine, nodeHash)
	if err != nil {
		return nil
	}
	graphID := node.GraphID
	if err := l3.DeleteNode(m.engine, nodeHash); err != nil {
		return err
	}
	m.l3Index.RemoveNode(nodeHash)
	m.l3Degree.OnNodeDeleted(graphID, nodeHash)
	m.l3Cache.Invalidate(graphID)
	return nil
}

// DeleteL3Edge deletes an edge and updates all indexes.
func (m *MemHop) DeleteL3Edge(edgeHash uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return core.ErrClosed
	}
	edge, err := l3.GetEdge(m.engine, edgeHash)
	if err != nil {
		return nil
	}
	graphID := edge.GraphID
	if err := l3.DeleteEdge(m.engine, edgeHash); err != nil {
		return err
	}
	m.l3Degree.OnEdgeDeleted(graphID, edge.NodeIDs)
	m.l3Cache.Invalidate(graphID)
	return nil
}

// CreateL3Graph creates a new L3 hypergraph slot.
func (m *MemHop) CreateL3Graph(name string) (*model.HypergraphSlot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, core.ErrClosed
	}
	return l3.CreateGraph(m.engine, name, model.HypergraphSource{Kind: model.SourceManual})
}

// DetectCommunities runs Louvain community detection on an L3 graph.
func (m *MemHop) DetectCommunities(
	graphID string, cfg *l3.CommunityConfig,
) (*l3.CommunityResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, core.ErrClosed
	}
	graphHash, err := hash.ParseID(graphID)
	if err != nil {
		return nil, core.NewError(core.ErrInvalidQuery, "parse graph id", err)
	}
	cc := l3.DefaultCommunityConfig()
	if cfg != nil {
		cc = *cfg
	}
	return l3.DetectCommunities(m.engine, graphHash, cc)
}

// SearchL3Nodes is the unified L3 knowledge search entry point.
// Routes to keyword, type, or score-based search depending on query fields.
func (m *MemHop) SearchL3Nodes(q query.L3SearchQuery) (*query.L3SearchResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, core.ErrClosed
	}
	return query.SearchL3Nodes(m.l3Index, m.engine, q)
}

// DeleteL3 deletes an L3 hypergraph and cleans up L2 references.
func (m *MemHop) DeleteL3(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return core.ErrClosed
	}
	if err := query.DeleteL3(m.engine, id); err != nil {
		return err
	}
	graphHash, _ := hash.ParseID(id)
	m.l3Cache.Invalidate(graphHash)
	m.l3Degree.ClearGraph(graphHash)
	return nil
}

// ListKnowledge lists L3 hypergraphs with pagination.
func (m *MemHop) ListKnowledge(q query.KnowledgeListQuery) (*query.KnowledgeListResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, core.ErrClosed
	}
	return query.ListKnowledge(m.engine, q)
}

// GetKnowledgeNodes returns L3 nodes matching a query.
func (m *MemHop) GetKnowledgeNodes(q query.KnowledgeNodeQuery) (*query.KnowledgeNodesResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, core.ErrClosed
	}
	return query.GetKnowledgeNodes(m.engine, q)
}
