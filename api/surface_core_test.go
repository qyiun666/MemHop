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

	// One finished turn: its content is appended, then it settles into the topic
	// that read opened.
	if err := settleTurn(db, sceneID, openedTopic, "remember the launch date", "noted, launching next monday"); err != nil {
		t.Fatalf("update: %v", err)
	}

	// The session read returns exactly that turn.
	again, err := db.Search(SearchQuery{SceneID: sceneID})
	if err != nil {
		t.Fatalf("search scene: %v", err)
	}
	if len(again.Topics) != 1 || again.Topics[0].ID != openedTopic {
		t.Fatalf("scene surface = %+v, want the one turn", again.Topics)
	}

	// Guards: an unknown scene is a lookup that misses, not an orphan write.
	ghost := common.FormatHash(common.HashID("ghost-scene"))
	if _, err := db.Search(SearchQuery{SceneID: ghost}); CodeOf(err) != ErrNotFound {
		t.Fatalf("search unknown scene: want ErrNotFound, got %v", err)
	}
	if err := db.Update(ghost, openedTopic); CodeOf(err) != ErrNotFound {
		t.Fatalf("update unknown scene: want ErrNotFound, got %v", err)
	}

	// A turn named by an id the library never issued is refused with
	// ErrInvalidQuery, and Update never sees content it did not read.
	for i, bad := range [][2]string{
		{"not-hex", openedTopic},
		{sceneID, ""},
		{sceneID, "0000000000000000"},
		{sceneID, "not-hex"},
	} {
		if err := db.Update(bad[0], bad[1]); CodeOf(err) != ErrInvalidQuery {
			t.Fatalf("bad turn ids %d: want ErrInvalidQuery, got %v", i, err)
		}
	}

	// What a turn is made of is refused at the append boundary: a record with no
	// content, the library's own consolidation role (named by value here, since it
	// is deliberately not a public constant), an event that never says what
	// happened, and a kind no reader can name.
	for i, bad := range []ArchiveSlot{
		{Kind: KindUtterance, Role: RoleUser, CreatedAt: 1},
		{Kind: KindUtterance, Role: 3, Content: "u", CreatedAt: 1},
		{Kind: KindEvent, Content: "c", CreatedAt: 1},
		{Kind: ArchiveKind(9), EventType: "x", Content: "c", CreatedAt: 1},
	} {
		if err := db.AppendArchive(openedTopic, bad); CodeOf(err) != ErrInvalidQuery {
			t.Fatalf("bad content %d: want ErrInvalidQuery, got %v", i, err)
		}
	}
}
