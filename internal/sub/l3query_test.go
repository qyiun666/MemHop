// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package sub

import (
	"testing"

	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
)

// testNode 构造 L3 节点辅助。
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

// writeNode 写入 L3 节点记录。
func writeNode(t *testing.T, engine *core.StorageEngine, n *core.HypergraphNode) {
	t.Helper()
	if err := core.WriteHypergraphNode(engine, n.IDHash, n); err != nil {
		t.Fatalf("write node: %v", err)
	}
}

// writeEdge 写入 L3 边记录。
func writeEdge(t *testing.T, engine *core.StorageEngine, e *core.HypergraphEdge) {
	t.Helper()
	if err := core.WriteHypergraphEdge(engine, e.IDHash, e); err != nil {
		t.Fatalf("write edge: %v", err)
	}
}

// TestQueryL3NodesModes 验证 IDs / Keyword / NodeType 三种模式与 Limit。
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

	// ByIDs：不存在的 ID 跳过。
	out, err := db.QueryL3Nodes(L3NodeQuery{GraphID: graphHex, IDs: []string{common.FormatHash(nodeA.IDHash), common.FormatHash(nodeB.IDHash), common.FormatHash(99999)}})
	if err != nil {
		t.Fatalf("QueryL3Nodes by ids: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("by ids: want 2 nodes, got %d", len(out))
	}

	// ByKeyword：大小写不敏感子串。
	out, err = db.QueryL3Nodes(L3NodeQuery{GraphID: graphHex, Keyword: "RUST"})
	if err != nil {
		t.Fatalf("QueryL3Nodes by keyword: %v", err)
	}
	if len(out) != 1 || out[0].IDHash != nodeA.IDHash {
		t.Fatalf("by keyword: want node A, got %v", out)
	}

	// ByType：精确匹配。
	out, err = db.QueryL3Nodes(L3NodeQuery{GraphID: graphHex, NodeType: "concept"})
	if err != nil {
		t.Fatalf("QueryL3Nodes by type: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("by type: want 2 nodes, got %d", len(out))
	}

	// Limit 截断。
	out, err = db.QueryL3Nodes(L3NodeQuery{GraphID: graphHex, NodeType: "concept", Limit: 1})
	if err != nil {
		t.Fatalf("QueryL3Nodes with limit: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("limit: want 1 node, got %d", len(out))
	}

	// GraphID 必填。
	if _, err := db.QueryL3Nodes(L3NodeQuery{}); err == nil {
		t.Fatal("want error for missing graph id")
	}
}

// TestQueryL3SubgraphDepth 多跳 BFS 与 maxDepth 截断。
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

	// 1 跳：A → B；仅 e1 两端均在子图内。
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

	// 2 跳：A → B → C；e1、e2 入选。
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

	// 3 跳：全图。
	sub, err = db.QueryL3Subgraph(graphHex, common.FormatHash(idA), 3, nil)
	if err != nil {
		t.Fatalf("QueryL3Subgraph depth 3: %v", err)
	}
	if len(sub.Nodes) != 4 || len(sub.Edges) != 3 {
		t.Fatalf("depth 3: want 4 nodes / 3 edges, got %d / %d", len(sub.Nodes), len(sub.Edges))
	}

	// maxDepth<=0 视为 1 跳。
	sub, err = db.QueryL3Subgraph(graphHex, common.FormatHash(idA), 0, nil)
	if err != nil {
		t.Fatalf("QueryL3Subgraph depth 0: %v", err)
	}
	if len(sub.Nodes) != 2 {
		t.Fatalf("depth 0: want 2 nodes, got %d", len(sub.Nodes))
	}
}

// TestQueryL3SubgraphHyperedge 超边降级：一条边连接三节点，1 跳可达全部。
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

// TestQueryL3SubgraphEdgeKinds edgeKinds 过滤：仅 causal 时 A 无邻居。
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

// TestQueryL3SubgraphStartMissing 起始节点不存在返回 ErrNotFound。
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
