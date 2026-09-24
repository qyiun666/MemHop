// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// What a partial sweep may and may not do. Each record ages on its own stamp, so an
// event written early can be gone while a later one survives — and the survivor has to
// keep the `Seq` it was written at. Nothing compacts: a turn is addressed by
// (topic, Seq), so a retried append to slot 7 must still mean slot 7 after a sweep, and
// both reads of that turn have to agree on it.
//
// The dialogue is the other half: one `Update` stamps a turn's two originals together,
// so they age as a pair and the ordinary expiry leaves `Messages` **empty** rather than
// gapped. A hole in `Messages` appears only when the host addressed a slot of its own,
// which is the extra utterance at Seq 9 below.

package internal

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

func TestSweepKeepsEverySurvivorAtItsOwnSeq(t *testing.T) {
	srv := contractLLMServer(t)
	db := newSearchTestDB(t, srv.URL)
	sceneID, topicID := openTurn(t, db)
	now := time.Now().UnixMilli()

	for _, e := range []struct {
		seq   uint64
		stamp int64
	}{{3, 1000}, {5, 1000}, {7, now}} {
		if _, err := db.AppendArchive(core.DefaultAgentID, core.ArchiveSlot{
			Kind: core.KindEvent, Seq: e.seq, ContentType: core.ContentText,
			EventType: "tool_call", Content: "step", CreatedAt: e.stamp,
		}); err != nil {
			t.Fatalf("append event at seq %d: %v", e.seq, err)
		}
	}
	if _, err := db.AppendArchive(core.DefaultAgentID, core.ArchiveSlot{
		Kind: core.KindUtterance, Seq: 9, ContentType: core.ContentText, Role: core.RoleUser,
		Content: "a note that will age out", CreatedAt: 1000,
	}); err != nil {
		t.Fatalf("append the extra utterance: %v", err)
	}
	appendTurn(t, db, now)
	// settle's guard, on a fresh stamp: the dialogue pair has to be inside the window
	// while the records written early are not.
	if ac := db.agents[core.DefaultAgentID]; ac == nil || ac.Scene != sceneID || ac.Turn != topicID {
		t.Fatal("the turn this test settles is not the one the domain holds open")
	}
	if err := endTurn(db, now); err != nil {
		t.Fatalf("end turn: %v", err)
	}
	topicHex := common.FormatHash(topicID)
	if got := eventSeqs(t, db, topicHex); !slices.Equal(got, []uint64{3, 5, 7}) {
		t.Fatalf("the turn's events did not start at 3,5,7: %v", got)
	}
	if got := messageSeqs(t, db, sceneID, topicID); !slices.Equal(got, []uint64{1, 2, 9}) {
		t.Fatalf("the turn's utterances did not start at 1,2,9: %v", got)
	}

	if _, err := db.RunDream(context.Background(), core.DefaultAgentID, 0); err != nil {
		t.Fatalf("dream: %v", err)
	}

	if got := eventSeqs(t, db, topicHex); !slices.Equal(got, []uint64{7}) {
		t.Fatalf("the sweep renumbered the event track: want [7], got %v", got)
	}
	if got := messageSeqs(t, db, sceneID, topicID); !slices.Equal(got, []uint64{1, 2}) {
		t.Fatalf("the sweep renumbered the dialogue: want [1 2], got %v", got)
	}
}

func eventSeqs(t *testing.T, db *DB, topicHex string) []uint64 {
	t.Helper()
	kind := core.KindEvent
	found, err := db.SearchL4(core.DefaultAgentID, L4Query{TopicID: &topicHex, Kind: &kind})
	if err != nil {
		t.Fatalf("SearchL4 events: %v", err)
	}
	seqs := make([]uint64, 0, len(found))
	for _, a := range found {
		seqs = append(seqs, a.Seq)
	}
	slices.Sort(seqs)
	return seqs
}

func messageSeqs(t *testing.T, db *DB, sceneID, topicID uint64) []uint64 {
	t.Helper()
	cctx, err := db.SceneContext(core.DefaultAgentID, common.FormatHash(sceneID))
	if err != nil {
		t.Fatalf("scene context: %v", err)
	}
	for _, topic := range cctx.Topics {
		if topic.TopicID != common.FormatHash(topicID) {
			continue
		}
		seqs := make([]uint64, 0, len(topic.Messages))
		for _, m := range topic.Messages {
			seqs = append(seqs, m.Seq)
		}
		slices.Sort(seqs)
		return seqs
	}
	t.Fatalf("the settled turn is missing from the scene read: %+v", cctx.Topics)
	return nil
}
