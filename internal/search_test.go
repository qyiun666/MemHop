// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Regression tests for the Search write-path ordering: the L2MetaIndex
// cache must be refreshed before createTopicInScene lists the scene's
// topics, so the newly created topic is part of the returned Contexts.
package internal

import (
	"context"
	"strings"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// TestSearchAutoCreateContextsIncludeNewTopic locks the timing: on an empty
// database the first Search(AutoCreate) must return the just-created topic
// in Contexts. A stale L2MetaIndex (sync after the listing) returns zero
// contexts and panics hosts indexing res.Contexts[0].
func TestSearchAutoCreateContextsIncludeNewTopic(t *testing.T) {
	srv := mockLLMServer(t, `{"keywords":["rust","memory"]}`)
	db := newSearchTestDB(t, srv.URL)

	res, err := db.Search(context.Background(), core.DefaultAgentID, SearchQuery{Text: "hello world", AutoCreate: true, Timestamp: 1000})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.NewTopicID == 0 {
		t.Fatal("Search should have created a topic")
	}
	if len(res.Contexts) != 1 {
		t.Fatalf("Contexts = %d topics, want 1 (the new topic)", len(res.Contexts))
	}
	if res.Contexts[0].ID != res.NewTopicID {
		t.Errorf("Contexts[0].ID = %d, want new topic %d", res.Contexts[0].ID, res.NewTopicID)
	}
	if got := res.Contexts[0].UserKeywords; len(got) != 2 || got[0] != "rust" {
		t.Errorf("Contexts[0].UserKeywords = %v, want [rust memory]", got)
	}
}

// TestSearchReturnsProfileBrief a stored profile shows up as a compact
// digest in ProfileBrief while the full Profile stays available.
func TestSearchReturnsProfileBrief(t *testing.T) {
	srv := mockLLMServer(t, `{"keywords":["rust"]}`)
	db := newSearchTestDB(t, srv.URL)
	profile := core.ProfileSlot{
		Name:        "meow",
		Role:        "helper",
		Personality: "curious",
		Preferences: map[string]string{"lang": "zh", "style": "concise"},
	}
	if err := repo.UpdateProfileL0(db.engine, core.DefaultAgentID, &profile); err != nil {
		t.Fatalf("UpdateProfileL0: %v", err)
	}
	res, err := db.Search(context.Background(), core.DefaultAgentID, SearchQuery{Text: "hello", AutoCreate: true, Timestamp: 1000})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, want := range []string{"name: meow", "role: helper", "personality: curious", "lang=zh", "style=concise"} {
		if !strings.Contains(res.ProfileBrief, want) {
			t.Errorf("ProfileBrief missing %q: %q", want, res.ProfileBrief)
		}
	}
	if res.Profile.Name != "meow" {
		t.Errorf("full Profile must stay intact, got %+v", res.Profile)
	}
}

// TestSearchDirectedContextsIncludeNewTopic covers the directed route: the
// topic created into an existing scene must show up in Contexts too.
func TestSearchDirectedContextsIncludeNewTopic(t *testing.T) {
	srv := mockLLMServer(t, `{"keywords":["rust","memory"]}`)
	db := newSearchTestDB(t, srv.URL)

	scene := core.NewSceneSlot("scene").SceneID
	sceneID := common.FormatHash(scene)
	res, err := db.Search(context.Background(), core.DefaultAgentID, SearchQuery{Text: "hello world", DirectedL2ID: &sceneID, Timestamp: 1000})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Contexts) != 1 {
		t.Fatalf("Contexts = %d topics, want 1 (the new topic)", len(res.Contexts))
	}
	if res.Contexts[0].ID != res.NewTopicID {
		t.Errorf("Contexts[0].ID = %d, want new topic %d", res.Contexts[0].ID, res.NewTopicID)
	}
}

// TestSearchFiltersSceneByL3 locks the SCENEFIND scene-domain filter: a
// Search that carries L3ID backfills the created scene's organizational L3
// domain, and a later retrieval with the same L3ID only returns contexts
// from scenes carrying that domain (SetSceneL3ID + candidateTopics).
func TestSearchFiltersSceneByL3(t *testing.T) {
	srv := mockLLMServer(t, `{"keywords":["rust","memory"]}`)
	db := newSearchTestDB(t, srv.URL)

	// First Search(AutoCreate + L3ID=A) creates and anchors a scene to L3ID=A.
	planA := common.FormatHash(101) // 16 hex
	qA := SearchQuery{Text: "rust ownership", AutoCreate: true, Timestamp: 1000, L3ID: &planA}
	if _, err := db.Search(context.Background(), core.DefaultAgentID, qA); err != nil {
		t.Fatalf("first Search: %v", err)
	}

	// A second Search with a different L3ID=B anchors another (independent) scene.
	planB := common.FormatHash(102)
	qB := SearchQuery{Text: "rust borrow checker", AutoCreate: true, Timestamp: 2000, L3ID: &planB}
	if _, err := db.Search(context.Background(), core.DefaultAgentID, qB); err != nil {
		t.Fatalf("second Search: %v", err)
	}

	// Retrieval scoped to L3ID=A must only surface contexts from that domain.
	res, err := db.Search(context.Background(), core.DefaultAgentID, SearchQuery{Text: "rust ownership", AutoCreate: false, Timestamp: 3000, L3ID: &planA})
	if err != nil {
		t.Fatalf("scoped Search: %v", err)
	}
	if len(res.Contexts) == 0 {
		t.Fatal("L3ID=A should return contexts from scene A")
	}
}

// newSearchTestDB assembles a DB with a mock LLM server and a working
// encoder over a fresh engine; the default-domain context (sparse/L2Meta
// indexes, Dream bookkeeping) is created lazily by contextFor, mirroring
// the Open assembly so background-Dream paths never hit nil state.
func newSearchTestDB(t *testing.T, llmURL string) *DB {
	t.Helper()
	db := newTestDB(t, newTestEngine(t))
	db.llm = New(&MemHopConfig{LLM: LlmConfig{APIURL: llmURL, APIKey: "test", Model: "mock"}})
	db.encoder = &mockEncoder{vec: testVec}
	return db
}
