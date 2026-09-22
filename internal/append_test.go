// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// The content write path: who owns which field of an appended record, and what one
// topic's shared Seq space does when two kinds want the same slot.

package internal

import (
	"strings"
	"testing"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/content"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

func utterance(seq uint64, role uint8, text string, ts int64) core.ArchiveSlot {
	return core.ArchiveSlot{
		Kind: core.KindUtterance, Seq: seq, Role: role, Content: text, CreatedAt: ts,
	}
}

// An append adopts what the utterance kind owns — speaker and medium — and derives
// the addressing from the topic it was given, so a host can read a record back,
// change it, and write it to the slot it came from.
func TestAppendArchiveAdoptsDeclaredUtteranceFields(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	sceneHex, hex, topicID := newTurnKey(t, db)

	if _, err := db.AppendArchive(core.DefaultAgentID, sceneHex, hex, core.ArchiveSlot{
		Kind: core.KindUtterance, Seq: core.SeqUser, Role: core.RoleSystem,
		ContentType: core.ContentImage, IDHash: 4242, TopicID: 4242,
		Content: "img://cat.png", CreatedAt: 1000,
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	owned := archivesOfTopic(t, db.engine, topicID)
	if len(owned) != 1 {
		t.Fatalf("topic owns %d records, want 1", len(owned))
	}
	got := owned[0]
	if got.Role != core.RoleSystem || got.ContentType != core.ContentImage || got.Seq != core.SeqUser {
		t.Fatalf("declared axes lost: %+v", got)
	}
	if got.TopicID != topicID || got.IDHash != core.HashContent(topicID, core.SeqUser) {
		t.Fatalf("an append must be addressed by the topic it named, not by what it passed: %+v", got)
	}
}

// Seq is one space the topic's two kinds share, and taking a held slot overwrites it
// instead of erroring — that is what lets a replayed turn converge. It reaches
// across kind: naming an event's slot replaces the event.
func TestAppendArchiveOverwritesAcrossKinds(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	sceneHex, hex, topicID := newTurnKey(t, db)

	if _, err := db.AppendArchive(core.DefaultAgentID, sceneHex, hex, ev("tool_call", 1000)); err != nil {
		t.Fatalf("append event: %v", err)
	}
	landed, err := db.eventsOf(core.DefaultAgentID, hex)
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	if len(landed) != 1 || landed[0].Seq != core.LastUtteranceSeq+1 {
		t.Fatalf("first event of a topic = %+v, want the slot above dialogue", landed)
	}
	taken := landed[0].Seq

	if _, err := db.AppendArchive(core.DefaultAgentID, sceneHex, hex, utterance(taken, core.RoleUser, "改口了", 1500)); err != nil {
		t.Fatalf("overwrite an event slot with an utterance: %v", err)
	}
	if still, _ := db.eventsOf(core.DefaultAgentID, hex); len(still) != 0 {
		t.Fatalf("the event survived being overwritten: %+v", still)
	}
	owned := archivesOfTopic(t, db.engine, topicID)
	if len(owned) != 1 || owned[0].Kind != core.KindUtterance || owned[0].Content != "改口了" {
		t.Fatalf("topic content after the overwrite = %+v, want the utterance only", owned)
	}
}

// Auto-allocation stays above every held slot, so appending in any order cannot
// collide: the two dialogue slots are still free for a host that names them after
// its events have landed.
func TestAppendArchiveAllocatesAboveHeldSlots(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	sceneHex, hex, topicID := newTurnKey(t, db)

	for i := 1; i <= 3; i++ {
		if _, err := db.AppendArchive(core.DefaultAgentID, sceneHex, hex, ev("tool_call", int64(100*i))); err != nil {
			t.Fatalf("append event %d: %v", i, err)
		}
	}
	if _, err := db.AppendArchive(core.DefaultAgentID, sceneHex, hex, utterance(0, core.RoleUser, "问答", 500)); err != nil {
		t.Fatalf("append an auto-allocated utterance: %v", err)
	}
	if _, err := db.AppendArchive(core.DefaultAgentID, sceneHex, hex, utterance(core.SeqUser, core.RoleUser, "命名槽位", 400)); err != nil {
		t.Fatalf("append into a reserved slot: %v", err)
	}
	var seqs []uint64
	for _, arc := range archivesOfTopic(t, db.engine, topicID) {
		seqs = append(seqs, arc.Seq)
	}
	// 1 (named), 2 free, 3/4/5 (events), 6 (auto utterance) — nothing collided.
	if len(seqs) != 5 {
		t.Fatalf("topic holds %d records, want 5: %v", len(seqs), seqs)
	}
}

// An over-budget record is refused rather than shortened, and the two kinds have
// different budgets: an event is a note about a step, an utterance is the step's
// whole text.
func TestAppendArchiveBudgets(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	sceneHex, hex, topicID := newTurnKey(t, db)

	if _, err := db.AppendArchive(core.DefaultAgentID, sceneHex, hex, core.ArchiveSlot{
		Kind: core.KindEvent, EventType: "tool_result", Content: strings.Repeat("a", content.MaxEventPayload+1), CreatedAt: 1,
	}); err == nil {
		t.Fatal("an over-budget event must be refused")
	}
	if _, err := db.AppendArchive(core.DefaultAgentID, sceneHex, hex, core.ArchiveSlot{
		Kind: core.KindUtterance, Role: core.RoleUser, Content: strings.Repeat("a", content.MaxUtterancePayload+1), CreatedAt: 1,
	}); err == nil {
		t.Fatal("an over-budget utterance must be refused, not truncated")
	}
	if _, err := db.AppendArchive(core.DefaultAgentID, sceneHex, hex, core.ArchiveSlot{
		Kind: core.KindUtterance, Role: core.RoleAgent, Content: strings.Repeat("a", content.MaxUtterancePayload), CreatedAt: 1,
	}); err != nil {
		t.Fatalf("an utterance at the budget limit: %v", err)
	}
	if owned := archivesOfTopic(t, db.engine, topicID); len(owned) != 1 {
		t.Fatalf("refused appends stored %d records, want only the accepted one", len(owned))
	}
}

// A record's timestamp is milliseconds since the epoch, and the two unit mistakes a
// host makes are refused rather than stored: a seconds-scale stamp writes a record
// the retention window sweeps at the next Dream, taking the turn's whole transcript
// with it while the sweep reports nothing, and a microsecond-scale one never expires.
// Both leave nothing behind, and the same instant in milliseconds is stored and found
// by a millisecond window.
func TestAppendArchiveRefusesATimestampInTheWrongUnit(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	sceneHex, hex, topicID := newTurnKey(t, db)
	now := time.Now()
	for _, tc := range []struct {
		name string
		ts   int64
	}{
		{"seconds since the epoch", now.Unix()},
		{"microseconds since the epoch", now.UnixMicro()},
	} {
		_, err := db.AppendArchive(core.DefaultAgentID, sceneHex, hex, core.ArchiveSlot{
			Kind: core.KindUtterance, Role: core.RoleUser, Content: "a turn", CreatedAt: tc.ts,
		})
		if common.CodeOf(err) != common.ErrInvalidQuery {
			t.Fatalf("%s (%d): want ErrInvalidQuery, got %v", tc.name, tc.ts, err)
		}
		if owned := archivesOfTopic(t, db.engine, topicID); len(owned) != 0 {
			t.Fatalf("%s: a refused timestamp stored %d records", tc.name, len(owned))
		}
	}

	ms := now.UnixMilli()
	if _, err := db.AppendArchive(core.DefaultAgentID, sceneHex, hex, core.ArchiveSlot{
		Kind: core.KindUtterance, Role: core.RoleUser, Content: "a turn", CreatedAt: ms,
	}); err != nil {
		t.Fatalf("a millisecond timestamp must be accepted: %v", err)
	}
	got, err := db.SearchL4(core.DefaultAgentID, core.L4Query{TopicID: &hex, Start: ms, End: ms})
	if err != nil {
		t.Fatalf("millisecond window: %v", err)
	}
	if len(got) != 1 || got[0].CreatedAt != ms {
		t.Fatalf("millisecond window read %+v, want the one record stamped %d", got, ms)
	}
}
