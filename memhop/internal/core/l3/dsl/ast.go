// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package dsl implements the L3 hypergraph query DSL.
package dsl

// Query is the top-level AST node for a DSL query.
type Query struct {
	Match     *NodeMatch
	Hyperedge *HyperedgeMatch
	Path      *PathQuery
	Subgraph  *SubgraphQuery
}

// NodeMatch represents: MATCH (n:concept) WHERE ... LIMIT 10
type NodeMatch struct {
	Variable    string
	NodeType    string
	WhereClause *WhereCondition
	Limit       int
}

// HyperedgeMatch represents: MATCH HYPEREDGE e-[n1, n2]- WHERE ... LIMIT 10
type HyperedgeMatch struct {
	EdgeVar     string
	NodeVars    []string
	WhereClause *WhereCondition
	Limit       int
}

// PathQuery represents: PATH FROM "node_id" DEPTH 3 EDGE_KINDS [...]
type PathQuery struct {
	StartNode string
	MaxDepth  int
	EdgeKinds []string
}

// SubgraphQuery represents: SUBGRAPH FROM "node_id" DEPTH 2
type SubgraphQuery struct {
	StartNode string
	MaxDepth  int
}

// WhereCondition is a WHERE clause condition tree.
type WhereCondition struct {
	PropertyCompare *PropertyCompareCondition
	TypeEquals      *string
	KeywordContains *string
	And             *BinaryCondition
	Or              *BinaryCondition
}

// PropertyCompareCondition represents: n.importance > 0.5
type PropertyCompareCondition struct {
	Property string
	Operator CompareOp
	Value    float32
}

// BinaryCondition represents AND/OR of two conditions.
type BinaryCondition struct {
	Left  *WhereCondition
	Right *WhereCondition
}

// CompareOp is a comparison operator.
type CompareOp int

const (
	OpGt CompareOp = iota
	OpGe
	OpLt
	OpLe
	OpEq
	OpNe
)
