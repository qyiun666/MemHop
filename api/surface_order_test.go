// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package api

import (
	"encoding/json"
	"testing"
)

// A host compares two recalls, indexes a listing by position, or diffs what the library
// answered before and after a Dream — all three assume the order a read returns is part of
// the answer, not an accident of which bucket Go happened to walk first. Every pure read is
// therefore taken repeatedly against the same populated file and must encode identically:
// map iteration is randomized per process, so a list assembled by ranging a map fails this
// the way it fails a host.
func TestSurfaceReadOrderIsDeterministic(t *testing.T) {
	m, sess, stubURL := openSurfaceLibrary(t)

	// Two sub-domains, so the file's own domain listing has something to order beyond
	// its single primary. Each gets its own id from the registry.
	for _, name := range []string{"worker", "helper", "archivist"} {
		if _, err := m.SubAgent(surfaceLLM(stubURL), ProfileInput{Name: name, Role: "sub"}); err != nil {
			t.Fatalf("SubAgent %s: %v", name, err)
		}
	}

	// Two graphs, so a listing of graphs has something to order as well. Each domain is
	// imported by its own batch, because that is how the batch reports the graph it
	// resolved into — an id the host names rather than one it guesses out of a listing.
	proj := []L3ImportItem{
		{Title: "auth", Domain: "proj", NodeType: "package", Content: "who logs in",
			Related: []L3Relation{{Titles: []string{"token", "session"}, Kind: EdgeDependency}}},
		{Title: "token", Domain: "proj", NodeType: "module", Content: "minted and checked",
			Related: []L3Relation{{Titles: []string{"session"}, Kind: EdgeDependency}}},
		{Title: "session", Domain: "proj", NodeType: "module", Content: "carries the token",
			Related: []L3Relation{{Titles: []string{"auth", "cache"}, Kind: EdgeDependency}}},
		{Title: "cache", Domain: "proj", NodeType: "module", Content: "sits in front",
			Related: []L3Relation{{Titles: []string{"auth", "token", "session"}, Kind: EdgeDependency}}},
	}
	side := []L3ImportItem{
		{Title: "worker", Domain: "other", NodeType: "module", Content: "runs the step",
			Related: []L3Relation{{Titles: []string{"auth"}, Kind: EdgeDependency}}},
		{Title: "queue", Domain: "other", NodeType: "module", Content: "holds the work",
			Related: []L3Relation{{Titles: []string{"worker", "auth"}, Kind: EdgeDependency}}},
	}
	impProj, err := sess.ImportL3(proj, L3ImportOverwrite)
	if err != nil {
		t.Fatalf("ImportL3 proj: %v", err)
	}
	if _, err := sess.ImportL3(side, L3ImportOverwrite); err != nil {
		t.Fatalf("ImportL3 side: %v", err)
	}
	projID := impProj.GraphIDs[0]

	for i := 0; i < 6; i++ {
		if _, err := sess.Search(SearchQuery{NewScene: i > 0}); err != nil {
			t.Fatalf("Search %d: %v", i, err)
		}
		if i == 0 {
			for _, in := range []ArchiveInput{
				{Kind: KindUtterance, ContentType: ContentText, Role: 1, CreatedAt: turnStamp, Content: "first line"},
				{Kind: KindUtterance, ContentType: ContentText, Role: 2, CreatedAt: turnStamp, Content: "answer"},
				{Kind: KindEvent, ContentType: ContentText, EventType: "tool_call", CreatedAt: turnStamp, Content: "grep auth"},
				{Kind: KindEvent, ContentType: ContentText, EventType: "tool_call", CreatedAt: turnStamp, Content: "read config"},
			} {
				if _, err := sess.AppendArchive(in); err != nil {
					t.Fatalf("AppendArchive: %v", err)
				}
			}
			for _, step := range []struct {
				parent uint32
				title  string
			}{{0, "root"}, {1, "child a"}, {1, "child b"}, {3, "grandchild"}} {
				if _, err := sess.PlanNodeAdd(step.parent, step.title); err != nil {
					t.Fatalf("PlanNodeAdd: %v", err)
				}
			}
		}
		if _, err := sess.Update(TurnEnd{Input: "in", Output: "out",
			Outcome: "answered", CreatedAt: turnStamp + int64(i)}); err != nil {
			t.Fatalf("Update %d: %v", i, err)
		}
	}
	scenes, err := sess.ListScenes("")
	if err != nil || len(scenes) < 2 {
		t.Fatalf("ListScenes: %+v err %v", scenes, err)
	}
	graphs, err := sess.ListL3()
	if err != nil || len(graphs) < 2 {
		t.Fatalf("ListL3: %+v err %v", graphs, err)
	}
	gnodes, err := sess.QueryL3Nodes(L3NodeQuery{GraphID: projID})
	if err != nil || len(gnodes) < 4 {
		t.Fatalf("QueryL3Nodes: %+v err %v", gnodes, err)
	}
	kind := KindEvent

	taken := map[string]func() any{
		"GetL0":              func() any { return mustRead(sess.GetL0()) },
		"ListL1":             func() any { return mustRead(sess.ListL1()) },
		"ListScenes":         func() any { return mustRead(sess.ListScenes("")) },
		"ListScenes/anchor":  func() any { return mustRead(sess.ListScenes(graphs[0].ID)) },
		"ListL3":             func() any { return mustRead(sess.ListL3()) },
		"GetL3":              func() any { return mustRead(sess.GetL3(projID)) },
		"QueryL3Nodes":       func() any { return mustRead(sess.QueryL3Nodes(L3NodeQuery{GraphID: projID})) },
		"QueryL3Subgraph":    func() any { return mustRead(sess.QueryL3Subgraph(projID, gnodes[0].ID, 4, nil)) },
		"SearchL4":           func() any { return mustRead(sess.SearchL4(L4Query{})) },
		"SearchL4/kind":      func() any { return mustRead(sess.SearchL4(L4Query{Kind: &kind})) },
		"SceneContext":       func() any { return mustRead(sess.SceneContext("")) },
		"SceneContext/other": func() any { return mustRead(sess.SceneContext(scenes[1].SceneID)) },
		"Agents":             func() any { return mustRead(m.Agents()) },
	}
	// Eight rounds, because a map with two or three keys can walk in the same order twice
	// by luck.
	for name, read := range taken {
		var first string
		for round := 0; round < 8; round++ {
			got := encode(t, name, read())
			if round == 0 {
				first = got
				continue
			}
			if got != first {
				t.Fatalf("%s answered differently on repeat %d:\n%s\n---\n%s", name, round, first, got)
			}
		}
	}
}

func mustRead[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func encode(tb testing.TB, label string, v any) string {
	tb.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		tb.Fatalf("marshal %s: %v", label, err)
	}
	return string(raw)
}
