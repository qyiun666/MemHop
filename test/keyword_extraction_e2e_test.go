// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//go:build integration

// Real-LLM regression tests for the keyword-extraction non-JSON bug
// (meowagent docs/issues/memhop_search_keywords_nonjson.md): long inputs
// drift toward natural-language summaries; extraction must self-heal, and the
// turn write that depends on it must never fail because of it.

package test

import (
	"context"
	"testing"
	"time"

	memhop "github.com/qyiun666/MemHop/api"
	internal "github.com/qyiun666/MemHop/internal"
	"github.com/qyiun666/MemHop/internal/cap/llmops"
	"github.com/qyiun666/MemHop/test/testsupport"
)

// longSessionText renders one full Locomo session as "speaker: text" lines,
// mirroring the host's whole-session injection that triggered the bug.
func longSessionText(t *testing.T, sessionIdx int) string {
	t.Helper()
	items := loadLocomo10(t, 1)
	sessions := items[0].Sessions
	if sessionIdx >= len(sessions) {
		t.Fatalf("fixture has %d sessions, want idx %d", len(sessions), sessionIdx)
	}
	var sb []byte
	for _, tn := range sessions[sessionIdx].Turns {
		sb = append(sb, tn.Speaker...)
		sb = append(sb, ": "...)
		sb = append(sb, tn.Text...)
		sb = append(sb, '\n')
	}
	return string(sb)
}

// TestExtractKeywordsLongInputRealLLM reproduces the original failure mode:
// a >2000-char whole-session text handed to the real LLM. Before the fix
// this returned keywords response parse failed; now it must never error
// (LLM extraction, format retry, or heuristic fallback all acceptable).
func TestExtractKeywordsLongInputRealLLM(t *testing.T) {
	cfg := &internal.MemHopConfig{}
	if err := testsupport.LoadLLMConfig(cfg); err != nil {
		t.Skipf("LLM not configured: %v", err)
	}
	p := internal.New(cfg)
	// Session 2 is 4322 chars (> keywordChunkRunes → chunked path).
	longText := longSessionText(t, 2)
	// Session 0 is 1560 chars (single-pass path with format retry).
	midText := longSessionText(t, 0)
	for name, text := range map[string]string{"chunked": longText, "single": midText} {
		t.Run(name, func(t *testing.T) {
			// Sampling variance: several attempts raise the chance of hitting
			// the summary-output form the bug report observed.
			for i := 0; i < 3; i++ {
				kw, err := llmops.ExtractKeywords(context.Background(), p, text)
				if err != nil {
					t.Fatalf("attempt %d: ExtractKeywords returned error: %v", i, err)
				}
				if len(kw) == 0 {
					t.Fatalf("attempt %d: no keywords at all", i)
				}
			}
		})
	}
}

// TestUpdateLongTurnNeverFails runs the write chain (real LLM) with
// whole-session long inputs. The turn must settle despite extraction drift —
// a topic with its distilled keywords and both originals.
func TestUpdateLongTurnNeverFails(t *testing.T) {
	db := testsupport.OpenMemHop(t)
	defer db.Close()

	texts := []string{longSessionText(t, 2), longSessionText(t, 0)}
	sceneID, _, err := db.OpenTurn("")
	if err != nil {
		t.Fatalf("OpenTurn: %v", err)
	}
	base := time.Now().UnixMilli()
	for i, text := range texts {
		ts := base + int64(i)*1000
		_, turnID, err := db.OpenTurn(sceneID)
		if err != nil {
			t.Fatalf("OpenTurn %d: %v", i, err)
		}
		topicID, err := db.Update(memhop.TurnUpdate{
			SceneID: sceneID, TopicID: turnID, UserText: text, UserTS: ts,
			AgentText: "已了解这段长对话", AgentTS: ts + 500,
		})
		if err != nil {
			t.Fatalf("attempt %d: Update returned error: %v", i, err)
		}
		if topicID == "" {
			t.Fatalf("attempt %d: no topic created", i)
		}
	}
	res, err := db.Search(memhop.SearchQuery{SceneID: sceneID})
	if err != nil {
		t.Fatalf("session read: %v", err)
	}
	if len(res.Topics) != len(texts) {
		t.Fatalf("surface = %d topics, want %d", len(res.Topics), len(texts))
	}
	for _, tp := range res.Topics {
		if len(tp.FusedKeywords) == 0 {
			t.Errorf("long turn %s settled with no keywords", tp.ID)
		}
		if len(tp.L4Refs) != 2 {
			t.Errorf("long turn %s lost its originals: %v", tp.ID, tp.L4Refs)
		}
	}
}

// TestExtractTurnKeywordsLongInput pins the capability contract directly: a
// long turn pair must never surface an error, and must yield keywords.
func TestExtractTurnKeywordsLongInput(t *testing.T) {
	cfg := &internal.MemHopConfig{}
	if err := testsupport.LoadLLMConfig(cfg); err != nil {
		t.Skipf("LLM not configured: %v", err)
	}
	p := internal.New(cfg)
	kw, err := llmops.ExtractTurnKeywords(context.Background(), p, longSessionText(t, 2), "收到")
	if err != nil {
		t.Fatalf("ExtractTurnKeywords: %v", err)
	}
	if len(kw) == 0 {
		t.Fatal("long turn distilled to nothing")
	}
}
