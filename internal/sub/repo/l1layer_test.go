// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package repo

import (
	"testing"
	"time"

	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
)

// TestSyncL1NodesFromL2 covers node creation, idempotent no-op, topic-set
// update and per-scene isolation.
func TestSyncL1NodesFromL2(t *testing.T) {
	engine := tempEngine(t)
	sceneA := common.FormatHash(common.HashID("sceneA"))

	if !CreateTopicL2(engine, sceneA, []string{"k1"}, 1000, 0) {
		t.Fatal("create topic 1")
	}
	if !CreateTopicL2(engine, sceneA, []string{"k2"}, 2000, 0) {
		t.Fatal("create topic 2")
	}
	changed, err := SyncL1NodesFromL2(engine)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if changed != 1 {
		t.Fatalf("want 1 node created, got %d", changed)
	}
	node := readSceneNode(engine, common.HashID("l1:"+sceneA))
	if node == nil {
		t.Fatal("l1 node missing")
	}
	if node.SceneID != mustParse(t, sceneA) || len(node.TopicIDs) != 2 {
		t.Fatalf("node mismatch: %+v", node)
	}
	if node.Importance != 1.0 {
		t.Fatalf("fresh node importance should be 1.0: %+v", node)
	}
	firstUpdatedAt := node.UpdatedAt

	// Unchanged topic set must be a no-op so decay keeps accumulating.
	changed, err = SyncL1NodesFromL2(engine)
	if err != nil {
		t.Fatalf("sync #2: %v", err)
	}
	if changed != 0 {
		t.Fatalf("want 0 changes, got %d", changed)
	}
	if node := readSceneNode(engine, common.HashID("l1:"+sceneA)); node.UpdatedAt != firstUpdatedAt {
		t.Fatalf("no-op sync must not refresh UpdatedAt")
	}

	// A new topic in the scene updates the node in place.
	if !CreateTopicL2(engine, sceneA, []string{"k3"}, 3000, 0) {
		t.Fatal("create topic 3")
	}
	changed, err = SyncL1NodesFromL2(engine)
	if err != nil {
		t.Fatalf("sync #3: %v", err)
	}
	if changed != 1 {
		t.Fatalf("want 1 node updated, got %d", changed)
	}
	node = readSceneNode(engine, common.HashID("l1:"+sceneA))
	if len(node.TopicIDs) != 3 || node.Importance != 1.0 {
		t.Fatalf("node should keep importance and grow topic set: %+v", node)
	}

	// A second scene gets its own node.
	sceneB := common.FormatHash(common.HashID("sceneB"))
	if !CreateTopicL2(engine, sceneB, []string{"kb"}, 1000, 0) {
		t.Fatal("create topic in scene B")
	}
	changed, err = SyncL1NodesFromL2(engine)
	if err != nil {
		t.Fatalf("sync #4: %v", err)
	}
	if changed != 1 {
		t.Fatalf("want 1 node for scene B, got %d", changed)
	}
	nodes := core.CollectAllSceneNodes(engine)
	if len(nodes) != 2 {
		t.Fatalf("want 2 nodes total, got %d", len(nodes))
	}
}

// TestSyncL1NodesFromL2SkipsCompressed verifies depth>2 topics do not enter
// nodes (compression groups are covered by their depth<=2 parent).
func TestSyncL1NodesFromL2SkipsCompressed(t *testing.T) {
	engine := tempEngine(t)
	sceneA := common.FormatHash(common.HashID("sceneA"))
	CreateTopicL2(engine, sceneA, []string{"k1"}, 1000, 0)

	parentID := core.ComputeTopicID(common.HashID(sceneA), 1000, 2000)
	deep := core.TopicSlot{
		ID: core.ComputeTopicID(common.HashID(sceneA), 1000, 2000), SceneID: common.HashID(sceneA),
		ParentID: &parentID, Depth: 3, UserKeywords: []string{"deep"},
		UserTimestamp: 1000, AgentTimestamp: 2000,
	}
	if err := core.WriteTopicSlot(engine, deep.ID, &deep); err != nil {
		t.Fatalf("write deep topic: %v", err)
	}
	if _, err := SyncL1NodesFromL2(engine); err != nil {
		t.Fatalf("sync: %v", err)
	}
	node := readSceneNode(engine, common.HashID("l1:"+sceneA))
	if node == nil || len(node.TopicIDs) != 1 {
		t.Fatalf("depth-3 topic must be excluded from the node: %+v", node)
	}
	if node.UpdatedAt > time.Now().UnixMilli() {
		t.Fatal("updated_at in the future")
	}
}

func mustParse(t *testing.T, s string) uint64 {
	t.Helper()
	v, err := common.ParseID(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return v
}
