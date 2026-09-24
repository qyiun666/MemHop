// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// The scene patch and the topic rename are both partly-idempotent calls: a host
// re-states what it already knows (a confirm read, a retried write). An append-only
// file charges for that by the byte, so a call with nothing to change must not append
// anything — otherwise `UpdateScene(id, ScenePatch{})`, which both guides document as
// the way to confirm a scene's anchor without listing the domain, grows the file once
// per look. The real-change case is measured in the same breath: a test that only proves
// "nothing was written" passes by never writing at all.

package internal

import (
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

func TestNoOpSceneAndTopicWritesAppendNothing(t *testing.T) {
	srv, _ := countingLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)
	res, err := db.Search(core.DefaultAgentID, SearchQuery{})
	if err != nil {
		t.Fatalf("open scene: %v", err)
	}
	appendTurn(t, db, 1000)
	if err := settle(db, res.Scene.SceneID, res.NewTopicID); err != nil {
		t.Fatalf("settle turn: %v", err)
	}
	sceneID := common.FormatHash(res.Scene.SceneID)
	topicID := common.FormatHash(res.NewTopicID)
	named := "the turn about ownership"
	if _, err := db.RenameTopic(core.DefaultAgentID, topicID, named); err != nil {
		t.Fatalf("name the topic: %v", err)
	}

	bytesBefore, recordsBefore, err := db.Stats()
	if err != nil {
		t.Fatalf("stats before: %v", err)
	}
	for i := 0; i < 20; i++ {
		// An empty patch is the documented confirm read; the same name is a retried
		// rename. Neither has anything to change.
		got, err := db.UpdateScene(core.DefaultAgentID, sceneID, ScenePatch{})
		if err != nil {
			t.Fatalf("UpdateScene with an empty patch: %v", err)
		}
		if got.SceneID != res.Scene.SceneID {
			t.Fatalf("the confirm read answered scene %d, want %d", got.SceneID, res.Scene.SceneID)
		}
		if _, err := db.RenameTopic(core.DefaultAgentID, topicID, named); err != nil {
			t.Fatalf("RenameTopic to the name it already carries: %v", err)
		}
	}
	bytesAfter, recordsAfter, err := db.Stats()
	if err != nil {
		t.Fatalf("stats after: %v", err)
	}
	if bytesAfter != bytesBefore || recordsAfter != recordsBefore {
		t.Fatalf("40 no-op calls grew the file: bytes %d -> %d, reachable records %d -> %d",
			bytesBefore, bytesAfter, recordsBefore, recordsAfter)
	}

	// A call that does change something still writes, and the change still reads back.
	title := "learning rust"
	if _, err := db.UpdateScene(core.DefaultAgentID, sceneID, ScenePatch{Name: &title}); err != nil {
		t.Fatalf("rename the scene: %v", err)
	}
	if _, err := db.RenameTopic(core.DefaultAgentID, topicID, "ownership, renamed"); err != nil {
		t.Fatalf("rename the topic: %v", err)
	}
	grown, grownRecords, err := db.Stats()
	if err != nil {
		t.Fatalf("stats after the real writes: %v", err)
	}
	if grown <= bytesAfter {
		t.Fatalf("two real writes appended nothing either: bytes stayed at %d", grown)
	}
	// Rewriting a record the domain already holds moves the index to the newer
	// version instead of adding a reachable one.
	if grownRecords != recordsAfter {
		t.Fatalf("real writes changed the reachable record count: %d -> %d", recordsAfter, grownRecords)
	}
	scenes, err := db.ListScenes(core.DefaultAgentID, "")
	if err != nil {
		t.Fatalf("list scenes: %v", err)
	}
	if len(scenes) != 1 || scenes[0].SceneName != title {
		t.Fatalf("the scene name did not land: %+v", scenes)
	}
	topics, err := db.SceneContext(core.DefaultAgentID, sceneID)
	if err != nil {
		t.Fatalf("scene context: %v", err)
	}
	if len(topics.Topics) != 1 || topics.Topics[0].Name != "ownership, renamed" {
		t.Fatalf("the topic name did not land: %+v", topics.Topics)
	}
}
