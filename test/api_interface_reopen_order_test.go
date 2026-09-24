// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// The order promise a host actually leans on is not "twice in one process" but "across my
// restarts": a worker that indexes a listing by position, diffs two recalls, or caches what
// a scene looked like yesterday is comparing reads taken from different processes. Those two
// processes do not build their state the same way either — one served its lists from the
// index it grew while writing, the next restores indexes from the checkpoint snapshot or,
// when the snapshot lags the format, from a full record scan. So every pure read is taken
// over a settled file, the file is closed, and each read is taken again on two fresh opens:
// the bytes have to match.

package test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	memhop "github.com/qyiun666/MemHop/api"
)

func TestInterfaceReadsSurviveAReopenByteForByte(t *testing.T) {
	llm := newMockLLM(t)
	path := filepath.Join(t.TempDir(), "reopen.meh")
	m := openMockDB(t, path, llm.srv.URL)
	db := newTestDB(t, m)
	stamp := time.Now().Add(-time.Hour).UnixMilli()

	for _, name := range []string{"worker", "helper"} {
		if _, err := m.SubAgent(testLLM(llm.srv.URL), memhop.ProfileInput{Name: name, Role: "sub"}); err != nil {
			t.Fatalf("SubAgent %s: %v", name, err)
		}
	}
	for _, domain := range []string{"proj", "notes"} {
		if _, err := db.ImportL3([]memhop.L3ImportItem{
			{Title: "a", Domain: domain, NodeType: "concept", Content: "first",
				Related: []memhop.L3Relation{{Titles: []string{"b"}, Kind: memhop.EdgeRelated}}},
			{Title: "b", Domain: domain, NodeType: "concept", Content: "second"},
		}, memhop.L3ImportMerge); err != nil {
			t.Fatalf("ImportL3 %s: %v", domain, err)
		}
	}
	for i := 0; i < 6; i++ {
		if _, err := db.Search(memhop.SearchQuery{NewScene: i == 3}); err != nil {
			t.Fatalf("Search %d: %v", i, err)
		}
		if i == 0 {
			// The tree first: an event can only name a step that exists.
			for _, step := range []struct {
				parent uint32
				title  string
			}{{0, "root"}, {1, "child a"}, {1, "child b"}, {3, "grandchild"}} {
				if _, err := db.PlanNodeAdd(step.parent, step.title); err != nil {
					t.Fatalf("PlanNodeAdd: %v", err)
				}
			}
			for _, in := range []memhop.ArchiveInput{
				{Kind: memhop.KindUtterance, ContentType: memhop.ContentText, Role: uint8(memhop.RoleUser),
					CreatedAt: stamp, Content: "does the index order survive a restart?"},
				{Kind: memhop.KindUtterance, ContentType: memhop.ContentText, Role: uint8(memhop.RoleAgent),
					CreatedAt: stamp + 10, Content: "it must"},
				{Kind: memhop.KindEvent, ContentType: memhop.ContentText, EventType: "tool_call",
					NodeSeq: 2, CreatedAt: stamp + 20, Content: "grep index"},
				{Kind: memhop.KindEvent, ContentType: memhop.ContentText, EventType: "tool_call",
					CreatedAt: stamp + 30, Content: "read order"},
			} {
				if _, err := db.AppendArchive(in); err != nil {
					t.Fatalf("AppendArchive: %v", err)
				}
			}
		}
		if _, err := db.Update(memhop.TurnEnd{Input: fmt.Sprintf("in %d", i),
			Output: fmt.Sprintf("out %d", i), Outcome: "answered",
			CreatedAt: stamp + int64(i)}); err != nil {
			t.Fatalf("Update %d: %v", i, err)
		}
	}
	if _, err := db.Dream(context.Background(), ""); err != nil {
		t.Fatalf("Dream: %v", err)
	}

	names, before := snapshotReads(t, db, m)
	if err := m.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	for round := 1; round <= 2; round++ {
		reopened := openMockDB(t, path, llm.srv.URL)
		again := newTestDB(t, reopened)
		_, got := snapshotReads(t, again, reopened)
		for i, name := range names {
			if got[i] != before[i] {
				t.Fatalf("reopen %d answered %s differently:\nfirst:  %s\nreopen: %s",
					round, name, before[i], got[i])
			}
		}
		if err := reopened.Close(); err != nil {
			t.Fatalf("close reopen %d: %v", round, err)
		}
	}
}

// snapshotReads encodes every pure read against one handle. Each argument the reads need —
// a graph id, a node id, a scene id — is taken from that same handle, so a mismatch between
// two processes is a difference in what the reads answer, never in what was asked of them.
func snapshotReads(tb testing.TB, db *testDB, m *memhop.DB) ([]string, []string) {
	tb.Helper()
	graphs, err := db.ListL3()
	if err != nil {
		tb.Fatalf("ListL3: %v", err)
	}
	if len(graphs) < 2 {
		tb.Fatalf("the fixture needs two graphs to order, got %d", len(graphs))
	}
	gnodes, err := db.QueryL3Nodes(memhop.L3NodeQuery{GraphID: graphs[0].ID})
	if err != nil {
		tb.Fatalf("QueryL3Nodes: %v", err)
	}
	if len(gnodes) == 0 {
		tb.Fatal("the first graph lists no nodes to start a subgraph walk from")
	}
	scenes, err := db.ListScenes("")
	if err != nil {
		tb.Fatalf("ListScenes: %v", err)
	}
	if len(scenes) < 2 {
		tb.Fatalf("the fixture needs two scenes to order, got %d", len(scenes))
	}
	events := memhop.KindEvent
	graphID, nodeID, sceneID, anchor := graphs[0].ID, gnodes[0].ID, scenes[1].SceneID, graphs[0].ID
	reads := []struct {
		name string
		read func() (any, error)
	}{
		{"Agents", func() (any, error) { return m.Agents() }},
		{"GetL0", func() (any, error) { return db.GetL0() }},
		{"GetL3", func() (any, error) { return db.GetL3(graphID) }},
		{"ListL1", func() (any, error) { return db.ListL1() }},
		{"ListL3", func() (any, error) { return db.ListL3() }},
		{"ListScenes", func() (any, error) { return db.ListScenes("") }},
		{"ListScenes/anchor", func() (any, error) { return db.ListScenes(anchor) }},
		{"QueryL3Nodes", func() (any, error) {
			return db.QueryL3Nodes(memhop.L3NodeQuery{GraphID: graphID})
		}},
		{"QueryL3Subgraph", func() (any, error) { return db.QueryL3Subgraph(graphID, nodeID, 4, nil) }},
		{"SceneContext", func() (any, error) { return db.SceneContext("") }},
		{"SceneContext/named", func() (any, error) { return db.SceneContext(sceneID) }},
		{"SearchL4", func() (any, error) { return db.SearchL4(memhop.L4Query{}) }},
		{"SearchL4/kind", func() (any, error) { return db.SearchL4(memhop.L4Query{Kind: &events}) }},
	}
	names := make([]string, 0, len(reads))
	encoded := make([]string, 0, len(reads))
	for _, r := range reads {
		v, err := r.read()
		if err != nil {
			tb.Fatalf("%s: %v", r.name, err)
		}
		raw, err := json.Marshal(v)
		if err != nil {
			tb.Fatalf("encode %s: %v", r.name, err)
		}
		names = append(names, r.name)
		encoded = append(encoded, string(raw))
	}
	return names, encoded
}
