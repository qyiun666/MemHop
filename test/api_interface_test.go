// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Offline interface tests: exercise the public API surface through memhop.Open
// with a mock OpenAI-compatible LLM server. No external services required; run
// with `go test ./test/...`.

package test

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	memhop "github.com/qyiun666/MemHop/api"
	internal "github.com/qyiun666/MemHop/internal"
)

// testDB is the offline test handle: an agent-domain session plus the
// file-level lifecycle methods of the underlying DB.
type testDB struct {
	*memhop.Session
	m *memhop.DB
}

func (h *testDB) Checkpoint() error { return h.m.Checkpoint() }
func (h *testDB) Close() error      { return h.m.Close() }
func (h *testDB) IsClosed() bool    { return h.m.IsClosed() }

// CompactTo is a file-level operation, so it lives on the DB handle rather than
// the session.
func (h *testDB) CompactTo(newPath string) error { return h.m.CompactTo(newPath) }

// testLLM is the mock endpoint every offline scenario opens against.
func testLLM(url string) memhop.LlmConfig {
	return memhop.LlmConfig{APIURL: url, APIKey: "mock", Model: "mock-model"}
}

// openMockDB opens a database backed by the mock LLM at path. Opening a file
// that is not there yet needs a primary profile, so every scenario supplies the
// same fixture one. Opts tweak the tuning knobs per scenario.
func openMockDB(t *testing.T, path, llmURL string, opts ...func(*memhop.MemHopDefaults)) *memhop.DB {
	t.Helper()
	defaults := memhop.DefaultMemHopDefaults
	for _, opt := range opts {
		opt(&defaults)
	}
	m, err := memhop.Open(path, testLLM(llmURL), defaults,
		&memhop.ProfileInput{Name: "test-primary", Role: "offline fixture"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return m
}

// newTestDB binds a session on the file's primary domain to an opened DB.
func newTestDB(t *testing.T, m *memhop.DB) *testDB {
	t.Helper()
	sess, err := m.Primary()
	if err != nil {
		m.Close()
		t.Fatalf("Primary: %v", err)
	}
	return &testDB{Session: sess, m: m}
}

// openTestDB opens a DB backed by the mock LLM in a temp dir.
func openTestDB(t *testing.T) (*testDB, *mockLLM) {
	t.Helper()
	llm := newMockLLM(t)
	m := openMockDB(t, filepath.Join(t.TempDir(), "test.meh"), llm.srv.URL)
	h := newTestDB(t, m)
	t.Cleanup(func() { _ = h.Close() })
	return h, llm
}

// openSession asks the library for a fresh host session (scene) and returns
// its hex id. NewScene is what asks: an empty query now continues the domain's
// current scene, which is how a host running one agent over one library reads.
func openSession(t *testing.T, db *testDB) string {
	t.Helper()
	res, err := db.Search(memhop.SearchQuery{NewScene: true})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	return res.Scene.SceneID
}

// openTurn opens the next turn of a session and returns the topic id the
// library issued for it — the id that turn's content, plan tree and distillation
// are keyed by, and the one this file reads back. The writes of that turn take no
// id: the library holds which turn is open.
func openTurn(t *testing.T, db *testDB, sceneID string) string {
	t.Helper()
	res, err := db.Search(memhop.SearchQuery{SceneID: sceneID})
	if err != nil {
		t.Fatalf("open turn: %v", err)
	}
	return res.NewTopicID
}

// turn closes the turn the last Search opened, the way a host ends one round: one
// Update carries the stimulus, the answer and the timestamp, and the library lands
// them on the slots dialogue owns and distills them into that turn's topic. It
// returns the topic the turn settled into — which turn that was is the library's to
// remember, so a caller that wants proof compares this id with the one Search
// handed back. The error is the close's own, so a caller can pin a rejection.
func turn(db *memhop.Session, user, agent string) (string, error) {
	topic, err := db.Update(memhop.TurnEnd{
		Input: user, Output: agent, CreatedAt: time.Now().UnixMilli(),
	})
	if err != nil {
		return "", err
	}
	return topic.ID, nil
}

func TestInterfaceOpenClose(t *testing.T) {
	db, _ := openTestDB(t)
	if db.IsClosed() {
		t.Fatal("db should be open after Open")
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !db.IsClosed() {
		t.Fatal("db should be closed after Close")
	}
}

// The memory loop contract offline: an empty-id Search mints a session, one
// Update closes one turn (topic + two originals + exactly one distillation),
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
	topicID := openTurn(t, db, sceneID)
	closedID, err := turn(db.Session, "用户要求重构代码", "好的,我来重构这段代码")
	if err != nil {
		t.Fatalf("turn: %v", err)
	}
	// The turn's writes name no id, so this is where the loop closes: the topic
	// Update settled into is the one Search minted for this turn.
	if closedID != topicID {
		t.Fatalf("Update closed topic %s, want the turn Search opened (%s)", closedID, topicID)
	}
	if calls := llm.calls["keywords"]; calls != before+1 {
		t.Fatalf("Update distilled %d times, want exactly one per turn", calls-before)
	}

	// The turn is now the session's read surface, with the content it appended
	// held under its own id.
	after, err := db.Search(memhop.SearchQuery{SceneID: sceneID})
	if err != nil {
		t.Fatalf("Search after Update: %v", err)
	}
	if len(after.Topics) != 1 || after.Topics[0].ID != topicID {
		t.Fatalf("surface = %+v, want the one turn topic %s", after.Topics, topicID)
	}
	owned, err := db.SearchL4(memhop.L4Query{TopicID: &topicID})
	if err != nil || len(owned) != 2 {
		t.Fatalf("turn %s owns %d originals, want 2 (err %v)", topicID, len(owned), err)
	}
	if owned[0].Seq != 1 || owned[0].Role != memhop.RoleUser || owned[0].Content != "用户要求重构代码" ||
		owned[1].Seq != 2 || owned[1].Role != memhop.RoleAgent || owned[1].Content != "好的,我来重构这段代码" {
		t.Fatalf("the turn's originals = %+v, want the dialogue exactly as it was appended", owned)
	}
	if !slices.Equal(after.Topics[0].FusedKeywords, []string{"重构", "代码", "测试"}) {
		t.Fatalf("the turn topic carries %q, want the three words distilled from it", after.Topics[0].FusedKeywords)
	}

	// One turn stays the library's turn until the next read: a record still lands
	// on it, and an empty one is refused where it is written, not where it is
	// distilled. (A turn cannot be pointed at from elsewhere: with no turn open
	// every write refuses, which TestInterfaceWritesRefuseWhenNoTurnIsOpen pins.)
	if _, err := db.AppendArchive(memhop.ArchiveSlot{
		Kind: memhop.KindUtterance, Role: memhop.RoleUser, CreatedAt: 1,
	}); err == nil {
		t.Fatal("an utterance with no content should fail")
	}

	// A host that runs a second session and then loses it cannot close the turn
	// that session had opened: the scene going takes its turn, and the write is
	// refused as a missing turn rather than landing on a scene the host never named.
	other := openSession(t, db)
	if err := db.DeleteScene(other); err != nil {
		t.Fatalf("DeleteScene: %v", err)
	}
	if _, err := db.Update(memhop.TurnEnd{
		Input: "这轮没有开过", Output: "不该落笔", CreatedAt: time.Now().UnixMilli(),
	}); err == nil || !strings.Contains(err.Error(), "no turn is open") {
		t.Fatalf("closing a turn whose scene is gone = %v, want the open-turn refusal", err)
	}
	if survived, err := db.Search(memhop.SearchQuery{SceneID: sceneID}); err != nil || len(survived.Topics) != 1 {
		t.Fatalf("the refused close disturbed the surviving session: %d topics, err %v", len(survived.Topics), err)
	}

	// L2: sessions opened by Search are listable.
	scenes, err := db.ListScenes("")
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	if len(scenes) == 0 {
		t.Fatal("ListScenes should return the session opened by Search")
	}

	// L4: the originals the host appended are searchable verbatim.
	arcs, err := db.SearchL4(internal.L4Query{Keyword: "重构"})
	if err != nil {
		t.Fatalf("SearchL4: %v", err)
	}
	if len(arcs) == 0 {
		t.Fatal("SearchL4 should find archives by keyword")
	}
	if one, err := db.SearchL4(internal.L4Query{IDs: []string{arcs[0].ID}}); err != nil || len(one) != 1 {
		t.Fatalf("archive by id: %d found, err %v", len(one), err)
	}
}

// The turn's writes name no ids, so the one mistake left to a host is writing when
// the domain holds no open turn — never read, or a turn deleted out from under it.
// Every one of the five writes refuses with the query code and says which fact it
// is missing, and none of them guesses a turn to write onto.
func TestInterfaceWritesRefuseWhenNoTurnIsOpen(t *testing.T) {
	db, llm := openTestDB(t)
	ts := time.Now().UnixMilli()

	writes := []struct {
		name string
		call func() error
	}{
		{"Update", func() error {
			_, err := db.Update(memhop.TurnEnd{Input: "没开轮就关", Output: "a", CreatedAt: ts})
			return err
		}},
		{"AppendArchive", func() error {
			_, err := db.AppendArchive(memhop.ArchiveSlot{
				Kind: memhop.KindEvent, EventType: "tool_call", Content: `{"tool":"read"}`, CreatedAt: ts,
			})
			return err
		}},
		{"PlanNodeAdd", func() error { _, err := db.PlanNodeAdd(0, "第一步"); return err }},
		{"PlanNodeUpdate", func() error {
			return db.PlanNodeUpdate(memhop.PlanStep{Seq: 1, Status: memhop.PlanStatusDone})
		}},
		{"PlanState", func() error { _, err := db.PlanState(); return err }},
	}
	for _, w := range writes {
		err := w.call()
		if err == nil {
			t.Fatalf("%s: a write with no open turn was accepted", w.name)
		}
		if memhop.CodeOf(err) != memhop.ErrInvalidQuery {
			t.Fatalf("%s: code %d, want the query code %d (%v)", w.name, memhop.CodeOf(err), memhop.ErrInvalidQuery, err)
		}
		if !strings.Contains(err.Error(), "no turn is open") {
			t.Fatalf("%s: %v, want the refusal to name the missing open turn", w.name, err)
		}
	}
	if calls := llm.calls["keywords"]; calls != 0 {
		t.Fatalf("a refused close asked the model %d times, want 0", calls)
	}

	// The same refusal when the turn is gone rather than never opened: a scene that
	// goes takes the turn opened on it, and the library does not move that close to
	// a scene the host never named.
	sceneID := openSession(t, db)
	if err := db.DeleteScene(sceneID); err != nil {
		t.Fatalf("DeleteScene: %v", err)
	}
	if _, err := db.Update(memhop.TurnEnd{Input: "轮没了", Output: "a", CreatedAt: ts}); err == nil ||
		!strings.Contains(err.Error(), "no turn is open") {
		t.Fatalf("closing a turn whose scene was deleted = %v, want the open-turn refusal", err)
	}
	// The refusal is about the domain's state, not about the handle: one read opens
	// a turn again, and the same write lands.
	openSession(t, db)
	if topicID, err := turn(db.Session, "重新开一轮", "好了"); err != nil {
		t.Fatalf("turn after opening a turn again: %v", err)
	} else if topicID == "" {
		t.Fatal("the re-opened turn closed onto no topic")
	}
}

// One turn costs exactly one LLM round trip: the distillation of what the turn
// appended, however many records that is.
func TestInterfaceOneDistillationPerTurn(t *testing.T) {
	db, llm := openTestDB(t)
	sceneID := openSession(t, db)

	start := llm.calls["keywords"]
	for i := range 5 {
		openTurn(t, db, sceneID)
		if _, err := turn(db.Session, fmt.Sprintf("问题 %d", i), "回复"); err != nil {
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
	slot := &memhop.ProfileInput{
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

// A model that answers off contract costs the host that turn's distillation and
// nothing else: the close is refused rather than storing a topic with no keyword
// track, and the originals the closing call writes itself stay exactly as they
// were. The turn is still open afterwards, so the host can close it again.
func TestInterfaceUpdateRefusesAnOffContractReply(t *testing.T) {
	db, llm := openTestDB(t)
	sceneID := openSession(t, db)
	topicID := openTurn(t, db, sceneID)
	ts := time.Now().UnixMilli()

	llm.offContract = "这不是契约里的回包"
	_, err := db.Update(memhop.TurnEnd{
		Input: "用户要求重构代码", Output: "好的,我来重构这段代码", CreatedAt: ts,
	})
	if memhop.CodeOf(err) != memhop.ErrLLM {
		t.Fatalf("Update over an off-contract reply = %v (code %d), want the LLM code %d",
			err, memhop.CodeOf(err), memhop.ErrLLM)
	}
	if llm.calls["keywords"] == 0 {
		t.Fatal("the refusal came without ever asking the model")
	}

	owned, err := db.SearchL4(memhop.L4Query{TopicID: &topicID})
	if err != nil || len(owned) != 2 {
		t.Fatalf("the refused close cost the turn its originals: %d records, err %v", len(owned), err)
	}
	if owned[0].Content != "用户要求重构代码" || owned[1].Content != "好的,我来重构这段代码" {
		t.Fatalf("the refused close rewrote what it had written: %+v", owned)
	}
	// SceneContext is the read that opens no turn, and that is why the check that
	// nothing settled goes through it: a Search here would replace the very turn the
	// retry below is meant to close.
	ctx, err := db.SceneContext(sceneID)
	if err != nil {
		t.Fatalf("SceneContext after the refused close: %v", err)
	}
	for _, topic := range ctx.Topics {
		if topic.TopicID == topicID {
			t.Fatalf("the refused close left a topic behind: %+v", topic)
		}
	}
	// The turn the refused close did not settle is still the domain's open one: the
	// same call, with the model back on contract, lands it.
	llm.offContract = ""
	if closed, err := turn(db.Session, "用户要求重构代码", "好的,我来重构这段代码"); err != nil {
		t.Fatalf("retrying the refused close: %v", err)
	} else if closed != topicID {
		t.Fatalf("the retry closed topic %s, want the turn the refused call left open (%s)", closed, topicID)
	}
}
