// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//go:build integration

package test

import (
	"context"
	"testing"
	"time"

	internal "github.com/qyiun666/MemHop/internal"
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/test/testsupport"
)

// TestLocomoDebug ingests one locomo session and queries it, printing the raw
// retrieved context so we can see why large-scale recall scored 0.
func TestLocomoDebug(t *testing.T) {
	items := loadLocomo10(t, 1)
	item := items[0]
	t.Logf("item %s: %d sessions, %d qa", item.SampleID, len(item.Sessions), len(item.QA))

	db := testsupport.OpenMemHop(t)
	defer db.Close()

	// Ingest only the FIRST session to keep it fast, via the normal retrieval
	// route (no AutoCreate/DirectedL2ID) with the absolute time parsed from
	// the session id.
	sess := item.Sessions[0]
	sessionBase := locomoSessionBaseTS(sess.ID)
	if sessionBase == 0 {
		sessionBase = time.Now().UnixMilli()
	}
	var activeTopic string
	for i, tn := range sess.Turns {
		ts := sessionBase + int64(i)*30_000
		if tn.Speaker == item.SpeakerA {
			res, err := db.Search(context.Background(), internal.SearchQuery{Text: tn.Text, Timestamp: ts})
			if err != nil {
				t.Fatalf("ingest Search: %v", err)
			}
			activeTopic = common.FormatHash(res.NewTopicID)
		} else if activeTopic != "" {
			if _, err := db.Update(activeTopic, tn.Text, ts); err != nil {
				t.Fatalf("ingest Update: %v", err)
			}
		}
	}
	t.Logf("ingested session %s: %d turns, sessionBase=%d", sess.ID, len(sess.Turns), sessionBase)

	// Query with the first QA and dump the raw context.
	qa := item.QA[0]
	t.Logf("question: %s", qa.Question)
	t.Logf("reference answer: %s", qa.Answer)

	res, err := db.Search(context.Background(), internal.SearchQuery{Text: qa.Question, Timestamp: sessionBase + 90_000})
	if err != nil {
		t.Fatalf("query Search: %v", err)
	}
	t.Logf("contexts returned: %d", len(res.Contexts))
	fmtTS := func(v int64) string {
		if v <= 0 {
			return "-"
		}
		return time.UnixMilli(v).UTC().Format("2006-01-02 15:04")
	}
	for i := range res.Contexts {
		c := &res.Contexts[i]
		t.Logf("ctx[%d] id=%d scene=%d depth=%d user=%s agent=%s user_kw=%v agent_kw=%v fused_kw=%v l4refs=%v",
			i, c.ID, c.SceneID, c.Depth, fmtTS(c.UserTimestamp), fmtTS(c.AgentTimestamp), c.UserKeywords, c.AgentKeywords, c.FusedKeywords, c.L4Refs)
	}
	ctxText := gatherLocomoContext(db, res)
	t.Logf("gathered context (%d chars):\n%s", len(ctxText), ctxText)
}
