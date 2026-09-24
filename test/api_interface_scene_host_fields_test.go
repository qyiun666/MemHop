// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Two fields on a scene belong to the host and to nobody else: the title it gave the
// conversation (`UpdateScene`) and the project domain it hung the conversation on (the anchor a
// per-project listing reads). Both live on a record the library rewrites for its own reasons —
// opening a turn bumps the counter and stamps the last-used ordering — and a consolidation pass
// rebuilds the mirrors those reads are served from. A rewrite that assembled the record from the
// fields its author cared about would drop a title and an anchor with no error anywhere, and the
// host would notice as an empty name in a listing, or a conversation missing from the project it
// anchored it to. This drives both the record rewrite and the rebuild, and checks both fields
// before and after a restart.

package test

import (
	"context"
	"path/filepath"
	"testing"

	memhop "github.com/qyiun666/MemHop/api"
)

func TestInterfaceHostFieldsOnASceneSurviveTheLibrarysOwnRewrites(t *testing.T) {
	llm := newMockLLM(t)
	path := filepath.Join(t.TempDir(), "scene_fields.meh")
	knobs := func(d *memhop.MemHopDefaults) {
		d.SceneDreamTopicThreshold = -1
		d.DreamCompressMinTopics = 2
	}
	m := openMockDB(t, path, llm.srv.URL, knobs)
	db := newTestDB(t, m)

	// A graph to anchor on, so the anchor is an id a project listing can be checked against
	// rather than a number that names nothing.
	imported, err := db.ImportL3([]memhop.L3ImportItem{
		{Title: "gateway", Domain: "proj", NodeType: "package", Content: "the edge"},
	}, memhop.L3ImportOverwrite)
	if err != nil || len(imported.Errors) != 0 {
		t.Fatalf("seed the project graph: %v %+v", err, imported)
	}
	graphID := imported.GraphIDs[0]

	sceneID := openSession(t, db)
	settleTurn(t, db, sceneID, "用户要求重构代码", "好的,我来重构这段代码")
	settleTurn(t, db, sceneID, "再谈第二件事", "第二件事也定了方案")

	title := "重构那一会话"
	if _, err := db.UpdateScene(sceneID, memhop.ScenePatch{Name: &title, L3ID: &graphID}); err != nil {
		t.Fatalf("UpdateScene: %v", err)
	}

	// The library's own rewrite of that record: opening a turn reads the slot back and stamps
	// two of its fields on the way out. This is where a title would vanish if the write
	// assembled a record from scratch instead of editing what it read.
	settleTurn(t, db, sceneID, "继续重构第二个模块", "第二个模块也补上测试")

	rep, err := db.Dream(context.Background(), "")
	if err != nil {
		t.Fatalf("Dream: %v", err)
	}
	if rep.L2TopicsCompressed == 0 {
		t.Fatalf("no consolidation ran, so this checks nothing about the mirrors it rebuilds: %+v", rep)
	}

	checkFields := func(who string, sess *memhop.Session) {
		scenes, err := sess.ListScenes("")
		if err != nil {
			t.Fatalf("ListScenes (%s): %v", who, err)
		}
		var found *memhop.SceneSlot
		for i := range scenes {
			if scenes[i].SceneID == sceneID {
				found = &scenes[i]
			}
		}
		if found == nil {
			t.Fatalf("after %s the scene the host anchored is not listed at all: %+v", who, scenes)
		}
		if found.SceneName != title || found.L3ID != graphID {
			t.Fatalf("after %s the host's fields read name=%q anchor=%q, want %q / %s",
				who, found.SceneName, found.L3ID, title, graphID)
		}
		byProject, err := sess.ListScenes(graphID)
		if err != nil {
			t.Fatalf("ListScenes by project (%s): %v", who, err)
		}
		if len(byProject) != 1 || byProject[0].SceneID != sceneID {
			t.Fatalf("after %s the project listing holds %d scene(s), want the one anchored there: %+v",
				who, len(byProject), byProject)
		}
		ctx, err := sess.SceneContext(sceneID)
		if err != nil {
			t.Fatalf("SceneContext (%s): %v", who, err)
		}
		if ctx.SceneName != title {
			t.Fatalf("after %s the transcript is titled %q, want the host's %q", who, ctx.SceneName, title)
		}
	}
	checkFields("the consolidation", db.Session)

	// The same answers must come off the file rather than out of the mirrors this process warmed:
	// a title kept only in a cache would read correctly right up to the restart.
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reopened := newTestDB(t, openMockDB(t, path, llm.srv.URL, knobs))
	checkFields("a restart", reopened.Session)
}
