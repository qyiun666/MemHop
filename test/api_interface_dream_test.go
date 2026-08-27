// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Offline interface tests: exercise the public API surface through
// memhop.OpenMultiWithEncoder with a mock encoder and a mock OpenAI-compatible
// LLM server. No external services required; run with `go test ./test/...`.

package test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	internal "github.com/qyiun666/MemHop/internal"
	"github.com/qyiun666/MemHop/internal/common"
)

func TestInterfaceDream(t *testing.T) {
	// Lower the compress threshold so two topics in one scene trigger the
	// consolidate call.
	llm := newMockLLM(t)
	m := openMockMulti(t, filepath.Join(t.TempDir(), "test.meh"), llm.srv.URL,
		func(d *internal.MemHopDefaults) { d.DreamCompressMinTopics = 2 })
	db := newTestDB(t, m)
	defer db.Close()

	ts := time.Now().UnixMilli()
	res, err := db.Search(context.Background(), internal.SearchQuery{Text: "用户要求重构代码", AutoCreate: true, Timestamp: ts})
	if err != nil {
		t.Fatalf("Search #1: %v", err)
	}
	sceneID := common.FormatHash(res.Contexts[0].SceneID)
	if _, err := db.Search(context.Background(), internal.SearchQuery{
		Text: "继续重构第二个模块", DirectedL2ID: &sceneID, Timestamp: ts + 1000,
	}); err != nil {
		t.Fatalf("Search #2: %v", err)
	}

	rep, err := db.Dream(context.Background(), "")
	if err != nil {
		t.Fatalf("Dream: %v", err)
	}
	if rep == nil || rep.ConsolidatedScenes < 1 || rep.L2TopicsCompressed < 1 {
		t.Fatalf("Dream should consolidate the active scene: %+v", rep)
	}
	if llm.calls["consolidate"] < 1 {
		t.Fatal("Dream should call consolidate on the active scene")
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
	if profile.EmotionPatterns["valence"] == "" || profile.Personality == "" {
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
	if _, err := db.Search(context.Background(), internal.SearchQuery{
		Text: "用户要求重构代码", AutoCreate: true, Timestamp: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if err := db.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen the same file: the tenant registry persists, so the same
	// name resolves to the same domain and its scenes/archives survive.
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
	arcs, err := db2.SearchL4(internal.L4Query{Keyword: "重构"})
	if err != nil {
		t.Fatalf("SearchL4 after reopen: %v", err)
	}
	if len(arcs) == 0 {
		t.Fatal("archives should persist across reopen")
	}
}
