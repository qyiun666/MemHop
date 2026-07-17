// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package dsl

// QueryResult is the result of executing a DSL query.
type QueryResult struct {
	Nodes    *NodeResultList `json:"nodes,omitempty"`
	Edges    *EdgeResultList `json:"edges,omitempty"`
	Hops     *HopResultList  `json:"hops,omitempty"`
	Subgraph *SubgraphResult `json:"subgraph,omitempty"`
}

// NodeResultList holds matched nodes.
type NodeResultList struct {
	Items []NodeResult `json:"items"`
	Total int          `json:"total"`
}

// EdgeResultList holds matched edges.
type EdgeResultList struct {
	Items []EdgeResult `json:"items"`
	Total int          `json:"total"`
}

// HopResultList holds traversal hops.
type HopResultList struct {
	Items []HopResult `json:"items"`
	Total int         `json:"total"`
}

// SubgraphResult holds extracted subgraph.
type SubgraphResult struct {
	Nodes []NodeResult `json:"nodes"`
	Edges []EdgeResult `json:"edges"`
}

// NodeResult is a matched graph node.
type NodeResult struct {
	IDHash     string   `json:"id_hash"`
	GraphID    string   `json:"graph_id"`
	Title      string   `json:"title"`
	NodeType   string   `json:"node_type"`
	Content    string   `json:"content,omitempty"`
	Keywords   []string `json:"keywords,omitempty"`
	Importance float32  `json:"importance"`
}

// EdgeResult is a matched graph edge.
type EdgeResult struct {
	IDHash  string   `json:"id_hash"`
	GraphID string   `json:"graph_id"`
	Kind    string   `json:"kind"`
	NodeIDs []string `json:"node_ids"`
	Weight  float32  `json:"weight"`
}

// HopResult is a single traversal hop.
type HopResult struct {
	FromNode string     `json:"from_node"`
	ToNode   string     `json:"to_node"`
	Edge     EdgeResult `json:"edge"`
	Depth    int        `json:"depth"`
}
