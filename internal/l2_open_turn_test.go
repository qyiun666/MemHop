// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"testing"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// A round that has been opened but not closed has no topic row: the row is minted by
// `Search` and created by `Update`. So the scene read — which is what a recall loop uses
// between its own steps — cannot show the round in progress, and a loop that recalls
// several times per round never feeds its own half-written content back to the model.
// What the round DID record is not lost: it is readable mid-round under the minted topic
// id, and it appears as that turn's messages once the turn settles.
func TestOpenTurnIsAbsentUntilItSettles(t *testing.T) {
	srv := mockLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)
	sceneID, topicID := openTurn(t, db)
	topicHex := common.FormatHash(topicID)

	rows := func(when string) int {
		t.Helper()
		sc, err := db.SceneContext(core.DefaultAgentID, "")
		if err != nil {
			t.Fatalf("scene context %s: %v", when, err)
		}
		for _, tp := range sc.Topics {
			if tp.TopicID == topicHex {
				t.Fatalf("the open turn showed on the scene read %s: %+v", when, tp)
			}
		}
		return len(sc.Topics)
	}
	if got := rows("before anything was recorded"); got != 0 {
		t.Fatalf("a scene whose only turn is open listed %d rows, want 0", got)
	}

	uttered := core.ArchiveSlot{
		TopicID: topicID, Kind: core.KindUtterance, Role: core.RoleUser,
		ContentType: core.ContentText, Content: "半轮里先记下的一句", CreatedAt: time.Now().UnixMilli(),
	}
	if _, err := db.AppendArchive(core.DefaultAgentID, uttered); err != nil {
		t.Fatalf("append mid-round: %v", err)
	}
	event := core.ArchiveSlot{
		TopicID: topicID, Kind: core.KindEvent, EventType: "tool_call",
		ContentType: core.ContentText, Content: `{"tool":"grep"}`, CreatedAt: time.Now().UnixMilli(),
	}
	if _, err := db.AppendArchive(core.DefaultAgentID, event); err != nil {
		t.Fatalf("append event mid-round: %v", err)
	}
	if got := rows("after this round recorded a line and an event"); got != 0 {
		t.Fatalf("the open turn reached the scene read mid-round: %d rows, want 0", got)
	}

	// The other channel does answer mid-round, by the id `Search` handed over.
	mid, err := db.SearchL4(core.DefaultAgentID, L4Query{TopicID: &topicHex})
	if err != nil {
		t.Fatalf("read this round mid-round: %v", err)
	}
	if len(mid) != 2 {
		t.Fatalf("mid-round read of this round gave %d records, want the line and the event", len(mid))
	}

	if err := settle(db, sceneID, topicID); err != nil {
		t.Fatalf("settle: %v", err)
	}
	sc, err := db.SceneContext(core.DefaultAgentID, "")
	if err != nil {
		t.Fatalf("scene context after settle: %v", err)
	}
	if len(sc.Topics) != 1 {
		t.Fatalf("after settling, the scene lists %d rows, want this round's one topic", len(sc.Topics))
	}
	closed := sc.Topics[0]
	if closed.TopicID != topicHex || closed.Depth != 1 || closed.ChildCount != 0 {
		t.Fatalf("the settled turn is not the one Search opened: %+v", closed)
	}
	if len(closed.Keywords) == 0 {
		t.Fatalf("settling left the turn with no keyword track: %+v", closed)
	}
	// What the round recorded mid-round is now part of the turn: the line it appended plus
	// the two dialogue slots `Update` filled. The event stays out of the transcript.
	var sawAppended bool
	for _, m := range closed.Messages {
		if m.Content == uttered.Content {
			sawAppended = true
		}
		if m.Role == core.RoleDream {
			t.Fatalf("a turn's own row carries a consolidated summary: %+v", m)
		}
	}
	if !sawAppended {
		t.Fatalf("the line recorded mid-round never reached the settled turn: %+v", closed.Messages)
	}
	if len(closed.Messages) != 3 {
		t.Fatalf("settled utterances = %d, want 2 slots + the recorded line: %+v", len(closed.Messages), closed.Messages)
	}

	// Positive control on the same read: open a second turn and the listing still answers
	// with exactly the settled one. So "absent" above is this read's rule, not a listing
	// that fails to see anything.
	next, err := db.Search(core.DefaultAgentID, SearchQuery{SceneID: common.FormatHash(sceneID)})
	if err != nil {
		t.Fatalf("open the next turn: %v", err)
	}
	sc, err = db.SceneContext(core.DefaultAgentID, "")
	if err != nil {
		t.Fatalf("scene context with a turn open: %v", err)
	}
	if len(sc.Topics) != 1 || sc.Topics[0].TopicID != topicHex {
		t.Fatalf("the read listed the open turn too: %+v", sc.Topics)
	}
	if common.FormatHash(next.NewTopicID) == topicHex {
		t.Fatal("the second read re-opened the turn that already settled")
	}
}
