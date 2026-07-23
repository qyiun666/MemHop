// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package search

import (
	"testing"

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
