// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//go:build integration

package test

import (
	"context"
	"testing"
	"time"

	memhop "github.com/qyiun666/MemHop/api"
	"github.com/qyiun666/MemHop/test/testsupport"
)

// TestE2EUpdateDream exercises the full memory loop against a real LLM:
// open the host session → settle one turn → read the session back → L4
// archive readback → Dream on that session → readable afterwards.
func TestE2EUpdateDream(t *testing.T) {
	db := testsupport.OpenMemHop(t)
	defer db.Close()

	ts := time.Now().UnixMilli()
	userText := "我喜欢在周末去海边跑步，尤其是清晨人少的时候"
	agentText := "海边晨跑很不错，空气清新还能看日出，记得做好防晒"

	// 1. Opening a fresh session hands back its empty surface and the topic id
	// of the turn it just opened.
	res, err := db.Search(memhop.SearchQuery{})
	if err != nil {
		t.Fatalf("Search(fresh): %v", err)
	}
	sceneID := res.Scene.SceneID
	if len(res.Topics) != 0 {
		t.Fatalf("fresh session returned %d topics", len(res.Topics))
	}
	if res.NewTopicID == "" {
		t.Fatal("the opening read must issue the turn's topic id")
	}

	// 2. The turn's content is appended under that topic, then settled: one
	// topic with distilled keywords over the two originals.
	topicID := res.NewTopicID
	if err := db.SettleTurn(sceneID, topicID, userText, agentText, ts); err != nil {
		t.Fatalf("settle turn: %v", err)
	}

	// 3. The session read hands the turn back.
	res2, err := db.Search(memhop.SearchQuery{SceneID: sceneID})
	if err != nil {
		t.Fatalf("Search(session): %v", err)
	}
	if len(res2.Topics) != 1 || res2.Topics[0].ID != topicID {
		t.Fatalf("surface = %+v, want the one turn %s", res2.Topics, topicID)
	}
	if len(res2.Topics[0].FusedKeywords) == 0 {
		t.Fatal("the turn topic carries no keywords")
	}
	t.Logf("topic %s keywords=%v", topicID, res2.Topics[0].FusedKeywords)

	// 4. L4 archive readback: TopicID is an overlay filter, so combine it with
	//    a primary mode (time range) to select the topic's archives.
	archives, err := db.SearchL4(memhop.L4Query{
		Start:   ts - 1000,
		End:     ts + 5000,
		TopicID: &topicID,
	})
	if err != nil {
		t.Fatalf("SearchL4: %v", err)
	}
	if len(archives) != 2 {
		t.Fatalf("expected 2 archives for the turn, got %d", len(archives))
	}
	for _, a := range archives {
		if a.Content != userText && a.Content != agentText {
			t.Errorf("archive content is not an original: %.40s", a.Content)
		}
	}

	// 5. Dream on this session (L2 compression + L1 rebuild/decay).
	rep, err := db.Dream(context.Background(), sceneID)
	if err != nil {
		t.Fatalf("Dream: %v", err)
	}
	if rep == nil {
		t.Fatal("Dream returned nil report")
	}
	t.Logf("dream report: consolidated=%d compressed=%d stages=%d", rep.ConsolidatedScenes, rep.L2TopicsCompressed, len(rep.Stages))

	// 6. After Dream the session must still read back.
	res3, err := db.Search(memhop.SearchQuery{SceneID: sceneID})
	if err != nil {
		t.Fatalf("Search after Dream: %v", err)
	}
	t.Logf("post-dream session has %d surface topic(s)", len(res3.Topics))
}

// TestE2EL0Profile covers L0 profile read/update round-trip.
func TestE2EL0Profile(t *testing.T) {
	db := testsupport.OpenMemHop(t)
	defer db.Close()

	// Fresh DB: GetL0 returns an empty profile (no ErrNotFound).
	slot, err := db.GetL0()
	if err != nil {
		t.Fatalf("GetL0: %v", err)
	}
	slot.Personality = "热爱户外运动的用户"
	if err := db.UpdateL0(slot); err != nil {
		t.Fatalf("UpdateL0: %v", err)
	}
	got, err := db.GetL0()
	if err != nil {
		t.Fatalf("GetL0 after update: %v", err)
	}
	if got.Personality != "热爱户外运动的用户" {
		t.Fatalf("Personality mismatch: %q", got.Personality)
	}
}
