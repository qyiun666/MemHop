// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package graph

import (
	"path/filepath"
	"strings"
	"testing"

	"memhop/internal/core/model"
	"memhop/internal/core/storage"
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

	sub, err := ExtractSubgraph(eng, visited)
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

func TestNodeContentTruncationCJK(t *testing.T) {
	eng := tempEngine(t)
	cases := []struct {
		name    string
		content string
		want    int // expected rune count after AddNode
	}{
		{"cjk_100", strings.Repeat("汉", 100), 100}, // 300 bytes, under limit
		{"cjk_200", strings.Repeat("汉", 200), 200}, // exactly at limit
		{"cjk_201", strings.Repeat("汉", 201), 200}, // 603 bytes, 1 rune over
		{"cjk_300", strings.Repeat("汉", 300), 200}, // well over
		{"ascii_250", strings.Repeat("x", 250), 200},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			idHash := uint64(1000 + i)
			node := makeNode(idHash, 1, tc.name)
			node.Content = tc.content
			if err := AddNode(eng, node); err != nil { // must not panic
				t.Fatal(err)
			}
			got, err := GetNode(eng, idHash)
			if err != nil {
				t.Fatal(err)
			}
			if n := len([]rune(got.Content)); n != tc.want {
				t.Fatalf("content runes: %d, want %d", n, tc.want)
			}
		})
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


