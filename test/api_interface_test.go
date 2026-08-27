// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Offline interface tests: exercise the public API surface through
// memhop.OpenMultiWithEncoder with a mock encoder and a mock OpenAI-compatible
// LLM server. No external services required; run with `go test ./test/...`.

package test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	memhop "github.com/qyiun666/MemHop/api"
	internal "github.com/qyiun666/MemHop/internal"
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// testDB is the offline test handle: an agent-domain session plus the
// file-level lifecycle methods of the underlying MultiAgentDB.
type testDB struct {
	*memhop.Session
	m *memhop.MultiAgentDB
}

func (h *testDB) Checkpoint() error { return h.m.Checkpoint() }
func (h *testDB) Close() error      { return h.m.Close() }
func (h *testDB) IsClosed() bool    { return h.m.IsClosed() }

// openMockMulti opens a multi-agent DB with the mock encoder + mock LLM at
// path; multi-agent is the only mode, so every handle goes through
// CreateAgent + Session. Opts tweak cfg.Defaults per scenario.
func openMockMulti(t *testing.T, path, llmURL string, opts ...func(*internal.MemHopDefaults)) *memhop.MultiAgentDB {
	t.Helper()
	cfg := &internal.MemHopConfig{
		DBPath:     path,
		VectorDim:  16,
		EmbedModel: "mock-embed",
	}
	cfg.LLM.APIURL = llmURL
	cfg.LLM.APIKey = "mock-key"
	cfg.LLM.Model = "mock-model"
	cfg.Defaults = *internal.DefaultMemHopDefaults
	for _, opt := range opts {
		opt(&cfg.Defaults)
	}
	m, err := memhop.OpenMultiWithEncoder(cfg, &mockEncoder{dim: 16})
	if err != nil {
		t.Fatalf("OpenMultiWithEncoder: %v", err)
	}
	return m
}

// newTestDB binds a session (tenant "test") to an opened multi-agent DB.
func newTestDB(t *testing.T, m *memhop.MultiAgentDB) *testDB {
	t.Helper()
	id, err := m.CreateAgent("test")
	if err != nil {
		m.Close()
		t.Fatalf("CreateAgent: %v", err)
	}
	sess, err := m.Session(id)
	if err != nil {
		m.Close()
		t.Fatalf("Session: %v", err)
	}
	return &testDB{Session: sess, m: m}
}

// openTestDB opens a DB backed by mock encoder + mock LLM in a temp dir.
func openTestDB(t *testing.T) (*testDB, *mockLLM) {
	t.Helper()
	llm := newMockLLM(t)
	m := openMockMulti(t, filepath.Join(t.TempDir(), "test.meh"), llm.srv.URL)
	h := newTestDB(t, m)
	t.Cleanup(func() { _ = h.Close() })
	return h, llm
}

// mockEncoder implements internal.Encoder with a deterministic pseudo-vector.
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
	res, err := db.Search(context.Background(), internal.SearchQuery{Text: "用户要求重构代码", AutoCreate: true, Timestamp: ts})
	if err != nil {
		t.Fatalf("Search(auto_create): %v", err)
	}
	if res.NewTopicID == 0 || len(res.Contexts) == 0 {
		t.Fatalf("auto-create should return new topic and contexts: %+v", res)
	}
	// auto-create links an L4 archive to the new topic; verify the link so
	// the host can expand the archive content from Contexts' L4Refs.
	linked := false
	for _, ctx := range res.Contexts {
		if len(ctx.L4Refs) > 0 {
			linked = true
			break
		}
	}
	if !linked {
		t.Fatal("auto-create Search should link L4 archives to topics")
	}

	// Update appends an agent reply to the topic.
	topicID := common.FormatHash(res.NewTopicID)
	if err := db.Update(topicID, "好的,我来重构这段代码", ts+1000); err != nil {
		t.Fatalf("Update should succeed on an existing topic: %v", err)
	}
	// Update on a missing topic must return an error.
	if err := db.Update(common.FormatHash(999), "无主话题", ts+2000); err == nil {
		t.Fatalf("Update on missing topic should fail")
	}

	// Normal Search retrieves the stored contexts.
	res2, err := db.Search(context.Background(), internal.SearchQuery{Text: "重构代码", Timestamp: ts + 3000})
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
	arcs, err := db.SearchL4(internal.L4Query{Keyword: "重构"})
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
	if _, err := db.Search(context.Background(), internal.SearchQuery{Text: "重构"}); err == nil {
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
