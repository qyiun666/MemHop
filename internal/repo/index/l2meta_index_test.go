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

// countEntries reads the cache the way its callers do — by iterating — so a test
// never needs an accessor the library itself does not use.
func countEntries(idx *L2MetaIndex) int {
	n := 0
	for range idx.Iter() {
		n++
	}
	return n
}

func TestL2MetaIndex(t *testing.T) {
	t.Run("basic_crud", func(t *testing.T) {
		idx := NewL2MetaIndex()
		meta := &L2Meta{
			IDHash:        42,
			Depth:         1,
			SceneID:       100,
			FusedKeywords: []string{"rust", "memory"},
			UserTimestamp: 2000,
		}
		idx.Update(meta)
		if countEntries(idx) != 1 {
			t.Errorf("expected len 1, got %d", countEntries(idx))
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
		if countEntries(idx) != 0 {
			t.Error("should be empty after remove")
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
	if countEntries(l2idx) != 1 {
		t.Fatalf("expected 1 L2 entry, got %d", countEntries(l2idx))
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
