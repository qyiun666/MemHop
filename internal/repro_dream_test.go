// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Repro harness: opens a COPY of a real .meh database and runs the exact
// dream L2-consolidation path against the real LLM, printing the raw error.
// Usage:
//
//	REPRO_DB=/tmp/repro/copy.meh MEMHOP_LLM_API_URL=... MEMHOP_LLM_API_KEY=... \
//	  MEMHOP_LLM_MODEL=deepseek-chat \
//	  go test ./internal/ -run TestReproDreamConsolidate -v

package internal

import (
	"context"
	"os"
	"testing"

	"github.com/qyiun666/MemHop/internal/cap/llmops"
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

func TestReproDreamConsolidate(t *testing.T) {
	path := os.Getenv("REPRO_DB")
	if path == "" {
		t.Skip("REPRO_DB not set")
	}
	cfg := &MemHopConfig{
		DBPath: path,
		LLM: LlmConfig{
			APIURL:          os.Getenv("MEMHOP_LLM_API_URL"),
			APIKey:          os.Getenv("MEMHOP_LLM_API_KEY"),
			Model:           os.Getenv("MEMHOP_LLM_MODEL"),
			TimeoutSecs:     120,
			MaxOutputTokens: 2048,
		},
		Defaults: *DefaultMemHopDefaults,
	}
	engine, err := core.Open(path)
	if err != nil {
		t.Fatalf("open engine: %v", err)
	}
	defer engine.CloseNoCheckpoint()

	db := newTestDB(t, engine)
	db.config = cfg
	db.llm = New(cfg)

	dumpSceneContext(t, db, cfg, "before dream")

	// Full pipeline on a copy: exercises L2 consolidation (LLM), index
	// rebuild, L1 sync/decay, L0 profile + distillation (LLM) end to end.
	rep, err := db.RunDream(context.Background(), core.DefaultAgentID, 0)
	t.Logf("RunDream(all): rep=%+v err=%v", rep, err)

	dumpSceneContext(t, db, cfg, "after dream")

	scenes := repo.CollectAllScenesL2(engine, core.DefaultAgentID)
	t.Logf("scenes on disk: %d", len(scenes))
	for _, s := range scenes {
		topics, err := repo.ListTopicsL2(repo.TopicListQuery{
			Engine:  engine,
			MetaIdx: nil,
			SceneID: s.SceneID,
			Depth:   1,
			Num:     2,
		})
		if err != nil {
			t.Logf("scene %s: ListTopicsL2 error: %v", s.SceneName, err)
			continue
		}
		t.Logf("scene %q (%s) topics=%d (compress min=%d)",
			s.SceneName, common.FormatHash(s.SceneID), len(topics), cfg.Defaults.DreamCompressMinTopics)
		if len(topics) < cfg.Defaults.DreamCompressMinTopics {
			t.Logf("  -> below threshold, consolidation skipped (not a failure)")
			continue
		}
		out, err := llmops.Consolidate(context.Background(), db.llm, topics)
		if err != nil {
			t.Logf("  -> CONSOLIDATE ERROR: %v", err)
			// Raw response evidence: call chat directly (same package) to
			// inspect what the LLM actually returned.
			user := llmops.BuildConsolidatePrompt(topics)
			resp, chatErr := db.llm.Chat(context.Background(), llmops.SystemConsolidate, user,
				llmops.ConsolidationMaxTokens, 0.0, 1.0)
			if chatErr != nil {
				t.Logf("  -> raw chat error: %v", chatErr)
			} else {
				t.Logf("  -> raw response len=%d head=%q tail=%q",
					len(resp), clip(resp, 0, 200), clip(resp, len(resp)-200, len(resp)))
			}
			t.Logf("  -> prompt chars: %d (first topic id=%d)",
				len(llmops.BuildConsolidatePrompt(topics)), topics[0].ID)
			continue
		}
		t.Logf("  -> consolidate ok, groups=%d compressionNeeded=%v",
			len(out.L2Groups), out.L2CompressionNeeded)
	}
}

func clip(s string, lo, hi int) string {
	if lo < 0 {
		lo = 0
	}
	if hi > len(s) {
		hi = len(s)
	}
	if lo > hi {
		lo = hi
	}
	return s[lo:hi]
}

// dumpSceneContext prints the scene shape reachable from the DB: scenes
// with depth-1 topic counts, and per-topic keywords, archive refs and
// children, so the real database state can be inspected against the
// rendering path ("scene 查看" → ListScenes).
func dumpSceneContext(t *testing.T, db *DB, cfg *MemHopConfig, label string) {
	t.Logf("--- %s ---", label)
	scenes := repo.CollectAllScenesL2(db.engine, core.DefaultAgentID)
	for _, s := range scenes {
		topics, err := repo.ListTopicsL2(repo.TopicListQuery{
			Engine:  db.engine,
			MetaIdx: nil,
			SceneID: s.SceneID,
			Depth:   1,
			Num:     2,
		})
		if err != nil {
			t.Logf("scene %q: ListTopicsL2 error: %v", s.SceneName, err)
			continue
		}
		t.Logf("scene %q topics=%d (topic_count=%d)",
			s.SceneName, len(topics), s.TopicCount)
		for _, tp := range topics {
			fused := ""
			if len(tp.FusedKeywords) > 0 && len(tp.L4Refs) == 0 {
				fused = " [FUSED, no archives]"
			}
			kwN := len(tp.FusedKeywords)
			t.Logf("  depth=%d id=%s kw=%d archives=%d child=%d%s",
				tp.Depth, common.FormatHash(tp.ID), kwN, len(tp.L4Refs), len(tp.ChildrenIDs), fused)
		}
	}
}
