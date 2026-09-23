// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// The bounds a settled turn leaves on its topic row are the span of its utterances. A tool
// event recorded mid-round carries a timestamp of its own and must not widen them: a host
// dating a memory by its row reads when that round was spoken, not when a call inside it
// happened to finish.
func TestTurnTopicBoundsIgnoreMidRoundEvents(t *testing.T) {
	srv := mockLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)
	_, topicID := openTurn(t, db)

	if _, err := db.AppendArchive(core.DefaultAgentID, core.ArchiveSlot{
		TopicID: topicID, Kind: core.KindEvent, EventType: "tool_call",
		ContentType: core.ContentText, Content: "read config/retry.yaml", CreatedAt: 9000,
	}); err != nil {
		t.Fatalf("append mid-round event: %v", err)
	}
	topic, err := db.Update(core.DefaultAgentID, core.TurnEnd{
		Input: userTurnText, Output: agentTurnText, CreatedAt: 1000,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if topic.UserTimestamp != 1000 || topic.AgentTimestamp != 1000 {
		t.Fatalf("a mid-round event widened the row's bounds: %d/%d, want 1000/1000",
			topic.UserTimestamp, topic.AgentTimestamp)
	}
	owned := archivesOfTopic(t, db.engine, topicID)
	if len(owned) != 3 {
		t.Fatalf("topic owns %d archives, want the event plus the two originals", len(owned))
	}
}

// A round abandoned to a second `Search` is not unwritten: it gets no topic row, so the scene
// read never shows it, while the record it appended stays on the log under an id no read
// names any more. Both halves of that sentence are what the guide's pitfall 5 claims, so
// both are asserted here — a host told "abandoning costs nothing" would size its retention
// window on a lie.
func TestAbandonedRoundKeepsItsRecords(t *testing.T) {
	srv := mockLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)
	_, abandoned := openTurn(t, db)

	if _, err := db.AppendArchive(core.DefaultAgentID, core.ArchiveSlot{
		TopicID: abandoned, Kind: core.KindUtterance, Role: core.RoleUser,
		ContentType: core.ContentText, Content: "这半轮被放弃了", CreatedAt: 1500,
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	second, err := db.Search(core.DefaultAgentID, SearchQuery{})
	if err != nil {
		t.Fatalf("second Search: %v", err)
	}
	if _, err := db.Update(core.DefaultAgentID, core.TurnEnd{
		Input: userTurnText, Output: agentTurnText, Outcome: "answered", CreatedAt: 2000,
	}); err != nil {
		t.Fatalf("settle the second round: %v", err)
	}

	sc, err := db.SceneContext(core.DefaultAgentID, "")
	if err != nil {
		t.Fatalf("SceneContext: %v", err)
	}
	if len(sc.Topics) != 1 {
		t.Fatalf("the scene read listed %d rows, want only the settled round", len(sc.Topics))
	}
	if got := common.FormatHash(abandoned); sc.Topics[0].TopicID == got {
		t.Fatalf("the abandoned round appeared on the scene read: %+v", sc.Topics[0])
	}

	// The abandoned record is still there, and only an id the host no longer holds reaches it.
	key := common.FormatHash(abandoned)
	still, err := db.SearchL4(core.DefaultAgentID, core.L4Query{TopicID: &key})
	if err != nil {
		t.Fatalf("SearchL4 by the abandoned turn's id: %v", err)
	}
	if len(still) != 1 || still[0].Content != "这半轮被放弃了" {
		t.Fatalf("the abandoned round's record did not survive: %+v", still)
	}
	whole, err := db.SearchL4(core.DefaultAgentID, core.L4Query{})
	if err != nil {
		t.Fatalf("SearchL4 across the domain: %v", err)
	}
	if len(whole) != 4 {
		t.Fatalf("the domain holds %d archives, want the abandoned line plus the settled round's three", len(whole))
	}
	// The read that lists rounds cannot see it, so an abandoned record is invisible to recall
	// while still costing the file its bytes.
	if _, err := db.Search(core.DefaultAgentID, SearchQuery{}); err != nil {
		t.Fatalf("third Search: %v", err)
	}
	if second.NewTopicID == abandoned {
		t.Fatal("the second Search handed back the abandoned turn's id; the counter did not move")
	}
}
