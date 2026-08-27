// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package index

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/qyiun666/MemHop/internal/repo/core"
)

func TestL2MetaIndex(t *testing.T) {
	t.Run("basic_crud", func(t *testing.T) {
		idx := NewL2MetaIndex()
		meta := &L2Meta{
			IDHash:      42,
			Title:       "test topic",
			Depth:       1,
			SceneID:     100,
			ChildrenIDs: []uint64{1, 2, 3},
			Timestamp:   2000,
		}
		idx.Update(meta)
		if idx.Len() != 1 {
			t.Errorf("expected len 1, got %d", idx.Len())
		}

		got := idx.Get(42)
		if got == nil || got.Title != "test topic" {
			t.Errorf("Get(42) should return 'test topic'")
		}

		sceneIDs := idx.GetByScene(100)
		if len(sceneIDs) != 1 || sceneIDs[0] != 42 {
			t.Errorf("GetByScene(100) should return [42], got %v", sceneIDs)
		}

		removed := idx.Remove(42)
		if removed == nil || removed.Title != "test topic" {
			t.Error("Remove should return removed meta")
		}
		if idx.Len() != 0 {
			t.Error("should be empty after remove")
		}
	})
}

func TestBuildL2MetaFromEngine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "l2meta.meh")
	engine, err := core.Create(path, 768)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close(&core.IndexSnapshotData{})

	topic := core.TopicSlot{
		ID:           101,
		SceneID:      1,
		Depth:        1,
		UserKeywords: []string{"rust", "memory", "search"},
		L3Refs:       []uint64{501},
	}
	data, _ := json.Marshal(topic)
	engine.WriteRecord(core.DefaultAgentID, core.RecL2Topic, 101, data)

	l2idx := BuildL2MetaFromEngine(engine, core.DefaultAgentID)
	if l2idx.Len() != 1 {
		t.Errorf("expected 1 L2 entry, got %d", l2idx.Len())
	}
	meta := l2idx.Get(101)
	if meta == nil {
		t.Fatal("should find meta for id 101")
	}
	if meta.Depth != 1 {
		t.Errorf("expected depth 1, got %d", meta.Depth)
	}
	if len(meta.L3Refs) != 1 || meta.L3Refs[0] != 501 {
		t.Errorf("expected L3Refs [501], got %v", meta.L3Refs)
	}
}
