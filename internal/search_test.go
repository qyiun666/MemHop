// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Search is a scene-scoped read: a scene is the host's session, so Search
// neither guesses which scene a message belongs to nor distills anything.
package internal

import (
	"strings"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/llm"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

func newSearchTestDB(t *testing.T, llmURL string) *DB {
	t.Helper()
	db := newTestDB(t, newTestEngine(t))
	db.llm = llm.New(LlmConfig{APIURL: llmURL, APIKey: "test", Model: "mock"})
	return db
}

// A domain with no scene yet gets its first one on an empty SceneID: the record
// lands on disk under a library-generated name, and the optional L3 anchor applies.
func TestSearchCreatesSceneWhenIDEmpty(t *testing.T) {
	srv := mockLLMServer(t, `{"keywords":["unused"]}`)
	db := newSearchTestDB(t, srv.URL)
	// The anchor must be a project domain that exists; a dangling one is rejected.
	// Graphs live in the file-wide shared L3 domain.
	if _, err := repo.EnsureGraphL3(db.engine, core.SharedPoolAgentID, "proj-anchor"); err != nil {
		t.Fatalf("create graph: %v", err)
	}
	l3ID := common.FormatHash(common.HashID("proj-anchor"))

	res, err := db.Search(core.DefaultAgentID, SearchQuery{L3ID: l3ID})
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
	if want := "session:" + common.FormatHash(res.Scene.SceneID); slot.SceneName != want {
		t.Errorf("scene name = %q, want the library-generated %q", slot.SceneName, want)
	}
	wantL3, err := common.ParseID(l3ID)
	if err != nil {
		t.Fatalf("parse l3 id: %v", err)
	}
	if slot.L3ID != wantL3 {
		t.Errorf("scene L3 anchor = %d, want %d", slot.L3ID, wantL3)
	}
}

// An empty SceneID continues the domain's own session — the read that starts one is
// the one that opens it — and NewScene is the only way to start another. When the
// domain no longer remembers which scene it was on, the records answer: the scene
// whose turn counter ran furthest, not the one created last.
func TestSearchContinuesItsSceneUnlessAskedForANewOne(t *testing.T) {
	srv := mockLLMServer(t, `{"keywords":["x"]}`)
	db := newSearchTestDB(t, srv.URL)

	first, err := db.Search(core.DefaultAgentID, SearchQuery{})
	if err != nil {
		t.Fatalf("first Search: %v", err)
	}
	again, err := db.Search(core.DefaultAgentID, SearchQuery{})
	if err != nil {
		t.Fatalf("second Search: %v", err)
	}
	if again.Scene.SceneID != first.Scene.SceneID {
		t.Fatalf("an empty SceneID opened a new session: %d, want the domain's own %d",
			again.Scene.SceneID, first.Scene.SceneID)
	}
	if again.NewTopicID == first.NewTopicID {
		t.Fatal("continuing a session reopened the same turn")
	}

	second, err := db.Search(core.DefaultAgentID, SearchQuery{NewScene: true})
	if err != nil {
		t.Fatalf("NewScene Search: %v", err)
	}
	if second.Scene.SceneID == first.Scene.SceneID {
		t.Fatalf("NewScene returned the scene already in use: %d", second.Scene.SceneID)
	}
	if !strings.HasPrefix(second.Scene.SceneName, "session:") {
		t.Errorf("a created scene falls back to session:<id>, got %q", second.Scene.SceneName)
	}

	// Bring the first session's counter ahead of the new one's, then drop the
	// context: the read that follows has nothing to continue from but the records,
	// and it must not pick the scene that was created last.
	if _, err := db.Search(core.DefaultAgentID, SearchQuery{SceneID: common.FormatHash(first.Scene.SceneID)}); err != nil {
		t.Fatalf("named Search: %v", err)
	}
	delete(db.agents, core.DefaultAgentID)
	resumed, err := db.Search(core.DefaultAgentID, SearchQuery{})
	if err != nil {
		t.Fatalf("Search after the context was dropped: %v", err)
	}
	if resumed.Scene.SceneID != first.Scene.SceneID {
		t.Fatalf("the read resumed scene %d, want the one whose counter ran furthest (%d)",
			resumed.Scene.SceneID, first.Scene.SceneID)
	}
	if want := core.ComputeTurnTopicID(first.Scene.SceneID, 4); resumed.NewTopicID != want {
		t.Fatalf("the resumed read issued %d, want the scene's next turn (%d): turns 1, 2 and 3 "+
			"were opened above", resumed.NewTopicID, want)
	}
}

// A non-empty SceneID must already exist: the library never creates a scene
// the host did not open, and Settle relies on that to reject stray turns.
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

// Search costs no LLM call and writes no memory record: the scene record is the
// only thing it touches, and only to open a turn.
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
	if res.Scene.TurnSeq != 1 {
		t.Errorf("read must open one turn, got %+v", res.Scene)
	}
}

// Each read opens exactly one turn: the topic id it hands back comes from the
// scene's own turn counter, so reopens advance it and never repeat it. Opening
// a turn mints no record — the surface stays as it was until Settle distills.
func TestSearchOpensOneTurnPerRead(t *testing.T) {
	srv := mockLLMServer(t, `{"keywords":["x"]}`)
	db := newSearchTestDB(t, srv.URL)

	first, err := db.Search(core.DefaultAgentID, SearchQuery{})
	if err != nil {
		t.Fatalf("first Search: %v", err)
	}
	if want := core.ComputeTurnTopicID(first.Scene.SceneID, 1); first.NewTopicID != want {
		t.Fatalf("a fresh scene must open turn 1: got %d, want %d", first.NewTopicID, want)
	}
	second, err := db.Search(core.DefaultAgentID, SearchQuery{SceneID: common.FormatHash(first.Scene.SceneID)})
	if err != nil {
		t.Fatalf("second Search: %v", err)
	}
	if second.NewTopicID == first.NewTopicID {
		t.Fatal("two reads of one scene issued the same turn topic")
	}
	if len(second.Topics) != 0 || second.Scene.TurnSeq != 2 {
		t.Fatalf("opening a turn must not create a topic, got %+v", second)
	}

	// Another session counts its own turns from one; reopening the first scene
	// continues where it left off.
	other, err := db.Search(core.DefaultAgentID, SearchQuery{NewScene: true})
	if err != nil {
		t.Fatalf("other scene: %v", err)
	}
	if other.NewTopicID != core.ComputeTurnTopicID(other.Scene.SceneID, 1) {
		t.Fatal("a new scene must start its own turn counter")
	}
	reopened, err := db.Search(core.DefaultAgentID, SearchQuery{SceneID: common.FormatHash(first.Scene.SceneID)})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if reopened.NewTopicID != core.ComputeTurnTopicID(first.Scene.SceneID, 3) {
		t.Fatalf("reopen must continue the counter, got %d", reopened.NewTopicID)
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

// A dangling anchor is refused before the scene exists. The fresh scene id is
// minted inside this call and never handed back on the refusal path, so a scene
// written first would stay on disk as a listing entry the host cannot name,
// re-anchor, or delete.
func TestSearchRefusesAnUnknownAnchorWithoutLeavingAScene(t *testing.T) {
	srv := mockLLMServer(t, `{"keywords":["unused"]}`)
	db := newSearchTestDB(t, srv.URL)

	dangling := common.FormatHash(common.HashID("proj-never-created"))
	if _, err := db.Search(core.DefaultAgentID, SearchQuery{L3ID: dangling}); common.CodeOf(err) != common.ErrNotFound {
		t.Fatalf("an unresolvable anchor must be reported, got %v", err)
	}
	if n := countRecords(db.engine, core.DefaultAgentID, core.RecL2Scene); n != 0 {
		t.Fatalf("the refusal left %d scene records behind", n)
	}
}

// Naming a scene that already exists together with an anchor is a mistake about
// when anchors are set, and the refusal says so on the argument alone: looking
// the named graph up would turn "this scene is already here" into a not-found
// (3001) about a record the host never asked to read, and would reach into the
// file-wide shared pool inside the caller's domain lock to say nothing new.
func TestSearchRefusesAnAnchorOnAnExistingSceneWithoutLookingItUp(t *testing.T) {
	srv := mockLLMServer(t, `{"keywords":["unused"]}`)
	db := newSearchTestDB(t, srv.URL)

	opened, err := db.Search(core.DefaultAgentID, SearchQuery{})
	if err != nil {
		t.Fatalf("open a scene: %v", err)
	}
	gone := common.FormatHash(common.HashID("proj-deleted-after-this-scene-opened"))
	_, err = db.Search(core.DefaultAgentID, SearchQuery{
		SceneID: common.FormatHash(opened.Scene.SceneID),
		L3ID:    gone,
	})
	if common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("an anchor on an existing scene must be refused as a bad query, got %v", err)
	}
	if !strings.Contains(err.Error(), common.FormatHash(opened.Scene.SceneID)) {
		t.Fatalf("the refusal must name the scene the host held: %v", err)
	}
}

// The same rule on the path where the host names no scene: an anchor handed in
// while the domain is already mid-conversation is refused on the argument alone,
// and the message says what to do instead. The anchor here does not resolve, so a
// lookup-first implementation would report a missing graph (3001) about a record
// the host never asked to read. The refusal must also not be the continue path
// breaking: the same read without an anchor stays on this scene.
func TestSearchRefusesAnAnchorWhileContinuingItsScene(t *testing.T) {
	srv := mockLLMServer(t, `{"keywords":["unused"]}`)
	db := newSearchTestDB(t, srv.URL)

	first, err := db.Search(core.DefaultAgentID, SearchQuery{})
	if err != nil {
		t.Fatalf("open a scene: %v", err)
	}
	dangling := common.FormatHash(common.HashID("proj-never-created"))
	_, err = db.Search(core.DefaultAgentID, SearchQuery{L3ID: dangling})
	if common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("an anchor on a continued scene must be refused as a bad query, got %v", err)
	}
	if strings.Contains(err.Error(), dangling) {
		t.Fatalf("the refusal must not report the anchor as an unreadable record: %v", err)
	}
	if strings.Contains(err.Error(), common.FormatHash(first.Scene.SceneID)) {
		t.Fatalf("the refusal must not name a scene the host never handed in: %v", err)
	}
	again, err := db.Search(core.DefaultAgentID, SearchQuery{})
	if err != nil {
		t.Fatalf("continue the domain's scene: %v", err)
	}
	if again.Scene.SceneID != first.Scene.SceneID {
		t.Fatalf("refused reads must leave the domain on its scene: want %x got %x",
			first.Scene.SceneID, again.Scene.SceneID)
	}
}
