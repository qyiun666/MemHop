// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Phase-3 API contract regression tests (offline, mock encoder):
//   - SearchQuery.Timestamp is required (Unix ms); zero/negative -> ErrInvalidQuery
//   - Partial Config.Defaults are backfilled at Open; search still ranks
//   - ImportMemory(Topic) is immediately searchable (l2Meta wired)
package test

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	memhop "github.com/qyiun666/MemHop/api"
	"github.com/qyiun666/MemHop/test/testsupport"
)

func TestSearchTimestampRequired(t *testing.T) {
	mh := testsupport.OpenMemHopMock(t)
	defer mh.Close()

	_, err := mh.Search(memhop.SearchQuery{Text: "hello world"})
	if err == nil {
		t.Fatal("Search with Timestamp=0 should fail")
	}
	if !errors.Is(err, memhop.ErrInvalidQuery) {
		t.Errorf("error should wrap ErrInvalidQuery, got %v", err)
	}

	_, err = mh.Search(memhop.SearchQuery{Text: "hello world", Timestamp: -5})
	if err == nil || !errors.Is(err, memhop.ErrInvalidQuery) {
		t.Errorf("negative Timestamp should wrap ErrInvalidQuery, got %v", err)
	}
}

func TestUpdateTimestampRequired(t *testing.T) {
	mh := testsupport.OpenMemHopMock(t)
	defer mh.Close()

	res, err := mh.Search(memhop.SearchQuery{
		Text: "timestamp contract topic", Timestamp: time.Now().UnixMilli(), AutoCreate: true,
	})
	if err != nil {
		t.Fatalf("Search AutoCreate: %v", err)
	}
	if len(res.Contexts) == 0 {
		t.Fatal("AutoCreate returned no contexts")
	}
	err = mh.Update(res.Contexts[0].ID, "agent reply", 0)
	if err == nil || !errors.Is(err, memhop.ErrInvalidQuery) {
		t.Errorf("Update with timestamp=0 should wrap ErrInvalidQuery, got %v", err)
	}
}

// TestPartialDefaultsSearchWorks opens a database whose Defaults only sets
// SearchWeights; DecayConfig/SessionConfig must be backfilled at Open and
// ranked search must run without error.
func TestPartialDefaultsSearchWorks(t *testing.T) {
	full := memhop.DefaultDefaults()
	cfg := memhop.Config{
		DBPath:     filepath.Join(t.TempDir(), "partial.meh"),
		VectorDim:  testsupport.MockVectorDim,
		EmbedModel: "mock-embed",
		Defaults: &memhop.ConfigDefaults{
			SearchWeights: full.SearchWeights, // only one sub-config provided
		},
	}
	cfg.LLM.APIURL = "http://127.0.0.1:1"
	cfg.LLM.APIKey = "sk-test"
	cfg.LLM.Model = "mock-model"
	cfg.LLM.TimeoutSecs = 1

	mh, err := memhop.OpenWithEncoder(&cfg, testsupport.NewMockEncoder(testsupport.MockVectorDim))
	if err != nil {
		t.Fatalf("Open with partial Defaults should succeed: %v", err)
	}
	defer mh.Close()

	now := time.Now().UnixMilli()
	seeds := []string{"apple pie baking recipe", "quantum computing hardware"}
	for _, s := range seeds {
		if _, err := mh.Search(memhop.SearchQuery{Text: s, Timestamp: now, AutoCreate: true}); err != nil {
			t.Fatalf("AutoCreate(%q): %v", s, err)
		}
	}
	res, err := mh.Search(memhop.SearchQuery{Text: "apple pie baking", Timestamp: now})
	if err != nil {
		t.Fatalf("ranked Search with partial Defaults: %v", err)
	}
	if len(res.Contexts) == 0 {
		t.Fatal("ranked Search returned no contexts")
	}
	// Ranking executed: scores must be in non-increasing order.
	for i := 1; i < len(res.Contexts); i++ {
		if res.Contexts[i].RetrievalScore > res.Contexts[i-1].RetrievalScore {
			t.Errorf("contexts not sorted by score: [%d]=%f > [%d]=%f",
				i, res.Contexts[i].RetrievalScore, i-1, res.Contexts[i-1].RetrievalScore)
		}
	}
}

// TestImportMemoryImmediatelySearchable verifies the l2Meta wiring: a topic
// imported via ImportMemory must be findable by Search without a restart.
func TestImportMemoryImmediatelySearchable(t *testing.T) {
	mh := testsupport.OpenMemHopMock(t)
	defer mh.Close()

	summary := "notes about telescope observation of galaxies"
	result, err := mh.ImportMemory(memhop.ImportRequest{
		TargetLayer: memhop.TargetTopic,
		Data: memhop.ImportData{
			Topics: []memhop.TopicImportItem{
				{Title: "galaxy telescope observation", Summary: &summary},
			},
		},
	})
	if err != nil {
		t.Fatalf("ImportMemory: %v", err)
	}
	if len(result.CreatedIDs) != 1 {
		t.Fatalf("expected 1 created topic, got %v", result.CreatedIDs)
	}
	importedID := result.CreatedIDs[0]

	res, err := mh.Search(memhop.SearchQuery{
		Text: "galaxy telescope observation", Timestamp: time.Now().UnixMilli(),
	})
	if err != nil {
		t.Fatalf("Search after ImportMemory: %v", err)
	}
	found := false
	for _, c := range append(res.Contexts, res.AssociatedContexts...) {
		if c.ID == importedID || c.SceneID == importedID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("imported topic %s not found in search results (contexts=%d assoc=%d)",
			importedID, len(res.Contexts), len(res.AssociatedContexts))
	}
}
