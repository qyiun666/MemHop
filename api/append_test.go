// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// mockLLM is an OpenAI-compatible chat completions stub for the turn
// distillation inside Update (AppendL4Message itself never calls the LLM).
func mockLLM(t *testing.T, content string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
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

// TestAppendL4MessageFacade: Update creates the turn topic, AppendL4Message
// appends another original to it, and the returned id round-trips through
// GetArchive with the right role.
func TestAppendL4MessageFacade(t *testing.T) {
	srv := mockLLM(t, `{"keywords":["补充","消息"]}`)
	cfg := openTestConfig(filepath.Join(t.TempDir(), "append.meh"))
	cfg.LLM.APIURL = srv.URL
	m, db := openMultiSession(t, cfg)
	defer func() {
		if err := m.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
	}()

	res, err := db.Search(SearchQuery{SceneName: "append session"})
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
	if topicID == "" {
		t.Fatal("update created no topic")
	}

	id, err := db.AppendL4Message(topicID, "用户补充", 1002, RoleUser, ContentText)
	if err != nil {
		t.Fatalf("AppendL4Message: %v", err)
	}
	if id == "" {
		t.Fatal("AppendL4Message returned empty id")
	}
	arc, err := db.GetArchive(id)
	if err != nil {
		t.Fatalf("GetArchive: %v", err)
	}
	if arc.Role != RoleUser || arc.Content != "用户补充" || arc.ContextID != topicID || arc.ContentType != ContentText {
		t.Fatalf("unexpected archive: %+v", arc)
	}
}
