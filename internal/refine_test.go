// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// refineTestTopic builds a scene + topic with nL4 L4 messages and a live
// dual-track (UserKeywords/AgentKeywords), mirroring a Search →
// AppendL4Message ×N → Update turn. Returns the topic id string.
func refineTestTopic(t *testing.T, db *DB, nL4 int) string {
	t.Helper()
	sceneID, err := repo.CreateSceneL2(db.engine, core.DefaultAgentID, "refine-scene")
	if err != nil {
		t.Fatalf("create scene: %v", err)
	}
	topicID := common.HashID("refine-topic")
	if !repo.CreateTopicL2WithID(db.engine, core.DefaultAgentID, sceneID, topicID, []string{"user-kw"}, 1000, 0) {
		t.Fatal("create topic")
	}
	topicIDStr := common.FormatHash(topicID)
	role := core.RoleUser
	ids := make([]uint64, 0, nL4)
	for i := 0; i < nL4; i++ {
		if i == nL4-1 {
			role = core.RoleAgent
		}
		id, err := repo.AppendArchiveL4(db.engine, core.DefaultAgentID, topicIDStr, role, core.ContentText, "msg-"+strings.Repeat("x", i+1), int64(1000+i))
		if err != nil {
			t.Fatalf("append l4: %v", err)
		}
		ids = append(ids, id)
	}
	if !repo.UpdateTopicL4RefsL2(db.engine, core.DefaultAgentID, topicIDStr, ids) {
		t.Fatal("update l4 refs")
	}
	if !repo.UpdateTopicL2(db.engine, core.DefaultAgentID, topicIDStr, []string{"agent-kw"}, 2000) {
		t.Fatal("update topic")
	}
	return topicIDStr
}

// countingLLMServer is mockLLMServer plus a call counter, used to prove the
// refine guard skips the LLM for topics that need no refining.
func countingLLMServer(t *testing.T, content string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{"role": "assistant", "content": content},
			}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// failingLLMServer returns status for every chat completion request
// (non-retryable codes only, so tests do not wait out the backoff).
func failingLLMServer(t *testing.T, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "mock llm failure", status)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestRefineTopicKeywords re-extracts all L4 messages into FusedKeywords:
// the dual-track is cleared (timestamps kept), depth unchanged, L4 intact.
func TestRefineTopicKeywords(t *testing.T) {
	srv := mockLLMServer(t, `{"keywords":["fused1","fused2"]}`)
	db := newSearchTestDB(t, srv.URL)
	topicID := refineTestTopic(t, db, 3)

	if err := db.RefineTopicKeywords(context.Background(), topicID); err != nil {
		t.Fatalf("RefineTopicKeywords: %v", err)
	}
	parsedID, err := common.ParseID(topicID)
	if err != nil {
		t.Fatalf("parse id: %v", err)
	}
	topic, err := core.ReadTopicLenient(db.engine, core.DefaultAgentID, parsedID)
	if err != nil || topic == nil {
		t.Fatalf("read topic: %v", err)
	}
	if len(topic.FusedKeywords) != 2 || topic.FusedKeywords[0] != "fused1" || topic.FusedKeywords[1] != "fused2" {
		t.Errorf("FusedKeywords = %v, want [fused1 fused2]", topic.FusedKeywords)
	}
	if len(topic.UserKeywords) != 0 || len(topic.AgentKeywords) != 0 {
		t.Errorf("dual-track not cleared: user=%v agent=%v", topic.UserKeywords, topic.AgentKeywords)
	}
	if topic.UserTimestamp != 1000 || topic.AgentTimestamp != 2000 {
		t.Errorf("timestamps not preserved: user=%d agent=%d", topic.UserTimestamp, topic.AgentTimestamp)
	}
	if topic.Depth != 1 {
		t.Errorf("Depth = %d, want 1", topic.Depth)
	}
	// All three L4 originals still readable.
	ids := make([]string, 0, len(topic.L4Refs))
	for _, id := range topic.L4Refs {
		ids = append(ids, common.FormatHash(id))
	}
	if arcs := repo.QueryArchiveL4(db.engine, core.DefaultAgentID, 3, "", 0, 0, ids); len(arcs) != 3 {
		t.Errorf("archives = %d, want 3", len(arcs))
	}
}

// TestRefineTopicKeywordsGuard1to1: a standard 1:1 topic (two L4 messages)
// is not refined — no LLM call, keywords untouched.
func TestRefineTopicKeywordsGuard1to1(t *testing.T) {
	srv, calls := countingLLMServer(t, `{"keywords":["fused1"]}`)
	db := newSearchTestDB(t, srv.URL)
	topicID := refineTestTopic(t, db, 2)

	if err := db.RefineTopicKeywords(context.Background(), topicID); err != nil {
		t.Fatalf("RefineTopicKeywords: %v", err)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("LLM called %d times, want 0", got)
	}
	parsedID, _ := common.ParseID(topicID)
	topic, _ := core.ReadTopicLenient(db.engine, core.DefaultAgentID, parsedID)
	if len(topic.UserKeywords) != 1 || topic.UserKeywords[0] != "user-kw" ||
		len(topic.AgentKeywords) != 1 || topic.AgentKeywords[0] != "agent-kw" {
		t.Errorf("dual-track changed: user=%v agent=%v", topic.UserKeywords, topic.AgentKeywords)
	}
	if len(topic.FusedKeywords) != 0 {
		t.Errorf("FusedKeywords = %v, want empty", topic.FusedKeywords)
	}
}

// TestRefineTopicKeywordsIdempotent: a second refine on an already refined
// topic is a no-op — the LLM is not called again and FusedKeywords hold.
func TestRefineTopicKeywordsIdempotent(t *testing.T) {
	srv, calls := countingLLMServer(t, `{"keywords":["fused1","fused2"]}`)
	db := newSearchTestDB(t, srv.URL)
	topicID := refineTestTopic(t, db, 3)

	if err := db.RefineTopicKeywords(context.Background(), topicID); err != nil {
		t.Fatalf("first refine: %v", err)
	}
	if err := db.RefineTopicKeywords(context.Background(), topicID); err != nil {
		t.Fatalf("second refine: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("LLM called %d times, want 1 (once for the first refine)", got)
	}
	parsedID, _ := common.ParseID(topicID)
	topic, _ := core.ReadTopicLenient(db.engine, core.DefaultAgentID, parsedID)
	if len(topic.FusedKeywords) != 2 || topic.FusedKeywords[0] != "fused1" {
		t.Errorf("FusedKeywords changed on second refine: %v", topic.FusedKeywords)
	}
}

// TestRefineTopicKeywordsErrors covers the failure paths: missing topic,
// LLM failure and empty extraction must all leave the topic untouched.
func TestRefineTopicKeywordsErrors(t *testing.T) {
	t.Run("missing topic", func(t *testing.T) {
		srv := mockLLMServer(t, `{"keywords":["x"]}`)
		db := newSearchTestDB(t, srv.URL)
		err := db.RefineTopicKeywords(context.Background(), "deadbeefdeadbeef")
		if common.CodeOf(err) != common.ErrNotFound {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
	t.Run("llm failure", func(t *testing.T) {
		srv := failingLLMServer(t, http.StatusBadRequest)
		db := newSearchTestDB(t, srv.URL)
		topicID := refineTestTopic(t, db, 3)
		err := db.RefineTopicKeywords(context.Background(), topicID)
		if common.CodeOf(err) != common.ErrLLM {
			t.Fatalf("err = %v, want ErrLLM", err)
		}
		parsedID, _ := common.ParseID(topicID)
		topic, _ := core.ReadTopicLenient(db.engine, core.DefaultAgentID, parsedID)
		if len(topic.UserKeywords) != 1 || len(topic.AgentKeywords) != 1 || len(topic.FusedKeywords) != 0 {
			t.Errorf("topic changed after llm failure: user=%v agent=%v fused=%v",
				topic.UserKeywords, topic.AgentKeywords, topic.FusedKeywords)
		}
	})
	t.Run("empty extraction", func(t *testing.T) {
		srv := mockLLMServer(t, `{"keywords":[]}`)
		db := newSearchTestDB(t, srv.URL)
		topicID := refineTestTopic(t, db, 3)
		err := db.RefineTopicKeywords(context.Background(), topicID)
		if common.CodeOf(err) != common.ErrLLM {
			t.Fatalf("err = %v, want ErrLLM", err)
		}
		parsedID, _ := common.ParseID(topicID)
		topic, _ := core.ReadTopicLenient(db.engine, core.DefaultAgentID, parsedID)
		if len(topic.UserKeywords) != 1 || len(topic.AgentKeywords) != 1 || len(topic.FusedKeywords) != 0 {
			t.Errorf("topic changed after empty extraction: user=%v agent=%v fused=%v",
				topic.UserKeywords, topic.AgentKeywords, topic.FusedKeywords)
		}
	})
}
