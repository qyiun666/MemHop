// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	memhop "github.com/qyiun666/MemHop/api"
)

// CompactTo rewrites the whole file from the live records, so it is the one path where a
// layer can be lost silently: the copy opens, answers, and looks fine while one bucket, one
// counter, or one domain quietly stopped being carried over. The existing check proves the
// deleted scene is gone and the graphs survived; this one proves everything else survived
// too — every read on both domains encodes identically before and after, the file got
// smaller, and the compacted copy still mints a turn that no earlier round used.
func TestInterfaceCompactedCopyAnswersIdentically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corpus.meh")
	url := newMockLLM(t).srv.URL
	db := openMockDB(t, path, url, func(d *memhop.MemHopDefaults) { d.SceneDreamTopicThreshold = -1 })

	primary, err := db.Primary()
	if err != nil {
		t.Fatalf("Primary: %v", err)
	}
	worker, err := db.SubAgent(testLLM(url), memhop.ProfileInput{Name: "worker", Role: "helper"})
	if err != nil {
		t.Fatalf("SubAgent: %v", err)
	}

	stamp := time.Now().Add(-time.Hour).UnixMilli()
	// One scene per round: `NewScene` is how a host asks for another session on the same
	// domain, so the corpus ends up with several scenes per bucket to carry over.
	build := func(sess *memhop.Session, rounds int, text string) {
		t.Helper()
		for i := 0; i < rounds; i++ {
			stamp += 1000
			if _, err := sess.Search(memhop.SearchQuery{NewScene: true}); err != nil {
				t.Fatalf("Search: %v", err)
			}
			if _, err := sess.AppendArchive(memhop.ArchiveInput{Kind: memhop.KindUtterance,
				ContentType: memhop.ContentText, Role: memhop.RoleUser, CreatedAt: stamp,
				Content: text + " 问 " + string(rune('a'+i))}); err != nil {
				t.Fatalf("AppendArchive: %v", err)
			}
			if _, err := sess.AppendArchive(memhop.ArchiveInput{Kind: memhop.KindEvent,
				ContentType: memhop.ContentText, EventType: "tool_call", CreatedAt: stamp,
				Content: "grep " + text}); err != nil {
				t.Fatalf("AppendArchive event: %v", err)
			}
			if _, err := sess.PlanNodeAdd(0, "step "+text); err != nil {
				t.Fatalf("PlanNodeAdd: %v", err)
			}
			if _, err := sess.Update(memhop.TurnEnd{Input: text + " in", Output: text + " out",
				Outcome: "answered", CreatedAt: stamp}); err != nil {
				t.Fatalf("Update: %v", err)
			}
		}
	}
	build(primary, 4, "主猫")
	build(worker, 2, "worker")
	if _, err := primary.ImportL3([]memhop.L3ImportItem{
		{Title: "auth", Domain: "proj", NodeType: "package", Content: "who logs in",
			Related: []memhop.L3Relation{{Titles: []string{"token"}, Kind: memhop.EdgeDependency}}},
		{Title: "token", Domain: "proj", NodeType: "module", Content: "minted and checked"},
	}, memhop.L3ImportOverwrite); err != nil {
		t.Fatalf("ImportL3: %v", err)
	}

	// Two corrections, so the copy has to carry a live set that is smaller than the log.
	scenes, err := primary.ListScenes("")
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	if len(scenes) != 4 {
		t.Fatalf("primary holds %d scenes, want one per round: %+v", len(scenes), scenes)
	}
	if err := primary.DeleteScene(scenes[1].SceneID); err != nil {
		t.Fatalf("DeleteScene: %v", err)
	}

	snapshot := func(label string, sess *memhop.Session) string {
		t.Helper()
		prof, err := sess.GetL0()
		if err != nil {
			t.Fatalf("%s GetL0: %v", label, err)
		}
		nodes, err := sess.ListL1()
		if err != nil {
			t.Fatalf("%s ListL1: %v", label, err)
		}
		list, err := sess.ListScenes("")
		if err != nil {
			t.Fatalf("%s ListScenes: %v", label, err)
		}
		type view struct {
			Profile any    `json:"profile"`
			Nodes   any    `json:"nodes"`
			Scenes  any    `json:"scenes"`
			Context []any  `json:"contexts"`
			Arch    any    `json:"archives"`
			Graphs  any    `json:"graphs"`
			Domain  string `json:"domain"`
		}
		v := view{Profile: prof, Nodes: nodes, Scenes: list, Domain: label}
		for _, sc := range list {
			ctx, err := sess.SceneContext(sc.SceneID)
			if err != nil {
				t.Fatalf("%s SceneContext(%s): %v", label, sc.SceneID, err)
			}
			v.Context = append(v.Context, ctx)
		}
		arch, err := sess.SearchL4(memhop.L4Query{})
		if err != nil {
			t.Fatalf("%s SearchL4: %v", label, err)
		}
		v.Arch = arch
		graphs, err := sess.ListL3()
		if err != nil {
			t.Fatalf("%s ListL3: %v", label, err)
		}
		v.Graphs = graphs
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("%s marshal: %v", label, err)
		}
		return string(raw)
	}

	beforePrimary := snapshot("primary", primary)
	beforeWorker := snapshot("worker", worker)
	beforeStats, err := db.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if beforeStats.RecordCount == 0 || beforeStats.FileBytes == 0 {
		t.Fatalf("the corpus reported nothing: %+v", beforeStats)
	}

	copyPath := filepath.Join(dir, "compacted.meh")
	if err := db.CompactTo(copyPath); err != nil {
		t.Fatalf("CompactTo: %v", err)
	}
	// `CompactTo` leaves the open file alone, so the numbers to compare are the copy's own.
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	lib, err := memhop.Open(copyPath, testLLM(url), memhop.DefaultMemHopDefaults,
		&memhop.ProfileInput{Name: "test-primary", Role: "offline fixture"})
	if err != nil {
		t.Fatalf("Open the compacted copy: %v", err)
	}
	defer lib.Close()
	copyStats, err := lib.Stats()
	if err != nil {
		t.Fatalf("Stats on the copy: %v", err)
	}
	if copyStats.RecordCount != beforeStats.RecordCount {
		t.Fatalf("the copy reaches %d records, want the same live set the open file reached (%d) — compaction lost or invented records",
			copyStats.RecordCount, beforeStats.RecordCount)
	}
	if copyStats.FileBytes >= beforeStats.FileBytes {
		t.Fatalf("the copy is no smaller: %d bytes vs %d — the deleted scene's records and the rewritten scene slots were not reclaimed",
			copyStats.FileBytes, beforeStats.FileBytes)
	}
	t.Logf("compaction: %d bytes -> %d, %d reachable records on both sides",
		beforeStats.FileBytes, copyStats.FileBytes, copyStats.RecordCount)
	afterPrimary, err := lib.Primary()
	if err != nil {
		t.Fatalf("Primary on the copy: %v", err)
	}
	afterWorker, err := lib.SubAgent(testLLM(url), memhop.ProfileInput{Name: "worker", Role: "helper"})
	if err != nil {
		t.Fatalf("SubAgent on the copy: %v", err)
	}
	if got := snapshot("primary", afterPrimary); got != beforePrimary {
		t.Fatalf("the compacted copy answers the primary domain differently:\nbefore: %s\nafter:  %s", beforePrimary, got)
	}
	if got := snapshot("worker", afterWorker); got != beforeWorker {
		t.Fatalf("the compacted copy answers the worker domain differently:\nbefore: %s\nafter:  %s", beforeWorker, got)
	}

	// The counters have to have been carried too: a compacted file that reset a scene's
	// turn counter would hand out an id some settled round already owns.
	res, err := afterPrimary.Search(memhop.SearchQuery{SceneID: scenes[0].SceneID})
	if err != nil {
		t.Fatalf("Search on the copy: %v", err)
	}
	// The carried-over counters are the point: an id the compacted file mints must not be
	// one a settled round already owns, which is what a reset turn counter would do.
	if strings.Contains(beforePrimary, res.NewTopicID) {
		t.Fatalf("the compacted copy minted a topic id a settled round already had: %s", res.NewTopicID)
	}
	before, err := afterPrimary.SceneContext(scenes[0].SceneID)
	if err != nil {
		t.Fatalf("SceneContext before the new close: %v", err)
	}
	if _, err := afterPrimary.Update(memhop.TurnEnd{Input: "after compact in",
		Output: "after compact out", Outcome: "answered", CreatedAt: stamp + 1000}); err != nil {
		t.Fatalf("Update on the copy: %v", err)
	}
	after1, err := afterPrimary.SceneContext(scenes[0].SceneID)
	if err != nil {
		t.Fatalf("SceneContext after the new close: %v", err)
	}
	if len(after1.Topics) != len(before.Topics)+1 {
		t.Fatalf("the copy gained %d rows, want exactly 1: before %d after %d",
			len(after1.Topics)-len(before.Topics), len(before.Topics), len(after1.Topics))
	}
	if after1.Topics[len(after1.Topics)-1].TopicID != res.NewTopicID {
		t.Fatalf("the round closed after compaction is not the one that was opened: %s vs %s",
			after1.Topics[len(after1.Topics)-1].TopicID, res.NewTopicID)
	}

	// A second pass, on a file with nothing dead left to move. Checkpointing first is what
	// makes the comparison honest: CompactTo writes its own snapshot, so the numbers only
	// mean the same thing once both files have one.
	if err := lib.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint before the second pass: %v", err)
	}
	live, err := lib.Stats()
	if err != nil {
		t.Fatalf("Stats before the second pass: %v", err)
	}
	second := filepath.Join(dir, "compacted-twice.meh")
	if err := lib.CompactTo(second); err != nil {
		t.Fatalf("second CompactTo: %v", err)
	}
	lib2, err := memhop.Open(second, testLLM(url), memhop.DefaultMemHopDefaults,
		&memhop.ProfileInput{Name: "test-primary", Role: "offline fixture"})
	if err != nil {
		t.Fatalf("Open the twice-compacted file: %v", err)
	}
	defer lib2.Close()
	secondStats, err := lib2.Stats()
	if err != nil {
		t.Fatalf("Stats on the second copy: %v", err)
	}
	if secondStats.RecordCount != live.RecordCount {
		t.Fatalf("the second pass changed the live set: %d vs %d", secondStats.RecordCount, live.RecordCount)
	}
	// Compaction is not a growth machine: with no dead records to reclaim it comes out no
	// larger than the file it read (measured here: about 150 bytes of slack shaved off the
	// tail). This is the relation a host planning capacity has to know, since the pair
	// `Stats` answers is a byte count and a record count — no difference between them is a
	// volume of space.
	if secondStats.FileBytes > live.FileBytes {
		t.Fatalf("a compaction of an already-compacted file grew it: %d -> %d bytes", live.FileBytes, secondStats.FileBytes)
	}
	t.Logf("second pass on the same live set: %d -> %d bytes, %d records",
		live.FileBytes, secondStats.FileBytes, secondStats.RecordCount)
}
