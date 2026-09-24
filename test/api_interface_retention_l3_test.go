// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	memhop "github.com/qyiun666/MemHop/api"
)

// The retention window is measured in wall-clock time and everything a turn leaves behind is
// inside it — but the knowledge graph is not a turn's record. It is the file's shared pool:
// one project's nodes, imported once and read by every domain as a tool, with its own clock
// that only moves when its content changes. A host that lets the engine consolidate for a
// month must be able to answer the same `l3_query` on day 31, and nothing in the sweep's
// shape says so out loud: the window is a single number, and the only thing separating "how
// long a turn outlives" from "how long anything outlives" is which records the sweep is
// allowed to name.
//
// So the sweep runs here for real — the turn's own archives must go, or this proves the
// window never fired rather than that it stopped at the graph — and then the reads a bound
// tool uses have to answer as they did before.
func TestInterfaceTheRetentionWindowStopsAtTheKnowledgeGraph(t *testing.T) {
	llm := newMockLLM(t)
	path := filepath.Join(t.TempDir(), "retention_l3.meh")
	m := openMockDB(t, path, llm.srv.URL, sweepOnly)
	sess, err := m.Primary()
	if err != nil {
		t.Fatalf("Primary: %v", err)
	}

	imported, err := sess.ImportL3([]memhop.L3ImportItem{{
		Title: "memhop", Domain: "引擎", NodeType: "concept", Content: "单文件记忆引擎",
	}}, memhop.L3ImportSkip)
	if err != nil || len(imported.GraphIDs) != 1 || len(imported.Errors) != 0 {
		t.Fatalf("seed the project graph: %v %+v", err, imported)
	}
	graphID := imported.GraphIDs[0]

	if _, err := sess.Search(memhop.SearchQuery{NewScene: true}); err != nil {
		t.Fatalf("open a scene: %v", err)
	}
	if _, err := turn(sess, "这个库里 memhop 是什么", "一个单文件的记忆引擎"); err != nil {
		t.Fatalf("close the turn: %v", err)
	}

	// The window is a millisecond wide, so the turn above is past it by the time the pass runs.
	time.Sleep(20 * time.Millisecond)
	rep, err := sess.Dream(context.Background(), "")
	if err != nil {
		t.Fatalf("Dream: %v", err)
	}
	if rep.L4RecordsPruned == 0 {
		t.Fatalf("nothing was swept, so this run cannot say the window stopped anywhere: %+v", rep)
	}

	graphs, err := sess.ListL3()
	if err != nil {
		t.Fatalf("ListL3 after the sweep: %v", err)
	}
	listed := false
	for _, g := range graphs {
		if g.ID == graphID {
			listed = true
		}
	}
	if !listed {
		t.Fatalf("the sweep took the project graph with the turn's originals: %s is gone from %+v",
			graphID, graphs)
	}

	// A tool call does not stop at the graph's name: it reads the node and asks it for its
	// content, which is the answer the host puts back in front of the model.
	graph, err := sess.GetL3(graphID)
	if err != nil {
		t.Fatalf("GetL3 after the sweep: %v", err)
	}
	if len(graph.Nodes) != 1 || graph.Nodes[0].Title != "memhop" {
		t.Fatalf("the graph reads with %d nodes after the sweep: %+v", len(graph.Nodes), graph.Nodes)
	}
	if graph.Nodes[0].Content != "单文件记忆引擎" {
		t.Fatalf("the node survived as an empty shell — its content is what a query answers with: %+v",
			graph.Nodes[0])
	}
	found, err := sess.QueryL3Nodes(memhop.L3NodeQuery{GraphID: graphID, Keyword: "记忆"})
	if err != nil {
		t.Fatalf("QueryL3Nodes after the sweep: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("the keyword a tool would search on now matches %d nodes: %+v", len(found), found)
	}
}
