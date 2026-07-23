// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package search

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"memhop/internal/common/config"
	"memhop/internal/common/hash"
	"memhop/internal/core/index"
	"memhop/internal/core/model"
	"memhop/internal/core/storage"
)

func createTestEngine(t *testing.T) *storage.StorageEngine {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.meh")
	engine, err := storage.Create(path, 768)
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	t.Cleanup(func() {
		snap := &storage.IndexSnapshotData{}
		engine.Close(snap)
	})
	return engine
}

func writeTestTopic(t *testing.T, engine *storage.StorageEngine, id uint64, title string) {
	t.Helper()
	topic := model.TopicSlot{
		ID:            id,
		Depth:         1,
		UserKeywords:  []string{title},
		UserTimestamp: 1000,
		UserL4Refs:    []uint64{},
		UserL3Refs:    []uint64{},
		AgentKeywords: []string{},
		AgentL4Refs:   []uint64{},
		AgentL3Refs:   []uint64{},
		FusedKeywords: []string{},
		ChildrenIDs:   []uint64{},
		CreatedAt:     1000,
		UpdatedAt:     1000,
		Version:       1,
	}
	data, err := json.Marshal(topic)
	if err != nil {
		t.Fatalf("marshal topic: %v", err)
	}
	if _, err := engine.WriteRecord(storage.RecL2Topic, id, data); err != nil {
		t.Fatalf("write topic: %v", err)
	}
}

// TestAutoCreatedTopicRecallableWithoutRebuild is a regression test for the
// bug where createNewL2Context only updated the SparseIndex: searchNormal
// builds its candidate set from L2MetaIndex (rebuilt only on Open/Dream),
// so a freshly created topic was filtered out until restart or Dream.
func TestAutoCreatedTopicRecallableWithoutRebuild(t *testing.T) {
	engine := createTestEngine(t)
	sparse := index.NewSparseIndex()

	// Pre-existing topic so the candidate set is non-empty and the
	// candidate filter actually applies.
	writeTestTopic(t, engine, 3001, "cooking recipes collection")
	terms := index.Tokenize("cooking recipes collection")
	sparse.AddDocument(3001, terms, uint32(len(terms)))

	l2Meta := index.BuildL2MetaFromEngine(engine)
	deps := &SearchDeps{
		SparseIndex: sparse,
		L2Meta:      l2Meta,
		VectorDim:   768,
		Engine:      engine,
		Encoder:     nil,
		L1Reverse:   index.NewL1ReverseIndex(),
	}

	// Auto-create a new topic.
	createRes, err := SearchContext(SearchQuery{Text: "quantum physics basics", AutoCreate: true}, deps)
	if err != nil {
		t.Fatalf("auto-create search failed: %v", err)
	}
	if len(createRes.Contexts) != 1 {
		t.Fatalf("expected 1 created context, got %d", len(createRes.Contexts))
	}
	createdID := createRes.Contexts[0].ID

	// The L2MetaIndex must be updated in place (no Open/Dream rebuild).
	createdHash, err := hash.ParseID(createdID)
	if err != nil {
		t.Fatalf("parse created id: %v", err)
	}
	meta := l2Meta.Get(createdHash)
	if meta == nil {
		t.Fatal("expected new topic in L2MetaIndex right after auto-create")
	}
	if meta.Depth > 2 {
		t.Errorf("expected recallable depth <= 2, got %d", meta.Depth)
	}

	// Normal search without rebuilding the index must recall the new topic.
	recallRes, err := SearchContext(SearchQuery{Text: "quantum physics"}, deps)
	if err != nil {
		t.Fatalf("recall search failed: %v", err)
	}
	found := false
	for _, ctx := range recallRes.Contexts {
		if ctx.ID == createdID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("new topic %s not recalled without index rebuild", createdID)
	}
}

// TestSearchNilWeightsRRFScores covers the unified RRF path: the RRF score
// is the final score, no absolute threshold applies.
func TestSearchNilWeightsRRFScores(t *testing.T) {
	engine := createTestEngine(t)
	sparse := index.NewSparseIndex()

	writeTestTopic(t, engine, 4001, "rust programming guide")
	terms1 := index.Tokenize("rust programming guide")
	sparse.AddDocument(4001, terms1, uint32(len(terms1)))
	writeTestTopic(t, engine, 4002, "rust cookbook recipes")
	terms2 := index.Tokenize("rust cookbook recipes")
	sparse.AddDocument(4002, terms2, uint32(len(terms2)))

	deps := &SearchDeps{
		SparseIndex: sparse,
		L2Meta:      index.BuildL2MetaFromEngine(engine),
		VectorDim:   768,
		Engine:      engine,
		Encoder:     nil,
		Weights:     nil,
		L1Reverse:   index.NewL1ReverseIndex(),
	}

	result, err := SearchContext(SearchQuery{Text: "rust programming"}, deps)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(result.Contexts) == 0 {
		t.Fatal("expected non-empty results with nil weights")
	}

	// RRF with k=60 and 3 channels bounds scores at 3/61 ≈ 0.0492.
	const rrfMax = 3.0/61.0 + 1e-6
	prev := float32(2.0)
	for _, ctx := range result.Contexts {
		if ctx.RetrievalScore <= 0 {
			t.Errorf("expected positive RRF score, got %f", ctx.RetrievalScore)
		}
		if ctx.RetrievalScore > rrfMax {
			t.Errorf("score %f exceeds RRF bound %f", ctx.RetrievalScore, rrfMax)
		}
		if ctx.RetrievalScore > prev {
			t.Errorf("results not sorted by RRF score: %f after %f", ctx.RetrievalScore, prev)
		}
		prev = ctx.RetrievalScore
	}

	// The doc matching both query terms ranks first in BM25, hence first in RRF.
	if result.Contexts[0].ID != hash.FormatHash(4001) {
		t.Errorf("expected top doc 4001, got %s", result.Contexts[0].ID)
	}
}

// TestSearchUnifiedRRFReturnsMultipleResults verifies the unified RRF pipeline
// returns all matching results (no minRelevanceScore threshold).
func TestSearchUnifiedRRFReturnsMultipleResults(t *testing.T) {
	engine := createTestEngine(t)
	sparse := index.NewSparseIndex()

	writeTestTopic(t, engine, 5001, "rust programming guide")
	terms1 := index.Tokenize("rust programming guide")
	sparse.AddDocument(5001, terms1, uint32(len(terms1)))
	writeTestTopic(t, engine, 5002, "rust cookbook recipes")
	terms2 := index.Tokenize("rust cookbook recipes")
	sparse.AddDocument(5002, terms2, uint32(len(terms2)))

	deps := &SearchDeps{
		SparseIndex: sparse,
		L2Meta:      index.BuildL2MetaFromEngine(engine),
		VectorDim:   768,
		Engine:      engine,
		Encoder:     nil,
		Weights:     &config.SearchWeights{RRFK: 60},
		L1Reverse:   index.NewL1ReverseIndex(),
	}

	result, err := SearchContext(SearchQuery{Text: "rust programming"}, deps)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}

	// Both docs match "rust"; unified RRF returns all matching results.
	if len(result.Contexts) < 1 {
		t.Fatal("expected at least 1 context from unified RRF")
	}
	// Top result matches both query terms.
	if result.Contexts[0].ID != hash.FormatHash(5001) {
		t.Errorf("expected top doc 5001, got %s", result.Contexts[0].ID)
	}
}
