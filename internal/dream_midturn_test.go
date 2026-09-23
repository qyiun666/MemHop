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
// drop the turn held open nor sweep records that are still inside the window: the close
// that was pending before the Dream still settles the same turn afterwards. What a pass
// does not exempt is the clock - see TestDreamSweepsAnExpiredMidRoundRecordAndStillClosesTheTurn.
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

// Retention measures the stamp, not the round's state: a record appended into a round that is
// still open is swept as soon as its own clock passes the window, exactly like a settled one.
// What the pass must not do is take the open turn with it. TestDreamLeavesTheOpenTurnCloseable
// pins the in-window half; this pins the other side, so nobody reads "Dream spares the round in
// progress" as "Dream spares everything that round recorded" - the case a host meets when a
// round sits open across a long pause (a tool result backfilled with its own older stamp, or a
// suspension outlasting the window).
func TestDreamSweepsAnExpiredMidRoundRecordAndStillClosesTheTurn(t *testing.T) {
	srv := mockLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)
	db.config.Defaults.ContentRetentionMs = 1000
	sess, err := db.NewSession(core.DefaultAgentID)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	_, topicID := openTurn(t, db)

	if _, err := db.AppendArchive(core.DefaultAgentID, core.ArchiveSlot{
		Kind: core.KindEvent, EventType: "tool_result", Content: "钟比窗口还老的轮中记录",
		CreatedAt: time.Now().Add(-time.Hour).UnixMilli(),
	}); err != nil {
		t.Fatalf("AppendArchive mid-round: %v", err)
	}
	if kept := archivesOfTopic(t, db.engine, topicID); len(kept) != 1 {
		t.Fatalf("the round holds %d records before the pass, want the one appended", len(kept))
	}

	if _, err := sess.Dream(context.Background(), ""); err != nil {
		t.Fatalf("Dream with a turn open: %v", err)
	}
	if kept := archivesOfTopic(t, db.engine, topicID); len(kept) != 0 {
		t.Fatalf("the expired mid-round record survived the sweep: %+v", kept)
	}

	// The turn itself survived: the pending close still settles it, on its own fresh clock.
	if err := endTurn(db, time.Now().UnixMilli()); err != nil {
		t.Fatalf("close the round the sweep passed through: %v", err)
	}
	scenes, err := db.ListScenes(core.DefaultAgentID, "")
	if err != nil || len(scenes) != 1 {
		t.Fatalf("ListScenes = %+v err %v", scenes, err)
	}
	ctx, err := db.SceneContext(core.DefaultAgentID, "")
	if err != nil {
		t.Fatalf("SceneContext: %v", err)
	}
	if len(ctx.Topics) != 1 || len(ctx.Topics[0].Messages) != 2 {
		t.Fatalf("the closed round reads back as %+v, want one row carrying the pair", ctx.Topics)
	}
}
