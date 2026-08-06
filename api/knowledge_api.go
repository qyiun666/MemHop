// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Knowledge domain: L3 hypergraph CRUD, search, and graph analysis.

package memhop

import (
	"github.com/qyiun666/MemHop/internal/common/mherrors"
	"github.com/qyiun666/MemHop/internal/repo/core/model"
	"github.com/qyiun666/MemHop/internal/query/crud"
	l3 "github.com/qyiun666/MemHop/internal/query/graph"
	"github.com/qyiun666/MemHop/internal/query/graph/dsl"
)

// Knowledge performs an L3 sub-operation identified by op.Kind. See
// KnowledgeOpKind constants for supported operations and required op fields.
func (m *MemHop) Knowledge(op KnowledgeOp) (*KnowledgeResult, error) {
	if err := m.beginRead(); err != nil {
		return nil, err
	}
	defer m.mu.RUnlock()
	switch op.Kind {
	case KOpCreateGraph:
		if op.Name == "" {
			return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "KOpCreateGraph requires Name")
		}
		s, err := l3.CreateGraph(m.engine, op.Name, model.HypergraphSource{Kind: model.SourceManual})
		if err != nil {
			return nil, err
		}
		return &KnowledgeResult{Slot: s}, nil

	case KOpAddNode:
		if op.Node == nil {
			return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "KOpAddNode requires Node")
		}
		return &KnowledgeResult{}, l3.AddNodeWithIndexes(m.engine, op.Node, m.l3Index, m.l3Degree, m.l3Cache)

	case KOpAddEdge:
		if op.Edge == nil {
			return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "KOpAddEdge requires Edge")
		}
		return &KnowledgeResult{}, l3.AddEdgeWithIndexes(m.engine, op.Edge, m.l3Degree, m.l3Cache)

	case KOpDeleteNode:
		nodeHash, err := parseGraphID(op.NodeID)
		if err != nil {
			return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "KOpDeleteNode requires a valid NodeID", err)
		}
		return &KnowledgeResult{}, l3.DeleteNodeWithIndexes(m.engine, nodeHash, m.l3Index, m.l3Degree, m.l3Cache)

	case KOpDeleteEdge:
		edgeHash, err := parseGraphID(op.EdgeID)
		if err != nil {
			return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "KOpDeleteEdge requires a valid EdgeID", err)
		}
		return &KnowledgeResult{}, l3.DeleteEdgeWithIndexes(m.engine, edgeHash, m.l3Degree, m.l3Cache)

	case KOpSearch:
		if op.SearchQuery == nil {
			return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "KOpSearch requires SearchQuery")
		}
		r, err := crud.SearchL3Nodes(m.l3Index, m.engine, *op.SearchQuery)
		if err != nil {
			return nil, err
		}
		return &KnowledgeResult{Search: r}, nil

	case KOpGetNodes:
		if op.NodesQuery == nil {
			return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "KOpGetNodes requires NodesQuery")
		}
		r, err := crud.GetKnowledgeNodes(m.engine, *op.NodesQuery)
		if err != nil {
			return nil, err
		}
		return &KnowledgeResult{Nodes: r}, nil

	case KOpGraphQuery:
		return m.knowledgeGraphQuery(op)

	case KOpDSL:
		if op.DSLString == "" {
			return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "KOpDSL requires DSLString")
		}
		parsed, err := dsl.Parse(op.DSLString)
		if err != nil {
			return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "dsl parse", err)
		}
		exec := dsl.NewExecutor(m.engine)
		r, err := exec.Execute(parsed)
		if err != nil {
			return nil, err
		}
		return &KnowledgeResult{DSL: r}, nil

	case KOpDetectCommunities:
		return m.knowledgeDetectCommunities(op)

	default:
		return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "unsupported KnowledgeOpKind")
	}
}

// knowledgeGraphQuery handles KOpGraphQuery. Split out to keep Knowledge()
// under the 50-line per-function guideline.
func (m *MemHop) knowledgeGraphQuery(op KnowledgeOp) (*KnowledgeResult, error) {
	if op.GraphID == "" || op.StartNode == "" {
		return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "KOpGraphQuery requires GraphID and StartNode")
	}
	graphHash, err := parseGraphID(op.GraphID)
	if err != nil {
		return nil, err
	}
	startHash, err := parseGraphID(op.StartNode)
	if err != nil {
		return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "parse start node", err)
	}
	kinds := l3.ParseGraphEdgeKinds(op.EdgeKinds)
	sub, err := l3.QuerySubgraph(m.engine, m.l3Cache, graphHash, startHash, op.MaxDepth, kinds)
	if err != nil {
		return nil, err
	}
	return &KnowledgeResult{Subgraph: subgraphToDTO(sub)}, nil
}

// knowledgeDetectCommunities handles KOpDetectCommunities.
func (m *MemHop) knowledgeDetectCommunities(op KnowledgeOp) (*KnowledgeResult, error) {
	if op.GraphID == "" {
		return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "KOpDetectCommunities requires GraphID")
	}
	graphHash, err := parseGraphID(op.GraphID)
	if err != nil {
		return nil, err
	}
	cc := l3.DefaultCommunityConfig()
	if op.CommunityCfg != nil {
		cc = *op.CommunityCfg
	}
	r, err := l3.DetectCommunities(m.engine, graphHash, cc)
	if err != nil {
		return nil, err
	}
	return &KnowledgeResult{Community: r}, nil
}

// IsDSLQuery returns true if the input string looks like a DSL query
// (MATCH / PATH / SUBGRAPH prefix, case-insensitive). Useful for the caller
// to decide between GraphQuery and DSL dispatch.
func IsDSLQuery(input string) bool {
	return l3.IsDSLQuery(input)
}
