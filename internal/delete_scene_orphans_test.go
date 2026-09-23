// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"path/filepath"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// Deleting a session is the correction a host makes when it decides something should never
// have been remembered, so the file has to end up as if that conversation had never
// happened — for every layer a host can read. Checking the live listing alone would miss a
// record the caches stopped showing but the log still holds: it comes back the moment the
// indexes are rebuilt from records, which is also the first thing a restart does.
//
// The comparison is against a second file driven the same way with the deleted scene simply
// never created, so the expectation is arithmetic rather than an enumeration of what the
// cascade is believed to touch. L1 is deliberately out of the comparison: a deleted scene's
// hyperedges are dropped by the next Dream's decay pass, which is documented and is not a
// record any host can read back.
func TestDeleteSceneLeavesNoOrphansInReadableLayers(t *testing.T) {
	srv := mockLLMServer(t, turnKeywords)
	dir := t.TempDir()

	open := func(name string) *DB {
		t.Helper()
		defaults := DefaultMemHopDefaults
		defaults.SceneDreamTopicThreshold = -1
		db, err := OpenDB(filepath.Join(dir, name),
			LlmConfig{APIURL: srv.URL, APIKey: "test", Model: "mock"},
			defaults, primaryProfile("primary"))
		if err != nil {
			t.Fatalf("OpenDB %s: %v", name, err)
		}
		t.Cleanup(func() { _ = db.Close() })
		return db
	}

	// drive settles one scene per entry, each entry saying how many rounds that scene gets.
	drive := func(db *DB, roundsPerScene ...int) []uint64 {
		t.Helper()
		var scenes []uint64
		stamp := int64(1000)
		for _, rounds := range roundsPerScene {
			var sceneID uint64
			for r := 0; r < rounds; r++ {
				res, err := db.Search(core.DefaultAgentID, SearchQuery{NewScene: r == 0})
				if err != nil {
					t.Fatalf("Search: %v", err)
				}
				if r == 0 {
					sceneID = res.Scene.SceneID
				}
				stamp++
				if _, err := db.AppendArchive(core.DefaultAgentID, core.ArchiveSlot{
					TopicID: res.NewTopicID, Kind: core.KindUtterance, Role: core.RoleUser,
					ContentType: core.ContentText, Content: "轮中说的一句", CreatedAt: stamp,
				}); err != nil {
					t.Fatalf("AppendArchive: %v", err)
				}
				if _, err := db.PlanNodeAdd(core.DefaultAgentID, 0, "这一步"); err != nil {
					t.Fatalf("PlanNodeAdd: %v", err)
				}
				if _, err := db.Update(core.DefaultAgentID, core.TurnEnd{Input: "问", Output: "答",
					Outcome: "done", CreatedAt: stamp}); err != nil {
					t.Fatalf("Update: %v", err)
				}
			}
			scenes = append(scenes, sceneID)
		}
		return scenes
	}

	type layers struct{ scenes, topics, archives, planNodes int }

	count := func(db *DB) layers {
		t.Helper()
		s, err := core.CollectAllStrict[core.SceneSlot](db.engine, core.DefaultAgentID, core.RecL2Scene)
		if err != nil {
			t.Fatalf("collect scenes: %v", err)
		}
		tp, err := core.CollectAllTopicsStrict(db.engine, core.DefaultAgentID)
		if err != nil {
			t.Fatalf("collect topics: %v", err)
		}
		ar, err := core.CollectAllStrict[core.ArchiveSlot](db.engine, core.DefaultAgentID, core.RecL4Archive)
		if err != nil {
			t.Fatalf("collect archives: %v", err)
		}
		pn, err := core.CollectAllPlanNodesStrict(db.engine, core.DefaultAgentID)
		if err != nil {
			t.Fatalf("collect plan nodes: %v", err)
		}
		return layers{len(s), len(tp), len(ar), len(pn)}
	}

	// X: two scenes, the first of two rounds; then the correction.
	x := open("x.meh")
	xScenes := drive(x, 2, 1)
	if err := x.DeleteScene(core.DefaultAgentID, common.FormatHash(xScenes[0])); err != nil {
		t.Fatalf("DeleteScene: %v", err)
	}

	// Y: the file that never had the deleted scene at all.
	y := open("y.meh")
	drive(y, 1)

	// Reopen both: the answer that matters is the one rebuilt from records.
	if err := x.Close(); err != nil {
		t.Fatalf("close x: %v", err)
	}
	if err := y.Close(); err != nil {
		t.Fatalf("close y: %v", err)
	}
	x = open("x.meh")
	y = open("y.meh")

	got, want := count(x), count(y)
	if got != want {
		t.Fatalf("after deleting a scene the file still holds different counts than one that never had it: "+
			"deleted-file {scenes %d topics %d archives %d planNodes %d} vs never-had {scenes %d topics %d archives %d planNodes %d}",
			got.scenes, got.topics, got.archives, got.planNodes,
			want.scenes, want.topics, want.archives, want.planNodes)
	}
	if want.topics == 0 || want.archives == 0 || want.planNodes == 0 {
		t.Fatalf("the comparison file holds nothing to compare: %+v", want)
	}
	if listed, err := x.ListScenes(core.DefaultAgentID, ""); err != nil || len(listed) != 1 {
		t.Fatalf("the surviving file lists %+v scenes, want exactly one (err %v)", listed, err)
	}
}
