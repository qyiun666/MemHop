// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package repo

import (
	"path/filepath"
	"testing"

	"github.com/qyiun666/MemHop/internal/repo/core"
)

func TestCreateTopicL2WithIDSameTimestampDifferentText(t *testing.T) {
	engine, err := core.Create(filepath.Join(t.TempDir(), "topics.meh"), 16)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { engine.Close(&core.IndexSnapshotData{}) })

	sceneID := core.NewSceneSlot("scene").SceneID
	id1 := core.ComputeTopicIDForText(sceneID, 1000, "hello")
	id2 := core.ComputeTopicIDForText(sceneID, 1000, "world")
	if id1 == id2 {
		t.Fatal("different text must produce different topic IDs")
	}
	if !CreateTopicL2WithID(engine, sceneID, id1, []string{"hello"}, 1000, 0) {
		t.Fatal("create first topic")
	}
	if !CreateTopicL2WithID(engine, sceneID, id2, []string{"world"}, 1000, 0) {
		t.Fatal("create second topic")
	}
	if _, err := core.ReadTopicSlot(engine, id1); err != nil {
		t.Fatalf("read first topic: %v", err)
	}
	if _, err := core.ReadTopicSlot(engine, id2); err != nil {
		t.Fatalf("read second topic: %v", err)
	}
}
