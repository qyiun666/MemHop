// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package query

import (
	"testing"

	"github.com/qyiun666/memhop/memhop/internal/core"
	"github.com/qyiun666/memhop/memhop/internal/core/index"
	"github.com/qyiun666/memhop/memhop/internal/hash"
)

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
		L1Reverse:   NewL1ReverseIndex(),
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

// TestSearchNilWeightsRRFScores covers the Weights==nil path: the RRF score
// is the final score and the 0.30 similarity threshold must not apply,
// otherwise callers that pass no weights always get empty results.
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
		L1Reverse:   NewL1ReverseIndex(),
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

// TestSearchDefaultWeightsNormalizedBM25 covers the Weights!=nil path:
// raw BM25 scores must be normalized to [0,1] by the result set's maximum
// before weighting, so the 0.30 threshold behaves per the documented formula.
func TestSearchDefaultWeightsNormalizedBM25(t *testing.T) {
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
		Weights: &core.SearchWeights{
			BM25Weight:   0.45,
			VectorWeight: 0.55,
			EntityWeight: 1.0,
			RRFK:         60,
		},
		L1Reverse: NewL1ReverseIndex(),
	}

	result, err := SearchContext(SearchQuery{Text: "rust programming"}, deps)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}

	// 5001 matches both terms → normalized BM25 = 1.0 → score = 0.45 ≥ 0.30.
	// 5002 matches only the low-IDF term "rust" (df=2 in a 2-doc corpus) →
	// normalized ≈ 0.21 → score ≈ 0.09 < 0.30 → filtered by the threshold.
	if len(result.Contexts) != 1 {
		t.Fatalf("expected exactly 1 context after threshold, got %d", len(result.Contexts))
	}
	top := result.Contexts[0]
	if top.ID != hash.FormatHash(5001) {
		t.Errorf("expected top doc 5001, got %s", top.ID)
	}
	// No vector/entity hits in this setup: score must equal BM25Weight * 1.0.
	if diff := float64(top.RetrievalScore) - 0.45; diff < -1e-5 || diff > 1e-5 {
		t.Errorf("expected normalized score 0.45, got %f", top.RetrievalScore)
	}
}
