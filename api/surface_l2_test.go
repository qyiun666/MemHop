// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L2 scene listing / context / merge surface tests.

package api

import (
	"testing"
)

func TestSurfaceL2Scenes(t *testing.T) {
	db := openSurfaceDB(t)

	first, err := db.Search(SearchQuery{SceneName: "scene one"})
	if err != nil {
		t.Fatalf("search scene one: %v", err)
	}
	second, err := db.Search(SearchQuery{SceneName: "scene two"})
	if err != nil {
		t.Fatalf("search scene two: %v", err)
	}
	// A scene with content, so the context view has something to render.
	if _, err := db.Update(turnUpdate(first.Scene.SceneID, "scene one topic", "noted")); err != nil {
		t.Fatalf("update: %v", err)
	}

	scenes, err := db.ListScenes()
	if err != nil || len(scenes) < 2 {
		t.Fatalf("want >=2 scenes, got %d err=%v", len(scenes), err)
	}
	sc, err := db.SceneContext(first.Scene.SceneID)
	if err != nil || sc == nil || sc.Topics == nil {
		t.Fatalf("scene context: %v", err)
	}
	// DeleteTopic removes a single topic subtree.
	if len(sc.Topics) > 0 {
		if err := db.DeleteTopic(sc.Topics[0].TopicID); err != nil {
			t.Fatalf("delete topic: %v", err)
		}
	}
	// Merge primary + secondary.
	primary := first.Scene.SceneID
	if err := db.MergeScenes(primary, []string{second.Scene.SceneID}); err != nil {
		t.Fatalf("merge scenes: %v", err)
	}
	// Merging a scene into itself as secondary must be rejected.
	if err := db.MergeScenes(primary, []string{primary}); CodeOf(err) != ErrInvalidQuery {
		t.Fatalf("self-merge: want ErrInvalidQuery, got %v", err)
	}
	// The merged-away session no longer resolves; a host reading it is told so.
	if _, err := db.Search(SearchQuery{SceneID: second.Scene.SceneID}); CodeOf(err) != ErrNotFound {
		t.Fatalf("search merged-away scene: want ErrNotFound, got %v", err)
	}
	if err := db.DeleteScene(primary); err != nil {
		t.Fatalf("delete scene: %v", err)
	}
	if err := db.DeleteScene(primary); err == nil {
		t.Fatal("deleting an absent scene must error")
	}
}
