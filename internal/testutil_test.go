// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/qyiun666/MemHop/internal/repo/core"
)

func newTestEngine(t *testing.T) *core.StorageEngine {
	t.Helper()
	engine, err := core.Create(filepath.Join(t.TempDir(), "test.meh"))
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	t.Cleanup(func() { engine.Close() })
	return engine
}

// newTopic builds a depth-1 turn topic fixture stamped at ts.
func newTopic(id, scene uint64, ts int64, kws []string) core.TopicSlot {
	return core.TopicSlot{
		ID: id, SceneID: scene, Depth: 1,
		FusedKeywords: kws, UserTimestamp: ts, AgentTimestamp: ts + 1,
	}
}

// writeTopic persists one topic record into an agent domain.
func writeTopic(t *testing.T, engine *core.StorageEngine, agentID uint64, topic core.TopicSlot) {
	t.Helper()
	data, err := json.Marshal(topic)
	if err != nil {
		t.Fatalf("marshal topic: %v", err)
	}
	if _, err := engine.WriteRecord(agentID, core.RecL2Topic, topic.ID, data); err != nil {
		t.Fatalf("write topic: %v", err)
	}
}

// mustWriteScene persists a scene record for a host session id.
func mustWriteScene(t *testing.T, engine *core.StorageEngine, agentID uint64, sceneID uint64, name string) {
	t.Helper()
	if err := core.WriteSceneSlot(engine, agentID, sceneID, &core.SceneSlot{SceneID: sceneID, SceneName: name}); err != nil {
		t.Fatalf("write scene: %v", err)
	}
}

// countingLLMServer answers every chat request with content and records how
// many times it was called — the read path must leave the counter at zero.
func countingLLMServer(t *testing.T, content string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	calls := &atomic.Int64{}
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
	return srv, calls
}

// recordedRequests collects the bodies one mock server was sent.
type recordedRequests struct {
	mu     sync.Mutex
	bodies []string
}

func (r *recordedRequests) add(body string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bodies = append(r.bodies, body)
}

// snapshot returns every body recorded so far, without racing the server goroutine.
func (r *recordedRequests) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.bodies...)
}

// recordingLLMServer answers every chat request with content and keeps each
// request body, so a test can assert what the engine actually sent — that a
// transcript reached the prompt with its speakers labelled, for instance.
func recordingLLMServer(t *testing.T, content string) (*httptest.Server, *recordedRequests) {
	t.Helper()
	var seen recordedRequests
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		seen.add(string(body))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{"role": "assistant", "content": content},
			}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
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

// countRecords counts the live records of one type in an agent domain.
func countRecords(engine *core.StorageEngine, agentID uint64, recordType uint8) int {
	n := 0
	for range engine.IndexByType(agentID, recordType) {
		n++
	}
	return n
}
