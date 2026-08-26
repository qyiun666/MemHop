// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"path/filepath"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

func newL3TestDB(t *testing.T) *DB {
	t.Helper()
	engine, err := core.Create(filepath.Join(t.TempDir(), "l3.meh"), 16)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := engine.Close(&core.IndexSnapshotData{}); err != nil {
			t.Errorf("close engine: %v", err)
		}
	})
	return newTestDB(t, engine)
}

func l3TestGraph(t *testing.T, db *DB) *L3Graph {
	t.Helper()
	graphs, err := db.ListL3(core.DefaultAgentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(graphs) != 1 {
		t.Fatalf("want 1 graph, got %d", len(graphs))
	}
	graph, err := db.getL3Graph(core.DefaultAgentID, common.FormatHash(graphs[0].IDHash))
	if err != nil {
		t.Fatal(err)
	}
	return graph
}

func TestImportL3OverwriteExisting(t *testing.T) {
	db := newL3TestDB(t)
	items := []L3ImportItem{{
		Title: "go-memory-model", Domain: "go", NodeType: "concept",
		Content: "old content", Keywords: []string{"old"},
	}}
	if _, err := db.ImportL3(core.DefaultAgentID, items, L3ImportSkip); err != nil {
		t.Fatal(err)
	}

	items[0].NodeType = "fact"
	items[0].Content = "new content"
	items[0].Keywords = []string{"new"}
	res, err := db.ImportL3(core.DefaultAgentID, items, L3ImportOverwrite)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.UpdatedIDs) != 1 || len(res.CreatedIDs) != 0 {
		t.Fatalf("unexpected result: %+v", res)
	}
	graph := l3TestGraph(t, db)
	if len(graph.Nodes) != 1 {
		t.Fatalf("nodes: %+v", graph.Nodes)
	}
	got := graph.Nodes[0]
	if got.Content != "new content" || got.NodeType != "fact" {
		t.Fatalf("overwrite did not apply: %+v", got)
	}
	if len(got.Keywords) != 1 || got.Keywords[0] != "new" {
		t.Fatalf("overwrite keywords: %+v", got.Keywords)
	}
}

func TestImportL3MergeExisting(t *testing.T) {
	db := newL3TestDB(t)
	items := []L3ImportItem{{
		Title: "merge-node", Domain: "go", NodeType: "concept",
		Content: "base", Keywords: []string{"a"},
	}}
	if _, err := db.ImportL3(core.DefaultAgentID, items, L3ImportOverwrite); err != nil {
		t.Fatal(err)
	}

	items[0].NodeType = "fact"
	items[0].Content = "extra"
	items[0].Keywords = []string{"b", "a"}
	res, err := db.ImportL3(core.DefaultAgentID, items, L3ImportMerge)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.UpdatedIDs) != 1 || len(res.CreatedIDs) != 0 {
		t.Fatalf("unexpected result: %+v", res)
	}
	graph := l3TestGraph(t, db)
	got := graph.Nodes[0]
	if got.Content != "base\nextra" {
		t.Fatalf("merge content: %q", got.Content)
	}
	if got.NodeType != "fact" {
		t.Fatalf("merge node type: %q", got.NodeType)
	}
	if len(got.Keywords) != 2 || got.Keywords[0] != "a" || got.Keywords[1] != "b" {
		t.Fatalf("merge keywords: %+v", got.Keywords)
	}
}

func TestImportL3SkipExisting(t *testing.T) {
	db := newL3TestDB(t)
	items := []L3ImportItem{{
		Title: "skip-node", Domain: "go", NodeType: "concept", Content: "keep",
	}}
	if _, err := db.ImportL3(core.DefaultAgentID, items, L3ImportOverwrite); err != nil {
		t.Fatal(err)
	}
	items[0].Content = "changed"
	res, err := db.ImportL3(core.DefaultAgentID, items, L3ImportSkip)
	if err != nil {
		t.Fatal(err)
	}
	if res.SkippedCount != 1 || len(res.UpdatedIDs) != 0 || len(res.CreatedIDs) != 0 {
		t.Fatalf("unexpected result: %+v", res)
	}
	graph := l3TestGraph(t, db)
	if got := graph.Nodes[0].Content; got != "keep" {
		t.Fatalf("skip changed content: %q", got)
	}
}

func TestImportL3RejectsUnknownMode(t *testing.T) {
	db := newL3TestDB(t)
	_, err := db.ImportL3(core.DefaultAgentID, []L3ImportItem{{Title: "x", Domain: "d"}}, L3ImportMode("bogus"))
	if err == nil || common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("expected ErrInvalidQuery, got %v", err)
	}
	if got := core.CollectAllGraphSlots(db.engine, core.DefaultAgentID); len(got) != 0 {
		t.Fatalf("no graph should be created for invalid mode: %+v", got)
	}
}
