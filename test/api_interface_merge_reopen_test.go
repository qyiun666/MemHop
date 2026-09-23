// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package test

import (
	"path/filepath"
	"strings"
	"testing"

	memhop "github.com/qyiun666/MemHop/api"
)

// Merging rewrites which scene a topic belongs to, so it is the one correction that moves
// records rather than deleting them: the scene id on every moved topic, its keyword track,
// the L4 archives keyed by those topics, and the caches that list them all have to agree.
// The existing merge case checks the live answer, which a cache that was updated while the
// records were not would also satisfy. This one closes the file and reads it back, so the
// reopened truth — rebuilt from records — is what has to match.
func TestInterfaceMergeSurvivesReopen(t *testing.T) {
	llm := newMockLLM(t)
	path := filepath.Join(t.TempDir(), "merge_reopen.meh")
	m := openMockDB(t, path, llm.srv.URL)
	sess, err := m.Primary()
	if err != nil {
		t.Fatalf("Primary: %v", err)
	}

	openA, err := sess.Search(memhop.SearchQuery{NewScene: true})
	if err != nil {
		t.Fatalf("open scene A: %v", err)
	}
	sceneA := openA.Scene.SceneID
	if _, err := turn(sess, "甲的第一书", "答甲一"); err != nil {
		t.Fatalf("A turn 1: %v", err)
	}
	if _, err := sess.Search(memhop.SearchQuery{}); err != nil {
		t.Fatalf("second round in A: %v", err)
	}
	if _, err := turn(sess, "甲的第二书", "答甲二"); err != nil {
		t.Fatalf("A turn 2: %v", err)
	}
	openB, err := sess.Search(memhop.SearchQuery{NewScene: true})
	if err != nil {
		t.Fatalf("open scene B: %v", err)
	}
	sceneB := openB.Scene.SceneID
	if _, err := turn(sess, "乙的第一书", "答乙一"); err != nil {
		t.Fatalf("B turn 1: %v", err)
	}

	if err := sess.MergeScenes(sceneA, []string{sceneB}); err != nil {
		t.Fatalf("MergeScenes: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened := openMockDB(t, path, llm.srv.URL)
	t.Cleanup(func() { _ = reopened.Close() })
	back, err := reopened.Primary()
	if err != nil {
		t.Fatalf("Primary after reopen: %v", err)
	}

	scenes, err := back.ListScenes("")
	if err != nil {
		t.Fatalf("ListScenes after reopen: %v", err)
	}
	if len(scenes) != 1 || scenes[0].SceneID != sceneA {
		t.Fatalf("the reopened file lists %+v, want only the surviving scene %s — a merged-away scene came back", scenes, sceneA)
	}
	if _, err := back.SceneContext(sceneB); memhop.CodeOf(err) != memhop.ErrNotFound {
		t.Fatalf("the merged-away scene reads after reopen: %v", err)
	}

	ctx, err := back.SceneContext(sceneA)
	if err != nil {
		t.Fatalf("SceneContext after reopen: %v", err)
	}
	if len(ctx.Topics) != 3 {
		t.Fatalf("the surviving scene lists %d rows after reopen, want the two it held plus the one merged in: %+v",
			len(ctx.Topics), ctx.Topics)
	}
	var prose []string
	for _, row := range ctx.Topics {
		if len(row.Keywords) == 0 {
			t.Fatalf("a row lost its keyword track across the merge: %+v", row)
		}
		for _, msg := range row.Messages {
			prose = append(prose, msg.Content)
		}
	}
	joined := strings.Join(prose, "|")
	for _, want := range []string{"甲的第一书", "答甲一", "甲的第二书", "答甲二", "乙的第一书", "答乙一"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("the merged transcript lost %q after reopen; it reads %q", want, joined)
		}
	}

	// The moved turn's own archives must still answer to its topic id — that is the address
	// a host got back before the merge and still holds afterwards.
	moved, err := back.SceneContext(sceneA)
	if err != nil {
		t.Fatalf("SceneContext: %v", err)
	}
	var movedID string
	for _, row := range moved.Topics {
		if len(row.Messages) > 0 && row.Messages[0].Content == "乙的第一书" {
			movedID = row.TopicID
		}
	}
	if movedID == "" {
		t.Fatal("the merged-in turn is not in the surviving scene at all")
	}
	archives, err := back.SearchL4(memhop.L4Query{TopicID: &movedID})
	if err != nil {
		t.Fatalf("SearchL4 by the merged topic id: %v", err)
	}
	if len(archives) != 2 {
		t.Fatalf("the merged topic owns %d archives after reopen, want the pair it closed with: %+v", len(archives), archives)
	}
}
