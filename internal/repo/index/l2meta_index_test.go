// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package index

import (
	"encoding/json"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/qyiun666/MemHop/internal/repo/core"
)

func TestL2MetaIndex(t *testing.T) {
	t.Run("basic_crud", func(t *testing.T) {
		idx := newL2MetaIndex()
		meta := &L2Meta{
			IDHash:        42,
			Depth:         1,
			SceneID:       100,
			FusedKeywords: []string{"rust", "memory"},
			UserTimestamp: 2000,
		}
		idx.Update(meta)
		if got := idx.TopicsByScene(100); len(got) != 1 || got[0].IDHash != 42 {
			t.Errorf("TopicsByScene(100) should list [42], got %+v", got)
		}
		if got := idx.Get(42); got == nil || !slices.Equal(got.FusedKeywords, []string{"rust", "memory"}) {
			t.Errorf("Get(42) should return the cached keywords, got %+v", got)
		}
		idx.Remove(42)
		if idx.Get(42) != nil {
			t.Error("the row is still addressable by id after Remove")
		}
		if got := idx.TopicsByScene(100); len(got) != 0 {
			t.Errorf("the scene still lists %d topics after Remove", len(got))
		}
	})

	// Search serves the scene read out of this cache, so an entry must rebuild
	// a topic slot identical to the stored record.
	t.Run("to_topic_slot_roundtrip", func(t *testing.T) {
		parent := uint64(7)
		want := core.TopicSlot{
			ID: 42, SceneID: 100, ParentID: &parent, Depth: 2,
			Name:          "决定把 L5 让给计划树的那一轮",
			FusedKeywords: []string{"登录"},
			UserTimestamp: 1000, AgentTimestamp: 1001,
		}
		got := L2MetaFromTopic(&want).ToTopicSlot()
		if got.ID != want.ID || *got.ParentID != parent || got.Depth != want.Depth ||
			got.Name != want.Name ||
			!slices.Equal(got.FusedKeywords, want.FusedKeywords) ||
			got.UserTimestamp != want.UserTimestamp || got.AgentTimestamp != want.AgentTimestamp {
			t.Fatalf("cached slot differs from the record: %+v", got)
		}
	})

	// The whole point of the cache is rebuilding a topic slot without reading
	// the record, so the two field sets must not drift apart. Listing them in
	// an assertion cannot catch it — both sides would simply omit the field —
	// so compare the structures themselves.
	t.Run("cache_covers_every_topic_field", func(t *testing.T) {
		record := fieldNames(reflect.TypeFor[core.TopicSlot]())
		cached := fieldNames(reflect.TypeFor[L2Meta]())
		delete(record, "ID")
		delete(cached, "IDHash") // L2Meta.IDHash <-> TopicSlot.ID
		if !maps.Equal(record, cached) {
			t.Fatalf("L2Meta and core.TopicSlot fields drifted:\n record=%v\n cache =%v",
				slices.Sorted(maps.Keys(record)), slices.Sorted(maps.Keys(cached)))
		}
	})
}

// A merge moves a whole scene at once. The row carries the scene it belongs to, so
// a move that updated only the scene list would leave the row pointing at the scene
// it came from — and the next removal would then look for it in the wrong list.
func TestRetargetSceneMovesTheWholeScene(t *testing.T) {
	idx := newL2MetaIndex()
	idx.Update(&L2Meta{IDHash: 11, SceneID: 1, Depth: 1})
	idx.Update(&L2Meta{IDHash: 12, SceneID: 1, Depth: 2})
	idx.Update(&L2Meta{IDHash: 21, SceneID: 2, Depth: 1})

	idx.RetargetScene(1, 2)

	if got := idx.TopicsByScene(1); len(got) != 0 {
		t.Fatalf("the merged-away scene still lists %d topics", len(got))
	}
	moved := idx.TopicsByScene(2)
	if len(moved) != 3 {
		t.Fatalf("the primary scene lists %d topics, want 3", len(moved))
	}
	for _, m := range moved {
		if m.SceneID != 2 {
			t.Fatalf("row %d is listed by scene 2 but still says %d", m.IDHash, m.SceneID)
		}
	}
	idx.Remove(11)
	if idx.Get(11) != nil {
		t.Fatal("row 11 is still addressable")
	}
	if got := idx.TopicsByScene(2); len(got) != 2 {
		t.Fatalf("after Remove the primary lists %d topics, want 2", len(got))
	}
}

// fieldNames maps each exported field to its type, so the cache structure can
// be compared against the record it stands in for.
func fieldNames(typ reflect.Type) map[string]string {
	out := make(map[string]string, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if !f.IsExported() {
			continue
		}
		out[f.Name] = f.Type.String()
	}
	return out
}

func TestBuildL2MetaFromEngine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "l2meta.meh")
	engine, err := core.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()

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
	if got := l2idx.TopicsByScene(1); len(got) != 1 {
		t.Fatalf("expected 1 L2 entry under scene 1, got %d", len(got))
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
