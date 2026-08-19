// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package index

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/qyiun666/MemHop/internal/repo/core"
)

func writeRawTopic(t *testing.T, engine *core.StorageEngine, id uint64, sceneID uint64, depth uint8, kws []string) {
	t.Helper()
	topic := core.TopicSlot{ID: id, SceneID: sceneID, Depth: depth, UserKeywords: kws}
	data, err := json.Marshal(topic)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.WriteRecord(core.RecL2Topic, id, data); err != nil {
		t.Fatal(err)
	}
}

func TestRebuildSearchIndexes(t *testing.T) {
	engine, err := core.Create(filepath.Join(t.TempDir(), "rebuild.meh"), 768)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close(&core.IndexSnapshotData{})

	// depth 1/2 enter sparse; depth 3 does not; all three enter L2Meta.
	writeRawTopic(t, engine, 1, 100, 1, []string{"alpha", "memory"})
	writeRawTopic(t, engine, 2, 100, 2, []string{"beta", "rust"})
	writeRawTopic(t, engine, 3, 200, 3, []string{"gamma", "deep"})

	sparse, l1Rev, l2Meta, err := RebuildSearchIndexes(engine)
	if err != nil {
		t.Fatal(err)
	}

	// depth<=2 documents are BM25-searchable
	if len(sparse.Search([]string{"memory"}, 10)) != 1 {
		t.Error("depth-1 topic should be searchable")
	}
	if len(sparse.Search([]string{"rust"}, 10)) != 1 {
		t.Error("depth-2 topic should be searchable")
	}
	if len(sparse.Search([]string{"deep"}, 10)) != 0 {
		t.Error("depth-3 topic should NOT be in sparse index")
	}
	// L2Meta contains all three topics
	if l2Meta.Len() != 3 {
		t.Errorf("l2Meta should have 3 entries, got %d", l2Meta.Len())
	}
	// L1 reverse index is non-empty and usable
	if l1Rev == nil {
		t.Fatal("l1Reverse should not be nil")
	}
}
