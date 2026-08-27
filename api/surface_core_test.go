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
	db, _ := openSurfaceDB(t)
	ctx := context.Background()

	// AutoCreate path deterministically mints a scene + topic.
	res, err := db.Search(ctx, SearchQuery{Text: "remember the launch date", AutoCreate: true, Timestamp: 1_700_000_000_000})
	if err != nil {
		t.Fatalf("search autocreate: %v", err)
	}
	if res.NewTopicID == 0 {
		t.Fatal("autocreate search must return a new topic id")
	}
	if res.Contexts == nil {
		t.Fatal("SearchResult.Contexts must be non-nil")
	}
	topicID := common.FormatHash(res.NewTopicID)
	if !isHexID(topicID) {
		t.Fatalf("topic id not hex: %q", topicID)
	}

	// Update appends the agent reply to the freshly created topic.
	if err := db.Update(topicID, "noted, launching next monday", 1_700_000_000_500); err != nil {
		t.Fatalf("update: %v", err)
	}
	// Normal retrieval path over a populated domain.
	if _, err := db.Search(ctx, SearchQuery{Text: "launch date", Timestamp: 1_700_000_001_000}); err != nil {
		t.Fatalf("search normal: %v", err)
	}
	// Directed search into an existing scene.
	scenes, err := db.ListScenes()
	if err != nil || len(scenes) == 0 {
		t.Fatalf("list scenes after writes: %d err=%v", len(scenes), err)
	}
	sceneID := common.FormatHash(scenes[0].SceneID)
	if _, err := db.Search(ctx, SearchQuery{Text: "monday", DirectedL2ID: &sceneID, Timestamp: 1_700_000_002_000}); err != nil {
		t.Fatalf("search directed: %v", err)
	}

	// AppendL4Message returns a new hex archive id.
	arcID, err := db.AppendL4Message(topicID, "extra context line", 1_700_000_002_500, 1)
	if err != nil {
		t.Fatalf("append l4: %v", err)
	}
	if !isHexID(common.FormatHash(arcID)) {
		t.Fatalf("archive id not hex: %d", arcID)
	}
	// Guard: unknown topic must be ErrNotFound, not an orphan write.
	ghost := common.FormatHash(common.HashID("ghost-topic"))
	if _, err := db.AppendL4Message(ghost, "x", 1_700_000_003_000, 0); CodeOf(err) != ErrNotFound {
		t.Fatalf("append to missing topic: want ErrNotFound, got %v", err)
	}
	// Timestamp validation contract.
	if _, err := db.Search(ctx, SearchQuery{Text: "no ts"}); CodeOf(err) != ErrInvalidQuery {
		t.Fatalf("search missing timestamp: want ErrInvalidQuery, got %v", err)
	}

	// RefineTopicKeywords is a guarded no-op on a topic without >2 messages.
	if err := db.RefineTopicKeywords(ctx, topicID); err != nil {
		t.Fatalf("refine (guarded no-op): %v", err)
	}
}
