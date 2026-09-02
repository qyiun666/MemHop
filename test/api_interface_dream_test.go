// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Offline interface tests for Dream consolidation and checkpoint persistence.

package test

import (
	"context"
	"path/filepath"
	"testing"

	memhop "github.com/qyiun666/MemHop/api"
	internal "github.com/qyiun666/MemHop/internal"
)

func TestInterfaceDream(t *testing.T) {
	// Lower the compress threshold so two turns in one session trigger the
	// consolidate call.
	llm := newMockLLM(t)
	m := openMockMulti(t, filepath.Join(t.TempDir(), "test.meh"), llm.srv.URL,
		func(d *internal.MemHopDefaults) { d.DreamCompressMinTopics = 2 })
	db := newTestDB(t, m)
	defer db.Close()

	sceneID := openSession(t, db)
	if _, err := db.Update(turn(sceneID, openTurn(t, db, sceneID), "用户要求重构代码", "好的,我来重构这段代码")); err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	if _, err := db.Update(turn(sceneID, openTurn(t, db, sceneID), "继续重构第二个模块", "第二个模块也补上测试")); err != nil {
		t.Fatalf("turn 2: %v", err)
	}

	rep, err := db.Dream(context.Background(), "")
	if err != nil {
		t.Fatalf("Dream: %v", err)
	}
	if rep == nil || rep.ConsolidatedScenes < 1 || rep.L2TopicsCompressed < 1 {
		t.Fatalf("Dream should consolidate the session: %+v", rep)
	}
	if llm.calls["consolidate"] < 1 {
		t.Fatal("Dream should call consolidate on the session")
	}
	// Consolidation fuses the group into one depth-1 topic, so the read
	// surface shrinks below the two turns written.
	res, err := db.Search(memhop.SearchQuery{SceneID: sceneID})
	if err != nil {
		t.Fatalf("Search after dream: %v", err)
	}
	if len(res.Topics) != 1 || len(res.Topics[0].ChildrenIDs) == 0 {
		t.Fatalf("surface = %+v, want one fused topic owning the turns", res.Topics)
	}
	// Stage timeline covers the full pipeline, distillation included.
	if len(rep.Stages) == 0 {
		t.Fatal("Dream report must carry stage records")
	}
	for _, st := range []string{"l2_compress", "l0_distill"} {
		found := false
		for _, s := range rep.Stages {
			if s.Name == st && s.Status == "ok" {
				found = true
			}
		}
		if !found {
			t.Fatalf("report missing ok stage %s: %+v", st, rep.Stages)
		}
	}
	// L1 nodes were synced from L2 during Dream, so the distill stage runs.
	if llm.calls["distill"] < 1 {
		t.Fatal("Dream should call distill for L0 profile")
	}
	if !rep.L0Updated {
		t.Fatalf("distill ran and merged; L0Updated must hold: %+v", rep)
	}
	// Distill output was merged into the L0 profile.
	profile, err := db.GetL0()
	if err != nil {
		t.Fatalf("GetL0 after dream: %v", err)
	}
	if profile.MBTI.Type == "" || profile.Personality == "" {
		t.Fatalf("dream distill should backfill emotion/mbti: %+v", profile)
	}

	// Directed Dream: an invalid scene id is rejected, a valid one succeeds.
	if _, err := db.Dream(context.Background(), "zz"); err == nil {
		t.Fatal("Dream with invalid scene_id should error")
	}
	if _, err := db.Dream(context.Background(), sceneID); err != nil {
		t.Fatalf("directed Dream on scene %s: err=%v", sceneID, err)
	}
}

func TestInterfaceCheckpointPersist(t *testing.T) {
	llm := newMockLLM(t)
	path := filepath.Join(t.TempDir(), "persist.meh")
	m := openMockMulti(t, path, llm.srv.URL)

	db := newTestDB(t, m)
	sceneID := openSession(t, db)
	topicID, err := db.Update(turn(sceneID, openTurn(t, db, sceneID), "用户要求重构代码", "好的,我来重构这段代码"))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := db.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen the same file: the tenant registry persists, so the same name
	// resolves to the same domain, and the caches rebuild from the records.
	m2 := openMockMulti(t, path, llm.srv.URL)
	defer m2.Close()
	db2 := newTestDB(t, m2)
	scenes, err := db2.ListScenes()
	if err != nil {
		t.Fatalf("ListScenes after reopen: %v", err)
	}
	if len(scenes) == 0 {
		t.Fatal("scenes should persist across reopen")
	}
	res, err := db2.Search(memhop.SearchQuery{SceneID: sceneID})
	if err != nil {
		t.Fatalf("Search after reopen: %v", err)
	}
	if len(res.Topics) != 1 || res.Topics[0].ID != topicID {
		t.Fatalf("topics did not persist: %+v", res.Topics)
	}
	if len(res.Topics[0].FusedKeywords) == 0 {
		t.Fatal("the keyword track must persist with the topic")
	}
	arcs, err := db2.SearchL4(internal.L4Query{Keyword: "重构"})
	if err != nil {
		t.Fatalf("SearchL4 after reopen: %v", err)
	}
	if len(arcs) == 0 {
		t.Fatal("archives should persist across reopen")
	}
	// Reading the host's own session id back from the reopened file is what
	// proves the id survives a restart.
}
