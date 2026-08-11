// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package sub

import (
	"testing"

	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
)

// testNode builds an L3 node helper.
func testNode(id, graphID uint64, title, nodeType, content string, kws []string) core.HypergraphNode {
	return core.HypergraphNode{
		IDHash:   id,
		GraphID:  graphID,
		Title:    title,
		NodeType: nodeType,
		Content:  content,
		Keywords: kws,
	}
}

// writeNode writes an L3 node record.
func writeNode(t *testing.T, engine *core.StorageEngine, n *core.HypergraphNode) {
	t.Helper()
	if err := core.WriteHypergraphNode(engine, n.IDHash, n); err != nil {
		t.Fatalf("write node: %v", err)
	}
}

// writeEdge writes an L3 edge record.
func writeEdge(t *testing.T, engine *core.StorageEngine, e *core.HypergraphEdge) {
	t.Helper()
	if err := core.WriteHypergraphEdge(engine, e.IDHash, e); err != nil {
		t.Fatalf("write edge: %v", err)
	}
}

// TestQueryL3NodesModes verifies the IDs/Keyword/NodeType modes and Limit.
func TestQueryL3NodesModes(t *testing.T) {
	engine := newTestEngine(t)
	db := &DB{engine: engine}
	graphID := common.HashID("graph")
	nodeA := testNode(common.HashID("a"), graphID, "Rust 所有权", "concept", "所有权系统", []string{"rust", "memory"})
	nodeB := testNode(common.HashID("b"), graphID, "Go 并发", "concept", "goroutine 与 channel", []string{"go"})
	nodeC := testNode(common.HashID("c"), graphID, "数据库索引", "tool", "B+ 树", []string{"db"})
	writeNode(t, engine, &nodeA)
	writeNode(t, engine, &nodeB)
	writeNode(t, engine, &nodeC)
	graphHex := common.FormatHash(graphID)

	// ByIDs: missing IDs are skipped.
	out, err := db.QueryL3Nodes(L3NodeQuery{GraphID: graphHex, IDs: []string{common.FormatHash(nodeA.IDHash), common.FormatHash(nodeB.IDHash), common.FormatHash(99999)}})
	if err != nil {
		t.Fatalf("QueryL3Nodes by ids: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("by ids: want 2 nodes, got %d", len(out))
	}

	// ByKeyword: case-insensitive substring.
	out, err = db.QueryL3Nodes(L3NodeQuery{GraphID: graphHex, Keyword: "RUST"})
	if err != nil {
		t.Fatalf("QueryL3Nodes by keyword: %v", err)
	}
	if len(out) != 1 || out[0].IDHash != nodeA.IDHash {
		t.Fatalf("by keyword: want node A, got %v", out)
	}

	// ByType: exact match.
	out, err = db.QueryL3Nodes(L3NodeQuery{GraphID: graphHex, NodeType: "concept"})
	if err != nil {
		t.Fatalf("QueryL3Nodes by type: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("by type: want 2 nodes, got %d", len(out))
	}

	// Limit truncation.
	out, err = db.QueryL3Nodes(L3NodeQuery{GraphID: graphHex, NodeType: "concept", Limit: 1})
	if err != nil {
		t.Fatalf("QueryL3Nodes with limit: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("limit: want 1 node, got %d", len(out))
	}

	// GraphID required.
	if _, err := db.QueryL3Nodes(L3NodeQuery{}); err == nil {
		t.Fatal("want error for missing graph id")
	}
}

// TestQueryL3SubgraphDepth multi-hop BFS with maxDepth truncation.
func TestQueryL3SubgraphDepth(t *testing.T) {
	engine := newTestEngine(t)
	db := &DB{engine: engine}
	graphID := common.HashID("graph")
	idA, idB, idC, idD := common.HashID("a"), common.HashID("b"), common.HashID("c"), common.HashID("d")
	for _, n := range []core.HypergraphNode{
		testNode(idA, graphID, "A", "t", "", nil),
		testNode(idB, graphID, "B", "t", "", nil),
		testNode(idC, graphID, "C", "t", "", nil),
		testNode(idD, graphID, "D", "t", "", nil),
	} {
		writeNode(t, engine, &n)
	}
	e1 := core.HypergraphEdge{IDHash: common.HashID("e1"), GraphID: graphID, Kind: core.EdgeRelated, NodeIDs: []uint64{idA, idB}}
	e2 := core.HypergraphEdge{IDHash: common.HashID("e2"), GraphID: graphID, Kind: core.EdgeCausal, NodeIDs: []uint64{idB, idC}}
	e3 := core.HypergraphEdge{IDHash: common.HashID("e3"), GraphID: graphID, Kind: core.EdgeCausal, NodeIDs: []uint64{idC, idD}}
	writeEdge(t, engine, &e1)
	writeEdge(t, engine, &e2)
	writeEdge(t, engine, &e3)
	graphHex := common.FormatHash(graphID)

	// 1 hop: A -> B; only e1 has both ends in the subgraph.
	sub, err := db.QueryL3Subgraph(graphHex, common.FormatHash(idA), 1, nil)
	if err != nil {
		t.Fatalf("QueryL3Subgraph depth 1: %v", err)
	}
	if len(sub.Nodes) != 2 {
		t.Fatalf("depth 1: want 2 nodes, got %d", len(sub.Nodes))
	}
	if len(sub.Edges) != 1 || sub.Edges[0].IDHash != e1.IDHash {
		t.Fatalf("depth 1: want only e1, got %v", sub.Edges)
	}

	// 2 hops: A -> B -> C; e1 and e2 selected.
	sub, err = db.QueryL3Subgraph(graphHex, common.FormatHash(idA), 2, nil)
	if err != nil {
		t.Fatalf("QueryL3Subgraph depth 2: %v", err)
	}
	if len(sub.Nodes) != 3 {
		t.Fatalf("depth 2: want 3 nodes, got %d", len(sub.Nodes))
	}
	if len(sub.Edges) != 2 {
		t.Fatalf("depth 2: want 2 edges, got %d", len(sub.Edges))
	}

	// 3 hops: the whole graph.
	sub, err = db.QueryL3Subgraph(graphHex, common.FormatHash(idA), 3, nil)
	if err != nil {
		t.Fatalf("QueryL3Subgraph depth 3: %v", err)
	}
	if len(sub.Nodes) != 4 || len(sub.Edges) != 3 {
		t.Fatalf("depth 3: want 4 nodes / 3 edges, got %d / %d", len(sub.Nodes), len(sub.Edges))
	}

	// maxDepth<=0 counts as 1 hop.
	sub, err = db.QueryL3Subgraph(graphHex, common.FormatHash(idA), 0, nil)
	if err != nil {
		t.Fatalf("QueryL3Subgraph depth 0: %v", err)
	}
	if len(sub.Nodes) != 2 {
		t.Fatalf("depth 0: want 2 nodes, got %d", len(sub.Nodes))
	}
}

// TestQueryL3SubgraphHyperedge hyperedge: one edge linking three nodes, all reachable in 1 hop.
func TestQueryL3SubgraphHyperedge(t *testing.T) {
	engine := newTestEngine(t)
	db := &DB{engine: engine}
	graphID := common.HashID("graph")
	idA, idB, idC := common.HashID("a"), common.HashID("b"), common.HashID("c")
	for _, n := range []core.HypergraphNode{
		testNode(idA, graphID, "A", "t", "", nil),
		testNode(idB, graphID, "B", "t", "", nil),
		testNode(idC, graphID, "C", "t", "", nil),
	} {
		writeNode(t, engine, &n)
	}
	eH := core.HypergraphEdge{IDHash: common.HashID("eh"), GraphID: graphID, Kind: core.EdgeRelated, NodeIDs: []uint64{idA, idB, idC}}
	writeEdge(t, engine, &eH)

	sub, err := db.QueryL3Subgraph(common.FormatHash(graphID), common.FormatHash(idA), 1, nil)
	if err != nil {
		t.Fatalf("QueryL3Subgraph hyperedge: %v", err)
	}
	if len(sub.Nodes) != 3 {
		t.Fatalf("hyperedge: want 3 nodes, got %d", len(sub.Nodes))
	}
	if len(sub.Edges) != 1 {
		t.Fatalf("hyperedge: want 1 edge, got %d", len(sub.Edges))
	}
}

// TestQueryL3SubgraphEdgeKinds edgeKinds filter: with causal only, A has no neighbors.
func TestQueryL3SubgraphEdgeKinds(t *testing.T) {
	engine := newTestEngine(t)
	db := &DB{engine: engine}
	graphID := common.HashID("graph")
	idA, idB := common.HashID("a"), common.HashID("b")
	for _, n := range []core.HypergraphNode{
		testNode(idA, graphID, "A", "t", "", nil),
		testNode(idB, graphID, "B", "t", "", nil),
	} {
		writeNode(t, engine, &n)
	}
	e1 := core.HypergraphEdge{IDHash: common.HashID("e1"), GraphID: graphID, Kind: core.EdgeRelated, NodeIDs: []uint64{idA, idB}}
	writeEdge(t, engine, &e1)

	sub, err := db.QueryL3Subgraph(common.FormatHash(graphID), common.FormatHash(idA), 3, []core.GraphEdgeKind{core.EdgeCausal})
	if err != nil {
		t.Fatalf("QueryL3Subgraph kinds: %v", err)
	}
	if len(sub.Nodes) != 1 || len(sub.Edges) != 0 {
		t.Fatalf("kinds filter: want 1 node / 0 edges, got %d / %d", len(sub.Nodes), len(sub.Edges))
	}
}

// TestQueryL3SubgraphStartMissing missing start node returns ErrNotFound.
func TestQueryL3SubgraphStartMissing(t *testing.T) {
	db := &DB{engine: newTestEngine(t)}
	_, err := db.QueryL3Subgraph(common.FormatHash(1), common.FormatHash(99999), 1, nil)
	if err == nil {
		t.Fatal("want error for missing start node")
	}
	if common.CodeOf(err) != common.ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
