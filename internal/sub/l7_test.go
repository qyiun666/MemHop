// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package sub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo"
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
// LLM: trajectory events → provider.Crystallize → L5 plugin with Path
// backfilled to the session ID.
func TestCrystallizeFullFlow(t *testing.T) {
	srv := mockLLMServer(t, `{"plugins":[{"name":"重构流程","trigger":"用户要求重构","plugin_type":"workflow","manifest":{"tools":[{"name":"read_file","config":"{\"file\":\"a.go\"}"},{"name":"write_file"}],"prompts":[{"name":"重构提示","config":"先读后写"}]}}]}`)
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
	if len(result.PluginIDs) != 1 {
		t.Fatalf("want 1 plugin, got %d", len(result.PluginIDs))
	}

	// Plugin persisted with Path = sessionID.
	plugin, err := db.GetPlugin(result.PluginIDs[0])
	if err != nil {
		t.Fatalf("get plugin: %v", err)
	}
	if plugin.Path == nil || *plugin.Path != session {
		t.Fatalf("path not backfilled: %+v", plugin)
	}
	if plugin.Name != "重构流程" || plugin.Trigger != "用户要求重构" || plugin.PluginType != "workflow" {
		t.Fatalf("plugin fields mismatch: %+v", plugin)
	}

	// Manifest persisted: tools in order with config, prompts section kept.
	tools := plugin.Manifest.Tools
	if len(tools) != 2 {
		t.Fatalf("want 2 tools, got %d", len(tools))
	}
	if tools[0].Name != "read_file" || tools[1].Name != "write_file" {
		t.Fatalf("tools mismatch: %+v", tools)
	}
	if tools[0].Config == nil || !strings.Contains(*tools[0].Config, "a.go") {
		t.Fatalf("tool config mismatch")
	}
	if tools[1].Config != nil {
		t.Fatalf("omitted config should stay nil")
	}
	if len(plugin.Manifest.Prompts) != 1 || plugin.Manifest.Prompts[0].Name != "重构提示" {
		t.Fatalf("prompts mismatch: %+v", plugin.Manifest.Prompts)
	}
}

// TestCrystallizeIdempotent re-crystallizing the same session returns the
// same plugin ID (name:trigger hash) instead of duplicating.
func TestCrystallizeIdempotent(t *testing.T) {
	srv := mockLLMServer(t, `{"plugins":[{"name":"发布流程","trigger":"准备发布时","plugin_type":"workflow","manifest":{"tools":[{"name":"run_test"}]}}]}`)
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
	if len(first.PluginIDs) != 1 || len(second.PluginIDs) != 1 || first.PluginIDs[0] != second.PluginIDs[0] {
		t.Fatalf("expected idempotent plugin ids, got %v vs %v", first.PluginIDs, second.PluginIDs)
	}
	plugins := repoListPlugins(t, db)
	if len(plugins) != 1 {
		t.Fatalf("want 1 plugin after re-crystallize, got %d", len(plugins))
	}
}

// mockLLMServerSeq serves one fixed completion response per request, in
// order, cycling back to the first when exhausted.
func mockLLMServerSeq(t *testing.T, contents ...string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	idx := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
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

// TestCrystallizeIdempotentPreservesRuntimeFields re-crystallizing the same
// session reuses the plugin ID, keeps host-accumulated runtime fields, and
// refreshes the manifest and type label.
func TestCrystallizeIdempotentPreservesRuntimeFields(t *testing.T) {
	srv := mockLLMServerSeq(t,
		`{"plugins":[{"name":"发布流程","trigger":"准备发布时","plugin_type":"workflow","manifest":{"tools":[{"name":"run_test"},{"name":"deploy"}]}}]}`,
		`{"plugins":[{"name":"发布流程","trigger":"准备发布时","plugin_type":"skill","manifest":{"skills":[{"name":"发布清单"}]}}]}`,
	)
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
	// Host accumulates runtime fields on the plugin.
	plugin, err := db.GetPlugin(first.PluginIDs[0])
	if err != nil {
		t.Fatalf("get plugin: %v", err)
	}
	plugin.Confidence = 0.9
	plugin.TriggerCount = 5
	plugin.Status = core.PluginActive
	if err := repo.UpdatePluginL5(db.engine, first.PluginIDs[0], plugin); err != nil {
		t.Fatalf("update plugin: %v", err)
	}
	// Re-crystallize with a different manifest and type label.
	second, err := db.Crystallize(context.Background(), session)
	if err != nil {
		t.Fatalf("second crystallize: %v", err)
	}
	if len(first.PluginIDs) != 1 || len(second.PluginIDs) != 1 || first.PluginIDs[0] != second.PluginIDs[0] {
		t.Fatalf("expected idempotent plugin ids, got %v vs %v", first.PluginIDs, second.PluginIDs)
	}
	got, err := db.GetPlugin(first.PluginIDs[0])
	if err != nil {
		t.Fatalf("get plugin: %v", err)
	}
	if got.Confidence != 0.9 || got.TriggerCount != 5 || got.Status != core.PluginActive {
		t.Fatalf("runtime fields reset by re-crystallize: %+v", got)
	}
	if got.PluginType != "skill" || len(got.Manifest.Skills) != 1 || len(got.Manifest.Tools) != 0 {
		t.Fatalf("manifest not refreshed: %+v", got)
	}
	if got.Path == nil || *got.Path != session {
		t.Fatalf("path not updated: %+v", got)
	}
}

func TestTruncateUTF8(t *testing.T) {
	s := "你好世界" // 3 bytes per rune
	if got := truncateUTF8(s, 4); got != "你" {
		t.Fatalf("truncate 4: %q", got)
	}
	if got := truncateUTF8(s, 6); got != "你好" {
		t.Fatalf("truncate 6: %q", got)
	}
	if got := truncateUTF8(s, 100); got != s {
		t.Fatalf("truncate 100: %q", got)
	}
	if got := truncateUTF8("a"+s, 5); !utf8.ValidString(got) {
		t.Fatalf("result invalid utf8: %q", got)
	}
}

func repoListPlugins(t *testing.T, db *DB) []core.PluginSlot {
	t.Helper()
	out, err := db.ListPlugins(PluginListQuery{})
	if err != nil {
		t.Fatalf("list plugins: %v", err)
	}
	return out
}

func TestParseCrystallizeResponse(t *testing.T) {
	resp := `{
  "plugins": [
    {"name": "重构流程", "trigger": "需要重构时", "plugin_type": "workflow", "manifest": {
      "tools": [{"name": "read_file", "config": "{\"file\":\"a.go\"}"}, {"name": "write_file"}],
      "skills": [{"name": "s1", "description": "d"}]
    }},
    {"name": "无名字", "trigger": "x", "manifest": {}},
    {"name": "", "trigger": "x", "manifest": {}}
  ]
}`
	out, err := parseCrystallizeResponse(resp)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(out.Plugins) != 2 {
		t.Fatalf("want 2 valid plugins, got %d", len(out.Plugins))
	}
	p := out.Plugins[0]
	if p.Name != "重构流程" || p.Trigger != "需要重构时" || p.PluginType != "workflow" {
		t.Fatalf("plugin mismatch: %+v", p)
	}
	if len(p.Manifest.Tools) != 2 || p.Manifest.Tools[0].Name != "read_file" || p.Manifest.Tools[1].Name != "write_file" {
		t.Fatalf("tools mismatch: %+v", p.Manifest.Tools)
	}
	if p.Manifest.Tools[0].Config == nil || !strings.Contains(*p.Manifest.Tools[0].Config, "a.go") {
		t.Fatalf("tool config mismatch")
	}
	if p.Manifest.Tools[1].Config != nil {
		t.Fatalf("omitted config should stay nil")
	}
	if len(p.Manifest.Skills) != 1 || p.Manifest.Skills[0].Name != "s1" {
		t.Fatalf("skills mismatch: %+v", p.Manifest.Skills)
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
