// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Public API surface tests: exercise every exported Session /
// MultiAgentDB method with valid and invalid parameters against a mock
// encoder + a stub LLM server, asserting request/response shapes and the
// numeric error-code contract. These run without external services.

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
)

// stubLLM returns one union JSON that satisfies every response parser
// (keywords / l2_groups / emotion / mbti / capabilities) with empty merge
// groups and capabilities, so consolidation / crystallize are no-ops.
func stubLLM() *httptest.Server {
	content := `{"keywords":["alpha","beta"],` +
		`"l2_groups":[],"l2_compression_needed":false,` +
		`"emotion":{"valence":0.2,"arousal":0.3,"dominance":0.1},` +
		`"mbti":{"i_e":0.4,"n_s":-0.2,"t_f":0.1,"j_p":-0.3,"type":"ENTJ"},` +
		`"per_node":[],"capabilities":[]}`
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-stub", "object": "chat.completion", "created": 0, "model": "m",
			"choices": []map[string]any{{
				"index": 0, "finish_reason": "stop",
				"message": map[string]any{"role": "assistant", "content": content},
			}},
			"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
		})
	}))
}

func surfaceConfig(t *testing.T, llmURL string) *MemHopConfig {
	t.Helper()
	return &internal.MemHopConfig{
		DBPath:     filepath.Join(t.TempDir(), "surface.meh"),
		VectorDim:  4,
		EmbedModel: "test-embed",
		LLM:        internal.LlmConfig{APIURL: llmURL, APIKey: "k", Model: "m"},
		Defaults:   *internal.DefaultMemHopDefaults,
	}
}

// openMultiSession opens the only supported mode (multi-agent) and binds a
// session to a freshly registered tenant.
func openMultiSession(t *testing.T, cfg *MemHopConfig, enc Encoder) (*MultiAgentDB, *Session) {
	t.Helper()
	m, err := OpenMultiWithEncoder(cfg, enc)
	if err != nil {
		t.Fatalf("open multi: %v", err)
	}
	id, err := m.CreateAgent("surface")
	if err != nil {
		m.Close()
		t.Fatalf("create agent: %v", err)
	}
	sess, err := m.Session(id)
	if err != nil {
		m.Close()
		t.Fatalf("session: %v", err)
	}
	return m, sess
}

func openSurfaceDB(t *testing.T) (*Session, func()) {
	t.Helper()
	llm := stubLLM()
	t.Cleanup(llm.Close)
	m, sess := openMultiSession(t, surfaceConfig(t, llm.URL), &openTestEncoder{dim: 4})
	return sess, func() { _ = m.Close() }
}

// isHexID reports whether s is a canonical 16-char lowercase hex id.
func isHexID(s string) bool {
	if len(s) != 16 {
		return false
	}
	_, err := common.ParseID(s)
	return err == nil
}

func TestSurfaceLifecycle(t *testing.T) {
	db, _ := openSurfaceDB(t)
	if db.IsClosed() {
		t.Fatal("fresh DB must be open")
	}
	if err := db.Checkpoint(); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	// Contract: an unknown / malformed archive id is a lookup miss (ErrNotFound).
	if _, err := db.GetArchive("nothex"); CodeOf(err) != ErrNotFound {
		t.Fatalf("GetArchive bad id: code=%v err=%v", CodeOf(err), err)
	}
}

func TestSurfaceL0Profile(t *testing.T) {
	db, _ := openSurfaceDB(t)
	prof, err := db.GetL0()
	if err != nil || prof == nil {
		t.Fatalf("GetL0 on fresh DB must return empty profile: %v", err)
	}
	if err := db.UpdateL0(&ProfileSlot{Name: "memhop", Role: "assistant"}); err != nil {
		t.Fatalf("UpdateL0: %v", err)
	}
	got, err := db.GetL0()
	if err != nil {
		t.Fatalf("GetL0 after update: %v", err)
	}
	if got.Name != "memhop" || got.Role != "assistant" {
		t.Fatalf("profile round-trip mismatch: %+v", got)
	}
	if err := db.UpdateL0(nil); CodeOf(err) != ErrInvalidQuery {
		t.Fatalf("UpdateL0(nil): want ErrInvalidQuery, got %v", err)
	}
}

// Closed-instance contract: after Close every domain operation is rejected
// with ErrClosed rather than touching a released engine.
func TestSurfaceClosedContract(t *testing.T) {
	llm := stubLLM()
	t.Cleanup(llm.Close)
	m, db := openMultiSession(t, surfaceConfig(t, llm.URL), &openTestEncoder{dim: 4})
	if err := m.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if !m.IsClosed() {
		t.Fatal("IsClosed must report true after Close")
	}
	if _, err := db.GetL0(); CodeOf(err) != ErrClosed {
		t.Fatalf("GetL0 after close: want ErrClosed, got %v", err)
	}
	if _, err := db.Search(context.Background(), SearchQuery{Text: "x", Timestamp: 1}); CodeOf(err) != ErrClosed {
		t.Fatalf("Search after close: want ErrClosed, got %v", err)
	}
	if err := db.Update("0000000000000001", "x", 2); CodeOf(err) != ErrClosed {
		t.Fatalf("Update after close: want ErrClosed, got %v", err)
	}
	// Double close is rejected with ErrClosed, not a panic.
	if err := m.Close(); CodeOf(err) != ErrClosed {
		t.Fatalf("double close: want ErrClosed, got %v", err)
	}
}

func TestSurfaceDreamEmptyDomain(t *testing.T) {
	db, _ := openSurfaceDB(t)
	// A domain with no active scenes succeeds without doing work.
	done, err := db.Dream(context.Background(), "")
	if err != nil || !done {
		t.Fatalf("dream on empty domain: done=%v err=%v", done, err)
	}
	// A directed dream on a nonexistent scene must parse-fail cleanly.
	if _, err := db.Dream(context.Background(), "bad-hex"); CodeOf(err) != ErrInvalidQuery {
		t.Fatalf("dream bad scene id: want ErrInvalidQuery, got %v", err)
	}
}

// The Open path must validate config before building an encoder; an invalid
// config is rejected without touching a real embedding service.
func TestSurfaceOpenValidatesConfig(t *testing.T) {
	if _, err := OpenMulti(&MemHopConfig{}); err == nil {
		t.Fatal("OpenMulti with empty config must fail validation")
	}
}
