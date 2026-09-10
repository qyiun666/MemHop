// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/llm"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

func TestCrystallizeNoTrajectory(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	_, err := db.Crystallize(context.Background(), core.DefaultAgentID, common.FormatHash(1), nil)
	if err == nil {
		t.Fatal("crystallize on empty session should fail")
	}
}

// A turn's trajectory is keyed by the topic id Search issued for it, so
// Crystallize reads exactly that turn: no other turn folds in, and every
// event's own topic link names the turn that holds it.
func TestCrystallizeReadsOneTurnTopic(t *testing.T) {
	var mu sync.Mutex
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		gotBody = string(body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{"role": "assistant", "content": `{"capabilities":[]}`},
			}},
		})
	}))
	t.Cleanup(srv.Close)

	db := newTestDB(t, newTestEngine(t))
	db.llm = llm.New(&MemHopConfig{LLM: LlmConfig{APIURL: srv.URL, APIKey: "test", Model: "mock"}})
	const turnA, turnB = uint64(4242), uint64(4243)
	for _, ev := range []struct {
		turn uint64
		slot core.ArchiveSlot
	}{
		{turnA, core.ArchiveSlot{Kind: core.KindEvent, EventType: "llm_request", Content: "signal-turn-a", CreatedAt: 100}},
		{turnA, core.ArchiveSlot{Kind: core.KindEvent, EventType: "tool_call", Content: "signal-turn-a-2", CreatedAt: 150}},
		{turnB, core.ArchiveSlot{Kind: core.KindEvent, EventType: "llm_output", Content: "signal-turn-b", CreatedAt: 200}},
	} {
		if err := db.AppendArchive(core.DefaultAgentID, common.FormatHash(ev.turn), ev.slot); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	if _, err := db.Crystallize(context.Background(), core.DefaultAgentID, common.FormatHash(turnA), nil); err != nil {
		t.Fatalf("crystallize: %v", err)
	}
	mu.Lock()
	body := gotBody
	mu.Unlock()
	if !strings.Contains(body, "signal-turn-a") || !strings.Contains(body, "signal-turn-a-2") {
		t.Fatalf("prompt must carry the whole turn: %s", body)
	}
	if strings.Contains(body, "signal-turn-b") {
		t.Fatal("another turn's events must not leak into this one")
	}

	events, err := db.eventsOf(core.DefaultAgentID, common.FormatHash(turnA))
	if err != nil {
		t.Fatalf("read trajectory: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want the turn's 2", len(events))
	}
	for _, ev := range events {
		if ev.ContextID != turnA {
			t.Fatalf("event seq %d must key to its turn topic %d, got session=%d",
				ev.Seq, turnA, ev.ContextID)
		}
	}
}

func mockLLMServer(t *testing.T, content string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/chat/completions") == false {
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

func mockLLMServerSeq(t *testing.T, contents ...string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	idx := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/chat/completions") == false {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		content := contents[idx%len(contents)]
		idx++
		mu.Unlock()
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

// The engine extracts candidates only: the reply's cards come back as-is,
// nothing is stored, and the host's existing catalog (passed in) is rendered
// into the prompt so the model can reuse or merge by name.
func TestCrystallizeReturnsCandidatesAgainstHostCatalog(t *testing.T) {
	var mu sync.Mutex
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		gotBody = string(body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{"role": "assistant", "content": `{"capabilities":[
					{"action":"reuse","reuse_id":"发布流程","capability":{"name":"发布流程"}},
					{"action":"merge","reuse_id":"发布流程","capability":{"name":"发布流程","version":"2","summary":"并入回滚步骤","resources":[{"type":"mcp","name":"rollback"}]}},
					{"action":"create","capability":{"name":"重构流程","summary":"重构代码","trigger":"用户要求重构","resources":[{"type":"mcp","name":"read_file","config":"{\"file\":\"a.go\"}"},{"type":"mcp","name":"write_file"}]}}
				]}`},
			}},
		})
	}))
	t.Cleanup(srv.Close)

	db := newTestDB(t, newTestEngine(t))
	db.llm = llm.New(&MemHopConfig{LLM: LlmConfig{APIURL: srv.URL, APIKey: "test", Model: "mock"}})
	session := common.FormatHash(123)
	for i := 1; i <= 3; i++ {
		if err := db.AppendArchive(core.DefaultAgentID, session, core.ArchiveSlot{Kind: core.KindEvent, EventType: "tool_call", Content: "step", CreatedAt: int64(i)}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	existing := []CapabilityImport{{
		Name: "发布流程", Summary: "发布", Trigger: "准备发布时",
		Resources: []ResourceRef{{Type: CapabilityMCP, Name: "run_test"}},
	}}
	out, err := db.Crystallize(context.Background(), core.DefaultAgentID, session, existing)
	if err != nil {
		t.Fatalf("crystallize: %v", err)
	}
	mu.Lock()
	body := gotBody
	mu.Unlock()
	if !strings.Contains(body, "发布流程") {
		t.Fatalf("prompt must render the host's existing catalog: %s", body)
	}
	if len(out.Capabilities) != 3 {
		t.Fatalf("candidates = %d, want 3: %+v", len(out.Capabilities), out)
	}
	reuse := out.Capabilities[0]
	if reuse.Action != "reuse" || reuse.ReuseID != "发布流程" {
		t.Fatalf("reuse candidate mismatch: %+v", reuse)
	}
	merge := out.Capabilities[1]
	if merge.Action != "merge" || merge.ReuseID != "发布流程" ||
		merge.Capability.Summary != "并入回滚步骤" || len(merge.Capability.Resources) != 1 ||
		merge.Capability.Resources[0].Name != "rollback" {
		t.Fatalf("merge candidate mismatch: %+v", merge)
	}
	create := out.Capabilities[2]
	if create.Action != "create" || create.Capability.Name != "重构流程" {
		t.Fatalf("create candidate mismatch: %+v", create)
	}
	if len(create.Capability.Resources) != 2 || create.Capability.Resources[0].Name != "read_file" {
		t.Fatalf("create resources mismatch: %+v", create.Capability.Resources)
	}
}

// Filtering is the host's job now: a candidate that fails card validation
// (no resources) is returned unfiltered, not skipped.
func TestCrystallizeCandidatesPassThroughUnvalidated(t *testing.T) {
	srv := mockLLMServer(t, `{"capabilities":[
		{"action":"create","capability":{"name":"无效能力","summary":"s","trigger":"t"}}
	]}`)
	db := newTestDB(t, newTestEngine(t))
	db.llm = llm.New(&MemHopConfig{LLM: LlmConfig{APIURL: srv.URL, APIKey: "test", Model: "mock"}})
	session := common.FormatHash(888)
	if err := db.AppendArchive(core.DefaultAgentID, session, core.ArchiveSlot{
		Kind: core.KindEvent, EventType: "tool_call", Content: "step", CreatedAt: 1,
	}); err != nil {
		t.Fatal(err)
	}
	out, err := db.Crystallize(context.Background(), core.DefaultAgentID, session, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Capabilities) != 1 || out.Capabilities[0].Capability.Name != "无效能力" {
		t.Fatalf("invalid candidate must pass through: %+v", out)
	}
}
