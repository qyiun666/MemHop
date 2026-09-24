// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// A subgraph is the answer a tool gives the model: "here is what this node relates to". For
// that to be one call, the returned edges have to be describable by the returned nodes — an
// edge naming a member that is not in the same result leaves the host with an id it cannot
// name, and the only way to finish the sentence is a second call (or a silent half-answer).
// Hyperedges are where this can actually happen: an edge spanning three nodes touches a node
// the traversal reached while the other two sit beyond the depth the caller asked for.

package test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	memhop "github.com/qyiun666/MemHop/api"
)

func TestInterfaceSubgraphEdgesNameOnlyNodesItReturns(t *testing.T) {
	llm := newMockLLM(t)
	db := newTestDB(t, openMockDB(t, filepath.Join(t.TempDir(), "subgraph.meh"), llm.srv.URL))

	imp, err := db.ImportL3([]memhop.L3ImportItem{
		{Title: "core", Domain: "proj", NodeType: "package", Content: "the entry",
			Related: []memhop.L3Relation{{Titles: []string{"util"}, Kind: memhop.EdgeDependency}}},
		// The hyperedge hangs off `util`, one step from the start, and spans two nodes the
		// walk reaches only by crossing it. This is the shape where an edge can name a
		// member the result never describes.
		{Title: "util", Domain: "proj", NodeType: "module", Content: "a helper",
			Related: []memhop.L3Relation{
				{Titles: []string{"deep", "side"}, Kind: memhop.EdgePartOf},
			}},
		{Title: "deep", Domain: "proj", NodeType: "module", Content: "two steps out"},
		{Title: "side", Domain: "proj", NodeType: "module", Content: "travels with deep"},
	}, memhop.L3ImportOverwrite)
	if err != nil || len(imp.Errors) != 0 {
		t.Fatalf("seed the graph: %v %+v", err, imp)
	}
	graphID := imp.GraphIDs[0]
	nodes, err := db.QueryL3Nodes(memhop.L3NodeQuery{GraphID: graphID})
	if err != nil {
		t.Fatalf("QueryL3Nodes: %v", err)
	}
	idBy := map[string]string{}
	for _, n := range nodes {
		idBy[n.Title] = n.ID
	}

	for _, depth := range []int{1, 2, 4, 0} {
		sub, err := db.QueryL3Subgraph(graphID, idBy["core"], depth, nil)
		if err != nil {
			t.Fatalf("QueryL3Subgraph(depth=%d): %v", depth, err)
		}
		present := make([]string, 0, len(sub.Nodes))
		for _, n := range sub.Nodes {
			present = append(present, n.ID)
		}
		for _, e := range sub.Edges {
			for _, member := range e.NodeIDs {
				if !slices.Contains(present, member) {
					t.Errorf("depth %d: edge %s (%s) names %s, which the same result does not describe — "+
						"the host cannot say what this edge relates without a second call\nnodes: %s",
						depth, e.ID, e.Kind, titleOf(idBy, member), titles(sub))
				}
			}
		}
		// The guard that keeps the check above from passing on an empty result: the shape it
		// is meant to cover has to be the one produced. At depth 1 the crossing hyperedge is
		// excluded — which is exactly why the closure check would otherwise have nothing to
		// say — and from depth 2 up it is present with all three members alongside it.
		sizes := edgeSizes(sub)
		slices.Sort(sizes)
		switch depth {
		case 1:
			if titles(sub) != "core,util" || !slices.Equal(sizes, []int{2}) {
				t.Errorf("depth 1 reached nodes [%s] with edge widths %v, want the one edge fully inside "+
					"the first step and the hyperedge left out", titles(sub), sizes)
			}
		case 0, 2, 4:
			if titles(sub) != "core,deep,side,util" || !slices.Equal(sizes, []int{2, 3}) {
				t.Errorf("depth %d reached nodes [%s] with edge widths %v, want the whole component and "+
					"both a binary edge and the hyperedge", depth, titles(sub), sizes)
			}
		}
	}
}

// titleOf reads a member id back to the label the fixture wrote, so a failure names the node
// a host would have to look up, not a hash.
func titleOf(idBy map[string]string, id string) string {
	for title, got := range idBy {
		if got == id {
			return title
		}
	}
	return id
}

func edgeSizes(sub *memhop.L3Subgraph) []int {
	out := make([]int, 0, len(sub.Edges))
	for _, e := range sub.Edges {
		out = append(out, len(e.NodeIDs))
	}
	return out
}

func titles(sub *memhop.L3Subgraph) string {
	rows := make([]string, 0, len(sub.Nodes))
	for _, n := range sub.Nodes {
		rows = append(rows, n.Title)
	}
	slices.Sort(rows)
	return strings.Join(rows, ",")
}
