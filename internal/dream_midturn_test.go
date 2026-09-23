// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"context"
	"testing"
	"time"

	"github.com/qyiun666/MemHop/internal/repo/core"
)

// A host that schedules consolidation on a timer runs Dream while the round it is
// driving is still open. The library holds that turn, and what the round has already
// written is the only evidence it is halfway through, so consolidation may neither
// drop the turn held open nor sweep those records: the close that was pending before
// the Dream still settles the same turn afterwards.
func TestDreamLeavesTheOpenTurnCloseable(t *testing.T) {
	srv := mockLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)
	sess, err := db.NewSession(core.DefaultAgentID)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	_, topicID := openTurn(t, db)

	if _, err := db.AppendArchive(core.DefaultAgentID, core.ArchiveSlot{
		Kind: core.KindEvent, EventType: "tool_result", Content: "half a round",
		CreatedAt: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatalf("AppendArchive mid-round: %v", err)
	}

	if _, err := sess.Dream(context.Background(), ""); err != nil {
		t.Fatalf("Dream with a turn open: %v", err)
	}
	kept := archivesOfTopic(t, db.engine, topicID)
	if len(kept) != 1 || kept[0].Content != "half a round" {
		t.Fatalf("the open turn's records after Dream = %+v, want the one event kept", kept)
	}

	if err := endTurn(db, time.Now().UnixMilli()); err != nil {
		t.Fatalf("close the turn that was open across a Dream: %v", err)
	}
	if got := archivesOfTopic(t, db.engine, topicID); len(got) != 3 {
		t.Fatalf("turn records after closing = %d, want the event plus the two dialogue slots",
			len(got))
	}
}
