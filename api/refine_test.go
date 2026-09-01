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
)

// refineLLM returns different keywords depending on whether the request text
// contains the appended messages: Update's distillation sees only the turn
// pair and gets "user-kw"; RefineTopicKeywords sends every L4 original, which
// contains "补充", and gets the fused set. This proves the refine input is the
// full L4Refs, not the turn text.
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

// TestRefineTopicKeywordsFacade: Update writes the turn topic, AppendL4Message
// ×2 grows L4Refs, and RefineTopicKeywords re-distills every original into the
// topic's single keyword track. SceneContext then shows the fused keywords
// while all four L4 messages survive.
func TestRefineTopicKeywordsFacade(t *testing.T) {
	srv := refineLLM(t)
	cfg := openTestConfig(filepath.Join(t.TempDir(), "refine.meh"))
	cfg.LLM.APIURL = srv.URL
	m, db := openMultiSession(t, cfg)
	defer func() {
		if err := m.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
	}()

	res, err := db.Search(SearchQuery{SceneName: "refine session"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	topicID, err := db.Update(TurnUpdate{
		SceneID: res.Scene.SceneID, UserText: "用户第一条消息", UserTS: 1000,
		AgentText: "先看看日志", AgentTS: 1001,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := db.AppendL4Message(topicID, "用户补充", 1002, RoleUser, ContentText); err != nil {
		t.Fatalf("append user: %v", err)
	}
	if _, err := db.AppendL4Message(topicID, "补充说明", 1003, RoleAgent, ContentText); err != nil {
		t.Fatalf("append agent: %v", err)
	}
	if err := db.RefineTopicKeywords(context.Background(), topicID); err != nil {
		t.Fatalf("RefineTopicKeywords: %v", err)
	}

	sc, err := db.SceneContext(res.Scene.SceneID)
	if err != nil {
		t.Fatalf("SceneContext: %v", err)
	}
	if len(sc.Topics) != 1 {
		t.Fatalf("topics = %d, want 1", len(sc.Topics))
	}
	kw := sc.Topics[0].Keywords
	if len(kw) != 2 || kw[0] != "fused" || kw[1] != "补充" {
		t.Fatalf("Keywords = %v, want the re-distilled [fused 补充]", kw)
	}
	if len(sc.Topics[0].L4IDs) != 4 {
		t.Fatalf("L4IDs = %d, want 4 (all messages preserved)", len(sc.Topics[0].L4IDs))
	}
}
