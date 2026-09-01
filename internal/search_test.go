// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Search is a scene-scoped read: a scene is the host's session, so Search
// neither guesses which scene a message belongs to nor distills anything.
package internal

import (
	"strings"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

func newSearchTestDB(t *testing.T, llmURL string) *DB {
	t.Helper()
	db := newTestDB(t, newTestEngine(t))
	db.llm = New(&MemHopConfig{LLM: LlmConfig{APIURL: llmURL, APIKey: "test", Model: "mock"}})
	return db
}

// An empty SceneID asks for a fresh scene: the record lands on disk, the
// result carries its host-owned id, and the optional name/L3 anchor apply.
func TestSearchCreatesSceneWhenIDEmpty(t *testing.T) {
	srv := mockLLMServer(t, `{"keywords":["unused"]}`)
	db := newSearchTestDB(t, srv.URL)
	l3ID := common.FormatHash(101)

	res, err := db.Search(core.DefaultAgentID, SearchQuery{SceneName: "购物助手", L3ID: l3ID})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.Scene.SceneID == 0 || len(res.Topics) != 0 {
		t.Fatalf("fresh scene must come back empty, got %+v", res)
	}
	slot, err := core.ReadSceneSlot(db.engine, core.DefaultAgentID, res.Scene.SceneID)
	if err != nil {
		t.Fatalf("scene not persisted: %v", err)
	}
	if slot.SceneName != "购物助手" {
		t.Errorf("scene name = %q, want the host-provided one", slot.SceneName)
	}
	wantL3, err := common.ParseID(l3ID)
	if err != nil {
		t.Fatalf("parse l3 id: %v", err)
	}
	if slot.L3ID != wantL3 {
		t.Errorf("scene L3 anchor = %d, want %d", slot.L3ID, wantL3)
	}
}

// Two empty-id Search calls are two sessions: each gets its own scene id.
func TestSearchFreshScenesDoNotCollide(t *testing.T) {
	srv := mockLLMServer(t, `{"keywords":["x"]}`)
	db := newSearchTestDB(t, srv.URL)

	first, err := db.Search(core.DefaultAgentID, SearchQuery{})
	if err != nil {
		t.Fatalf("first Search: %v", err)
	}
	second, err := db.Search(core.DefaultAgentID, SearchQuery{})
	if err != nil {
		t.Fatalf("second Search: %v", err)
	}
	if first.Scene.SceneID == second.Scene.SceneID {
		t.Fatalf("both calls returned scene %d", first.Scene.SceneID)
	}
	if !strings.HasPrefix(second.Scene.SceneName, "session:") {
		t.Errorf("an unnamed scene falls back to session:<id>, got %q", second.Scene.SceneName)
	}
}

// A non-empty SceneID must already exist: the library never creates a scene
// the host did not open, and Update relies on that to reject stray turns.
func TestSearchRejectsUnknownScene(t *testing.T) {
	srv := mockLLMServer(t, `{"keywords":["x"]}`)
	db := newSearchTestDB(t, srv.URL)

	_, err := db.Search(core.DefaultAgentID, SearchQuery{SceneID: common.FormatHash(4242)})
	if common.CodeOf(err) != common.ErrNotFound {
		t.Fatalf("unknown scene err = %v, want ErrNotFound", err)
	}
}

// The read surface is the scene's depth-1 set in turn order; sunk history
// (depth 2+) stays out of the host's context.
func TestSearchReadsSceneSurface(t *testing.T) {
	srv := mockLLMServer(t, `{"keywords":["x"]}`)
	db := newSearchTestDB(t, srv.URL)
	const sceneID = uint64(7)
	mustWriteScene(t, db.engine, core.DefaultAgentID, sceneID, "session")

	writeTopic(t, db.engine, core.DefaultAgentID, newTopic(11, sceneID, 200, []string{"second"}))
	writeTopic(t, db.engine, core.DefaultAgentID, newTopic(12, sceneID, 100, []string{"first"}))
	parent := uint64(11)
	writeTopic(t, db.engine, core.DefaultAgentID, core.TopicSlot{
		ID: 13, SceneID: sceneID, Depth: 2, ParentID: &parent, FusedKeywords: []string{"sunk"},
	})
	writeTopic(t, db.engine, core.DefaultAgentID, newTopic(14, 999, 300, []string{"other scene"}))

	res, err := db.Search(core.DefaultAgentID, SearchQuery{SceneID: common.FormatHash(sceneID)})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Topics) != 2 {
		t.Fatalf("Topics = %d, want the 2 depth-1 topics of this scene: %+v", len(res.Topics), res.Topics)
	}
	if res.Topics[0].ID != 12 || res.Topics[1].ID != 11 {
		t.Errorf("topics not in turn order: %d then %d", res.Topics[0].ID, res.Topics[1].ID)
	}
	if got := res.Topics[0].FusedKeywords; len(got) != 1 || got[0] != "first" {
		t.Errorf("keyword track lost: %v", got)
	}
}

// Search costs no LLM call and writes no memory record: only the scene's
// usage counters move.
func TestSearchIsReadOnlyAndCallsNoLLM(t *testing.T) {
	srv, calls := countingLLMServer(t, `{"keywords":["should not be called"]}`)
	db := newSearchTestDB(t, srv.URL)
	const sceneID = uint64(7)
	mustWriteScene(t, db.engine, core.DefaultAgentID, sceneID, "session")
	writeTopic(t, db.engine, core.DefaultAgentID, newTopic(11, sceneID, 100, []string{"first"}))

	before := [3]int{
		countRecords(db.engine, core.DefaultAgentID, core.RecL2Topic),
		countRecords(db.engine, core.DefaultAgentID, core.RecL2Scene),
		countRecords(db.engine, core.DefaultAgentID, core.RecL4Archive),
	}
	res, err := db.Search(core.DefaultAgentID, SearchQuery{SceneID: common.FormatHash(sceneID)})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("Search made %d LLM calls, want 0", got)
	}
	after := [3]int{
		countRecords(db.engine, core.DefaultAgentID, core.RecL2Topic),
		countRecords(db.engine, core.DefaultAgentID, core.RecL2Scene),
		countRecords(db.engine, core.DefaultAgentID, core.RecL4Archive),
	}
	if after != before {
		t.Fatalf("Search wrote records: before %v after %v", before, after)
	}
	if res.Scene.HitCount != 1 {
		t.Errorf("usage counter should record the read, got %+v", res.Scene)
	}
}

// A stored profile shows up as a compact digest in ProfileBrief while the
// full Profile stays available.
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
	res, err := db.Search(core.DefaultAgentID, SearchQuery{})
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

// Scene.TopicCount is host-visible and must agree with what the same read
// returns: the record itself never stores the count, the read derives it.
func TestSearchReportsSceneTopicCount(t *testing.T) {
	srv := mockLLMServer(t, `{"keywords":["x"]}`)
	db := newSearchTestDB(t, srv.URL)
	const sceneID = uint64(7)
	mustWriteScene(t, db.engine, core.DefaultAgentID, sceneID, "session")
	writeTopic(t, db.engine, core.DefaultAgentID, newTopic(11, sceneID, 100, []string{"first"}))
	writeTopic(t, db.engine, core.DefaultAgentID, newTopic(12, sceneID, 200, []string{"second"}))
	parent := uint64(11)
	writeTopic(t, db.engine, core.DefaultAgentID, core.TopicSlot{
		ID: 13, SceneID: sceneID, Depth: 2, ParentID: &parent, FusedKeywords: []string{"sunk"},
	})

	res, err := db.Search(core.DefaultAgentID, SearchQuery{SceneID: common.FormatHash(sceneID)})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.Scene.TopicCount != 2 {
		t.Fatalf("Scene.TopicCount = %d, want 2 (depth-1 only, sunk topic excluded)", res.Scene.TopicCount)
	}
	scenes, err := db.ListScenes(core.DefaultAgentID)
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	if scenes[0].TopicCount != res.Scene.TopicCount {
		t.Fatalf("ListScenes reports %d but Search reports %d", scenes[0].TopicCount, res.Scene.TopicCount)
	}
}
