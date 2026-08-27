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

// mockLLM is an OpenAI-compatible chat completions stub for the keyword
// extraction inside Search (AppendL4Message itself never calls the LLM).
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

// TestAppendL4MessageFacade: Search creates the topic, AppendL4Message
// appends a user message to it, and the returned id round-trips through
// GetArchive with the right role.
func TestAppendL4MessageFacade(t *testing.T) {
	srv := mockLLM(t, `{"keywords":["补充","消息"]}`)
	cfg := openTestConfig(filepath.Join(t.TempDir(), "append.meh"))
	cfg.LLM.APIURL = srv.URL
	m, db := openMultiSession(t, cfg, &openTestEncoder{dim: 4})
	defer func() {
		if err := m.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
	}()

	ts := int64(1000)
	res, err := db.Search(context.Background(), internal.SearchQuery{Text: "用户第一条消息", AutoCreate: true, Timestamp: ts})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if res.NewTopicID == 0 {
		t.Fatal("search created no topic")
	}
	topicID := common.FormatHash(res.NewTopicID)

	id, err := db.AppendL4Message(topicID, "用户补充", ts+1, core.RoleUser)
	if err != nil {
		t.Fatalf("AppendL4Message: %v", err)
	}
	if id == 0 {
		t.Fatal("AppendL4Message returned zero id")
	}
	arc, err := db.GetArchive(common.FormatHash(id))
	if err != nil {
		t.Fatalf("GetArchive: %v", err)
	}
	if arc.Role != core.RoleUser || arc.Content != "用户补充" || arc.ContextID != res.NewTopicID {
		t.Fatalf("unexpected archive: %+v", arc)
	}
}
