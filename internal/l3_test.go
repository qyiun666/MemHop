// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"fmt"
	"path/filepath"
	"slices"
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

// l3TestGraphByName reads the graph a domain name imported into, for the cases
// where the test domain holds more than one graph.
func l3TestGraphByName(t *testing.T, db *DB, name string) *L3Graph {
	t.Helper()
	graphs, err := db.ListL3(core.DefaultAgentID)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range graphs {
		if g.Name == name {
			graph, err := db.getL3Graph(core.DefaultAgentID, common.FormatHash(g.IDHash))
			if err != nil {
				t.Fatal(err)
			}
			return graph
		}
	}
	t.Fatalf("no graph named %q", name)
	return nil
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

func TestImportL3SourceRef(t *testing.T) {
	db := newL3TestDB(t)
	items := []L3ImportItem{{
		Title: "main.go", Domain: "proj", NodeType: "file",
		Content: "entrypoint", SourceRef: "cmd/memhop-mcp/main.go:1",
	}}
	if _, err := db.ImportL3(core.DefaultAgentID, items, L3ImportOverwrite); err != nil {
		t.Fatal(err)
	}
	graph := l3TestGraph(t, db)
	if got := graph.Nodes[0].SourceRef; got == nil || *got != "cmd/memhop-mcp/main.go:1" {
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
	if _, err := db.engine.DeleteRecordBatch(core.DefaultAgentID, []uint64{graph.Edges[0].IDHash}); err != nil {
		t.Fatal(err)
	}
	if err := core.WriteHypergraphEdge(db.engine, core.DefaultAgentID, legacyID, &legacy); err != nil {
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

// Correcting one knowledge node means removing it: the cascade has to take the
// hyperedges that touch it with it, since an edge pointing at a deleted node
// resolves to nothing, and leave the rest of the graph alone.
func TestDeleteL3NodesCascadesEdges(t *testing.T) {
	db := newL3TestDB(t)
	items := []L3ImportItem{
		{Title: "a", Domain: "p", Content: "a", Related: []L3Relation{
			{Titles: []string{"b"}, Kind: GraphEdgeKind(EdgePartOf)},
			{Titles: []string{"c"}, Kind: GraphEdgeKind(EdgePartOf)},
		}},
		{Title: "b", Domain: "p", Content: "b"},
		{Title: "c", Domain: "p", Content: "c", Related: []L3Relation{{Titles: []string{"b"}, Kind: GraphEdgeKind(EdgeRelated)}}},
	}
	res, err := db.ImportL3(core.DefaultAgentID, items, L3ImportOverwrite)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.GraphIDs) != 1 {
		t.Fatalf("graph ids: %+v", res)
	}
	graphID := res.GraphIDs[0]
	graph := l3TestGraph(t, db)
	if len(graph.Nodes) != 3 || len(graph.Edges) != 3 {
		t.Fatalf("baseline: %d nodes / %d edges", len(graph.Nodes), len(graph.Edges))
	}

	var bID, cID uint64
	for _, n := range graph.Nodes {
		switch n.Title {
		case "b":
			bID = n.IDHash
		case "c":
			cID = n.IDHash
		}
	}
	if err := db.DeleteL3Nodes(core.DefaultAgentID, graphID, []string{common.FormatHash(bID)}); err != nil {
		t.Fatal(err)
	}
	graph = l3TestGraph(t, db)
	titles := make([]string, 0, len(graph.Nodes))
	for _, n := range graph.Nodes {
		titles = append(titles, n.Title)
	}
	if !slices.Contains(titles, "a") || !slices.Contains(titles, "c") || slices.Contains(titles, "b") {
		t.Fatalf("nodes after delete: %v", titles)
	}
	if len(graph.Edges) != 1 || graph.Edges[0].Kind != GraphEdgeKind(EdgePartOf) {
		t.Fatalf("edges after delete: %+v", graph.Edges)
	}
	if slices.Contains(graph.Edges[0].NodeIDs, bID) {
		t.Fatalf("an edge still points at the deleted node: %+v", graph.Edges[0])
	}

	// A node that is not in this graph — and one that is not a node at all —
	// are both refusals, not silent no-ops.
	if err := db.DeleteL3Nodes(core.DefaultAgentID, graphID, []string{common.FormatHash(cID)}); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteL3Nodes(core.DefaultAgentID, graphID, []string{common.FormatHash(cID)}); common.CodeOf(err) != common.ErrNotFound {
		t.Fatalf("delete a node twice: %v", err)
	}
	other, err := db.ImportL3(core.DefaultAgentID, []L3ImportItem{{Title: "elsewhere", Domain: "q", Content: "x"}}, L3ImportOverwrite)
	if err != nil {
		t.Fatal(err)
	}
	otherGraph := l3TestGraphByName(t, db, "q")
	if err := db.DeleteL3Nodes(core.DefaultAgentID, other.GraphIDs[0], []string{common.FormatHash(otherGraph.Nodes[0].IDHash)}); err != nil {
		t.Fatalf("delete the other graph's node: %v", err)
	}
	if err := db.DeleteL3Nodes(core.DefaultAgentID, graphID, []string{graphID}); common.CodeOf(err) != common.ErrNotFound {
		t.Fatalf("delete a graph id as a node: %v", err)
	}
}

// TestImportL3NaryHyperedge verifies one relation naming several targets lands
// as a single edge over the whole member set — the fact the storage layer was
// shaped for (edge id hashes the member set, BFS connects every member, the
// node cascade matches any member) and which the import surface used to
// dissolve into pairs.
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

	// Deleting one member cascades the whole hyperedge, not just one pair.
	if err := db.DeleteL3Nodes(core.DefaultAgentID, res.GraphIDs[0], []string{common.FormatHash(nodeIDOf(again, "session.go"))}); err != nil {
		t.Fatalf("delete member: %v", err)
	}
	after, err := db.GetL3(core.DefaultAgentID, res.GraphIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Edges) != 0 {
		t.Fatalf("removing a member must cascade the hyperedge, %d edges left", len(after.Edges))
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
	if _, err := repo.UpdateGraphL3(db.engine, core.DefaultAgentID, alpha, &taken); err != nil {
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
