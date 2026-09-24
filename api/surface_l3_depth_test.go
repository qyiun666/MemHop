// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// How far a subgraph read goes: maxDepth counts hops, and a non-positive one is
// no bound rather than one hop.

package api

import (
	"slices"
	"testing"
)

// A subgraph is the one read where the host says how far to go, so that number
// has to mean one thing: N hops from the start node, and a non-positive N is no
// bound at all. The chain below is three hops long, which puts every reading on
// a different node set — a depth that counted levels of the import, or a zero
// read as "one hop", lands somewhere else and fails here.
//
// The second half is what makes the answer usable: every edge it carries names
// only nodes it also carries. An edge reaching outside would send the host
// looking up an id this very read refused to return.
func TestSurfaceSubgraphDepthCountsHopsAndZeroMeansNoBound(t *testing.T) {
	db := openSurfaceDB(t)
	imp, err := db.ImportL3([]L3ImportItem{
		{Title: "a", Domain: "chain"},
		{Title: "b", Domain: "chain", Related: []L3Relation{{Titles: []string{"a"}}}},
		{Title: "c", Domain: "chain", Related: []L3Relation{{Titles: []string{"b"}}}},
		{Title: "d", Domain: "chain", Related: []L3Relation{{Titles: []string{"c"}}}},
	}, L3ImportOverwrite)
	if err != nil || len(imp.Errors) != 0 {
		t.Fatalf("import the chain: err=%v errors=%v", err, imp.Errors)
	}
	graphs, err := db.ListL3()
	if err != nil || len(graphs) != 1 {
		t.Fatalf("list graphs: %d err=%v", len(graphs), err)
	}
	graphID := graphs[0].ID
	nodes, err := db.QueryL3Nodes(L3NodeQuery{GraphID: graphID})
	if err != nil {
		t.Fatalf("query nodes: %v", err)
	}
	id := map[string]string{}
	for _, n := range nodes {
		id[n.Title] = n.ID
	}
	if len(id) != 4 {
		t.Fatalf("the fixture is %d nodes, want the four of a chain: %v", len(id), id)
	}

	full, err := db.QueryL3Subgraph(graphID, id["a"], 0, nil)
	if err != nil {
		t.Fatalf("the unbounded walk: %v", err)
	}
	if len(full.Nodes) != 4 || len(full.Edges) != 3 {
		t.Fatalf("the fixture is not a three-hop chain: %d nodes %d edges", len(full.Nodes), len(full.Edges))
	}

	for _, tc := range []struct {
		start string
		depth int
		want  []string
	}{
		{"a", 1, []string{"a", "b"}},
		{"a", 2, []string{"a", "b", "c"}},
		{"a", 3, []string{"a", "b", "c", "d"}},
		{"a", 99, []string{"a", "b", "c", "d"}},
		{"a", -1, []string{"a", "b", "c", "d"}},
		{"c", 1, []string{"b", "c", "d"}},
		{"d", 1, []string{"c", "d"}},
	} {
		got, err := db.QueryL3Subgraph(graphID, id[tc.start], tc.depth, nil)
		if err != nil {
			t.Fatalf("%d hop(s) from %s: %v", tc.depth, tc.start, err)
		}
		titles := make([]string, 0, len(got.Nodes))
		for _, n := range got.Nodes {
			titles = append(titles, n.Title)
		}
		slices.Sort(titles)
		if !slices.Equal(titles, tc.want) {
			t.Fatalf("%d hop(s) from %s reached %v, want %v", tc.depth, tc.start, titles, tc.want)
		}
		reached := map[string]bool{}
		for _, n := range got.Nodes {
			reached[n.ID] = true
		}
		for _, e := range got.Edges {
			for _, member := range e.NodeIDs {
				if !reached[member] {
					t.Fatalf("%d hop(s) from %s carried an edge naming %s, a node the same answer does not return",
						tc.depth, tc.start, member)
				}
			}
		}
	}
}
