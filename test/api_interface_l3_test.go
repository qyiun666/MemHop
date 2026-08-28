// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Offline interface tests: exercise the public API surface through
// memhop.OpenWithEncoder with a mock encoder and a mock OpenAI-compatible
// LLM server. No external services required; run with `go test ./test/...`.

package test

import (
	"context"
	"testing"
	"time"

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

	// Search must link the matching L3 graph onto the new topic as L3Refs,
	// which is what makes DirectedL3ID scoping work.
	sres, err := db.Search(context.Background(), internal.SearchQuery{Text: "Go 内存模型", AutoCreate: true, Timestamp: time.Now().UnixMilli()})
	if err != nil {
		t.Fatalf("Search after ImportL3: %v", err)
	}
	linked := false
	for _, topic := range sres.Contexts {
		if topic.ID != sres.NewTopicID {
			continue
		}
		for _, ref := range topic.L3Refs {
			if ref == graphs[0].IDHash {
				linked = true
			}
		}
	}
	if !linked {
		t.Fatalf("Search should link matching L3 graph into topic L3Refs: %+v", sres.Contexts)
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
