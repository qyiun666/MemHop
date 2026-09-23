// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// A relation names its far side by title, and resolution is scoped to the graph the item's
// own domain resolved into. That scoping is what makes deleting a project domain survivable:
// an edge reaching into another graph would leave the surviving graph's subgraph walk naming
// a record that no longer reads — the one divergence this library answers with a hard ErrIO
// rather than a skipped row. So this pins three things together: a cross-graph relation is
// refused per item instead of silently resolving, the graph that asked keeps no edge for it,
// and after that graph is deleted the file is indistinguishable — across a reopen — from one
// that never imported it.
func TestGraphRelationsStayInsideTheirOwnGraph(t *testing.T) {
	srv := mockLLMServer(t, turnKeywords)
	dir := t.TempDir()

	open := func(name string) *DB {
		t.Helper()
		defaults := DefaultMemHopDefaults
		defaults.SceneDreamTopicThreshold = -1
		db, err := OpenDB(filepath.Join(dir, name),
			LlmConfig{APIURL: srv.URL, APIKey: "test", Model: "mock"}, defaults, primaryProfile("primary"))
		if err != nil {
			t.Fatalf("OpenDB %s: %v", name, err)
		}
		t.Cleanup(func() { _ = db.Close() })
		return db
	}

	shared := []L3ImportItem{
		{Title: "engine", Domain: "memhop", NodeType: "package", Content: "one file",
			Related: []core.L3Relation{{Titles: []string{"storage"}, Kind: core.EdgeDependency}}},
		{Title: "storage", Domain: "memhop", NodeType: "module", Content: "mmap log"},
	}
	// This graph's only relation names a title that lives in the other graph.
	reach := []L3ImportItem{
		{Title: "host", Domain: "meowagent", NodeType: "module", Content: "the caller",
			Related: []core.L3Relation{{Titles: []string{"engine"}, Kind: core.EdgeDependency}}},
	}

	x := open("x.meh")
	resShared, err := x.ImportL3(core.DefaultAgentID, shared, L3ImportOverwrite)
	if err != nil {
		t.Fatalf("import the memhop graph: %v", err)
	}
	memhopGraph := resShared.GraphIDs[0]

	resReach, err := x.ImportL3(core.DefaultAgentID, reach, L3ImportOverwrite)
	if err != nil {
		t.Fatalf("import the reaching graph: %v", err)
	}
	hostGraph := resReach.GraphIDs[0]
	if len(resReach.Errors) == 0 {
		t.Fatalf("a relation naming a title from another graph was accepted: %+v", resReach)
	}
	if !strings.Contains(strings.Join(resReach.Errors, " "), "engine") {
		t.Fatalf("the refusal does not name the member it could not resolve: %v", resReach.Errors)
	}

	// The asking graph keeps one node and no edge: nothing reached across the boundary.
	hostNodes, err := x.QueryL3Nodes(core.DefaultAgentID, L3NodeQuery{GraphID: hostGraph})
	if err != nil || len(hostNodes) != 1 {
		t.Fatalf("the reaching graph has %+v nodes (err %v), want only the node it declared", hostNodes, err)
	}
	hostSub, err := x.QueryL3Subgraph(core.DefaultAgentID, hostGraph, common.FormatHash(hostNodes[0].IDHash), 3, nil)
	if err != nil {
		t.Fatalf("subgraph of the reaching node: %v", err)
	}
	if len(hostSub.Edges) != 0 || len(hostSub.Nodes) != 1 {
		t.Fatalf("the reaching graph kept a cross-graph edge: nodes %d edges %d", len(hostSub.Nodes), len(hostSub.Edges))
	}

	// The stronger case is one batch spanning two domains: the title table is built per
	// batch, so this is where a lookup that ignored the graph could actually find a match.
	cross, err := x.ImportL3(core.DefaultAgentID, []L3ImportItem{
		{Title: "alpha", Domain: "d1", NodeType: "module", Content: "one side",
			Related: []core.L3Relation{{Titles: []string{"beta"}, Kind: core.EdgeDependency}}},
		{Title: "beta", Domain: "d2", NodeType: "module", Content: "the other side"},
	}, L3ImportOverwrite)
	if err != nil {
		t.Fatalf("import the cross-domain batch: %v", err)
	}
	if len(cross.Errors) == 0 {
		t.Fatalf("a batch spanning two graphs resolved a relation across them: %+v", cross)
	}
	d1 := ""
	for _, id := range cross.GraphIDs {
		nodes, err := x.QueryL3Nodes(core.DefaultAgentID, L3NodeQuery{GraphID: id})
		if err != nil {
			t.Fatalf("QueryL3Nodes(%s): %v", id, err)
		}
		if len(nodes) == 1 && nodes[0].Title == "alpha" {
			d1 = id
		}
	}
	if d1 == "" {
		t.Fatalf("could not find the alpha graph among %+v", cross.GraphIDs)
	}
	d1Nodes, err := x.QueryL3Nodes(core.DefaultAgentID, L3NodeQuery{GraphID: d1})
	if err != nil || len(d1Nodes) != 1 {
		t.Fatalf("d1 nodes = %+v err %v", d1Nodes, err)
	}
	d1Sub, err := x.QueryL3Subgraph(core.DefaultAgentID, d1, common.FormatHash(d1Nodes[0].IDHash), 3, nil)
	if err != nil {
		t.Fatalf("subgraph of alpha: %v", err)
	}
	if len(d1Sub.Edges) != 0 {
		t.Fatalf("alpha reached into another graph within one batch: %+v", d1Sub.Edges)
	}
	// Clear everything except the graph the control file also keeps, so the pool comparison
	// below is about one graph's cascade rather than about how a batch orders its ids.
	before, err := x.ListL3(core.DefaultAgentID)
	if err != nil {
		t.Fatalf("ListL3 before the sweep: %v", err)
	}
	for _, g := range before {
		id := common.FormatHash(g.IDHash)
		if id == memhopGraph {
			continue
		}
		if err := x.DeleteL3(core.DefaultAgentID, id); err != nil {
			t.Fatalf("DeleteL3 %s: %v", id, err)
		}
	}

	// The control: a file that never imported the second graph at all.
	y := open("y.meh")
	if _, err := y.ImportL3(core.DefaultAgentID, shared, L3ImportOverwrite); err != nil {
		t.Fatalf("import on the control file: %v", err)
	}

	if err := x.Close(); err != nil {
		t.Fatalf("close x: %v", err)
	}
	if err := y.Close(); err != nil {
		t.Fatalf("close y: %v", err)
	}
	x, y = open("x.meh"), open("y.meh")

	pool := func(db *DB) [3]int {
		t.Helper()
		slots, err := core.CollectAllStrict[core.HypergraphSlot](db.engine, core.SharedPoolAgentID, core.RecL3GraphSlot)
		if err != nil {
			t.Fatalf("collect graph slots: %v", err)
		}
		nodes, err := core.CollectAllStrict[core.HypergraphNode](db.engine, core.SharedPoolAgentID, core.RecL3GraphNode)
		if err != nil {
			t.Fatalf("collect graph nodes: %v", err)
		}
		edges, err := core.CollectAllStrict[core.HypergraphEdge](db.engine, core.SharedPoolAgentID, core.RecL3GraphEdge)
		if err != nil {
			t.Fatalf("collect graph edges: %v", err)
		}
		return [3]int{len(slots), len(nodes), len(edges)}
	}
	gotX, gotY := pool(x), pool(y)
	if gotX != gotY {
		t.Fatalf("after deleting a graph the pool holds %+v, while the file that never had it holds %+v", gotX, gotY)
	}
	if gotY != [3]int{1, 2, 1} {
		t.Fatalf("the control file is not the two nodes and one edge the batch should leave: %+v", gotY)
	}

	remaining, err := x.ListL3(core.DefaultAgentID)
	if err != nil || len(remaining) != 1 {
		t.Fatalf("ListL3 after reopen = %+v err %v, want the one surviving graph", remaining, err)
	}
	if common.FormatHash(remaining[0].IDHash) != memhopGraph {
		t.Fatalf("the surviving graph is %s, want %s", common.FormatHash(remaining[0].IDHash), memhopGraph)
	}
	full, err := x.GetL3(core.DefaultAgentID, memhopGraph)
	if err != nil {
		t.Fatalf("GetL3 the surviving graph: %v", err)
	}
	if len(full.Nodes) != 2 || len(full.Edges) != 1 {
		t.Fatalf("the surviving graph reads %d nodes / %d edges, want 2 / 1", len(full.Nodes), len(full.Edges))
	}
	sub, err := x.QueryL3Subgraph(core.DefaultAgentID, memhopGraph, common.FormatHash(full.Nodes[0].IDHash), 3, nil)
	if err != nil {
		t.Fatalf("subgraph walk in the surviving graph after the reopen: %v", err)
	}
	if len(sub.Nodes) != 2 {
		t.Fatalf("the walk reaches %d nodes, want the pair its own edge joins: %+v", len(sub.Nodes), sub.Nodes)
	}
}
