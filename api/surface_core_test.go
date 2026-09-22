// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Search / Update surface tests: the hot-path turn contract.

package api

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
)

// Which scene a read continues is the domain's own memory: an un-named read opens its
// next turn on the scene the domain is working, NewScene starts a fresh one, and a
// reopened file restores the current scene from the records — the one whose turn counter
// ran furthest — rather than starting a third. A host carrying no key across calls is
// what this contract makes possible, so it is pinned end to end, through a restart.
func TestSearchContinuesTheDomainScene(t *testing.T) {
	llm := stubLLM()
	t.Cleanup(llm.Close)
	path := filepath.Join(t.TempDir(), "surface.meh")
	m, err := Open(path, surfaceLLM(llm.URL), DefaultMemHopDefaults, surfaceProfile())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	sess, err := m.SubAgent(surfaceLLM(llm.URL), ProfileInput{Name: "surface"})
	if err != nil {
		_ = m.Close()
		t.Fatalf("SubAgent: %v", err)
	}

	// The domain's first read has no scene to continue, so it gets one.
	first, err := sess.Search(SearchQuery{})
	if err != nil {
		t.Fatalf("first search: %v", err)
	}
	if !isHexID(first.Scene.SceneID) {
		t.Fatalf("scene id not hex: %q", first.Scene.SceneID)
	}
	// An un-named read continues that scene, minting its next turn.
	cont, err := sess.Search(SearchQuery{})
	if err != nil {
		t.Fatalf("continue: %v", err)
	}
	if cont.Scene.SceneID != first.Scene.SceneID {
		t.Fatalf("an un-named read started a new scene: %s then %s", first.Scene.SceneID, cont.Scene.SceneID)
	}
	if !isHexID(cont.NewTopicID) || cont.NewTopicID == first.NewTopicID {
		t.Fatalf("continuing the scene minted %q, want a fresh turn after %q", cont.NewTopicID, first.NewTopicID)
	}
	// NewScene is how a host asks for a second conversation.
	fresh, err := sess.Search(SearchQuery{NewScene: true})
	if err != nil {
		t.Fatalf("new scene: %v", err)
	}
	if fresh.Scene.SceneID == first.Scene.SceneID {
		t.Fatalf("NewScene returned the scene it was asked to leave: %s", fresh.Scene.SceneID)
	}

	// The current scene is the records' fact, not the live handle's: reopening the file
	// resumes the scene whose counter ran furthest (here, the first one's two turns) and
	// the turn writes work from that read alone, with nothing carried over.
	if err := m.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	m2, err := Open(path, surfaceLLM(llm.URL), DefaultMemHopDefaults, surfaceProfile())
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = m2.Close() }()
	sess2, err := m2.SubAgent(surfaceLLM(llm.URL), ProfileInput{Name: "surface"})
	if err != nil {
		t.Fatalf("SubAgent after reopen: %v", err)
	}
	resumed, err := sess2.Search(SearchQuery{})
	if err != nil {
		t.Fatalf("search after reopen: %v", err)
	}
	if resumed.Scene.SceneID != first.Scene.SceneID {
		t.Fatalf("a reopened domain resumed %s, want the scene whose turn counter ran furthest (%s)",
			resumed.Scene.SceneID, first.Scene.SceneID)
	}
	topic, err := sess2.Update(TurnEnd{Input: "still the same conversation", Output: "yes", CreatedAt: turnStamp})
	if err != nil {
		t.Fatalf("update on the resumed scene: %v", err)
	}
	if topic.SceneID != first.Scene.SceneID || topic.ID != resumed.NewTopicID {
		t.Fatalf("the close after a restart landed on %+v, want %s's turn %s",
			topic, first.Scene.SceneID, resumed.NewTopicID)
	}
}

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

	// One finished turn: Update names no id and still closes exactly the turn Search
	// opened — the topic it returns is the one that read issued, under the scene it
	// returned, with the distilled track on it.
	closed, err := db.Update(TurnEnd{
		Input: "remember the launch date", Output: "noted, launching next monday", CreatedAt: turnStamp,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if closed.ID != openedTopic || closed.SceneID != sceneID {
		t.Fatalf("Update closed topic %+v, want %s of scene %s", closed, openedTopic, sceneID)
	}
	if len(closed.FusedKeywords) == 0 {
		t.Fatalf("the closed turn carries no keyword track: %+v", closed)
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

	// What a turn is made of is refused at the append boundary: a record with no
	// content, the library's own consolidation role (named by value here, since it
	// is deliberately not a public constant), an event that never says what
	// happened, and a kind no reader can name.
	for i, bad := range []ArchiveInput{
		{Kind: KindUtterance, Role: RoleUser, CreatedAt: 1},
		{Kind: KindUtterance, Role: 3, Content: "u", CreatedAt: 1},
		{Kind: KindEvent, Content: "c", CreatedAt: 1},
		{Kind: ArchiveKind(9), EventType: "x", Content: "c", CreatedAt: 1},
	} {
		if _, err := db.AppendArchive(bad); CodeOf(err) != ErrInvalidQuery {
			t.Fatalf("bad content %d: want ErrInvalidQuery, got %v", i, err)
		}
	}

	// Nothing is written onto a turn the domain does not hold. Deleting the scene takes
	// the turn the last read opened with it, so the five writes on that turn are refused
	// — they cannot fall back to guessing a scene or a turn to write.
	if err := db.DeleteScene(sceneID); err != nil {
		t.Fatalf("delete scene: %v", err)
	}
	for name, write := range noTurnWrites(db) {
		if err := write(); CodeOf(err) != ErrInvalidQuery || !strings.Contains(err.Error(), "no turn is open") {
			t.Fatalf("%s with no turn open: err=%v, want ErrInvalidQuery naming the missing turn", name, err)
		}
	}
}
