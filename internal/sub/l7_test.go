// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package sub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
)

func TestAppendTrajectorySeqAutoIncrement(t *testing.T) {
	db := &DB{engine: newTestEngine(t)}
	session := common.FormatHash(99)
	for i := 1; i <= 3; i++ {
		if err := db.AppendTrajectory(session, core.TrajectorySlot{EventType: "turn_start", Timestamp: int64(i)}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	events, err := db.ReadTrajectory(session)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("want 3 events, got %d", len(events))
	}
	for i, ev := range events {
		if ev.Seq != uint64(i+1) {
			t.Fatalf("seq[%d] = %d, want %d", i, ev.Seq, i+1)
		}
	}
}

func TestAppendTrajectoryValidation(t *testing.T) {
	db := &DB{engine: newTestEngine(t)}
	if err := db.AppendTrajectory(common.FormatHash(1), core.TrajectorySlot{Timestamp: 1}); err == nil {
		t.Fatal("empty event type should fail")
	}
	if err := db.AppendTrajectory(common.FormatHash(1), core.TrajectorySlot{EventType: "tool_call"}); err == nil {
		t.Fatal("zero timestamp should fail")
	}
}

func TestAppendTrajectoryPayloadTruncated(t *testing.T) {
	db := &DB{engine: newTestEngine(t)}
	long := strings.Repeat("x", maxTrajectoryPayload+100)
	if err := db.AppendTrajectory(common.FormatHash(3), core.TrajectorySlot{EventType: "tool_call", Payload: long, Timestamp: 1}); err != nil {
		t.Fatalf("append: %v", err)
	}
	events, err := db.ReadTrajectory(common.FormatHash(3))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != 1 || len(events[0].Payload) > maxTrajectoryPayload {
		t.Fatalf("payload not truncated: %d bytes", len(events[0].Payload))
	}
}

func TestDeleteTrajectorySub(t *testing.T) {
	db := &DB{engine: newTestEngine(t)}
	session := common.FormatHash(77)
	if err := db.AppendTrajectory(session, core.TrajectorySlot{EventType: "turn_start", Timestamp: 1}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := db.DeleteTrajectory(session); err != nil {
		t.Fatalf("delete: %v", err)
	}
	events, err := db.ReadTrajectory(session)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("want empty trajectory, got %d events", len(events))
	}
}

func TestCrystallizeNoTrajectory(t *testing.T) {
	db := &DB{engine: newTestEngine(t)}
	_, err := db.Crystallize(context.Background(), common.FormatHash(1))
	if err == nil {
		t.Fatal("crystallize on empty session should fail")
	}
}

// mockLLMServer serves a fixed crystallize completion response on the
// OpenAI-compatible chat/completions endpoint.
func mockLLMServer(t *testing.T, content string) *httptest.Server {
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

// TestCrystallizeFullFlow drives the whole L7 → L5 pipeline against a mock
// LLM: trajectory events → provider.Crystallize → L5 chain + steps with
// Path backfilled to the session ID.
func TestCrystallizeFullFlow(t *testing.T) {
	srv := mockLLMServer(t, `{"chains":[{"title":"重构流程","trigger":"用户要求重构","steps":[{"action":"read_file","parameters":"{\"file\":\"a.go\"}"},{"action":"write_file"}]}]}`)
	db := &DB{
		engine: newTestEngine(t),
		llm:    New(&MemHopConfig{LLM: LlmConfig{APIURL: srv.URL, APIKey: "test", Model: "mock"}}),
	}
	session := common.FormatHash(123)
	for i := 1; i <= 3; i++ {
		if err := db.AppendTrajectory(session, core.TrajectorySlot{EventType: "tool_call", Payload: "step", Timestamp: int64(i)}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	result, err := db.Crystallize(context.Background(), session)
	if err != nil {
		t.Fatalf("crystallize: %v", err)
	}
	if len(result.ChainIDs) != 1 {
		t.Fatalf("want 1 chain, got %d", len(result.ChainIDs))
	}

	// Chain persisted with Path = sessionID.
	chain, err := db.GetL5(result.ChainIDs[0])
	if err != nil {
		t.Fatalf("get chain: %v", err)
	}
	if chain.Path == nil || *chain.Path != session {
		t.Fatalf("path not backfilled: %+v", chain)
	}
	if chain.Title != "重构流程" || chain.Trigger != "用户要求重构" {
		t.Fatalf("chain fields mismatch: %+v", chain)
	}

	// Steps persisted in order with parameters.
	steps := core.CollectAllActionSteps(db.engine)
	if len(steps) != 2 {
		t.Fatalf("want 2 steps, got %d", len(steps))
	}
	if steps[0].StepOrder != 1 || steps[0].Action != "read_file" || steps[1].StepOrder != 2 || steps[1].Action != "write_file" {
		t.Fatalf("steps mismatch: %+v", steps)
	}
	if steps[0].Parameters == nil || *steps[0].Parameters != `{"file":"a.go"}` {
		t.Fatalf("step parameters mismatch")
	}
	if steps[1].Parameters != nil {
		t.Fatalf("omitted parameters should stay nil")
	}
}

// TestCrystallizeIdempotent re-crystallizing the same session returns the
// same chain ID (title:trigger hash) instead of duplicating.
func TestCrystallizeIdempotent(t *testing.T) {
	srv := mockLLMServer(t, `{"chains":[{"title":"发布流程","trigger":"准备发布时","steps":[{"action":"run_test"}]}]}`)
	db := &DB{
		engine: newTestEngine(t),
		llm:    New(&MemHopConfig{LLM: LlmConfig{APIURL: srv.URL, APIKey: "test", Model: "mock"}}),
	}
	session := common.FormatHash(456)
	if err := db.AppendTrajectory(session, core.TrajectorySlot{EventType: "tool_call", Payload: "p", Timestamp: 1}); err != nil {
		t.Fatalf("append: %v", err)
	}
	first, err := db.Crystallize(context.Background(), session)
	if err != nil {
		t.Fatalf("first crystallize: %v", err)
	}
	second, err := db.Crystallize(context.Background(), session)
	if err != nil {
		t.Fatalf("second crystallize: %v", err)
	}
	if len(first.ChainIDs) != 1 || len(second.ChainIDs) != 1 || first.ChainIDs[0] != second.ChainIDs[0] {
		t.Fatalf("expected idempotent chain ids, got %v vs %v", first.ChainIDs, second.ChainIDs)
	}
	chains := repoListChains(t, db)
	if len(chains) != 1 {
		t.Fatalf("want 1 chain after re-crystallize, got %d", len(chains))
	}
}

func repoListChains(t *testing.T, db *DB) []core.ActionChainSlot {
	t.Helper()
	out, err := db.ListL5(L5ListQuery{})
	if err != nil {
		t.Fatalf("list chains: %v", err)
	}
	return out
}

func TestParseCrystallizeResponse(t *testing.T) {
	resp := `{
  "chains": [
    {"title": "重构流程", "trigger": "需要重构时", "steps": [
      {"action": "read_file", "parameters": "{\"file\":\"a.go\"}"},
      {"action": "write_file"}
    ]},
    {"title": "无步骤链", "trigger": "x", "steps": []}
  ]
}`
	out, err := parseCrystallizeResponse(resp)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(out.Chains) != 1 {
		t.Fatalf("want 1 valid chain, got %d", len(out.Chains))
	}
	c := out.Chains[0]
	if c.Title != "重构流程" || c.Trigger != "需要重构时" {
		t.Fatalf("chain mismatch: %+v", c)
	}
	if len(c.Steps) != 2 || c.Steps[0].Action != "read_file" || c.Steps[1].Action != "write_file" {
		t.Fatalf("steps mismatch: %+v", c.Steps)
	}
	if c.Steps[0].Parameters == nil || !strings.Contains(*c.Steps[0].Parameters, "a.go") {
		t.Fatalf("step parameters mismatch")
	}
	if c.Steps[1].Parameters != nil {
		t.Fatalf("omitted parameters should stay nil")
	}
}

func TestBuildCrystallizePrompt(t *testing.T) {
	events := []core.TrajectorySlot{
		{Seq: 1, EventType: "turn_start", Payload: "hi", Timestamp: 100},
		{Seq: 2, EventType: "tool_call", Payload: "read a.go", Timestamp: 200},
	}
	prompt := buildCrystallizePrompt(events)
	if !strings.Contains(prompt, "tool_call") || !strings.Contains(prompt, "read a.go") {
		t.Fatalf("prompt missing events: %s", prompt)
	}
}
