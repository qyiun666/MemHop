// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

func newL3TestDB(t *testing.T) *DB {
	t.Helper()
	engine, err := core.Create(filepath.Join(t.TempDir(), "l3.meh"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := engine.Close(); err != nil {
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
	graph, err := db.getL3Graph(common.FormatHash(graphs[0].IDHash))
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

// A graph's UpdatedAt is its change clock. It moves for each kind of write a
// batch can land on the graph — a node created, a node restated, an edge added —
// and stays put for a batch that only read it. Each case rewinds the stored stamp
// rather than sleeping, because the clock is millisecond-resolution and two
// imports can easily fall inside one.
func TestImportL3StampsGraphClock(t *testing.T) {
	db := newL3TestDB(t)
	rewind := func(t *testing.T, hexID string, to int64) {
		t.Helper()
		graphID, err := common.ParseID(hexID)
		if err != nil {
			t.Fatalf("parse graph id: %v", err)
		}
		slot, err := repo.ReadSharedGraphL3(db.engine, graphID)
		if err != nil {
			t.Fatalf("read graph: %v", err)
		}
		slot.UpdatedAt = to
		if err := core.WriteGraphSlot(db.engine, core.SharedPoolAgentID, slot.IDHash, slot); err != nil {
			t.Fatalf("rewind graph clock: %v", err)
		}
	}
	stamp := func(t *testing.T, hexID string) int64 {
		t.Helper()
		graphID, err := common.ParseID(hexID)
		if err != nil {
			t.Fatalf("parse graph id: %v", err)
		}
		slot, err := repo.ReadSharedGraphL3(db.engine, graphID)
		if err != nil {
			t.Fatalf("read graph: %v", err)
		}
		return slot.UpdatedAt
	}

	res, err := db.ImportL3(core.DefaultAgentID, []L3ImportItem{
		{Title: "clock-a", Domain: "go", Content: "v1"},
		{Title: "clock-b", Domain: "go", Content: "v1"},
	}, L3ImportSkip)
	if err != nil {
		t.Fatal(err)
	}
	graphID := res.GraphIDs[0]

	// Nothing new here: the same two nodes, skipped, and no relation declared.
	rewind(t, graphID, 1000)
	if _, err := db.ImportL3(core.DefaultAgentID, []L3ImportItem{
		{Title: "clock-a", Domain: "go", Content: "v2"},
		{Title: "clock-b", Domain: "go", Content: "v2"},
	}, L3ImportSkip); err != nil {
		t.Fatal(err)
	}
	if got := stamp(t, graphID); got != 1000 {
		t.Fatalf("a batch that wrote nothing moved the clock to %d", got)
	}

	// A node created into the existing graph.
	rewind(t, graphID, 2000)
	if _, err := db.ImportL3(core.DefaultAgentID, []L3ImportItem{
		{Title: "clock-c", Domain: "go", Content: "v1"},
	}, L3ImportSkip); err != nil {
		t.Fatal(err)
	}
	if got := stamp(t, graphID); got <= 2000 {
		t.Fatalf("creating a node left the clock at %d", got)
	}

	// A node restated, with the graph's slot otherwise untouched.
	rewind(t, graphID, 3000)
	if _, err := db.ImportL3(core.DefaultAgentID, []L3ImportItem{
		{Title: "clock-c", Domain: "go", Content: "v2"},
	}, L3ImportOverwrite); err != nil {
		t.Fatal(err)
	}
	if got := stamp(t, graphID); got <= 3000 {
		t.Fatalf("restating a node left the clock at %d", got)
	}

	// An edge over nodes the graph already holds — no node write at all.
	rewind(t, graphID, 4000)
	if _, err := db.ImportL3(core.DefaultAgentID, []L3ImportItem{{
		Title: "clock-c", Domain: "go", Content: "v2",
		Related: []L3Relation{{Kind: core.EdgeRelated, Titles: []string{"clock-a"}}},
	}}, L3ImportSkip); err != nil {
		t.Fatal(err)
	}
	if got := stamp(t, graphID); got <= 4000 {
		t.Fatalf("adding a hyperedge left the clock at %d", got)
	}
}

func TestImportL3RejectsUnknownMode(t *testing.T) {
	db := newL3TestDB(t)
	_, err := db.ImportL3(core.DefaultAgentID, []L3ImportItem{{Title: "x", Domain: "d"}}, L3ImportMode("bogus"))
	if err == nil || common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("expected ErrInvalidQuery, got %v", err)
	}
	// A host follows the words inside a refusal, so they have to be the words the type
	// accepts — and the value it was given has to be there too, or three candidates are
	// a guess instead of an answer.
	for _, name := range []string{string(L3ImportSkip), string(L3ImportMerge), string(L3ImportOverwrite)} {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("the refusal does not name the mode %q: %v", name, err)
		}
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("the refusal does not say what it was handed: %v", err)
	}
	got, err := core.CollectAllGraphSlots(db.engine, core.SharedPoolAgentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("no graph should be created for invalid mode: %+v", got)
	}
}

func TestImportL3SourceRef(t *testing.T) {
	db := newL3TestDB(t)
	items := []L3ImportItem{{
		Title: "main.go", Domain: "proj", NodeType: "file",
		Content: "entrypoint", SourceRef: "internal/l0.go:22",
	}}
	if _, err := db.ImportL3(core.DefaultAgentID, items, L3ImportOverwrite); err != nil {
		t.Fatal(err)
	}
	graph := l3TestGraph(t, db)
	if got := graph.Nodes[0].SourceRef; got == nil || *got != "internal/l0.go:22" {
		t.Fatalf("source ref: %v", got)
	}
}

// TestImportL3Relations: Related entries become graph hyperedges regardless
// of item order (a relation may target a later item), and re-importing the
// same batch does not duplicate edges (deterministic edge ids).
func TestImportL3Relations(t *testing.T) {
	db := newL3TestDB(t)
	items := []L3ImportItem{
		{Title: "main.go", Domain: "proj", NodeType: "file", Content: "m",
			Related: []L3Relation{{Titles: []string{"later.go"}, Kind: GraphEdgeKind(EdgeDependency)}}},
		{Title: "later.go", Domain: "proj", NodeType: "file", Content: "l"},
	}
	res, err := db.ImportL3(core.DefaultAgentID, items, L3ImportOverwrite)
	if err != nil {
		t.Fatal(err)
	}
	if res.EdgesCreated != 1 {
		t.Fatalf("edges created: %+v", res)
	}
	graph := l3TestGraph(t, db)
	if len(graph.Edges) != 1 {
		t.Fatalf("edges: %+v", graph.Edges)
	}
	if e := graph.Edges[0]; e.Kind != EdgeDependency || len(e.NodeIDs) != 2 {
		t.Fatalf("edge: kind=%v nodes=%v", e.Kind, e.NodeIDs)
	}

	if _, err := db.ImportL3(core.DefaultAgentID, items, L3ImportOverwrite); err != nil {
		t.Fatal(err)
	}
	if graph := l3TestGraph(t, db); len(graph.Edges) != 1 {
		t.Fatalf("re-import duplicated edges: %+v", graph.Edges)
	}
}

// TestImportL3RelationErrors: unresolvable, self-referencing and invalid-kind
// relations are reported per entry while the node itself still imports.
func TestImportL3RelationErrors(t *testing.T) {
	db := newL3TestDB(t)
	items := []L3ImportItem{{
		Title: "a", Domain: "p", Content: "a",
		Related: []L3Relation{
			{Titles: []string{"ghost"}},
			{Titles: []string{"a"}, Kind: GraphEdgeKind(EdgeRelated)},
			{Titles: []string{"a"}, Kind: GraphEdgeKind(99)},
		},
	}}
	res, err := db.ImportL3(core.DefaultAgentID, items, L3ImportSkip)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 3 {
		t.Fatalf("errors: %+v", res.Errors)
	}
	if res.EdgesCreated != 0 {
		t.Fatalf("edges created: %+v", res)
	}
	graph := l3TestGraph(t, db)
	if len(graph.Edges) != 0 {
		t.Fatalf("edges: %+v", graph.Edges)
	}
	if len(graph.Nodes) != 1 {
		t.Fatalf("node should still import: %+v", graph.Nodes)
	}
}

// A node pair can carry several kinds of relation at once — "a related to b"
// and "a part of b" are two facts. An edge id built from the pair alone lets
// the second write overwrite the first, and the pair ends up with whichever
// kind landed last.
func TestImportL3KeepsDistinctEdgeKindsOnOnePair(t *testing.T) {
	db := newL3TestDB(t)
	items := []L3ImportItem{
		{Title: "a", Domain: "p", Content: "a", Related: []L3Relation{
			{Titles: []string{"b"}, Kind: GraphEdgeKind(EdgeRelated)},
			{Titles: []string{"b"}, Kind: GraphEdgeKind(EdgePartOf)},
		}},
		{Title: "b", Domain: "p", Content: "b"},
	}
	res, err := db.ImportL3(core.DefaultAgentID, items, L3ImportOverwrite)
	if err != nil {
		t.Fatal(err)
	}
	if res.EdgesCreated != 2 || len(res.Errors) != 0 {
		t.Fatalf("import result: %+v", res)
	}
	graph := l3TestGraph(t, db)
	if len(graph.Edges) != 2 {
		t.Fatalf("edges: %+v", graph.Edges)
	}
	kinds := map[GraphEdgeKind]bool{}
	for _, e := range graph.Edges {
		kinds[e.Kind] = true
	}
	if !kinds[GraphEdgeKind(EdgeRelated)] || !kinds[GraphEdgeKind(EdgePartOf)] {
		t.Fatalf("kinds lost: %+v", graph.Edges)
	}

	// Same batch again: deterministic ids plus the semantic dedupe keep the
	// graph at two edges.
	if _, err := db.ImportL3(core.DefaultAgentID, items, L3ImportOverwrite); err != nil {
		t.Fatal(err)
	}
	if graph := l3TestGraph(t, db); len(graph.Edges) != 2 {
		t.Fatalf("re-import duplicated edges: %+v", graph.Edges)
	}
}

// Edges written before the kind joined the edge id keep their pair-only hash.
// A re-import must recognise them by (graph, sorted pair, kind) instead of
// landing a second edge that says the same thing.
func TestImportL3DedupesPairHashedLegacyEdge(t *testing.T) {
	db := newL3TestDB(t)
	items := []L3ImportItem{
		{Title: "a", Domain: "p", Content: "a", Related: []L3Relation{{Titles: []string{"b"}, Kind: GraphEdgeKind(EdgeCausal)}}},
		{Title: "b", Domain: "p", Content: "b"},
	}
	if _, err := db.ImportL3(core.DefaultAgentID, items, L3ImportOverwrite); err != nil {
		t.Fatal(err)
	}
	graph := l3TestGraph(t, db)
	if len(graph.Nodes) != 2 || len(graph.Edges) != 1 {
		t.Fatalf("baseline graph: %+v", graph)
	}
	ids := []uint64{graph.Nodes[0].IDHash, graph.Nodes[1].IDHash}
	slices.Sort(ids)
	legacyID := common.HashID(fmt.Sprintf("%s:%v", common.FormatHash(graph.Edges[0].GraphID), ids))
	legacy := core.HypergraphEdge{
		IDHash: legacyID, GraphID: graph.Edges[0].GraphID,
		Kind: graph.Edges[0].Kind, NodeIDs: ids, CreatedAt: graph.Edges[0].CreatedAt,
	}
	if _, err := db.engine.DeleteRecordBatch(core.SharedPoolAgentID, []uint64{graph.Edges[0].IDHash}); err != nil {
		t.Fatal(err)
	}
	if err := core.WriteHypergraphEdge(db.engine, core.SharedPoolAgentID, legacyID, &legacy); err != nil {
		t.Fatal(err)
	}

	if _, err := db.ImportL3(core.DefaultAgentID, items, L3ImportOverwrite); err != nil {
		t.Fatal(err)
	}
	if graph := l3TestGraph(t, db); len(graph.Edges) != 1 {
		t.Fatalf("legacy edge duplicated: %+v", graph.Edges)
	}
}

// An id names one record: pointing UpdateL3/DeleteL3 at a node must report
// "graph not found", not rename the node's record into a graph slot.
func TestL3GraphWritesRejectNodeID(t *testing.T) {
	db := newL3TestDB(t)
	items := []L3ImportItem{{Title: "a", Domain: "p", Content: "a"}}
	if _, err := db.ImportL3(core.DefaultAgentID, items, L3ImportOverwrite); err != nil {
		t.Fatal(err)
	}
	graph := l3TestGraph(t, db)
	nodeID := common.FormatHash(graph.Nodes[0].IDHash)

	name := "hijacked"
	if _, err := db.UpdateL3(core.DefaultAgentID, nodeID, &name); common.CodeOf(err) != common.ErrNotFound {
		t.Fatalf("UpdateL3 over a node id: %v", err)
	}
	if err := db.DeleteL3(core.DefaultAgentID, nodeID); common.CodeOf(err) != common.ErrNotFound {
		t.Fatalf("DeleteL3 over a node id: %v", err)
	}
	if graph := l3TestGraph(t, db); graph.Nodes[0].Title != "a" || len(graph.Nodes) != 1 {
		t.Fatalf("node record was modified: %+v", graph)
	}
}

// TestImportL3NaryHyperedge verifies one relation naming several targets lands
// as a single edge over the whole member set — the fact the storage layer is
// shaped for: the edge id hashes the member set, and a BFS from any one member
// reaches every other over that single edge rather than over a fan of pairs.
func TestImportL3NaryHyperedge(t *testing.T) {
	db := newL3TestDB(t)
	items := []L3ImportItem{
		{Title: "module:auth", Domain: "proj", Content: "the whole",
			Related: []L3Relation{{Titles: []string{"login.go", "token.go", "session.go"}, Kind: GraphEdgeKind(EdgePartOf)}}},
		{Title: "login.go", Domain: "proj", Content: "l"},
		{Title: "token.go", Domain: "proj", Content: "t"},
		{Title: "session.go", Domain: "proj", Content: "s"},
	}
	res, err := db.ImportL3(core.DefaultAgentID, items, L3ImportOverwrite)
	if err != nil {
		t.Fatal(err)
	}
	if res.EdgesCreated != 1 || len(res.Errors) != 0 {
		t.Fatalf("want one edge and no errors, got %d edges %+v", res.EdgesCreated, res.Errors)
	}
	g, err := db.GetL3(core.DefaultAgentID, res.GraphIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Edges) != 1 || len(g.Edges[0].NodeIDs) != 4 {
		t.Fatalf("want 1 edge over 4 members, got %d edges: %+v", len(g.Edges), g.Edges)
	}
	if g.Edges[0].Kind != GraphEdgeKind(EdgePartOf) {
		t.Fatalf("kind lost: %d", g.Edges[0].Kind)
	}

	// BFS one hop from any member reaches the other three over that single edge.
	start := nodeIDOf(g, "token.go")
	sub, err := db.QueryL3Subgraph(core.DefaultAgentID, res.GraphIDs[0], common.FormatHash(start), 1, nil)
	if err != nil {
		t.Fatalf("subgraph: %v", err)
	}
	if len(sub.Nodes) != 4 || len(sub.Edges) != 1 {
		t.Fatalf("one hop over the hyperedge should reach 4 nodes, got %d nodes / %d edges", len(sub.Nodes), len(sub.Edges))
	}

	// Re-importing the same batch keeps it one edge.
	if _, err := db.ImportL3(core.DefaultAgentID, items, L3ImportSkip); err != nil {
		t.Fatal(err)
	}
	again, err := db.GetL3(core.DefaultAgentID, res.GraphIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Edges) != 1 {
		t.Fatalf("re-import duplicated the hyperedge: %d", len(again.Edges))
	}
}

// TestImportL3RelationMemberErrors verifies the arities and member sets a
// relation may not name are each reported rather than quietly dropped.
func TestImportL3RelationMemberErrors(t *testing.T) {
	db := newL3TestDB(t)
	items := []L3ImportItem{{
		Title: "a", Domain: "p", Content: "a",
		Related: []L3Relation{
			{Titles: []string{"b"}},                          // b does not exist in the graph
			{Titles: nil},                                    // names no far side at all
			{Titles: []string{""}},                           // empty target
			{Titles: []string{"a"}},                          // self-referencing
			{Titles: []string{"c", "c"}},                     // duplicate member
			{Titles: []string{"c"}, Kind: GraphEdgeKind(99)}, // kind outside the vocabulary
		},
	}, {Title: "c", Domain: "p", Content: "c"}}
	res, err := db.ImportL3(core.DefaultAgentID, items, L3ImportOverwrite)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 6 {
		t.Fatalf("want 6 reported relation errors, got %d: %+v", len(res.Errors), res.Errors)
	}
	if res.EdgesCreated != 0 {
		t.Fatalf("a malformed member set must create no edge, got %d", res.EdgesCreated)
	}
	// a valid n-ary relation among them still lands
	res2, err := db.ImportL3(core.DefaultAgentID, []L3ImportItem{{
		Title: "a", Domain: "p", Content: "a",
		Related: []L3Relation{{Titles: []string{"c"}, Kind: GraphEdgeKind(EdgeRelated)}},
	}, {Title: "c", Domain: "p", Content: "c"}}, L3ImportOverwrite)
	if err != nil {
		t.Fatal(err)
	}
	if res2.EdgesCreated != 1 || len(res2.Errors) != 0 {
		t.Fatalf("valid relation rejected: %+v %+v", res2.EdgesCreated, res2.Errors)
	}
}

func nodeIDOf(g *L3Graph, title string) uint64 {
	for _, n := range g.Nodes {
		if n.Title == title {
			return n.IDHash
		}
	}
	return 0
}

// ---- graph identity and the L3 -> L2 anchor direction ----

// importOne seeds a one-node graph under domain and returns its id hash.
func importOne(t *testing.T, db *DB, domain, title string) uint64 {
	t.Helper()
	res, err := db.ImportL3(core.DefaultAgentID, []L3ImportItem{
		{Title: title, Domain: domain, Content: title},
	}, L3ImportSkip)
	if err != nil {
		t.Fatalf("import %s: %v", domain, err)
	}
	if len(res.GraphIDs) != 1 {
		t.Fatalf("domain %s wrote %d graphs, want 1", domain, len(res.GraphIDs))
	}
	id, err := common.ParseID(res.GraphIDs[0])
	if err != nil {
		t.Fatalf("graph id %q: %v", res.GraphIDs[0], err)
	}
	return id
}

// mustAnchor writes a scene record anchored to l3ID (0 = unanchored).
func mustAnchor(t *testing.T, engine *core.StorageEngine, sceneID, l3ID uint64) {
	t.Helper()
	slot := core.NewSceneSlot(sceneID, fmt.Sprintf("session:%d", sceneID))
	slot.L3ID = l3ID
	if err := core.WriteSceneSlot(engine, core.DefaultAgentID, sceneID, &slot); err != nil {
		t.Fatalf("write scene %d: %v", sceneID, err)
	}
}

// TestUpdateL3RejectsNameCollision pins the invariant the import router relies
// on: a domain label addresses exactly one graph. A rename onto a taken label
// would leave two slots with the same Name, and the batch cache that maps
// name -> id then resolves the domain to whichever slot the record scan happens
// to visit last.
func TestUpdateL3RejectsNameCollision(t *testing.T) {
	db := newL3TestDB(t)
	alpha := importOne(t, db, "alpha", "a1")
	importOne(t, db, "beta", "b1")

	taken := "beta"
	if _, err := db.UpdateL3(core.DefaultAgentID, common.FormatHash(alpha), &taken); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("rename onto a taken label: code=%d err=%v", common.CodeOf(err), err)
	}
	// The graph must be untouched by the refused rename.
	g, err := db.GetL3(core.DefaultAgentID, common.FormatHash(alpha))
	if err != nil {
		t.Fatalf("get alpha: %v", err)
	}
	if g.Slot.Name != "alpha" {
		t.Fatalf("refused rename changed the name to %q", g.Slot.Name)
	}
	// Renaming to the name it already has is a no-op patch, not a collision.
	self := "alpha"
	if _, err := db.UpdateL3(core.DefaultAgentID, common.FormatHash(alpha), &self); err != nil {
		t.Fatalf("rename onto own name: %v", err)
	}
	// A free label still renames.
	free := "gamma"
	if _, err := db.UpdateL3(core.DefaultAgentID, common.FormatHash(alpha), &free); err != nil {
		t.Fatalf("rename onto a free label: %v", err)
	}
	// The label it now carries is free again, so an empty one is refused: a graph
	// with no label is one ImportL3 can never address again.
	blank := ""
	if _, err := db.UpdateL3(core.DefaultAgentID, common.FormatHash(alpha), &blank); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("an empty label must be refused, code=%d err=%v", common.CodeOf(err), err)
	}
	if _, err := db.UpdateL3(core.DefaultAgentID, common.FormatHash(alpha), nil); err != nil {
		t.Fatalf("a nil name is the no-change spelling: %v", err)
	}
	after, err := db.GetL3(core.DefaultAgentID, common.FormatHash(alpha))
	if err != nil || after.Slot.Name != "gamma" {
		t.Fatalf("the refused rename left its mark: name=%q err=%v", after.Slot.Name, err)
	}
}

// TestImportL3NameCollisionRoutesByDerivation keeps the read path total for a
// file that already carries two slots under one label (written before the
// rename check existed): the domain resolves to the graph its id derives from,
// deterministically, instead of to whichever slot the record scan visits last.
func TestImportL3NameCollisionRoutesByDerivation(t *testing.T) {
	db := newL3TestDB(t)
	alpha := importOne(t, db, "alpha", "a1")
	beta := importOne(t, db, "beta", "b1")

	// Force the duplicate label through the record layer.
	taken := "beta"
	if _, err := repo.UpdateGraphL3(db.engine, core.SharedPoolAgentID, alpha, &taken); err != nil {
		t.Fatalf("forced rename: %v", err)
	}
	res, err := db.ImportL3(core.DefaultAgentID, []L3ImportItem{
		{Title: "b2", Domain: "beta", Content: "b2"},
	}, L3ImportSkip)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(res.GraphIDs) != 1 || res.GraphIDs[0] != common.FormatHash(beta) {
		t.Fatalf("domain %q routed to %v, want %s", "beta", res.GraphIDs, common.FormatHash(beta))
	}
	g, err := db.GetL3(core.DefaultAgentID, common.FormatHash(beta))
	if err != nil {
		t.Fatalf("get beta: %v", err)
	}
	if len(g.Nodes) != 2 {
		t.Fatalf("beta graph holds %d nodes, want 2 (b1, b2)", len(g.Nodes))
	}
	shadowed, err := db.GetL3(core.DefaultAgentID, common.FormatHash(alpha))
	if err != nil {
		t.Fatalf("get shadowed graph: %v", err)
	}
	for _, n := range shadowed.Nodes {
		if n.Title == "b2" {
			t.Fatalf("b2 landed in the shadowing graph %s instead of %s",
				common.FormatHash(alpha), common.FormatHash(beta))
		}
	}
}

// A graph slot the pool cannot decode is not a label the pool has free. The
// import batch seeded its label → graph map from a scan that stepped over what it
// could not read, so importing that slot's label answered "no such graph" and
// wrote a second slot under the same name — the domain's nodes then live under two
// ids, and the graph the host named first keeps what it held, unreachable by
// label. The refusal also names the record, because nothing else in the engine can.
func TestImportL3RefusesUnreadableGraphSlot(t *testing.T) {
	db := newL3TestDB(t)
	alpha := importOne(t, db, "alpha", "a1")

	// Move alpha's label so its slot no longer answers to hash("alpha"): resolving
	// "beta" now runs through that record.
	taken := "beta"
	if _, err := repo.UpdateGraphL3(db.engine, core.SharedPoolAgentID, alpha, &taken); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if _, err := db.engine.WriteRecord(core.SharedPoolAgentID, core.RecL3GraphSlot, alpha,
		[]byte(`{"na`)); err != nil {
		t.Fatalf("damage the slot: %v", err)
	}

	_, err := db.ImportL3(core.DefaultAgentID, []L3ImportItem{
		{Title: "b1", Domain: "beta", Content: "b1"},
	}, L3ImportMerge)
	if common.CodeOf(err) != common.ErrDeserialization {
		t.Fatalf("importing a label held by an unreadable slot: code=%d err=%v", common.CodeOf(err), err)
	}
	if !strings.Contains(err.Error(), common.FormatHash(alpha)) {
		t.Fatalf("the refusal must name the record it could not read: %v", err)
	}
	if n := countRecords(db.engine, core.SharedPoolAgentID, core.RecL3GraphSlot); n != 1 {
		t.Fatalf("the refusal left %d graph slots, want the one it refused to read around", n)
	}
}

// A node the pool cannot decode is not a title the graph is missing. The batch
// loaded its per-graph title set from a listing that stepped over unreadable
// records, so a Merge import answered "create it" — which lands on the same
// positional id and rewrites the very record it could not read, while the report
// counted that under CreatedIDs as something new. The membership index is built
// once up front now, so the whole batch refuses and nothing is written.
func TestImportL3RefusesUnreadableNode(t *testing.T) {
	db := newL3TestDB(t)
	graph := importOne(t, db, "alpha", "a1")
	nodeID := repo.NodeIDL3(graph, "a1")
	if _, err := db.engine.WriteRecord(core.SharedPoolAgentID, core.RecL3GraphNode, nodeID,
		[]byte(`{"ti`)); err != nil {
		t.Fatalf("damage the node: %v", err)
	}

	res, err := db.ImportL3(core.DefaultAgentID, []L3ImportItem{
		{Title: "a2", Domain: "alpha", Content: "a2"},
	}, L3ImportMerge)
	if common.CodeOf(err) != common.ErrDeserialization {
		t.Fatalf("import over an unreadable node: code=%d err=%v", common.CodeOf(err), err)
	}
	if res != nil {
		t.Fatalf("a refused batch reports no result, got %+v", res)
	}
	if !strings.Contains(err.Error(), common.FormatHash(nodeID)) {
		t.Fatalf("the refusal must name the record it could not read: %v", err)
	}
	if n := countRecords(db.engine, core.SharedPoolAgentID, core.RecL3GraphNode); n != 1 {
		t.Fatalf("the graph holds %d node records after the refusal, want the one it refused to read", n)
	}
}

// TestDeleteL3ClearsSceneAnchors keeps the anchor invariant whole in both
// directions: writing an anchor is refused when the graph does not exist, so
// deleting the graph has to drop the anchors that named it — otherwise
// ListScenes(l3ID) lists sessions under a project domain nothing can resolve.
func TestDeleteL3ClearsSceneAnchors(t *testing.T) {
	db := newL3TestDB(t)
	gone := importOne(t, db, "proj", "p1")
	kept := importOne(t, db, "other", "o1")

	mustAnchor(t, db.engine, 11, gone)
	mustAnchor(t, db.engine, 12, gone)
	mustAnchor(t, db.engine, 13, kept)
	mustAnchor(t, db.engine, 14, 0)

	if err := db.DeleteL3(core.DefaultAgentID, common.FormatHash(gone)); err != nil {
		t.Fatalf("DeleteL3: %v", err)
	}
	if scenes, err := db.ListScenes(core.DefaultAgentID, common.FormatHash(gone)); err != nil || len(scenes) != 0 {
		t.Fatalf("scenes still anchored to the deleted graph: %d err=%v", len(scenes), err)
	}
	all, err := db.ListScenes(core.DefaultAgentID, "")
	if err != nil {
		t.Fatalf("list scenes: %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("want 4 scenes, got %d", len(all))
	}
	for _, s := range all {
		switch s.SceneID {
		case 11, 12:
			if s.L3ID != 0 {
				t.Errorf("scene %d still anchored to %s", s.SceneID, common.FormatHash(s.L3ID))
			}
		case 13:
			if s.L3ID != kept {
				t.Errorf("scene 13 anchor damaged: %d want %d", s.L3ID, kept)
			}
		}
	}
	// The other graph and its nodes survive untouched.
	if g, err := db.GetL3(core.DefaultAgentID, common.FormatHash(kept)); err != nil || len(g.Nodes) != 1 {
		t.Fatalf("sibling graph damaged: nodes=%d err=%v", len(g.Nodes), err)
	}
}

// Every L3 read is assembled from a hash-map scan of the shared pool, so without
// a sort one host would see the same graph in a different order on each call —
// and a capped node query would fall on an arbitrary subset of it.
func TestL3ReadsAreOrderStable(t *testing.T) {
	db := newL3TestDB(t)
	items := make([]L3ImportItem, 0, 6)
	for i := range 6 {
		items = append(items, L3ImportItem{Title: fmt.Sprintf("n%d", i), Domain: "order"})
	}
	items[0].Related = []L3Relation{{Titles: []string{"n1", "n2"}}}
	items[1].Related = []L3Relation{{Titles: []string{"n3"}}}
	items[2].Related = []L3Relation{{Titles: []string{"n4"}, Kind: core.EdgePartOf}}
	res, err := db.ImportL3(core.DefaultAgentID, items, L3ImportSkip)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(res.Errors) > 0 {
		t.Fatalf("import errors: %v", res.Errors)
	}
	graphID := res.GraphIDs[0]

	listNodeIDs := func(limit int) []uint64 {
		got, err := db.QueryL3Nodes(core.DefaultAgentID, L3NodeQuery{GraphID: graphID, Limit: limit})
		if err != nil {
			t.Fatalf("QueryL3Nodes: %v", err)
		}
		ids := make([]uint64, 0, len(got))
		for _, n := range got {
			ids = append(ids, n.IDHash)
		}
		return ids
	}
	full := listNodeIDs(0)
	if len(full) != 6 {
		t.Fatalf("want 6 nodes, got %d", len(full))
	}
	if !slices.IsSorted(full) {
		t.Errorf("QueryL3Nodes is not sorted by id: %v", full)
	}
	for i := 0; i < 4; i++ {
		if again := listNodeIDs(0); !slices.Equal(full, again) {
			t.Fatalf("repeat %d answered in a different order: %v vs %v", i, full, again)
		}
	}
	if capped := listNodeIDs(3); !slices.Equal(capped, full[:3]) {
		t.Errorf("Limit=3 is not the first 3 of the sorted listing: %v vs %v", capped, full[:3])
	}

	graph, err := db.GetL3(core.DefaultAgentID, graphID)
	if err != nil {
		t.Fatalf("GetL3: %v", err)
	}
	nodeIDs := make([]uint64, 0, len(graph.Nodes))
	for _, n := range graph.Nodes {
		nodeIDs = append(nodeIDs, n.IDHash)
	}
	if !slices.IsSorted(nodeIDs) {
		t.Errorf("GetL3 nodes are not sorted by id: %v", nodeIDs)
	}
	edgeIDs := make([]uint64, 0, len(graph.Edges))
	for _, e := range graph.Edges {
		edgeIDs = append(edgeIDs, e.IDHash)
	}
	if len(edgeIDs) == 0 || !slices.IsSorted(edgeIDs) {
		t.Errorf("GetL3 edges are not sorted by id: %v", edgeIDs)
	}

	for _, domain := range []string{"alpha", "beta"} {
		if _, err := db.ImportL3(core.DefaultAgentID, []L3ImportItem{
			{Title: "seed", Domain: domain},
		}, L3ImportSkip); err != nil {
			t.Fatalf("import %s: %v", domain, err)
		}
	}
	slots, err := db.ListL3(core.DefaultAgentID)
	if err != nil {
		t.Fatalf("ListL3: %v", err)
	}
	if len(slots) != 3 {
		t.Fatalf("want 3 graphs, got %d", len(slots))
	}
	for i := 1; i < len(slots); i++ {
		if slots[i-1].IDHash >= slots[i].IDHash {
			t.Fatalf("ListL3 is not sorted by id: %v", slots)
		}
	}
}

// The node listing tolerates a record that will not decode; the subgraph read
// cannot, because its node set comes from the members the edges name — a member
// it reaches but cannot read is the pool disagreeing with itself, and answering
// with a smaller graph would hide that.
func TestQueryL3SubgraphReportsUnreadableNode(t *testing.T) {
	db := newL3TestDB(t)
	res, err := db.ImportL3(core.DefaultAgentID, []L3ImportItem{
		{Title: "a", Domain: "g", Related: []L3Relation{{Titles: []string{"b"}}}},
		{Title: "b", Domain: "g"},
	}, L3ImportSkip)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	graphHash, err := common.ParseID(res.GraphIDs[0])
	if err != nil {
		t.Fatalf("graph id: %v", err)
	}
	const corruptTitle = "b"
	corruptID := repo.NodeIDL3(graphHash, corruptTitle)
	if _, err := db.engine.WriteRecord(core.SharedPoolAgentID, core.RecL3GraphNode,
		corruptID, []byte(`{"id":`)); err != nil {
		t.Fatalf("replace node %s with an undecodable payload: %v", corruptTitle, err)
	}

	_, err = db.QueryL3Subgraph(core.DefaultAgentID, res.GraphIDs[0],
		common.FormatHash(repo.NodeIDL3(graphHash, "a")), 1, nil)
	if common.CodeOf(err) != common.ErrDeserialization {
		t.Fatalf("want ErrDeserialization for an unreadable member, got %v", err)
	}
}

// The start node is an id a previous read handed the host, so "no such node" and
// "the node will not read back" are two different answers: the first sends the host
// to another node, the second tells it this graph is damaged where it stands.
// Answering the first for both would let a damaged graph read as an empty one and
// be re-imported over.
func TestQueryL3SubgraphReportsUnreadableStartNode(t *testing.T) {
	db := newL3TestDB(t)
	res, err := db.ImportL3(core.DefaultAgentID, []L3ImportItem{
		{Title: "a", Domain: "g", Related: []L3Relation{{Titles: []string{"b"}}}},
		{Title: "b", Domain: "g"},
	}, L3ImportSkip)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	graphHash, err := common.ParseID(res.GraphIDs[0])
	if err != nil {
		t.Fatalf("graph id: %v", err)
	}
	startID := repo.NodeIDL3(graphHash, "a")
	if _, err := db.engine.WriteRecord(core.SharedPoolAgentID, core.RecL3GraphNode,
		startID, []byte(`{"id":`)); err != nil {
		t.Fatalf("make the start node unreadable: %v", err)
	}

	if _, err := db.QueryL3Subgraph(core.DefaultAgentID, res.GraphIDs[0],
		common.FormatHash(startID), 1, nil); common.CodeOf(err) != common.ErrDeserialization {
		t.Fatalf("an unreadable start node must not be answered as a missing one, got %v", err)
	}
}

// A graph's cascade is built by enumerating the whole node and edge buckets and
// keeping the members, so a member that will not read back has to stop the delete:
// the survivors would keep naming a graph the host was told is gone, and a re-import
// under the same name would adopt them as its own.
func TestDeleteL3RefusesUnreadableNode(t *testing.T) {
	db := newL3TestDB(t)
	res, err := db.ImportL3(core.DefaultAgentID, []L3ImportItem{
		{Title: "a", Domain: "g", Related: []L3Relation{{Titles: []string{"b"}}}},
		{Title: "b", Domain: "g"},
	}, L3ImportSkip)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	graphHash, err := common.ParseID(res.GraphIDs[0])
	if err != nil {
		t.Fatalf("graph id: %v", err)
	}
	survivor := repo.NodeIDL3(graphHash, "a")
	if _, err := db.engine.WriteRecord(core.SharedPoolAgentID, core.RecL3GraphNode,
		repo.NodeIDL3(graphHash, "b"), []byte(`{"id":`)); err != nil {
		t.Fatalf("make one node unreadable: %v", err)
	}

	if err := db.DeleteL3(core.DefaultAgentID, res.GraphIDs[0]); common.CodeOf(err) != common.ErrDeserialization {
		t.Fatalf("the cascade must refuse on a member it could not enumerate, got %v", err)
	}
	if _, err := core.ReadHypergraphNode(db.engine, core.SharedPoolAgentID, survivor); err != nil {
		t.Fatalf("a refused cascade deletes no member: %v", err)
	}
	graphID, err := common.ParseID(res.GraphIDs[0])
	if err != nil {
		t.Fatalf("parse graph id: %v", err)
	}
	if _, err := repo.ReadSharedGraphL3(db.engine, graphID); err != nil {
		t.Fatalf("nor the graph slot itself: %v", err)
	}
}

// Every L3 id derives from text the host supplies and the whole pool shares one id
// space, so a domain written as "<graph hex>:<title>" hashes to exactly the address of
// that title's node in that graph. The graph read the create path performs answers
// such a collision with "no such graph", and taking that as permission stores a graph
// slot over somebody's node — which then answers to neither kind: the node listing
// stops naming it, and the record reports a type it never was.
func TestImportL3RefusesADomainNamingANodeAddress(t *testing.T) {
	db := newL3TestDB(t)
	graphHash := importOne(t, db, "go", "escape-analysis")
	nodeAddress := common.FormatHash(graphHash) + ":escape-analysis"
	if common.HashID(nodeAddress) != repo.NodeIDL3(graphHash, "escape-analysis") {
		t.Fatalf("the fixture no longer names a node address: %x", nodeAddress)
	}

	res, err := db.ImportL3(core.DefaultAgentID, []L3ImportItem{
		{Title: "other", Domain: nodeAddress, Content: "clobber"},
	}, L3ImportSkip)
	if err != nil {
		t.Fatalf("the batch itself is not the refusal: %v", err)
	}
	if len(res.Errors) != 1 || !strings.Contains(res.Errors[0], "already holds this address") {
		t.Fatalf("the colliding domain must be refused by name, got %+v", res.Errors)
	}
	graph := l3TestGraph(t, db)
	if len(graph.Nodes) != 1 || graph.Nodes[0].Title != "escape-analysis" ||
		graph.Nodes[0].Content != "escape-analysis" {
		t.Fatalf("the refused import rewrote the node: %+v", graph.Nodes)
	}
	if n := countRecords(db.engine, core.SharedPoolAgentID, core.RecL3GraphSlot); n != 1 {
		t.Fatalf("the pool holds %d graph slots after a refused create, want 1", n)
	}
}

// The L3 read faces answer with the whole pool or with the record they could not
// read. A listing one entry short is not a smaller graph: to a host it is a claim
// that the pool never held that node, and a missing edge is a claim that two nodes
// are unrelated, because the subgraph walk can only report what the adjacency
// relates.
func TestL3ReadsRefuseARecordTheyCannotDecode(t *testing.T) {
	db := newL3TestDB(t)
	res, err := db.ImportL3(core.DefaultAgentID, []L3ImportItem{
		{Title: "a", Domain: "d", NodeType: "concept",
			Related: []L3Relation{{Kind: core.EdgeRelated, Titles: []string{"b"}}}},
		{Title: "b", Domain: "d", NodeType: "concept"},
	}, L3ImportSkip)
	if err != nil {
		t.Fatal(err)
	}
	graphHex := res.GraphIDs[0]
	graphHash, err := common.ParseID(graphHex)
	if err != nil {
		t.Fatal(err)
	}
	nodeA := common.FormatHash(repo.NodeIDL3(graphHash, "a"))
	nodeB := repo.NodeIDL3(graphHash, "b")
	edges, err := repo.ListEdgeL3(db.engine, core.SharedPoolAgentID, graphHash)
	if err != nil || len(edges) != 1 {
		t.Fatalf("want the one imported edge, got %v err %v", edges, err)
	}
	undecodable := []byte(`{"id":`)
	wantCode := common.ErrDeserialization

	// A damaged node: the graph view and the node query both read the node set, and
	// neither may answer one node short.
	if _, err := db.engine.WriteRecord(core.SharedPoolAgentID, core.RecL3GraphNode, nodeB, undecodable); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetL3(core.DefaultAgentID, graphHex); common.CodeOf(err) != wantCode {
		t.Errorf("GetL3 over a damaged node = %v, want the read's own code", err)
	}
	if _, err := db.QueryL3Nodes(core.DefaultAgentID, L3NodeQuery{GraphID: graphHex}); common.CodeOf(err) != wantCode {
		t.Errorf("QueryL3Nodes over a damaged node = %v, want the read's own code", err)
	}

	// Put the node back and damage the edge instead: the adjacency decides
	// reachability, so the subgraph must refuse rather than report the two nodes as
	// unrelated, and the graph view must not hand back a graph without its edge.
	if err := core.WriteHypergraphNode(db.engine, core.SharedPoolAgentID, nodeB,
		&core.HypergraphNode{IDHash: nodeB, GraphID: graphHash, Title: "b", NodeType: "concept"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.engine.WriteRecord(core.SharedPoolAgentID, core.RecL3GraphEdge, edges[0].IDHash, undecodable); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetL3(core.DefaultAgentID, graphHex); common.CodeOf(err) != wantCode {
		t.Errorf("GetL3 over a damaged edge = %v, want the read's own code", err)
	}
	if _, err := db.QueryL3Subgraph(core.DefaultAgentID, graphHex, nodeA, 1, nil); common.CodeOf(err) != wantCode {
		t.Errorf("QueryL3Subgraph over a damaged edge = %v, want the read's own code", err)
	}

	// A damaged graph slot: the pool listing is what a host resolves its scene
	// anchors against, so it must refuse rather than answer that the pool is empty.
	if _, err := db.engine.WriteRecord(core.SharedPoolAgentID, core.RecL3GraphSlot, graphHash, undecodable); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ListL3(core.DefaultAgentID); common.CodeOf(err) != wantCode {
		t.Errorf("ListL3 over a damaged slot = %v, want the read's own code", err)
	}
}
