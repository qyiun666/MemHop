// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Offline interface tests: exercise the public API surface through
// memhop.OpenMulti with a mock OpenAI-compatible LLM server. No external
// services required; run with `go test ./test/...`.

package test

import (
	"testing"

	memhop "github.com/qyiun666/MemHop/api"
	internal "github.com/qyiun666/MemHop/internal"
)

func TestInterfaceL3(t *testing.T) {
	db, _ := openTestDB(t)
	res, err := db.ImportL3([]internal.L3ImportItem{
		{Title: "Go 内存模型", Domain: "go", NodeType: "concept",
			Content: "Go 内存模型定义了 happens-before 规则", Keywords: []string{"go", "内存"}},
	}, internal.L3ImportSkip)
	if err != nil {
		t.Fatalf("ImportL3: %v", err)
	}
	if len(res.CreatedIDs) == 0 {
		t.Fatalf("ImportL3 should create nodes: %+v", res)
	}

	graphs, err := db.ListL3()
	if err != nil {
		t.Fatalf("ListL3: %v", err)
	}
	if len(graphs) != 1 {
		t.Fatalf("want 1 graph, got %d", len(graphs))
	}
	graphID := graphs[0].IDHash

	g, err := db.GetL3(graphID)
	if err != nil {
		t.Fatalf("GetL3: %v", err)
	}
	if len(g.Nodes) == 0 {
		t.Fatal("GetL3 should return nodes")
	}

	nodes, err := db.QueryL3Nodes(internal.L3NodeQuery{GraphID: graphID, Keyword: "go"})
	if err != nil {
		t.Fatalf("QueryL3Nodes: %v", err)
	}
	if len(nodes) == 0 {
		t.Fatal("QueryL3Nodes should find the imported node")
	}

	subgraph, err := db.QueryL3Subgraph(graphID, nodes[0].IDHash, 2, nil)
	if err != nil {
		t.Fatalf("QueryL3Subgraph: %v", err)
	}
	if len(subgraph.Nodes) == 0 {
		t.Fatal("QueryL3Subgraph should return nodes")
	}

	// L2↔L3 lives on the scene: opening a session with an L3 id anchors it,
	// and the domain listing finds that session back.
	anchored, err := db.Search(memhop.SearchQuery{L3ID: graphs[0].IDHash})
	if err != nil {
		t.Fatalf("Search with l3 id: %v", err)
	}
	domainScenes, err := db.ListScenes(graphs[0].IDHash)
	if err != nil {
		t.Fatalf("ListScenesByL3: %v", err)
	}
	if len(domainScenes) != 1 || domainScenes[0].SceneID != anchored.Scene.SceneID {
		t.Fatalf("anchored session missing from its domain: %+v", domainScenes)
	}

	newName := "改名"
	if _, err := db.UpdateL3(graphID, &newName); err != nil {
		t.Fatalf("UpdateL3: %v", err)
	}
	if err := db.DeleteL3(graphID); err != nil {
		t.Fatalf("DeleteL3: %v", err)
	}
	graphs, err = db.ListL3()
	if err != nil {
		t.Fatalf("ListL3 after delete: %v", err)
	}
	if len(graphs) != 0 {
		t.Fatalf("want 0 graphs after delete, got %d", len(graphs))
	}
}

// Correcting one knowledge node must not mean rebuilding the graph: the node
// goes, the hyperedges bound to it go with it, and the other members survive.
func TestInterfaceDeleteL3Nodes(t *testing.T) {
	db, _ := openTestDB(t)
	imported, err := db.ImportL3([]internal.L3ImportItem{
		{Title: "写入路径", Domain: "记忆", NodeType: "concept", Content: "一轮沉淀两条原文",
			Related: []internal.L3Relation{{Titles: []string{"轮次 id", "场景"}, Kind: memhop.EdgeRelated}}},
		{Title: "轮次 id", Domain: "记忆", NodeType: "concept", Content: "由 Search 为这一轮铸出"},
		{Title: "场景", Domain: "记忆", NodeType: "concept", Content: "一个宿主会话"},
	}, internal.L3ImportSkip)
	if err != nil {
		t.Fatalf("ImportL3: %v", err)
	}
	graphID := imported.GraphIDs[0]
	full := mustGraph(t, db, graphID)
	if len(full.Nodes) != 3 || len(full.Edges) != 1 {
		t.Fatalf("imported graph = %d nodes %d edges, want 3/1", len(full.Nodes), len(full.Edges))
	}
	doomed := nodeID(t, full, "轮次 id")

	if err := db.DeleteL3Nodes(graphID, []string{doomed}); err != nil {
		t.Fatalf("DeleteL3Nodes: %v", err)
	}
	after := mustGraph(t, db, graphID)
	if len(after.Nodes) != 2 {
		t.Fatalf("graph keeps %d nodes after the delete: %+v", len(after.Nodes), after.Nodes)
	}
	if _, ok := findNode(after, "轮次 id"); ok {
		t.Fatal("the deleted node is still in the graph")
	}
	// The edge named the deleted node, so it resolves to nothing and must be
	// gone rather than left as a two-member relation nobody asked for.
	if len(after.Edges) != 0 {
		t.Fatalf("the cascade left an edge behind: %+v", after.Edges)
	}
	if _, ok := findNode(after, "场景"); !ok {
		t.Fatalf("a surviving member of the deleted edge went with it: %+v", after.Nodes)
	}

	// Every refusal below has to delete nothing: an id from another graph, an
	// id already deleted, and an empty batch.
	other, err := db.ImportL3([]internal.L3ImportItem{
		{Title: "已退役的检索", Domain: "检索", NodeType: "concept", Content: "不再使用的子系统"},
	}, internal.L3ImportSkip)
	if err != nil {
		t.Fatalf("ImportL3 second domain: %v", err)
	}
	foreign := nodeID(t, mustGraph(t, db, other.GraphIDs[0]), "已退役的检索")
	if err := db.DeleteL3Nodes(graphID, []string{foreign}); err == nil {
		t.Fatal("deleting a node that belongs to another graph should be refused")
	}
	if err := db.DeleteL3Nodes(graphID, []string{doomed}); err == nil {
		t.Fatal("deleting a node twice should be refused, not silently succeed")
	}
	if err := db.DeleteL3Nodes(graphID, nil); err == nil {
		t.Fatal("an empty node batch should be refused")
	}
	if still := mustGraph(t, db, graphID); len(still.Nodes) != 2 || len(still.Edges) != 0 {
		t.Fatalf("the refused deletes changed the graph: %+v", still.Nodes)
	}
	if gone := mustGraph(t, db, other.GraphIDs[0]); len(gone.Nodes) != 1 {
		t.Fatalf("the refused cross-graph delete took another graph's node: %+v", gone.Nodes)
	}
}

func mustGraph(t *testing.T, db *testDB, graphID string) memhop.L3Graph {
	t.Helper()
	g, err := db.GetL3(graphID)
	if err != nil {
		t.Fatalf("GetL3(%s): %v", graphID, err)
	}
	return *g
}

func findNode(g memhop.L3Graph, title string) (string, bool) {
	for _, n := range g.Nodes {
		if n.Title == title {
			return n.IDHash, true
		}
	}
	return "", false
}

func nodeID(t *testing.T, g memhop.L3Graph, title string) string {
	t.Helper()
	id, ok := findNode(g, title)
	if !ok {
		t.Fatalf("graph has no node titled %q: %+v", title, g.Nodes)
	}
	return id
}
