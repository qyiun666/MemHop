// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L2 scene listing / context / merge surface tests.

package api

import (
	"testing"
)

func TestSurfaceL2Scenes(t *testing.T) {
	db := openSurfaceDB(t)

	first, err := db.Search(SearchQuery{})
	if err != nil {
		t.Fatalf("search scene one: %v", err)
	}
	// A second conversation is asked for, not inferred: an un-named read continues
	// the domain's current scene.
	second, err := db.Search(SearchQuery{NewScene: true})
	if err != nil {
		t.Fatalf("search scene two: %v", err)
	}
	// A scene with content, so the context view has something to render.
	if _, err := settleTurn(db, "scene one topic", "noted"); err != nil {
		t.Fatalf("settle: %v", err)
	}

	scenes, err := db.ListScenes("")
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

// The scene read is served out of a cache, so writing the record alone would
// leave a new name invisible until the next consolidation rebuilt the index.
// This is the guard on that mirroring: both reads have to see the name at once.
func TestSurfaceRenameTopicIsVisibleOnTheReadPath(t *testing.T) {
	db := openSurfaceDB(t)
	res, err := db.Search(SearchQuery{})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if _, err := settleTurn(db, "把 L5 让给计划树", "好"); err != nil {
		t.Fatalf("settle turn: %v", err)
	}

	const want = "决定把 L5 让给计划树的那一轮"
	got, err := db.RenameTopic(res.NewTopicID, want)
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if got.Name != want || got.ID != res.NewTopicID {
		t.Fatalf("written topic = %+v, want %s named %q", got, res.NewTopicID, want)
	}
	if len(got.FusedKeywords) == 0 {
		t.Fatal("the rename disturbed the keyword track")
	}

	ctx, err := db.SceneContext(res.Scene.SceneID)
	if err != nil {
		t.Fatalf("scene context: %v", err)
	}
	if len(ctx.Topics) != 1 || ctx.Topics[0].Name != want {
		t.Fatalf("scene context did not pick the name up: %+v", ctx.Topics)
	}
	// This read opens another turn, so the surface holds two topics; the renamed
	// one is found by id.
	surf, err := db.Search(SearchQuery{SceneID: res.Scene.SceneID})
	if err != nil {
		t.Fatalf("search again: %v", err)
	}
	named := false
	for _, topic := range surf.Topics {
		if topic.ID == res.NewTopicID {
			named = topic.Name == want
		}
	}
	if !named {
		t.Fatalf("the scene surface did not pick the name up: %+v", surf.Topics)
	}
}

// Empty is the absence of a name rather than one: a topic is created unnamed and
// stays so until somebody names it, so clearing it adds nothing. An unknown topic
// is reported instead of being invented, and a malformed id never reaches storage.
func TestSurfaceRenameTopicRefusals(t *testing.T) {
	db := openSurfaceDB(t)
	res, err := db.Search(SearchQuery{})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if _, err := db.RenameTopic(res.NewTopicID, ""); CodeOf(err) != ErrInvalidQuery {
		t.Fatalf("empty name: want ErrInvalidQuery, got %v", err)
	}
	if _, err := db.RenameTopic("0000000000000001", "x"); CodeOf(err) != ErrNotFound {
		t.Fatalf("unknown topic: want ErrNotFound, got %v", err)
	}
	if _, err := db.RenameTopic("not-hex", "x"); CodeOf(err) != ErrInvalidQuery {
		t.Fatalf("malformed topic id: want ErrInvalidQuery, got %v", err)
	}
}

// The transcript read can name nothing at all: which scene this domain is working is
// the library's own memory, so a loop that recalls between its rounds carries no id and
// opens no turn to do it. A domain that has never been read answers with an empty
// transcript — "nothing has been said here yet" is a fact, not a failure, and a recall
// port whose error terminates the round needs no special case for it. Starting a
// conversation is still what the read that opens a turn does, and a scene the host *names*
// that is not there is still ErrNotFound.
func TestSurfaceSceneReadNeedsNoId(t *testing.T) {
	db := openSurfaceDB(t)

	unread, err := db.SceneContext("")
	if err != nil {
		t.Fatalf(`SceneContext("") on an unread domain: %v`, err)
	}
	if len(unread.Topics) != 0 || unread.SceneName != "" {
		t.Fatalf("an unread domain answered %+v, want an empty transcript with no scene named", unread)
	}
	// The empty answer belongs to the read that named nothing; a named id that is not a
	// scene is still a miss, and 0 is a scene id no library ever issued.
	if _, err := db.SceneContext("0000000000000000"); CodeOf(err) != ErrNotFound {
		t.Fatalf("a named scene that is not there: err = %v, want ErrNotFound", err)
	}
	res, err := db.Search(SearchQuery{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	blind, err := db.SceneContext("")
	if err != nil {
		t.Fatalf(`SceneContext(""): %v`, err)
	}
	named, err := db.SceneContext(res.Scene.SceneID)
	if err != nil {
		t.Fatalf("SceneContext(named): %v", err)
	}
	if blind.SceneName != named.SceneName || len(blind.Topics) != len(named.Topics) {
		t.Fatalf("the un-named read answered %+v, want the scene the named one answered (%+v)",
			blind, named)
	}
}
