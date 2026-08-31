// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L3 hypergraph surface tests (import/query/subgraph).

package api

import (
	"testing"
)

func TestSurfaceL3Knowledge(t *testing.T) {
	db := openSurfaceDB(t)
	items := []L3ImportItem{
		{Title: "Rust ownership", Domain: "lang", NodeType: "concept", Content: "borrow checker", Keywords: []string{"rust", "ownership"}},
		{Title: "Go routines", Domain: "lang", NodeType: "concept", Content: "scheduler", Keywords: []string{"go", "goroutine"}},
	}
	imp, err := db.ImportL3(items, L3ImportSkip)
	if err != nil {
		t.Fatalf("import l3: %v", err)
	}
	if imp.CreatedIDs == nil {
		t.Fatal("L3ImportResult.CreatedIDs must be non-nil")
	}
	for _, id := range imp.CreatedIDs {
		if !isHexID(id) {
			t.Fatalf("created node id not hex: %q", id)
		}
	}
	graphs, err := db.ListL3()
	if err != nil || len(graphs) == 0 {
		t.Fatalf("list l3 graphs: %d err=%v", len(graphs), err)
	}
	// ImportL3 建 manual 图谱：Source.Manual 的 ContextID 恒为 0，api 面必须渲染
	// 空串而非 16 个 0（formatOptionalID 语义）。
	if got := graphs[0].Source; got.Kind != "manual" || got.ContextID != "" {
		t.Fatalf("manual graph source should render kind=manual context_id=%q, got kind=%q context_id=%q",
			"", got.Kind, got.ContextID)
	}
	graphID := graphs[0].IDHash
	g, err := db.GetL3(graphID)
	if err != nil || g == nil || g.Nodes == nil {
		t.Fatalf("get l3 graph: %v", err)
	}
	newName := "renamed-domain"
	if _, err := db.UpdateL3(graphID, &newName); err != nil {
		t.Fatalf("update l3: %v", err)
	}
	nodes, err := db.QueryL3Nodes(L3NodeQuery{GraphID: graphID, Keyword: "rust"})
	if err != nil {
		t.Fatalf("query nodes by keyword: %v", err)
	}
	if len(nodes) == 0 {
		t.Fatal("keyword query should match the rust node")
	}
	if _, err := db.QueryL3Nodes(L3NodeQuery{GraphID: ""}); CodeOf(err) != ErrInvalidQuery {
		t.Fatalf("query nodes missing graph: want ErrInvalidQuery, got %v", err)
	}
	if _, err := db.QueryL3Subgraph(graphID, nodes[0].IDHash, 2, []GraphEdgeKind{EdgeRelated}); err != nil {
		t.Fatalf("query subgraph: %v", err)
	}
	if err := db.DeleteL3(graphID); err != nil {
		t.Fatalf("delete l3: %v", err)
	}
}
