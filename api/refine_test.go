// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qyiun666/MemHop/internal"
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// refineLLM returns different keywords depending on whether the request
// text contains the appended messages: Search sees only the first message
// and gets "user-kw"; RefineTopicKeywords sends all L4 originals, which
// contain "补充", and gets the fused set. This proves the refine input is
// the full L4Refs, not the Search text.
func refineLLM(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		content := `{"keywords":["user-kw"]}`
		for _, m := range body.Messages {
			if strings.Contains(m.Content, "补充") {
				content = `{"keywords":["fused","补充"]}`
				break
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{"role": "assistant", "content": content},
			}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestRefineTopicKeywordsFacade: Search creates the topic, AppendL4Message
// ×2 grows L4Refs past the 1:1 threshold, RefineTopicKeywords fuses all
// messages into FusedKeywords and clears the dual track; SceneContext then
// shows only the fused keywords while all three L4 messages survive.
func TestRefineTopicKeywordsFacade(t *testing.T) {
	srv := refineLLM(t)
	cfg := openTestConfig(filepath.Join(t.TempDir(), "refine.meh"))
	cfg.LLM.APIURL = srv.URL
	db, err := OpenWithEncoder(cfg, &openTestEncoder{dim: 4})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
	}()

	ts := int64(1000)
	res, err := db.Search(context.Background(), internal.SearchQuery{Text: "用户第一条消息", AutoCreate: true, Timestamp: ts})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	topicID := common.FormatHash(res.NewTopicID)
	if _, err := db.AppendL4Message(topicID, "用户补充", ts+1, core.RoleUser); err != nil {
		t.Fatalf("append user: %v", err)
	}
	if _, err := db.AppendL4Message(topicID, "补充说明", ts+2, core.RoleAgent); err != nil {
		t.Fatalf("append agent: %v", err)
	}
	if err := db.RefineTopicKeywords(context.Background(), topicID); err != nil {
		t.Fatalf("RefineTopicKeywords: %v", err)
	}
	// Idempotent second call must not fail.
	if err := db.RefineTopicKeywords(context.Background(), topicID); err != nil {
		t.Fatalf("second RefineTopicKeywords: %v", err)
	}

	sc, err := db.SceneContext(common.FormatHash(res.Contexts[0].SceneID))
	if err != nil {
		t.Fatalf("SceneContext: %v", err)
	}
	if len(sc.Topics) != 1 {
		t.Fatalf("topics = %d, want 1", len(sc.Topics))
	}
	kw := sc.Topics[0].Keywords
	if len(kw) != 2 || kw[0] != "fused" || kw[1] != "补充" {
		t.Fatalf("Keywords = %v, want [fused 补充] (dual track must be cleared)", kw)
	}
	if len(sc.Topics[0].L4IDs) != 3 {
		t.Fatalf("L4IDs = %d, want 3 (all messages preserved)", len(sc.Topics[0].L4IDs))
	}
}
