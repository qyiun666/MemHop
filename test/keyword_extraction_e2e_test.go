// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//go:build integration

// Real-LLM regression tests for the keyword-extraction non-JSON bug
// (meowagent docs/issues/memhop_search_keywords_nonjson.md): long inputs
// drift toward natural-language summaries; extraction must self-heal and
// Search must never fail because of it.

package test

import (
	"context"
	"testing"
	"time"

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

// TestSearchLongInputNeverFails runs the full Search chain (real encoder +
// real LLM) with whole-session long inputs. Search must never fail because
// of keyword extraction; a topic must still be created each round.
func TestSearchLongInputNeverFails(t *testing.T) {
	db := testsupport.OpenMemHop(t)
	defer db.Close()
	texts := []string{longSessionText(t, 2), longSessionText(t, 0)}
	base := time.Now().UnixMilli()
	for i, text := range texts {
		res, err := db.Search(context.Background(), internal.SearchQuery{Text: text, Timestamp: base + int64(i)*1000})
		if err != nil {
			t.Fatalf("attempt %d: Search returned error: %v", i, err)
		}
		if res.NewTopicID == 0 {
			t.Fatalf("attempt %d: no topic created", i)
		}
	}
}
