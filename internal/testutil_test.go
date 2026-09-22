// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/domain"
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

// newTurnKey opens a scene the way the read path does and hands back the pair a
// turn-keyed write needs: the scene id and the turn topic id, both hex — the
// only keys AppendArchive now accepts. The uint64 form comes back too, for
// tests that read the topic's records straight off the engine. Binds the file's
// default domain.
func newTurnKey(t *testing.T, db *DB) (sceneHex, topicHex string, topicID uint64) {
	return newTurnKeyFor(t, db, core.DefaultAgentID)
}

// newTurnKeyFor is newTurnKey for an explicit agent domain.
func newTurnKeyFor(t *testing.T, db *DB, agentID uint64) (sceneHex, topicHex string, topicID uint64) {
	t.Helper()
	res, err := db.Search(agentID, SearchQuery{})
	if err != nil {
		t.Fatalf("open scene: %v", err)
	}
	return common.FormatHash(res.Scene.SceneID), common.FormatHash(res.NewTopicID), res.NewTopicID
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

// writeTopicCached writes one topic and mirrors it into the domain's L2Meta
// cache, the way the root's own write path does.
func writeTopicCached(t *testing.T, ac *domain.Context, engine *core.StorageEngine, agentID uint64, topic core.TopicSlot) {
	t.Helper()
	writeTopic(t, engine, agentID, topic)
	ac.SyncL2Meta(&topic)
}

// mustWriteScene persists a scene record for a host session id.
func mustWriteScene(t *testing.T, engine *core.StorageEngine, agentID uint64, sceneID uint64, name string) {
	t.Helper()
	if err := core.WriteSceneSlot(engine, agentID, sceneID, &core.SceneSlot{SceneID: sceneID, SceneName: name}); err != nil {
		t.Fatalf("write scene: %v", err)
	}
}

// chatPath serves the one endpoint the engine calls; any other path 404s, which is
// how a test notices the engine asking for something it was never configured to use.
func chatPath(h func(w http.ResponseWriter, r *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		h(w, r)
	}
}

// answerCompletion writes one OpenAI-style non-streaming completion.
func answerCompletion(w http.ResponseWriter, content string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"choices": []map[string]any{{
			"message": map[string]any{"role": "assistant", "content": content},
		}},
	})
}

// mockServer starts a chat endpoint over h and closes it with the test.
func mockServer(t *testing.T, h func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(chatPath(h))
	t.Cleanup(srv.Close)
	return srv
}

// mockLLMServer answers every chat completion request with the same content —
// the plain stub for tests that only need the LLM call to succeed.
func mockLLMServer(t *testing.T, content string) *httptest.Server {
	return mockServer(t, func(w http.ResponseWriter, _ *http.Request) {
		answerCompletion(w, content)
	})
}

// mockLLMServerSeq answers successive chat completion requests from contents in
// order, wrapping around — for a pipeline whose stages must each get their own
// reply. The cursor is guarded because the server runs on its own goroutine.
func mockLLMServerSeq(t *testing.T, contents ...string) *httptest.Server {
	var mu sync.Mutex
	idx := 0
	return mockServer(t, func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		content := contents[idx%len(contents)]
		idx++
		mu.Unlock()
		answerCompletion(w, content)
	})
}

// contractLLMServer answers each of the three LLM contracts with a valid reply of
// its own, told apart by the system prompt. A stub that answers every call with
// the keyword track makes a full Dream stop at the distillation stage — and that
// is the stage's contract working (a reply carrying no emotion/mbti block is no
// answer), not something a scene-read test means to exercise.
func contractLLMServer(t *testing.T) *httptest.Server {
	return mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		content := turnKeywords
		if len(body.Messages) > 0 {
			switch system := body.Messages[0].Content; {
			case strings.Contains(system, "associative memory samples"):
				content = `{"emotion":{"valence":0.5,"arousal":0.5,"dominance":0.5},` +
					`"mbti":{"i_e":0.1,"n_s":0.1,"t_f":0.1,"j_p":0.1},"personality":"务实","per_node":[]}`
			case strings.Contains(system, "L2 chat memory topics"):
				content = `{"l2_groups":[]}`
			}
		}
		answerCompletion(w, content)
	})
}

// cancellingLLMServer answers like mockLLMServerSeq and cancels cancel right
// after replying to the cancelOn-th request (1-based). A test needs that timing
// when the cancellation must land after a stage has already written: then it is
// the next checkpoint that exits the pipeline, not a failed model call.
func cancellingLLMServer(t *testing.T, cancel context.CancelFunc, cancelOn int, contents ...string) *httptest.Server {
	var mu sync.Mutex
	idx := 0
	return mockServer(t, func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		at := idx + 1
		idx = at
		content := contents[(at-1)%len(contents)]
		mu.Unlock()
		answerCompletion(w, content)
		if at == cancelOn {
			cancel()
		}
	})
}

// countingLLMServer answers every chat request with content and records how
// many times it was called — the read path must leave the counter at zero.
func countingLLMServer(t *testing.T, content string) (*httptest.Server, *atomic.Int64) {
	calls := &atomic.Int64{}
	srv := mockServer(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		answerCompletion(w, content)
	})
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
	var seen recordedRequests
	srv := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen.add(string(body))
		answerCompletion(w, content)
	})
	return srv, &seen
}

// failingLLMServer returns status for every chat completion request
// (non-retryable codes only, so tests do not wait out the backoff).
func failingLLMServer(t *testing.T, status int) *httptest.Server {
	return mockServer(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "mock llm failure", status)
	})
}

// countRecords counts the live records of one type in an agent domain.
func countRecords(engine *core.StorageEngine, agentID uint64, recordType uint8) int {
	n := 0
	for range engine.IndexByType(agentID, recordType) {
		n++
	}
	return n
}
