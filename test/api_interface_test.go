// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Offline interface tests: exercise the public API surface through
// memhop.OpenWithEncoder with a mock encoder and a mock OpenAI-compatible
// LLM server. No external services required; run with `go test ./test/...`.

package test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	memhop "github.com/qyiun666/MemHop"
	"github.com/qyiun666/MemHop/internal/sub"
	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
)

// mockEncoder implements sub.Encoder with a deterministic pseudo-vector.
type mockEncoder struct{ dim int }

func (m *mockEncoder) Encode(text string) ([]float32, error) {
	vec := make([]float32, m.dim)
	h := uint64(1469598103934665603) // FNV offset basis
	for _, r := range text {
		h = (h ^ uint64(r)) * 1099511628211
	}
	for i := range vec {
		vec[i] = float32((h>>(uint(i)%8*8))&0xff) / 255.0
	}
	return vec, nil
}

func (m *mockEncoder) IsAvailable() bool { return true }

// mockLLM serves OpenAI-compatible /chat/completions and dispatches by the
// system prompt of each LLM call point; call counters are exposed.
type mockLLM struct {
	srv   *httptest.Server
	calls map[string]int
}

func newMockLLM(t *testing.T) *mockLLM {
	t.Helper()
	m := &mockLLM{calls: map[string]int{}}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var sys, user string
		for _, msg := range req.Messages {
			switch msg.Role {
			case "system":
				sys = msg.Content
			case "user":
				user = msg.Content
			}
		}
		var content string
		lower := strings.ToLower(sys)
		switch {
		case strings.Contains(lower, "extract keywords"):
			m.calls["keywords"]++
			content = `{"keywords":["重构","代码","测试"]}`
		case strings.Contains(lower, "l2 chat memory"):
			m.calls["consolidate"]++
			content = consolidateReply(user)
		case strings.Contains(lower, "l1 associative"):
			m.calls["distill"]++
			content = `{"emotion":{"valence":0.8,"arousal":0.6,"dominance":0.5},"mbti":{"i_e":0.2,"n_s":0.3,"t_f":-0.1,"j_p":0.4,"type":"ESFP"},"per_node":[]}`
		case strings.Contains(lower, "operation trajectory"):
			m.calls["crystallize"]++
			content = `{"plugins":[{"name":"重构流程","trigger":"用户要求重构","plugin_type":"workflow","manifest":{"tools":[{"name":"read_file","config":"{\"file\":\"a.go\"}"}]}}]}`
		default:
			t.Errorf("mockLLM: unknown system prompt: %.80s", sys)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		resp := map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": content}}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(m.srv.Close)
	return m
}

// consolidateReply builds a merge group from the first two topic ids echoed
// in the consolidate user prompt ("- id=... depth=..." lines).
func consolidateReply(user string) string {
	idRe := regexp.MustCompile(`id=(\d+)`)
	sceneRe := regexp.MustCompile(`## scene_id = (\d+)`)
	ids := idRe.FindAllStringSubmatch(user, -1)
	scene := sceneRe.FindStringSubmatch(user)
	if len(scene) == 0 || len(ids) < 2 {
		return `{"l2_groups":[],"l2_compression_needed":false}`
	}
	return fmt.Sprintf(`{"l2_groups":[{"scene_id":%s,"node_hashes":[%s,%s],"merged_title":"合并主题","merged_summary":"合并摘要保留全部细节"}],"l2_compression_needed":true}`,
		scene[1], ids[0][1], ids[1][1])
}

// openTestDB opens a DB backed by mock encoder + mock LLM in a temp dir.
func openTestDB(t *testing.T) (*memhop.DB, *mockLLM) {
	t.Helper()
	llm := newMockLLM(t)
	cfg := &memhop.MemHopConfig{
		DBPath:     filepath.Join(t.TempDir(), "test.meh"),
		VectorDim:  16,
		EmbedModel: "mock-embed",
	}
	cfg.LLM.APIURL = llm.srv.URL
	cfg.LLM.APIKey = "mock-key"
	cfg.LLM.Model = "mock-model"
	cfg.Defaults = *sub.DefaultMemHopDefaults
	db, err := memhop.OpenWithEncoder(cfg, &mockEncoder{dim: 16})
	if err != nil {
		t.Fatalf("OpenWithEncoder: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db, llm
}

func TestInterfaceOpenClose(t *testing.T) {
	db, _ := openTestDB(t)
	if db.IsClosed() {
		t.Fatal("db should be open after OpenWithEncoder")
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !db.IsClosed() {
		t.Fatal("db should be closed after Close")
	}
}

func TestInterfaceSearchUpdateL2L4(t *testing.T) {
	db, _ := openTestDB(t)
	ts := time.Now().UnixMilli()

	// Search with AutoCreate creates a scene + topic + L4 archive + centroid.
	res, err := db.Search(memhop.SearchQuery{Text: "用户要求重构代码", AutoCreate: true, Timestamp: ts})
	if err != nil {
		t.Fatalf("Search(auto_create): %v", err)
	}
	if res.NewTopicID == 0 || len(res.Contexts) == 0 {
		t.Fatalf("auto-create should return new topic and contexts: %+v", res)
	}

	// Update appends an agent reply to the topic.
	topicID := common.FormatHash(res.NewTopicID)
	if !db.Update(topicID, "好的,我来重构这段代码", ts+1000) {
		t.Fatal("Update should succeed on an existing topic")
	}
	// Update on a missing topic fails silently.
	if db.Update(common.FormatHash(999), "无主话题", ts+2000) {
		t.Fatal("Update on missing topic should fail")
	}

	// Normal Search retrieves the stored contexts.
	res2, err := db.Search(memhop.SearchQuery{Text: "重构代码", Timestamp: ts + 3000})
	if err != nil {
		t.Fatalf("Search(normal): %v", err)
	}
	if len(res2.Contexts) == 0 {
		t.Fatal("normal search should retrieve contexts")
	}

	// L2: scenes created by Search are listable.
	scenes, err := db.ListScenes()
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	if len(scenes) == 0 {
		t.Fatal("ListScenes should return the scene created by Search")
	}

	// L4: archives written by Search/Update are searchable.
	arcs, err := db.SearchL4(memhop.L4Query{Keyword: "重构"})
	if err != nil {
		t.Fatalf("SearchL4: %v", err)
	}
	if len(arcs) == 0 {
		t.Fatal("SearchL4 should find archives by keyword")
	}
	if _, err := db.GetArchive(common.FormatHash(arcs[0].IDHash)); err != nil {
		t.Fatalf("GetArchive: %v", err)
	}

	// Validation: Timestamp is required.
	if _, err := db.Search(memhop.SearchQuery{Text: "重构"}); err == nil {
		t.Fatal("Search without Timestamp should fail")
	}
}

func TestInterfaceL0(t *testing.T) {
	db, _ := openTestDB(t)
	slot := &core.ProfileSlot{
		Name:        "测试画像",
		Preferences: map[string]string{"language": "Go"},
		StyleTraits: []string{"prefers_brevity"},
	}
	if err := db.UpdateL0(slot); err != nil {
		t.Fatalf("UpdateL0: %v", err)
	}
	got, err := db.GetL0()
	if err != nil {
		t.Fatalf("GetL0: %v", err)
	}
	if got.Name != "测试画像" || got.Preferences["language"] != "Go" {
		t.Fatalf("L0 mismatch: %+v", got)
	}
}

func TestInterfaceL3(t *testing.T) {
	db, _ := openTestDB(t)
	res, err := db.ImportL3([]memhop.L3ImportItem{
		{Title: "Go 内存模型", Domain: "go", NodeType: "concept",
			Content: "Go 内存模型定义了 happens-before 规则", Keywords: []string{"go", "内存"}},
	}, memhop.L3ImportSkip)
	if err != nil {
		t.Fatalf("ImportL3: %v", err)
	}
	if len(res.CreatedIDs) == 0 {
		t.Fatalf("ImportL3 should create nodes: %+v", res)
	}

	graphs, err := db.ListL3()
	if err != nil {
		t.Fatalf("ListL3: %v", err)
	}
	if len(graphs) != 1 {
		t.Fatalf("want 1 graph, got %d", len(graphs))
	}
	graphID := common.FormatHash(graphs[0].IDHash)

	g, err := db.GetL3(graphID)
	if err != nil {
		t.Fatalf("GetL3: %v", err)
	}
	if len(g.Nodes) == 0 {
		t.Fatal("GetL3 should return nodes")
	}

	nodes, err := db.QueryL3Nodes(memhop.L3NodeQuery{GraphID: graphID, Keyword: "go"})
	if err != nil {
		t.Fatalf("QueryL3Nodes: %v", err)
	}
	if len(nodes) == 0 {
		t.Fatal("QueryL3Nodes should find the imported node")
	}

	subgraph, err := db.QueryL3Subgraph(graphID, common.FormatHash(nodes[0].IDHash), 2, nil)
	if err != nil {
		t.Fatalf("QueryL3Subgraph: %v", err)
	}
	if len(subgraph.Nodes) == 0 {
		t.Fatal("QueryL3Subgraph should return nodes")
	}

	newName := "改名"
	if _, err := db.UpdateL3(graphID, &newName); err != nil {
		t.Fatalf("UpdateL3: %v", err)
	}
	if err := db.DeleteL3(graphID); err != nil {
		t.Fatalf("DeleteL3: %v", err)
	}
	graphs, err = db.ListL3()
	if err != nil {
		t.Fatalf("ListL3 after delete: %v", err)
	}
	if len(graphs) != 0 {
		t.Fatalf("want 0 graphs after delete, got %d", len(graphs))
	}
}

func TestInterfaceL5(t *testing.T) {
	db, _ := openTestDB(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.json")
	content := `{"name":"重构流程","trigger":"用户要求重构","plugin_type":"workflow","manifest":{"tools":[{"name":"read_file"}]}}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write plugin file: %v", err)
	}

	id, err := db.ImportPlugin(path)
	if err != nil {
		t.Fatalf("ImportPlugin: %v", err)
	}
	plugin, err := db.GetPlugin(id)
	if err != nil {
		t.Fatalf("GetPlugin: %v", err)
	}
	if plugin.Name != "重构流程" || plugin.PluginType != "workflow" {
		t.Fatalf("plugin mismatch: %+v", plugin)
	}
	plugins, err := db.ListPlugins(memhop.PluginListQuery{})
	if err != nil {
		t.Fatalf("ListPlugins: %v", err)
	}
	if len(plugins) != 1 {
		t.Fatalf("want 1 plugin, got %d", len(plugins))
	}

	// Search matches plugins by trigger text.
	res, err := db.Search(memhop.SearchQuery{Text: "用户要求重构代码", AutoCreate: true, Timestamp: time.Now().UnixMilli()})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Plugins) == 0 {
		t.Fatal("Search should match the imported plugin by trigger")
	}

	if err := db.DeletePlugin(id); err != nil {
		t.Fatalf("DeletePlugin: %v", err)
	}
	plugins, err = db.ListPlugins(memhop.PluginListQuery{})
	if err != nil {
		t.Fatalf("ListPlugins after delete: %v", err)
	}
	if len(plugins) != 0 {
		t.Fatalf("want 0 plugins after delete, got %d", len(plugins))
	}
}

func TestInterfaceL7(t *testing.T) {
	db, _ := openTestDB(t)
	session := "0000000000000001"
	ts := time.Now().UnixMilli()

	if err := db.AppendTrajectory(session, core.TrajectorySlot{
		EventType: "tool_call", Payload: `{"tool":"read_file","file":"a.go"}`, Timestamp: ts,
	}); err != nil {
		t.Fatalf("AppendTrajectory: %v", err)
	}
	if err := db.AppendTrajectory(session, core.TrajectorySlot{
		EventType: "tool_result", Payload: "file content", Timestamp: ts + 500,
	}); err != nil {
		t.Fatalf("AppendTrajectory #2: %v", err)
	}
	events, err := db.ReadTrajectory(session)
	if err != nil {
		t.Fatalf("ReadTrajectory: %v", err)
	}
	if len(events) != 2 || events[0].Seq != 1 || events[1].Seq != 2 {
		t.Fatalf("want 2 events with seq 1,2: %+v", events)
	}

	// Crystallize turns the trajectory into an L5 plugin via the mock LLM.
	res, err := db.Crystallize(context.Background(), session)
	if err != nil {
		t.Fatalf("Crystallize: %v", err)
	}
	if len(res.PluginIDs) != 1 {
		t.Fatalf("want 1 plugin id: %+v", res)
	}
	plugins, err := db.ListPlugins(memhop.PluginListQuery{})
	if err != nil {
		t.Fatalf("ListPlugins after crystallize: %v", err)
	}
	if len(plugins) != 1 {
		t.Fatalf("want 1 plugin after crystallize, got %d", len(plugins))
	}

	if err := db.DeleteTrajectory(session); err != nil {
		t.Fatalf("DeleteTrajectory: %v", err)
	}
	events, err = db.ReadTrajectory(session)
	if err != nil {
		t.Fatalf("ReadTrajectory after delete: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("want 0 events after delete, got %d", len(events))
	}
}

func TestInterfaceDream(t *testing.T) {
	// Lower the compress threshold so two topics in one scene trigger the
	// consolidate call.
	llm := newMockLLM(t)
	cfg := &memhop.MemHopConfig{
		DBPath:     filepath.Join(t.TempDir(), "test.meh"),
		VectorDim:  16,
		EmbedModel: "mock-embed",
	}
	cfg.LLM.APIURL = llm.srv.URL
	cfg.LLM.APIKey = "mock-key"
	cfg.LLM.Model = "mock-model"
	cfg.Defaults = *sub.DefaultMemHopDefaults
	cfg.Defaults.DreamCompressMinTopics = 2
	db, err := memhop.OpenWithEncoder(cfg, &mockEncoder{dim: 16})
	if err != nil {
		t.Fatalf("OpenWithEncoder: %v", err)
	}
	defer db.Close()

	ts := time.Now().UnixMilli()
	res, err := db.Search(memhop.SearchQuery{Text: "用户要求重构代码", AutoCreate: true, Timestamp: ts})
	if err != nil {
		t.Fatalf("Search #1: %v", err)
	}
	sceneID := common.FormatHash(res.Contexts[0].SceneID)
	if _, err := db.Search(memhop.SearchQuery{
		Text: "继续重构第二个模块", DirectedL2ID: &sceneID, Timestamp: ts + 1000,
	}); err != nil {
		t.Fatalf("Search #2: %v", err)
	}

	ok, err := db.Dream(context.Background())
	if err != nil {
		t.Fatalf("Dream: %v", err)
	}
	if !ok {
		t.Fatal("Dream should report progress")
	}
	if llm.calls["consolidate"] < 1 {
		t.Fatal("Dream should call consolidate on the active scene")
	}
	// L1 nodes were synced from L2 during Dream, so the distill stage runs.
	if llm.calls["distill"] < 1 {
		t.Fatal("Dream should call distill for L0 profile")
	}
	// Distill output was merged into the L0 profile.
	profile, err := db.GetL0()
	if err != nil {
		t.Fatalf("GetL0 after dream: %v", err)
	}
	if profile.EmotionPatterns["valence"] == "" || profile.Personality == "" {
		t.Fatalf("dream distill should backfill emotion/mbti: %+v", profile)
	}
}

func TestInterfaceCheckpointPersist(t *testing.T) {
	llm := newMockLLM(t)
	path := filepath.Join(t.TempDir(), "persist.meh")
	cfg := &memhop.MemHopConfig{
		DBPath:     path,
		VectorDim:  16,
		EmbedModel: "mock-embed",
	}
	cfg.LLM.APIURL = llm.srv.URL
	cfg.LLM.APIKey = "mock-key"
	cfg.LLM.Model = "mock-model"
	cfg.Defaults = *sub.DefaultMemHopDefaults

	db, err := memhop.OpenWithEncoder(cfg, &mockEncoder{dim: 16})
	if err != nil {
		t.Fatalf("OpenWithEncoder: %v", err)
	}
	if _, err := db.Search(memhop.SearchQuery{
		Text: "用户要求重构代码", AutoCreate: true, Timestamp: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if err := db.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen the same file: scenes and archives must survive.
	db2, err := memhop.OpenWithEncoder(cfg, &mockEncoder{dim: 16})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db2.Close()
	scenes, err := db2.ListScenes()
	if err != nil {
		t.Fatalf("ListScenes after reopen: %v", err)
	}
	if len(scenes) == 0 {
		t.Fatal("scenes should persist across reopen")
	}
	arcs, err := db2.SearchL4(memhop.L4Query{Keyword: "重构"})
	if err != nil {
		t.Fatalf("SearchL4 after reopen: %v", err)
	}
	if len(arcs) == 0 {
		t.Fatal("archives should persist across reopen")
	}
}
