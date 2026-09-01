// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package index

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"

	"github.com/qyiun666/MemHop/internal/repo/core"
)

func TestL2MetaIndex(t *testing.T) {
	t.Run("basic_crud", func(t *testing.T) {
		idx := NewL2MetaIndex()
		meta := &L2Meta{
			IDHash:        42,
			Depth:         1,
			SceneID:       100,
			ChildrenIDs:   []uint64{1, 2, 3},
			FusedKeywords: []string{"rust", "memory"},
			UserTimestamp: 2000,
		}
		idx.Update(meta)
		if idx.Len() != 1 {
			t.Errorf("expected len 1, got %d", idx.Len())
		}
		if got := idx.Get(42); got == nil || !slices.Equal(got.FusedKeywords, []string{"rust", "memory"}) {
			t.Errorf("Get(42) should return the cached keywords, got %+v", got)
		}
		if sceneIDs := idx.GetByScene(100); len(sceneIDs) != 1 || sceneIDs[0] != 42 {
			t.Errorf("GetByScene(100) should return [42], got %v", sceneIDs)
		}
		if removed := idx.Remove(42); removed == nil || removed.Depth != 1 {
			t.Error("Remove should return removed meta")
		}
		if idx.Len() != 0 {
			t.Error("should be empty after remove")
		}
	})

	// Search serves the scene read out of this cache, so an entry must rebuild
	// a topic slot identical to the stored record.
	t.Run("to_topic_slot_roundtrip", func(t *testing.T) {
		parent := uint64(7)
		want := core.TopicSlot{
			ID: 42, SceneID: 100, ParentID: &parent, Depth: 2,
			ChildrenIDs: []uint64{1, 2}, FusedKeywords: []string{"登录"},
			UserTimestamp: 1000, AgentTimestamp: 1001, L4Refs: []uint64{9},
		}
		got := L2MetaFromTopic(&want).ToTopicSlot()
		if got.ID != want.ID || *got.ParentID != parent || got.Depth != want.Depth ||
			!slices.Equal(got.FusedKeywords, want.FusedKeywords) ||
			!slices.Equal(got.ChildrenIDs, want.ChildrenIDs) ||
			!slices.Equal(got.L4Refs, want.L4Refs) ||
			got.UserTimestamp != want.UserTimestamp || got.AgentTimestamp != want.AgentTimestamp {
			t.Fatalf("cached slot differs from the record: %+v", got)
		}
	})
}

func TestBuildL2MetaFromEngine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "l2meta.meh")
	engine, err := core.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close(nil)

	topic := core.TopicSlot{
		ID: 101, SceneID: 1, Depth: 1,
		FusedKeywords: []string{"rust", "memory", "search"},
	}
	data, err := json.Marshal(topic)
	if err != nil {
		t.Fatalf("marshal topic: %v", err)
	}
	if _, err := engine.WriteRecord(core.DefaultAgentID, core.RecL2Topic, 101, data); err != nil {
		t.Fatalf("write topic: %v", err)
	}

	l2idx := BuildL2MetaFromEngine(engine, core.DefaultAgentID)
	if l2idx.Len() != 1 {
		t.Fatalf("expected 1 L2 entry, got %d", l2idx.Len())
	}
	meta := l2idx.Get(101)
	if meta == nil {
		t.Fatal("should find meta for id 101")
	}
	if meta.Depth != 1 {
		t.Errorf("expected depth 1, got %d", meta.Depth)
	}
	if !slices.Equal(meta.FusedKeywords, []string{"rust", "memory", "search"}) {
		t.Errorf("expected the keyword track, got %v", meta.FusedKeywords)
	}
}

// A pre-v1.5 record on disk carries two keyword tracks; the cache must expose
// them as the single track rather than an empty one.
func TestBuildL2MetaFromEngineFoldsLegacyRecord(t *testing.T) {
	dir := t.TempDir()
	engine, err := core.Create(filepath.Join(dir, "legacy.meh"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close(nil)

	raw := `{"id":201,"scene_id":1,"depth":1,"user_keywords":["a"],"agent_keywords":["b"]}`
	if _, err := engine.WriteRecord(core.DefaultAgentID, core.RecL2Topic, 201, []byte(raw)); err != nil {
		t.Fatalf("write legacy record: %v", err)
	}
	meta := BuildL2MetaFromEngine(engine, core.DefaultAgentID).Get(201)
	if meta == nil {
		t.Fatal("legacy record not indexed")
	}
	if !slices.Equal(meta.FusedKeywords, []string{"a", "b"}) {
		t.Fatalf("legacy tracks not folded: %v", meta.FusedKeywords)
	}
}
