// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Search / Update surface tests: the hot-path turn contract.

package api

import (
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
)

func TestSurfaceTurnFlow(t *testing.T) {
	db := openSurfaceDB(t)

	// An empty SceneID mints a host session, answers with its empty surface and
	// issues the topic id of the turn being opened.
	res, err := db.Search(SearchQuery{})
	if err != nil {
		t.Fatalf("search create: %v", err)
	}
	if !isHexID(res.Scene.SceneID) {
		t.Fatalf("scene id not hex: %q", res.Scene.SceneID)
	}
	if !isHexID(res.NewTopicID) {
		t.Fatalf("issued topic id not hex: %q", res.NewTopicID)
	}
	if res.Topics == nil {
		t.Fatal("SearchResult.Topics must be non-nil")
	}
	sceneID, openedTopic := res.Scene.SceneID, res.NewTopicID

	// One finished turn settles into the topic that read opened.
	topicID, err := db.Update(TurnUpdate{
		SceneID:   sceneID,
		TopicID:   openedTopic,
		UserText:  "remember the launch date",
		UserTS:    1_700_000_000_000,
		AgentText: "noted, launching next monday",
		AgentTS:   1_700_000_000_500,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if topicID != openedTopic {
		t.Fatalf("update settled %q, want the opened topic %q", topicID, openedTopic)
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

	// Guards: an unknown scene is a lookup that misses, not an orphan write.
	ghost := common.FormatHash(common.HashID("ghost-scene"))
	if _, err := db.Search(SearchQuery{SceneID: ghost}); CodeOf(err) != ErrNotFound {
		t.Fatalf("search unknown scene: want ErrNotFound, got %v", err)
	}
	if _, err := db.Update(turnUpdate(ghost, openedTopic, "u", "a")); CodeOf(err) != ErrNotFound {
		t.Fatalf("update unknown scene: want ErrNotFound, got %v", err)
	}

	// Malformed turns are rejected with ErrInvalidQuery.
	badTurns := []TurnUpdate{
		{SceneID: sceneID, TopicID: openedTopic, UserText: "", UserTS: 1, AgentText: "a", AgentTS: 2},
		{SceneID: sceneID, TopicID: openedTopic, UserText: "u", UserTS: 0, AgentText: "a", AgentTS: 2},
		{SceneID: sceneID, TopicID: openedTopic, UserText: "u", UserTS: 5, AgentText: "a", AgentTS: 4},
		{SceneID: "not-hex", TopicID: openedTopic, UserText: "u", UserTS: 1, AgentText: "a", AgentTS: 2},
		{SceneID: sceneID, UserText: "u", UserTS: 1, AgentText: "a", AgentTS: 2},
		{SceneID: sceneID, TopicID: "0000000000000000", UserText: "u", UserTS: 1, AgentText: "a", AgentTS: 2},
		{SceneID: sceneID, TopicID: "not-hex", UserText: "u", UserTS: 1, AgentText: "a", AgentTS: 2},
	}
	for i, in := range badTurns {
		if _, err := db.Update(in); CodeOf(err) != ErrInvalidQuery {
			t.Fatalf("bad turn %d: want ErrInvalidQuery, got %v", i, err)
		}
	}
}
