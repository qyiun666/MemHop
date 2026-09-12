// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Offline interface tests: exercise the public API surface through
// memhop.Open with a mock OpenAI-compatible LLM server. No external
// services required; run with `go test ./test/...`.

package test

import (
	"encoding/json"
	"slices"
	"strings"
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
	if len(res.CreatedIDs) != 1 || len(res.UpdatedIDs) != 0 || res.SkippedCount != 0 ||
		len(res.Errors) != 0 || len(res.GraphIDs) != 1 {
		t.Fatalf("ImportL3 = %+v, want the one node created into the one graph it named", res)
	}
	// The result is a public alias of the internal record, so its tags are the wire
	// contract: a clean batch answers with every list present, and "no failures"
	// reads as [] rather than as a key a client cannot tell from "not reported".
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("encode the import result: %v", err)
	}
	if !strings.Contains(string(raw), `"errors":[]`) || !strings.Contains(string(raw), `"graph_ids":[`) {
		t.Fatalf("import result JSON = %s, want errors and graph_ids present as lists", raw)
	}

	graphs, err := db.ListL3()
	if err != nil {
		t.Fatalf("ListL3: %v", err)
	}
	if len(graphs) != 1 || graphs[0].Name != "go" {
		t.Fatalf("ListL3 = %+v, want the one graph under the domain it was imported into", graphs)
	}
	graphID := graphs[0].IDHash

	g, err := db.GetL3(graphID)
	if err != nil {
		t.Fatalf("GetL3: %v", err)
	}
	if len(g.Nodes) != 1 || len(g.Edges) != 0 {
		t.Fatalf("GetL3 = %+v, want the one imported node and no edge", g)
	}
	assertImportedNode(t, g.Nodes[0], graphID, res.CreatedIDs[0])

	nodes, err := db.QueryL3Nodes(internal.L3NodeQuery{GraphID: graphID, Keyword: "go"})
	if err != nil {
		t.Fatalf("QueryL3Nodes: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("QueryL3Nodes = %+v, want the one node whose keyword track holds %q", nodes, "go")
	}
	assertImportedNode(t, nodes[0], graphID, res.CreatedIDs[0])

	subgraph, err := db.QueryL3Subgraph(graphID, nodes[0].IDHash, 2, nil)
	if err != nil {
		t.Fatalf("QueryL3Subgraph: %v", err)
	}
	if len(subgraph.Nodes) != 1 || len(subgraph.Edges) != 0 {
		t.Fatalf("QueryL3Subgraph = %+v, want the start node alone: it has no edge to reach", subgraph)
	}
	assertImportedNode(t, subgraph.Nodes[0], graphID, res.CreatedIDs[0])

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

// assertImportedNode pins the one node this test imported, field by field. Three
// read paths hand it back, and a count alone would let any of them return a node
// whose title, body or keyword track was rewritten on the way out.
func assertImportedNode(t *testing.T, n memhop.HypergraphNode, graphID, idHash string) {
	t.Helper()
	if n.IDHash != idHash || n.GraphID != graphID {
		t.Fatalf("node ids = %+v, want %s in graph %s", n, idHash, graphID)
	}
	if n.Title != "Go 内存模型" || n.NodeType != "concept" ||
		n.Content != "Go 内存模型定义了 happens-before 规则" {
		t.Fatalf("node = %+v, want the imported title, type and content", n)
	}
	if !slices.Equal(n.Keywords, []string{"go", "内存"}) {
		t.Fatalf("node keywords = %q, want the imported track", n.Keywords)
	}
}
