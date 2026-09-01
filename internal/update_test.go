// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Update is the single write of the hot path: one finished turn becomes one
// depth-1 topic plus two L4 archives, distilled by exactly one LLM call.
package internal

import (
	"context"
	"net/http"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

const turnKeywords = `{"keywords":["rust","所有权"]}`

// openScene gives a test the host session the turn must land in.
func openScene(t *testing.T, db *DB, name string) uint64 {
	t.Helper()
	res, err := db.Search(core.DefaultAgentID, SearchQuery{SceneName: name})
	if err != nil {
		t.Fatalf("open scene: %v", err)
	}
	return res.Scene.SceneID
}

func turnOf(sceneID uint64) TurnUpdate {
	return TurnUpdate{
		SceneID:   common.FormatHash(sceneID),
		UserText:  "rust 的所有权规则是什么",
		UserTS:    1000,
		AgentText: "所有权系统靠移动语义保证内存安全",
		AgentTS:   2000,
	}
}

// One Update writes one topic (single keyword track, both timestamps) and its
// two originals as L4 archives; the returned id accepts further messages.
func TestUpdateWritesOneTurnTopic(t *testing.T) {
	srv, calls := countingLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)
	sceneID := openScene(t, db, "session")

	topicID, err := db.Update(core.DefaultAgentID, turnOf(sceneID))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("Update made %d LLM calls, want exactly 1", got)
	}
	topic, err := core.ReadTopicLenient(db.engine, core.DefaultAgentID, topicID)
	if err != nil || topic == nil {
		t.Fatalf("read topic: %v", err)
	}
	if topic.Depth != 1 || topic.SceneID != sceneID {
		t.Fatalf("topic placed wrong: %+v", topic)
	}
	if len(topic.FusedKeywords) != 2 || topic.FusedKeywords[0] != "rust" {
		t.Fatalf("keyword track mismatch: %v", topic.FusedKeywords)
	}
	if topic.UserTimestamp != 1000 || topic.AgentTimestamp != 2000 {
		t.Fatalf("timestamp mismatch: %+v", topic)
	}
	if len(topic.L4Refs) != 2 {
		t.Fatalf("L4Refs = %v, want both originals", topic.L4Refs)
	}
	gotUser, gotAgent := false, false
	for _, ref := range topic.L4Refs {
		arc, err := core.ReadArchiveSlot(db.engine, core.DefaultAgentID, ref)
		if err != nil {
			t.Fatalf("read archive %d: %v", ref, err)
		}
		switch arc.Role {
		case core.RoleUser:
			gotUser = arc.Content == "rust 的所有权规则是什么"
		case core.RoleAgent:
			gotAgent = arc.Content == "所有权系统靠移动语义保证内存安全"
		}
	}
	if !gotUser || !gotAgent {
		t.Fatalf("originals not archived verbatim: user=%v agent=%v", gotUser, gotAgent)
	}

	// The turn is now part of what a host reads back for that session.
	res, err := db.Search(core.DefaultAgentID, SearchQuery{SceneID: common.FormatHash(sceneID)})
	if err != nil {
		t.Fatalf("Search after Update: %v", err)
	}
	if len(res.Topics) != 1 || res.Topics[0].ID != topicID {
		t.Fatalf("scene surface = %+v, want the one new topic", res.Topics)
	}
}

// The returned topic id keeps accepting this turn's intermediate messages.
func TestUpdateResultAcceptsAppendL4(t *testing.T) {
	srv := mockLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)
	sceneID := openScene(t, db, "session")

	topicID, err := db.Update(core.DefaultAgentID, turnOf(sceneID))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, err := db.AppendL4Message(core.DefaultAgentID, common.FormatHash(topicID),
		"工具输出：编译通过", 2500, core.RoleAgent, core.ContentText); err != nil {
		t.Fatalf("AppendL4Message: %v", err)
	}
	topic, err := core.ReadTopicLenient(db.engine, core.DefaultAgentID, topicID)
	if err != nil || topic == nil {
		t.Fatalf("read topic: %v", err)
	}
	if len(topic.L4Refs) != 3 {
		t.Fatalf("L4Refs = %d, want 3", len(topic.L4Refs))
	}
}

// A turn must land in a scene the host opened: an unknown id is rejected and
// nothing is written.
func TestUpdateRejectsUnknownScene(t *testing.T) {
	srv, calls := countingLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)

	_, err := db.Update(core.DefaultAgentID, turnOf(4242))
	if common.CodeOf(err) != common.ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("LLM called %d times for a rejected turn", got)
	}
	if n := countRecords(db.engine, core.DefaultAgentID, core.RecL2Topic); n != 0 {
		t.Fatalf("rejected turn left %d topics behind", n)
	}
}

// A malformed turn is refused before any record or LLM call exists.
func TestUpdateValidatesPayload(t *testing.T) {
	srv, calls := countingLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)
	sceneID := openScene(t, db, "session")
	base := turnOf(sceneID)

	cases := []struct {
		name  string
		patch func(*TurnUpdate)
	}{
		{"empty user text", func(u *TurnUpdate) { u.UserText = "" }},
		{"empty agent text", func(u *TurnUpdate) { u.AgentText = "" }},
		{"zero user timestamp", func(u *TurnUpdate) { u.UserTS = 0 }},
		{"agent before user", func(u *TurnUpdate) { u.AgentTS = 999 }},
		{"unparsable scene id", func(u *TurnUpdate) { u.SceneID = "not-hex" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			tc.patch(&in)
			if _, err := db.Update(core.DefaultAgentID, in); common.CodeOf(err) != common.ErrInvalidQuery {
				t.Fatalf("err = %v, want ErrInvalidQuery", err)
			}
		})
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("invalid turns reached the LLM %d times", got)
	}
	if n := countRecords(db.engine, core.DefaultAgentID, core.RecL2Topic); n != 0 {
		t.Fatalf("invalid turns wrote %d topics", n)
	}
}

// The distillation runs before any write: an LLM failure must not leave a
// half-written turn (orphan archive or keywordless topic) behind.
func TestUpdateDistillFailureLeavesNoTrace(t *testing.T) {
	srv := failingLLMServer(t, http.StatusBadRequest)
	db := newSearchTestDB(t, srv.URL)
	sceneID := openScene(t, db, "session")

	before := countRecords(db.engine, core.DefaultAgentID, core.RecL2Topic)
	archivesBefore := countRecords(db.engine, core.DefaultAgentID, core.RecL4Archive)

	if _, err := db.Update(core.DefaultAgentID, turnOf(sceneID)); common.CodeOf(err) != common.ErrLLM {
		t.Fatalf("err = %v, want ErrLLM", err)
	}
	if got := countRecords(db.engine, core.DefaultAgentID, core.RecL2Topic); got != before {
		t.Fatalf("failed turn wrote topics: %d -> %d", before, got)
	}
	if got := countRecords(db.engine, core.DefaultAgentID, core.RecL4Archive); got != archivesBefore {
		t.Fatalf("failed turn wrote archives: %d -> %d", archivesBefore, got)
	}
}

// An extraction that yields nothing must not create a contentless topic.
func TestUpdateRejectsEmptyExtraction(t *testing.T) {
	srv := mockLLMServer(t, `{"keywords":[]}`)
	db := newSearchTestDB(t, srv.URL)
	sceneID := openScene(t, db, "session")

	if _, err := db.Update(core.DefaultAgentID, turnOf(sceneID)); common.CodeOf(err) != common.ErrLLM {
		t.Fatalf("err = %v, want ErrLLM", err)
	}
	if n := countRecords(db.engine, core.DefaultAgentID, core.RecL2Topic); n != 0 {
		t.Fatalf("empty extraction wrote %d topics", n)
	}
}

// Consolidation is scheduled per scene once its surface passes the threshold,
// and not below it (the scene is a host session, not an active-set slot).
func TestConsolidateSceneThreshold(t *testing.T) {
	t.Run("over threshold schedules the scene dream", func(t *testing.T) {
		srv := mockLLMServer(t, turnKeywords)
		db := newSearchTestDB(t, srv.URL)
		db.config.Defaults.SceneDreamTopicThreshold = 2
		db.config.Defaults.DreamCompressMinTopics = 100 // keep the pass itself LLM-free
		ac := testDefaultContext(db)
		const sceneID = uint64(7)
		mustWriteScene(t, db.engine, core.DefaultAgentID, sceneID, "s")
		for i := 1; i <= 3; i++ {
			writeTopic(t, db.engine, core.DefaultAgentID, newTopic(uint64(10+i), sceneID, int64(i*100), []string{"kw"}))
			ac.syncL2Meta(db, uint64(10+i))
		}
		db.consolidateScene(ac, sceneID)
		if _, ok := ac.dreamInFlight[sceneID]; !ok {
			t.Fatal("scene not scheduled for consolidation")
		}
	})

	t.Run("at_or_under_threshold_is_noop", func(t *testing.T) {
		srv := mockLLMServer(t, turnKeywords)
		db := newSearchTestDB(t, srv.URL)
		db.config.Defaults.SceneDreamTopicThreshold = 3
		ac := testDefaultContext(db)
		const sceneID = uint64(8)
		mustWriteScene(t, db.engine, core.DefaultAgentID, sceneID, "s")
		writeTopic(t, db.engine, core.DefaultAgentID, newTopic(21, sceneID, 100, []string{"kw"}))
		ac.syncL2Meta(db, 21)

		db.consolidateScene(ac, sceneID)
		if len(ac.dreamInFlight) != 0 {
			t.Fatalf("scene scheduled below threshold: %v", ac.dreamInFlight)
		}
	})

	t.Run("zero threshold disables the trigger", func(t *testing.T) {
		srv := mockLLMServer(t, turnKeywords)
		db := newSearchTestDB(t, srv.URL)
		db.config.Defaults.SceneDreamTopicThreshold = 0
		ac := testDefaultContext(db)
		const sceneID = uint64(9)
		mustWriteScene(t, db.engine, core.DefaultAgentID, sceneID, "s")
		for i := 1; i <= 5; i++ {
			writeTopic(t, db.engine, core.DefaultAgentID, newTopic(uint64(30+i), sceneID, int64(i*100), []string{"kw"}))
			ac.syncL2Meta(db, uint64(30+i))
		}
		db.consolidateScene(ac, sceneID)
		if len(ac.dreamInFlight) != 0 {
			t.Fatalf("threshold 0 must never trigger, got %v", ac.dreamInFlight)
		}
	})
}

// Refine re-distills from every original of the topic, so a turn whose extra
// messages arrived after the fact gets them into the keyword track.
func TestRefineAfterAppendUsesAllOriginals(t *testing.T) {
	srv, calls := countingLLMServer(t, `{"keywords":["编译","移动语义"]}`)
	db := newSearchTestDB(t, srv.URL)
	sceneID := openScene(t, db, "session")
	topicID, err := db.Update(core.DefaultAgentID, turnOf(sceneID))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, err := db.AppendL4Message(core.DefaultAgentID, common.FormatHash(topicID),
		"补充：还需要借用检查器", 2600, core.RoleUser, core.ContentText); err != nil {
		t.Fatalf("append: %v", err)
	}
	before := calls.Load()

	if err := db.RefineTopicKeywords(context.Background(), core.DefaultAgentID, common.FormatHash(topicID)); err != nil {
		t.Fatalf("RefineTopicKeywords: %v", err)
	}
	if calls.Load() != before+1 {
		t.Fatalf("refine must re-distill once, calls %d -> %d", before, calls.Load())
	}
	topic, err := core.ReadTopicLenient(db.engine, core.DefaultAgentID, topicID)
	if err != nil || topic == nil {
		t.Fatalf("read topic: %v", err)
	}
	if len(topic.FusedKeywords) != 2 || topic.FusedKeywords[0] != "编译" {
		t.Fatalf("keywords not replaced: %v", topic.FusedKeywords)
	}
	if topic.UserTimestamp != 1000 || topic.AgentTimestamp != 2000 {
		t.Fatalf("refine must not move the turn timestamps: %+v", topic)
	}
}

// A host that retries a settled turn (same scene and both timestamps) gets the
// same topic and the same two archives back — the turn never accumulates
// duplicates, so an at-least-once write loop stays safe.
func TestUpdateReplayIsIdempotent(t *testing.T) {
	srv := mockLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)
	sceneID := openScene(t, db, "session")

	first, err := db.Update(core.DefaultAgentID, turnOf(sceneID))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	second, err := db.Update(core.DefaultAgentID, turnOf(sceneID))
	if err != nil {
		t.Fatalf("replay Update: %v", err)
	}
	if first != second {
		t.Fatalf("replay derived a new topic id: %d vs %d", first, second)
	}
	if n := countRecords(db.engine, core.DefaultAgentID, core.RecL2Topic); n != 1 {
		t.Fatalf("topic records = %d, want 1", n)
	}
	if n := countRecords(db.engine, core.DefaultAgentID, core.RecL4Archive); n != 2 {
		t.Fatalf("archive records = %d, want 2 (the turn's two originals)", n)
	}
	res, err := db.Search(core.DefaultAgentID, SearchQuery{SceneID: common.FormatHash(sceneID)})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Topics) != 1 || len(res.Topics[0].L4Refs) != 2 {
		t.Fatalf("scene surface after replay = %+v", res.Topics)
	}
}
