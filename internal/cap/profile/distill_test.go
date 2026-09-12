// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package profile

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/qyiun666/MemHop/internal/repo/core"
)

func tempEngine(t *testing.T) *core.StorageEngine {
	t.Helper()
	engine, err := core.Create(filepath.Join(t.TempDir(), "profile.meh"))
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	return engine
}

// writeNode stores one L1 scene node carrying count topics, each with its own
// keyword track.
func writeNode(t *testing.T, engine *core.StorageEngine, id uint64, importance float32, updatedAt int64, count int) {
	t.Helper()
	node := core.SceneNode{
		IDHash: id, SceneID: id, Importance: importance,
		CreatedAt: updatedAt, UpdatedAt: updatedAt,
	}
	for i := 0; i < count; i++ {
		topicID := id*1000 + uint64(i)
		topic := core.TopicSlot{
			ID: topicID, SceneID: id, Depth: 1,
			FusedKeywords: []string{"kw-a", "kw-b", "kw-c", "kw-d", "kw-e"},
		}
		if err := core.WriteTopicSlot(engine, core.DefaultAgentID, topicID, &topic); err != nil {
			t.Fatalf("write topic: %v", err)
		}
		node.TopicIDs = append(node.TopicIDs, topicID)
	}
	if err := core.WriteSceneNode(engine, core.DefaultAgentID, id, &node); err != nil {
		t.Fatalf("write node: %v", err)
	}
}

// The samples a distillation is asked to read are the best-ranked nodes, not the
// first ones the scan happened to reach: rank is importance discounted by age, and
// a cut that dropped either half would send the model a set of scenes chosen by
// accident. Keywords are capped per sample because each one is a record read.
func TestSamplesRanksBeforeCappingAndBoundsEachRow(t *testing.T) {
	engine := tempEngine(t)
	now := time.Now().UnixMilli()
	const (
		staleStrong = uint64(101) // high importance, a year of age
		freshMid    = uint64(102) // middling importance, just touched
		weakBase    = uint64(500)
		weakCount   = 200
	)
	yearMs := int64(365*24*60*60) * 1000
	writeNode(t, engine, staleStrong, 0.9, now-yearMs, 5)
	writeNode(t, engine, freshMid, 0.5, now, 5)
	for i := 0; i < weakCount; i++ {
		writeNode(t, engine, weakBase+uint64(i), 0.1, now, 1)
	}

	got := Samples(engine, core.DefaultAgentID)
	if len(got) != maxDistillSamples {
		t.Fatalf("got %d samples, want the cap of %d out of %d nodes",
			len(got), maxDistillSamples, weakCount+2)
	}
	if got[0].IDHash != freshMid {
		t.Fatalf("first sample is node %x with importance %v updated %d, want the fresh one: rank discounts age",
			got[0].IDHash, got[0].Importance, got[0].UpdatedAt)
	}
	for _, s := range got {
		if s.IDHash == staleStrong {
			t.Fatal("the weakest rank must be the one the cap drops, not the one with the highest importance")
		}
	}
	if len(got[0].Keywords) != maxDistillKeywordsPerSample {
		t.Fatalf("sample carries %d keywords, want the per-sample cap of %d",
			len(got[0].Keywords), maxDistillKeywordsPerSample)
	}
}
