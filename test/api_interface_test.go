// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Offline interface tests: exercise the public API surface through
// memhop.OpenMulti with a mock OpenAI-compatible LLM server. No external
// services required; run with `go test ./test/...`.

package test

import (
	"path/filepath"
	"testing"
	"time"

	memhop "github.com/qyiun666/MemHop/api"
	internal "github.com/qyiun666/MemHop/internal"
	"github.com/qyiun666/MemHop/internal/common"
)

// testDB is the offline test handle: an agent-domain session plus the
// file-level lifecycle methods of the underlying MultiAgentDB.
type testDB struct {
	*memhop.Session
	m *memhop.MultiAgentDB
}

func (h *testDB) Checkpoint() error { return h.m.Checkpoint() }
func (h *testDB) Close() error      { return h.m.Close() }
func (h *testDB) IsClosed() bool    { return h.m.IsClosed() }

// openMockMulti opens a multi-agent DB with the mock LLM at path; multi-agent
// is the only mode, so every handle goes through CreateAgent + Session.
// Opts tweak cfg.Defaults per scenario.
func openMockMulti(t *testing.T, path, llmURL string, opts ...func(*internal.MemHopDefaults)) *memhop.MultiAgentDB {
	t.Helper()
	cfg := &internal.MemHopConfig{
		DBPath: path,
	}
	cfg.LLM.APIURL = llmURL
	cfg.LLM.APIKey = "mock"
	cfg.LLM.Model = "mock-model"
	cfg.Defaults = *internal.DefaultMemHopDefaults
	for _, opt := range opts {
		opt(&cfg.Defaults)
	}
	m, err := memhop.OpenMulti(cfg)
	if err != nil {
		t.Fatalf("OpenMulti: %v", err)
	}
	return m
}

// newTestDB binds a session (tenant "test") to an opened multi-agent DB.
func newTestDB(t *testing.T, m *memhop.MultiAgentDB) *testDB {
	t.Helper()
	id, err := m.CreateAgent("test")
	if err != nil {
		m.Close()
		t.Fatalf("CreateAgent: %v", err)
	}
	sess, err := m.Session(id)
	if err != nil {
		m.Close()
		t.Fatalf("Session: %v", err)
	}
	return &testDB{Session: sess, m: m}
}

// openTestDB opens a DB backed by the mock LLM in a temp dir.
func openTestDB(t *testing.T) (*testDB, *mockLLM) {
	t.Helper()
	llm := newMockLLM(t)
	m := openMockMulti(t, filepath.Join(t.TempDir(), "test.meh"), llm.srv.URL)
	h := newTestDB(t, m)
	t.Cleanup(func() { _ = h.Close() })
	return h, llm
}

// openSession asks the library for a fresh host session (scene) and returns
// its hex id.
func openSession(t *testing.T, db *testDB) string {
	t.Helper()
	res, err := db.Search(memhop.SearchQuery{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	return res.Scene.SceneID
}

// openTurn opens the next turn of a session and returns the topic id the
// library issued for it — the id the turn must settle into.
func openTurn(t *testing.T, db *testDB, sceneID string) string {
	t.Helper()
	res, err := db.Search(memhop.SearchQuery{SceneID: sceneID})
	if err != nil {
		t.Fatalf("open turn: %v", err)
	}
	return res.NewTopicID
}

// turn builds one finished turn settling the given topic id.
func turn(sceneID, topicID, user, agent string) memhop.TurnUpdate {
	ts := time.Now().UnixMilli()
	return memhop.TurnUpdate{
		SceneID: sceneID, TopicID: topicID, UserText: user, UserTS: ts, AgentText: agent, AgentTS: ts + 1,
	}
}

func TestInterfaceOpenClose(t *testing.T) {
	db, _ := openTestDB(t)
	if db.IsClosed() {
		t.Fatal("db should be open after OpenMulti")
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !db.IsClosed() {
		t.Fatal("db should be closed after Close")
	}
}

// The memory loop contract offline: an empty-id Search mints a session, one
// Update settles one turn (topic + two originals + exactly one distillation),
// and the same session read hands it back.
func TestInterfaceSearchUpdateL2L4(t *testing.T) {
	db, llm := openTestDB(t)
	sceneID := openSession(t, db)

	// A fresh session has no topics, and reading it distills nothing.
	fresh, err := db.Search(memhop.SearchQuery{SceneID: sceneID})
	if err != nil {
		t.Fatalf("Search(scene): %v", err)
	}
	if len(fresh.Topics) != 0 {
		t.Fatalf("fresh session should be empty, got %+v", fresh.Topics)
	}
	if calls := llm.calls["keywords"]; calls != 0 {
		t.Fatalf("Search distilled %d times, want 0 (reads never distill)", calls)
	}

	before := llm.calls["keywords"]
	topicID, err := db.Update(turn(sceneID, openTurn(t, db, sceneID), "用户要求重构代码", "好的,我来重构这段代码"))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if calls := llm.calls["keywords"]; calls != before+1 {
		t.Fatalf("Update distilled %d times, want exactly one per turn", calls-before)
	}

	// The turn is now the session's read surface, with both originals linked.
	after, err := db.Search(memhop.SearchQuery{SceneID: sceneID})
	if err != nil {
		t.Fatalf("Search after Update: %v", err)
	}
	if len(after.Topics) != 1 || after.Topics[0].ID != topicID {
		t.Fatalf("surface = %+v, want the one turn topic %s", after.Topics, topicID)
	}
	if len(after.Topics[0].L4Refs) != 2 {
		t.Fatalf("topic L4Refs = %v, want both originals", after.Topics[0].L4Refs)
	}
	if len(after.Topics[0].FusedKeywords) == 0 {
		t.Fatal("the turn topic must carry its distilled keywords")
	}

	// A turn for a session nobody opened is rejected, and so is a malformed one.
	orphanTurn := common.FormatHash(common.HashID("unopened-turn"))
	if _, err := db.Update(turn(common.FormatHash(999), orphanTurn, "无主话题", "无主回复")); err == nil {
		t.Fatal("Update on an unknown scene should fail")
	}
	if _, err := db.Update(memhop.TurnUpdate{SceneID: sceneID, UserText: "", UserTS: 1, AgentText: "a", AgentTS: 2}); err == nil {
		t.Fatal("empty user text should fail")
	}

	// L2: sessions opened by Search are listable.
	scenes, err := db.ListScenes()
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	if len(scenes) == 0 {
		t.Fatal("ListScenes should return the session opened by Search")
	}

	// L4: the originals written by Update are searchable verbatim.
	arcs, err := db.SearchL4(internal.L4Query{Keyword: "重构"})
	if err != nil {
		t.Fatalf("SearchL4: %v", err)
	}
	if len(arcs) == 0 {
		t.Fatal("SearchL4 should find archives by keyword")
	}
	if _, err := db.GetArchive(arcs[0].IDHash); err != nil {
		t.Fatalf("GetArchive: %v", err)
	}
}

// Update costs exactly one LLM round trip per turn — the point of the
// re-designed write path, where the retired contract costed two.
func TestInterfaceOneDistillationPerTurn(t *testing.T) {
	db, llm := openTestDB(t)
	sceneID := openSession(t, db)

	start := llm.calls["keywords"]
	for i := range 5 {
		if _, err := db.Update(turn(sceneID, openTurn(t, db, sceneID), "问题 "+common.FormatHash(uint64(i)), "回复")); err != nil {
			t.Fatalf("turn %d: %v", i, err)
		}
	}
	if got := llm.calls["keywords"] - start; got != 5 {
		t.Fatalf("5 turns cost %d distillations, want 5", got)
	}
	res, err := db.Search(memhop.SearchQuery{SceneID: sceneID})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Topics) != 5 {
		t.Fatalf("surface = %d topics, want 5 turns", len(res.Topics))
	}
}

func TestInterfaceL0(t *testing.T) {
	db, _ := openTestDB(t)
	slot := &memhop.ProfileSlot{
		Name:        "测试画像",
		Preferences: map[string]string{"language": "Go"},
	}
	if err := db.UpdateL0(slot); err != nil {
		t.Fatalf("UpdateL0: %v", err)
	}
	got, err := db.GetL0()
	if err != nil {
		t.Fatalf("GetL0: %v", err)
	}
	if got.Name != "测试画像" || got.Preferences["language"] != "Go" {
		t.Fatalf("L0 mismatch: %+v", got)
	}
}
