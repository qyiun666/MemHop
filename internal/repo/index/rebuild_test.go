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
	topic := core.TopicSlot{ID: id, SceneID: sceneID, Depth: depth, FusedKeywords: kws}
	data, err := json.Marshal(topic)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.WriteRecord(core.DefaultAgentID, core.RecL2Topic, id, data); err != nil {
		t.Fatal(err)
	}
}

func TestBuildL2MetaIndexesEveryDepth(t *testing.T) {
	engine, err := core.Create(filepath.Join(t.TempDir(), "rebuild.meh"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()

	writeRawTopic(t, engine, 1, 100, 1, []string{"alpha", "memory"})
	writeRawTopic(t, engine, 2, 100, 2, []string{"beta", "rust"})
	writeRawTopic(t, engine, 3, 200, 3, []string{"gamma", "deep"})

	l2Meta := BuildL2MetaFromEngine(engine, core.DefaultAgentID)
	// The scene read is served by depth-1 entries, so every topic must be
	// cached regardless of depth.
	if got := l2Meta.TopicsByScene(100); len(got) != 2 {
		t.Errorf("scene 100 should hold 2 topics, got %d", len(got))
	}
	if got := l2Meta.TopicsByScene(200); len(got) != 1 {
		t.Errorf("scene 200 should hold its depth-3 topic, got %d", len(got))
	}
	if deep := l2Meta.Get(3); deep == nil || deep.Depth != 3 {
		t.Errorf("the deep topic is not cached as stored: %+v", deep)
	}
}
