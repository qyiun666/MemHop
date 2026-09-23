// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Update is the one call that closes a turn: it records what the turn opened
// with and what it ended with, then distils the topic's utterances. Which turn
// it closes is the domain's own — Search minted it — so no test here hands an id
// to a write.
package internal

import (
	"net/http"
	"slices"
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
// the turn it is about to close. The domain holds that id now, so a write call
// takes none: what openTurn hands back is for reading records back and for
// addressing the turn in an L4 query.
func openTurn(t *testing.T, db *DB) (uint64, uint64) {
	t.Helper()
	res, err := db.Search(core.DefaultAgentID, SearchQuery{})
	if err != nil {
		t.Fatalf("open scene: %v", err)
	}
	return res.Scene.SceneID, res.NewTopicID
}

// appendTurn is the host's half of a turn recorded while it ran: the two
// originals in the two slots dialogue owns, under the timestamps the topic will
// report. A closing call writes those same two slots itself, so a test that
// appends and then closes the turn lands one pair of records, not two.
//
// The write takes its address from the domain, so it names no ids: a test that lost
// the turn finds out at the close, which refuses a turn the domain no longer holds
// (see settle).
func appendTurn(t *testing.T, db *DB, userTS int64) {
	t.Helper()
	slots := []core.ArchiveSlot{
		{Kind: core.KindUtterance, Seq: core.SeqUser, Role: core.RoleUser, Content: userTurnText, CreatedAt: userTS},
		{Kind: core.KindUtterance, Seq: core.SeqAgent, Role: core.RoleAgent, Content: agentTurnText, CreatedAt: userTS + 1000},
	}
	for _, slot := range slots {
		if _, err := db.AppendArchive(core.DefaultAgentID, slot); err != nil {
			t.Fatalf("AppendArchive seq %d: %v", slot.Seq, err)
		}
	}
}

// endTurn is the host's whole closing call: the two originals a turn leaves
// behind, in one call that also distils them.
func endTurn(db *DB, ts int64) error {
	_, err := db.Update(core.DefaultAgentID, core.TurnEnd{Input: userTurnText, Output: agentTurnText, CreatedAt: ts})
	return err
}

// settle closes the turn a fixture believes is open. The scene and topic
// arguments are the test's belief, not the library's instruction: the domain
// decides for itself which turn that is, so the helper refuses to hide a caller
// that lost it. The timestamp is the one every fixture that appends first passes.
func settle(db *DB, sceneID, topicID uint64) error {
	ac := db.agents[core.DefaultAgentID]
	if ac == nil || ac.Scene != sceneID || ac.Turn != topicID {
		return common.NewError(common.ErrInvalidQuery,
			"the turn this test settles is not the one the domain holds open")
	}
	return endTurn(db, 1000)
}

// wantNoOpenTurn asserts the refusal every turn write makes when the domain holds
// no open turn: the host has to read before it can write, and guessing a turn would
// close one nobody opened.
func wantNoOpenTurn(t *testing.T, err error) {
	t.Helper()
	if common.CodeOf(err) != common.ErrInvalidQuery || !strings.Contains(err.Error(), "no turn is open") {
		t.Fatalf("err = %v, want ErrInvalidQuery saying %q", err, "no turn is open")
	}
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

// Closing a turn yields one depth-1 topic for it: a single keyword track, the
// timestamps of the content the distillation read, and the two originals stored
// verbatim under the one CreatedAt the closing call named.
func TestUpdateWritesOneTurnTopic(t *testing.T) {
	srv, calls := countingLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)
	sceneID, topicID := openTurn(t, db)

	returned, err := db.Update(core.DefaultAgentID, core.TurnEnd{
		Input: userTurnText, Output: agentTurnText, CreatedAt: 1000,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("Update made %d LLM calls, want exactly 1", got)
	}
	if returned == nil || returned.ID != topicID {
		t.Fatalf("the close hands back the topic as stored, got %+v", returned)
	}
	if want := []string{"rust", "所有权"}; !slices.Equal(returned.FusedKeywords, want) {
		t.Fatalf("returned keyword track = %v, want %v", returned.FusedKeywords, want)
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
	// One call, one instant: the pair it writes carries the CreatedAt that call
	// named, so both sides of the exchange are stamped together.
	if topic.UserTimestamp != 1000 || topic.AgentTimestamp != 1000 {
		t.Fatalf("timestamp mismatch: %+v", topic)
	}
	owned := archivesOfTopic(t, db.engine, topicID)
	if len(owned) != 2 {
		t.Fatalf("topic owns %d archives, want the two originals the close wrote", len(owned))
	}
	gotUser, gotAgent := false, false
	for _, arc := range owned {
		switch arc.Role {
		case core.RoleUser:
			gotUser = arc.Content == userTurnText && arc.Seq == core.SeqUser
		case core.RoleAgent:
			gotAgent = arc.Content == agentTurnText && arc.Seq == core.SeqAgent
		}
	}
	if !gotUser || !gotAgent {
		t.Fatalf("originals not stored verbatim on the slots dialogue owns: user=%v agent=%v", gotUser, gotAgent)
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

// Successive turns of one session each settle into the topic id the read that
// opened them issued. The domain carries one turn at a time, so a host closes the
// turn it has before opening the next — and the surface comes back in turn order.
func TestUpdateSettlesEachScenesTurnsInOrder(t *testing.T) {
	srv := mockLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)
	sceneID, firstID := openTurn(t, db)

	if err := endTurn(db, 1000); err != nil {
		t.Fatalf("first Update: %v", err)
	}
	second, err := db.Search(core.DefaultAgentID, SearchQuery{SceneID: common.FormatHash(sceneID)})
	if err != nil {
		t.Fatalf("second Search: %v", err)
	}
	if second.NewTopicID == firstID {
		t.Fatalf("two reads of one scene issued the same turn topic: %d", second.NewTopicID)
	}
	if err := endTurn(db, 3000); err != nil {
		t.Fatalf("second Update: %v", err)
	}

	// Each turn settled into the topic its own read issued, and nowhere else: the
	// ids are the scene's turn counter, so the pair is derivable from the records.
	if want := core.ComputeTurnTopicID(sceneID, 1); firstID != want {
		t.Fatalf("the first read issued %d, want turn 1 (%d)", firstID, want)
	}
	if want := core.ComputeTurnTopicID(sceneID, 2); second.NewTopicID != want {
		t.Fatalf("the second read issued %d, want turn 2 (%d)", second.NewTopicID, want)
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

// A turn belongs to the scene it was opened on: when that scene is deleted, the
// domain stops holding a turn on it and the close is refused rather than written
// onto a scene that no longer exists.
func TestUpdateRejectsUnknownScene(t *testing.T) {
	srv, calls := countingLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)
	sceneID, _ := openTurn(t, db)

	if err := db.DeleteScene(core.DefaultAgentID, common.FormatHash(sceneID)); err != nil {
		t.Fatalf("DeleteScene: %v", err)
	}
	wantNoOpenTurn(t, endTurn(db, 1000))
	if got := calls.Load(); got != 0 {
		t.Fatalf("a turn with no scene behind it reached the LLM %d times", got)
	}
	if n := countRecords(db.engine, core.DefaultAgentID, core.RecL2Topic); n != 0 {
		t.Fatalf("the refused close left %d topics behind", n)
	}
}

// Every turn write refuses a domain that holds no open turn, and refuses it before
// anything is stored: no read happened yet, and guessing a turn would close one
// nobody opened.
func TestUpdateRefusesWhenNoTurnIsOpen(t *testing.T) {
	srv, calls := countingLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)

	wantNoOpenTurn(t, endTurn(db, 1000))
	if got := calls.Load(); got != 0 {
		t.Fatalf("a close with no turn open reached the LLM %d times", got)
	}
	if n := countRecords(db.engine, core.DefaultAgentID, core.RecL2Topic); n != 0 {
		t.Fatalf("a close with no turn open wrote %d topics", n)
	}

	// The library can take a turn back too: deleting the topic that holds the open
	// turn clears it, so the next close is refused instead of written onto a topic
	// that is gone.
	_, topicID := openTurn(t, db)
	if err := endTurn(db, 1000); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := db.DeleteTopic(core.DefaultAgentID, common.FormatHash(topicID)); err != nil {
		t.Fatalf("DeleteTopic: %v", err)
	}
	wantNoOpenTurn(t, endTurn(db, 2000))
	if got := calls.Load(); got != 1 {
		t.Fatalf("LLM calls = %d, want the one distillation the accepted close spent", got)
	}
	if n := countRecords(db.engine, core.DefaultAgentID, core.RecL2Topic); n != 0 {
		t.Fatalf("the refused close wrote %d topics", n)
	}
}

// The close boundary checks the host's timestamp the same way the append boundary
// does, and checks it before anything is spent or stored: a seconds-scale stamp would
// settle a turn whose originals the next Dream reads as long expired, and the host
// would find out only when the transcript is gone. A refusal leaves the turn open, so
// the same close carrying a millisecond instant still settles it.
func TestUpdateRefusesATimestampInTheWrongUnit(t *testing.T) {
	srv, calls := countingLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)
	_, topicID := openTurn(t, db)

	for _, tc := range []struct {
		name string
		ts   int64
	}{
		{"seconds since the epoch", 1_700_000_000},
		{"microseconds since the epoch", 1_700_000_000_000_000},
	} {
		err := endTurn(db, tc.ts)
		if common.CodeOf(err) != common.ErrInvalidQuery {
			t.Fatalf("%s close (%d): want ErrInvalidQuery, got %v", tc.name, tc.ts, err)
		}
		if got := calls.Load(); got != 0 {
			t.Fatalf("%s close spent %d LLM calls on a turn it refused", tc.name, got)
		}
		if owned := archivesOfTopic(t, db.engine, topicID); len(owned) != 0 {
			t.Fatalf("%s close stored %d records under the open turn", tc.name, len(owned))
		}
	}
	if n := countRecords(db.engine, core.DefaultAgentID, core.RecL2Topic); n != 0 {
		t.Fatalf("a refused close created %d topics", n)
	}

	if err := endTurn(db, 1_700_000_000_000); err != nil {
		t.Fatalf("the same close with a millisecond instant: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("LLM calls = %d, want the one distillation the accepted close spent", got)
	}
}

// A close that leaves nothing to distill is refused without spending an LLM call: an
// empty keyword track written now would read back as the real distillation of a turn
// nobody can any longer quote. Both shapes of it are refused — a call that names
// nothing, and one that records only how the turn ended, which leaves the topic with
// an event and no dialogue. The two refusals are not the same shape, and that is the
// point of pinning them together: the empty one writes nothing, while the second keeps
// the outcome it carried — it is the only record that this round ever happened, and a
// refusal that discarded it would lose a fact on the way out.
func TestUpdateRejectsTurnWithNoContent(t *testing.T) {
	srv, calls := countingLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)
	openTurn(t, db)

	if _, err := db.Update(core.DefaultAgentID, core.TurnEnd{CreatedAt: 1000}); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("an empty turn end: err = %v, want ErrInvalidQuery", err)
	}
	if n := countRecords(db.engine, core.DefaultAgentID, core.RecL4Archive); n != 0 {
		t.Fatalf("a close that names nothing wrote %d content records", n)
	}
	if _, err := db.Update(core.DefaultAgentID, core.TurnEnd{Outcome: "answered", CreatedAt: 1000}); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("an outcome with no dialogue: err = %v, want ErrInvalidQuery", err)
	}
	if n := countRecords(db.engine, core.DefaultAgentID, core.RecL4Archive); n != 1 {
		t.Fatalf("the refused close kept %d records, want the one outcome event it carried", n)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("a turn with nothing to distill reached the LLM %d times", got)
	}
	if n := countRecords(db.engine, core.DefaultAgentID, core.RecL2Topic); n != 0 {
		t.Fatalf("a contentless turn wrote %d topics", n)
	}
}

// The distillation runs before the topic is written: an LLM failure must not
// leave a keywordless topic behind. The two originals the refused close wrote stay —
// they are the turn's own content, and the retry rewrites the same two slots rather
// than accumulating a third record.
func TestUpdateDistillFailureLeavesNoTopic(t *testing.T) {
	srv := failingLLMServer(t, http.StatusBadRequest)
	db := newSearchTestDB(t, srv.URL)
	_, topicID := openTurn(t, db)

	if err := endTurn(db, 1000); common.CodeOf(err) != common.ErrLLM {
		t.Fatalf("err = %v, want ErrLLM", err)
	}
	if got := countRecords(db.engine, core.DefaultAgentID, core.RecL2Topic); got != 0 {
		t.Fatalf("failed turn wrote topics: %d", got)
	}
	if owned := archivesOfTopic(t, db.engine, topicID); len(owned) != 2 {
		t.Fatalf("a refused close owns %d records, want the two originals it wrote", len(owned))
	}
}

// An extraction that yields nothing must not create a contentless topic.
func TestUpdateRejectsEmptyExtraction(t *testing.T) {
	srv := mockLLMServer(t, `{"keywords":[]}`)
	db := newSearchTestDB(t, srv.URL)
	openTurn(t, db)

	if err := endTurn(db, 1000); common.CodeOf(err) != common.ErrLLM {
		t.Fatalf("err = %v, want ErrLLM", err)
	}
	if n := countRecords(db.engine, core.DefaultAgentID, core.RecL2Topic); n != 0 {
		t.Fatalf("empty extraction wrote %d topics", n)
	}
}

// A turn distills only what its own utterances say: the label the record
// carries is what keeps the two sides apart in the prompt, so an extraction that
// sees both speakers is the check that the transcript was rendered, not glued.
func TestUpdateDistillsRenderedTranscript(t *testing.T) {
	srv, seen := recordingLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)
	openTurn(t, db)

	if err := endTurn(db, 1000); err != nil {
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

// Closing the same turn twice re-derives its track from the content the topic
// holds and creates no second topic — so an at-least-once write loop stays safe.
func TestUpdateReplayIsIdempotent(t *testing.T) {
	srv := mockLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)
	sceneID, topicID := openTurn(t, db)

	if err := endTurn(db, 1000); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := endTurn(db, 1000); err != nil {
		t.Fatalf("replay Update: %v", err)
	}
	if n := countRecords(db.engine, core.DefaultAgentID, core.RecL2Topic); n != 1 {
		t.Fatalf("topic records = %d, want 1", n)
	}
	if n := countRecords(db.engine, core.DefaultAgentID, core.RecL4Archive); n != 2 {
		t.Fatalf("content records = %d, want the two originals", n)
	}
	if owned := archivesOfTopic(t, db.engine, topicID); len(owned) != 2 {
		t.Fatalf("the replay left %d records on this turn, want the same two slots rewritten", len(owned))
	}
	res, err := db.Search(core.DefaultAgentID, SearchQuery{SceneID: common.FormatHash(sceneID)})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Topics) != 1 {
		t.Fatalf("scene surface after replay = %+v", res.Topics)
	}
}

// A revised turn is revised by closing it again with the new wording: the two slots
// the dialogue owns are rewritten in place, and settling again distills the new
// pair. The superseded wording stops being searchable and L4 holds one
// version of the turn — nothing had to be listed as owned beforehand.
func TestUpdateReplayOverwritesPriorContent(t *testing.T) {
	srv := mockLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)
	_, topicID := openTurn(t, db)
	if err := endTurn(db, 1000); err != nil {
		t.Fatalf("first Update: %v", err)
	}

	if _, err := db.Update(core.DefaultAgentID, core.TurnEnd{
		Input: "rust 的借用检查器怎么工作", Output: "同一时刻只允许一个可变借用", CreatedAt: 2000,
	}); err != nil {
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

// The close still owns one contract: the turn it settles must be a turn topic of the
// scene the read picked. A host can no longer name a topic to break that, so the
// fixture moves the domain onto the id it wants closed. A Dream-fused parent (also
// depth 1 in this scene, but derived from timestamps rather than from the turn
// counter) is refused, and so is a depth-2 topic whose id no turn counter issued, a
// turn of another scene, and an invented id. A turn Dream has sunk keeps its turn id
// and is settled again on purpose (see TestUpdateReplayKeepsASunkTurnSunk).
func TestUpdateRejectsForeignOrFusedTopic(t *testing.T) {
	srv, calls := countingLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)
	sceneID, topicID := openTurn(t, db)
	ac := testDefaultContext(db)

	// A fused group: depth-2 child under a depth-1 fused parent, as Dream leaves it.
	fusedChild := newTopic(common.HashID("a topic no turn counter issued"), sceneID, 100, []string{"kw"})
	fusedParent := core.ComputeFusedTopicID(sceneID, 500, 600, []uint64{fusedChild.ID})
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
		{"a depth-2 topic no turn counter issued", fusedChild.ID},
		{"another scene's turn", core.ComputeTurnTopicID(sceneID+1, 1)},
		{"invented id", common.HashID("not-a-turn-this-scene-opened")},
	}
	for _, tc := range cases {
		ac.Turn = tc.topicID
		if err := endTurn(db, 1000); common.CodeOf(err) != common.ErrInvalidQuery {
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
		t.Fatalf("the depth-2 fixture was modified: %+v (%v)", child, err)
	}

	// The turn the read actually minted still closes.
	ac.Turn = topicID
	if err := endTurn(db, 1000); err != nil {
		t.Fatalf("Update of the opened turn: %v", err)
	}
}

// A turn Dream has sunk is still a turn this scene opened, so closing it again is a
// rewrite and not an error — and the rewrite must not undo the consolidation:
// depth, parent link and the host's own name come off the stored record, so the turn
// stays under its fused group while its keyword track is refreshed. The gate judges
// the key; where the turn sits is the settle write's answer.
func TestUpdateReplayKeepsASunkTurnSunk(t *testing.T) {
	srv, _ := countingLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)
	sceneID, topicID := openTurn(t, db)
	if err := endTurn(db, 1000); err != nil {
		t.Fatalf("first close: %v", err)
	}

	ac := testDefaultContext(db)
	fusedParent := core.ComputeFusedTopicID(sceneID, 500, 600, []uint64{topicID})
	writeTopicCached(t, ac, db.engine, core.DefaultAgentID, newTopic(fusedParent, sceneID, 500, []string{"kw"}))
	sunk, err := core.ReadTopicSlot(db.engine, core.DefaultAgentID, topicID)
	if err != nil {
		t.Fatalf("read the settled turn: %v", err)
	}
	sunk.Depth = 2
	sunk.ParentID = &fusedParent
	sunk.Name = "宿主给的名字"
	writeTopicCached(t, ac, db.engine, core.DefaultAgentID, *sunk)

	if err := endTurn(db, 1000); err != nil {
		t.Fatalf("replaying a sunk turn: %v", err)
	}
	after, err := core.ReadTopicSlot(db.engine, core.DefaultAgentID, topicID)
	if err != nil {
		t.Fatalf("read the replayed turn: %v", err)
	}
	if after.Depth != 2 || after.ParentID == nil || *after.ParentID != fusedParent {
		t.Fatalf("the replay brought a sunk turn back to the surface: depth=%d parent=%v",
			after.Depth, after.ParentID)
	}
	if after.Name != "宿主给的名字" {
		t.Fatalf("the replay renamed the turn: %q", after.Name)
	}
	if want := []string{"rust", "所有权"}; !slices.Equal(after.FusedKeywords, want) {
		t.Fatalf("keyword track = %v, want the distilled %v", after.FusedKeywords, want)
	}
}

// A decision-loop kernel can end one round twice: the arm that suspends it and the
// resume that finishes it are separate invocations, and each hands its own closing
// call over. This is what a turn then keeps — the dialogue is the pair the *last* close
// stated (Seq 1 and 2 are this turn's dialogue, not a log of every exchange), while
// both endings stay on the event track as their own records. A host that must keep an
// earlier arm's words records them with AppendArchive while the round runs; the close
// is not where a turn's history accumulates.
func TestTwoClosesOfOneTurnKeepBothEndingsAndTheLastDialogue(t *testing.T) {
	srv := mockLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)
	_, topicID := openTurn(t, db)

	first, err := db.Update(core.DefaultAgentID, core.TurnEnd{
		Input: "要不要换成 mmap", Output: "换成 mmap，读路径零拷贝",
		Outcome: "suspended", CreatedAt: 1000,
	})
	if err != nil {
		t.Fatalf("close at suspension: %v", err)
	}
	second, err := db.Update(core.DefaultAgentID, core.TurnEnd{
		Input: "那写入呢", Output: "写入走 msync",
		Outcome: "done", CreatedAt: 2000,
	})
	if err != nil {
		t.Fatalf("close after the resume: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("the two closes settled into two topics (%d, %d), want the turn's one",
			first.ID, second.ID)
	}
	if n := countRecords(db.engine, core.DefaultAgentID, core.RecL2Topic); n != 1 {
		t.Fatalf("topic records = %d, want 1", n)
	}

	// Addressed by slot, because the slot is what says which line of the turn this is;
	// the record scan order is not the turn's order.
	dialogue := map[uint64]string{}
	endings := map[string]bool{}
	for _, arc := range archivesOfTopic(t, db.engine, topicID) {
		switch arc.Kind {
		case core.KindUtterance:
			dialogue[arc.Seq] = arc.Content
		case core.KindEvent:
			if arc.EventType != outcomeEvent {
				t.Fatalf("an event this turn did not close with: %+v", arc)
			}
			endings[arc.Content] = true
		}
	}
	if len(dialogue) != 2 || dialogue[core.SeqUser] != "那写入呢" || dialogue[core.SeqAgent] != "写入走 msync" {
		t.Fatalf("the turn's dialogue = %v, want the last close's pair on Seq %d and %d",
			dialogue, core.SeqUser, core.SeqAgent)
	}
	if len(endings) != 2 || !endings["suspended"] || !endings["done"] {
		t.Fatalf("the turn's endings = %v, want both arms' outcomes kept apart", endings)
	}
}
