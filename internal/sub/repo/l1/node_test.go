// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package l1

import (
	"path/filepath"
	"testing"

	"github.com/qyiun666/MemHop/internal/common/hash"
	"github.com/qyiun666/MemHop/internal/repo/index"
	"github.com/qyiun666/MemHop/internal/repo/core/storage"
)

func newTestEngine(t *testing.T) *storage.StorageEngine {
	t.Helper()
	engine, err := storage.Create(filepath.Join(t.TempDir(), "l1.meh"), 768)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close(&storage.IndexSnapshotData{}) })
	return engine
}

func TestCreateNodeAndFindAssociated(t *testing.T) {
	engine := newTestEngine(t)
	l1Idx := index.NewL1ReverseIndex()
	sceneA := hash.FormatHash(hash.HashID("scene-a"))
	sceneB := hash.FormatHash(hash.HashID("scene-b"))

	nodeID, err := CreateNode(engine, l1Idx, sceneA, []uint64{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateNode(engine, l1Idx, sceneB, []uint64{3}); err != nil {
		t.Fatal(err)
	}

	// 关联查询：选中 sceneA 应命中节点 A（含 topic 1,2），不含节点 B
	nodes := FindAssociatedNodes(engine, l1Idx, []string{sceneA})
	if len(nodes) != 1 {
		t.Fatalf("expected 1 associated node, got %d", len(nodes))
	}
	if len(nodes[0].TopicIDs) != 2 || nodes[0].TopicIDs[0] != 1 {
		t.Errorf("topic ids mismatch: %v", nodes[0].TopicIDs)
	}
	// 节点记录已落盘
	if !engine.Contains(nodeID) {
		t.Error("node record missing on disk")
	}
}

func TestUpdateNodeSyncsIndex(t *testing.T) {
	engine := newTestEngine(t)
	l1Idx := index.NewL1ReverseIndex()
	sceneA := hash.FormatHash(hash.HashID("scene-a"))
	sceneB := hash.FormatHash(hash.HashID("scene-b"))

	nodeID, err := CreateNode(engine, l1Idx, sceneA, []uint64{1})
	if err != nil {
		t.Fatal(err)
	}
	nodeIDStr := hash.FormatHash(nodeID)

	// 换场景更新：索引应旧场景移除、新场景注册
	node := ListNodes(engine, &sceneA)[0]
	node.SceneID = hash.HashID("scene-b")
	if err := UpdateNode(engine, l1Idx, nodeIDStr, &node); err != nil {
		t.Fatal(err)
	}
	if got := FindAssociatedNodes(engine, l1Idx, []string{sceneA}); len(got) != 0 {
		t.Errorf("old scene still associated: %+v", got)
	}
	if got := FindAssociatedNodes(engine, l1Idx, []string{sceneB}); len(got) != 1 {
		t.Errorf("new scene not associated: %+v", got)
	}
	// 磁盘记录已更新
	if got := ListNodes(engine, &sceneB); len(got) != 1 || got[0].IDHash != nodeID {
		t.Errorf("node not updated on disk: %+v", got)
	}
}

func TestListNodesFilter(t *testing.T) {
	engine := newTestEngine(t)
	l1Idx := index.NewL1ReverseIndex()
	sceneA := hash.FormatHash(hash.HashID("scene-a"))
	sceneB := hash.FormatHash(hash.HashID("scene-b"))
	if _, err := CreateNode(engine, l1Idx, sceneA, []uint64{1}); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateNode(engine, l1Idx, sceneB, []uint64{2}); err != nil {
		t.Fatal(err)
	}
	if got := ListNodes(engine, &sceneA); len(got) != 1 {
		t.Errorf("filtered list: %d nodes", len(got))
	}
	if got := ListNodes(engine, nil); len(got) != 2 {
		t.Errorf("full list: %d nodes", len(got))
	}
}

func TestRebuildL1Index(t *testing.T) {
	engine := newTestEngine(t)
	l1Idx := index.NewL1ReverseIndex()
	sceneA := hash.FormatHash(hash.HashID("scene-a"))
	if _, err := CreateNode(engine, l1Idx, sceneA, []uint64{1}); err != nil {
		t.Fatal(err)
	}
	// 全新索引重建（模拟 open）：从磁盘恢复
	rebuilt := RebuildL1Index(engine)
	if got := FindAssociatedNodes(engine, rebuilt, []string{sceneA}); len(got) != 1 {
		t.Errorf("rebuild lost associations: %+v", got)
	}
}
