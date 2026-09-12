// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Update is the read side of the hot path: the content a turn appended under its
// own topic id becomes one depth-1 topic, distilled by exactly one LLM call.
// Update writes no content, so every test here appends first.
package internal

import (
	"net/http"
	"strings"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

const turnKeywords = `{"keywords":["rust","所有权"]}`

const (
	userTurnText  = "rust 的所有权规则是什么"
	agentTurnText = "所有权系统靠移动语义保证内存安全"
)

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

// appendTurn is the host's half of a turn: the two originals in the two slots
// dialogue owns, under the timestamps the topic will report.
func appendTurn(t *testing.T, db *DB, topicID uint64, userTS int64) {
	t.Helper()
	slots := []core.ArchiveSlot{
		{Kind: core.KindUtterance, Seq: core.SeqUser, Role: core.RoleUser, Content: userTurnText, CreatedAt: userTS},
		{Kind: core.KindUtterance, Seq: core.SeqAgent, Role: core.RoleAgent, Content: agentTurnText, CreatedAt: userTS + 1000},
	}
	for _, slot := range slots {
		if err := db.AppendArchive(core.DefaultAgentID, common.FormatHash(topicID), slot); err != nil {
			t.Fatalf("AppendArchive seq %d: %v", slot.Seq, err)
		}
	}
}

func settle(db *DB, sceneID, topicID uint64) error {
	return db.Update(core.DefaultAgentID, common.FormatHash(sceneID), common.FormatHash(topicID))
}

// archivesOfTopic reads what a topic owns straight off the archive records, so
// a test never asks the index it is checking.
func archivesOfTopic(t *testing.T, engine *core.StorageEngine, topicID uint64) []core.ArchiveSlot {
	t.Helper()
	var out []core.ArchiveSlot
	for _, arc := range core.CollectAllArchives(engine, core.DefaultAgentID) {
		if arc.TopicID == topicID {
			out = append(out, arc)
		}
	}
	return out
}

// Settling a turn whose content is appended gives that turn one topic: single
// keyword track, the timestamps of the content it read, and nothing written by
// Update itself.
func TestUpdateWritesOneTurnTopic(t *testing.T) {
	srv, calls := countingLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)
	sceneID, topicID := openTurn(t, db)
	appendTurn(t, db, topicID, 1000)

	if err := settle(db, sceneID, topicID); err != nil {
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
	owned := archivesOfTopic(t, db.engine, topicID)
	if len(owned) != 2 {
		t.Fatalf("topic owns %d archives, want the two appended originals", len(owned))
	}
	gotUser, gotAgent := false, false
	for _, arc := range owned {
		switch arc.Role {
		case core.RoleUser:
			gotUser = arc.Content == userTurnText
		case core.RoleAgent:
			gotAgent = arc.Content == agentTurnText
		}
	}
	if !gotUser || !gotAgent {
		t.Fatalf("originals not stored verbatim: user=%v agent=%v", gotUser, gotAgent)
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
	for _, settleTurn := range []struct {
		topicID uint64
		userTS  int64
	}{
		{second.NewTopicID, 3000}, // the later turn settles first
		{firstID, 1000},
	} {
		appendTurn(t, db, settleTurn.topicID, settleTurn.userTS)
		if err := settle(db, sceneID, settleTurn.topicID); err != nil {
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

	if err := settle(db, 4242, 99); common.CodeOf(err) != common.ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("LLM called %d times for a rejected turn", got)
	}
	if n := countRecords(db.engine, core.DefaultAgentID, core.RecL2Topic); n != 0 {
		t.Fatalf("rejected turn left %d topics behind", n)
	}
}

// A malformed id is refused before any record or LLM call exists. An empty
// scene id means "settle into the domain's own id space", which is a scene that
// does not exist, so it is the unknown-scene path rather than this one.
func TestUpdateValidatesIds(t *testing.T) {
	srv, calls := countingLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)
	sceneID, topicID := openTurn(t, db)
	sceneHex, topicHex := common.FormatHash(sceneID), common.FormatHash(topicID)

	cases := []struct {
		name         string
		scene, topic string
	}{
		{"unparsable scene id", "not-hex", topicHex},
		{"missing topic id", sceneHex, ""},
		{"zero topic id", sceneHex, "0000000000000000"},
		{"unparsable topic id", sceneHex, "not-hex"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			appendTurn(t, db, topicID, 1000)
			if err := db.Update(core.DefaultAgentID, tc.scene, tc.topic); common.CodeOf(err) != common.ErrInvalidQuery {
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

// A turn whose content is gone is refused without spending an LLM call: an empty
// keyword track written now would read back as the real distillation of a turn
// nobody can any longer quote.
func TestUpdateRejectsTurnWithNoContent(t *testing.T) {
	srv, calls := countingLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)
	sceneID, topicID := openTurn(t, db)

	if err := settle(db, sceneID, topicID); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("err = %v, want ErrInvalidQuery", err)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("contentless turn reached the LLM %d times", got)
	}
	if n := countRecords(db.engine, core.DefaultAgentID, core.RecL2Topic); n != 0 {
		t.Fatalf("contentless turn wrote %d topics", n)
	}
}

// The distillation runs before the topic is written: an LLM failure must not
// leave a keywordless topic behind. The content the host appended earlier stays —
// Update never owned it and has no business undoing it — and the retry converges
// on the turn rather than duplicating it.
func TestUpdateDistillFailureLeavesNoTopic(t *testing.T) {
	srv := failingLLMServer(t, http.StatusBadRequest)
	db := newSearchTestDB(t, srv.URL)
	sceneID, topicID := openTurn(t, db)
	appendTurn(t, db, topicID, 1000)

	archivesBefore := countRecords(db.engine, core.DefaultAgentID, core.RecL4Archive)
	if err := settle(db, sceneID, topicID); common.CodeOf(err) != common.ErrLLM {
		t.Fatalf("err = %v, want ErrLLM", err)
	}
	if got := countRecords(db.engine, core.DefaultAgentID, core.RecL2Topic); got != 0 {
		t.Fatalf("failed turn wrote topics: %d", got)
	}
	if got := countRecords(db.engine, core.DefaultAgentID, core.RecL4Archive); got != archivesBefore {
		t.Fatalf("failed turn cost content: %d -> %d", archivesBefore, got)
	}
}

// An extraction that yields nothing must not create a contentless topic.
func TestUpdateRejectsEmptyExtraction(t *testing.T) {
	srv := mockLLMServer(t, `{"keywords":[]}`)
	db := newSearchTestDB(t, srv.URL)
	sceneID, topicID := openTurn(t, db)
	appendTurn(t, db, topicID, 1000)

	if err := settle(db, sceneID, topicID); common.CodeOf(err) != common.ErrLLM {
		t.Fatalf("err = %v, want ErrLLM", err)
	}
	if n := countRecords(db.engine, core.DefaultAgentID, core.RecL2Topic); n != 0 {
		t.Fatalf("empty extraction wrote %d topics", n)
	}
}

// A turn that distills only what its own utterances say: the label the record
// carries is what keeps the two sides apart in the prompt, so an extraction that
// sees both speakers is the check that the transcript was rendered, not glued.
func TestUpdateDistillsRenderedTranscript(t *testing.T) {
	srv, seen := recordingLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)
	sceneID, topicID := openTurn(t, db)
	appendTurn(t, db, topicID, 1000)

	if err := settle(db, sceneID, topicID); err != nil {
		t.Fatalf("Update: %v", err)
	}
	bodies := seen.snapshot()
	if len(bodies) != 1 {
		t.Fatalf("distillation sent %d requests, want 1", len(bodies))
	}
	for _, want := range []string{"User: " + userTurnText, "Assistant: " + agentTurnText} {
		if !strings.Contains(bodies[0], want) {
			t.Fatalf("transcript missing %q; sent: %s", want, bodies[0])
		}
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
			writeTopicCached(t, ac, db.engine, core.DefaultAgentID,
				newTopic(uint64(10+i), sceneID, int64(i*100), []string{"kw"}))
		}
		db.consolidateScene(ac, sceneID)
		if _, ok := ac.DreamInFlight[sceneID]; !ok {
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
		writeTopicCached(t, ac, db.engine, core.DefaultAgentID, newTopic(21, sceneID, 100, []string{"kw"}))

		db.consolidateScene(ac, sceneID)
		if len(ac.DreamInFlight) != 0 {
			t.Fatalf("scene scheduled below threshold: %v", ac.DreamInFlight)
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
			writeTopicCached(t, ac, db.engine, core.DefaultAgentID,
				newTopic(uint64(30+i), sceneID, int64(i*100), []string{"kw"}))
		}
		db.consolidateScene(ac, sceneID)
		if len(ac.DreamInFlight) != 0 {
			t.Fatalf("threshold 0 must never trigger, got %v", ac.DreamInFlight)
		}
	})
}

// Settling the same turn twice re-derives its track from the content the topic
// holds and creates no second topic — so an at-least-once write loop stays safe.
func TestUpdateReplayIsIdempotent(t *testing.T) {
	srv := mockLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)
	sceneID, topicID := openTurn(t, db)
	appendTurn(t, db, topicID, 1000)

	if err := settle(db, sceneID, topicID); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := settle(db, sceneID, topicID); err != nil {
		t.Fatalf("replay Update: %v", err)
	}
	if n := countRecords(db.engine, core.DefaultAgentID, core.RecL2Topic); n != 1 {
		t.Fatalf("topic records = %d, want 1", n)
	}
	if n := countRecords(db.engine, core.DefaultAgentID, core.RecL4Archive); n != 2 {
		t.Fatalf("content records = %d, want the two appended originals", n)
	}
	res, err := db.Search(core.DefaultAgentID, SearchQuery{SceneID: common.FormatHash(sceneID)})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Topics) != 1 {
		t.Fatalf("scene surface after replay = %+v", res.Topics)
	}
}

// A revised turn is revised by rewriting the slots it occupies: appending over
// Seq 1 and 2 replaces the originals in place, and settling again distills the
// new pair. The superseded wording stops being searchable and L4 holds one
// version of the turn — nothing had to be listed as owned beforehand.
func TestUpdateReplayOverwritesPriorContent(t *testing.T) {
	srv := mockLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)
	sceneID, topicID := openTurn(t, db)
	appendTurn(t, db, topicID, 1000)
	if err := settle(db, sceneID, topicID); err != nil {
		t.Fatalf("first Update: %v", err)
	}

	revised := []core.ArchiveSlot{
		{Kind: core.KindUtterance, Seq: core.SeqUser, Role: core.RoleUser, Content: "rust 的借用检查器怎么工作", CreatedAt: 1000},
		{Kind: core.KindUtterance, Seq: core.SeqAgent, Role: core.RoleAgent, Content: "同一时刻只允许一个可变借用", CreatedAt: 2000},
	}
	for _, slot := range revised {
		if err := db.AppendArchive(core.DefaultAgentID, common.FormatHash(topicID), slot); err != nil {
			t.Fatalf("revised append: %v", err)
		}
	}
	if err := settle(db, sceneID, topicID); err != nil {
		t.Fatalf("revised Update: %v", err)
	}

	if n := countRecords(db.engine, core.DefaultAgentID, core.RecL4Archive); n != 2 {
		t.Fatalf("live content records = %d, want 2: the rewrite landed in place", n)
	}
	if owned := archivesOfTopic(t, db.engine, topicID); len(owned) != 2 {
		t.Fatalf("topic owns %d records after replay, want the revised pair only", len(owned))
	}
	if hits, err := db.SearchL4(core.DefaultAgentID, L4Query{Keyword: "所有权规则"}); err != nil {
		t.Fatalf("SearchL4: %v", err)
	} else if len(hits) != 0 {
		t.Fatalf("superseded text still searchable: %+v", hits)
	}
	if hits, err := db.SearchL4(core.DefaultAgentID, L4Query{Keyword: "可变借用"}); err != nil {
		t.Fatalf("SearchL4: %v", err)
	} else if len(hits) != 1 {
		t.Fatalf("revised text should be the surviving record, got %+v", hits)
	}
}

// Update owns one contract on the write side: the topic it settles must be a
// turn topic of the scene it names. A replayed turn and an opened-but-earlier
// turn both qualify (pinned above); a Dream-fused topic does not — writing one
// would reset its depth and orphan the turns it folded — and neither does a
// turn of another scene.
func TestUpdateRejectsForeignOrFusedTopic(t *testing.T) {
	srv, calls := countingLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)
	sceneID, topicID := openTurn(t, db)
	appendTurn(t, db, topicID, 1000)

	// A fused group: depth-2 child under a depth-1 fused parent, as Dream leaves it.
	fusedParent := core.ComputeTopicID(sceneID, 500, 600)
	fusedChild := newTopic(core.ComputeTopicID(sceneID, 100, 200), sceneID, 100, []string{"kw"})
	fusedChild.Depth = 2
	fusedChild.ParentID = &fusedParent
	writeTopic(t, db.engine, core.DefaultAgentID, newTopic(fusedParent, sceneID, 500, []string{"kw"}))
	writeTopic(t, db.engine, core.DefaultAgentID, fusedChild)

	before := countRecords(db.engine, core.DefaultAgentID, core.RecL2Topic)
	cases := []struct {
		name    string
		topicID uint64
	}{
		{"fused parent", fusedParent},
		{"sunk child", fusedChild.ID},
		{"another scene's turn", core.ComputeTurnTopicID(sceneID+1, 1)},
		{"invented id", common.HashID("not-a-turn-this-scene-opened")},
	}
	for _, tc := range cases {
		if err := settle(db, sceneID, tc.topicID); common.CodeOf(err) != common.ErrInvalidQuery {
			t.Fatalf("%s: err = %v, want ErrInvalidQuery", tc.name, err)
		}
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("rejected turns reached the LLM %d times", got)
	}
	if got := countRecords(db.engine, core.DefaultAgentID, core.RecL2Topic); got != before {
		t.Fatalf("rejected turns wrote topics: %d -> %d", before, got)
	}
	if child, err := core.ReadTopicSlot(db.engine, core.DefaultAgentID, fusedChild.ID); err != nil || child.Depth != 2 {
		t.Fatalf("sunk topic was modified: %+v (%v)", child, err)
	}

	// The turn Search actually opened still settles.
	if err := settle(db, sceneID, topicID); err != nil {
		t.Fatalf("Update of the opened turn: %v", err)
	}
}
