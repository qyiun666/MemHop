// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Tests for index types migrated from l3 package.

package index

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/qyiun666/MemHop/internal/sub/repo/core"
)

func tempEngine(t *testing.T) *core.StorageEngine {
	t.Helper()
	p := filepath.Join(t.TempDir(), "test.meh")
	eng, err := core.Create(p, 128)
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	t.Cleanup(func() { eng.Close(&core.IndexSnapshotData{}) })
	return eng
}

func makeTestNode(id uint64, graphID uint64, title, nodeType, content string, keywords []string) *core.HypergraphNode {
	return &core.HypergraphNode{
		IDHash:     id,
		GraphID:    graphID,
		Title:      title,
		NodeType:   nodeType,
		Content:    content,
		Keywords:   keywords,
		Importance: 0.8,
	}
}

func TestL3IndexBuild(t *testing.T) {
	eng := tempEngine(t)
	graphID := uint64(42)

	nodes := []*core.HypergraphNode{
		makeTestNode(1, graphID, "Go Language", "concept", "A programming language", []string{"go", "golang"}),
		makeTestNode(2, graphID, "Rust Language", "concept", "Systems language", []string{"rust", "systems"}),
		makeTestNode(3, graphID, "HTTP Server", "function", "Handles HTTP requests", []string{"http", "server"}),
	}
	for _, n := range nodes {
		writeTestNode(eng, n)
	}

	idx := NewL3Index()
	if err := idx.BuildFromEngine(eng); err != nil {
		t.Fatal(err)
	}

	if idx.Len() != 3 {
		t.Fatalf("expected 3 indexed nodes, got %d", idx.Len())
	}

	// Search by keyword
	goResults := idx.SearchByKeyword("go", 10)
	if len(goResults) != 1 || goResults[0] != 1 {
		t.Fatalf("keyword 'go' search: %v", goResults)
	}

	// Search by type
	concepts := idx.SearchByType("concept", graphID, 10)
	if len(concepts) != 2 {
		t.Fatalf("type 'concept' search: expected 2, got %d", len(concepts))
	}
}

func TestL3IndexAddRemove(t *testing.T) {
	idx := NewL3Index()

	node := makeTestNode(100, 1, "Test", "concept", "test content", []string{"test"})
	idx.AddNode(node)
	if idx.Len() != 1 {
		t.Fatalf("len after add: %d", idx.Len())
	}

	results := idx.SearchByKeyword("test", 10)
	if len(results) != 1 {
		t.Fatalf("keyword search after add: %v", results)
	}

	idx.RemoveNode(100)
	if idx.Len() != 0 {
		t.Fatalf("len after remove: %d", idx.Len())
	}

	results = idx.SearchByKeyword("test", 10)
	if len(results) != 0 {
		t.Fatalf("keyword search after remove: %v", results)
	}
}

func TestL3IndexReAddSameHash(t *testing.T) {
	idx := NewL3Index()
	h := uint64(7)

	// First add with the old keyword set.
	idx.AddNode(makeTestNode(h, 1, "Old", "concept", "old content", []string{"oldkw"}))
	if got := idx.SearchByKeyword("oldkw", 10); len(got) != 1 || got[0] != h {
		t.Fatalf("old keyword search after first add: %v", got)
	}

	// Re-add the same hash with a different keyword set.
	idx.AddNode(makeTestNode(h, 1, "New", "concept", "new content", []string{"newkw"}))
	if got := idx.SearchByKeyword("oldkw", 10); len(got) != 0 {
		t.Fatalf("stale keyword search after re-add: %v", got)
	}
	if got := idx.SearchByKeyword("newkw", 10); len(got) != 1 || got[0] != h {
		t.Fatalf("new keyword search after re-add: %v", got)
	}

	// RemoveNode must leave no residue.
	idx.RemoveNode(h)
	if got := idx.SearchByKeyword("newkw", 10); len(got) != 0 {
		t.Fatalf("keyword search after remove: %v", got)
	}
	if idx.Len() != 0 {
		t.Fatalf("len after remove: %d", idx.Len())
	}
}

func TestL3IndexTypeFilterByGraph(t *testing.T) {
	idx := NewL3Index()

	idx.AddNode(makeTestNode(1, 10, "A", "concept", "", []string{"a"}))
	idx.AddNode(makeTestNode(2, 10, "B", "function", "", []string{"b"}))
	idx.AddNode(makeTestNode(3, 20, "C", "concept", "", []string{"c"}))

	// Filter by type only (graphID=0)
	results := idx.SearchByType("concept", 0, 10)
	if len(results) != 2 {
		t.Fatalf("expected 2 concepts across all graphs, got %d: %v", len(results), results)
	}

	// Filter by type + graph
	results = idx.SearchByType("concept", 10, 10)
	if len(results) != 1 || results[0] != 1 {
		t.Fatalf("expected 1 concept in graph 10, got %v", results)
	}
}

// --- test helpers ---

func writeTestNode(eng *core.StorageEngine, node *core.HypergraphNode) {
	data, _ := json.Marshal(node)
	eng.WriteRecord(core.RecL3GraphNode, node.IDHash, data)
}
