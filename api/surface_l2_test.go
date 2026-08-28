// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L2 scene listing / context / merge surface tests.

package api

import (
	"context"
	"testing"
)

func TestSurfaceL2Scenes(t *testing.T) {
	db := openSurfaceDB(t)
	ctx := context.Background()
	_, _ = db.Search(ctx, SearchQuery{Text: "scene one topic", AutoCreate: true, Timestamp: 1_700_000_010_000})
	_, _ = db.Search(ctx, SearchQuery{Text: "scene two topic", AutoCreate: true, Timestamp: 1_700_000_020_000})

	scenes, err := db.ListScenes()
	if err != nil || len(scenes) < 2 {
		t.Fatalf("want >=2 scenes, got %d err=%v", len(scenes), err)
	}
	// ActiveSceneIDs returns hex ids consistent with SceneContext input.
	active := db.ActiveSceneIDs()
	for _, id := range active {
		if !isHexID(id) {
			t.Fatalf("active scene id not hex: %q", id)
		}
	}
	sc, err := db.SceneContext(scenes[0].SceneID)
	if err != nil || sc == nil || sc.Topics == nil {
		t.Fatalf("scene context: %v", err)
	}
	// DeleteTopic removes a single topic subtree; a fresh search mints one.
	if len(sc.Topics) > 0 {
		if err := db.DeleteTopic(sc.Topics[0].TopicID); err != nil {
			t.Fatalf("delete topic: %v", err)
		}
	}
	// Merge primary + secondary.
	primary := scenes[0].SceneID
	secondary := scenes[1].SceneID
	if err := db.MergeScenes(primary, []string{secondary}); err != nil {
		t.Fatalf("merge scenes: %v", err)
	}
	// Merging a scene into itself as secondary must be rejected.
	if err := db.MergeScenes(primary, []string{primary}); CodeOf(err) != ErrInvalidQuery {
		t.Fatalf("self-merge: want ErrInvalidQuery, got %v", err)
	}
	// Delete the merged scene, then a missing scene must error.
	if err := db.DeleteScene(primary); err != nil {
		t.Fatalf("delete scene: %v", err)
	}
	if err := db.DeleteScene(primary); err == nil {
		t.Fatal("deleting an absent scene must error")
	}
}
