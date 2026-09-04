// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L3 hypergraph surface tests (import/query/subgraph).

package api

import (
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
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

// TestSurfaceL3GraphLabelIsUnique pins what a host can reach of the graph
// identity: a domain label addresses one graph, because ImportL3 routes a
// domain by that label. Renaming a graph onto a label another graph carries is
// refused and changes nothing.
func TestSurfaceL3GraphLabelIsUnique(t *testing.T) {
	db := openSurfaceDB(t)
	if _, err := db.ImportL3([]L3ImportItem{{Title: "a", Domain: "alpha"}}, L3ImportSkip); err != nil {
		t.Fatalf("import alpha: %v", err)
	}
	if _, err := db.ImportL3([]L3ImportItem{{Title: "b", Domain: "beta"}}, L3ImportSkip); err != nil {
		t.Fatalf("import beta: %v", err)
	}
	alphaID := common.FormatHash(common.HashID("alpha"))

	taken := "beta"
	if _, err := db.UpdateL3(alphaID, &taken); CodeOf(err) != ErrInvalidQuery {
		t.Fatalf("rename onto a taken label: code=%v err=%v", CodeOf(err), err)
	}
	g, err := db.GetL3(alphaID)
	if err != nil || g.Slot.Name != "alpha" {
		t.Fatalf("refused rename changed the graph: name=%q err=%v", g.Slot.Name, err)
	}
	// A free label still renames, and renaming back onto itself is a no-op.
	free := "alpha-project"
	if _, err := db.UpdateL3(alphaID, &free); err != nil {
		t.Fatalf("rename onto a free label: %v", err)
	}
	if _, err := db.UpdateL3(alphaID, &free); err != nil {
		t.Fatalf("rename onto its own label: %v", err)
	}
	// The label a graph was renamed to keeps addressing that same graph.
	res, err := db.ImportL3([]L3ImportItem{{Title: "c", Domain: free}}, L3ImportSkip)
	if err != nil || len(res.GraphIDs) != 1 || res.GraphIDs[0] != alphaID {
		t.Fatalf("import under the renamed label routed to %v want [%s]: err=%v", res.GraphIDs, alphaID, err)
	}
}

// TestSurfaceL3DeleteDropsSceneAnchor pins the L3 -> L2 direction of the anchor
// contract: both write paths refuse a graph that does not exist, so deleting the
// graph has to unanchor the scenes that named it.
func TestSurfaceL3DeleteDropsSceneAnchor(t *testing.T) {
	db := openSurfaceDB(t)
	proj, err := db.ImportL3([]L3ImportItem{{Title: "p", Domain: "proj"}}, L3ImportSkip)
	if err != nil {
		t.Fatalf("import proj: %v", err)
	}
	other, err := db.ImportL3([]L3ImportItem{{Title: "o", Domain: "keep"}}, L3ImportSkip)
	if err != nil {
		t.Fatalf("import keep: %v", err)
	}
	sr, err := db.Search(SearchQuery{L3ID: proj.GraphIDs[0]})
	if err != nil {
		t.Fatalf("search anchored: %v", err)
	}
	if sr.Scene.L3ID != proj.GraphIDs[0] {
		t.Fatalf("scene not anchored on create: %q", sr.Scene.L3ID)
	}
	if _, err := db.Search(SearchQuery{L3ID: other.GraphIDs[0]}); err != nil {
		t.Fatalf("search second scene: %v", err)
	}

	if err := db.DeleteL3(proj.GraphIDs[0]); err != nil {
		t.Fatalf("DeleteL3: %v", err)
	}
	scenes, err := db.ListScenes("")
	if err != nil || len(scenes) != 2 {
		t.Fatalf("list scenes: n=%d err=%v", len(scenes), err)
	}
	for _, s := range scenes {
		switch s.SceneID {
		case sr.Scene.SceneID:
			if s.L3ID != "" {
				t.Errorf("deleted graph still anchored: l3_id=%q", s.L3ID)
			}
		default:
			if s.L3ID != other.GraphIDs[0] {
				t.Errorf("sibling scene anchor damaged: l3_id=%q", s.L3ID)
			}
		}
	}
	if byProj, _ := db.ListScenes(proj.GraphIDs[0]); len(byProj) != 0 {
		t.Errorf("ListScenes(deleted graph) still returns %d scenes", len(byProj))
	}
	// The scene itself stays readable — only the anchor goes.
	if _, err := db.SceneContext(sr.Scene.SceneID); err != nil {
		t.Errorf("SceneContext after its graph was deleted: %v", err)
	}
}
