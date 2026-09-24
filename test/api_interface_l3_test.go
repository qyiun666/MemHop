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
	graphID := graphs[0].ID

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

	subgraph, err := db.QueryL3Subgraph(graphID, nodes[0].ID, 2, nil)
	if err != nil {
		t.Fatalf("QueryL3Subgraph: %v", err)
	}
	if len(subgraph.Nodes) != 1 || len(subgraph.Edges) != 0 {
		t.Fatalf("QueryL3Subgraph = %+v, want the start node alone: it has no edge to reach", subgraph)
	}
	assertImportedNode(t, subgraph.Nodes[0], graphID, res.CreatedIDs[0])

	// L2↔L3 lives on the scene: opening a new session with an L3 id anchors it,
	// and the domain listing finds that session back. NewScene is what asks for a
	// session here — an unnamed read continues the one the domain is on, and naming
	// an anchor onto a scene that already exists is refused.
	anchored, err := db.Search(memhop.SearchQuery{NewScene: true, L3ID: graphs[0].ID})
	if err != nil {
		t.Fatalf("Search with l3 id: %v", err)
	}
	domainScenes, err := db.ListScenes(graphs[0].ID)
	if err != nil {
		t.Fatalf("ListScenesByL3: %v", err)
	}
	if len(domainScenes) != 1 || domainScenes[0].SceneID != anchored.Scene.SceneID {
		t.Fatalf("anchored session missing from its domain: %+v", domainScenes)
	}

	newName := "改名"
	if _, err := db.UpdateL3(graphID, newName); err != nil {
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
	if n.ID != idHash || n.GraphID != graphID {
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

// Renaming a graph is a label change and nothing else: the id a scene anchored on and the
// host's own node ids stay put, and after the change **both** labels reach the same graph —
// the one it was created under, because the id derives from that first label, and the new
// one, because the slots are also matched by name. That is the difference between a rename
// and a split, and the tool schema a host publishes for ImportL3 carries a label, not an id.
func TestInterfaceGraphRenameKeepsItsIdAndBothLabelsRoute(t *testing.T) {
	db, _ := openTestDB(t)
	first, err := db.ImportL3([]memhop.L3ImportItem{
		{Title: "引擎", Domain: "memhop", NodeType: "concept", Content: "记忆库"},
		{Title: "格式", Domain: "memhop", NodeType: "concept", Content: "26 字节帧"},
	}, memhop.L3ImportSkip)
	if err != nil || len(first.CreatedIDs) != 2 || len(first.GraphIDs) != 1 {
		t.Fatalf("first import: %+v err %v", first, err)
	}
	graphID := first.GraphIDs[0]
	anchored, err := db.Search(memhop.SearchQuery{NewScene: true, L3ID: graphID})
	if err != nil {
		t.Fatalf("anchor a scene on the graph: %v", err)
	}

	renamed, err := db.UpdateL3(graphID, "记忆引擎项目")
	if err != nil {
		t.Fatalf("UpdateL3: %v", err)
	}
	if renamed.Slot.ID != graphID {
		t.Fatalf("the rename moved the graph: %s -> %s", graphID, renamed.Slot.ID)
	}
	if renamed.Slot.Name != "记忆引擎项目" || len(renamed.Nodes) != 2 {
		t.Fatalf("the rename did not restatement the label or disturbed the members: %+v", renamed.Slot)
	}

	// The anchor is keyed by id, so a label change leaves the scene's listing alone.
	scenes, err := db.ListScenes(graphID)
	if err != nil || len(scenes) != 1 || scenes[0].SceneID != anchored.Scene.SceneID {
		t.Fatalf("anchored listing after the rename = %+v err %v", scenes, err)
	}

	// Both labels reach the same graph, and neither of them opens a second one.
	for _, label := range []string{"记忆引擎项目", "memhop"} {
		batch, err := db.ImportL3([]memhop.L3ImportItem{
			{Title: "来自 " + label, Domain: label, NodeType: "concept", Content: "同一张图"},
		}, memhop.L3ImportSkip)
		if err != nil {
			t.Fatalf("import by label %q: %v", label, err)
		}
		if len(batch.GraphIDs) != 1 || batch.GraphIDs[0] != graphID {
			t.Fatalf("importing the domain as %q resolved %v, want the one graph %s — a rename must not split it",
				label, batch.GraphIDs, graphID)
		}
	}
	graphs, err := db.ListL3()
	if err != nil || len(graphs) != 1 {
		t.Fatalf("ListL3 after imports by both labels = %+v err %v, want the single renamed graph", graphs, err)
	}
	nodes, err := db.QueryL3Nodes(memhop.L3NodeQuery{GraphID: graphID})
	if err != nil || len(nodes) != 4 {
		t.Fatalf("the graph now holds %+v err %v, want the two original nodes plus the two labelled imports", nodes, err)
	}
}

// A model inventing the far side of a relation is the ordinary failure on the tool path,
// and so is a title that lives in another graph: the batch must say which item it could not
// link instead of returning a success whose graph simply has no edge.
func TestInterfaceImportReportsAnUnlinkableRelation(t *testing.T) {
	db, _ := openTestDB(t)
	res, err := db.ImportL3([]memhop.L3ImportItem{
		{Title: "引擎", Domain: "memhop", NodeType: "concept", Content: "记忆库"},
		{Title: "格式", Domain: "other-domain", NodeType: "concept", Content: "26 字节帧"},
		{Title: "引擎", Domain: "memhop", NodeType: "concept", Content: "记忆库", Related: []memhop.L3Relation{
			{Titles: []string{"格式", "根本不存在的标题"}, Kind: memhop.EdgeRelated},
		}},
	}, memhop.L3ImportSkip)
	if err != nil {
		t.Fatalf("ImportL3: %v", err)
	}
	if len(res.Errors) == 0 {
		t.Fatalf("a batch whose relations named no reachable node reported no errors: %+v", res)
	}
	graphs := map[string]memhop.L3Graph{}
	for _, id := range res.GraphIDs {
		g, err := db.GetL3(id)
		if err != nil {
			t.Fatalf("GetL3(%s): %v", id, err)
		}
		graphs[id] = *g
	}
	if len(graphs) != 2 {
		t.Fatalf("two domains imported into %d graphs, want one per domain: %+v", len(graphs), res.GraphIDs)
	}
	for id, g := range graphs {
		for _, e := range g.Edges {
			t.Fatalf("graph %s carries an edge %s built across domains or from an invented title: %+v", id, e.ID, e.NodeIDs)
		}
	}
	// The nodes themselves landed: a refused link is not a refused batch.
	for _, g := range graphs {
		if len(g.Nodes) == 0 {
			t.Fatalf("the batch that reported a bad link also stored no nodes: %+v", g)
		}
	}
}
