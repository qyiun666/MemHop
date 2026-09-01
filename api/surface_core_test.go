// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Search / Update / AppendL4Message surface tests.

package api

import (
	"context"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
)

func TestSurfaceSearchUpdateAppend(t *testing.T) {
	db := openSurfaceDB(t)
	ctx := context.Background()

	// An empty SceneID mints a host session and answers with its empty surface.
	res, err := db.Search(SearchQuery{SceneName: "launch planning"})
	if err != nil {
		t.Fatalf("search create: %v", err)
	}
	if !isHexID(res.Scene.SceneID) {
		t.Fatalf("scene id not hex: %q", res.Scene.SceneID)
	}
	if res.Topics == nil {
		t.Fatal("SearchResult.Topics must be non-nil")
	}
	sceneID := res.Scene.SceneID

	// One finished turn becomes one topic; the id the host gets is hex.
	topicID, err := db.Update(TurnUpdate{
		SceneID:   sceneID,
		UserText:  "remember the launch date",
		UserTS:    1_700_000_000_000,
		AgentText: "noted, launching next monday",
		AgentTS:   1_700_000_000_500,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !isHexID(topicID) {
		t.Fatalf("topic id not hex: %q", topicID)
	}

	// The session read returns exactly that turn.
	again, err := db.Search(SearchQuery{SceneID: sceneID})
	if err != nil {
		t.Fatalf("search scene: %v", err)
	}
	if len(again.Topics) != 1 || again.Topics[0].ID != topicID {
		t.Fatalf("scene surface = %+v, want the one turn", again.Topics)
	}
	if again.Scene.HitCount == 0 {
		t.Fatal("reads must fold usage back into the scene record")
	}

	// AppendL4Message returns a new hex archive id on the same topic.
	arcID, err := db.AppendL4Message(topicID, "extra context line", 1_700_000_002_500, RoleAgent, ContentText)
	if err != nil {
		t.Fatalf("append l4: %v", err)
	}
	if !isHexID(arcID) {
		t.Fatalf("archive id not hex: %s", arcID)
	}

	// Guards: an unknown scene and an unknown topic are lookups that miss,
	// not orphan writes.
	ghost := common.FormatHash(common.HashID("ghost-scene"))
	if _, err := db.Search(SearchQuery{SceneID: ghost}); CodeOf(err) != ErrNotFound {
		t.Fatalf("search unknown scene: want ErrNotFound, got %v", err)
	}
	if _, err := db.Update(turnUpdate(ghost, "u", "a")); CodeOf(err) != ErrNotFound {
		t.Fatalf("update unknown scene: want ErrNotFound, got %v", err)
	}
	ghostTopic := common.FormatHash(common.HashID("ghost-topic"))
	if _, err := db.AppendL4Message(ghostTopic, "x", 1_700_000_003_000, RoleUser, ContentText); CodeOf(err) != ErrNotFound {
		t.Fatalf("append to missing topic: want ErrNotFound, got %v", err)
	}

	// Malformed turns are rejected with ErrInvalidQuery.
	badTurns := []TurnUpdate{
		{SceneID: sceneID, UserText: "", UserTS: 1, AgentText: "a", AgentTS: 2},
		{SceneID: sceneID, UserText: "u", UserTS: 0, AgentText: "a", AgentTS: 2},
		{SceneID: sceneID, UserText: "u", UserTS: 5, AgentText: "a", AgentTS: 4},
		{SceneID: "not-hex", UserText: "u", UserTS: 1, AgentText: "a", AgentTS: 2},
	}
	for i, in := range badTurns {
		if _, err := db.Update(in); CodeOf(err) != ErrInvalidQuery {
			t.Fatalf("bad turn %d: want ErrInvalidQuery, got %v", i, err)
		}
	}

	// Refine re-distills from every original of the topic.
	if err := db.RefineTopicKeywords(ctx, topicID); err != nil {
		t.Fatalf("refine: %v", err)
	}
}
