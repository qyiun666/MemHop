// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package search

import (
	"testing"

	"memhop/internal/common/config"
	"memhop/internal/core/index"
	"memhop/internal/core/model"
)

// ---------------------------------------------------------------------------
// filterByMinScore
// ---------------------------------------------------------------------------

func TestFilterByMinScore(t *testing.T) {
	candidates := []scoredContext{
		{score: 0.9},
		{score: 0.3},
		{score: 0.5},
		{score: 0.1},
	}

	t.Run("filters below threshold", func(t *testing.T) {
		got := filterByMinScore(candidates, 0.4)
		if len(got) != 2 {
			t.Fatalf("expected 2, got %d", len(got))
		}
		if got[0].score != 0.9 || got[1].score != 0.5 {
			t.Errorf("unexpected scores: %f, %f", got[0].score, got[1].score)
		}
	})

	t.Run("empty when all below", func(t *testing.T) {
		got := filterByMinScore(candidates, 1.0)
		if len(got) != 0 {
			t.Errorf("expected empty, got %d", len(got))
		}
	})

	t.Run("keeps all when threshold is zero", func(t *testing.T) {
		got := filterByMinScore(candidates, 0)
		if len(got) != 4 {
			t.Errorf("expected 4, got %d", len(got))
		}
	})

	t.Run("nil input returns empty slice", func(t *testing.T) {
		got := filterByMinScore(nil, 0.5)
		if got == nil || len(got) != 0 {
			t.Errorf("expected non-nil empty slice, got %v", got)
		}
	})
}

// ---------------------------------------------------------------------------
// filterByLayers
// ---------------------------------------------------------------------------

func TestFilterByLayers(t *testing.T) {
	candidates := []scoredContext{
		{topic: &model.TopicSlot{Depth: 1}},
		{topic: &model.TopicSlot{Depth: 2}},
		{topic: &model.TopicSlot{Depth: 3}},
		{topic: &model.TopicSlot{Depth: 1}},
	}

	t.Run("filters by specified layers", func(t *testing.T) {
		got := filterByLayers(candidates, []uint8{1})
		if len(got) != 2 {
			t.Fatalf("expected 2, got %d", len(got))
		}
	})

	t.Run("empty layers returns all", func(t *testing.T) {
		got := filterByLayers(candidates, nil)
		if len(got) != 4 {
			t.Errorf("expected 4, got %d", len(got))
		}
	})

	t.Run("no match returns empty slice", func(t *testing.T) {
		got := filterByLayers(candidates, []uint8{99})
		if got == nil || len(got) != 0 {
			t.Errorf("expected non-nil empty slice, got %v", got)
		}
	})
}

// ---------------------------------------------------------------------------
// buildDocScoreMap
// ---------------------------------------------------------------------------

func TestBuildDocScoreMap(t *testing.T) {
	t.Run("builds correct map", func(t *testing.T) {
		docs := []index.ScoredDoc{
			{IDHash: 100, Score: 0.8},
			{IDHash: 200, Score: 0.5},
			{IDHash: 300, Score: 0.3},
		}
		m := buildDocScoreMap(docs)
		if len(m) != 3 {
			t.Fatalf("expected 3 entries, got %d", len(m))
		}
		if m[100] != 0.8 || m[200] != 0.5 || m[300] != 0.3 {
			t.Errorf("unexpected scores in map")
		}
	})

	t.Run("empty input", func(t *testing.T) {
		m := buildDocScoreMap(nil)
		if len(m) != 0 {
			t.Errorf("expected empty map, got %d entries", len(m))
		}
	})
}

// ---------------------------------------------------------------------------
// collectL3Refs
// ---------------------------------------------------------------------------

func TestCollectL3Refs(t *testing.T) {
	t.Run("merges user and agent refs", func(t *testing.T) {
		topic := &model.TopicSlot{
			UserL3Refs:  []uint64{10, 20},
			AgentL3Refs: []uint64{30},
		}
		refs := collectL3Refs(topic)
		if len(refs) != 3 {
			t.Fatalf("expected 3 refs, got %d", len(refs))
		}
	})

	t.Run("empty when no refs", func(t *testing.T) {
		topic := &model.TopicSlot{}
		refs := collectL3Refs(topic)
		if len(refs) != 0 {
			t.Errorf("expected 0 refs, got %d", len(refs))
		}
	})
}

// ---------------------------------------------------------------------------
// collectAllL3IDs — deduplication
// ---------------------------------------------------------------------------

func TestCollectAllL3IDs(t *testing.T) {
	t.Run("deduplicates across contexts", func(t *testing.T) {
		scored := []scoredContext{
			{topic: &model.TopicSlot{UserL3Refs: []uint64{10, 20}}},
			{topic: &model.TopicSlot{UserL3Refs: []uint64{20, 30}, AgentL3Refs: []uint64{10}}},
		}
		ids := collectAllL3IDs(scored)
		if len(ids) != 3 {
			t.Fatalf("expected 3 unique IDs, got %d", len(ids))
		}
	})

	t.Run("empty returns non-nil empty slice", func(t *testing.T) {
		ids := collectAllL3IDs(nil)
		if ids == nil || len(ids) != 0 {
			t.Errorf("expected non-nil empty slice")
		}
	})
}

// ---------------------------------------------------------------------------
// emptyProfile / emptyResult
// ---------------------------------------------------------------------------

func TestEmptyProfile(t *testing.T) {
	p := emptyProfile()
	if p.Preferences == nil || p.Lexicon == nil || p.EmotionPatterns == nil {
		t.Error("expected non-nil maps")
	}
	if p.StyleTraits == nil {
		t.Error("expected non-nil StyleTraits slice")
	}
}

func TestEmptyResult(t *testing.T) {
	r := emptyResult()
	if r.Contexts == nil || r.AssociatedContexts == nil || r.Crystals == nil {
		t.Error("expected non-nil slices")
	}
	if len(r.Contexts) != 0 || len(r.AssociatedContexts) != 0 {
		t.Error("expected empty slices")
	}
}

// ---------------------------------------------------------------------------
// applyChannelWeights
// ---------------------------------------------------------------------------

func TestApplyChannelWeights(t *testing.T) {
	bm25 := []index.ScoredDoc{
		{IDHash: 1, Score: 10.0},
		{IDHash: 2, Score: 5.0},
	}
	vector := []index.ScoredDoc{
		{IDHash: 1, Score: 0.9},
	}
	entity := []index.ScoredDoc{
		{IDHash: 2, Score: 0.8},
	}
	merged := []index.ScoredDoc{
		{IDHash: 1, Score: 0},
		{IDHash: 2, Score: 0},
	}

	t.Run("nil weights returns merged unchanged", func(t *testing.T) {
		result := applyChannelWeights(merged, bm25, vector, entity, nil)
		if result[0].Score != 0 || result[1].Score != 0 {
			t.Error("expected unchanged scores with nil weights")
		}
	})

	t.Run("zero weights returns merged unchanged", func(t *testing.T) {
		w := &config.SearchWeights{}
		result := applyChannelWeights(merged, bm25, vector, entity, w)
		if result[0].Score != 0 || result[1].Score != 0 {
			t.Error("expected unchanged scores with zero weights")
		}
	})

	t.Run("BM25 normalized and weighted", func(t *testing.T) {
		mergedCopy := []index.ScoredDoc{{IDHash: 1}, {IDHash: 2}}
		w := &config.SearchWeights{BM25Weight: 1.0}
		result := applyChannelWeights(mergedCopy, bm25, nil, nil, w)
		// Doc 1: bm25=10/10=1.0 * 1.0 = 1.0
		// Doc 2: bm25=5/10=0.5 * 1.0 = 0.5
		if result[0].IDHash != 1 || result[0].Score < 0.99 {
			t.Errorf("expected doc 1 score ~1.0, got id=%d score=%f", result[0].IDHash, result[0].Score)
		}
		if result[1].IDHash != 2 || result[1].Score < 0.49 || result[1].Score > 0.51 {
			t.Errorf("expected doc 2 score ~0.5, got id=%d score=%f", result[1].IDHash, result[1].Score)
		}
	})

	t.Run("multi-channel combination", func(t *testing.T) {
		mergedCopy := []index.ScoredDoc{{IDHash: 1}, {IDHash: 2}}
		w := &config.SearchWeights{BM25Weight: 0.5, VectorWeight: 0.3, EntityWeight: 0.2}
		result := applyChannelWeights(mergedCopy, bm25, vector, entity, w)
		// Doc 1: 0.5*(10/10) + 0.3*0.9 + 0.2*0 = 0.5+0.27 = 0.77
		// Doc 2: 0.5*(5/10) + 0.3*0 + 0.2*0.8 = 0.25+0.16 = 0.41
		// Sorted: doc 1 first (0.77 > 0.41)
		if result[0].IDHash != 1 {
			t.Errorf("expected doc 1 first, got %d", result[0].IDHash)
		}
		const expected1 = 0.77
		if diff := float64(result[0].Score) - expected1; diff < -0.01 || diff > 0.01 {
			t.Errorf("doc 1: expected ~%f, got %f", expected1, result[0].Score)
		}
	})
}
