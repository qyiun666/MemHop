// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Update is the single write of the hot path: the turn Search opened becomes
// one depth-1 topic plus two L4 archives, distilled by exactly one LLM call.
package internal

import (
	"net/http"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

const turnKeywords = `{"keywords":["rust","所有权"]}`

// openTurn gives a test the host session and the topic id Search issued for
// the turn it is about to settle.
func openTurn(t *testing.T, db *DB) (uint64, uint64) {
	t.Helper()
	res, err := db.Search(core.DefaultAgentID, SearchQuery{})
	if err != nil {
		t.Fatalf("open scene: %v", err)
	}
	return res.Scene.SceneID, res.NewTopicID
}

func turnOf(sceneID, topicID uint64) TurnUpdate {
	return TurnUpdate{
		SceneID:   common.FormatHash(sceneID),
		TopicID:   common.FormatHash(topicID),
		UserText:  "rust 的所有权规则是什么",
		UserTS:    1000,
		AgentText: "所有权系统靠移动语义保证内存安全",
		AgentTS:   2000,
	}
}

// One Update writes the topic Search opened: single keyword track, both
// timestamps, and its two originals as L4 archives.
func TestUpdateWritesOneTurnTopic(t *testing.T) {
	srv, calls := countingLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)
	sceneID, topicID := openTurn(t, db)

	got, err := db.Update(core.DefaultAgentID, turnOf(sceneID, topicID))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got != topicID {
		t.Fatalf("Update returned topic %d, want the id Search issued (%d)", got, topicID)
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

// Successive turns of one session each settle into the topic id that read
// issued, so their order in the surface follows the conversation, not the
// clock the host happens to report.
func TestUpdateSettlesEachScenesTurnsInOrder(t *testing.T) {
	srv := mockLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)
	sceneID, firstID := openTurn(t, db)

	second, err := db.Search(core.DefaultAgentID, SearchQuery{SceneID: common.FormatHash(sceneID)})
	if err != nil {
		t.Fatalf("second Search: %v", err)
	}
	for _, settle := range []struct {
		topicID uint64
		userTS  int64
	}{
		{second.NewTopicID, 3000}, // the later turn settles first
		{firstID, 1000},
	} {
		in := turnOf(sceneID, settle.topicID)
		in.UserTS = settle.userTS
		in.AgentTS = settle.userTS + 1000
		if _, err := db.Update(core.DefaultAgentID, in); err != nil {
			t.Fatalf("Update: %v", err)
		}
	}
	res, err := db.Search(core.DefaultAgentID, SearchQuery{SceneID: common.FormatHash(sceneID)})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Topics) != 2 {
		t.Fatalf("scene surface = %+v, want 2 turn topics", res.Topics)
	}
	if res.Topics[0].ID != firstID || res.Topics[1].ID != second.NewTopicID {
		t.Fatalf("surface order = %d then %d, want %d then %d",
			res.Topics[0].ID, res.Topics[1].ID, firstID, second.NewTopicID)
	}
}

// A turn must land in a scene the host opened: an unknown id is rejected and
// nothing is written.
func TestUpdateRejectsUnknownScene(t *testing.T) {
	srv, calls := countingLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)

	_, err := db.Update(core.DefaultAgentID, turnOf(4242, 99))
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
	sceneID, topicID := openTurn(t, db)
	base := turnOf(sceneID, topicID)

	cases := []struct {
		name  string
		patch func(*TurnUpdate)
	}{
		{"empty user text", func(u *TurnUpdate) { u.UserText = "" }},
		{"empty agent text", func(u *TurnUpdate) { u.AgentText = "" }},
		{"zero user timestamp", func(u *TurnUpdate) { u.UserTS = 0 }},
		{"agent before user", func(u *TurnUpdate) { u.AgentTS = 999 }},
		{"unparsable scene id", func(u *TurnUpdate) { u.SceneID = "not-hex" }},
		{"missing topic id", func(u *TurnUpdate) { u.TopicID = "" }},
		{"zero topic id", func(u *TurnUpdate) { u.TopicID = "0000000000000000" }},
		{"unparsable topic id", func(u *TurnUpdate) { u.TopicID = "not-hex" }},
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
	sceneID, topicID := openTurn(t, db)

	before := countRecords(db.engine, core.DefaultAgentID, core.RecL2Topic)
	archivesBefore := countRecords(db.engine, core.DefaultAgentID, core.RecL4Archive)

	if _, err := db.Update(core.DefaultAgentID, turnOf(sceneID, topicID)); common.CodeOf(err) != common.ErrLLM {
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
	sceneID, topicID := openTurn(t, db)

	if _, err := db.Update(core.DefaultAgentID, turnOf(sceneID, topicID)); common.CodeOf(err) != common.ErrLLM {
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

// A host that retries a settled turn (same topic id) gets that same topic and
// the same two archives back — the turn never accumulates duplicates, so an
// at-least-once write loop stays safe.
func TestUpdateReplayIsIdempotent(t *testing.T) {
	srv := mockLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)
	sceneID, topicID := openTurn(t, db)

	first, err := db.Update(core.DefaultAgentID, turnOf(sceneID, topicID))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	second, err := db.Update(core.DefaultAgentID, turnOf(sceneID, topicID))
	if err != nil {
		t.Fatalf("replay Update: %v", err)
	}
	if first != second {
		t.Fatalf("replay settled a different topic: %d vs %d", first, second)
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

// Update is the only L4 write path, so it is also where a non-text turn gets
// its type: the slot is archived under the declared content type while the
// other side keeps the text default, and the scene read reports it back.
func TestUpdateStoresDeclaredContentTypes(t *testing.T) {
	srv := mockLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)
	sceneID, topicID := openTurn(t, db)

	in := turnOf(sceneID, topicID)
	in.UserText = "img://cat.png"
	in.UserType = core.ContentImage
	if _, err := db.Update(core.DefaultAgentID, in); err != nil {
		t.Fatalf("Update: %v", err)
	}

	topicHex := common.FormatHash(topicID)
	byTopic := func(ct core.ContentType) []core.ArchiveSlot {
		// topic_id and type are filters only: the query needs one of the
		// three modes, so sweep the time range covering this turn.
		q := L4Query{Start: 1, End: 10000, TopicID: &topicHex, Type: &ct}
		got, err := db.SearchL4(core.DefaultAgentID, q)
		if err != nil {
			t.Fatalf("SearchL4 by type %d: %v", ct, err)
		}
		return got
	}
	images := byTopic(core.ContentImage)
	if len(images) != 1 || images[0].Content != "img://cat.png" || images[0].Role != core.RoleUser {
		t.Fatalf("image archive = %+v, want the user slot only", images)
	}
	texts := byTopic(core.ContentText)
	if len(texts) != 1 || texts[0].Role != core.RoleAgent {
		t.Fatalf("text archive = %+v, want the agent slot to keep the default type", texts)
	}

	ctx, err := db.SceneContext(core.DefaultAgentID, common.FormatHash(sceneID))
	if err != nil {
		t.Fatalf("SceneContext: %v", err)
	}
	msgs := ctx.Topics[0].Messages
	if len(msgs) != 2 || msgs[0].Type != core.ContentImage || msgs[1].Type != core.ContentText {
		t.Fatalf("scene context types = %+v, want image then text", msgs)
	}
}
