// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// A host running the loop reads the same scene twice in a row and by two doors: `Search`
// opens the next turn and hands back the surface topics, `SceneContext` reads the same
// scene as a transcript down to depth 2. Both are served from the same L2Meta cache, so
// they must not offer two answers about one scene — different rows, different order, or
// different per-row fields would each be a host bug waiting to happen. This pins the depth-1
// rows of the transcript read against the turn-opening read, after a consolidation pass so
// the two levels actually differ.

package test

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	memhop "github.com/qyiun666/MemHop/api"
	internal "github.com/qyiun666/MemHop/internal"
)

func TestInterfaceSceneReadsAgreeOnTheirSharedRows(t *testing.T) {
	llm := newMockLLM(t)
	m := openMockDB(t, filepath.Join(t.TempDir(), "parity.meh"), llm.srv.URL,
		func(d *internal.MemHopDefaults) { d.DreamCompressMinTopics = 2 })
	db := newTestDB(t, m)
	defer db.Close()

	base := time.Now().Add(-time.Hour).UnixMilli()
	stamps := []int64{}
	for i := range 4 {
		stamps = append(stamps, base-int64(4-i)*60000)
	}
	scoped := openSession(t, db)
	ids := []string{}
	for i, pair := range [][2]string{
		{"用户要求重构登录模块", "登录模块开始重构"},
		{"继续把 token 校验挪进去", "token 校验已挪进去"},
		{"顺手补了两条测试", "测试补上了"},
		{"最后确认一遍命名", "命名一致"},
	} {
		if _, err := db.Search(memhop.SearchQuery{}); err != nil {
			t.Fatalf("open turn %d: %v", i, err)
		}
		closed, err := db.Update(memhop.TurnEnd{Input: pair[0], Output: pair[1],
			Outcome: "done", CreatedAt: stamps[i]})
		if err != nil {
			t.Fatalf("close turn %d: %v", i, err)
		}
		ids = append(ids, closed.ID)
	}

	if _, err := db.Dream(context.Background(), scoped); err != nil {
		t.Fatalf("Dream: %v", err)
	}

	turn, err := db.Search(memhop.SearchQuery{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	transcript, err := db.SceneContext(scoped)
	if err != nil {
		t.Fatalf("SceneContext: %v", err)
	}

	// The transcript carries both levels; the turn-opening read carries the surface only.
	surface := []memhop.SceneContextTopic{}
	for _, row := range transcript.Topics {
		if row.Depth == 1 {
			surface = append(surface, row)
		}
	}
	if len(turn.Topics) != len(surface) {
		t.Fatalf("Search listed %d topics while the transcript's depth-1 rows are %d",
			len(turn.Topics), len(surface))
	}
	if len(transcript.Topics) <= len(surface) {
		t.Fatalf("the transcript has %d rows against %d at depth 1, want the sunk turns visible too",
			len(transcript.Topics), len(surface))
	}
	for i := range surface {
		if turn.Topics[i].ID != surface[i].TopicID {
			t.Fatalf("row %d is %s from Search and %s from SceneContext, so the two reads disagree on order",
				i, turn.Topics[i].ID, surface[i].TopicID)
		}
		if turn.Topics[i].UserTimestamp != surface[i].UserTimestamp ||
			turn.Topics[i].AgentTimestamp != surface[i].AgentTimestamp {
			t.Fatalf("row %s carries different time bounds per read: %+v vs %+v",
				surface[i].TopicID, turn.Topics[i], surface[i])
		}
		if !slices.Equal(turn.Topics[i].FusedKeywords, surface[i].Keywords) {
			t.Fatalf("row %s has the keyword track %q in one read and %q in the other",
				surface[i].TopicID, turn.Topics[i].FusedKeywords, surface[i].Keywords)
		}
	}

	// The shared rows are the same set the turns were settled as, whichever way they were
	// folded: a sunk turn must still be readable below its group.
	gone := 0
	for _, id := range ids {
		if !slices.ContainsFunc(transcript.Topics, func(row memhop.SceneContextTopic) bool {
			return row.TopicID == id
		}) {
			gone++
		}
	}
	if gone == len(ids) {
		t.Fatal("every settled turn vanished from the transcript read after consolidation")
	}

	// And the order is the spoken order, not the cache's accident: the surface rows ascend
	// by the timestamp a host stamped.
	if !strings.Contains(transcript.SceneName, "session:") && transcript.SceneName != "" {
		t.Fatalf("unexpected scene name %q", transcript.SceneName)
	}
	for i := 1; i < len(surface); i++ {
		if surface[i-1].UserTimestamp > surface[i].UserTimestamp {
			t.Fatalf("depth-1 rows are out of spoken order at %d: %d after %d",
				i, surface[i].UserTimestamp, surface[i-1].UserTimestamp)
		}
	}
}
