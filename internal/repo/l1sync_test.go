// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L1 scene-node sync (data layer) tests.

package repo

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

func tempEngine(t *testing.T) *core.StorageEngine {
	t.Helper()
	eng, err := core.Create(filepath.Join(t.TempDir(), "test.meh"))
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	t.Cleanup(func() { eng.Close(&core.IndexSnapshotData{}) })
	return eng
}

// mustCreateTurn writes one depth-1 turn topic under sceneID and returns its
// id; the agent timestamp is derived so each call stays collision-free.
func mustCreateTurn(t *testing.T, engine *core.StorageEngine, sceneID uint64, kws []string, userTS int64) uint64 {
	t.Helper()
	id := core.ComputeTurnTopicID(sceneID, userTS, userTS+1)
	if !CreateTurnTopicL2(engine, core.DefaultAgentID, sceneID, id, kws, userTS, userTS+1) {
		t.Fatalf("create topic %v", kws)
	}
	return id
}

// TestSyncL1NodesFromL2 covers node creation, idempotent no-op, topic-set
// update and per-scene isolation.
func TestSyncL1NodesFromL2(t *testing.T) {
	engine := tempEngine(t)
	sceneA := common.HashID("sceneA")

	mustCreateTurn(t, engine, sceneA, []string{"k1"}, 1000)
	mustCreateTurn(t, engine, sceneA, []string{"k2"}, 2000)
	changed, err := SyncL1NodesFromL2(engine, core.DefaultAgentID)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if changed != 1 {
		t.Fatalf("want 1 node created, got %d", changed)
	}
	node, err := core.ReadSceneNode(engine, core.DefaultAgentID, common.HashID("l1:"+common.FormatHash(sceneA)))
	if err != nil {
		t.Fatal("l1 node missing")
	}
	if node.SceneID != sceneA || len(node.TopicIDs) != 2 {
		t.Fatalf("node mismatch: %+v", node)
	}
	if node.Importance != 1.0 {
		t.Fatalf("fresh node importance should be 1.0: %+v", node)
	}
	firstUpdatedAt := node.UpdatedAt

	// Unchanged topic set must be a no-op so decay keeps accumulating.
	changed, err = SyncL1NodesFromL2(engine, core.DefaultAgentID)
	if err != nil {
		t.Fatalf("sync #2: %v", err)
	}
	if changed != 0 {
		t.Fatalf("want 0 changes, got %d", changed)
	}
	if node, err := core.ReadSceneNode(engine, core.DefaultAgentID, common.HashID("l1:"+common.FormatHash(sceneA))); err == nil && node.UpdatedAt != firstUpdatedAt {
		t.Fatalf("no-op sync must not refresh UpdatedAt")
	}

	// A new topic in the scene updates the node in place.
	mustCreateTurn(t, engine, sceneA, []string{"k3"}, 3000)
	changed, err = SyncL1NodesFromL2(engine, core.DefaultAgentID)
	if err != nil {
		t.Fatalf("sync #3: %v", err)
	}
	if changed != 1 {
		t.Fatalf("want 1 node updated, got %d", changed)
	}
	node, err = core.ReadSceneNode(engine, core.DefaultAgentID, common.HashID("l1:"+common.FormatHash(sceneA)))
	if err != nil {
		t.Fatalf("read node after update: %v", err)
	}
	if len(node.TopicIDs) != 3 || node.Importance != 1.0 {
		t.Fatalf("node should keep importance and grow topic set: %+v", node)
	}

	// A second scene gets its own node.
	sceneB := common.HashID("sceneB")
	mustCreateTurn(t, engine, sceneB, []string{"kb"}, 1000)
	changed, err = SyncL1NodesFromL2(engine, core.DefaultAgentID)
	if err != nil {
		t.Fatalf("sync #4: %v", err)
	}
	if changed != 1 {
		t.Fatalf("want 1 node for scene B, got %d", changed)
	}
	nodes := core.CollectAllSceneNodes(engine, core.DefaultAgentID)
	if len(nodes) != 2 {
		t.Fatalf("want 2 nodes total, got %d", len(nodes))
	}
}

// TestSyncL1NodesFromL2SkipsCompressed verifies depth>2 topics do not enter
// nodes (compression groups are covered by their depth<=2 parent).
func TestSyncL1NodesFromL2SkipsCompressed(t *testing.T) {
	engine := tempEngine(t)
	sceneA := common.HashID("sceneA")
	mustCreateTurn(t, engine, sceneA, []string{"k1"}, 1000)

	parentID := core.ComputeTopicID(sceneA, 1000, 2000)
	deep := core.TopicSlot{
		ID: core.ComputeTopicID(sceneA, 1000, 2000), SceneID: sceneA,
		ParentID: &parentID, Depth: 3, FusedKeywords: []string{"deep"},
		UserTimestamp: 1000, AgentTimestamp: 2000,
	}
	if err := core.WriteTopicSlot(engine, core.DefaultAgentID, deep.ID, &deep); err != nil {
		t.Fatalf("write deep topic: %v", err)
	}
	if _, err := SyncL1NodesFromL2(engine, core.DefaultAgentID); err != nil {
		t.Fatalf("sync: %v", err)
	}
	node, err := core.ReadSceneNode(engine, core.DefaultAgentID, common.HashID("l1:"+common.FormatHash(sceneA)))
	if err != nil || len(node.TopicIDs) != 1 {
		t.Fatalf("depth-3 topic must be excluded from the node: %+v", node)
	}
	if node.UpdatedAt > time.Now().UnixMilli() {
		t.Fatal("updated_at in the future")
	}
}

// TestBuildL1Hyperedges covers edge creation from keyword-overlap Jaccard,
// threshold filtering, idempotent refresh and weight strengthening (max wins).

func mustParse(t *testing.T, s string) uint64 {
	t.Helper()
	v, err := common.ParseID(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return v
}
