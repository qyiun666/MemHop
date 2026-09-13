// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Public API surface tests: exercise every exported Session and DB method with
// valid and invalid parameters against a stub LLM server, asserting
// request/response shapes and the numeric error-code contract. These run without
// external services.

package api

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/qyiun666/MemHop/internal"
	"github.com/qyiun666/MemHop/internal/common"
)

// stubLLM returns one union JSON that satisfies every response parser
// (keywords / l2_groups / emotion+mbti+per_node) with empty merge groups, so
// consolidation is a no-op.
func stubLLM() *httptest.Server {
	content := `{"keywords":["alpha","beta"],` +
		`"l2_groups":[],` +
		`"emotion":{"valence":0.2,"arousal":0.3,"dominance":0.1},` +
		`"mbti":{"i_e":0.4,"n_s":-0.2,"t_f":0.1,"j_p":-0.3,"type":"ENTJ"},` +
		`"per_node":[]}`
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

// surfaceLLM is the stub endpoint configuration every surface scenario uses.
func surfaceLLM(url string) LlmConfig {
	return LlmConfig{APIURL: url, APIKey: "k", Model: "m"}
}

// surfaceProfile is the primary profile a fresh file is opened with.
func surfaceProfile() *ProfileInput {
	return &ProfileInput{Name: "surface-primary", Role: "surface fixture"}
}

// openSurfaceSession opens a database in a fresh temp dir and binds a session to
// a sub-agent domain, which is how a host that wants an isolated domain does it.
func openSurfaceSession(t *testing.T, llmURL string) (*DB, *Session) {
	t.Helper()
	m, err := Open(filepath.Join(t.TempDir(), "surface.meh"), surfaceLLM(llmURL),
		DefaultMemHopDefaults, surfaceProfile())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	sess, err := m.SubAgent(surfaceLLM(llmURL), ProfileInput{Name: "surface"})
	if err != nil {
		m.Close()
		t.Fatalf("SubAgent: %v", err)
	}
	return m, sess
}

// openSurfaceDB opens a database and binds a session to a sub-agent domain; the
// DB is closed via t.Cleanup so the TempDir .meh file is released before removal
// (Windows unlink fails on open handles).
func openSurfaceDB(t *testing.T) *Session {
	t.Helper()
	llm := stubLLM()
	t.Cleanup(llm.Close)
	m, sess := openSurfaceSession(t, llm.URL)
	t.Cleanup(func() { _ = m.Close() })
	return sess
}

// isHexID reports whether s is a canonical 16-char lowercase hex id.
func isHexID(s string) bool {
	if len(s) != 16 {
		return false
	}
	_, err := common.ParseID(s)
	return err == nil
}

// File-level lifecycle (Checkpoint/IsClosed/Close) lives on the DB handle, not
// on a Session — see TestSurfaceMultiAgent. What the session must guarantee is
// that a malformed id is rejected as a bad query, not reported as a miss.
// l3Graph seeds a project domain named name with a one-node import and returns
// its 16-hex id: a scene may only anchor to a domain that exists.
func l3Graph(t *testing.T, db *Session, name string) string {
	t.Helper()
	if _, err := db.ImportL3([]L3ImportItem{{Title: name, Domain: name, NodeType: "concept", Content: "seed", Keywords: []string{"k"}}}, L3ImportSkip); err != nil {
		t.Fatalf("seed graph %s: %v", name, err)
	}
	return common.FormatHash(common.HashID(name))
}

func TestSurfaceLifecycle(t *testing.T) {
	db := openSurfaceDB(t)
	if _, err := db.SearchL4(L4Query{IDs: []string{"nothex"}}); CodeOf(err) != ErrInvalidQuery {
		t.Fatalf("malformed archive id: code=%v err=%v", CodeOf(err), err)
	}
	if _, err := db.SceneContext("nothex"); CodeOf(err) != ErrInvalidQuery {
		t.Fatalf("malformed scene id: code=%v err=%v", CodeOf(err), err)
	}
	if rep, err := db.Dream(context.Background(), "nothex"); CodeOf(err) != ErrInvalidQuery || rep != nil {
		t.Fatalf("dream on malformed scene id: rep=%v err=%v", rep, err)
	}
}

func TestSurfaceL0Profile(t *testing.T) {
	db := openSurfaceDB(t)
	prof, err := db.GetL0()
	if err != nil || prof == nil {
		t.Fatalf("GetL0 on fresh DB must return empty profile: %v", err)
	}
	if err := db.UpdateL0(&ProfileInput{Name: "memhop", Role: "assistant"}); err != nil {
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

// The library-owned half of the profile is read-only on the host surface, and
// the shape is what enforces it: ProfileInput — the argument to Open, SubAgent
// and UpdateL0 — carries only the four host-owned fields, so an emotion, an MBTI
// type, a domain identity or a timestamp cannot be sent at all. Everything
// writable still comes back on the read shape. That a write inherits the
// distilled half rather than zeroing it is the engine's own contract
// (TestUpdateL0KeepsDistilledHalf), so it is not restated here.
func TestSurfaceL0DistilledHalfIsReadOnly(t *testing.T) {
	writable := map[string]bool{"Name": true, "Role": true, "Personality": true, "Preferences": true}
	if got := exportedFieldSet(reflect.TypeFor[ProfileInput]()); !maps.Equal(got, writable) {
		t.Fatalf("ProfileInput carries %v, want the host-owned fields %v",
			slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(writable)))
	}
	// Everything a host may write is also what it reads back, plus the four
	// fields only the library writes.
	for _, owned := range []string{"EmotionState", "MBTI", "AgentType", "UpdatedAtMs"} {
		writable[owned] = true
	}
	if got := exportedFieldSet(reflect.TypeFor[ProfileSlot]()); !maps.Equal(got, writable) {
		t.Fatalf("ProfileSlot carries %v, want %v",
			slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(writable)))
	}
}

func exportedFieldSet(typ reflect.Type) map[string]bool {
	out := make(map[string]bool, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		out[typ.Field(i).Name] = true
	}
	return out
}

// Closed-instance contract: after Close every domain operation is rejected
// with ErrClosed rather than touching a released engine.
func TestSurfaceClosedContract(t *testing.T) {
	llm := stubLLM()
	t.Cleanup(llm.Close)
	m, db := openSurfaceSession(t, llm.URL)
	if err := m.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if !m.IsClosed() {
		t.Fatal("IsClosed must report true after Close")
	}
	if _, err := db.GetL0(); CodeOf(err) != ErrClosed {
		t.Fatalf("GetL0 after close: want ErrClosed, got %v", err)
	}
	if _, err := db.Search(SearchQuery{}); CodeOf(err) != ErrClosed {
		t.Fatalf("Search after close: want ErrClosed, got %v", err)
	}
	if err := db.Update("0000000000000001", "0000000000000002"); CodeOf(err) != ErrClosed {
		t.Fatalf("Update after close: want ErrClosed, got %v", err)
	}
	// Double close is rejected with ErrClosed, not a panic.
	if err := m.Close(); CodeOf(err) != ErrClosed {
		t.Fatalf("double close: want ErrClosed, got %v", err)
	}
}

func TestSurfaceDreamEmptyDomain(t *testing.T) {
	db := openSurfaceDB(t)
	// A domain with no scenes yet succeeds without doing work.
	rep, err := db.Dream(context.Background(), "")
	if err != nil || rep == nil {
		t.Fatalf("dream on empty domain: rep=%v err=%v", rep, err)
	}
	if rep.ConsolidatedScenes != 0 {
		t.Fatalf("empty domain must not consolidate: %+v", rep)
	}
	// A directed dream on a nonexistent scene must parse-fail cleanly.
	if _, err := db.Dream(context.Background(), "bad-hex"); CodeOf(err) != ErrInvalidQuery {
		t.Fatalf("dream bad scene id: want ErrInvalidQuery, got %v", err)
	}
}

// An unusable set of arguments is rejected before anything is opened.
func TestSurfaceOpenValidatesArguments(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open("", surfaceLLM("http://127.0.0.1:1"), DefaultMemHopDefaults, surfaceProfile()); err == nil {
		t.Fatal("Open with an empty path must fail")
	}
	if _, err := Open(filepath.Join(dir, "a.meh"), LlmConfig{}, DefaultMemHopDefaults, surfaceProfile()); err == nil {
		t.Fatal("Open with an unspecified endpoint must fail")
	}
	if _, err := Open(filepath.Join(dir, "b.meh"), surfaceLLM("http://127.0.0.1:1"), DefaultMemHopDefaults, nil); err == nil {
		t.Fatal("Open on a file that is not there yet, with no primary profile, must fail")
	}
}

// A list this package maps encodes as [] even when the record behind it holds none:
// an imported node may carry no keywords, and a topic's track is empty only if a
// writer let it be — which the mapping has no way to know. One field answering null
// while its neighbours answer [] is two shapes for one answer, and a host decoding
// into a slice would have to special-case it.
func TestMappedListsEncodeAsEmptyNotNull(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
		want string
	}{
		{"a topic with no keyword track", mustEncode(t, fromTopicSlot(internal.TopicSlot{})), `"fused_keywords":[]`},
		{"a node with no keywords", mustEncode(t, fromHypergraphNode(internal.HypergraphNode{})), `"keywords":[]`},
		{"a profile with no preferences", mustEncode(t, fromProfileSlot(internal.ProfileSlot{})), `"preferences":{}`},
	} {
		if !strings.Contains(string(tc.body), tc.want) {
			t.Errorf("%s: json = %s, want it to hold %s", tc.name, tc.body, tc.want)
		}
	}
}

func mustEncode(t *testing.T, v any) []byte {
	t.Helper()
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode %T: %v", v, err)
	}
	return out
}
