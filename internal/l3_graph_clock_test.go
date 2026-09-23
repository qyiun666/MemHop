// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// The graph slot's UpdatedAt answers "when did this graph last change", not "when was a
// read or an import last aimed at it" — so a host that lists its project knowledge by
// recency gets recency of content, not of access. The batch therefore keeps two sets: the
// graphs it resolved a domain into (which GraphIDs reports) and the graphs whose nodes or
// edges it actually wrote (which are the only ones it stamps).
func TestGraphSlotClockAnswersChangeNotAccess(t *testing.T) {
	db := newL3TestDB(t)
	proj := []L3ImportItem{
		{Title: "module:auth", Domain: "proj", NodeType: "package", Content: "who logs in",
			Related: []L3Relation{{Titles: []string{"login.go"}, Kind: GraphEdgeKind(EdgePartOf)}}},
		{Title: "login.go", Domain: "proj", NodeType: "file", Content: "the handler"},
	}
	ops := []L3ImportItem{
		{Title: "ci", Domain: "ops", NodeType: "concept", Content: "pipelines",
			Related: []L3Relation{{Titles: []string{"runner"}, Kind: GraphEdgeKind(EdgePartOf)}}},
		{Title: "runner", Domain: "ops", NodeType: "concept", Content: "executors"},
	}
	items := append(append([]L3ImportItem{}, proj...), ops...)

	first, err := db.ImportL3(core.DefaultAgentID, items, L3ImportOverwrite)
	if err != nil {
		t.Fatalf("import the batch: %v", err)
	}
	if len(first.GraphIDs) != 2 || first.EdgesCreated != 2 {
		t.Fatalf("first batch reported graphs=%+v edges=%d, want two graphs and two edges",
			first.GraphIDs, first.EdgesCreated)
	}

	// Which hex id is which graph is the batch's business, not the test's: name them.
	byName, err := db.ListL3(core.DefaultAgentID)
	if err != nil {
		t.Fatalf("ListL3: %v", err)
	}
	if len(byName) != 2 {
		t.Fatalf("ListL3 answered %d graphs, want 2", len(byName))
	}
	var projID, opsID uint64
	for _, s := range byName {
		switch s.Name {
		case "proj":
			projID = s.IDHash
		case "ops":
			opsID = s.IDHash
		}
	}
	if projID == 0 || opsID == 0 {
		t.Fatalf("the two domains did not each get a graph: %+v", byName)
	}

	// Both clocks are set by that batch; put them back to known-old values so "moved
	// forward" and "did not move" are both read as equalities rather than as timing races.
	rewind := func(id uint64, to int64) {
		slot, err := core.ReadGraphSlot(db.engine, core.SharedPoolAgentID, id)
		if err != nil {
			t.Fatalf("read graph slot: %v", err)
		}
		slot.UpdatedAt = to
		if err := core.WriteGraphSlot(db.engine, core.SharedPoolAgentID, id, slot); err != nil {
			t.Fatalf("rewind graph clock: %v", err)
		}
	}
	rewind(projID, 1000)
	rewind(opsID, 2000)

	// The clocks are read straight off the records, so a read path that stamped on the way
	// is caught by the assertion right after it rather than one step later.
	slots := func() map[uint64]int64 {
		out := map[uint64]int64{}
		for _, id := range []uint64{projID, opsID} {
			slot, err := core.ReadGraphSlot(db.engine, core.SharedPoolAgentID, id)
			if err != nil {
				t.Fatalf("read graph slot: %v", err)
			}
			out[id] = slot.UpdatedAt
		}
		return out
	}

	// A read leaves both clocks where they were, whichever read it is.
	if got, err := db.ListL3(core.DefaultAgentID); err != nil || len(got) != 2 {
		t.Fatalf("ListL3 answered %d graphs (err %v), want 2", len(got), err)
	}
	if _, err := db.GetL3(core.DefaultAgentID, common.FormatHash(projID)); err != nil {
		t.Fatalf("GetL3: %v", err)
	}
	graph, err := db.GetL3(core.DefaultAgentID, common.FormatHash(opsID))
	if err != nil {
		t.Fatalf("GetL3: %v", err)
	}
	if _, err := db.QueryL3Subgraph(core.DefaultAgentID, common.FormatHash(opsID),
		common.FormatHash(graph.Nodes[0].IDHash), 1, nil); err != nil {
		t.Fatalf("QueryL3Subgraph: %v", err)
	}
	if got := slots(); got[projID] != 1000 || got[opsID] != 2000 {
		t.Fatalf("reads advanced the clocks: proj=%d ops=%d, want 1000 and 2000", got[projID], got[opsID])
	}

	// A Skip batch that resolves both domains but writes nothing names both graphs and
	// stamps neither — the two sets are not the same set.
	skipped, err := db.ImportL3(core.DefaultAgentID, items, L3ImportSkip)
	if err != nil {
		t.Fatalf("re-import in Skip mode: %v", err)
	}
	if len(skipped.GraphIDs) != 2 || skipped.SkippedCount != 4 || skipped.EdgesCreated != 0 {
		t.Fatalf("Skip batch reported graphs=%+v skipped=%d edges=%d, want both graphs, 4 nodes skipped, no edge",
			skipped.GraphIDs, skipped.SkippedCount, skipped.EdgesCreated)
	}
	if got := slots(); got[projID] != 1000 || got[opsID] != 2000 {
		t.Fatalf("a batch that wrote nothing advanced the clocks: proj=%d ops=%d, want 1000 and 2000", got[projID], got[opsID])
	}

	// Writing two nodes of one graph moves that graph's clock and leaves the other exactly
	// where it was: which graphs get stamped is which graphs' contents were written, so the
	// pair of answers pins the stamped set from both sides. The log growing is the witness
	// that this batch really wrote — a batch that wrote nothing would leave the same two
	// clocks and pass the two assertions below for the wrong reason.
	sizeBefore, _, err := db.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	changed := []L3ImportItem{
		{Title: "module:auth", Domain: "proj", NodeType: "package", Content: "who logs in, rewritten"},
		{Title: "login.go", Domain: "proj", NodeType: "file", Content: "the handler, rewritten"},
	}
	wrote, err := db.ImportL3(core.DefaultAgentID, changed, L3ImportOverwrite)
	if err != nil {
		t.Fatalf("overwrite two nodes: %v", err)
	}
	if len(wrote.UpdatedIDs) != 2 || len(wrote.GraphIDs) != 1 {
		t.Fatalf("overwrite batch reported updated=%+v graphs=%+v, want two nodes in one graph",
			wrote.UpdatedIDs, wrote.GraphIDs)
	}
	got := slots()
	if got[projID] <= 1000 {
		t.Errorf("the graph this batch wrote kept its old clock: %d", got[projID])
	}
	if got[opsID] != 2000 {
		t.Errorf("a graph this batch only resolved kept advancing: %d, want 2000", got[opsID])
	}
	sizeAfter, _, err := db.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if sizeAfter <= sizeBefore {
		t.Errorf("the overwrite appended nothing (log %d bytes before, %d after), so the two clock answers above prove nothing",
			sizeBefore, sizeAfter)
	}

	// A label is part of what the graph is, so renaming it counts as a change too.
	if _, err := db.UpdateL3(core.DefaultAgentID, common.FormatHash(opsID), ptrToString("pipelines")); err != nil {
		t.Fatalf("rename the graph: %v", err)
	}
	if got := slots(); got[opsID] == 2000 {
		t.Errorf("renaming a graph left its clock at %d, want it moved forward", got[opsID])
	}
}

func ptrToString(s string) *string { return &s }
