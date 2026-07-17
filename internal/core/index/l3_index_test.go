// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Tests for index types migrated from l3 package.

package index

import (
	"path/filepath"
	"testing"

	"memhop/internal/core/model"
	"memhop/internal/core/storage"
)

func tempEngine(t *testing.T) *storage.StorageEngine {
	t.Helper()
	p := filepath.Join(t.TempDir(), "test.meh")
	eng, err := storage.Create(p, 128)
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	return eng
}

func makeTestNode(id uint64, graphID uint64, title, nodeType, content string, keywords []string) *model.HypergraphNode {
	return &model.HypergraphNode{
		IDHash:     id,
		GraphID:    graphID,
		Title:      title,
		NodeType:   nodeType,
		Content:    content,
		Keywords:   keywords,
		Importance: 0.8,
	}
}

func TestAdjacencyCache(t *testing.T) {
	cache := NewAdjacencyCache(3)

	// Miss
	if _, ok := cache.Get(1); ok {
		t.Fatal("expected miss")
	}

	// Put and get
	adj := make(map[uint64][]model.AdjacencyEntry)
	adj[1] = []model.AdjacencyEntry{{NodeHash: 1, EdgeHash: 1}}
	cache.Put(1, adj)

	got, ok := cache.Get(1)
	if !ok {
		t.Fatal("expected hit")
	}
	if len(got[1]) != 1 || got[1][0].NodeHash != 1 {
		t.Fatal("wrong adjacency data")
	}

	// LRU eviction
	cache.Put(2, make(map[uint64][]model.AdjacencyEntry))
	cache.Put(3, make(map[uint64][]model.AdjacencyEntry))
	cache.Put(4, make(map[uint64][]model.AdjacencyEntry)) // should evict key 1

	if _, ok := cache.Get(1); ok {
		t.Fatal("expected eviction of key 1")
	}

	// Invalidate
	cache.Invalidate(2)
	if _, ok := cache.Get(2); ok {
		t.Fatal("expected invalidate to remove key 2")
	}

	// InvalidateAll
	cache.InvalidateAll()
	if cache.Len() != 0 {
		t.Fatal("expected empty after InvalidateAll")
	}
}

func TestDegreeTracker(t *testing.T) {
	eng := tempEngine(t)
	graphID := uint64(1)
	buildTestGraph(t, eng, graphID)

	dt := NewDegreeTracker()

	// Cold start: mark dirty, rebuild
	dt.MarkDirty(graphID)
	if !dt.IsDirty(graphID) {
		t.Fatal("expected dirty")
	}

	if err := dt.Rebuild(eng, graphID); err != nil {
		t.Fatal(err)
	}
	if dt.IsDirty(graphID) {
		t.Fatal("expected clean after rebuild")
	}

	// OnNodeAdded
	dt.OnNodeAdded(graphID, 99)
	if dt.GetDegree(graphID, 99) != 0 {
		t.Fatal("new node should have degree 0")
	}

	// Increment/DecrementNode — use node 99 which has degree 0
	dt.IncrementNode(graphID, 99)
	if dt.GetDegree(graphID, 99) != 1 {
		t.Fatalf("expected degree 1, got %d", dt.GetDegree(graphID, 99))
	}
	dt.DecrementNode(graphID, 99)
	if dt.GetDegree(graphID, 99) != 0 {
		t.Fatalf("expected degree 0, got %d", dt.GetDegree(graphID, 99))
	}
	// Saturate at 0
	dt.DecrementNode(graphID, 99)
	if dt.GetDegree(graphID, 99) != 0 {
		t.Fatal("degree should not go below 0")
	}

	// FindIsolatedNodes: node 3 has no edges
	iso := dt.FindIsolatedNodes(graphID)
	if len(iso) == 0 {
		t.Fatal("expected at least one isolated node")
	}

	// FindLowDegreeNodes
	low := dt.FindLowDegreeNodes(graphID, 0)
	if len(low) == 0 {
		t.Fatal("expected at least one low-degree node")
	}

	// ClearGraph
	dt.ClearGraph(graphID)
	if dt.GetDegree(graphID, 1) != 0 {
		t.Fatal("expected cleared")
	}
}

func TestL3IndexBuild(t *testing.T) {
	eng := tempEngine(t)
	graphID := uint64(42)

	nodes := []*model.HypergraphNode{
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

	// Search by graph
	allInGraph := idx.SearchByGraph(graphID)
	if len(allInGraph) != 3 {
		t.Fatalf("graph search: expected 3, got %d", len(allInGraph))
	}

	// Get node info
	info := idx.GetNodeInfo(1)
	if info == nil || info.Title != "Go Language" {
		t.Fatalf("node info: %v", info)
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

func TestL3IndexBM25(t *testing.T) {
	idx := NewL3Index()

	idx.AddNode(makeTestNode(1, 1, "Go Programming", "concept",
		"Go is a statically typed compiled programming language designed at Google",
		[]string{"go", "golang"}))
	idx.AddNode(makeTestNode(2, 1, "Rust Programming", "concept",
		"Rust is a multi-paradigm systems programming language focused on safety",
		[]string{"rust", "systems"}))

	results := idx.BM25Search([]string{"programming", "language"}, 5)
	if len(results) == 0 {
		t.Fatal("expected BM25 results")
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

func writeTestNode(eng *storage.StorageEngine, node *model.HypergraphNode) {
	data, _ := node.MarshalJSON()
	eng.WriteRecord(storage.RecL3GraphNode, node.IDHash, data)
}

func buildTestGraph(t *testing.T, eng *storage.StorageEngine, graphID uint64) {
	t.Helper()
	nodes := []*model.HypergraphNode{
		makeTestNode(1, graphID, "alpha", "entity", "alpha content", []string{"alpha"}),
		makeTestNode(2, graphID, "beta", "entity", "beta content", []string{"beta"}),
		makeTestNode(3, graphID, "gamma", "entity", "gamma content", []string{"gamma"}),
	}
	for _, n := range nodes {
		writeTestNode(eng, n)
	}
	// Edge: alpha -- beta
	edge1 := model.HypergraphEdge{
		IDHash:  100,
		GraphID: graphID,
		NodeIDs: []uint64{1, 2},
		Kind:    model.EdgeRelated,
	}
	e1data, _ := edge1.MarshalJSON()
	eng.WriteRecord(storage.RecL3GraphEdge, 100, e1data)
	// Edge: beta -- gamma
	edge2 := model.HypergraphEdge{
		IDHash:  101,
		GraphID: graphID,
		NodeIDs: []uint64{2, 3},
		Kind:    model.EdgeRelated,
	}
	e2data, _ := edge2.MarshalJSON()
	eng.WriteRecord(storage.RecL3GraphEdge, 101, e2data)
}
