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
	"github.com/qyiun666/MemHop/internal/repo/core"
)

func TestCrystallizeNoTrajectory(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	_, err := db.Crystallize(context.Background(), core.DefaultAgentID, common.FormatHash(1))
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
	db.llm = New(&MemHopConfig{LLM: LlmConfig{APIURL: srv.URL, APIKey: "test", Model: "mock"}})
	const turnA, turnB = uint64(4242), uint64(4243)
	for _, ev := range []struct {
		turn uint64
		slot core.TrajectorySlot
	}{
		{turnA, core.TrajectorySlot{EventType: "llm_request", Payload: "signal-turn-a", Timestamp: 100}},
		{turnA, core.TrajectorySlot{EventType: "tool_call", Payload: "signal-turn-a-2", Timestamp: 150}},
		{turnB, core.TrajectorySlot{EventType: "llm_output", Payload: "signal-turn-b", Timestamp: 200}},
	} {
		if err := db.AppendTrajectory(core.DefaultAgentID, common.FormatHash(ev.turn), ev.slot); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	if _, err := db.Crystallize(context.Background(), core.DefaultAgentID, common.FormatHash(turnA)); err != nil {
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

	events, err := db.ReadTrajectory(core.DefaultAgentID, common.FormatHash(turnA))
	if err != nil {
		t.Fatalf("read trajectory: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want the turn's 2", len(events))
	}
	for _, ev := range events {
		if ev.SessionID != turnA || ev.TopicID != turnA {
			t.Fatalf("event seq %d must key to its turn topic %d, got session=%d topic=%d",
				ev.Seq, turnA, ev.SessionID, ev.TopicID)
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

func TestCrystallizeFullFlow(t *testing.T) {
	srv := mockLLMServer(t, `{"capabilities":[{"action":"create","capability":{"name":"重构流程","type":"composite","summary":"重构代码","trigger":"用户要求重构","resources":[{"type":"mcp","name":"read_file","config":"{\"file\":\"a.go\"}"},{"type":"mcp","name":"write_file"}]}}]}`)
	db := newTestDB(t, newTestEngine(t))
	db.llm = New(&MemHopConfig{LLM: LlmConfig{APIURL: srv.URL, APIKey: "test", Model: "mock"}})
	session := common.FormatHash(123)
	for i := 1; i <= 3; i++ {
		if err := db.AppendTrajectory(core.DefaultAgentID, session, core.TrajectorySlot{EventType: "tool_call", Payload: "step", Timestamp: int64(i)}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	result, err := db.Crystallize(context.Background(), core.DefaultAgentID, session)
	if err != nil {
		t.Fatalf("crystallize: %v", err)
	}
	if len(result.CreatedIDs) != 1 || len(result.ReusedIDs) != 0 || len(result.MergedIDs) != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}

	cap, err := db.GetCapability(core.DefaultAgentID, result.CreatedIDs[0])
	if err != nil {
		t.Fatalf("get capability: %v", err)
	}
	if cap.Status != core.CapabilityDraft || cap.Origin != core.CapabilityOriginCrystallized {
		t.Fatalf("crystallized capability metadata mismatch: %+v", cap)
	}
	if cap.Type != core.CapabilityComposite || cap.Name != "重构流程" {
		t.Fatalf("capability fields mismatch: %+v", cap)
	}
	if len(cap.Resources) != 2 || cap.Resources[0].Name != "read_file" || cap.Resources[1].Name != "write_file" {
		t.Fatalf("resources mismatch: %+v", cap.Resources)
	}
}

func TestCrystallizeReusesExisting(t *testing.T) {
	srv := mockLLMServer(t, `{"capabilities":[{"action":"create","capability":{"name":"发布流程","type":"mcp","summary":"发布","trigger":"准备发布时","resources":[{"type":"mcp","name":"run_test"}]}}]}`)
	db := newTestDB(t, newTestEngine(t))
	db.llm = New(&MemHopConfig{LLM: LlmConfig{APIURL: srv.URL, APIKey: "test", Model: "mock"}})
	session := common.FormatHash(456)
	if err := db.AppendTrajectory(core.DefaultAgentID, session, core.TrajectorySlot{EventType: "tool_call", Payload: "p", Timestamp: 1}); err != nil {
		t.Fatalf("append: %v", err)
	}
	first, err := db.Crystallize(context.Background(), core.DefaultAgentID, session)
	if err != nil {
		t.Fatalf("first crystallize: %v", err)
	}
	if len(first.CreatedIDs) != 1 {
		t.Fatalf("first result: %+v", first)
	}

	secondContent := `{"capabilities":[{"action":"reuse","reuse_id":"` + first.CreatedIDs[0] + `","capability":{"name":"发布流程","type":"mcp","summary":"发布","trigger":"准备发布时","resources":[{"type":"mcp","name":"run_test"}]}}]}`
	srv2 := mockLLMServer(t, secondContent)
	db.llm = New(&MemHopConfig{LLM: LlmConfig{APIURL: srv2.URL, APIKey: "test", Model: "mock"}})
	second, err := db.Crystallize(context.Background(), core.DefaultAgentID, session)
	if err != nil {
		t.Fatalf("second crystallize: %v", err)
	}
	if len(second.CreatedIDs) != 0 || len(second.ReusedIDs) != 1 || second.ReusedIDs[0] != first.CreatedIDs[0] {
		t.Fatalf("second result: %+v", second)
	}
	caps, err := db.ListCapabilities(core.DefaultAgentID, CapabilityListQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(caps) != 1 {
		t.Fatalf("want 1 capability, got %d", len(caps))
	}
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

func TestCrystallizeReuseMinimalPayload(t *testing.T) {
	srv := mockLLMServer(t, `{"capabilities":[{"action":"create","capability":{"name":"最小复用","type":"mcp","summary":"s","trigger":"t","resources":[{"type":"mcp","name":"x"}]}}]}`)
	db := newTestDB(t, newTestEngine(t))
	db.llm = New(&MemHopConfig{LLM: LlmConfig{APIURL: srv.URL, APIKey: "test", Model: "mock"}})
	session := common.FormatHash(321)
	if err := db.AppendTrajectory(core.DefaultAgentID, session, core.TrajectorySlot{EventType: "tool_call", Payload: "p", Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	first, err := db.Crystallize(context.Background(), core.DefaultAgentID, session)
	if err != nil {
		t.Fatalf("first crystallize: %v", err)
	}

	// A reuse decision carries a minimal payload (name only, no
	// type/resources): it must be accepted, not rejected by import
	// validation.
	secondContent := `{"capabilities":[{"action":"reuse","reuse_id":"` + first.CreatedIDs[0] + `","capability":{"name":"最小复用"}}]}`
	srv2 := mockLLMServer(t, secondContent)
	db.llm = New(&MemHopConfig{LLM: LlmConfig{APIURL: srv2.URL, APIKey: "test", Model: "mock"}})
	second, err := db.Crystallize(context.Background(), core.DefaultAgentID, session)
	if err != nil {
		t.Fatalf("second crystallize: %v", err)
	}
	if len(second.Errors) != 0 {
		t.Fatalf("minimal reuse produced errors: %+v", second.Errors)
	}
	if len(second.ReusedIDs) != 1 || second.ReusedIDs[0] != first.CreatedIDs[0] {
		t.Fatalf("minimal reuse must be accepted: %+v", second)
	}
}

func TestCrystallizeCreateDoesNotOverwriteActiveByName(t *testing.T) {
	srv := mockLLMServer(t, `{"capabilities":[{"action":"create","capability":{"name":"已有能力","type":"mcp","summary":"新摘要","trigger":"新触发","resources":[{"type":"mcp","name":"new_tool"}]}}]}`)
	db := newTestDB(t, newTestEngine(t))
	db.llm = New(&MemHopConfig{LLM: LlmConfig{APIURL: srv.URL, APIKey: "test", Model: "mock"}})
	cap := &core.Capability{
		IDHash: core.CapabilityID("已有能力"), Name: "已有能力", Type: core.CapabilityMCP,
		Summary: "旧摘要", Trigger: "旧触发", Status: core.CapabilityActive,
		Origin: core.CapabilityOriginImported, Resources: []core.ResourceRef{{Type: core.CapabilityMCP, Name: "old_tool"}},
	}
	if err := core.WriteCapability(db.engine, core.DefaultAgentID, cap.IDHash, cap); err != nil {
		t.Fatal(err)
	}
	session := common.FormatHash(789)
	if err := db.AppendTrajectory(core.DefaultAgentID, session, core.TrajectorySlot{EventType: "tool_call", Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	result, err := db.Crystallize(context.Background(), core.DefaultAgentID, session)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.CreatedIDs) != 0 || len(result.ReusedIDs) != 1 {
		t.Fatalf("same-name create should be reuse: %+v", result)
	}
	got, err := db.GetCapability(core.DefaultAgentID, common.FormatHash(cap.IDHash))
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary != "旧摘要" || got.Resources[0].Name != "old_tool" {
		t.Fatalf("active capability was overwritten: %+v", got)
	}
}

func TestCrystallizeDetails(t *testing.T) {
	// Phase 1: one valid create + one invalid create (mcp type without the
	// required resource → validate fails → skip detail).
	srv := mockLLMServer(t, `{"capabilities":[
		{"action":"create","capability":{"name":"明细测试能力","type":"composite","summary":"s","trigger":"t","resources":[{"type":"mcp","name":"x"}]}},
		{"action":"create","capability":{"name":"无效能力","type":"mcp","summary":"s","trigger":"t"}}
	]}`)
	db := newTestDB(t, newTestEngine(t))
	db.llm = New(&MemHopConfig{LLM: LlmConfig{APIURL: srv.URL, APIKey: "test", Model: "mock"}})
	session := common.FormatHash(888)
	if err := db.AppendTrajectory(core.DefaultAgentID, session, core.TrajectorySlot{EventType: "tool_call", Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	first, err := db.Crystallize(context.Background(), core.DefaultAgentID, session)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.CreatedIDs) != 1 || len(first.Errors) != 1 {
		t.Fatalf("phase1 result: %+v", first)
	}
	if len(first.Details) != 2 {
		t.Fatalf("phase1 Details = %d, want 2", len(first.Details))
	}
	byName := map[string]CrystallizeDetail{}
	for _, d := range first.Details {
		byName[d.Name] = d
	}
	create := byName["明细测试能力"]
	if create.Action != "create" || create.CapabilityID != first.CreatedIDs[0] || create.Reason != "" {
		t.Fatalf("create detail: %+v", create)
	}
	skip := byName["无效能力"]
	if skip.Action != "skip" || skip.CapabilityID != "" || skip.Reason == "" {
		t.Fatalf("skip detail: %+v", skip)
	}

	// Phase 2: reuse the created capability → detail carries the reused ID.
	srv2 := mockLLMServer(t, `{"capabilities":[{"action":"reuse","reuse_id":"`+first.CreatedIDs[0]+`","capability":{"name":"明细测试能力"}}]}`)
	db.llm = New(&MemHopConfig{LLM: LlmConfig{APIURL: srv2.URL, APIKey: "test", Model: "mock"}})
	second, err := db.Crystallize(context.Background(), core.DefaultAgentID, session)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.ReusedIDs) != 1 || len(second.Details) != 1 {
		t.Fatalf("phase2 result: %+v", second)
	}
	reuse := second.Details[0]
	if reuse.Action != "reuse" || reuse.CapabilityID != first.CreatedIDs[0] || reuse.Reason != "" {
		t.Fatalf("reuse detail: %+v", reuse)
	}
}
