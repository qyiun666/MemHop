// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Regression tests for the Search write-path ordering: the L2MetaIndex
// cache must be refreshed before createTopicInScene lists the scene's
// topics, so the newly created topic is part of the returned Contexts.
package internal

import (
	"context"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

// TestSearchAutoCreateContextsIncludeNewTopic locks the timing: on an empty
// database the first Search(AutoCreate) must return the just-created topic
// in Contexts. A stale L2MetaIndex (sync after the listing) returns zero
// contexts and panics hosts indexing res.Contexts[0].
func TestSearchAutoCreateContextsIncludeNewTopic(t *testing.T) {
	srv := mockLLMServer(t, `{"keywords":["rust","memory"]}`)
	db := newSearchTestDB(t, srv.URL)

	res, err := db.Search(context.Background(), SearchQuery{Text: "hello world", AutoCreate: true, Timestamp: 1000})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.NewTopicID == 0 {
		t.Fatal("Search should have created a topic")
	}
	if len(res.Contexts) != 1 {
		t.Fatalf("Contexts = %d topics, want 1 (the new topic)", len(res.Contexts))
	}
	if res.Contexts[0].ID != res.NewTopicID {
		t.Errorf("Contexts[0].ID = %d, want new topic %d", res.Contexts[0].ID, res.NewTopicID)
	}
	if got := res.Contexts[0].UserKeywords; len(got) != 2 || got[0] != "rust" {
		t.Errorf("Contexts[0].UserKeywords = %v, want [rust memory]", got)
	}
}

// TestSearchDirectedContextsIncludeNewTopic covers the directed route: the
// topic created into an existing scene must show up in Contexts too.
func TestSearchDirectedContextsIncludeNewTopic(t *testing.T) {
	srv := mockLLMServer(t, `{"keywords":["rust","memory"]}`)
	db := newSearchTestDB(t, srv.URL)

	scene := core.NewSceneSlot("scene").SceneID
	sceneID := common.FormatHash(scene)
	res, err := db.Search(context.Background(), SearchQuery{Text: "hello world", DirectedL2ID: &sceneID, Timestamp: 1000})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Contexts) != 1 {
		t.Fatalf("Contexts = %d topics, want 1 (the new topic)", len(res.Contexts))
	}
	if res.Contexts[0].ID != res.NewTopicID {
		t.Errorf("Contexts[0].ID = %d, want new topic %d", res.Contexts[0].ID, res.NewTopicID)
	}
}

// newSearchTestDB assembles a DB with a mock LLM server, a working encoder
// and fresh in-memory indices, mirroring the Open assembly.
func newSearchTestDB(t *testing.T, llmURL string) *DB {
	t.Helper()
	cfg := &MemHopConfig{Defaults: *DefaultMemHopDefaults}
	db := &DB{
		engine:      newTestEngine(t),
		config:      cfg,
		llm:         New(&MemHopConfig{LLM: LlmConfig{APIURL: llmURL, APIKey: "test", Model: "mock"}}),
		encoder:     &mockEncoder{vec: testVec},
		sparseIndex: index.NewSparseIndex(),
	}
	db.l1Reverse.Store(index.NewL1ReverseIndex())
	db.l2Meta = index.BuildL2MetaFromEngine(db.engine)
	return db
}
