// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package l3

import (
	"path/filepath"
	"testing"

	"github.com/qyiun666/memhop/memhop/internal/core/model"
	"github.com/qyiun666/memhop/memhop/internal/core/storage"
	"github.com/qyiun666/memhop/memhop/internal/hash"
)

// --- test helpers ---

func tempEngine(t *testing.T) *storage.StorageEngine {
	t.Helper()
	p := filepath.Join(t.TempDir(), "test.meh")
	eng, err := storage.Create(p, 768)
	if err != nil {
		t.Fatal(err)
	}
	return eng
}

func makeNode(idHash, graphID uint64, title string) *model.HypergraphNode {
	return &model.HypergraphNode{
		IDHash:  idHash,
		GraphID: graphID,
		Title:   title,
		Content: "content-" + title,
	}
}

func makeEdge(idHash, graphID uint64, kind model.GraphEdgeKind, nodeIDs []uint64) *model.HypergraphEdge {
	return &model.HypergraphEdge{
		IDHash:  idHash,
		GraphID: graphID,
		Kind:    kind,
		NodeIDs: nodeIDs,
		Weight:  1.0,
	}
}

// buildTestGraph creates a 5-node graph with mixed edge types:
//
//	n101 --Related-- n102
//	n102 --Related-- n103
//	n103 --Causal--- n104
//	n101 --PartOf--- [n103, n105]  (hyperedge)
func buildTestGraph(t *testing.T, eng *storage.StorageEngine, graphID uint64) {
	t.Helper()
	for _, n := range []uint64{101, 102, 103, 104, 105} {
		if err := AddNode(eng, makeNode(n, graphID, "node")); err != nil {
			t.Fatal(err)
		}
	}
	edges := []*model.HypergraphEdge{
		makeEdge(201, graphID, model.EdgeRelated, []uint64{101, 102}),
		makeEdge(202, graphID, model.EdgeRelated, []uint64{102, 103}),
		makeEdge(203, graphID, model.EdgeCausal, []uint64{103, 104}),
		makeEdge(204, graphID, model.EdgePartOf, []uint64{101, 103, 105}),
	}
	for _, e := range edges {
		if err := AddEdge(eng, e); err != nil {
			t.Fatal(err)
		}
	}
}

// --- Tests ---

func TestCreateGraph(t *testing.T) {
	eng := tempEngine(t)
	src := model.HypergraphSource{Kind: model.SourceManual}
	slot, err := CreateGraph(eng, "test-graph", src)
	if err != nil {
		t.Fatal(err)
	}
	if slot.Name != "test-graph" {
		t.Fatalf("name: %q", slot.Name)
	}
	if slot.IDHash == 0 {
		t.Fatal("id_hash should be nonzero")
	}

	// Read back
	got, err := GetGraphSlot(eng, slot.IDHash)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "test-graph" {
		t.Fatalf("readback name: %q", got.Name)
	}

	// Add node
	node := makeNode(101, slot.IDHash, "hello")
	if err := AddNode(eng, node); err != nil {
		t.Fatal(err)
	}
	readNode, err := GetNode(eng, 101)
	if err != nil {
		t.Fatal(err)
	}
	if readNode.Title != "hello" {
		t.Fatalf("node title: %q", readNode.Title)
	}

	// Add edge
	edge := makeEdge(201, slot.IDHash, model.EdgeRelated, []uint64{101, 102})
	if err := AddEdge(eng, edge); err != nil {
		t.Fatal(err)
	}
	readEdge, err := GetEdge(eng, 201)
	if err != nil {
		t.Fatal(err)
	}
	if len(readEdge.NodeIDs) != 2 {
		t.Fatalf("edge node count: %d", len(readEdge.NodeIDs))
	}

	// List
	nodes, _ := ListNodes(eng, slot.IDHash)
	if len(nodes) != 1 {
		t.Fatalf("list nodes: %d", len(nodes))
	}
	edges, _ := ListEdges(eng, slot.IDHash)
	if len(edges) != 1 {
		t.Fatalf("list edges: %d", len(edges))
	}
}

func TestBFS(t *testing.T) {
	eng := tempEngine(t)
	graphID := uint64(1)
	buildTestGraph(t, eng, graphID)

	layers := BFSFromEngine(eng, graphID, 101, 2, nil)

	// depth 1: nodes reachable from 101 via edges 201 and 204
	// edge 201: 101->102, edge 204: 101->103,105
	// expected depth-1 nodes: {102, 103, 105}
	if len(layers) < 1 {
		t.Fatal("expected at least 1 layer")
	}
	d1 := toSet(layers[0])
	if !d1[102] || !d1[103] || !d1[105] {
		t.Fatalf("depth-1 missing expected nodes: %v", layers[0])
	}

	// depth 2: from 103, edge 203 reaches 104
	if len(layers) < 2 {
		t.Fatal("expected 2 layers")
	}
	d2 := toSet(layers[1])
	if !d2[104] {
		t.Fatalf("depth-2 missing node 104: %v", layers[1])
	}
}

func TestBFSEdgeKindFilter(t *testing.T) {
	eng := tempEngine(t)
	graphID := uint64(1)
	buildTestGraph(t, eng, graphID)

	// Only Related edges: 201 (101-102), 202 (102-103)
	layers := BFSFromEngine(eng, graphID, 101, 3, []model.GraphEdgeKind{model.EdgeRelated})
	// depth 1: 102 (via edge 201)
	// depth 2: 103 (via edge 202)
	allVisited := make(map[uint64]bool)
	allVisited[101] = true
	for _, layer := range layers {
		for _, n := range layer {
			allVisited[n] = true
		}
	}
	if !allVisited[102] || !allVisited[103] {
		t.Fatalf("expected 102 and 103 via Related edges, got: %v", allVisited)
	}
	// 104 and 105 should NOT be reachable (Causal and PartOf filtered out)
	if allVisited[104] || allVisited[105] {
		t.Fatalf("104/105 should not be reachable via Related only: %v", allVisited)
	}
}

func TestExtractSubgraph(t *testing.T) {
	eng := tempEngine(t)
	graphID := uint64(1)
	buildTestGraph(t, eng, graphID)

	// BFS from 101, depth 1 → nodes {102, 103, 105}
	layers := BFSFromEngine(eng, graphID, 101, 1, nil)
	visited := map[uint64]bool{101: true}
	for _, layer := range layers {
		for _, n := range layer {
			visited[n] = true
		}
	}

	adj := BuildAdjacencyIndex(eng, graphID)
	sub, err := ExtractSubgraph(eng, adj, visited)
	if err != nil {
		t.Fatal(err)
	}
	if len(sub.Nodes) != 4 { // 101, 102, 103, 105
		t.Fatalf("expected 4 nodes, got %d", len(sub.Nodes))
	}
	// Edges where at least 2 endpoints are in visited:
	// 201 (101,102) ✓, 204 (101,103,105) ✓
	// 202 (102,103) ✓ — both 102 and 103 are visited
	// 203 (103,104) — only 103 visited → ✗
	if len(sub.Edges) < 2 {
		t.Fatalf("expected at least 2 edges, got %d", len(sub.Edges))
	}
}

func TestBFSWithAdjacency(t *testing.T) {
	eng := tempEngine(t)
	graphID := uint64(1)
	buildTestGraph(t, eng, graphID)

	adj := BuildAdjacencyIndex(eng, graphID)
	layers := BFSWithAdjacency(adj, 101, 2, nil)

	if len(layers) < 1 {
		t.Fatal("expected at least 1 layer")
	}
	d1 := toSet(layers[0])
	if !d1[102] || !d1[103] || !d1[105] {
		t.Fatalf("depth-1 missing expected nodes: %v", layers[0])
	}
	if len(layers) >= 2 {
		d2 := toSet(layers[1])
		if !d2[104] {
			t.Fatalf("depth-2 missing node 104: %v", layers[1])
		}
	}
}

func TestBFSWithAdjacencyFilter(t *testing.T) {
	eng := tempEngine(t)
	graphID := uint64(1)
	buildTestGraph(t, eng, graphID)

	adj := BuildAdjacencyIndex(eng, graphID)
	// Only Related edges
	layers := BFSWithAdjacency(adj, 101, 3, []model.GraphEdgeKind{model.EdgeRelated})
	all := make(map[uint64]bool)
	all[101] = true
	for _, l := range layers {
		for _, n := range l {
			all[n] = true
		}
	}
	if !all[102] || !all[103] {
		t.Fatalf("expected 102,103 via Related: %v", all)
	}
	if all[104] || all[105] {
		t.Fatalf("104/105 should not be reachable: %v", all)
	}
}

func TestAdjacencyCache(t *testing.T) {
	cache := NewAdjacencyCache(3)

	// Miss
	if _, ok := cache.Get(1); ok {
		t.Fatal("expected miss")
	}

	// Put + Get
	adj1 := map[uint64][]AdjacencyEntry{100: {{NodeHash: 100, EdgeHash: 200}}}
	cache.Put(1, adj1)
	got, ok := cache.Get(1)
	if !ok || got[100][0].EdgeHash != 200 {
		t.Fatal("cache miss after put")
	}

	// Eviction: fill to max(3) then add one more.
	// After Get(1), order is [1]. Then Put(2)→[1,2], Put(3)→[1,2,3].
	// Put(4) evicts front(1), order becomes [2,3,4].
	cache.Put(2, adj1)
	cache.Put(3, adj1)
	cache.Put(4, adj1)
	if _, ok := cache.Get(1); ok {
		t.Fatal("expected graph 1 evicted (was oldest after Get moved it)")
	}
	if _, ok := cache.Get(3); !ok {
		t.Fatal("graph 3 should still be cached")
	}

	// Invalidate graph 3 (1 was already evicted)
	cache.Invalidate(3)
	if _, ok := cache.Get(3); ok {
		t.Fatal("expected invalidation")
	}

	// Len: only 2 and 4 remain
	if cache.Len() != 2 {
		t.Fatalf("len: %d", cache.Len())
	}

	// InvalidateAll
	cache.InvalidateAll()
	if cache.Len() != 0 {
		t.Fatalf("len after clear: %d", cache.Len())
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
	dt.Rebuild(eng, graphID)
	if dt.IsDirty(graphID) {
		t.Fatal("should be clean after rebuild")
	}

	// Check degrees: 101 is in edges 201, 204 → degree 2
	// 102 in 201, 202 → degree 2
	// 103 in 202, 203, 204 → degree 3
	// 104 in 203 → degree 1
	// 105 in 204 → degree 1
	if d := dt.GetDegree(graphID, 101); d != 2 {
		t.Fatalf("101 degree: %d", d)
	}
	if d := dt.GetDegree(graphID, 103); d != 3 {
		t.Fatalf("103 degree: %d", d)
	}

	// Add an isolated node
	dt.OnNodeAdded(graphID, 999)
	if d := dt.GetDegree(graphID, 999); d != 0 {
		t.Fatalf("999 degree: %d", d)
	}
	isolated := dt.FindIsolatedNodes(graphID)
	if !contains(isolated, 999) {
		t.Fatal("999 should be isolated")
	}

	// Incremental: add edge referencing 999
	dt.OnEdgeAdded(graphID, []uint64{999, 101})
	if d := dt.GetDegree(graphID, 999); d != 1 {
		t.Fatalf("999 degree after edge: %d", d)
	}
	isolated = dt.FindIsolatedNodes(graphID)
	if contains(isolated, 999) {
		t.Fatal("999 should no longer be isolated")
	}

	// Delete edge → decrement
	dt.OnEdgeDeleted(graphID, []uint64{999, 101})
	if d := dt.GetDegree(graphID, 999); d != 0 {
		t.Fatalf("999 degree after delete: %d", d)
	}

	// Clear + InvalidateAll
	dt.InvalidateAll()
	if dt.GetDegree(graphID, 101) != 0 {
		t.Fatal("degrees should be cleared")
	}
}

func TestDeleteGraph(t *testing.T) {
	eng := tempEngine(t)

	// Create slot — use its hash as graphID
	src := model.HypergraphSource{Kind: model.SourceManual}
	slot, err := CreateGraph(eng, "del-test", src)
	if err != nil {
		t.Fatal(err)
	}
	graphID := slot.IDHash

	buildTestGraph(t, eng, graphID)

	// Verify pre-delete
	nodes, _ := ListNodes(eng, graphID)
	if len(nodes) != 5 {
		t.Fatalf("pre-delete nodes: %d", len(nodes))
	}

	// Delete
	if err := DeleteGraph(eng, graphID); err != nil {
		t.Fatal(err)
	}

	// Verify slot gone
	if eng.Contains(graphID) {
		t.Fatal("slot should be deleted")
	}

	// Verify members gone
	nodes, _ = ListNodes(eng, graphID)
	if len(nodes) != 0 {
		t.Fatalf("post-delete nodes: %d", len(nodes))
	}
	edges, _ := ListEdges(eng, graphID)
	if len(edges) != 0 {
		t.Fatalf("post-delete edges: %d", len(edges))
	}
}

func TestDeleteNodeCascade(t *testing.T) {
	eng := tempEngine(t)
	graphID := uint64(1)
	buildTestGraph(t, eng, graphID)

	// Delete node 101 → should cascade-delete edges 201 and 204
	if err := DeleteNode(eng, 101); err != nil {
		t.Fatal(err)
	}

	// Node 101 gone
	if _, err := GetNode(eng, 101); err == nil {
		t.Fatal("node 101 should be deleted")
	}

	// Edges 201, 204 should be gone
	edges, _ := ListEdges(eng, graphID)
	for _, e := range edges {
		if e.IDHash == 201 || e.IDHash == 204 {
			t.Fatalf("edge %d should have been cascade-deleted", e.IDHash)
		}
	}
	// Remaining: 202 (102-103), 203 (103-104)
	if len(edges) != 2 {
		t.Fatalf("remaining edges: %d", len(edges))
	}
}

func TestImportEntities(t *testing.T) {
	eng := tempEngine(t)
	graphID := uint64(42)

	hints := []EntityHint{
		{Title: "Go Language", NodeType: "concept", Content: "A programming language", Keywords: []string{"go", "golang"}},
		{Title: "Rust Language", NodeType: "concept", Content: "Systems language", Keywords: []string{"rust"}},
	}

	hashes, err := ImportEntities(eng, graphID, hints, 99)
	if err != nil {
		t.Fatal(err)
	}
	if len(hashes) != 2 {
		t.Fatalf("imported: %d", len(hashes))
	}

	// Verify nodes exist
	for _, h := range hashes {
		node, err := GetNode(eng, h)
		if err != nil {
			t.Fatal(err)
		}
		if node.GraphID != graphID {
			t.Fatalf("graph_id mismatch: %d", node.GraphID)
		}
	}

	// Verify hash determinism
	expected := hash.HashID("Go Language")
	if hashes[0] != expected {
		t.Fatalf("hash mismatch: got %d, want %d", hashes[0], expected)
	}
}

func TestNodeContentTruncation(t *testing.T) {
	eng := tempEngine(t)
	long := make([]byte, 300)
	for i := range long {
		long[i] = 'x'
	}
	node := makeNode(101, 1, "trunc")
	node.Content = string(long)

	if err := AddNode(eng, node); err != nil {
		t.Fatal(err)
	}
	got, err := GetNode(eng, 101)
	if err != nil {
		t.Fatal(err)
	}
	if len([]rune(got.Content)) != maxNodeContentLen {
		t.Fatalf("content len: %d, want %d", len([]rune(got.Content)), maxNodeContentLen)
	}
}

// --- helpers ---

func toSet(ids []uint64) map[uint64]bool {
	s := make(map[uint64]bool, len(ids))
	for _, id := range ids {
		s[id] = true
	}
	return s
}

func contains(ids []uint64, v uint64) bool {
	for _, id := range ids {
		if id == v {
			return true
		}
	}
	return false
}

// --- Community Detection Tests ---

func TestReduceHyperedges(t *testing.T) {
	// 3-node hyperedge → 3 binary edges (clique)
	edges := []*model.HypergraphEdge{
		makeEdge(1, 100, model.EdgeRelated, []uint64{10, 20, 30}),
	}
	binary := ReduceHyperedges(edges, 10)
	if len(binary) != 3 {
		t.Fatalf("expected 3 binary edges, got %d", len(binary))
	}
}

func TestReduceHyperedgesSkipLarge(t *testing.T) {
	nodes := make([]uint64, 15)
	for i := range nodes {
		nodes[i] = uint64(i)
	}
	edges := []*model.HypergraphEdge{
		makeEdge(1, 100, model.EdgeRelated, nodes),
	}
	binary := ReduceHyperedges(edges, 10)
	if len(binary) != 0 {
		t.Fatalf("expected 0 binary edges for oversized hyperedge, got %d", len(binary))
	}
}

func TestReduceHyperedgesDedupWeights(t *testing.T) {
	edges := []*model.HypergraphEdge{
		makeEdge(1, 100, model.EdgeRelated, []uint64{10, 20}),
		makeEdge(2, 100, model.EdgeRelated, []uint64{10, 20}),
	}
	binary := ReduceHyperedges(edges, 10)
	if len(binary) != 1 {
		t.Fatalf("expected 1 deduped edge, got %d", len(binary))
	}
	// weight = 1.0/(2-1) + 1.0/(2-1) = 2.0
	if diff := binary[0].Weight - 2.0; diff > 1e-6 || diff < -1e-6 {
		t.Fatalf("expected weight 2.0, got %f", binary[0].Weight)
	}
}

func TestCommunityDetection(t *testing.T) {
	eng := tempEngine(t)
	graphID := uint64(1)

	// Two clusters: {101,102,103} connected, {201,202,203} connected
	// Bridge edge: 103-201
	for _, n := range []uint64{101, 102, 103, 201, 202, 203} {
		if err := AddNode(eng, makeNode(n, graphID, "cnode")); err != nil {
			t.Fatal(err)
		}
	}
	edges := []*model.HypergraphEdge{
		makeEdge(301, graphID, model.EdgeRelated, []uint64{101, 102}),
		makeEdge(302, graphID, model.EdgeRelated, []uint64{102, 103}),
		makeEdge(303, graphID, model.EdgeRelated, []uint64{101, 103}),
		makeEdge(304, graphID, model.EdgeRelated, []uint64{201, 202}),
		makeEdge(305, graphID, model.EdgeRelated, []uint64{202, 203}),
		makeEdge(306, graphID, model.EdgeRelated, []uint64{201, 203}),
		makeEdge(307, graphID, model.EdgeRelated, []uint64{103, 201}), // bridge
	}
	for _, e := range edges {
		if err := AddEdge(eng, e); err != nil {
			t.Fatal(err)
		}
	}

	cfg := DefaultCommunityConfig()
	result, err := DetectCommunities(eng, graphID, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalNodes != 6 {
		t.Fatalf("expected 6 nodes, got %d", result.TotalNodes)
	}
	if result.TotalCommunities < 1 {
		t.Fatal("expected at least 1 community")
	}
	// With the bridge, Louvain should find 2 communities (or at least 1)
	if result.TotalCommunities > 3 {
		t.Fatalf("too many communities: %d", result.TotalCommunities)
	}
}

func TestCommunityDetectionEmptyGraph(t *testing.T) {
	eng := tempEngine(t)
	cfg := DefaultCommunityConfig()
	result, err := DetectCommunities(eng, 99999, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalNodes != 0 {
		t.Fatalf("expected 0 nodes, got %d", result.TotalNodes)
	}
}

// --- L3 Index Tests ---

func makeIndexedNode(idHash, graphID uint64, title, nodeType, content string, keywords []string) *model.HypergraphNode {
	return &model.HypergraphNode{
		IDHash:   idHash,
		GraphID:  graphID,
		Title:    title,
		NodeType: nodeType,
		Content:  content,
		Keywords: keywords,
	}
}

func TestL3IndexBuild(t *testing.T) {
	eng := tempEngine(t)
	graphID := uint64(42)

	nodes := []*model.HypergraphNode{
		makeIndexedNode(1, graphID, "Go Language", "concept", "A programming language", []string{"go", "golang"}),
		makeIndexedNode(2, graphID, "Rust Language", "concept", "Systems language", []string{"rust", "systems"}),
		makeIndexedNode(3, graphID, "HTTP Server", "function", "Handles HTTP requests", []string{"http", "server"}),
	}
	for _, n := range nodes {
		if err := AddNode(eng, n); err != nil {
			t.Fatal(err)
		}
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

	node := makeIndexedNode(100, 1, "Test", "concept", "test content", []string{"test"})
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

	idx.AddNode(makeIndexedNode(1, 1, "Go Programming", "concept",
		"Go is a statically typed compiled programming language designed at Google",
		[]string{"go", "golang"}))
	idx.AddNode(makeIndexedNode(2, 1, "Rust Programming", "concept",
		"Rust is a multi-paradigm systems programming language focused on safety",
		[]string{"rust"}))
	idx.AddNode(makeIndexedNode(3, 1, "Python Scripting", "concept",
		"Python is a high-level general-purpose programming language",
		[]string{"python"}))

	// Search for "programming language" should match all three
	results := idx.BM25Search([]string{"programming", "language"}, 10)
	if len(results) < 2 {
		t.Fatalf("BM25 search: expected at least 2 results, got %d", len(results))
	}

	// Results should be sorted by score descending
	for i := 1; i < len(results); i++ {
		if results[i].Score > results[i-1].Score {
			t.Fatal("BM25 results not sorted by score descending")
		}
	}
}

func TestL3IndexTypeFilterByGraph(t *testing.T) {
	idx := NewL3Index()

	idx.AddNode(makeIndexedNode(1, 10, "A", "concept", "", []string{"a"}))
	idx.AddNode(makeIndexedNode(2, 10, "B", "function", "", []string{"b"}))
	idx.AddNode(makeIndexedNode(3, 20, "C", "concept", "", []string{"c"}))

	// type=concept, graphID=10 → only node 1
	results := idx.SearchByType("concept", 10, 10)
	if len(results) != 1 || results[0] != 1 {
		t.Fatalf("type+graph filter: %v", results)
	}

	// type=concept, graphID=0 (no graph filter) → nodes 1 and 3
	results = idx.SearchByType("concept", 0, 10)
	if len(results) != 2 {
		t.Fatalf("type no-graph filter: expected 2, got %d", len(results))
	}
}
