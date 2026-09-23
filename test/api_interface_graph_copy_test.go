// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// One recruited worker, one file of its own — and with it an empty L3 pool, because the
// project graph is shared inside a file, not across files. So a host that wants the worker
// to know the project has to copy the graph, and the question this answers is whether that
// is doable through the public surface alone: read the parent's graph, rebuild the batch,
// import it. No private type, no id surgery, no re-derivation of addresses.

package test

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	memhop "github.com/qyiun666/MemHop/api"
)

// copyBatch is the recipe, written once so a host can copy it: one item per node, and every
// hyperedge hung under its lowest member as that item's relation. A hyperedge is an
// unordered set, so picking one member as the batch's anchor loses nothing — the stored edge
// is keyed by the sorted member set plus the kind.
func copyBatch(g *memhop.L3Graph) []memhop.L3ImportItem {
	title := map[string]string{}
	for _, n := range g.Nodes {
		title[n.ID] = n.Title
	}
	bySource := map[string][]memhop.L3Relation{}
	items := make([]memhop.L3ImportItem, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		items = append(items, memhop.L3ImportItem{
			Title: n.Title, Domain: g.Slot.Name, NodeType: n.NodeType, Content: n.Content,
			Keywords: slices.Clone(n.Keywords), SourceRef: n.SourceRef,
		})
	}
	for _, e := range g.Edges {
		members := make([]string, 0, len(e.NodeIDs))
		for _, id := range e.NodeIDs {
			members = append(members, title[id])
		}
		slices.Sort(members)
		source := members[0]
		bySource[source] = append(bySource[source], memhop.L3Relation{
			Titles: members[1:], Kind: e.Kind,
		})
	}
	for i := range items {
		items[i].Related = bySource[items[i].Title]
	}
	return items
}

func TestInterfaceGraphCopyBetweenLibraries(t *testing.T) {
	llm := newMockLLM(t)
	parent := newTestDB(t, openMockDB(t, filepath.Join(t.TempDir(), "parent.meh"), llm.srv.URL))
	worker := newTestDB(t, openMockDB(t, filepath.Join(t.TempDir(), "worker.meh"), llm.srv.URL))

	if _, err := parent.ImportL3([]memhop.L3ImportItem{
		{Title: "auth", Domain: "proj", NodeType: "package", Content: "who logs in",
			Keywords: []string{"login", "token"},
			Related: []memhop.L3Relation{
				{Titles: []string{"login.go", "token.go"}, Kind: memhop.EdgeDependency},
				{Titles: []string{"session"}, Kind: memhop.EdgePartOf},
			}},
		{Title: "login.go", Domain: "proj", NodeType: "file", Content: "the handler"},
		{Title: "token.go", Domain: "proj", NodeType: "file", Content: "mints and checks"},
		{Title: "session", Domain: "proj", NodeType: "concept", Content: "carries the token",
			SourceRef: "docs/auth.md:12",
			Related:   []memhop.L3Relation{{Titles: []string{"auth", "token.go"}, Kind: memhop.EdgeDependency}}},
	}, memhop.L3ImportOverwrite); err != nil {
		t.Fatalf("seed the parent graph: %v", err)
	}

	src, err := parent.GetL3(onlyGraph(t, parent).ID)
	if err != nil {
		t.Fatalf("read the parent graph: %v", err)
	}
	if len(src.Nodes) != 4 || len(src.Edges) != 3 {
		t.Fatalf("the parent graph is %d nodes / %d edges, want 4/3", len(src.Nodes), len(src.Edges))
	}

	// The worker is a different family: it starts knowing nothing.
	if got, err := worker.ListL3(); err != nil || len(got) != 0 {
		t.Fatalf("the worker's pool is %+v (err %v), want empty", got, err)
	}

	res, err := worker.ImportL3(copyBatch(src), memhop.L3ImportOverwrite)
	if err != nil {
		t.Fatalf("import the copy: %v", err)
	}
	if len(res.Errors) != 0 || len(res.CreatedIDs) != 4 || res.EdgesCreated != 3 {
		t.Fatalf("the copy reported %+v, want four nodes and three edges and no errors", res)
	}

	dst, err := worker.GetL3(res.GraphIDs[0])
	if err != nil {
		t.Fatalf("read the copy: %v", err)
	}
	// A graph's id derives from its label, so the copy lands on the same id the parent's
	// graph has — a host can name a project domain identically in every file.
	if dst.Slot.ID != src.Slot.ID || dst.Slot.Name != src.Slot.Name {
		t.Fatalf("the copy is %s/%s, want the parent's %s/%s",
			dst.Slot.ID, dst.Slot.Name, src.Slot.ID, src.Slot.Name)
	}

	// Nodes, field by field, keyed by title: the text a host reads into a prompt.
	srcNodes := nodesByTitle(src.Nodes)
	dstNodes := nodesByTitle(dst.Nodes)
	if len(srcNodes) != len(dstNodes) {
		t.Fatalf("the copy holds %d nodes, want %d", len(dstNodes), len(srcNodes))
	}
	for name, want := range srcNodes {
		got, ok := dstNodes[name]
		if !ok {
			t.Fatalf("the copy lost the node %q", name)
		}
		if got.NodeType != want.NodeType || got.Content != want.Content ||
			!slices.Equal(got.Keywords, want.Keywords) || got.SourceRef != want.SourceRef {
			t.Fatalf("node %q came across changed:\n got %+v\nwant %+v", name, got, want)
		}
	}

	// Edges, keyed by kind plus the sorted member titles: a set, not a pair.
	if got, want := edgeSets(dst), edgeSets(src); got != want {
		t.Fatalf("the copy's edges are %s, want %s", got, want)
	}

	// Importing the same copy again is still three edges — the copy is safe to retry, which
	// is what a host that crashed mid-spawn needs.
	if _, err := worker.ImportL3(copyBatch(src), memhop.L3ImportSkip); err != nil {
		t.Fatalf("re-import the copy: %v", err)
	}
	repeat, err := worker.GetL3(res.GraphIDs[0])
	if err != nil {
		t.Fatalf("read the copy after the replay: %v", err)
	}
	if len(repeat.Nodes) != 4 || len(repeat.Edges) != 3 {
		t.Fatalf("the replay left %d nodes / %d edges, want 4/3", len(repeat.Nodes), len(repeat.Edges))
	}
}

func onlyGraph(t *testing.T, db *testDB) memhop.HypergraphSlot {
	t.Helper()
	graphs, err := db.ListL3()
	if err != nil {
		t.Fatalf("ListL3: %v", err)
	}
	if len(graphs) != 1 {
		t.Fatalf("the file holds %d graphs, want 1", len(graphs))
	}
	return graphs[0]
}

func nodesByTitle(nodes []memhop.HypergraphNode) map[string]memhop.HypergraphNode {
	out := make(map[string]memhop.HypergraphNode, len(nodes))
	for _, n := range nodes {
		out[n.Title] = n
	}
	return out
}

func edgeSets(g *memhop.L3Graph) string {
	titles := map[string]string{}
	for _, n := range g.Nodes {
		titles[n.ID] = n.Title
	}
	rows := make([]string, 0, len(g.Edges))
	for _, e := range g.Edges {
		members := make([]string, 0, len(e.NodeIDs))
		for _, id := range e.NodeIDs {
			members = append(members, titles[id])
		}
		slices.Sort(members)
		rows = append(rows, fmt.Sprintf("%d:{%s}", e.Kind, strings.Join(members, ",")))
	}
	slices.Sort(rows)
	return strings.Join(rows, " ")
}
